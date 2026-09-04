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
// The declaration is held as a pointer so the fixture can drive all three states
// the provider contract carries, rather than the two a string reaches: a named
// directory, an explicitly empty one, and no declaration at all.
type segmentEngine struct {
	attachmentSegment *string
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

// AttachmentSegment is the declaration under test. A nil pointer declares
// nothing, which is the shape every shipped provider has and the one the
// default-path proof drives. A pointer to the empty string declares that no
// enclosing directory is wanted, which is a claim about layout rather than the
// absence of one.
func (e *segmentEngine) AttachmentSegment() (string, bool) {
	if e.attachmentSegment == nil {
		return "", false
	}
	return *e.attachmentSegment, true
}

// declares builds the pointer a provider naming segment reports. Passing nil to
// the driver instead is what a provider declaring nothing reports, and the two are
// deliberately spelled differently at every call site: declares("") is a provider
// asking for no enclosing directory, which is not the same claim as declaring
// nothing at all.
func declares(segment string) *string { return &segment }

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
// declaring segment, and returns the emitted paths in path order. A nil segment
// drives a provider that declares nothing.
func runSegmentRender(t *testing.T, segment *string) ([]string, error) {
	t.Helper()
	return runSegmentRenderWith(t, segment, nil)
}

// runSegmentRenderWith is the same driver with extra component-scope attachments
// on the one host, named by the instance names given.
//
// Each extra attachment carries the SAME feature type as the first, so it runs
// the same FeatureRecipe and renders the same file name. That is deliberate and
// it is the shape the collision question is actually about: what separates two
// such attachments is their instance name and nothing else, so if the placement
// loses that coordinate their output meets on one path.
func runSegmentRenderWith(t *testing.T, segment *string, extraInstances []string) ([]string, error) {
	t.Helper()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(segmentHost, types.IRNode{Component: "core/api", BoundaryPath: segmentBoundary})

	// Both attachments are listed against the node, and the boundary-scope one is
	// routed to its own scope by the ExecutionScope its FeatureRecipe declares
	// rather than by where it is listed — which is how the render reaches a
	// boundary-scope attachment at all.
	componentFeatures := types.OrderedMap[types.IRFeatureEntry]{}
	componentFeatures.Set(segmentComponentFeature, types.IRFeatureEntry{Type: segmentComponentType})
	for _, instance := range extraInstances {
		componentFeatures.Set(instance, types.IRFeatureEntry{Type: segmentComponentType})
	}
	componentFeatures.Set(segmentBoundaryFeature, types.IRFeatureEntry{Type: segmentBoundaryType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(segmentHost, componentFeatures)

	featureOrder := append([]string{segmentComponentFeature}, extraInstances...)
	featureOrder = append(featureOrder, segmentBoundaryFeature)

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
		FeatureOrder: ir.FeatureOrder{segmentHost: featureOrder},
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

	declared, err := runSegmentRender(t, declares("addons"))
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

	undeclared, err := runSegmentRender(t, nil)
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

	got, err := runSegmentRender(t, nil)
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

// TestRenderAll_AttachmentSegmentDeclaredEmpty_IsNotTheHostDefault is the proof
// that the report's three states are three, and it is the reason the declaration
// is carried by reference rather than as a string.
//
// A provider declaring an empty directory and a provider declaring nothing are
// different claims: the first asks for no enclosing directory, the second leaves
// the host its own default. Held in one string those two would be the same value,
// so the difference would be unsayable — and the failure would be silent, because
// a provider asking for no directory would simply receive the default and emit a
// layout it never asked for.
//
// Both runs are driven side by side so the assertion is the DIFFERENCE rather than
// a literal: an equality on the flat paths alone would pass if the default were
// ever removed, and an equality on the default paths alone would pass if the empty
// declaration were ignored. Neither passes here unless both states are honoured.
//
// The declared-empty run is also the negative control for the bound below: an
// empty value is not a path, so it must be accepted rather than refused.
//
// # The coordinates move into the FILENAME, and dropping the directory alone is
// not the property
//
// The expected paths carry no directory level at all: an attachment's own
// coordinates arrive as a filename prefix rather than as directory segments, so
// its files sit in the very directory the component's own file sits in. That is
// the whole point of the declaration — a target that reads only the files
// sitting directly in a directory reads the attachment's too — and dropping the
// enclosing directory while leaving the coordinates as directories would satisfy
// the sentence and not the requirement, since the files would still be nested
// two levels down and still unread.
//
// The prefix meets the file's own name at the segment separator the encoding
// reserves, which is what keeps two contributors off one filename. The
// separator is read from the encoding rather than written as a literal here:
// spelling it would put a second copy of the one rule the derivation's
// injectivity rests on into a test, where it could agree today and drift after.
func TestRenderAll_AttachmentSegmentDeclaredEmpty_IsNotTheHostDefault(t *testing.T) {
	t.Parallel()

	flat, err := runSegmentRender(t, declares(""))
	if err != nil {
		t.Fatalf("declaring no enclosing directory is a claim, not a bound violation, and must render: %v", err)
	}
	sep := renderer.AttachmentFileSeparatorForTest
	wantFlat := []string{
		"dist/cluster/segprov/" + segmentHost + sep + segmentComponentFeature + sep + "manifest.yaml",
		"dist/cluster/segprov/" + segmentBoundaryFeature + sep + "manifest.yaml",
		"dist/cluster/segprov/manifest.yaml",
	}
	if strings.Join(flat, "\n") != strings.Join(wantFlat, "\n") {
		t.Fatalf("a declared empty directory did not flatten the attachment beside the component\nwant:\n%s\ngot:\n%s",
			strings.Join(wantFlat, "\n"), strings.Join(flat, "\n"))
	}
	// No emitted path may carry a directory level beyond the dispatch path
	// itself, which is the requirement the expected paths above encode and the
	// one a "dropped the enclosing directory" reading would miss.
	for _, path := range flat {
		if rest := strings.TrimPrefix(path, "dist/cluster/segprov/"); strings.Contains(rest, "/") {
			t.Errorf("a declared empty directory still left a nested path: %s", path)
		}
	}

	// The companion state, run through the same corpus and the same provider, so
	// the only difference between the two results is the declaration itself.
	undeclared, err := runSegmentRender(t, nil)
	if err != nil {
		t.Fatalf("the undeclared companion run must render: %v", err)
	}
	if strings.Join(flat, "\n") == strings.Join(undeclared, "\n") {
		t.Fatalf("declaring an empty directory and declaring nothing emitted the same paths, so the two states are still one:\n%s",
			strings.Join(flat, "\n"))
	}
	// Read from the host's own constant rather than written as a literal, which is
	// the opposite choice from the expected paths above and deliberate: an absence
	// claim should follow the default so that changing the default re-points the
	// claim at the new one, whereas an expected path written as a literal is what
	// catches the default moving in the first place.
	for _, path := range flat {
		if strings.Contains(path, "/"+renderer.DefaultAttachmentSegment+"/") {
			t.Errorf("a provider that declared no enclosing directory still received the host default: %s", path)
		}
	}
}

// TestRenderAll_TwoAttachmentsOnOneHostBothSurviveFlatPlacement is the direct
// proof of the collision guarantee, driven end to end through RenderAll rather
// than over the derivation.
//
// # Why it asserts BOTH files rather than that the flattening happened
//
// A test asserting that one expected file exists PASSES ON THE DEFECT. Two
// attachments on one component render the same file name, so if the placement
// dropped or folded the coordinate that separates them, one file would overwrite
// the other and an assertion naming either would still find its file. Only
// requiring both, and requiring the count, can tell the two apart.
//
// # And nothing else would have caught it
//
// The failure this refuses is SILENT. A component-scope attachment's files merge
// into the bucket of the node hosting them, and that bucket is stamped with one
// producer label computed per node — so both attachments arrive as a single
// producer, the rule that refuses one output path written by more than one
// contributor cannot fire, and the losing file is dropped by a plain map
// assignment. No halt, no diagnostic, no warning: a Feature the author attached
// is simply missing from the output. So this assertion is not a convenience over
// a guard that would also catch it; it is the only thing watching.
//
// The nested run is included as the companion, because it is what establishes
// that the two attachments were genuinely distinct contributors before the
// flattening — a flat run alone could not distinguish "both survived" from "the
// fixture only ever had one".
func TestRenderAll_TwoAttachmentsOnOneHostBothSurviveFlatPlacement(t *testing.T) {
	t.Parallel()

	const second = "sec"
	sep := renderer.AttachmentFileSeparatorForTest

	flat, err := runSegmentRenderWith(t, declares(""), []string{second})
	if err != nil {
		t.Fatalf("two attachments on one host must render: %v", err)
	}

	// Both, named individually, so a failure says which one went missing.
	first := "dist/cluster/segprov/" + segmentHost + sep + segmentComponentFeature + sep + "manifest.yaml"
	other := "dist/cluster/segprov/" + segmentHost + sep + second + sep + "manifest.yaml"
	present := map[string]bool{}
	for _, path := range flat {
		present[path] = true
	}
	for _, want := range []string{first, other} {
		if !present[want] {
			t.Errorf("an attachment's file is absent from the flat output: %s\nemitted:\n%s",
				want, strings.Join(flat, "\n"))
		}
	}
	if first == other {
		t.Fatal("the two attachments derive one filename, so the assertion above cannot distinguish them")
	}

	// The nested companion: the same two attachments under the host default,
	// where they are separated by directories instead. Both survive there too,
	// which is what makes the flat result a preserved property rather than a
	// coincidence of this fixture.
	nested, err := runSegmentRenderWith(t, nil, []string{second})
	if err != nil {
		t.Fatalf("the nested companion run must render: %v", err)
	}
	if len(nested) != len(flat) {
		t.Errorf("flattening changed how many files are emitted: nested %d, flat %d\nnested:\n%s\nflat:\n%s",
			len(nested), len(flat), strings.Join(nested, "\n"), strings.Join(flat, "\n"))
	}
	t.Logf("flat output:\n%s", strings.Join(flat, "\n"))
	t.Logf("nested output:\n%s", strings.Join(nested, "\n"))
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

			got, err := runSegmentRender(t, declares(segment))
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

			got, err := runSegmentRender(t, declares(segment))
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
