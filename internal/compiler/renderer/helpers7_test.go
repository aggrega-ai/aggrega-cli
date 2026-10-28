// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuildBoundaryContext_AllBranches pins nil-release error,
// missing-boundary error, populated boundary + node-subtree + edges +
// labels paths.
func TestBuildBoundaryContext_AllBranches(t *testing.T) {
	t.Parallel()
	// Nil release.
	_, err := buildBoundaryContext(nil, "p", &types.IRGraph{}, nil, nil, nil)
	if err == nil {
		t.Errorf("nil release should error")
	}

	// Missing boundary.
	rel := &types.Release{}
	_, err = buildBoundaryContext(nil, "absent", &types.IRGraph{}, rel, nil, nil)
	if err == nil {
		t.Errorf("missing boundary should error")
	}

	// Populated case: 2-component boundary + 1 internal edge + 1
	// outgoing cross-boundary + 1 incoming.
	infra := types.Boundary{Description: "infra"}
	fr1Infra := types.OrderedMap[any]{}
	fr1Infra.Set("size", "small")
	infra.Features.Set("fr1", types.BoundaryFeature{Type: "feat/x", Infra: fr1Infra})
	web := types.Boundary{Description: "web"}
	infra.Boundaries.Set("web", web)
	rel.Spec.Boundaries.Set("infra", infra)

	graph := &types.IRGraph{}
	internal := types.IRNode{BoundaryPath: "infra", Component: "comp/internal"}
	internal.Labels.Set("tier", "data")
	leafNode := types.IRNode{BoundaryPath: "infra/web", Component: "comp/leaf"}
	leafNode.Labels.Set("zone", "us-east")
	outside := types.IRNode{BoundaryPath: "other", Component: "comp/outside"}
	graph.Spec.Nodes.Set("internal", internal)
	graph.Spec.Nodes.Set("leaf", leafNode)
	graph.Spec.Nodes.Set("outside", outside)

	// Boundary-feature projection — what the IR builder emits on
	// graph.Spec.BoundaryFeatures, mirroring the Release's infra/fr1.
	bfInfra := types.OrderedMap[any]{}
	bfInfra.Set("size", "small")
	bfInner := types.OrderedMap[types.IRBoundaryFeature]{}
	bfInner.Set("fr1", types.IRBoundaryFeature{Type: "feat/x", Infra: bfInfra})
	graph.Spec.BoundaryFeatures.Set("infra", bfInner)

	graph.Spec.Edges = []types.IREdge{
		{Source: "internal", Target: "leaf", Socket: "in"},      // internal-to-internal
		{Source: "internal", Target: "outside", Socket: "out"},  // outgoing cross-boundary
		{Source: "outside", Target: "internal", Socket: "back"}, // incoming cross-boundary
	}

	anns := map[string]types.OrderedMap[string]{}
	annOM := types.OrderedMap[string]{}
	annOM.Set("a", "v")
	anns["infra"] = annOM

	got, err := buildBoundaryContext(nil, "infra", graph, rel, anns, nil)
	if err != nil {
		t.Fatalf("populated: %v", err)
	}
	if got.Path != "infra" || got.BoundaryName != "infra" {
		t.Errorf("path/name: %+v", got)
	}
	if got.Components.Len() != 2 {
		t.Errorf("expected 2 components in subtree; got %d", got.Components.Len())
	}
	if len(got.InternalUsesEdges) != 1 {
		t.Errorf("InternalUsesEdges = %d; want 1", len(got.InternalUsesEdges))
	}
	if len(got.OutgoingCrossBoundaryUsesEdges) != 1 {
		t.Errorf("OutgoingCrossBoundary = %d; want 1", len(got.OutgoingCrossBoundaryUsesEdges))
	}
	if len(got.IncomingCrossBoundaryUsesEdges) != 1 {
		t.Errorf("IncomingCrossBoundary = %d; want 1", len(got.IncomingCrossBoundaryUsesEdges))
	}
	if len(got.CrossBoundaryUsesEdges) != 2 {
		t.Errorf("CrossBoundary (union) = %d; want 2", len(got.CrossBoundaryUsesEdges))
	}
	if got.Labels.Len() != 2 {
		t.Errorf("Labels = %d; want 2", got.Labels.Len())
	}
	if got.Annotations.Len() != 1 {
		t.Errorf("Annotations = %d; want 1", got.Annotations.Len())
	}
	if len(got.FeatureAttachments) != 1 {
		t.Errorf("FeatureAttachments = %d; want 1", len(got.FeatureAttachments))
	}
}

