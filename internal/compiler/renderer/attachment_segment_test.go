// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The directory an attached Feature's files sit under is the PROVIDER's to
// declare, and these are the proofs that the declaration is read rather than
// merely carried.
//
// Why it cannot be derived from anything the host already holds: two providers
// that combine their contributors identically can need opposite answers. One
// whose target reads only the files sitting directly in a directory needs an
// attachment's files beside the ones already there; one whose target discovers
// its units in a named child directory needs them inside it. Both declare the
// same assembly mode, so the host has nothing to tell them apart by — which is
// why the answer is declared and not inferred.
//
// Every case below drives the real RenderAll over a synthetic provider, so what
// is proven is the emitted path rather than the value of a field.

// segmentEngine is a synthetic flat-output provider that renders one fixed file
// per node and declares the directory an attached Feature's files sit under.
//
// Flat output is the shape that reaches the placement under test: a provider
// that folds its contributors into one document consumes an attachment's output
// as fragments instead of placing it, so the directory would never be composed.
type segmentEngine struct {
	attachmentSegment string
}

func (e *segmentEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{{
		Path:    "manifest.yaml",
		Content: []byte("node: " + nodeCtx.NodeID + "\n"),
	}}, nil
}

func (e *segmentEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (e *segmentEngine) BoundaryAssembly() string { return "none" }

// AttachmentSegment is the declaration under test. An empty value declares none,
// which is the shape every shipped provider has and the one the default-path
// proof drives.
func (e *segmentEngine) AttachmentSegment() string { return e.attachmentSegment }

// The corpus every case below drives: one component with a Feature attached to
// it and a second Feature attached to the boundary around it, so both attachment
// scopes pass through the same provider in one run.
//
// Both scopes in one corpus is deliberate. They compose different coordinates
// onto the directory — a component-scope attachment names its host as well as
// its instance, a boundary-scope one sits inside the boundary already — so a
// proof over one scope says nothing about the other, and the directory is the
// one thing they must agree on.
const (
	segmentHost             = "api"
	segmentComponentFeature = "mon"
	segmentBoundaryFeature  = "aux"
	segmentComponentType    = "core/mon:1.0.0"
	segmentBoundaryType     = "core/aux:1.0.0"
	segmentBoundary         = "cluster"
	segmentProvider         = "segprov"
)

// runSegmentRender drives RenderAll over that corpus through a provider
// declaring segment, and returns the emitted paths in path order.
func runSegmentRender(t *testing.T, segment string) ([]string, error) {
	t.Helper()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(segmentHost, types.IRNode{Component: "core/api", BoundaryPath: segmentBoundary})

	// Both attachments are listed against the node, and the boundary-scope one is
	// routed to its own scope by the ExecutionScope its FeatureRecipe declares
	// rather than by where it is listed — which is how the render reaches a
	// boundary-scope attachment at all.
	componentFeatures := types.OrderedMap[types.IRFeatureEntry]{}
	componentFeatures.Set(segmentComponentFeature, types.IRFeatureEntry{Type: segmentComponentType})
	componentFeatures.Set(segmentBoundaryFeature, types.IRFeatureEntry{Type: segmentBoundaryType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(segmentHost, componentFeatures)

	boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
	boundaryFeatures.Set(segmentBoundaryFeature, types.BoundaryFeature{Type: segmentBoundaryType, Infra: types.OrderedMap[any]{}})
	boundary := types.Boundary{
		Directory:  "dist/" + segmentBoundary,
		Features:   boundaryFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	boundary.Components.Set(segmentHost, types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(segmentBoundary, boundary)

	boundaryProj := types.OrderedMap[types.IRBoundaryFeature]{}
	boundaryProj.Set(segmentBoundaryFeature, types.IRBoundaryFeature{Type: segmentBoundaryType, Infra: types.OrderedMap[any]{}})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set(segmentBoundary, boundaryProj)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "attachment-segment-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{segmentHost: []string{segmentComponentFeature, segmentBoundaryFeature}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "attachment-segment-app"}},
			Release:   &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				segmentHost: {BoundaryPath: segmentBoundary, Directory: "dist/" + segmentBoundary},
			},
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/api" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: segmentProvider, Implements: "core/api", Source: "./chart"},
			}, true
		},
		// Two FeatureRecipes, one per attachment scope, because the scope is
		// declared on the FeatureRecipe rather than on the attachment: one
		// FeatureRecipe cannot be attached at both.
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			switch {
			case strings.HasPrefix(featureType, "core/mon"):
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "mon-fr", Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability:    segmentProvider,
						Implements:    segmentComponentType,
						Source:        "./chart",
						ValuesMapping: types.ValuesMapping{Inline: `{}`},
					},
				}, true
			case strings.HasPrefix(featureType, "core/aux"):
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability:     segmentProvider,
						Implements:     segmentBoundaryType,
						ExecutionScope: "boundary",
						Source:         "./chart",
						ValuesMapping:  types.ValuesMapping{Inline: `{}`},
					},
				}, true
			}
			return nil, false
		},
	}

	engine := &segmentEngine{attachmentSegment: segment}
	factory := func(_ string) (renderer.Provider, error) { return engine, nil }
	res, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		return nil, err
	}
	return sortedKeys(res.Files), nil
}

