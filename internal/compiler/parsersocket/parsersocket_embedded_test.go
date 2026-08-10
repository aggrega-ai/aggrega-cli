// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package parsersocket_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// embeddedParserCatalog builds a catalog carrying the four starter parser Plugin
// entities with the same field values the generated hub entities declare — a
// pluginType: parser Plugin per format advertising its parse:<format> capability.
// Its ParserPluginForFormat resolves a requested format to the entity, exactly as
// the shipped catalog does. The bytes come from the binary's own embed tier, so
// the test exercises the real embedded guests rather than a fixture.
func embeddedParserCatalog() *catalog.Catalog {
	plugins := map[string]types.Plugin{}
	for _, format := range []string{"hcl", "toml", "yaml", "json"} {
		key := "parser/" + format + ":0.0.0-test"
		plugins[key] = types.Plugin{
			APIVersion: "aggrega.ai/v1alpha1",
			Kind:       "Plugin",
			Metadata:   types.PluginMetadata{Name: format, Version: "0.0.0-test", PluginType: "parser"},
			Spec: types.PluginSpec{
				AbiVersion: "v1alpha1",
				Parser:     &types.PluginParser{Capabilities: []string{"parse:" + format}},
				Exports:    []types.PluginExport{{Name: "parse"}},
				Runtime:    &types.PluginRuntime{MaxMemoryPages: 4096},
			},
		}
	}
	return &catalog.Catalog{Plugins: plugins}
}

// embeddedParserLoader resolves a parser Plugin entity to its bytes in the binary's
// embed tier, keyed on the entity's bare-identity artifact reference — the same
// byte source a core parser supplies in production.
func embeddedParserLoader() parsersocket.ParserLoader {
	return func(p types.Plugin) (wasm.PluginSpec, error) {
		b, ok := embedded.Lookup(p.ArtifactReference())
		if !ok {
			return wasm.PluginSpec{}, fmt.Errorf("embed tier has no bytes for %q", p.ArtifactReference())
		}
		return wasm.PluginSpec{Name: p.Coordinate(), Bytes: b}, nil
	}
}

// TestEmbeddedParsers_ResolveAndRun proves the whole host orchestration against the
// real embedded starter parsers: for each format the catalog resolver matches
// parse:<format> to the embedded Plugin entity, the host loads the guest bytes from
// the embed tier, runs the parse export, and decodes the canonical tree. The
// representative sources all normalize to the same sorted-key tree, so the
// assertion is the format-agnostic contract every starter parser meets.
func TestEmbeddedParsers_ResolveAndRun(t *testing.T) {
	cat := embeddedParserCatalog()
	load := embeddedParserLoader()

	cases := []struct {
		format string
		src    string
		want   string
	}{
		{"json", `{"b": 2, "a": 1}`, `{"a":1,"b":2}`},
		{"yaml", "b: 2\na: 1\n", `{"a":1,"b":2}`},
		{"toml", "b = 2\na = 1\n", `{"a":1,"b":2}`},
		{"hcl", "b = 2\na = 1\n", `{"a":1,"b":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			out, err := parsersocket.ParseArtifact(context.Background(), cat, load, tc.format, []byte(tc.src), nil)
			if err != nil {
				t.Fatalf("ParseArtifact(%q): %v", tc.format, err)
			}
			if len(out.Diagnostics) != 0 {
				t.Fatalf("%s: unexpected diagnostics: %+v", tc.format, out.Diagnostics)
			}
			if got := string(out.Tree); got != tc.want {
				t.Fatalf("%s tree = %s; want %s", tc.format, got, tc.want)
			}
		})
	}
}

// TestEmbeddedParsers_Deterministic pins byte-identity end-to-end through the
// embedded HCL guest: the same source and format yield a byte-identical tree
// across two independent hermetic invocations.
func TestEmbeddedParsers_Deterministic(t *testing.T) {
	cat := embeddedParserCatalog()
	load := embeddedParserLoader()
	src := []byte("region = \"eu-west-1\"\nresource \"aws_s3_bucket\" \"logs\" {\n  bucket = \"acme-logs\"\n}\n")

	first, err := parsersocket.ParseArtifact(context.Background(), cat, load, "hcl", src, nil)
	if err != nil {
		t.Fatalf("first ParseArtifact: %v", err)
	}
	second, err := parsersocket.ParseArtifact(context.Background(), cat, load, "hcl", src, nil)
	if err != nil {
		t.Fatalf("second ParseArtifact: %v", err)
	}
	if !bytes.Equal(first.Tree, second.Tree) {
		t.Fatalf("non-deterministic embedded hcl tree:\n first: %s\nsecond: %s", first.Tree, second.Tree)
	}
}

// TestEmbeddedParsers_MalformedYieldsDiagnostic proves a malformed source drives
// the embedded guest to report a diagnostic rather than crash: the run completes
// and the output carries an error-severity diagnostic.
func TestEmbeddedParsers_MalformedYieldsDiagnostic(t *testing.T) {
	cat := embeddedParserCatalog()
	load := embeddedParserLoader()

	out, err := parsersocket.ParseArtifact(context.Background(), cat, load, "json", []byte(`{"broken": }`), nil)
	if err != nil {
		t.Fatalf("ParseArtifact on malformed json errored at the host boundary: %v", err)
	}
	hasError := false
	for _, d := range out.Diagnostics {
		if d.Severity == "error" {
			hasError = true
		}
	}
	if !hasError {
		t.Fatalf("malformed json produced no error diagnostic: tree=%s diags=%+v", out.Tree, out.Diagnostics)
	}
}