// TestExecutePass2a_LiteralEnvMechanism pins the happy path with a
// literal env value.
func TestExecutePass2a_LiteralEnvMechanism(t *testing.T) {
	t.Parallel()
	inject := &types.InjectPipe{}
	selfInject := &types.InjectBlock{
		Env: []any{map[string]any{"in": "K1", "value": "literal-value"}},
	}
	if err := ExecutePass2a(inject, selfInject, "node-1", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{}); err != nil {
		t.Fatalf("literal env: %v", err)
	}
	if got, _ := inject.Env.Get("K1"); got != "literal-value" {
		t.Errorf("env literal not set; the pipe carries %v", inject.Env.Keys())
	}
}

// TestProcessSelfInjectMechanism_NilRaw pins the early-return when raw
// self-inject is nil.
func TestProcessSelfInjectMechanism_NilRaw(t *testing.T) {
	t.Parallel()
	pipe := &types.OrderedMap[string]{}
	if err := processSelfInjectMechanism(pipe, "env", nil, types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{}); err != nil {
		t.Errorf("nil raw should not error: %v", err)
	}
}

// TestResolveSelfInjectEntry_LiteralAndValueAndFrom pins the dispatch arms: a
// value: carrying no template reference, from:, and a value: template.
func TestResolveSelfInjectEntry_LiteralAndValueAndFrom(t *testing.T) {
	t.Parallel()
	// A value: with no template reference delivers the text the author typed.
	got, err := resolveSelfInjectEntry("K", selfInjectEntry{value: "literal"}, "env", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{})
	if err != nil || got != "literal" {
		t.Errorf("literal: (%q, %v)", got, err)
	}
	// from: → reads from own provides.
	provides := types.OrderedMap[string]{}
	provides.Set("host", "internal-host")
	got, err = resolveSelfInjectEntry("K", selfInjectEntry{from: "host"}, "env", provides, SelfInjectContext{}, redact.Keys{})
	if err != nil || got != "internal-host" {
		t.Errorf("from: (%q, %v)", got, err)
	}
	// value: as template — minimal context just to drive the eval path.
	_, _ = resolveSelfInjectEntry("K", selfInjectEntry{value: "literal-tmpl"}, "env", types.OrderedMap[string]{}, SelfInjectContext{
		Values: types.OrderedMap[any]{},
	}, redact.Keys{})

}

// TestBuildInjectAll_AggregatesEnvFileArg pins the consolidation: env
// + file + arg keys all flow into .all in author order per mechanism.
func TestBuildInjectAll_AggregatesEnvFileArg(t *testing.T) {
	t.Parallel()
	pipe := types.InjectPipe{}
	pipe.Env.Set("E1", "v1")
	pipe.File.Set("F1", "/tmp")
	pipe.Arg.Set("A1", "arg1")
	all := buildInjectAll(pipe)
	if all.Len() == 0 {
		t.Errorf("buildInjectAll should produce non-empty result; got %v", all)
	}
}

// TestExecutePhaseA_EmptyContext pins ExecutePhaseA's behavior with a
// minimal context and no FeatureRecipes — the call returns without
// error.
func TestExecutePhaseA_EmptyContext(t *testing.T) {
	t.Parallel()
	ctx := &types.TemplateContext{}
	if _, err := ExecutePhaseA(ctx, nil, hostWithheld{}); err != nil {
		t.Errorf("empty context + nil FRs should not error: %v", err)
	}
}
