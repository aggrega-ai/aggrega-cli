// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

// TestResolveAllExtends_PopulatesEffectiveSchema: an extends-ing Component gets a
// composed effective schema carrying both inherited and own keys, exposed via
// CatalogLookup.EffectiveSchema; a non-extends Component gets none (consumers
// fall back to the authored schema, so the non-extends path is unchanged).
func TestResolveAllExtends_PopulatesEffectiveSchema(t *testing.T) {
	parent := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/parent", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Schema: schemaNode(t, "values:\n  tls_enabled: { type: boolean, required: true }\n"),
		},
	}
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/child", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends: "app/parent",
			Schema:  schemaNode(t, "values:\n  port: { type: integer, minimum: 1 }\n"),
		},
	}
	standalone := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/solo", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Schema: schemaNode(t, "values:\n  name: { type: string }\n"),
		},
	}
	cat := &Catalog{Components: map[string]types.Component{
		"app/parent": parent,
		"app/child":  child,
		"app/solo":   standalone,
	}, Features: map[string]types.Feature{}}

	if errs := cat.ResolveAllExtends(); len(errs) > 0 {
		t.Fatalf("ResolveAllExtends: %v", errs)
	}

	lookup := cat.CatalogLookup()
	eff, ok := lookup.EffectiveSchema("app/child")
	if !ok {
		t.Fatal("extends-ing child must have an effective schema")
	}
	props, _ := eff["properties"].(map[string]any)
	values, _ := props["values"].(map[string]any)
	vprops, _ := values["properties"].(map[string]any)
	if _, ok := vprops["tls_enabled"]; !ok {
		t.Errorf("effective schema missing inherited tls_enabled; got %v", vprops)
	}
	if _, ok := vprops["port"]; !ok {
		t.Errorf("effective schema missing own key port; got %v", vprops)
	}

	if _, ok := lookup.EffectiveSchema("app/solo"); ok {
		t.Error("non-extends Component must NOT have an effective schema (falls back to authored)")
	}
}