// TestRenderAll_AttachmentSegmentDeclared_ReachesBothScopes is the declared-path
// proof, and it runs the declared and the undeclared shapes side by side so the
// difference between naming a directory and naming none is that directory and
// nothing else: the undeclared run's paths, with the default directory
// substituted, must equal the declared run's exactly.
//
// The declared value differs from the host's default on purpose. A provider
// naming the default's own word would emit paths indistinguishable from naming
// nothing, so such a proof would pass whether or not the declaration was read.
func TestRenderAll_AttachmentSegmentDeclared_ReachesBothScopes(t *testing.T) {
	t.Parallel()

	declared, err := runSegmentRender(t, "addons")
	if err != nil {
		t.Fatalf("a provider declaring a directory must render: %v", err)
	}
	want := []string{
		"dist/cluster/segprov/addons/" + segmentHost + "/" + segmentComponentFeature + "/manifest.yaml",
		"dist/cluster/segprov/addons/" + segmentBoundaryFeature + "/manifest.yaml",
		"dist/cluster/segprov/manifest.yaml",
	}
	if strings.Join(declared, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the declared directory did not reach the emitted paths\nwant:\n%s\ngot:\n%s",
			strings.Join(want, "\n"), strings.Join(declared, "\n"))
	}

	undeclared, err := runSegmentRender(t, "")
	if err != nil {
		t.Fatalf("the undeclared companion run must render: %v", err)
	}
	rewritten := make([]string, 0, len(undeclared))
	for _, k := range undeclared {
		rewritten = append(rewritten, strings.Replace(k, "/features/", "/addons/", 1))
	}
	if strings.Join(rewritten, "\n") != strings.Join(declared, "\n") {
		t.Errorf("declaring a directory changed more than the directory\ndeclared:\n%s\nundeclared, directory substituted:\n%s",
			strings.Join(declared, "\n"), strings.Join(rewritten, "\n"))
	}
}

// TestRenderAll_AttachmentSegmentAbsent_KeepsTheHostDefault is the additive
// half, and it is what makes the declaration shippable without moving a single
// existing provider's output: a provider declaring nothing emits exactly what it
// emitted before the declaration existed.
//
// The expected paths are written as literals rather than read from the host's
// default, because reading the default would make the assertion follow a change
// to it instead of catching one.
func TestRenderAll_AttachmentSegmentAbsent_KeepsTheHostDefault(t *testing.T) {
	t.Parallel()

	got, err := runSegmentRender(t, "")
	if err != nil {
		t.Fatalf("a provider declaring no directory must render: %v", err)
	}
	want := []string{
		"dist/cluster/segprov/features/" + segmentHost + "/" + segmentComponentFeature + "/manifest.yaml",
		"dist/cluster/segprov/features/" + segmentBoundaryFeature + "/manifest.yaml",
		"dist/cluster/segprov/manifest.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("an undeclared provider's attachment layout moved\nwant:\n%s\ngot:\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// TestRenderAll_AttachmentSegmentOutsideOneDirectoryHalts is the bound firing.
// The host composes the attachment's own coordinates into the declared
// directory, so a value that is not a single directory name would place those
// files outside the position the attachment rendered into. The refusal has to
// name the provider that declared it and the value itself — a halt naming
// neither leaves an author with nothing to fix.
func TestRenderAll_AttachmentSegmentOutsideOneDirectoryHalts(t *testing.T) {
	t.Parallel()

	for _, segment := range []string{
		"a/b",             // a path rather than a directory
		"/features",       // a leading separator
		"features/",       // a trailing separator
		"..",              // the parent directory
		".",               // the current directory
		"../../etc",       // a traversal out of the position
		`sub\dir`,         // the other platform's separator
		"a/../../../root", // a traversal disguised inside a path
	} {
		t.Run(segment, func(t *testing.T) {
			t.Parallel()

			got, err := runSegmentRender(t, segment)
			if err == nil {
				t.Fatalf("expected a halt for %q, got files: %v", segment, got)
			}
			if !errors.Is(err, renderer.ErrAttachmentSegmentNotOneDirectory()) {
				t.Fatalf("the render failed for a different reason than the bound: %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, fmt.Sprintf("provider %q", segmentProvider)) {
				t.Errorf("refusal does not name the provider\ngot: %s", msg)
			}
			// Compared against the quoted rendering rather than the raw value: the
			// diagnostic quotes what it names, so a value carrying a backslash
			// reaches the reader escaped and asserting on the raw bytes would fail
			// on a message that names it correctly.
			if !strings.Contains(msg, fmt.Sprintf("%q", segment)) {
				t.Errorf("refusal does not name the value %q\ngot: %s", segment, msg)
			}
		})
	}
}

// TestRenderAll_AttachmentSegmentBoundAcceptsAnOrdinaryDirectoryName is the
// counterpart that keeps the bound from being satisfiable by refusing
// everything. A halt on any of these would reject a legal declaration.
func TestRenderAll_AttachmentSegmentBoundAcceptsAnOrdinaryDirectoryName(t *testing.T) {
	t.Parallel()

	for _, segment := range []string{"charts", "modules", "addons", "my.extras", "sub_units", "v2"} {
		t.Run(segment, func(t *testing.T) {
			t.Parallel()

			got, err := runSegmentRender(t, segment)
			if err != nil {
				t.Fatalf("%q is a single directory name and must be accepted: %v", segment, err)
			}
			want := "dist/cluster/" + segmentProvider + "/" + segment + "/" + segmentHost + "/" + segmentComponentFeature + "/manifest.yaml"
			found := false
			for _, k := range got {
				if k == want {
					found = true
				}
			}
			if !found {
				t.Errorf("expected %q, got: %v", want, got)
			}
		})
	}
}
