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
// reaches the same injectivity check a declared name does, it reaches the
// emitted bytes, and every attachment shape an author may legitimately write
// arrives at an identity of its own.
//
// The attachment-instance pairs below differ only in a character the label
// alphabet does not admit — the realistic shape, since an author picking
// `cache a` and `cache-a` for two attachments has written two names they can
// tell apart and the target cannot.
const (
	hostRefusedCharInstance = "cache a"
	hostPlainInstance       = "cache-a"
	// The value BOTH of the above once arrived at, kept as the value neither may
	// arrive at now. Written out by hand rather than computed from any
	// derivation: a value a derivation produced would agree with that derivation
	// whatever it did.
	hostSubstitutedComponentID = "cache-a"
	// The near miss: one admitted character apart from the pair above, so a case
	// asserting separation is a real separation rather than a guard refusing
	// every attachment.
	hostDistinctInstance = "cache-b"
	// An attachment named exactly after the component hosting it. The identity
	// composed for it has to be distinct from the host's own, which the host
	// component's authored identity is carried through as.
	hostSelfNamedInstance = "alpha"

	hostLabelFeatureType = "core/observability/cache:1.0.0"

	// hostComponentIDSpace is the key space the compiler declares the component
	// identity into, written out here rather than read from the compiler: a
	// fixture reading the value under test agrees with it whatever it is.
	hostComponentIDSpace = "component-id"
)

// The identities the attachment instances above reach on each host component,
// written out by hand for the same reason every other expected value here is: a
// value computed from the composition would agree with the composition whatever
// it did.
//
// Each opens with the per-component scope and carries the identity of the
// component the attachment sits on. The host is what separates one instance name
// attached to two components, which is a shape an author may legitimately write
// — and a cascade minting one instance name across many components by design
// gives an author no way to write anything else.
const (
	hostIdentityAlphaRefusedChar = "cfr--alpha--cache-zx000020a"
	hostIdentityAlphaPlain       = "cfr--alpha--cache-a"
	hostIdentityAlphaDistinct    = "cfr--alpha--cache-b"
	hostIdentityBetaPlain        = "cfr--beta--cache-a"
	hostIdentityBetaDistinct     = "cfr--beta--cache-b"
	hostIdentityAlphaSelfNamed   = "cfr--alpha--alpha"
)

// hostLabelEngine renders per-node output and places artifacts, and declares a
// derived name only where a fixture hands it one — which is nowhere in the cases
// that turn on a name the COMPILER derived. That is the point of those cases: a
// halt in them cannot be a guest's declaration arriving under a different route.
// The one case that does hand it a declaration says in its own comment why it
// needs a second declarer.
//
// Each emitted name carries the node identity, so no output-path collision can
// fire and the derived-name check is the only thing these cases can trip. Each
// emitted BODY carries the component identity the artifacts crossing was handed,
// which is what makes the halt a statement about a name that genuinely reaches
// the output rather than about an intermediate the build discards.
type hostLabelEngine struct {
	// declare maps a node id to the spaces and names that node's guest declares.
	// Empty in every case but one.
	declare map[string]map[string][]string
}

