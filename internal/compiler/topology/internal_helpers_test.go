// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestFindComponent_TopLevel pins the top-level branch of the
// component lookup. A component declared directly under
// spec.components resolves without descending into subsystems.
func TestFindComponent_TopLevel(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server"})
	got := findComponent(agg, "api")
	if got == nil {
		t.Fatal("expected non-nil ComponentInstance; got nil")
	}
	if got.Type != "core/server" {
		t.Errorf("expected core/server; got %q", got.Type)
	}
}

// TestFindComponent_SubsystemNested pins the subsystem-descent branch.
// A component declared inside a subsystem resolves via the recursive
// findComponentInSubsystem walker.
func TestFindComponent_SubsystemNested(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	innerComps := types.OrderedMap[types.ComponentInstance]{}
	innerComps.Set("db", types.ComponentInstance{Type: "core/database/postgres"})
	inner := types.Subsystem{Components: innerComps}
	agg.Spec.Subsystems.Set("data", inner)
	got := findComponent(agg, "db")
	if got == nil {
		t.Fatal("expected non-nil ComponentInstance from subsystem")
	}
	if got.Type != "core/database/postgres" {
		t.Errorf("expected postgres; got %q", got.Type)
	}
}

// TestFindComponent_DeepNested pins the recursive descent into nested
// subsystems. A component declared under a sub-subsystem resolves
// through two levels of recursion.
func TestFindComponent_DeepNested(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	leafComps := types.OrderedMap[types.ComponentInstance]{}
	leafComps.Set("deep", types.ComponentInstance{Type: "core/server"})
	leaf := types.Subsystem{Components: leafComps}
	midSubs := types.OrderedMap[types.Subsystem]{}
	midSubs.Set("leaf-sub", leaf)
	mid := types.Subsystem{Subsystems: midSubs}
	agg.Spec.Subsystems.Set("outer", mid)
	got := findComponent(agg, "deep")
	if got == nil {
		t.Fatal("expected non-nil from deep-nested subsystem")
	}
}

// TestFindComponent_NotFoundReturnsNil pins the not-found path:
// a name absent from top-level components AND every subsystem
// returns nil rather than panic. The Build pipeline's edge phase
// relies on this contract.
func TestFindComponent_NotFoundReturnsNil(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	if got := findComponent(agg, "missing"); got != nil {
		t.Errorf("expected nil; got %+v", got)
	}
}

// TestCopyVisited_DeepCopy pins the deep-copy contract of the
// cycle-detection map duplicator. The result must be writable
// without affecting the source.
func TestCopyVisited_DeepCopy(t *testing.T) {
	t.Parallel()
	src := map[string]bool{"a": true, "b": false}
	dst := copyVisited(src)
	if len(dst) != 2 {
		t.Errorf("copy length: expected 2; got %d", len(dst))
	}
	dst["c"] = true
	if _, has := src["c"]; has {
		t.Errorf("source mutated by destination write: %v", src)
	}
	dst["a"] = false
	if !src["a"] {
		t.Errorf("source key 'a' affected: %v", src)
	}
}

// TestCopyVisited_EmptyMap pins the empty-source branch: an empty map
// produces a non-nil empty map that callers can write into.
func TestCopyVisited_EmptyMap(t *testing.T) {
	t.Parallel()
	src := map[string]bool{}
	dst := copyVisited(src)
	if dst == nil {
		t.Fatal("expected non-nil empty map; got nil")
	}
	if len(dst) != 0 {
		t.Errorf("expected empty; got %d entries", len(dst))
	}
	dst["new"] = true
	if _, has := src["new"]; has {
		t.Errorf("source mutated: %v", src)
	}
}

// TestQualifiedID_PrefixOrName pins the qualified-id formatter. An
// empty prefix returns the bare name; a non-empty prefix prepends
// "<prefix>.".
func TestQualifiedID_PrefixOrName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prefix, name, want string
	}{
		{"", "api", "api"},
		{"data", "primary", "data.primary"},
		{"outer.middle", "leaf", "outer.middle.leaf"},
	}
	for _, c := range cases {
		if got := qualifiedID(c.prefix, c.name); got != c.want {
			t.Errorf("qualifiedID(%q, %q) = %q; want %q", c.prefix, c.name, got, c.want)
		}
	}
}

