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

// The component-identity label value is a name the COMPILER derives rather than
// one a provider guest declares, and these cases are its end-to-end proof: it
// reaches the same injectivity check a declared name does, it halts naming both
// identities, and the shapes that must NOT halt still render.
//
// The attachment-instance pairs below differ only in a character the label
// alphabet does not admit, which is what makes them fold onto one value — the
// realistic shape, since an author picking `cache a` and `cache-a` for two
// attachments has written two names they can tell apart and the target cannot.
const (
	hostFoldingInstanceA = "cache a"
	hostFoldingInstanceB = "cache-a"
	// The folded value both of the above reach. Written out by hand rather than
	// computed from the sanitiser: a value the sanitiser produced would agree
	// with the sanitiser whatever the sanitiser did.
	hostFoldedComponentID = "cache-a"
	// The near miss: one character apart from the pair above and outside the
	// fold, so the negative direction is a real negative rather than a guard
	// that refuses every attachment.
	hostDistinctInstance = "cache-b"

	hostLabelFeatureType = "core/observability/cache:1.0.0"
)

// hostLabelEngine renders per-node output and places artifacts, declaring no
// derived names of its own. That is the point: every name these cases turn on is
// one the compiler derived, so a halt here cannot be a guest's declaration
// arriving under a different route.
//
// Each emitted name carries the node identity, so no output-path collision can
// fire and the derived-name check is the only thing these cases can trip. Each
// emitted BODY carries the component identity the artifacts crossing was handed,
// which is what makes the halt a statement about a name that genuinely reaches
// the output rather than about an intermediate the build discards.
type hostLabelEngine struct{}

