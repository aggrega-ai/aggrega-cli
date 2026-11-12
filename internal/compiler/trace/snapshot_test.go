// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package trace

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func makeCtx() *types.TemplateContext {
	values := types.OrderedMap[any]{}
	values.Set("port", 8080)
	infra := types.OrderedMap[any]{}
	infra.Set("replicas", 2)
	locals := types.OrderedMap[string]{}
	locals.Set("primary", "main")
	return &types.TemplateContext{
		Values: values,
		Infra:  infra,
		Locals: locals,
		Context: types.ContextBlock{
			InstanceID: "api",
		},
		Source:   "./charts/api",
		Workload: &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"},
	}
}

// TestPillarSnapshot_PhaseB0_AbsentWorkload validates the per-pass mapping
// honesty contract: a source-resolution snapshot must NOT contain .workload
// because .workload is not yet populated there. Earlier framework-rendered
// error messages would surface .workload = nil at source resolution because
// the field existed on the context regardless of pass; the snapshot helper
// drops the field at passes where the compiler had no visibility into it.
func TestPillarSnapshot_PhaseB0_AbsentWorkload(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(makeCtx(), PassPhaseB0, redact.Keys{})
	if _, present := snap["workload"]; present {
		t.Errorf("source-resolution snapshot must NOT contain .workload; got: %v", snap)
	}
	// .source IS present at source resolution (that pass is what resolves it).
	if _, present := snap["source"]; !present {
		t.Errorf("source-resolution snapshot must contain .source; got: %v", snap)
	}
	// Baseline pillars present.
	if _, present := snap["values"]; !present {
		t.Errorf("source-resolution snapshot must contain .values; got: %v", snap)
	}
}

// TestPillarSnapshot_PhaseB3_PresentWorkload validates the mirror
// condition: at artifact evaluation, .workload is populated (workload
// synthesis set it) and surfaces in the snapshot.
func TestPillarSnapshot_PhaseB3_PresentWorkload(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(makeCtx(), PassPhaseB3, redact.Keys{})
	if _, present := snap["workload"]; !present {
		t.Errorf("artifact-evaluation snapshot must contain .workload; got keys: %v", keysOf(snap))
	}
}

// TestPillarSnapshot_PhaseB6_BoundaryShape validates feature rendering's
// boundary-scope branch: when ctx.Boundary is non-nil, the snapshot drops
// .context.* (which is meaningless at boundary scope) and surfaces
// .boundary.* instead.
func TestPillarSnapshot_PhaseB6_BoundaryShape(t *testing.T) {
	t.Parallel()
	ctx := makeCtx()
	ctx.Boundary = &types.BoundaryContext{
		Path:         "cluster/services-net",
		BoundaryName: "services-net",
	}
	snap := PillarSnapshot(ctx, PassPhaseB6, redact.Keys{})
	if _, present := snap["boundary"]; !present {
		t.Errorf("feature-rendering boundary-scope snapshot must contain .boundary; got keys: %v", keysOf(snap))
	}
	// .context.instance_id must NOT appear (boundary scope drops it).
	if _, present := snap["context.instance_id"]; present {
		t.Errorf("feature-rendering boundary-scope snapshot must NOT contain .context.instance_id; got keys: %v", keysOf(snap))
	}
}

// TestPillarSnapshot_PhaseProvides_Baseline validates provides evaluation
// emits the baseline pillars only — values, infra, context.instance_id,
// metadata — without inject (inject hydration populates it), without locals
// or source (source resolution does), without workload (workload synthesis
// does).
func TestPillarSnapshot_PhaseProvides_Baseline(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(makeCtx(), PassProvides, redact.Keys{})
	for _, key := range []string{"context.inject", "context.uses", "locals", "source", "workload"} {
		if _, present := snap[key]; present {
			t.Errorf("provides-evaluation snapshot must NOT contain %q; got keys: %v", key, keysOf(snap))
		}
	}
}

// TestPillarSnapshot_NilCtx returns an empty map (defensive — error paths
// may produce a nil-context snapshot when the failure is upstream of
// context construction).
func TestPillarSnapshot_NilCtx(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(nil, PassPhaseB3, redact.Keys{})
	if len(snap) != 0 {
		t.Errorf("nil ctx must return empty map; got: %v", snap)
	}
}

