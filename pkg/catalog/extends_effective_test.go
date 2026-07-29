// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
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

func marshalSchema(t *testing.T, n yaml.Node) string {
	t.Helper()
	b, err := yaml.Marshal(&n)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	return string(b)
}

// TestResolveAllExtends_ComponentInheritsParentSchemaVerbatim is the guard on
// the one property every Component-schema reader depends on: a Component's
// authored schema and the schema enforced against its instances are the same
// object, on an extending Component as much as a standalone one.
//
// Three things are pinned together because they are one property seen from
// three sides. The resolved child's Spec.Schema is byte-identical to the chain
// root's — so the reader that takes the authored node is reading the inherited
// contract. It carries no provenance keys — so the copy is a node assignment
// and not a one-element run through the union primitive, which tags every node
// it emits. And no second, composed view of a Component's schema exists to
// disagree with the first: nothing populates one for a Component, on either the
// eager or the closure path.
//
// Re-permitting pillar authoring on an extending Component, or reintroducing a
// composed Component view, breaks this test. Both are how a Component came to
// have two schemas that agreed on every input anyone happened to try.
func TestResolveAllExtends_ComponentInheritsParentSchemaVerbatim(t *testing.T) {
	rootSchema := "values:\n  tls_enabled: { type: boolean }\n  port: { type: integer, minimum: 1024 }\n"
	parent := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/parent", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Schema: schemaNode(t, rootSchema)},
	}
	// An extending Component authors no pillar content: its Spec.Schema is the
	// inheritance marker, which the merge replaces with the root's block.
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/child", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends: "app/parent",
			Schema:  schemaNode(t, validator.InheritedSchemaMarker),
		},
	}
	grandchild := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/grandchild", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends: "app/child",
			Schema:  schemaNode(t, validator.InheritedSchemaMarker),
		},
	}
	standalone := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/solo", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Schema: schemaNode(t, "values:\n  name: { type: string }\n")},
	}
	cat := &Catalog{Components: map[string]types.Component{
		"app/parent":     parent,
		"app/child":      child,
		"app/grandchild": grandchild,
		"app/solo":       standalone,
	}, Features: map[string]types.Feature{}}

	if errs := cat.ResolveAllExtends(); len(errs) > 0 {
		t.Fatalf("ResolveAllExtends: %v", errs)
	}

	lookup := cat.CatalogLookup()
	want := marshalSchema(t, parent.Spec.Schema)

	// Every level of the chain carries the root's schema, byte for byte — a
	// multi-level chain propagates the copy rather than composing at each hop.
	for _, uri := range []string{"app/child", "app/grandchild"} {
		got, ok := lookup.Component(uri)
		if !ok {
			t.Fatalf("Component %q not found after resolution", uri)
		}
		if have := marshalSchema(t, got.Spec.Schema); have != want {
			t.Errorf("Component %q schema is not the chain root's, verbatim:\n got: %s\nwant: %s", uri, have, want)
		}
		if have := marshalSchema(t, got.Spec.Schema); strings.Contains(have, "__source_origin") {
			t.Errorf("Component %q schema carries provenance keys, so it went through the union primitive rather than a node copy:\n%s", uri, have)
		}
		// No composed second view: the accessor holds Features only.
		if _, ok := lookup.EffectiveSchema(uri); ok {
			t.Errorf("Component %q has a composed schema view beside its authored one; the two can then disagree", uri)
		}
	}

	// A standalone Component is untouched and likewise has no second view.
	solo, ok := lookup.Component("app/solo")
	if !ok {
		t.Fatal("Component \"app/solo\" not found")
	}
	if have, orig := marshalSchema(t, solo.Spec.Schema), marshalSchema(t, standalone.Spec.Schema); have != orig {
		t.Errorf("standalone Component schema changed:\n got: %s\nwant: %s", have, orig)
	}
	if _, ok := lookup.EffectiveSchema("app/solo"); ok {
		t.Error("standalone Component must have no composed schema view")
	}
}