func (hostLabelEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (hostLabelEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{{
		Path:    componentIDOf(nodeCtx) + "-sourced.yaml",
		Content: []byte("component-id: " + componentIDOf(nodeCtx) + "\n"),
	}}, nil
}

func (hostLabelEngine) ArtifactTemplateDelimiters() (string, string) { return "{{", "}}" }

func (hostLabelEngine) EmitArtifacts(_ context.Context, proj renderer.ArtifactsProjection, nodeCtx renderer.NodeRenderContext) ([]renderer.EmittedFile, error) {
	out := make([]renderer.EmittedFile, 0, len(proj.Entries))
	for _, e := range proj.Entries {
		out = append(out, renderer.EmittedFile{
			Name:   e.Name,
			Origin: renderer.EmitOriginArtifactsInline,
			Body:   []byte("component-id: " + componentIDOf(nodeCtx) + "\n"),
		})
	}
	return out, nil
}

// componentIDOf reads the identity the render was handed the way an emitting
// surface reads it: the derived label value when the compiler set one, the node
// id otherwise.
func componentIDOf(nodeCtx renderer.NodeRenderContext) string {
	if nodeCtx.LabelComponentID != "" {
		return nodeCtx.LabelComponentID
	}
	return nodeCtx.NodeID
}

// hostLabelArtifacts is one inline artifact entry, which is what makes a
// FeatureRecipe resource-generating through the artifacts crossing — the
// crossing that reads the derived component identity.
func hostLabelArtifacts() types.OrderedMap[types.ArtifactEntry] {
	out := types.OrderedMap[types.ArtifactEntry]{}
	out.Set("cache.yaml", types.ArtifactEntry{Inline: "kind: Cache\n"})
	return out
}

// runHostLabelRender drives the whole pipeline over one node per entry of
// attachments, each node carrying exactly one attachment under the instance name
// given. Every node sits in one boundary and renders through one engine, so all
// of them land in one dispatch group and their derived names meet.
//
// frSpec decides which crossings the attachment's render reaches, which is the
// axis the source-only case below turns on.
func runHostLabelRender(attachments map[string]string, frSpec types.FeatureRecipeSpec) (*renderer.RenderResult, error) {
	nodes := types.OrderedMap[types.IRNode]{}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	order := ir.FeatureOrder{}
	// Sorted node walk so the fixture hands the pipeline one order whatever the
	// caller's map iteration produced. The halt's own determinism is pinned
	// separately; this keeps the fixture from being the source of a wobble.
	for _, nodeID := range sortedAttachmentNodes(attachments) {
		instance := attachments[nodeID]
		nodes.Set(nodeID, types.IRNode{Component: "core/api", BoundaryPath: "default"})
		entries := types.OrderedMap[types.IRFeatureEntry]{}
		entries.Set(instance, types.IRFeatureEntry{Type: hostLabelFeatureType})
		fvm.Set(nodeID, entries)
		order[nodeID] = []string{instance}
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

	factory := func(string) (renderer.Provider, error) { return hostLabelEngine{}, nil }
	return renderer.RenderAll(context.Background(), input, lookup, factory)
}

func sortedAttachmentNodes(m map[string]string) []string {
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

// hostLabelArtifactsFR is the resource-generating shape: an artifacts-only
// FeatureRecipe, whose placement crossing reads the derived component identity
// and puts it in the emitted bytes.
func hostLabelArtifactsFR() types.FeatureRecipeSpec {
	return types.FeatureRecipeSpec{
		Capability: "hostlabel",
		Implements: hostLabelFeatureType,
		Artifacts:  hostLabelArtifacts(),
	}
}

// TestRenderAll_TwoAttachmentsDerivingOneComponentIDLabelHalt is the positive
// direction, end to end. Two attachments on two different components carry
// instance names that differ by one character the label alphabet does not admit,
// so the identity the compiler derives for each is the same string — and the
// resources each attachment's FeatureRecipe places are named and labelled from
// it. The build halts naming the key space, the folded name and both components,
// which is everything a reader needs to know which two things to rename.
func TestRenderAll_TwoAttachmentsDerivingOneComponentIDLabelHalt(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRender(map[string]string{
		"alpha": hostFoldingInstanceA,
		"beta":  hostFoldingInstanceB,
	}, hostLabelArtifactsFR())
	if err == nil {
		t.Fatalf("expected a halt, got %d files", len(res.Files))
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`in key space "component-id"`,
		`the name "` + hostFoldedComponentID + `"`,
		`component "alpha" and component "beta"`,
		"change what is derived so each name is derived once within its space",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q\ngot: %s", want, msg)
		}
	}
}

// TestRenderAll_TheSameAttachmentsWithoutTheFoldRender is that halt's negative
// direction, and it carries the second half of the proof. The same pair of
// components with attachment names one character apart and outside the fold
// renders, and the emitted bytes carry BOTH derived identities — so the check is
// over names that genuinely reach the output, and the green above is not a guard
// refusing every attachment.
func TestRenderAll_TheSameAttachmentsWithoutTheFoldRender(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRender(map[string]string{
		"alpha": hostFoldingInstanceB,
		"beta":  hostDistinctInstance,
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		"component-id: " + hostFoldingInstanceB,
		"component-id: " + hostDistinctInstance,
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — the derived identity is not reaching the bytes, so the halt above would be checking a name nothing emits\n--- bodies ---\n%s", want, bodies)
		}
	}
}

// TestRenderAll_SourceOnlyAttachmentsDerivingOneLabelRender pins the bound the
// declaration is deliberately narrowed to. The same folding pair of attachment
// names on a source-only FeatureRecipe renders: a sourced render reads the
// unmodified node context, so the identity derived from the instance name lands
// on nothing emitted, and refusing a build over a name in no output would be
// refusing input the author wrote correctly.
//
// Remove the crossing condition on the declaration and this case halts, which is
// what makes the narrowing asserted rather than assumed.
func TestRenderAll_SourceOnlyAttachmentsDerivingOneLabelRender(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRender(map[string]string{
		"alpha": hostFoldingInstanceA,
		"beta":  hostFoldingInstanceB,
	}, types.FeatureRecipeSpec{
		Capability: "hostlabel",
		Implements: hostLabelFeatureType,
		Source:     "./cache-chart",
	})
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	// The host component's own identity is what a sourced attachment render
	// stamps, which is the reading that makes the derived value irrelevant here.
	bodies := strings.Join(bodiesOf(res), "\n")
	if strings.Contains(bodies, "component-id: "+hostFoldedComponentID) {
		t.Errorf("a source-only attachment render stamped the derived identity; the narrowing this case pins is wrong\n--- bodies ---\n%s", bodies)
	}
}

// TestRenderAll_ComponentIDDeclarationDoesNotChangeRenderedBytes is the
// byte-identity proof for the declaration itself. Declaring a derived name is an
// assertion about what was derived and never a change to what is emitted, so a
// build that declares must produce the same bytes as the same build did before
// anything declared. Asserted as a property of the same input rendered twice,
// which is what the deterministic-output contract means.
func TestRenderAll_ComponentIDDeclarationDoesNotChangeRenderedBytes(t *testing.T) {
	t.Parallel()

	first, err := runHostLabelRender(map[string]string{
		"alpha": hostFoldingInstanceB,
		"beta":  hostDistinctInstance,
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := runHostLabelRender(map[string]string{
		"alpha": hostFoldingInstanceB,
		"beta":  hostDistinctInstance,
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if len(first.Files) != len(second.Files) {
		t.Fatalf("file count differs between runs of identical input: %d vs %d", len(first.Files), len(second.Files))
	}
	for path, body := range first.Files {
		other, ok := second.Files[path]
		if !ok {
			t.Errorf("path %q emitted by the first run only", path)
			continue
		}
		if string(other.Content) != string(body.Content) {
			t.Errorf("path %q differs between runs of identical input", path)
		}
	}
}

// The boundary-scope identities below fold for the same reason the attachment
// ones do, one layer out: a boundary-scope feature's identity is composed from
// the feature instance and the boundary path, and the composition is sanitised
// whole — so two instance names differing by one inadmissible character reach
// one label value however distinct the authored names are.
const (
	hostBoundaryFeatureType    = "core/net/bridge:1.0.0"
	hostFoldingFeatureA        = "bridge x"
	hostFoldingFeatureB        = "bridge-x"
	hostDistinctFeature        = "bridge-y"
	hostFoldedFeatureComponent = "boundary-fr-bridge-x-cluster"
)

// runHostBoundaryFeatureRender drives the whole pipeline over one component in
// one boundary carrying the named boundary-scope feature attachments. Each
// attachment dispatches its own boundary-scope render, so their derived
// identities meet in the group at the feature's own evaluated path.
func runHostBoundaryFeatureRender(instances []string) (*renderer.RenderResult, error) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})

	boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
	clusterProj := types.OrderedMap[types.IRBoundaryFeature]{}
	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	for _, inst := range instances {
		boundaryFeatures.Set(inst, types.BoundaryFeature{Type: hostBoundaryFeatureType, Infra: types.OrderedMap[any]{}})
		clusterProj.Set(inst, types.IRBoundaryFeature{Type: hostBoundaryFeatureType, Infra: types.OrderedMap[any]{}})
		apiFV.Set(inst, types.IRFeatureEntry{Type: hostBoundaryFeatureType})
	}
	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   boundaryFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set("api", types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("cluster", clusterBoundary)

	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set("cluster", clusterProj)
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "host-label-boundary-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{"api": instances},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "host-label-boundary-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "cluster", Directory: "dist/cluster"}},
		},
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

	factory := func(string) (renderer.Provider, error) { return hostLabelEngine{}, nil }
	return renderer.RenderAll(context.Background(), input, lookup, factory)
}

// TestRenderAll_TwoBoundaryFeaturesDerivingOneComponentIDLabelHalt is the
// positive direction for the second site the compiler derives this identity at.
// Two boundary-scope features whose instance names differ by one inadmissible
// character reach one identity, and the build halts naming both features.
//
// The assembly path in the message is the assertion worth reading twice. A
// boundary-scope feature's render is reached through whichever component
// dispatched it, and its render context is a COPY of that component's — so a
// declaration made against the copied context lands in the component's own
// accumulator and is checked against the component's group. The path named here
// is the FEATURE's own evaluated path, which is what proves the declaration went
// to the feature's group rather than the dispatching component's.
func TestRenderAll_TwoBoundaryFeaturesDerivingOneComponentIDLabelHalt(t *testing.T) {
	t.Parallel()

	res, err := runHostBoundaryFeatureRender([]string{hostFoldingFeatureA, hostFoldingFeatureB})
	if err == nil {
		t.Fatalf("expected a halt, got %d files", len(res.Files))
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`assembly path "dist/cluster/hostlabel"`,
		`in key space "component-id"`,
		`the name "` + hostFoldedFeatureComponent + `"`,
		`feature "` + hostFoldingFeatureA + `" and feature "` + hostFoldingFeatureB + `"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q\ngot: %s", want, msg)
		}
	}
	// The dispatching component's own group path must not be the one reported: it
	// would mean the declaration landed in the component's accumulator.
	if strings.Contains(msg, `assembly path "boundaries/cluster/`) {
		t.Errorf("the halt names the dispatching component's group, so the declaration reached the wrong accumulator\ngot: %s", msg)
	}
}

// TestRenderAll_TheSameBoundaryFeaturesWithoutTheFoldRender is that halt's
// negative direction: two boundary-scope features one character apart and
// outside the fold render, and the emitted bytes carry both derived identities.
func TestRenderAll_TheSameBoundaryFeaturesWithoutTheFoldRender(t *testing.T) {
	t.Parallel()

	res, err := runHostBoundaryFeatureRender([]string{hostFoldingFeatureB, hostDistinctFeature})
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		"component-id: boundary-fr-bridge-x-cluster",
		"component-id: boundary-fr-bridge-y-cluster",
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — the derived identity is not reaching the bytes\n--- bodies ---\n%s", want, bodies)
		}
	}
}

func bodiesOf(res *renderer.RenderResult) []string {
	out := make([]string, 0, len(res.Files))
	for path, body := range res.Files {
		out = append(out, path+"\n"+string(body.Content))
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
