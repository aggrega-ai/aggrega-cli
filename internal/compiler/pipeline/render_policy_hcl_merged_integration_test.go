// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// mergeFormatAssembler is a synthetic merge-boundary provider engine whose
// AssembleBoundary returns a fixed set of merged files, each pairing assembled
// bytes with a producer-declared format (possibly empty). It stands in for a
// real assembling provider guest that declares a format on its merged output, so
// the end-to-end proofs drive RenderResult.Formats through the real assembler-
// merge branch rather than a hand-built map. Its per-node Render emits one
// fragment so the node contributes; the fragment is intermediate (BoundaryAssembly
// "merge") and never written directly — only the declared merged files reach
// RenderResult.Files.
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

// runMergedRender drives the full Step 5 pipeline through a single merge-boundary
// node whose engine assembles the given merged files, and returns the
// RenderResult. RenderResult.Formats is populated by the real assembler-merge
// branch, so a routing proof that feeds Files+Formats into sortedRenderedArtifacts
// exercises the merge→Formats→parser-selection seam end-to-end.
func runMergedRender(t *testing.T, merged map[string]renderer.AssembledFile) *renderer.RenderResult {
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
	factory := func(_ string) (renderer.Engine, error) {
		return &mergeFormatAssembler{merged: merged}, nil
	}
	res, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	return res
}

// artifactEndingWith returns the single projected artifact whose path ends with
// suffix, failing when zero or more than one match — so a proof addresses a
// merged file by the name the assembler emitted, independent of the dispatch-path
// prefix the orchestrator reattaches.
func artifactEndingWith(t *testing.T, arts []analyzer.RenderedArtifact, suffix string) analyzer.RenderedArtifact {
	t.Helper()
	var found analyzer.RenderedArtifact
	matches := 0
	for _, a := range arts {
		if strings.HasSuffix(a.Path, suffix) {
			found = a
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly one artifact ending %q, got %d", suffix, matches)
	}
	return found
}

func formatKeyEndingWith(t *testing.T, formats map[string]string, suffix string) (string, bool) {
	t.Helper()
	for k := range formats {
		if strings.HasSuffix(k, suffix) {
			return k, true
		}
	}
	return "", false
}

// TestRenderPolicyHCL_MergedProducerFormatOverridesExtension is the end-to-end
// merge→Formats→parser-routing proof. A render THROUGH the assembler-merge branch
// emits one merged file at a ".json" path whose producing provider declared "hcl"
// — a format the ".json" extension does not imply. The proof first asserts
// RenderResult.Formats carries "hcl" for that file (the producer format survived
// the merge), then feeds the merged render's Files+Formats into the same
// render-policy projection seam the flat proofs use and asserts the file parsed as
// HCL (tree non-empty, resource block present) — the producer tag beat the
// conflicting ".json" extension across the whole chain. The untagged control on
// the identical path (assembler declared an empty format) is absent from Formats
// and takes the extension to native JSON, leaving its tree empty. That control is
// the non-vacuity isolation: were the producer format ignored, the tagged file
// would route by extension exactly like the control and its tree would be empty.
func TestRenderPolicyHCL_MergedProducerFormatOverridesExtension(t *testing.T) {
	ctx := context.Background()

	// Tagged: the merged file's assembler declared "hcl" though its path is ".json".
	res := runMergedRender(t, map[string]renderer.AssembledFile{
		"resource.json": {Content: []byte(hclArtifact), Format: "hcl"},
	})
	fk, ok := formatKeyEndingWith(t, res.Formats, "resource.json")
	if !ok {
		t.Fatalf("RenderResult.Formats has no entry for the merged resource.json; producer format did not survive the merge (Formats=%v)", res.Formats)
	}
	if res.Formats[fk] != "hcl" {
		t.Fatalf("merged producer format: Formats[%q] = %q, want %q", fk, res.Formats[fk], "hcl")
	}
	t.Logf("merged Formats[%q] = %q (producer format survived the merge)", fk, res.Formats[fk])

	arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), res.Files, res.Formats)
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (merged producer hcl over .json): %v", err)
	}
	art := artifactEndingWith(t, arts, "resource.json")
	if art.Format != "hcl" {
		t.Errorf("projected artifact format: got %q, want %q", art.Format, "hcl")
	}
	if len(art.Tree) == 0 {
		t.Fatalf("merged producer format did not route to the HCL parser: %q carries no parsed tree", art.Path)
	}
	var root map[string]any
	if err := json.Unmarshal(art.Tree, &root); err != nil {
		t.Fatalf("parsed tree is not a JSON object: %v", err)
	}
	if _, ok := root["resource"]; !ok {
		t.Errorf("parsed tree missing the resource block (not parsed as HCL): %s", art.Tree)
	}
	t.Logf("merged .json routed to HCL by producer format — parsed tree: %s", string(art.Tree))

	// Control: the identical path, but the assembler declared no format, so the
	// merge leaves it absent from Formats and the extension selects native JSON,
	// which the host does not pre-parse. Its empty tree isolates the override.
	ctrl := runMergedRender(t, map[string]renderer.AssembledFile{
		"resource.json": {Content: []byte(hclArtifact), Format: ""},
	})
	if _, ok := formatKeyEndingWith(t, ctrl.Formats, "resource.json"); ok {
		t.Errorf("control merged file carries a format entry; expected none (Formats=%v)", ctrl.Formats)
	}
	ctrlArts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), ctrl.Files, ctrl.Formats)
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (control untagged .json): %v", err)
	}
	ctrlArt := artifactEndingWith(t, ctrlArts, "resource.json")
	if ctrlArt.Format != "" {
		t.Errorf("control artifact carries a format %q; expected none", ctrlArt.Format)
	}
	if len(ctrlArt.Tree) != 0 {
		t.Errorf("control untagged .json carries a tree %s; the extension resolves native JSON, which stays unparsed", ctrlArt.Tree)
	}
	t.Logf("control — untagged merged .json takes the extension to native JSON, tree empty (len=%d)", len(ctrlArt.Tree))
}