// TestPillarSnapshot_WithholdsDerivedLocalsAndSource covers the two projections
// whose content is a template OUTPUT rather than an authored pillar. The key set
// arrives already computed — this package reads no template — so the assertion
// here is that the projection APPLIES it, and applies it discriminately.
//
// Each positive assertion is paired with a control on the same surface: an entry
// the set does not name must survive verbatim, which is what tells a working
// substitution apart from a blanket one.
func TestPillarSnapshot_WithholdsDerivedLocalsAndSource(t *testing.T) {
	t.Parallel()
	ctx := makeCtx()
	// Author order is deliberately not alphabetical, so a sort creeping into the
	// substitution shows up as a reordered key list.
	locals := types.OrderedMap[string]{}
	locals.Set("zdsn", "postgres://app:hunter2@db/appdb")
	locals.Set("appLabel", "demo-local")
	ctx.Locals = locals
	ctx.Source = "oci://ghcr.io/app:hunter2"

	snap := PillarSnapshot(ctx, PassPhaseB0, redact.Keys{
		Locals: map[string]bool{"zdsn": true},
		Source: map[string]bool{redact.WholeSourceKey: true},
	})

	shown, ok := snap["locals"].(types.OrderedMap[string])
	if !ok {
		t.Fatalf("the locals projection must stay an ordered block; got %T", snap["locals"])
	}
	if v, _ := shown.Get("zdsn"); v != redact.Placeholder() {
		t.Errorf("the withheld local must show the placeholder; got %q", v)
	}
	if v, _ := shown.Get("appLabel"); v != "demo-local" {
		t.Errorf("a local the set does not name must survive verbatim; got %q", v)
	}
	if got := shown.Keys(); len(got) != 2 || got[0] != "zdsn" {
		t.Errorf("author order must survive the substitution; got %v", got)
	}
	if snap["source"] != redact.Placeholder() {
		t.Errorf("the withheld source must show the placeholder; got %v", snap["source"])
	}
}

// TestPillarSnapshot_EmptyKeysLeavesDerivedProjectionsVerbatim is the other half
// of the same boundary: a node with nothing declared must produce the projection
// a build predating the axis produced, byte for byte.
func TestPillarSnapshot_EmptyKeysLeavesDerivedProjectionsVerbatim(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(makeCtx(), PassPhaseB0, redact.Keys{})
	shown, ok := snap["locals"].(types.OrderedMap[string])
	if !ok {
		t.Fatalf("the locals projection must stay an ordered block; got %T", snap["locals"])
	}
	if v, _ := shown.Get("primary"); v != "main" {
		t.Errorf("an empty set must leave the locals projection untouched; got %q", v)
	}
	if snap["source"] != "./charts/api" {
		t.Errorf("an empty set must leave the source projection untouched; got %v", snap["source"])
	}
}

// TestPillarSnapshot_WorkloadProjectionPerDisplayDecision covers the projection
// that is not addressable by key, across all three of the display decisions a set
// can carry. Each is asserted beside the other two rather than on its own, because
// the failure to catch is one decision's branch behaving like another's.
//
// Verbatim shows the document. Substituted also shows the document — the
// substituted one, which arrives on the context — but must ANNOUNCE that, or a
// reader takes a re-render for what the build emitted. Unavailable replaces the
// document with the withholding statement and keeps the key, because a projection
// that merely vanished would read as "this Recipe declares no workload", a
// different fact about the build.
func TestPillarSnapshot_WorkloadProjectionPerDisplayDecision(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{"port": true}

	verbatim := PillarSnapshot(makeCtx(), PassPhaseB3, redact.Keys{Values: declared})
	if _, isDoc := verbatim["workload"].(*types.Workload); !isDoc {
		t.Errorf("the default decision shows the emitted document; got %T", verbatim["workload"])
	}
	if note, present := verbatim["workload_note"]; present {
		t.Errorf("an unsubstituted document must carry no announcement; got %v", note)
	}

	substituted := PillarSnapshot(makeCtx(), PassPhaseB3, redact.Keys{
		Values:   declared,
		Workload: redact.WorkloadSubstituted,
	})
	if _, isDoc := substituted["workload"].(*types.Workload); !isDoc {
		t.Errorf("a substituted display still shows a document; got %T", substituted["workload"])
	}
	if got := substituted["workload_note"]; got != redact.SubstitutedProjection() {
		t.Errorf("a substituted display must announce that it is a re-render; got %v", got)
	}

	unavailable := PillarSnapshot(makeCtx(), PassPhaseB3, redact.Keys{
		Values:   declared,
		Workload: redact.WorkloadUnavailable,
	})
	shown, present := unavailable["workload"]
	if !present {
		t.Fatalf("the key must survive so the reader can see something was withheld; got keys: %v", keysOf(unavailable))
	}
	if shown != redact.WithheldProjection() {
		t.Errorf("an unavailable projection must show the withholding statement; got %v", shown)
	}
	if _, isDoc := shown.(*types.Workload); isDoc {
		t.Error("the rendered document must not survive the withholding")
	}
	if note, present := unavailable["workload_note"]; present {
		t.Errorf("the withholding statement stands alone; got a second announcement %v", note)
	}
}

