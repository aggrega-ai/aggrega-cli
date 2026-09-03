// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A contributor to a dispatch group is identified by the coordinates the author
// wrote, and these cases are the end-to-end proof that every one of them is
// carried. Two contributors that differ in a coordinate the identification drops
// arrive under one name, and the group check then reads one declarer where there
// are two — so the two names it was meant to compare are never compared at all.
//
// The two shapes below are the two places a coordinate can be dropped: a feature
// attached at two different boundaries, and two attachments on one component.
const (
	// The two boundaries below assemble to ONE dispatch path, which is what puts
	// their contributors in one group and makes their derived names meet. They
	// are distinct boundaries an author wrote and can tell apart.
	twoBoundaryDirectory = "dist/shared"
	twoBoundaryGroupPath = "dist/shared/hostlabel"
	twoBoundaryPathA     = "eu"
	twoBoundaryPathB     = "us"
	// One instance name, written at both boundaries. Attaching one feature under
	// one name at several boundaries is ordinary authoring.
	sharedFeatureInstance = "bridge"
	// The name both features' guests declare. It stands for any name two
	// contributors can arrive at: what is under test is whether the two are
	// recognised as two contributors, not how they reached one name.
	sharedGuestDerivedName = "sharedQueue"
)

// runTwoBoundaryFeatureRender drives the whole pipeline over one component in
// each of two boundaries that assemble to a single dispatch path, each boundary
// carrying the named feature attachments.
//
// Both boundaries name the same directory, so both dispatch groups resolve to
// one path and every contributor's derived names meet in it. That is the whole
// point of the fixture: without a shared path there is no group for two
// contributors to collide in.
func runTwoBoundaryFeatureRender(
	instancesByBoundary map[string][]string,
	declare map[string]map[string][]string,
) (*renderer.RenderResult, error) {
	nodes := types.OrderedMap[types.IRNode]{}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	boundaries := types.OrderedMap[types.Boundary]{}
	order := ir.FeatureOrder{}
	boundaryInfra := map[string]*resolver.BoundaryInfra{}

	for _, boundaryPath := range sortedKeysOf(instancesByBoundary) {
		instances := instancesByBoundary[boundaryPath]
		nodeID := "api-" + boundaryPath
		nodes.Set(nodeID, types.IRNode{Component: "core/api", BoundaryPath: boundaryPath})
		boundaryInfra[nodeID] = &resolver.BoundaryInfra{
			BoundaryPath: boundaryPath, Directory: twoBoundaryDirectory,
		}

		boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
		proj := types.OrderedMap[types.IRBoundaryFeature]{}
		nodeFV := types.OrderedMap[types.IRFeatureEntry]{}
		for _, inst := range instances {
			boundaryFeatures.Set(inst, types.BoundaryFeature{Type: hostBoundaryFeatureType, Infra: types.OrderedMap[any]{}})
			proj.Set(inst, types.IRBoundaryFeature{Type: hostBoundaryFeatureType, Infra: types.OrderedMap[any]{}})
			nodeFV.Set(inst, types.IRFeatureEntry{Type: hostBoundaryFeatureType})
		}
		fvm.Set(nodeID, nodeFV)
		bfProj.Set(boundaryPath, proj)
		order[nodeID] = instances

		boundary := types.Boundary{
			Directory:  twoBoundaryDirectory,
			Features:   boundaryFeatures,
			Components: types.OrderedMap[types.BoundaryComponent]{},
			Boundaries: types.OrderedMap[types.Boundary]{},
		}
		boundary.Components.Set(nodeID, types.BoundaryComponent{})
		boundaries.Set(boundaryPath, boundary)
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "two-boundary-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: order,
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "two-boundary-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: boundaryInfra,
		},
		GraphView: derivedKeyGraphFacts{},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "hostlabel", Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == hostBoundaryFeatureType {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "bridge-fr", Version: "1.0.0"},
					Spec: types.FeatureRecipeSpec{
						Capability:     "hostlabel",
						Implements:     hostBoundaryFeatureType,
						ExecutionScope: "boundary",
						Artifacts:      hostLabelArtifacts(),
					},
				}, true
			}
			return nil, false
		},
	}

	factory := func(string) (renderer.Provider, error) { return hostLabelEngine{declare: declare}, nil }
	return renderer.RenderAll(context.Background(), input, lookup, factory)
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// TestRenderAll_OneFeatureNameAtTwoBoundariesDerivingOneNameHalts is the
// boundary-scope half of the identification. One feature instance name written
// at two different boundaries is two contributors: the author wrote two
// attachments, each renders separately, and each derives its own names. Where
// both boundaries assemble to one dispatch path and the two arrive at one name,
// one of the two is silently displaced in the merged output.
//
// The halt has to name BOTH boundaries, because the instance name is identical
// at each and is therefore no help at all in telling the reader which two things
// to separate.
func TestRenderAll_OneFeatureNameAtTwoBoundariesDerivingOneNameHalts(t *testing.T) {
	t.Parallel()

	res, err := runTwoBoundaryFeatureRender(
		map[string][]string{
			twoBoundaryPathA: {sharedFeatureInstance},
			twoBoundaryPathB: {sharedFeatureInstance},
		},
		map[string]map[string][]string{
			"<boundary-fr>:" + sharedFeatureInstance + "@" + twoBoundaryPathA: {
				hostComponentIDSpace: {sharedGuestDerivedName},
			},
			"<boundary-fr>:" + sharedFeatureInstance + "@" + twoBoundaryPathB: {
				hostComponentIDSpace: {sharedGuestDerivedName},
			},
		})
	if err == nil {
		t.Fatalf("expected a halt, got %d files", len(res.Files))
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	findings := derivedNameFindings(t, err)
	if len(findings) != 2 {
		t.Fatalf("expected one finding per contributing boundary, got %d: %v", len(findings), findings)
	}
	whole := strings.Join(findings, "\n")
	for _, want := range []string{
		`dispatch path "` + twoBoundaryGroupPath + `"`,
		`key space "` + hostComponentIDSpace + `"`,
		`"` + sharedGuestDerivedName + `"`,
		`"` + twoBoundaryPathA + `"`,
		`"` + twoBoundaryPathB + `"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the findings do not carry %q\ngot: %v", want, findings)
		}
	}
}

// TestRenderAll_TwoBoundaryFeaturesWithNoSharedNameRender is that halt's negative
// direction: the same two boundaries and the same one instance name, with each
// feature's guest declaring a name of its own. Without it the case above would pass
// equally well against a guard that refused every feature attached at more than one
// boundary, so what is under test is that the name check answers on the NAMES.
//
// This fixture's two contributors also meet on one output path, and that is a
// second, separate collision rather than a flaw in the negative direction. Both
// boundaries declare one directory — which is what puts their contributors in one
// group at all — and a boundary-scope attachment's files land at its instance's own
// features position, which the two share because the instance name is shared. So
// two renders each produce a cache.yaml for one path, and one path holds one file.
//
// The build therefore halts, on the assembled-output rule and not on the name rule,
// and the assertion is written against the sentinels rather than against whether the
// build completed. Asserting completion would tie this case to a second rule it is
// not about, and would go red the next time anything changed there.
//
// 🔴 The halt naming both boundaries is the property, not an incidental. The
// contributors' attributions are compared for equality by the rule that reports who
// met at one path, so an attribution carrying only the shared instance name would
// make the two one producer and the rule would report nothing — which reads exactly
// like a build with no collision, while one boundary's rendered output silently
// leaves the build.
func TestRenderAll_TwoBoundaryFeaturesWithNoSharedNameRender(t *testing.T) {
	t.Parallel()

	_, err := runTwoBoundaryFeatureRender(
		map[string][]string{
			twoBoundaryPathA: {sharedFeatureInstance},
			twoBoundaryPathB: {sharedFeatureInstance},
		},
		map[string]map[string][]string{
			"<boundary-fr>:" + sharedFeatureInstance + "@" + twoBoundaryPathA: {
				hostComponentIDSpace: {sharedGuestDerivedName + "-eu"},
			},
			"<boundary-fr>:" + sharedFeatureInstance + "@" + twoBoundaryPathB: {
				hostComponentIDSpace: {sharedGuestDerivedName + "-us"},
			},
		})
	if err == nil {
		t.Fatal("expected the output-path halt: two contributors write one path here")
	}
	if errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the name rule fired on two DIFFERENT names, which would make it a guard against "+
			"attaching one feature at two boundaries: %v", err)
	}
	if !errors.Is(err, renderer.ErrAssembledOutputCollision()) {
		t.Fatalf("expected ErrAssembledOutputCollision, got: %v", err)
	}
	for _, want := range []string{
		twoBoundaryGroupPath + "/features/" + sharedFeatureInstance,
		"feature " + sharedFeatureInstance + " attached at boundary " + twoBoundaryPathA,
		"feature " + sharedFeatureInstance + " attached at boundary " + twoBoundaryPathB,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not carry %q, so a reader cannot tell which two attachments met\ngot: %v", want, err)
		}
	}
}

// TestRenderAll_TwoAttachmentsOnOneComponentDifferingInARefusedCharacterSeparate
// is the per-component half. Two attachments on ONE component are two things the
// author wrote under two keys, each with its own FeatureRecipe render and its own
// generated resources — so each needs an identity of its own, or one attachment's
// resources are named after the other's and the merged output carries one of the
// two.
//
// The pair below differs only in a character a label value may not carry, which
// is where an identity written into the label alphabet by substitution ran two
// keys together: substitution is many-to-one. The identity is now composed
// through an encoding that writes such a character out instead of folding it onto
// a hyphen, so the two keys reach two identities and the build completes.
//
// The expected identities are written out by hand rather than composed here: a
// value the composition produced would agree with the composition whatever it
// did.
func TestRenderAll_TwoAttachmentsOnOneComponentDifferingInARefusedCharacterSeparate(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRenderMulti(map[string][]string{
		"alpha": {hostRefusedCharInstance, hostPlainInstance},
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		"component-id: " + hostIdentityAlphaRefusedChar,
		"component-id: " + hostIdentityAlphaPlain,
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — the two keys have not reached two identities\n--- bodies ---\n%s", want, bodies)
		}
	}
	// The value the pair ran together into must not be an identity any more:
	// reaching it is the collision this composition removes rather than reports.
	if strings.Contains(bodies, "component-id: "+hostSubstitutedComponentID+"\n") {
		t.Errorf("emitted output still carries the run-together value %q as an identity\n--- bodies ---\n%s", hostSubstitutedComponentID, bodies)
	}
}

// TestRenderAll_TwoAttachmentsOnOneComponentReachTwoIdentities is the ordinary
// direction: two attachments on one component whose keys differ in a character
// the alphabet admits reach two identities and render, so the case above is a
// separation being demonstrated rather than a second attachment being admitted
// by accident.
func TestRenderAll_TwoAttachmentsOnOneComponentReachTwoIdentities(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRenderMulti(map[string][]string{
		"alpha": {hostPlainInstance, hostDistinctInstance},
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		"component-id: " + hostIdentityAlphaPlain,
		"component-id: " + hostIdentityAlphaDistinct,
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — the composed identity is not reaching the bytes\n--- bodies ---\n%s", want, bodies)
		}
	}
}

// TestRenderAll_AnAttachmentsGuestDeclaresUnderTheAttachmentsOwnLabel is the
// end-to-end proof that the declaration sink is rewired for an attachment's
// render rather than inherited from the node whose context was copied.
//
// The fixture engine declares the same name at every crossing it is asked for. On
// this node those crossings are the component's own source render and the
// attachment's artifacts placement — two derivations over two different authored
// inputs whose output merges into one assembled output. Under the node's own sink
// both would arrive as the node and read as one contributor restating itself; the
// halt is therefore the observable difference the rewiring makes, and each finding
// names which of the two derived the name.
func TestRenderAll_AnAttachmentsGuestDeclaresUnderTheAttachmentsOwnLabel(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRenderDeclaring(
		map[string][]string{"alpha": {hostDistinctInstance}},
		hostLabelArtifactsFR(),
		map[string]map[string][]string{
			"alpha": {hostComponentIDSpace: {sharedGuestDerivedName}},
		})
	if err == nil {
		t.Fatalf("expected a halt, got %d files", len(res.Files))
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	findings := derivedNameFindings(t, err)
	whole := strings.Join(findings, "\n")
	for _, want := range []string{
		`derived by component "alpha", provider`,
		`derived by component "alpha", carrying attachment "` + hostDistinctInstance + `"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the findings do not carry %q\ngot: %v", want, findings)
		}
	}
}