// TestRenderPolicyHCL_MergedMixedRoutesPerFile proves a render through a merge
// boundary that emits TWO merged files with different declared formats routes
// each by its own producer format, not its extension. The "hcl" file sits at a
// ".json" path (extension would say native JSON, unparsed) yet parses as HCL; the
// "json" file sits at a ".tf" path (extension would say HCL, parsed) yet stays
// native and unparsed. Each file's Formats entry and routing follow its own
// declared format; neither leaks into the other.
func TestRenderPolicyHCL_MergedMixedRoutesPerFile(t *testing.T) {
	ctx := context.Background()
	jsonArtifact := `{"resource":{"aws_kms_key":{"main":{"description":"acme"}}}}`
	res := runMergedRender(t, map[string]renderer.AssembledFile{
		"alpha.json": {Content: []byte(hclArtifact), Format: "hcl"},
		"beta.tf":    {Content: []byte(jsonArtifact), Format: "json"},
	})

	alphaK, ok := formatKeyEndingWith(t, res.Formats, "alpha.json")
	if !ok || res.Formats[alphaK] != "hcl" {
		t.Fatalf("alpha merged format entry missing/wrong: %v", res.Formats)
	}
	betaK, ok := formatKeyEndingWith(t, res.Formats, "beta.tf")
	if !ok || res.Formats[betaK] != "json" {
		t.Fatalf("beta merged format entry missing/wrong: %v", res.Formats)
	}

	arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), res.Files, res.Formats)
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (mixed merged output): %v", err)
	}

	alpha := artifactEndingWith(t, arts, "alpha.json")
	if alpha.Format != "hcl" {
		t.Errorf("alpha artifact format: got %q, want %q", alpha.Format, "hcl")
	}
	if len(alpha.Tree) == 0 {
		t.Fatalf("alpha.json (producer hcl) carries no parsed tree; not routed to the HCL parser")
	}
	var alphaRoot map[string]any
	if err := json.Unmarshal(alpha.Tree, &alphaRoot); err != nil {
		t.Fatalf("alpha tree is not a JSON object: %v", err)
	}
	if _, ok := alphaRoot["resource"]; !ok {
		t.Errorf("alpha tree missing the resource block (not HCL-parsed): %s", alpha.Tree)
	}
	t.Logf("mixed merged — alpha.json routed to HCL by producer format, tree: %s", string(alpha.Tree))

	beta := artifactEndingWith(t, arts, "beta.tf")
	if beta.Format != "json" {
		t.Errorf("beta artifact format: got %q, want %q", beta.Format, "json")
	}
	if len(beta.Tree) != 0 {
		t.Errorf("beta.tf (producer json) carries a tree %s; the json producer format is native and stays unparsed despite the .tf extension", beta.Tree)
	}
	if beta.Content != jsonArtifact {
		t.Errorf("beta.tf content changed: %q", beta.Content)
	}
	t.Logf("mixed merged — beta.tf kept native by producer format, tree empty (len=%d), content verbatim", len(beta.Tree))
}