// TestBuild_PopulatesLabelsFromCatalog pins the catalog-label-injection
// arm of Build's step 2: nodes whose Component type resolves through
// catalogLookup receive the catalog Component's metadata.Labels copied
// onto the TopologyNode.
func TestBuild_PopulatesLabelsFromCatalog(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server"})
	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "server")
	cat := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server" {
				return types.Component{Metadata: types.ComponentMetadata{Labels: labels}}, true
			}
			return types.Component{}, false
		},
	}
	rr := &resolver.ResolveResult{Aggregate: agg}
	result, err := Build(BuildConfig{
		ResolveResult: rr,
		CatalogLookup: cat,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	node, ok := result.Nodes.Get("api")
	if !ok || node == nil {
		t.Fatal("expected 'api' node")
	}
	v, hasLabel := node.Labels.Get("component.aggrega.ai/type")
	if !hasLabel || v != "server" {
		t.Errorf("expected catalog labels copied; got %+v", node.Labels)
	}
}

// TestBuild_BoundaryInfraPopulatesBoundaryPath pins the boundary-info
// arm of Build's step 3: nodes that appear in the resolver's
// BoundaryInfra map receive their BoundaryPath copied onto the
// TopologyNode.
func TestBuild_BoundaryInfraPopulatesBoundaryPath(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server"})
	rr := &resolver.ResolveResult{
		Aggregate: agg,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {BoundaryPath: "prod/web"},
		},
	}
	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	node, _ := result.Nodes.Get("api")
	if node == nil || node.BoundaryPath != "prod/web" {
		t.Errorf("expected BoundaryPath=prod/web; got %+v", node)
	}
}

// TestBuild_ComponentUsesProducesEdges pins the edge-normalisation
// arm of Build's step 4: a component declaring a `uses` socket
// produces NormalizedEdge entries in compositional author order.
func TestBuild_ComponentUsesProducesEdges(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{
		Compact: []string{"db"},
	})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Uses: uses})
	agg.Spec.Components.Set("db", types.ComponentInstance{Type: "core/database/postgres"})
	rr := &resolver.ResolveResult{Aggregate: agg}
	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(result.Edges) == 0 {
		t.Errorf("expected at least one edge from uses; got none")
	}
}

// TestBuild_FeatureUsesProducesEdgesWithOriginFeature pins the
// feature-uses edge arm of Build's step 5: a Feature attached to a
// component with its own `uses:` socket produces NormalizedEdges
// marked with the originating Feature type.
func TestBuild_FeatureUsesProducesEdgesWithOriginFeature(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	featUses := types.OrderedMap[types.UsesSocketValue]{}
	featUses.Set("metrics_target", types.UsesSocketValue{Compact: []string{"backend"}})
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("monitoring", types.FeatureInstance{Type: "core/monitoring", Uses: featUses})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Features: feats})
	agg.Spec.Components.Set("backend", types.ComponentInstance{Type: "core/server"})
	rr := &resolver.ResolveResult{Aggregate: agg}
	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sawFeatureEdge := false
	for _, e := range result.Edges {
		if e.OriginFeature == "core/monitoring" {
			sawFeatureEdge = true
		}
	}
	if !sawFeatureEdge {
		t.Errorf("expected edge marked OriginFeature=core/monitoring; got %v", result.Edges)
	}
}

// TestBuild_SecretUsesProducesEdges pins the secret-uses edge arm of
// Build's step 5. A Secret attached to a component with `uses:`
// produces edges marked with the Secret's type as OriginFeature.
func TestBuild_SecretUsesProducesEdges(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	secUses := types.OrderedMap[types.UsesSocketValue]{}
	secUses.Set("backing_secret", types.UsesSocketValue{Compact: []string{"backend"}})
	secs := types.OrderedMap[types.SecretInstance]{}
	secs.Set("db-creds", types.SecretInstance{Type: "core/credentials", Uses: secUses})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Secrets: secs})
	agg.Spec.Components.Set("backend", types.ComponentInstance{Type: "core/server"})
	rr := &resolver.ResolveResult{Aggregate: agg}
	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sawSecretEdge := false
	for _, e := range result.Edges {
		if e.OriginFeature == "core/credentials" {
			sawSecretEdge = true
		}
	}
	if !sawSecretEdge {
		t.Errorf("expected edge marked OriginFeature=core/credentials; got %v", result.Edges)
	}
}

// TestFlattenSubsystem_NestedSubsystemRecursion pins the recursive
// descent into nested subsystems within flattenSubsystem. Components
// at multiple nesting levels all surface as flat nodes with their
// qualified IDs.
func TestFlattenSubsystem_NestedSubsystemRecursion(t *testing.T) {
	t.Parallel()
	leafComps := types.OrderedMap[types.ComponentInstance]{}
	leafComps.Set("leaf", types.ComponentInstance{Type: "core/server"})
	leaf := types.Subsystem{Components: leafComps}
	midSubs := types.OrderedMap[types.Subsystem]{}
	midSubs.Set("inner", leaf)
	mid := types.Subsystem{Subsystems: midSubs}
	result := &flattenResult{
		Nodes:              types.OrderedMap[*TopologyNode]{},
		FeatureAttachments: map[string][]FeatureAttachment{},
		SecretAttachments:  map[string][]SecretAttachment{},
	}
	if err := flattenSubsystem(result, "", ".outer", mid); err != nil {
		t.Fatalf("flattenSubsystem: %v", err)
	}
	if _, ok := result.Nodes.Get("leaf"); !ok {
		t.Errorf("expected leaf node to flatten; got nodes: %v", result.Nodes.Keys())
	}
}
