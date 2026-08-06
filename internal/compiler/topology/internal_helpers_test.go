// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
	agg.Spec.Components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})
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

// TestBuild_CredentialAttachmentUsesProducesEdges pins the attachment-uses
// edge arm of Build's step 5 for a credential attachment. A credential
// attached to a component with `uses:` produces edges marked with the
// credential Feature's type as OriginFeature, exactly as any other
// attachment's uses block does.
func TestBuild_CredentialAttachmentUsesProducesEdges(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	secUses := types.OrderedMap[types.UsesSocketValue]{}
	secUses.Set("backing_secret", types.UsesSocketValue{Compact: []string{"backend"}})
	secs := types.OrderedMap[types.FeatureInstance]{}
	secs.Set("db-creds", types.FeatureInstance{Type: "core/credentials", Uses: secUses})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Features: secs})
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

// TestFlattenSubsystems_NestedSubsystemRecursion pins the recursive descent into
// nested subsystems. Components at multiple nesting levels all surface as flat
// nodes with their qualified IDs, and each carries the dotted path of every
// subsystem on its ancestry.
func TestFlattenSubsystems_NestedSubsystemRecursion(t *testing.T) {
	t.Parallel()
	leafComps := types.OrderedMap[types.ComponentInstance]{}
	leafComps.Set("leaf", types.ComponentInstance{Type: "core/server"})
	leaf := types.Subsystem{Components: leafComps}
	midSubs := types.OrderedMap[types.Subsystem]{}
	midSubs.Set("inner", leaf)
	mid := types.Subsystem{Subsystems: midSubs}
	outer := types.OrderedMap[types.Subsystem]{}
	outer.Set("outer", mid)
	agg := &types.Aggregate{}
	agg.Spec.Subsystems = outer
	result := &flattenResult{
		Nodes:              types.OrderedMap[*TopologyNode]{},
		FeatureAttachments: map[string][]FeatureAttachment{},
	}
	if err := flattenSubsystems(result, "", "", "", types.Normalize(agg), nil); err != nil {
		t.Fatalf("flattenSubsystems: %v", err)
	}
	node, ok := result.Nodes.Get("leaf")
	if !ok {
		t.Fatalf("expected leaf node to flatten; got nodes: %v", result.Nodes.Keys())
	}
	if node.SubsystemPath != "outer.inner" {
		t.Errorf("SubsystemPath = %q, want %q", node.SubsystemPath, "outer.inner")
	}
}

// mkReleaseUses builds a Release whose boundary "prod" declares the given
// per-edge uses surface on component nodeID, exercising the Release pass of
// Build (a Release configures/wires edges on the components an Aggregate
// declares).
func mkReleaseUses(nodeID string, uses types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]) *types.Release {
	comps := types.OrderedMap[types.BoundaryComponent]{}
	comps.Set(nodeID, types.BoundaryComponent{Uses: uses})
	b := types.Boundary{Components: comps}
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", b)
	rel := &types.Release{}
	rel.Spec.Boundaries = boundaries
	return rel
}

// relSocketAlias wraps a single (socket, alias, entry) into the two-level
// socket->alias->entry shape the Release uses surface carries.
func relSocketAlias(socket, alias string, entry types.ReleaseUsesEntry) types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]] {
	aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
	aliases.Set(alias, entry)
	uses := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	uses.Set(socket, aliases)
	return uses
}

// findEdge returns the first edge matching (source, socket, alias), or nil.
func findEdge(edges []NormalizedEdge, source, socket, alias string) *NormalizedEdge {
	for i := range edges {
		if edges[i].Source == source && edges[i].Socket == socket && edges[i].Alias == alias {
			return &edges[i]
		}
	}
	return nil
}

