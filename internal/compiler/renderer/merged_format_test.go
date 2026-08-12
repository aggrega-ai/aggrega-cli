// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// mergeFormatAssembler is a synthetic merge-boundary provider engine whose
// AssembleBoundary returns a fixed set of merged files, each pairing assembled
// bytes with a producer-declared format (possibly empty). It stands in for a
// real assembling provider guest that declares a format on its merged output.
// Its per-node Render emits one fragment so the node contributes to the dispatch
// group; the fragment is intermediate (BoundaryAssembly "merge") and is never
// written directly — only the declared merged files reach RenderResult.Files, so
// a test observes exactly the merged output it declared.
type mergeFormatAssembler struct {
	merged map[string]renderer.AssembledFile
}

func (a *mergeFormatAssembler) Render(_ context.Context, _ map[string]any, _ renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{{Path: "fragment.yaml", Content: []byte("x: 1\n")}}, nil
}

func (a *mergeFormatAssembler) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (a *mergeFormatAssembler) BoundaryAssembly() string { return "merge" }

func (a *mergeFormatAssembler) AssembleBoundary(_ context.Context, _ renderer.BoundaryInfo) (map[string]renderer.AssembledFile, bool, error) {
	out := map[string]renderer.AssembledFile{}
	for k, v := range a.merged {
		out[k] = v
	}
	return out, false, nil
}

// runMergeFormatRender drives the full Step 5 pipeline through a single merge-
// boundary node whose engine assembles the given merged files. It returns the
// RenderResult so a test reads each file's producer-declared format off its
// RenderResult.Files record as populated by the real assembler-merge branch —
// not a hand-built map.
func runMergeFormatRender(t *testing.T, merged map[string]renderer.AssembledFile) *renderer.RenderResult {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("svc", types.IRNode{Component: "core/api", BoundaryPath: "default"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "merge-app"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
		ProviderVersion: func(ref string) string {
			if i := strings.LastIndex(ref, ":"); i >= 0 {
				return ref[i+1:]
			}
			return ""
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "merge-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "merge:1.0.0", Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	factory := func(_ string) (renderer.Provider, error) {
		return &mergeFormatAssembler{merged: merged}, nil
	}
	res, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	return res
}

// fileKeyEndingWith returns the single RenderResult.Files key whose path ends
// with suffix, failing when zero or more than one match — so a test addresses a
// merged file by the name the assembler emitted, independent of the dispatch-
// path prefix the orchestrator reattaches.
func fileKeyEndingWith(t *testing.T, files map[string]renderer.AssembledFile, suffix string) string {
	t.Helper()
	var found string
	matches := 0
	for k := range files {
		if strings.HasSuffix(k, suffix) {
			found = k
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly one file ending %q, got %d (files: %v)", suffix, matches, keysOf(files))
	}
	return found
}

func keysOf(files map[string]renderer.AssembledFile) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	return out
}

// TestRenderAll_MergedProducerFormat_PopulatesFormats proves a render THROUGH the
// assembler-merge branch carries the producer-declared format on the merged
// file's RenderResult.Files record — the format survives the merge boundary bound
// to its file. The merged path ends ".json" while the declared format is "hcl",
// so the value cannot have come from an extension inference.
func TestRenderAll_MergedProducerFormat_PopulatesFormats(t *testing.T) {
	t.Parallel()
	res := runMergeFormatRender(t, map[string]renderer.AssembledFile{
		"resource.json": {Content: []byte("region = \"eu-west-1\"\n"), Format: "hcl"},
	})
	key := fileKeyEndingWith(t, res.Files, "resource.json")
	t.Logf("merged file key: %s", key)
	if got := res.Files[key].Format; got != "hcl" {
		t.Fatalf("producer format did not survive the merge: Files[%q].Format = %q, want %q", key, got, "hcl")
	}
	t.Logf("Files[%q].Format = %q (producer format survived the merge, format != .json extension)", key, res.Files[key].Format)
}

// TestRenderAll_MergedMixedFormats_CarryPerFile proves two merged files with
// different declared formats each carry their own format through the merge on
// their RenderResult.Files records — neither leaks into the other. One path's
// extension disagrees with its declared format in each direction, so each record
// proves the producer format, not the extension, supplied it.
func TestRenderAll_MergedMixedFormats_CarryPerFile(t *testing.T) {
	t.Parallel()
	res := runMergeFormatRender(t, map[string]renderer.AssembledFile{
		"alpha.json": {Content: []byte("region = \"eu\"\n"), Format: "hcl"},
		"beta.tf":    {Content: []byte(`{"a":1}`), Format: "json"},
	})
	alpha := fileKeyEndingWith(t, res.Files, "alpha.json")
	beta := fileKeyEndingWith(t, res.Files, "beta.tf")
	if got := res.Files[alpha].Format; got != "hcl" {
		t.Errorf("alpha merged format: Files[%q].Format = %q, want %q", alpha, got, "hcl")
	}
	if got := res.Files[beta].Format; got != "json" {
		t.Errorf("beta merged format: Files[%q].Format = %q, want %q", beta, got, "json")
	}
	t.Logf("mixed merge — Files[%q].Format=%q, Files[%q].Format=%q", alpha, res.Files[alpha].Format, beta, res.Files[beta].Format)
}

// TestRenderAll_MergedUntaggedFormat_AbsentFromFormats proves a merged file whose
// assembler declared an EMPTY format is present in RenderResult.Files with an
// empty Format on its record — the record carries a format only when the producer
// declared one, so a consumer falls back to extension selection.
func TestRenderAll_MergedUntaggedFormat_AbsentFromFormats(t *testing.T) {
	t.Parallel()
	res := runMergeFormatRender(t, map[string]renderer.AssembledFile{
		"main.tf": {Content: []byte("region = \"eu\"\n"), Format: ""},
	})
	key := fileKeyEndingWith(t, res.Files, "main.tf")
	if got := res.Files[key].Format; got != "" {
		t.Fatalf("untagged merged file carries a format Files[%q].Format = %q; expected empty", key, got)
	}
	t.Logf("untagged merged file %q present in Files with empty Format (extension fallback applies downstream)", key)
}

// TestRenderAll_MergedFormats_Deterministic proves two renders through the same
// merge-boundary assembler produce byte-identical file content AND identical
// per-file formats — the deterministic-output guarantee holds for the content and
// the format metadata carried on each record.
func TestRenderAll_MergedFormats_Deterministic(t *testing.T) {
	t.Parallel()
	build := func() *renderer.RenderResult {
		return runMergeFormatRender(t, map[string]renderer.AssembledFile{
			"alpha.json": {Content: []byte("region = \"eu\"\n"), Format: "hcl"},
			"beta.tf":    {Content: []byte(`{"a":1}`), Format: "json"},
			"plain.txt":  {Content: []byte("no format\n"), Format: ""},
		})
	}
	a := build()
	b := build()
	if len(a.Files) != len(b.Files) {
		t.Fatalf("Files count drift: %d vs %d", len(a.Files), len(b.Files))
	}
	for p, af := range a.Files {
		if !bytes.Equal(af.Content, b.Files[p].Content) {
			t.Fatalf("non-deterministic Files content at %q", p)
		}
		if af.Format != b.Files[p].Format {
			t.Fatalf("non-deterministic Files format at %q: %q vs %q", p, af.Format, b.Files[p].Format)
		}
	}
	t.Logf("byte-identical across runs: %d files (content + format)", len(a.Files))
}