// runHostLabelRenderMulti is runHostLabelRender with more than one attachment
// per node, which is the shape the same-component cases turn on. Every node
// sits in one boundary and renders through one engine, so all of them land in
// one dispatch group.
func runHostLabelRenderMulti(attachments map[string][]string, frSpec types.FeatureRecipeSpec) (*renderer.RenderResult, error) {
	return runHostLabelRenderDeclaring(attachments, frSpec, nil)
}

// runHostLabelRenderDeclaring is runHostLabelRenderMulti with the fixture engine
// asked to declare names of its own, which is the axis the sink-rewiring case
// turns on: the engine declares at every crossing it is handed, and the crossings
// belong to different producers.
func runHostLabelRenderDeclaring(
	attachments map[string][]string,
	frSpec types.FeatureRecipeSpec,
	declare map[string]map[string][]string,
) (*renderer.RenderResult, error) {
	nodes := types.OrderedMap[types.IRNode]{}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	order := ir.FeatureOrder{}
	for _, nodeID := range sortedKeysOf(attachments) {
		nodes.Set(nodeID, types.IRNode{Component: "core/api", BoundaryPath: "default"})
		entries := types.OrderedMap[types.IRFeatureEntry]{}
		for _, instance := range attachments[nodeID] {
			entries.Set(instance, types.IRFeatureEntry{Type: hostLabelFeatureType})
		}
		fvm.Set(nodeID, entries)
		order[nodeID] = attachments[nodeID]
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "host-label-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  order,
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
		GraphView:     derivedKeyGraphFacts{},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "hostlabel", Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == hostLabelFeatureType {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "cache-fr", Version: "1.0.0"},
					Spec:     frSpec,
				}, true
			}
			return nil, false
		},
	}

	factory := func(string) (renderer.Provider, error) { return hostLabelEngine{declare: declare}, nil }
	return renderer.RenderAll(context.Background(), input, lookup, factory)
}