// TestPillarSnapshot_WithholdsConsumedTargetProvides covers the CROSS-NODE
// projection: the values withheld here are governed by declarations the displayed
// node did not make.
//
// Three controls sit beside the positive assertion. The marked edge's own unmarked
// sibling key must survive; a second edge publishing the SAME key name must survive
// whole, which is what proves the withholding is not name-shaped; and the marked
// edge's non-provides fields must survive, because a target's provides declaration
// does not govern the connection's own configuration.
func TestPillarSnapshot_WithholdsConsumedTargetProvides(t *testing.T) {
	t.Parallel()
	ctx := makeCtx()
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("storeEdge", types.EnrichedEdge{
		Target:    "store",
		Component: "core/uses-target",
		Provides:  edgeProvides("zzTargetSecret", "wobble", "aaTargetHost", "store"),
		Infra:     om2("pool_size", 50),
	})
	uses.Set("plainEdge", types.EnrichedEdge{
		Target:    "plain",
		Component: "core/uses-plain",
		Provides:  edgeProvides("zzTargetSecret", "visible", "aaTargetHost", "plain"),
	})
	ctx.Context.Uses = uses

	snap := PillarSnapshot(ctx, PassPhaseA, redact.Keys{
		UsesProvides: map[string]map[string]bool{"storeEdge": {"zzTargetSecret": true}},
	})

	shown, ok := snap["context.uses"].(types.OrderedMap[types.EnrichedEdge])
	if !ok {
		t.Fatalf("the uses projection must stay an ordered edge map; got %T", snap["context.uses"])
	}
	marked, _ := shown.Get("storeEdge")
	if v, _ := marked.Provides.Get("zzTargetSecret"); v != redact.Placeholder() {
		t.Errorf("the target's declared provides key must show the placeholder; got %q", v)
	}
	if v, _ := marked.Provides.Get("aaTargetHost"); v != "store" {
		t.Errorf("an undeclared sibling on the same edge must survive; got %q", v)
	}
	if v, _ := marked.Infra.Get("pool_size"); v != 50 {
		t.Errorf("per-connection infra is not what a provides declaration governs; got %v", v)
	}
	plain, _ := shown.Get("plainEdge")
	if v, _ := plain.Provides.Get("zzTargetSecret"); v != "visible" {
		t.Errorf("the same key name on an undeclared target's edge must survive; got %q", v)
	}
	if got := shown.Keys(); len(got) != 2 || got[0] != "storeEdge" {
		t.Errorf("alias order must survive the substitution; got %v", got)
	}
}

// TestPillarSnapshot_CustomOnlyPipeShowsInjectBlock covers a delivery pipe that
// carries nothing but a custom mechanism — a FeatureRecipe delivering under a
// mechanism of its own name, with no env, file or arg key on the node. Templates
// read those keys through the mechanism's name and through the merged view, so the
// projection must show the block, and must withhold the marked key on both views
// it prints while an unmarked sibling survives.
//
// The last row is the boundary of the rule: a custom mechanism present with no
// keys delivers nothing, and shows no block.
func TestPillarSnapshot_CustomOnlyPipeShowsInjectBlock(t *testing.T) {
	t.Parallel()
	customOnly := func() *types.TemplateContext {
		ctx := makeCtx()
		vault := edgeProvides("MARIADB_ROOT_PASSWORD", "root-plaintext", "VAULT_PATH", "secret/data/db")
		additional := types.OrderedMap[types.OrderedMap[string]]{}
		additional.Set("vault", vault)
		ctx.Context.Inject = types.InjectPipe{
			Env:        types.OrderedMap[string]{},
			File:       types.OrderedMap[string]{},
			Arg:        types.OrderedMap[string]{},
			All:        edgeProvides("MARIADB_ROOT_PASSWORD", "root-plaintext", "VAULT_PATH", "secret/data/db"),
			Additional: additional,
		}
		return ctx
	}
	keys := redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{}, redact.CrossNode{},
		redact.DeliveryKeys{"vault": {"MARIADB_ROOT_PASSWORD": true}}, nil, redact.Derived{})

	for _, pass := range []Pass{PassInject, PassPhaseA, PassPhaseB3} {
		snap := PillarSnapshot(customOnly(), pass, keys)
		shown, ok := snap["context.inject"].(types.InjectPipe)
		if !ok {
			t.Fatalf("pass %v: a pipe carrying only a custom mechanism must show its inject block; got keys %v", pass, keysOf(snap))
		}
		vault, present := shown.Additional.Get("vault")
		if !present {
			t.Fatalf("pass %v: the custom mechanism must be shown; got %v", pass, shown.Additional.Keys())
		}
		if v, _ := vault.Get("MARIADB_ROOT_PASSWORD"); v != redact.Placeholder() {
			t.Errorf("pass %v: the marked key must be withheld under its mechanism; got %q", pass, v)
		}
		if v, _ := shown.All.Get("MARIADB_ROOT_PASSWORD"); v != redact.Placeholder() {
			t.Errorf("pass %v: the marked key must be withheld in the merged view; got %q", pass, v)
		}
		if v, _ := vault.Get("VAULT_PATH"); v != "secret/data/db" {
			t.Errorf("pass %v: an unmarked key under the mechanism must survive; got %q", pass, v)
		}
		if v, _ := shown.All.Get("VAULT_PATH"); v != "secret/data/db" {
			t.Errorf("pass %v: an unmarked key in the merged view must survive; got %q", pass, v)
		}
	}

	empty := makeCtx()
	additional := types.OrderedMap[types.OrderedMap[string]]{}
	additional.Set("vault", types.OrderedMap[string]{})
	empty.Context.Inject = types.InjectPipe{Additional: additional}
	if _, present := PillarSnapshot(empty, PassPhaseA, keys)["context.inject"]; present {
		t.Error("a custom mechanism carrying no key delivers nothing and must show no inject block")
	}
}

func edgeProvides(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func om2(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
