// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"testing"

	"go.yaml.in/yaml/v3"
)

func schemaNode(t *testing.T, y string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(y), &n); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	return n
}

// chainEntry compiles a compact schema into a CatalogEntry's JSONSchema, the
// merge input the catalog populates in production.
func chainEntry(t *testing.T, uri, extends, schema string) CatalogEntry {
	t.Helper()
	node := schemaNode(t, schema)
	return CatalogEntry{URI: uri, Extends: extends, JSONSchema: CompileSchemaForMerge(node, nil)}
}

// TestComposeExtends_MergesPillars: the composed effective schema carries BOTH
// the parent's and the child's values-pillar keys — the foundation of inherited-
// constraint enforcement.
func TestComposeExtends_MergesPillars(t *testing.T) {
	t.Parallel()
	child := chainEntry(t, "app/child:1.0.0", "app/parent:1.0.0",
		"values:\n  port: { type: integer, minimum: 1 }\n")
	parent := chainEntry(t, "app/parent:1.0.0", "",
		"values:\n  tls_enabled: { type: boolean, required: true }\n")
	lookup := func(uri string) (CatalogEntry, bool) {
		if uri == "app/parent:1.0.0" {
			return parent, true
		}
		return CatalogEntry{}, false
	}

	comp := CompositionFromResult("app/child:1.0.0", ResolveFeatureSchemaChain(child, lookup))
	if comp == nil || comp.EffectiveSchema == nil {
		t.Fatal("expected a composed effective schema")
	}
	props, _ := comp.EffectiveSchema["properties"].(map[string]any)
	values, _ := props["values"].(map[string]any)
	vprops, _ := values["properties"].(map[string]any)
	if _, ok := vprops["tls_enabled"]; !ok {
		t.Errorf("composed schema missing inherited parent key tls_enabled; got %v", vprops)
	}
	if _, ok := vprops["port"]; !ok {
		t.Errorf("composed schema missing child key port; got %v", vprops)
	}
}

// TestComposeExtends_NoSchemaIsNoComposition: a chain whose entries declare no
// schema composes nothing (CompileSchemaForMerge → nil; merge stays dormant).
func TestComposeExtends_NoSchemaIsNoComposition(t *testing.T) {
	t.Parallel()
	child := CatalogEntry{URI: "c", Extends: "p"}
	parent := CatalogEntry{URI: "p"}
	lookup := func(uri string) (CatalogEntry, bool) {
		if uri == "p" {
			return parent, true
		}
		return CatalogEntry{}, false
	}
	if comp := CompositionFromResult("c", ResolveFeatureSchemaChain(child, lookup)); comp != nil {
		t.Fatalf("no-schema chain must compose nothing; got %+v", comp)
	}
}

// TestComposeExtends_NarrowingIsSignalOnly: a child that tightens an inherited
// constraint produces a narrowing notice but NO error — the merge does not halt
// (narrowing-on-extend enforcement is the opt-in publishing linter's job).
func TestComposeExtends_NarrowingIsSignalOnly(t *testing.T) {
	t.Parallel()
	// Child widens the inherited lower bound (1 < 5) — a violation of the
	// narrow-on-extend rule, recorded as a signal-only notice, never a halt.
	child := chainEntry(t, "c", "p",
		"values:\n  port: { type: integer, minimum: 1 }\n")
	parent := chainEntry(t, "p", "",
		"values:\n  port: { type: integer, minimum: 5 }\n")
	lookup := func(uri string) (CatalogEntry, bool) {
		if uri == "p" {
			return parent, true
		}
		return CatalogEntry{}, false
	}
	result := ResolveFeatureSchemaChain(child, lookup)
	if len(result.Errors) != 0 {
		t.Fatalf("narrowing must not halt; got errors %v", result.Errors)
	}
	comp := CompositionFromResult("c", result)
	if comp == nil || len(comp.Narrowings) == 0 {
		t.Fatalf("expected a signal-only narrowing notice; got %+v", comp)
	}
}

// TestComposeExtends_MissingParentHalts: a child extending an unresolvable parent
// halts the chain resolution with ErrInheritanceMerge — the reachable structural
// halt. (A content-level merge failure cannot occur: the merger is total over
// non-nil schemas, erroring only on empty/nil input, which the engine filters.)
func TestComposeExtends_MissingParentHalts(t *testing.T) {
	t.Parallel()
	child := chainEntry(t, "c", "does/not:exist",
		"values:\n  port: { type: integer }\n")
	lookup := func(string) (CatalogEntry, bool) { return CatalogEntry{}, false }
	result := ResolveFeatureSchemaChain(child, lookup)
	if len(result.Errors) == 0 {
		t.Fatal("expected a structural halt for the missing parent")
	}
	for _, e := range result.Errors {
		if !errors.Is(e, ErrInheritanceMerge()) {
			t.Errorf("missing-parent halt must wrap ErrInheritanceMerge; got %v", e)
		}
	}
}
