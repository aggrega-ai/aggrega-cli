// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
		Workload: &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"},
	}
}

// TestPillarSnapshot_PhaseB0_AbsentWorkload validates the per-pass mapping
// honesty contract: a Phase B.0 snapshot must NOT contain .workload because
// .workload is not yet populated at Phase B.0. Earlier framework-rendered
// error messages would surface .workload = nil at B.0 because the field
// existed on the context regardless of pass; the snapshot helper drops the
// field at passes where the compiler had no visibility into it.
func TestPillarSnapshot_PhaseB0_AbsentWorkload(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(makeCtx(), PassPhaseB0, redact.Keys{})
	if _, present := snap["workload"]; present {
		t.Errorf("Phase B.0 snapshot must NOT contain .workload; got: %v", snap)
	}
	// .source IS present at Phase B.0 (resolved during the source-eval pass).
	if _, present := snap["source"]; !present {
		t.Errorf("Phase B.0 snapshot must contain .source; got: %v", snap)
	}
	// Baseline pillars present.
	if _, present := snap["values"]; !present {
		t.Errorf("Phase B.0 snapshot must contain .values; got: %v", snap)
	}
}

// TestPillarSnapshot_PhaseB3_PresentWorkload validates the mirror
// condition: at Phase B.3 (artifacts evaluation), .workload is populated
// (set during Phase B.2) and surfaces in the snapshot.
func TestPillarSnapshot_PhaseB3_PresentWorkload(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(makeCtx(), PassPhaseB3, redact.Keys{})
	if _, present := snap["workload"]; !present {
		t.Errorf("Phase B.3 snapshot must contain .workload; got keys: %v", keysOf(snap))
	}
}

// TestPillarSnapshot_PhaseB6_BoundaryShape validates the Phase B.6 boundary-
// scope branch: when ctx.Boundary is non-nil, the snapshot drops .context.*
// (which is meaningless at boundary scope) and surfaces .boundary.* instead.
func TestPillarSnapshot_PhaseB6_BoundaryShape(t *testing.T) {
	t.Parallel()
	ctx := makeCtx()
	ctx.Boundary = &types.BoundaryContext{
		Path:         "cluster/services-net",
		BoundaryName: "services-net",
	}
	snap := PillarSnapshot(ctx, PassPhaseB6, redact.Keys{})
	if _, present := snap["boundary"]; !present {
		t.Errorf("Phase B.6 boundary-scope snapshot must contain .boundary; got keys: %v", keysOf(snap))
	}
	// .context.instance_id must NOT appear (boundary scope drops it).
	if _, present := snap["context.instance_id"]; present {
		t.Errorf("Phase B.6 boundary-scope snapshot must NOT contain .context.instance_id; got keys: %v", keysOf(snap))
	}
}

// TestPillarSnapshot_PhaseProvides_Baseline validates Pass 1 emits the
// baseline pillars only — values, infra, context.instance_id, metadata —
// without inject (Pass 2 hydrates), without locals (B.0), without
// source (B.0), without workload (B.2).
func TestPillarSnapshot_PhaseProvides_Baseline(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(makeCtx(), PassProvides, redact.Keys{})
	for _, key := range []string{"context.inject", "context.uses", "locals", "source", "workload"} {
		if _, present := snap[key]; present {
			t.Errorf("Pass 1 snapshot must NOT contain %q; got keys: %v", key, keysOf(snap))
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

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
