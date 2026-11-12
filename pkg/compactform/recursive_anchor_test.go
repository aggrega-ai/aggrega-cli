// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	"go.yaml.in/yaml/v3"
)

// These tests prove the compiler REFUSES a self-referential anchor instead of
// following it. The refusal is what keeps them survivable: compileNode follows
// alias links without tracking where it has been, so if a guard is removed the
// failure here is a stack overflow that takes the whole test binary down rather
// than a reported failure. That is the intended shape — a loud death is a
// correct signal — so do not "repair" a crash here by deleting the fixture. The
// rule itself is proven safely, without recursion, in pkg/anchorcycle.

func TestCompileRefusesRecursiveAnchorInInput(t *testing.T) {
	t.Parallel()
	// The document parses cleanly; only a walk over it diverges.
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("values: &loop\n  nested: *loop\n"), &node); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}

	_, err := Compile(&node)
	if err == nil {
		t.Fatal("Compile accepted a document whose anchor is aliased inside itself")
	}
	if !errors.Is(err, ErrCircularRef()) {
		t.Errorf("error does not match ErrCircularRef; got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "&loop") {
		t.Errorf("diagnostic does not name the anchor an author has to break; got: %v", err)
	}
}

// TestCompileRefusesRecursiveAnchorInsideEscapeBody covers the entry the
// input-level check structurally cannot see: a `$jsonschema` block scalar is a
// STRING when Compile inspects its input, and its anchors only become anchors
// when that string is parsed. So a document can be entirely free of recursion
// at the outer level and still carry it one parse down.
func TestCompileRefusesRecursiveAnchorInsideEscapeBody(t *testing.T) {
	t.Parallel()
	var node yaml.Node
	src := "$jsonschema: |\n  properties: &loop\n    inner: *loop\n"
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}
	// Guard the premise: the OUTER document must itself be free of recursion,
	// or this test would be re-proving the input-level check.
	if _, found := anchorcycle.Find(&node); found {
		t.Fatal("fixture's outer document is itself recursive; it no longer covers the escape-body entry")
	}

	_, err := Compile(&node)
	if err == nil {
		t.Fatal("Compile accepted an escape body whose anchor is aliased inside itself")
	}
	if !errors.Is(err, ErrCircularRef()) {
		t.Errorf("error does not match ErrCircularRef; got %T: %v", err, err)
	}
	var ce *CompileError
	if errors.As(err, &ce) && ce.Keyword != "$jsonschema" {
		t.Errorf("diagnostic does not attribute the failure to the escape keyword; keyword = %q", ce.Keyword)
	}
}

// TestCompileAcceptsDeepAndAliasReusingSchemas is the other half: the refusal
// must be of recursion, never of depth or of heavy alias reuse.
func TestCompileAcceptsDeepAndAliasReusingSchemas(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, src string }{
		{
			name: "one anchor reused from several places",
			src:  "values:\n  base: &b\n    type: string\n  a: *b\n  b: *b\n  c: *b\n",
		},
		{
			name: "nested well beyond any hand-written schema",
			src:  deepSchemaSrc(300),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var node yaml.Node
			if err := yaml.Unmarshal([]byte(tc.src), &node); err != nil {
				t.Fatalf("fixture did not parse: %v", err)
			}
			if _, err := Compile(&node); errors.Is(err, ErrCircularRef()) {
				t.Errorf("a document with no recursion was refused as circular: %v", err)
			}
		})
	}
}

func deepSchemaSrc(depth int) string {
	var b strings.Builder
	for i := range depth {
		b.WriteString(strings.Repeat("  ", i))
		b.WriteString("properties:\n")
		b.WriteString(strings.Repeat("  ", i+1))
		b.WriteString("child:\n")
	}
	b.WriteString(strings.Repeat("  ", depth*2))
	b.WriteString("type: string\n")
	return b.String()
}
