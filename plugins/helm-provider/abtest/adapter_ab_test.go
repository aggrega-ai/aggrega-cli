// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// cannedLookup returns a pluginpath.Lookup that resolves every URI to the guest
// bytes b, carrying an explicit memory cap. The standard-Go SDK guest declares a
// linear-memory minimum above the tight platform default (1024 pages); the cap
// here is high enough (4096 pages) that the guest loads rather than trapping,
// which also proves the adapter plumbs the resolved plugin's MaxMemoryPages
// through resolver.PluginSpec into the loader — a load at the default cap would
// trap before render_node runs.
func cannedLookup(b []byte) pluginpath.Lookup {
	return func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{
			Bytes:  b,
			Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096, TimeoutMs: 300000},
		}, nil
	}
}

// TestWasmEngineAdapterRenderByteIdentity proves the host-side wasmengine.Engine
// adapter reproduces the in-tree HelmEngine.Render byte-for-byte when driven
// strictly through the renderer.Engine interface. The adapter projects the node
// onto the provider render wire, loads the guest through the production
// describe-gated loader, invokes render_node, and folds the output back into the
// interface's path→bytes map — the same seam a later change selects in the engine
// dispatch. Both sides receive identical values, provenance, indent, and
// annotations, so a byte-identical match proves the adapter's projection +
// load-describe-invoke path reproduces the in-tree engine exactly.
//
// Cases: the default indent, indent 4 (re-marshals the injected blocks visibly
// wider), and a non-empty-annotations case (the namespace annotation flows
// through the adapter's single-bucket flattening and the guest's bucket merge).
// The cross-case guard proves the indent carried through the adapter is
// observable rather than a no-op.
func TestWasmEngineAdapterRenderByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmBytes, err := os.ReadFile(buildPluginWasm(t))
	if err != nil {
		t.Fatalf("read guest wasm: %v", err)
	}

	repoTestdata := absPath(t, "../../../internal/compiler/renderer/helm/testdata")
	hostChartDir := filepath.Join(repoTestdata, "minimal-chart")

	nodeID := "api"
	// The node's values as they exist in the IR: an ordered map whose numerics
	// are json.Number (the shape the renderer hands the engine). Both the oracle
	// and the adapter receive the same plain values map, so the A/B compares the
	// render path, not a value-typing mismatch introduced by the test.
	valuesOrdered := valuesFromJSON(t, `{"image":"api:2.0.0","replicas":5}`)
	values := orderedToPlain(&valuesOrdered)

	// The candidate is driven strictly through the renderer.Engine interface
	// value — never a concrete method — so the A/B exercises exactly the seam the
	// engine dispatch selects.
	var eng renderer.Engine = wasmengine.New(cannedLookup(wasmBytes), "test://helm-renderer", (&helm.HelmEngine{}).Capabilities(), &wasm.Loader{})

	cases := []struct {
		name        string
		indent      int
		annotations map[string]string
	}{
		{"default-indent", 0, nil},
		{"indent-4", 4, nil},
		{"annotations", 0, map[string]string{"aggrega.ai/namespace": "payments-prod", "team": "payments"}},
	}

	captured := make(map[string]map[string][]byte, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A single node context feeds both sides — the adapter reads the same
			// values, provenance, indent, and annotations the oracle reads.
			nodeCtx := renderer.NodeRenderContext{
				NodeID:      nodeID,
				Source:      hostChartDir,
				YAMLIndent:  tc.indent,
				Annotations: tc.annotations,
			}
			stampOracleProvenance(&nodeCtx)

			oracle, err := (&helm.HelmEngine{}).Render(ctx, values, nodeCtx)
			if err != nil {
				t.Fatalf("oracle Render: %v", err)
			}

			candidate, err := eng.Render(ctx, values, nodeCtx)
			if err != nil {
				t.Fatalf("adapter Render: %v", err)
			}

			assertByteIdentical(t, oracle, candidate)
			captured[tc.name] = candidate
		})
	}

	// The indent must be observable through the adapter, or a byte-identical
	// match at every case proves nothing about the carriage.
	if def, ok := captured["default-indent"]; ok {
		if wide, ok := captured["indent-4"]; ok && byteMapsEqual(def, wide) {
			t.Fatal("indent 4 produced byte-identical output to the default indent through the adapter; the indent carriage is not observable, so the A/B is a no-op")
		}
	}
}

// TestWasmEngineAdapterSynthByteIdentity proves the wasmengine.Engine adapter
// reproduces the in-tree HelmEngine.SynthesizeWorkload byte-for-byte when driven
// strictly through the renderer.WorkloadSynthesizer interface. The adapter
// projects the workload and its per-slot secret provides (built from the same
// template-context projection the in-tree engine uses) onto the provider render
// wire, invokes render_node, and returns the synthesized manifests in emission
// order. Emission order is load-bearing, so the files are compared index-for-
// index. The three synth fixtures exercise the Deployment/secret, all-volume, and
// StatefulSet translation surfaces.
func TestWasmEngineAdapterSynthByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmBytes, err := os.ReadFile(buildPluginWasm(t))
	if err != nil {
		t.Fatalf("read guest wasm: %v", err)
	}

	var synth renderer.WorkloadSynthesizer = wasmengine.New(cannedLookup(wasmBytes), "test://helm-renderer", (&helm.HelmEngine{}).Capabilities(), &wasm.Loader{})

	cases := []struct {
		name   string
		nodeID string
		build  func() (*types.Workload, map[string]types.OrderedMap[string], *types.TemplateContext)
	}{
		{"deployment-with-secrets-and-service", "api", buildDeploymentCase},
		{"all-volume-variants", "store", buildVolumeCase},
		{"statefulset-headless", "postgres", buildStatefulSetCase},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _, tmplCtx := tc.build()

			nodeCtx := renderer.NodeRenderContext{NodeID: tc.nodeID}
			stampOracleProvenance(&nodeCtx)

			// ORACLE — the in-tree engine, fed the natural Workload and the
			// template context it projects into per-slot secret provides.
			oracle, err := (&helm.HelmEngine{}).SynthesizeWorkload(ctx, tmplCtx, w, nodeCtx)
			if err != nil {
				t.Fatalf("oracle SynthesizeWorkload: %v", err)
			}

			// CANDIDATE — the adapter through the WorkloadSynthesizer interface.
			// It builds the workload projection (including the same per-slot secret
			// provides) from the same inputs and synthesizes across the ABI.
			candidate, err := synth.SynthesizeWorkload(ctx, tmplCtx, w, nodeCtx)
			if err != nil {
				t.Fatalf("adapter SynthesizeWorkload: %v", err)
			}

			// Reuse the harness's ordered byte-identity assertion, which compares
			// an EmittedFile oracle to a RenderedFile candidate; the adapter's
			// EmittedFile output maps name→path and body→content one-for-one.
			assertSynthByteIdentical(t, oracle, emittedToRendered(candidate))
		})
	}
}

// emittedToRendered adapts the interface's []EmittedFile to the []RenderedFile
// shape assertSynthByteIdentical compares against, preserving order.
func emittedToRendered(files []renderer.EmittedFile) []renderer.RenderedFile {
	out := make([]renderer.RenderedFile, len(files))
	for i, f := range files {
		out[i] = renderer.RenderedFile{Path: f.Name, Content: f.Body}
	}
	return out
}