// TestBuild_ComponentUsesValuesInfraFlowToEdge pins the developer-authored
// per-edge values/infra arm: a verbose `uses` entry carrying values and infra
// projects those maps onto the produced edge.
func TestBuild_ComponentUsesValuesInfraFlowToEdge(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	values := types.OrderedMap[any]{}
	values.Set("pool_size", 20)
	infra := types.OrderedMap[any]{}
	infra.Set("storage_class", "fast")
	verbose.Set("db", types.VerboseUsesEntry{Target: "db", Values: values, Infra: infra})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Uses: uses})
	agg.Spec.Components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})

	result, err := Build(BuildConfig{ResolveResult: &resolver.ResolveResult{Aggregate: agg}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	e := findEdge(result.Edges, "api", "databases", "db")
	if e == nil {
		t.Fatalf("expected edge api/databases/db; got %v", result.Edges)
	}
	if v, ok := e.Values.Get("pool_size"); !ok || v != 20 {
		t.Errorf("edge values.pool_size = %v (ok=%v), want 20", v, ok)
	}
	if v, ok := e.Infra.Get("storage_class"); !ok || v != "fast" {
		t.Errorf("edge infra.storage_class = %v (ok=%v), want fast", v, ok)
	}
}

// TestBuild_ReleaseInfraMergesOntoExistingEdge pins the Release pass's
// configure arm: a Release entry matching an existing edge by
// (consumer, socket, alias) merges its infra onto that edge without
// retargeting and without marking the edge Release-origin.
func TestBuild_ReleaseInfraMergesOntoExistingEdge(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Uses: uses})
	agg.Spec.Components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})

	relInfra := types.OrderedMap[any]{}
	relInfra.Set("pool_size", 20)
	rel := mkReleaseUses("api", relSocketAlias("databases", "db", types.ReleaseUsesEntry{Infra: relInfra}))

	rr := &resolver.ResolveResult{
		Aggregate:     agg,
		Release:       rel,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "prod"}},
	}
	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	e := findEdge(result.Edges, "api", "databases", "db")
	if e == nil {
		t.Fatalf("expected edge api/databases/db; got %v", result.Edges)
	}
	if v, ok := e.Infra.Get("pool_size"); !ok || v != 20 {
		t.Errorf("merged infra.pool_size = %v (ok=%v), want 20", v, ok)
	}
	if e.Target != "db" {
		t.Errorf("Target = %q, want db (merge must not retarget)", e.Target)
	}
	if e.OriginRelease {
		t.Errorf("OriginRelease = true on a configured existing edge; want false")
	}
}

// TestBuild_ReleaseCreatesEdgeForUnmatchedSocket pins the Release pass's wire
// arm: a Release entry naming a target into a socket the Aggregate left
// unwired creates a new edge stamped OriginRelease.
func TestBuild_ReleaseCreatesEdgeForUnmatchedSocket(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server"})
	agg.Spec.Components.Set("cache", types.ComponentInstance{Type: "core/cache/redis"})

	rel := mkReleaseUses("api", relSocketAlias("caches", "primary", types.ReleaseUsesEntry{Target: "cache"}))
	rr := &resolver.ResolveResult{
		Aggregate:     agg,
		Release:       rel,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "prod"}},
	}
	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	e := findEdge(result.Edges, "api", "caches", "primary")
	if e == nil {
		t.Fatalf("expected Release-created edge api/caches/primary; got %v", result.Edges)
	}
	if e.Target != "cache" {
		t.Errorf("Target = %q, want cache", e.Target)
	}
	if !e.OriginRelease {
		t.Errorf("OriginRelease = false on a Release-created edge; want true")
	}
}

// TestBuild_ReleaseConflictingTargetHalts pins the no-retarget Loud Guardrail:
// a Release entry that names a target differing from an existing
// developer-declared edge's target halts with ErrReleaseEdgeRetarget — the
// developer owns the architecture they declared.
func TestBuild_ReleaseConflictingTargetHalts(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Uses: uses})
	agg.Spec.Components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})
	agg.Spec.Components.Set("other", types.ComponentInstance{Type: "core/datastore/postgres"})

	rel := mkReleaseUses("api", relSocketAlias("databases", "db", types.ReleaseUsesEntry{Target: "other"}))
	rr := &resolver.ResolveResult{
		Aggregate:     agg,
		Release:       rel,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "prod"}},
	}
	_, err := Build(BuildConfig{ResolveResult: rr})
	if !errors.Is(err, ErrReleaseEdgeRetarget) {
		t.Fatalf("want ErrReleaseEdgeRetarget; got %v", err)
	}
}

