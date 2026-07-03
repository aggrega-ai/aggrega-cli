// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestWasmEngineAssemblerAssembleByteIdentity proves the host-side
// wasmengine.AssemblerEngine reproduces the in-tree ComposeEngine.AssembleBoundary
// byte-for-byte — the assembled docker-compose.yaml AND the nest-per-node signal —
// when both consume the same host renderer.BoundaryInfo. The candidate projects
// the BoundaryInfo onto the assemble_boundary wire, loads the freshly-built guest
// through the production describe-gated loader, invokes the export, and folds the
// guest's output back into the BoundaryAssembler's (files, nestPerNode) return.
//
// The multi-node dispatch group exercises the three carriers the wire extension
// added, so a candidate that dropped any of them on the wire would diverge from
// the oracle:
//
//   - depends_on inference: an IR edge web -> db must surface as a depends_on on
//     the web service (edges carried in AssembleBoundaryInput.edges).
//   - per-node network derivation: web and db carry distinct aggrega.ai/namespace
//     annotations, so they land on distinct networks (annotations carried per
//     rendered node).
//   - the boundary_path fallback: cache carries no namespace annotation, so its
//     network is derived from boundary_path (carried in
//     AssembleBoundaryInput.boundary_path).
//
// The oracle-content assertions below guard non-vacuity: they fail if the oracle
// itself stopped producing depends_on / the derived networks, which would make the
// byte-identity comparison prove nothing about the wire carriage.
func TestWasmEngineAssemblerAssembleByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmBytes, err := os.ReadFile(buildPluginWasm(t))
	if err != nil {
		t.Fatalf("read guest wasm: %v", err)
	}

	// A three-node dispatch group. Service names equal node ids (the compose
	// synthesiser names the main service after the node), so the web -> db edge's
	// endpoints match contributing service names and infer a depends_on.
	info := renderer.BoundaryInfo{
		BoundaryPath:  "shared-stage",
		AggregateName: "shop",
		IntermediatesByNode: map[string][]renderer.EmittedFile{
			"web":   synthFragment(t, "web", assembleNodeWorkload("web:1.0", 8080)),
			"db":    synthFragment(t, "db", assembleNodeWorkload("postgres:15", 5432)),
			"cache": synthFragment(t, "cache", assembleNodeWorkload("redis:7", 6379)),
		},
		NodeAnnotations: map[string]map[string]string{
			"web":   {renderer.AnnotationNamespace: "frontend-net"},
			"db":    {renderer.AnnotationNamespace: "backend-net"},
			"cache": {}, // no namespace annotation -> network derived from boundary_path
		},
		NodeComponent:          map[string]string{"web": "core/web-service", "db": "core/database", "cache": "core/cache"},
		ContributingBoundaries: []string{"shared-stage"},
		Edges:                  []renderer.IREdgeInfo{{Source: "web", Target: "db"}},
		YAMLIndent:             2,
	}

	// ORACLE — the in-tree compose engine's BoundaryAssembler.
	oracle, oracleNest, err := (&compose.ComposeEngine{}).AssembleBoundary(ctx, info)
	if err != nil {
		t.Fatalf("oracle AssembleBoundary: %v", err)
	}

	// Non-vacuity guards on the oracle output: if these features vanish, the
	// byte-identity comparison below proves nothing about the wire carriage.
	body := string(oracle[compose.AssembledOutputFile])
	if body == "" {
		t.Fatalf("oracle produced no %s", compose.AssembledOutputFile)
	}
	for _, want := range []string{"depends_on", "frontend-net:", "backend-net:", "shared-stage:"} {
		if !strings.Contains(body, want) {
			t.Fatalf("oracle output missing %q — the A/B would be vacuous:\n%s", want, body)
		}
	}

	// CANDIDATE — the AssemblerEngine, which internally wire-round-trips the same
	// BoundaryInfo through the freshly-built guest.
	asm := wasmengine.NewAssembler(cannedLookup(wasmBytes), "test://compose-renderer", &compose.ComposeEngine{}, &wasm.Loader{})
	candidate, candNest, err := asm.AssembleBoundary(ctx, info)
	if err != nil {
		t.Fatalf("candidate AssembleBoundary: %v", err)
	}

	assertByteIdentical(t, oracle, candidate)

	if oracleNest != candNest {
		t.Fatalf("nestPerNode differs: oracle %v, candidate %v", oracleNest, candNest)
	}
	if candNest {
		t.Errorf("compose assembles flat; nestPerNode must be false, got %v", candNest)
	}
}

// synthFragment synthesises a single node's Intermediate compose fragment through
// the in-tree engine, so the A/B feeds both sides real render-path fragments.
func synthFragment(t *testing.T, nodeID string, w *types.Workload) []renderer.EmittedFile {
	t.Helper()
	nodeCtx := renderer.NodeRenderContext{NodeID: nodeID}
	files, err := (&compose.ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
	if err != nil {
		t.Fatalf("synth fragment %q: %v", nodeID, err)
	}
	return files
}

// assembleNodeWorkload builds a minimal single-container workload whose main
// service the synthesiser names after the node id.
func assembleNodeWorkload(image string, port int) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: image}
	c.Ports.Set("p", port)
	w.Containers.Set("main", c)
	s := types.ServiceSpec{Type: "ClusterIP"}
	s.Ports.Set("p", port)
	w.Services.Set("svc", s)
	return w
}
