// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// versionedAssembler is a synthetic provider engine that both renders per node
// and assembles the dispatch group, standing in for a version-distinct provider-
// rendering plugin. Its Render stamps the shared provenance labels onto a manifest
// (mirroring what the real engines do via BuildAggregaProvenanceLabels), so the
// orchestrator-populated provider identity flows into the emitted output. Its
// AssembleBoundary records — through the shared assembledBy pointer — which
// version's engine the orchestrator elected to run, and echoes the group's
// intermediate fragments into the assembled output so their labels are observable.
type versionedAssembler struct {
	version       string
	assembledBy   *string
	assembleCount *int
}

func (a *versionedAssembler) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ")
	b.WriteString(nodeCtx.NodeID)
	b.WriteString("\n  labels:\n")
	labels := renderer.BuildAggregaProvenanceLabels(nodeCtx)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys) // real engines emit sorted labels for byte-stable output
	for _, k := range keys {
		fmt.Fprintf(&b, "    %s: %q\n", k, labels[k])
	}
	return []renderer.RenderedFile{{Path: "manifest.yaml", Content: []byte(b.String())}}, nil
}

func (a *versionedAssembler) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// BoundaryAssembly marks this synthetic provider as a fragment-merging
// boundary-assembler, so the orchestrator classifies its per-node output
// Intermediate=true and routes it to AssembleBoundary — the equivalent of the
// production assembling guests' self-report.
func (a *versionedAssembler) BoundaryAssembly() string { return "merge" }

func (a *versionedAssembler) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string][]byte, bool, error) {
	*a.assembledBy = a.version
	*a.assembleCount++
	out := map[string][]byte{}
	for nodeID, files := range info.IntermediatesByNode {
		for _, f := range files {
			out["charts/"+nodeID+"/"+f.Name] = f.Body
		}
	}
	return out, false, nil
}

// TestRenderAll_MixedProviderVersionGroup_AssemblesAtHighest proves the operator
// ruling for a boundary whose components pin two versions of one provider: they
// group as ONE dispatch (grouping is by bare provider), the build does not halt,
// the group assembles through the HIGHEST-version engine (not the first seen),
// each component's output carries the aggrega.ai/provider and
// aggrega.ai/provider-version labels with the right values, and the whole render
// is byte-stable across repeated runs.
func TestRenderAll_MixedProviderVersionGroup_AssemblesAtHighest(t *testing.T) {
	t.Parallel()

	// Two components in one boundary. The lower version sorts first in the
	// deterministic node walk, so a first-seen assembler election would pick it —
	// the assertion below that the HIGHEST version assembled is what distinguishes
	// the ruled behaviour from the previous greedy-first behaviour.
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api-v1", types.IRNode{Component: "core/api", BoundaryPath: "default"})
	nodes.Set("api-v2", types.IRNode{Component: "core/api", BoundaryPath: "default"})

	providerByNode := map[string]string{
		"api-v1": "compose:1.0.0",
		"api-v2": "compose:2.0.0",
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "compose-app"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
		// Report the resolved version as the pin's version segment — the synthetic
		// analogue of the hybrid factory's provider→plugin resolution.
		ProviderVersion: func(ref string) string {
			if i := strings.LastIndex(ref, ":"); i >= 0 {
				return ref[i+1:]
			}
			return ""
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(instanceName, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: providerByNode[instanceName], Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	run := func() (map[string][]byte, string, int) {
		var assembledBy string
		var assembleCount int
		factory := func(providerRef string) (renderer.Engine, error) {
			ver := ""
			if i := strings.LastIndex(providerRef, ":"); i >= 0 {
				ver = providerRef[i+1:]
			}
			return &versionedAssembler{version: ver, assembledBy: &assembledBy, assembleCount: &assembleCount}, nil
		}
		res, err := renderer.RenderAll(context.Background(), input, lookup, factory)
		if err != nil {
			t.Fatalf("RenderAll: %v", err)
		}
		return res.Files, assembledBy, assembleCount
	}

	files, assembledBy, assembleCount := run()

	// (a) single grouped output: exactly one AssembleBoundary call for the group.
	if assembleCount != 1 {
		t.Fatalf("AssembleBoundary called %d times; want exactly 1 (both versions must collapse into one dispatch group)", assembleCount)
	}

	// (c) the HIGHEST version's engine assembled.
	if assembledBy != "2.0.0" {
		t.Fatalf("group assembled by version %q; want the highest version 2.0.0", assembledBy)
	}

	// (d) each component's provider labels appear on its emitted manifest.
	dump := allBodies(files)
	for _, want := range []string{
		`aggrega.ai/provider: "compose"`,
		`aggrega.ai/provider-version: "1.0.0"`,
		`aggrega.ai/provider-version: "2.0.0"`,
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("rendered output missing %q\n--- output ---\n%s", want, dump)
		}
	}

	// (e) byte-stable across repeated runs.
	files2, _, _ := run()
	if len(files) != len(files2) {
		t.Fatalf("file-count drift across runs: %d vs %d", len(files), len(files2))
	}
	for p, c := range files {
		if !bytes.Equal(c, files2[p]) {
			t.Fatalf("non-deterministic output at %q across runs", p)
		}
	}
}

func allBodies(files map[string][]byte) string {
	var b strings.Builder
	for p, c := range files {
		b.WriteString(p)
		b.WriteString(":\n")
		b.Write(c)
		b.WriteString("\n")
	}
	return b.String()
}