// TestBuild_ReleaseSameTargetConfigures pins the configure-vs-retarget
// distinction: a Release entry naming the SAME target as an existing edge is a
// configure (merge infra), not a retarget — no halt, infra merges.
func TestBuild_ReleaseSameTargetConfigures(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Uses: uses})
	agg.Spec.Components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})

	relInfra := types.OrderedMap[any]{}
	relInfra.Set("x", 1)
	rel := mkReleaseUses("api", relSocketAlias("databases", "db", types.ReleaseUsesEntry{Target: "db", Infra: relInfra}))
	rr := &resolver.ResolveResult{
		Aggregate:     agg,
		Release:       rel,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "prod"}},
	}
	result, err := Build(BuildConfig{ResolveResult: rr})
	if err != nil {
		t.Fatalf("same-target configure must not halt; got %v", err)
	}
	e := findEdge(result.Edges, "api", "databases", "db")
	if e == nil {
		t.Fatalf("expected edge api/databases/db; got %v", result.Edges)
	}
	if v, ok := e.Infra.Get("x"); !ok || v != 1 {
		t.Errorf("same-target configure should merge infra; infra.x = %v (ok=%v)", v, ok)
	}
}

// TestBuild_ReleaseDanglingTargetHalts pins the dangling-target Loud Guardrail
// for a Release-wired edge: a Release entry wiring a new edge to a non-existent
// component halts with ErrDanglingUsesTarget.
func TestBuild_ReleaseDanglingTargetHalts(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server"})

	rel := mkReleaseUses("api", relSocketAlias("caches", "primary", types.ReleaseUsesEntry{Target: "ghost"}))
	rr := &resolver.ResolveResult{
		Aggregate:     agg,
		Release:       rel,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "prod"}},
	}
	_, err := Build(BuildConfig{ResolveResult: rr})
	if !errors.Is(err, ErrDanglingUsesTarget) {
		t.Fatalf("want ErrDanglingUsesTarget; got %v", err)
	}
}

// TestBuild_AggregateDanglingTargetHalts pins that the dangling-target guardrail
// is origin-agnostic: a developer-authored Aggregate edge to a non-existent
// target halts with the same ErrDanglingUsesTarget as the Release-wired case —
// no silent drop.
func TestBuild_AggregateDanglingTargetHalts(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{}
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"ghost-db"}})
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Uses: uses})

	rr := &resolver.ResolveResult{Aggregate: agg}
	_, err := Build(BuildConfig{ResolveResult: rr})
	if !errors.Is(err, ErrDanglingUsesTarget) {
		t.Fatalf("want ErrDanglingUsesTarget for a dangling Aggregate edge; got %v", err)
	}
}

// TestBuild_ReleaseEdgeSliceDeterministic pins determinism of the edge slice
// across repeated builds, including Release-created edges: identical input
// yields a byte-identical edge ordering (Invariant 8).
func TestBuild_ReleaseEdgeSliceDeterministic(t *testing.T) {
	t.Parallel()
	build := func() []NormalizedEdge {
		agg := &types.Aggregate{}
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
		agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/server", Uses: uses})
		agg.Spec.Components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})
		agg.Spec.Components.Set("cache", types.ComponentInstance{Type: "core/cache/redis"})
		agg.Spec.Components.Set("queue", types.ComponentInstance{Type: "core/queue"})

		// Two Release-created edges under distinct sockets, in author order.
		aliasesA := types.OrderedMap[types.ReleaseUsesEntry]{}
		aliasesA.Set("primary", types.ReleaseUsesEntry{Target: "cache"})
		aliasesB := types.OrderedMap[types.ReleaseUsesEntry]{}
		aliasesB.Set("jobs", types.ReleaseUsesEntry{Target: "queue"})
		ru := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
		ru.Set("caches", aliasesA)
		ru.Set("queues", aliasesB)
		rel := mkReleaseUses("api", ru)

		rr := &resolver.ResolveResult{
			Aggregate:     agg,
			Release:       rel,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "prod"}},
		}
		result, err := Build(BuildConfig{ResolveResult: rr})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return result.Edges
	}

	first := build()
	key := func(e NormalizedEdge) string {
		return e.Source + "|" + e.Socket + "|" + e.Alias + "|" + e.Target
	}
	for run := 0; run < 3; run++ {
		got := build()
		if len(got) != len(first) {
			t.Fatalf("edge count drift: run %d has %d edges, first had %d", run, len(got), len(first))
		}
		for i := range got {
			if key(got[i]) != key(first[i]) {
				t.Errorf("edge order drift at %d (run %d): %q vs %q", i, run, key(got[i]), key(first[i]))
			}
		}
	}
}
