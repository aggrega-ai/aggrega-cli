// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Parent contributes values.replicas (maximum 3, default 1) and a provides key;
// the child extends it and authors no pillar content, so replicas, its
// constraint, its default, and the provides key are all INHERITED verbatim.
const extendsParentCompYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/test/parent-comp
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      replicas: { type: integer, maximum: 3, default: 1 }
    provides:
      endpoint: { type: string }
`

const extendsChildCompYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/test/child-comp
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/test/parent-comp
  schema: inherited
`

const extendsParentFeatYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/test/parent-feat
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  schema:
    values:
      threshold: { type: integer, maximum: 10 }
`

const extendsChildFeatYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/test/child-feat
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  extends: core/test/parent-feat
  schema:
    values:
      label: { type: string }
`

func pillarProps(t *testing.T, eff compactform.Schema, pillar string) map[string]any {
	t.Helper()
	props, _ := eff["properties"].(map[string]any)
	p, _ := props[pillar].(map[string]any)
	pp, _ := p["properties"].(map[string]any)
	return pp
}

// TestLazyLookup_ExtendsComposition_BuildPathServesInheritedSchema is the
// end-to-end proof that the build path validates an extending entity against the
// schema it inherits, exercised through the real build primitives — and that the
// two kinds reach that outcome by different routes.
//
// A Component inherits its base's pillar block verbatim, so there is nothing to
// compose and no second view to serve: the accessor holds no Component entry,
// and CompileComponentSchema (the guard's entry) reads the authored node, which
// after resolution IS the base's. A value valid under the inherited constraint
// conforms; an over-limit one is rejected for the RIGHT reason rather than as an
// unknown property.
//
// A Feature still composes its chain's pillars, so the accessor serves an
// extending Feature's composed schema — the one kind the composition tier still
// has a reason to exist for.
func TestLazyLookup_ExtendsComposition_BuildPathServesInheritedSchema(t *testing.T) {
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "parent-comp.yaml", extendsParentCompYAML)
	writeCatalogFile(t, cd, "child-comp.yaml", extendsChildCompYAML)
	writeCatalogFile(t, cd, "parent-feat.yaml", extendsParentFeatYAML)
	writeCatalogFile(t, cd, "child-feat.yaml", extendsChildFeatYAML)

	lazy := NewLazyLookup(ws, ".aggrega/cache", []string{cd}, nil)
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "core/test/child-comp:1.0.0"},
		{URI: "core/test/child-feat:1.0.0"},
	}}
	if errs := lazy.ResolveClosureExtends(cl); len(errs) > 0 {
		t.Fatalf("ResolveClosureExtends: %v", errs)
	}
	lookup := lazy.AsCatalogLookup()

	// COMPONENT: no second, composed view exists for a Component — the authored
	// node is the whole answer.
	if _, ok := lookup.EffectiveSchema("core/test/child-comp:1.0.0"); ok {
		t.Error("an extending Component must have no composed schema view beside its authored one")
	}

	// The schema the build's real entry compiles carries the inherited
	// values.replicas (with its base default) and the inherited provides.endpoint
	// key, because the merge copied the base's block onto the child.
	compiled, ok := resolver.CompileComponentSchema(lookup, "core/test/child-comp:1.0.0")
	if !ok {
		t.Fatal("CompileComponentSchema returned no schema")
	}
	vprops := pillarProps(t, compiled, "values")
	if _, ok := vprops["replicas"]; !ok {
		t.Errorf("compiled values pillar missing INHERITED replicas; got keys %v", propKeys(vprops))
	}
	if pprops := pillarProps(t, compiled, "provides"); pprops["endpoint"] == nil {
		t.Errorf("compiled provides pillar missing INHERITED endpoint key; got keys %v", propKeys(pprops))
	}
	if rep, _ := vprops["replicas"].(map[string]any); rep["default"] == nil {
		t.Errorf("inherited replicas lost its base default; got %v", rep)
	}

	// Value conformance through the same entry. replicas=2 is valid under the
	// inherited maximum 3 (a child-only schema would reject it as an unknown
	// property); replicas=99 is rejected — and NOT for the unknown-property
	// reason.
	conform := func(replicas int) []*resolver.ResolverError {
		inst := types.OrderedMap[any]{}
		inst.Set("replicas", replicas)
		return resolver.ValidatePillarConformance(compiled, "values", inst, nil, "child", "", "schema.values")
	}
	if errs := conform(2); len(errs) != 0 {
		t.Errorf("replicas=2 is valid under the inherited maximum 3; must conform, got %v", errs)
	}
	errs99 := conform(99)
	if len(errs99) == 0 {
		t.Fatal("replicas=99 exceeds the inherited maximum 3; must be rejected")
	}
	var joined strings.Builder
	for _, e := range errs99 {
		joined.WriteString(e.Error())
	}
	if strings.Contains(joined.String(), "inverse schema") {
		t.Errorf("replicas=99 rejected for the WRONG reason (unknown-property closed-schema, not exceeds-maximum): %s", joined.String())
	}

	// FEATURE: the accessor resolves feature keys — an extending Feature's chain
	// still composes, and its composed schema is what the accessor serves.
	feff, ok := lookup.EffectiveSchema("core/test/child-feat:1.0.0")
	if !ok {
		t.Fatal("no composed effective schema for the extends-ing feature (accessor must resolve feature keys)")
	}
	if fv := pillarProps(t, feff, "values"); fv["threshold"] == nil || fv["label"] == nil {
		t.Errorf("composed feature values missing inherited threshold or child label; got keys %v", propKeys(fv))
	}
}

func propKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
