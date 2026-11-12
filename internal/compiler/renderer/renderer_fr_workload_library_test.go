// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// A FeatureRecipe workload flows through the same Phase B machinery as a base
// Recipe (via a synthetic Recipe), so a workload library `$import:
// <Library>#/workloads/<entry>` resolves against the WorkloadLibrary set the
// render input carries — provided that set reaches the FR synth state. These
// proofs run the real RenderAll pipeline: the library-imported FR workload
// renders byte-identically to the equivalent inline FR workload (Invariant 8),
// and an unknown library halts loudly through the same path. Both fail if the
// FR synth state stops receiving the library set.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// frWorkloadBody carries a Go-template expression so the proof exercises the
// full resolve → EvalTemplate → synthesize path, not a static string. `printf`
// is a Go-template builtin, so no external values context is needed for the
// expression to evaluate.
const frWorkloadBody = `apiVersion: aggrega.ai/v1beta1
kind: Workload
containers:
  main:
    image: app:{{ printf "%d.%d" 1 0 }}
`

func frWorkloadLibraryFixture(name, entry, body string) types.WorkloadLibrary {
	wl := types.WorkloadLibrary{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "WorkloadLibrary",
		Metadata:   types.WorkloadLibraryMetadata{Name: name, Version: "1.0.0"},
	}
	wl.Spec.Workloads.Set(entry, types.WorkloadEntry{Body: body})
	return wl
}

// renderFRWorkload renders a component-scope FeatureRecipe whose workload is w,
// with the given WorkloadLibrary set on the render input, and returns the
// path→bytes map RenderAll emits. It drives the real orchestrator → phaseB6 →
// renderFeatureRecipeFiles path, so w's library `$import` resolves only if the
// FR synth state receives the library set.
func renderFRWorkload(t *testing.T, w types.WorkloadTemplate, wlibs map[string]types.WorkloadLibrary) (map[string][]byte, error) {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", BoundaryPath: "backend"})

	fvm := func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("frwl", types.IRFeatureEntry{Type: "core/frwl"})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		return outer
	}()

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec:     types.IRSpec{Nodes: nodes, ValuesMapping: types.OrderedMap[json.RawMessage]{}, FeatureValuesMapping: fvm},
		},
		FeatureOrder:      ir.FeatureOrder{"api": {"frwl"}},
		ResolveResult:     &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		WorkloadLibraries: wlibs,
		CatalogLookup: resolver.CatalogLookup{
			Component: func(string) (types.Component, bool) { return types.Component{}, false },
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI == "core/frwl:1.0.0" || typeURI == "core/frwl" {
					return types.Feature{}, true
				}
				return types.Feature{}, false
			},
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec:     types.RecipeSpec{Workload: types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1beta1\nkind: Workload\ncontainers:\n  main:\n    image: api:latest\n"}},
			}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft == "core/frwl" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "frwl-helm"},
					Spec:     types.FeatureRecipeSpec{Capability: "helm", Implements: "core/frwl:1.0.0", Workload: w},
				}, true
			}
			return nil, false
		},
	}
	res, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		return nil, err
	}
	return res.FileBytes(), nil
}

// TestFRWorkloadLibraryImport_ByteIdenticalToInline is the load-bearing
// determinism proof (Invariant 8): a FeatureRecipe workload imported from a WorkloadLibrary
// renders byte-identically to the equivalent inline FR workload. The library
// indirection collapses in phaseB2 (the resolver returns the entry body verbatim,
// then the SAME synthesis runs), so equal emitted bytes prove the relax is
// exercised end-to-end. Without the FR synth-state threading the library variant
// would surface library-not-found; the equality below therefore also guards the
// threading against regression.
func TestFRWorkloadLibraryImport_ByteIdenticalToInline(t *testing.T) {
	t.Parallel()

	inline, err := renderFRWorkload(t, types.WorkloadTemplate{Inline: frWorkloadBody}, nil)
	if err != nil {
		t.Fatalf("inline FR workload render: %v", err)
	}

	wlibs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": frWorkloadLibraryFixture("BaseWorkloads", "Deployment", frWorkloadBody),
	}
	library, err := renderFRWorkload(t, types.WorkloadTemplate{Import: "BaseWorkloads#/workloads/Deployment"}, wlibs)
	if err != nil {
		t.Fatalf("library-import FR workload render: %v", err)
	}

	inlineBytes, err := yaml.Marshal(inline)
	if err != nil {
		t.Fatalf("marshal inline file map: %v", err)
	}
	libraryBytes, err := yaml.Marshal(library)
	if err != nil {
		t.Fatalf("marshal library file map: %v", err)
	}
	if string(inlineBytes) != string(libraryBytes) {
		t.Errorf("library-imported FR workload is not byte-identical to inline:\n--- inline ---\n%s\n--- library ---\n%s", inlineBytes, libraryBytes)
	}
	// Sanity: the FR actually emitted files (the render was not a silent no-op),
	// so the byte-identity is over real rendered content.
	if len(library) == 0 {
		t.Fatal("library FR render emitted no files; byte-identity would be vacuous")
	}
}

// TestFRWorkloadLibraryImport_UnknownLibraryHalts proves the FR workload library
// path halts loudly through RenderAll when the referenced library is absent —
// the same not-found sentinel the base-Recipe path surfaces, reached because the
// FR synth state receives the (here empty of the referenced entry) library set.
func TestFRWorkloadLibraryImport_UnknownLibraryHalts(t *testing.T) {
	t.Parallel()

	wlibs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": frWorkloadLibraryFixture("BaseWorkloads", "Deployment", frWorkloadBody),
	}
	_, err := renderFRWorkload(t, types.WorkloadTemplate{Import: "Absent#/workloads/Deployment"}, wlibs)
	if err == nil {
		t.Fatal("unknown WorkloadLibrary at featurerecipe.workload must halt; got nil")
	}
	if !errors.Is(err, renderer.ErrWorkloadLibraryNotFound()) {
		t.Errorf("error = %v; want ErrWorkloadLibraryNotFound", err)
	}
}