// TestRenderPolicyHCL_MergedUntaggedExtensionFallback proves the extension
// fallback still governs merged files whose assembler declared no format: a ".tf"
// merged file with an empty producer format is absent from Formats and resolves to
// HCL through the fixed extension table alone (tree non-empty), while a ".json"
// merged file with an empty format takes the extension to native JSON and stays
// unparsed. The producer tier is additive; its absence restores pure extension
// selection.
func TestRenderPolicyHCL_MergedUntaggedExtensionFallback(t *testing.T) {
	ctx := context.Background()
	res := runMergedRender(t, map[string]renderer.AssembledFile{
		"main.tf":     {Content: []byte(hclArtifact), Format: ""},
		"native.json": {Content: []byte(`{"a":1}`), Format: ""},
	})
	if len(res.Formats) != 0 {
		t.Fatalf("untagged merged output populated Formats: %v", res.Formats)
	}

	arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), res.Files, res.Formats)
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (untagged merged output): %v", err)
	}

	tf := artifactEndingWith(t, arts, "main.tf")
	if tf.Format != "" {
		t.Errorf("main.tf carries a producer format %q; expected none — the extension supplies the parser", tf.Format)
	}
	if len(tf.Tree) == 0 {
		t.Fatalf("untagged .tf merged file carries no parsed tree; the extension fallback did not resolve HCL")
	}
	var root map[string]any
	if err := json.Unmarshal(tf.Tree, &root); err != nil {
		t.Fatalf("main.tf tree is not a JSON object: %v", err)
	}
	if _, ok := root["resource"]; !ok {
		t.Errorf("main.tf tree missing the resource block (not HCL-parsed): %s", tf.Tree)
	}
	t.Logf("untagged merged .tf resolved HCL via the extension fallback — tree: %s", string(tf.Tree))

	nj := artifactEndingWith(t, arts, "native.json")
	if len(nj.Tree) != 0 {
		t.Errorf("untagged .json merged file carries a tree %s; the extension resolves native JSON, which stays unparsed", nj.Tree)
	}
	t.Logf("untagged merged .json stayed native via the extension fallback, tree empty (len=%d)", len(nj.Tree))
}

// TestRenderPolicyHCL_MergedRoutingDeterministic proves two independent merged
// renders feed the parser seam to byte-identical projected trees and identical
// producer formats — the deterministic-output guarantee holds across the whole
// merge→Formats→parser-routing chain, not just the raw file bytes.
func TestRenderPolicyHCL_MergedRoutingDeterministic(t *testing.T) {
	ctx := context.Background()
	project := func() []analyzer.RenderedArtifact {
		res := runMergedRender(t, map[string]renderer.AssembledFile{
			"resource.json": {Content: []byte(hclArtifact), Format: "hcl"},
			"native.json":   {Content: []byte(`{"a":1}`), Format: ""},
		})
		arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), res.Files, res.Formats)
		if err != nil {
			t.Fatalf("sortedRenderedArtifacts: %v", err)
		}
		return arts
	}
	first := project()
	second := project()
	if len(first) != len(second) {
		t.Fatalf("artifact count drift across runs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Path != second[i].Path {
			t.Fatalf("path order drift at %d: %q vs %q", i, first[i].Path, second[i].Path)
		}
		if first[i].Format != second[i].Format {
			t.Fatalf("format drift at %q: %q vs %q", first[i].Path, first[i].Format, second[i].Format)
		}
		if string(first[i].Tree) != string(second[i].Tree) {
			t.Fatalf("parsed tree drift at %q:\n first: %s\nsecond: %s", first[i].Path, first[i].Tree, second[i].Tree)
		}
	}
	t.Logf("byte-identical merge→routing across runs: %d artifacts", len(first))
}