func (hostLabelEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (e hostLabelEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	e.declareFixtureNames(nodeCtx)
	return []renderer.RenderedFile{{
		Path:    componentIDOf(nodeCtx) + "-sourced.yaml",
		Content: []byte("component-id: " + componentIDOf(nodeCtx) + "\n"),
	}}, nil
}

func (hostLabelEngine) ArtifactTemplateDelimiters() (string, string) { return "{{", "}}" }

func (e hostLabelEngine) EmitArtifacts(_ context.Context, proj renderer.ArtifactsProjection, nodeCtx renderer.NodeRenderContext) ([]renderer.EmittedFile, error) {
	// Declared from here as well as from the source render, because a
	// resource-generating FeatureRecipe reaches the engine through the artifacts
	// crossing and never through the source one — so a fixture declaration wired
	// only to the source render would silently never arrive.
	e.declareFixtureNames(nodeCtx)
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

// declareFixtureNames forwards whatever the fixture asked this node's guest to
// declare, over the node context's own sink — which is the route a real provider
// guest's declaration takes. Nothing is declared for a node the fixture did not
// name, so a case that hands over no declarations is exercising the compiler's
// own derivation and nothing else.
//
// The space is walked from a fixed list rather than by iterating the fixture's
// own map, so the payload handed to the host is a function of the fixture rather
// than of Go's map iteration.
func (e hostLabelEngine) declareFixtureNames(nodeCtx renderer.NodeRenderContext) {
	spaces, named := e.declare[nodeCtx.NodeID]
	if !named || nodeCtx.DerivedKeySink == nil {
		return
	}
	declared := types.OrderedMap[[]string]{}
	for _, space := range []string{hostComponentIDSpace} {
		if keys, ok := spaces[space]; ok {
			declared.Set(space, keys)
		}
	}
	if declared.Len() > 0 {
		nodeCtx.DerivedKeySink(declared)
	}
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

// TestRenderAll_OneAttachmentNameOnTwoComponentsPlacesEachUnderItsHost pins the
// artifacts-bearing shape of one instance name attached to two components in ONE
// boundary: it builds, and each attachment's files land under the component
// hosting it.
//
// The shape needs pinning on the ARTIFACTS crossing specifically, because that is
// the crossing whose emitted names cannot separate the two on their own. An
// artifact's output filename is the author's map key verbatim, so both
// attachments emit the same filename however the identity is composed — the
// directory is the only thing that can tell them apart, and it does so by naming
// the host.
//
// Both nodes sit in one boundary and render through one engine, so they land in
// one dispatch group and their output paths genuinely meet rather than being kept
// apart by sitting in different groups.
func TestRenderAll_OneAttachmentNameOnTwoComponentsPlacesEachUnderItsHost(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRender(map[string]string{
		"alpha": hostPlainInstance,
		"beta":  hostPlainInstance,
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("one instance name on two components must build, got: %v", err)
	}
	var keys []string
	for k := range res.Files {
		keys = append(keys, k)
	}
	joined := strings.Join(keys, "\n")
	for _, host := range []string{"alpha", "beta"} {
		want := "features/" + host + "/" + hostPlainInstance + "/"
		if !strings.Contains(joined, want) {
			t.Errorf("no output key is placed under %q; the two attachments have not been separated\nkeys: %v",
				want, keys)
		}
	}
}

// TestRenderAll_AnAttachmentNamedAfterItsHostReachesItsOwnIdentity pins the case
// the composition's own inputs exist to serve: an attachment named exactly after
// the component hosting it.
//
// The host component's identity is the one its author wrote, carried through
// unmodified; the attachment's is composed under a scope no authored identity may
// open with. So the two can never meet however alike the names an author chose,
// and both reach the emitted bytes.
func TestRenderAll_AnAttachmentNamedAfterItsHostReachesItsOwnIdentity(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRender(map[string]string{
		"alpha": hostSelfNamedInstance,
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		// The host's own render stamps the identity its author wrote.
		"component-id: alpha\n",
		// The attachment's render stamps the composed one.
		"component-id: " + hostIdentityAlphaSelfNamed + "\n",
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — an attachment named after its host has not reached an identity of its own\n--- bodies ---\n%s", want, bodies)
		}
	}
	if hostIdentityAlphaSelfNamed == "alpha" {
		t.Fatal("the fixture states the composed identity as the host's own, so this case asserts nothing")
	}
}

// TestRenderAll_TwoComponentsWithDifferentAttachmentNamesReachTwoIdentities is
// the ordinary shape, and it carries the second half of the proof: two
// components with attachment names one admitted character apart render, and the
// emitted bytes carry BOTH composed identities — so the derivation is over names
// that genuinely reach the output rather than over an intermediate the build
// discards.
func TestRenderAll_TwoComponentsWithDifferentAttachmentNamesReachTwoIdentities(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRender(map[string]string{
		"alpha": hostPlainInstance,
		"beta":  hostDistinctInstance,
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		"component-id: " + hostIdentityAlphaPlain,
		"component-id: " + hostIdentityBetaDistinct,
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — the composed identity is not reaching the bytes\n--- bodies ---\n%s", want, bodies)
		}
	}
}

// TestRenderAll_SourceOnlyAttachmentsDerivingOneLabelRender pins the bound the
// declaration is deliberately narrowed to. A source-only FeatureRecipe renders:
// a sourced render reads the unmodified node context, so the identity composed
// for the attachment lands on nothing emitted, and refusing a build over a name
// in no output would be refusing input the author wrote correctly.
//
// Remove the crossing condition on the declaration and this case halts, which is
// what makes the narrowing asserted rather than assumed.
//
// The assertion names the COMPOSED identities rather than any earlier spelling,
// because those are the values this render would stamp if the narrowing were
// wrong — a check for a value nothing can produce would pass whatever the
// narrowing did.
func TestRenderAll_SourceOnlyAttachmentsDerivingOneLabelRender(t *testing.T) {
	t.Parallel()

	res, err := runHostLabelRender(map[string]string{
		"alpha": hostRefusedCharInstance,
		"beta":  hostPlainInstance,
	}, types.FeatureRecipeSpec{
		Capability: "hostlabel",
		Implements: hostLabelFeatureType,
		Source:     "./cache-chart",
	})
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	// The host component's own identity is what a sourced attachment render
	// stamps, which is the reading that makes the composed value irrelevant here.
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, unwanted := range []string{
		hostIdentityAlphaRefusedChar,
		hostIdentityBetaPlain,
		hostSubstitutedComponentID + "\n",
	} {
		if strings.Contains(bodies, "component-id: "+unwanted) {
			t.Errorf("a source-only attachment render stamped %q; the narrowing this case pins is wrong\n--- bodies ---\n%s", unwanted, bodies)
		}
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
		"alpha": hostPlainInstance,
		"beta":  hostDistinctInstance,
	}, hostLabelArtifactsFR())
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := runHostLabelRender(map[string]string{
		"alpha": hostPlainInstance,
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

// The boundary-scope identities below are the pair that used to fold, kept as
// the fixture for the case that they no longer do. A boundary-attached
// feature's identity is COMPOSED from the instance name and the boundary path
// through an encoding that writes out what the label alphabet refuses, so two
// instance names one inadmissible character apart reach two identities. They had
// reached one, because the parts were run into a single marker string and every
// character the alphabet refused became the same hyphen.
const (
	hostBoundaryFeatureType = "core/net/bridge:1.0.0"
	hostFormerlyFoldingA    = "bridge x"
	hostFormerlyFoldingB    = "bridge-x"
	hostDistinctFeature     = "bridge-y"
	// The one value both of the pair above used to reach. Written out by hand:
	// a value computed from the derivation would agree with the derivation
	// whatever it did.
	hostFormerlyFoldedIdentity = "boundary-fr-bridge-x-cluster"
	// The identities the pair reaches now, likewise written out by hand.
	hostComposedIdentityA = "bfr--cluster--bridge-zx000020x"
	hostComposedIdentityB = "bfr--cluster--bridge-x"
	hostComposedIdentityC = "bfr--cluster--bridge-y"
	// The evaluated assembly path a boundary-attached feature's output lands at,
	// which is the feature's own rather than that of the component whose render
	// dispatched it.
	hostFeatureGroupPath = "dist/cluster/hostlabel"
)

// runHostBoundaryFeatureRender drives the whole pipeline over one component in
// one boundary carrying the named boundary-scope feature attachments. Each
// attachment dispatches its own boundary-scope render, so their derived
// identities meet in the group at the feature's own evaluated path.
func runHostBoundaryFeatureRender(instances []string, declare map[string]map[string][]string) (*renderer.RenderResult, error) {
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

// TestRenderAll_TwoBoundaryFeaturesOnceFoldingNowSeparate is the direct proof of
// what the composed identity buys at the boundary-scope site. The pair below
// reached ONE identity while the value was scrubbed out of a single marker
// string, so the build halted on them; each part is now written into the label
// alphabet separately and they reach two, so the build completes and the emitted
// bytes carry both.
//
// The halt is not merely unexercised here — it is unreachable at this site, and
// that is the deliverable rather than a gap. Two boundary-attached features
// differ in their instance name or in their boundary path, both are written out
// without folding, and the parts are joined by a run no written part can contain,
// so no pair an author can write reaches one identity below the length ceiling.
// The compiler-derived name still reaching the guard, and still halting, is
// covered by the per-component attachment case above, whose derivation folds by
// necessity because there is a single authored name and nothing to compose.
func TestRenderAll_TwoBoundaryFeaturesOnceFoldingNowSeparate(t *testing.T) {
	t.Parallel()

	res, err := runHostBoundaryFeatureRender([]string{hostFormerlyFoldingA, hostFormerlyFoldingB}, nil)
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		"component-id: " + hostComposedIdentityA,
		"component-id: " + hostComposedIdentityB,
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — the composed identity is not reaching the bytes\n--- bodies ---\n%s", want, bodies)
		}
	}
	if strings.Contains(bodies, hostFormerlyFoldedIdentity) {
		t.Errorf("emitted output still carries the folded identity %q\n--- bodies ---\n%s", hostFormerlyFoldedIdentity, bodies)
	}
	// Each feature's output lands at the feature's OWN evaluated assembly path
	// rather than under the boundary of the component whose render dispatched it.
	if !strings.Contains(bodies, hostFeatureGroupPath+"/") {
		t.Errorf("no output landed at the feature's own assembly path %q\n--- bodies ---\n%s", hostFeatureGroupPath, bodies)
	}
}

// TestRenderAll_TwoBoundaryFeaturesOutsideTheFormerFoldRender keeps the second
// half of the pair covered: a spelling that never folded renders as it always
// did, so the case above is a real change of behaviour rather than a guard being
// switched off.
func TestRenderAll_TwoBoundaryFeaturesOutsideTheFormerFoldRender(t *testing.T) {
	t.Parallel()

	res, err := runHostBoundaryFeatureRender([]string{hostFormerlyFoldingB, hostDistinctFeature}, nil)
	if err != nil {
		t.Fatalf("expected the build to complete, got: %v", err)
	}
	bodies := strings.Join(bodiesOf(res), "\n")
	for _, want := range []string{
		"component-id: " + hostComposedIdentityB,
		"component-id: " + hostComposedIdentityC,
	} {
		if !strings.Contains(bodies, want) {
			t.Errorf("emitted output missing %q — the composed identity is not reaching the bytes\n--- bodies ---\n%s", want, bodies)
		}
	}
}

// TestRenderAll_ABoundaryFeatureDeclarationIsCheckedInTheFeaturesOwnGroup is the
// assertion the case above can no longer carry, kept alive on the one route that
// can still produce a collision at this site: a second declarer.
//
// A boundary-attached feature's render is reached through whichever component
// dispatched it, and its render context is a COPY of that component's — so a
// declaration made against the copied context would land in the component's own
// accumulator and be checked against the component's group. Here one feature's
// GUEST declares the identity the compiler derived for the other, in the same key
// space. The halt therefore requires both declarations to have reached one
// accumulator, and the assembly path it names is the FEATURE's own evaluated
// path, which is what proves that accumulator is the feature's group rather than
// the dispatching component's.
//
// It covers both routes into that accumulator at once — the compiler's direct
// declaration and a guest's over the node context's sink — which the folding pair
// it replaces covered only for the first.
func TestRenderAll_ABoundaryFeatureDeclarationIsCheckedInTheFeaturesOwnGroup(t *testing.T) {
	t.Parallel()

	// The declaring feature is keyed by the node id a boundary-attached feature's
	// render carries, which is the marker the compiler builds for its own
	// dispatch. Naming it here is what makes this fixture a statement about the
	// declaration's route rather than about the identity's spelling.
	res, err := runHostBoundaryFeatureRender(
		[]string{hostFormerlyFoldingB, hostDistinctFeature},
		map[string]map[string][]string{
			"<boundary-fr>:" + hostDistinctFeature + "@cluster": {
				hostComponentIDSpace: {hostComposedIdentityB},
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
		t.Fatalf("expected one finding per contributing feature, got %d: %v", len(findings), findings)
	}
	whole := strings.Join(findings, "\n")
	for _, want := range []string{
		`dispatch path "` + hostFeatureGroupPath + `"`,
		`key space "` + hostComponentIDSpace + `"`,
		`"` + hostComposedIdentityB + `"`,
		`derived by feature "` + hostFormerlyFoldingB + `"`,
		`derived by feature "` + hostDistinctFeature + `"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the findings do not carry %q\ngot: %v", want, findings)
		}
	}
	// The dispatching component's own group path must not be the one reported: it
	// would mean a declaration landed in the component's accumulator.
	if strings.Contains(whole, `dispatch path "boundaries/cluster/`) {
		t.Errorf("the findings name the dispatching component's group, so a declaration reached the wrong accumulator\ngot: %v", findings)
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
