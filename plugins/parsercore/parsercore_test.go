// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package parsercore_test

import (
	"bytes"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
)

// parseFunc is the shared shape of every starter conversion, so one table can
// drive the round-trip, determinism, and malformed-input assertions across all
// four formats.
type parseFunc func([]byte) abi.ParseOutput

// TestParsers_RoundTripCanonicalTree pins the canonical tree each starter parser
// produces for a representative input. The expected tree is the byte-exact JSON a
// consumer receives, so the assertions double as the format's tree contract.
func TestParsers_RoundTripCanonicalTree(t *testing.T) {
	cases := []struct {
		name  string
		parse parseFunc
		src   string
		tree  string
	}{
		{
			name:  "json",
			parse: parsercore.ParseJSON,
			src:   `{"replicas": 3, "image": "nginx", "ports": [80, 443], "enabled": true}`,
			tree:  `{"enabled":true,"image":"nginx","ports":[80,443],"replicas":3}`,
		},
		{
			name:  "yaml",
			parse: parsercore.ParseYAML,
			src:   "replicas: 3\nimage: nginx\nports:\n  - 80\n  - 443\nenabled: true\n",
			tree:  `{"enabled":true,"image":"nginx","ports":[80,443],"replicas":3}`,
		},
		{
			name:  "toml",
			parse: parsercore.ParseTOML,
			src:   "replicas = 3\nimage = \"nginx\"\nports = [80, 443]\nenabled = true\n",
			tree:  `{"enabled":true,"image":"nginx","ports":[80,443],"replicas":3}`,
		},
		{
			name:  "hcl",
			parse: parsercore.ParseHCL,
			src:   "replicas = 3\nimage = \"nginx\"\nports = [80, 443]\nenabled = true\n",
			tree:  `{"enabled":true,"image":"nginx","ports":[80,443],"replicas":3}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.parse([]byte(tc.src))
			if len(out.Diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
			}
			if got := string(out.Tree); got != tc.tree {
				t.Fatalf("tree mismatch\n got: %s\nwant: %s", got, tc.tree)
			}
		})
	}
}

// TestHCLParser_BlocksAndPointers proves the HCL-specific structure: blocks nest
// under type then labels and collect into arrays, and a non-literal attribute
// (a resource reference) is preserved as its raw source text rather than dropped.
func TestHCLParser_BlocksAndPointers(t *testing.T) {
	src := `resource "aws_db_instance" "main" {
  engine = "postgres"
  size   = 20
}

resource "aws_db_instance" "replica" {
  replicate_source_db = aws_db_instance.main.id
}
`
	out := parsercore.ParseHCL([]byte(src))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	want := `{"resource":{"aws_db_instance":{"main":[{"engine":"postgres","size":20}],"replica":[{"replicate_source_db":"aws_db_instance.main.id"}]}}}`
	if got := string(out.Tree); got != want {
		t.Fatalf("tree mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestParsers_MalformedYieldsDiagnostic proves a malformed source reports an error
// diagnostic rather than crashing — the contract that lets a consumer surface a
// parse failure as a finding. The formats that cannot partially recover a value
// (json, yaml, toml) return a null tree; HCL recovers whatever parsed and returns
// that partial tree alongside the diagnostics, so it is exempt from the null-tree
// assertion.
func TestParsers_MalformedYieldsDiagnostic(t *testing.T) {
	cases := []struct {
		name         string
		parse        parseFunc
		src          string
		wantNullTree bool
	}{
		{"json", parsercore.ParseJSON, `{"broken": }`, true},
		{"yaml", parsercore.ParseYAML, "key: [unterminated\n", true},
		{"toml", parsercore.ParseTOML, "key = = 1\n", true},
		{"hcl", parsercore.ParseHCL, "resource \"x\" {\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.parse([]byte(tc.src))
			if len(out.Diagnostics) == 0 {
				t.Fatalf("malformed %s produced no diagnostic; tree=%s", tc.name, out.Tree)
			}
			hasError := false
			for _, d := range out.Diagnostics {
				if d.Severity == "halt" {
					hasError = true
				}
			}
			if !hasError {
				t.Fatalf("malformed %s produced no halt-severity diagnostic: %+v", tc.name, out.Diagnostics)
			}
			if tc.wantNullTree && string(out.Tree) != "null" {
				t.Fatalf("malformed %s tree = %s; want null", tc.name, out.Tree)
			}
		})
	}
}

// TestParsers_Deterministic pins the byte-identity invariant: parsing the same
// source twice yields a byte-identical tree, for every starter format.
func TestParsers_Deterministic(t *testing.T) {
	cases := []struct {
		name  string
		parse parseFunc
		src   string
	}{
		{"json", parsercore.ParseJSON, `{"b": 2, "a": 1, "c": {"z": 3, "y": 4}}`},
		{"yaml", parsercore.ParseYAML, "b: 2\na: 1\nc:\n  z: 3\n  y: 4\n"},
		{"toml", parsercore.ParseTOML, "b = 2\na = 1\n[c]\nz = 3\ny = 4\n"},
		{"hcl", parsercore.ParseHCL, "b = 2\na = 1\nc = { z = 3, y = 4 }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := tc.parse([]byte(tc.src))
			second := tc.parse([]byte(tc.src))
			if !bytes.Equal(first.Tree, second.Tree) {
				t.Fatalf("non-deterministic %s tree:\n first: %s\nsecond: %s", tc.name, first.Tree, second.Tree)
			}
		})
	}
}

// TestParsers_IntegerPrecision proves large integers keep their exact digits
// rather than degrading through float64, so a tree round-trips a value a consumer
// can trust.
func TestParsers_IntegerPrecision(t *testing.T) {
	const big = "9007199254740993" // 2^53 + 1, unrepresentable exactly as float64
	out := parsercore.ParseJSON([]byte(`{"n": ` + big + `}`))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	want := `{"n":` + big + `}`
	if got := string(out.Tree); got != want {
		t.Fatalf("integer precision lost\n got: %s\nwant: %s", got, want)
	}
}
