// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nestFormatAssembler is a synthetic provider that places each contributor's
// files under a directory of its own and composes a root file beside them, as a
// provider vendoring one module per component does. Its per-node render declares
// a format on one file and none on the other, so a test reads what the placement
// carried for each.
type nestFormatAssembler struct{}

func (nestFormatAssembler) Render(_ context.Context, _ map[string]any, _ renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{
		{Path: "main.tf", Content: []byte("resource \"null_resource\" \"a\" {}\n"), Format: "hcl"},
		{Path: "notes.txt", Content: []byte("no format\n")},
	}, nil
}

func (nestFormatAssembler) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (nestFormatAssembler) BoundaryAssembly() string { return "nest" }

// NestSegment names the directory this provider's contributors sit under.
func (nestFormatAssembler) NestSegment() string { return "modules" }

func (nestFormatAssembler) AssembleBoundary(_ context.Context, _ renderer.BoundaryInfo) (map[string]renderer.AssembledFile, error) {
	return map[string]renderer.AssembledFile{
		"root.tf": {Content: []byte("module \"svc\" {}\n"), Format: "hcl"},
	}, nil
}

// TestRenderAll_NestedPerNodeFiles_KeepProducerFormat proves a contributor's own
// file, placed under the contributor's directory by a nesting provider, carries the
// format the provider declared for it on its RenderResult.Files record — the same
// format a flat placement carries — and that a file declared with none stays
// without one. The key is required to sit under the nesting segment, so the record
// read is the one the nested placement produced rather than a flat one.
func TestRenderAll_NestedPerNodeFiles_KeepProducerFormat(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("svc", types.IRNode{Component: "core/api", BoundaryPath: "default"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "nest-app"},
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
				Metadata: types.RecipeMetadata{Name: "nest-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "nest:1.0.0", Source: "./module"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	factory := func(_ string) (renderer.Provider, error) { return nestFormatAssembler{}, nil }

	res, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	declared := fileKeyEndingWith(t, res.Files, "/main.tf")
	if !strings.Contains(declared, "modules/") {
		t.Fatalf("the contributor's file %q is not under the nesting segment, so the nested placement was not exercised (files: %v)",
			declared, keysOf(res.Files))
	}
	if got := res.Files[declared].Format; got != "hcl" {
		t.Errorf("Files[%q].Format = %q; want the producer-declared %q", declared, got, "hcl")
	}
	undeclared := fileKeyEndingWith(t, res.Files, "/notes.txt")
	if got := res.Files[undeclared].Format; got != "" {
		t.Errorf("Files[%q].Format = %q; a file declared with no format must carry none", undeclared, got)
	}
}
