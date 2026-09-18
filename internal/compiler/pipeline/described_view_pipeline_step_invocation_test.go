// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The workspace these proofs compile. It is its own fixture because no other one
// carries what they need together: a component a sub-module contributes whose
// Recipe declares a pipeline, an attachment on a component whose FeatureRecipe
// declares one, and an attachment declared at a boundary whose FeatureRecipe
// declares one. Before it, the third of those existed nowhere in the tree at all,
// so a rule over it could be invoked and would report on nothing.
//
// # Three deployments over one Aggregate, each differing in one binding
//
// The Aggregate, the attachments, their FeatureRecipes and the step plugin are
// the same documents in all three. What moves between them is which Recipe the
// component type binds, and that single authored difference is what each proof
// turns on.
//
// The first is admitted: every step's plugin declares the provider rendering the
// documents that step addresses. The second binds a Recipe whose declared
// provider the step plugin does not list, so the build halts on the
// compatibility condition. The third binds a Recipe whose provider folds its
// dispatch group into one document, so an attachment's own-scope step has no
// discrete document left to address and the build halts on that condition
// instead.
//
// Each refusing deployment was arranged so exactly ONE of the four declared
// rules raises. That is not tidiness: the render reports a single finding as
// itself and joins several under an envelope carrying no position of its own, so
// a build raising two unrelated findings could not have either one's position
// read.
const (
	stepAggregatePath = "../../../internal/compiler/testdata/pipeline-provider-compat/aggregate.yaml"
	stepReleasePath   = "../../../internal/compiler/testdata/pipeline-provider-compat/release.yaml"
	stepManifestPath  = "../../../internal/compiler/testdata/pipeline-provider-compat/manifest.yaml"
	stepCatalogPath   = "../../../internal/compiler/testdata/pipeline-provider-compat/catalog"

	// The deployment whose component renders through a provider the step plugin
	// its own Recipe's pipeline names declares no compatibility with. Its build
	// halts, and the finding names that provider.
	stepIncompatibleOwnManifestPath = "../../../internal/compiler/testdata/pipeline-provider-compat/manifest-incompatible-own.yaml"
	stepIncompatibleOwnReleasePath  = "../../../internal/compiler/testdata/pipeline-provider-compat/release-incompatible-own.yaml"

	// The deployment whose component renders through a provider that folds its
	// dispatch group into a single document. Its build halts on the attachment's
	// own-scope step, which has no discrete document to address.
	stepMergedTargetManifestPath = "../../../internal/compiler/testdata/pipeline-provider-compat/manifest-merged-target.yaml"
	stepMergedTargetReleasePath  = "../../../internal/compiler/testdata/pipeline-provider-compat/release-merged-target.yaml"

	// The plugin every pipeline in the fixture names, and the artifact behind it.
	// Its own self-report declares compatibility with the provider the admitted
	// deployment's entities declare, so an admitted build is admitted because the
	// declaration matches rather than because nothing was read.
	stepPluginURI      = "pipeline-step/patch:1.0.0"
	stepPluginArtifact = "frp-step-patch-rust.wasm"

	// The component the sub-module contributes, under the identity the composed
	// graph gives it. Spelled here so a fixture that stopped contributing it fails
	// loudly rather than leaving a proof asserting nothing.
	contributedNode = "data.main-db"

	// The coordinates the refusal proofs are positioned at. Spelled here so a
	// fixture that stopped carrying them fails loudly rather than leaving a proof
	// asserting nothing.
	stepNode           = "api"
	stepAttachment     = "request-guard"
	stepAttachmentStep = "annotate_attachment_output"
)

// stepBuild is one compiled and rendered deployment of the fixture, together
// with the render's own error rather than a failure — so a proof about a build
// the render refuses can read that refusal.
type stepBuild struct {
	out           *stepsOneToFourOutput
	catalogLookup resolver.CatalogLookup
	recipeLookup  renderer.RecipeLookup
	lookup        pluginpath.Lookup
	result        *renderer.RenderResult
	renderErr     error
	coordinates   renderer.GraphCoordinates
	axes          stepAxes
}

// buildStepFixture compiles one deployment of the fixture, resolves its
// pipeline-step plugin through the real resolution cascade, and renders it the
// way the production spine renders: through the same provider dispatch, with the
// plugin resolution wired, and with the enumeration the finalised graph carries
// handed to the render.
//
// Three things here cannot be left out, and each of them silently empties a
// proof over this build when it is:
//
// The enumeration is what makes the rules run at all. The render invokes the
// rules declared over what its plugins report about themselves only when it is
// given the finalised graph's enumeration. A render without it asks nothing and
// completes over a workspace those rules refuse, leaving a refusal observable
// only by re-asking beside the render; with it, the halt a refused build returns
// IS the rule's verdict.
//
// The plugin resolution is what makes the answers real. A render wired with no
// resolution runs no pipeline and describes no plugin, so the presentation
// answers every plugin fact with its zero value — and the rules are not invoked
// over such a build at all, precisely because an unread self-report and a plugin
// declaring nothing are indistinguishable.
//
// The provider dispatch has to be the build's own rather than a stand-in,
// because two of the facts these rules read are the provider guest's own claims
// about itself. A stand-in engine that makes no assembly claim answers the empty
// string, which is neither of the two answers a proof needs to tell apart.
func buildStepFixture(t *testing.T, manifestPath, releasePath string) stepBuild {
	t.Helper()

	catalogPath := stageStepPlugin(t)
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: stepAggregatePath,
		releasePath:   releasePath,
		manifestPath:  manifestPath,
		catalogPath:   catalogPath,
	}, bytesOf(t, stepAggregatePath))
	if out.GraphCoordinates == nil {
		t.Fatal("the compilation produced no enumeration of the finalised graph; the render would invoke no rule over it")
	}

	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", catalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	recipeLookup := cat.RecipeLookupWithManifest(manifest)

	lookup := buildEntityPluginLookup(
		fixtureInputs(), catalogLookup,
	)

	result, renderErr := renderStepFixture(t, out, catalogLookup, recipeLookup, lookup)

	var release *types.Release
	if out.ResolveResult != nil {
		release = out.ResolveResult.Release
	}
	return stepBuild{
		out:           out,
		catalogLookup: catalogLookup,
		recipeLookup:  recipeLookup,
		lookup:        lookup,
		result:        result,
		renderErr:     renderErr,
		coordinates:   out.GraphCoordinates,
		axes: stepAxes{
			graph:        out.IRGraph,
			recipeLookup: recipeLookup,
			release:      release,
			releaseFile:  releasePath,
		},
	}
}

// renderStepFixture renders the compiled deployment with the plugin resolution
// wired, the finalised graph's enumeration supplied, and the build's own provider
// dispatch — and returns the render's own error rather than failing, so a proof
// about a build the render refuses can read that refusal.
func renderStepFixture(
	t *testing.T,
	out *stepsOneToFourOutput,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
	lookup pluginpath.Lookup,
) (*renderer.RenderResult, error) {
	t.Helper()

	ctx := context.Background()
	sharedLoader := wasm.NewCachingLoaderFromContext(ctx, wasm.ExternalInputsFromContext(ctx))
	factory, providerVersion, closeEngines := enginefactory.NewHybrid(catalogLookup, lookup, sharedLoader)
	t.Cleanup(func() { _ = closeEngines(ctx) })

	return renderer.RenderAll(ctx, renderer.RendererInput{
		Graph:            out.IRGraph,
		GraphView:        out.GraphView,
		GraphCoordinates: out.GraphCoordinates,
		FeatureOrder:     out.FeatureOrder,
		ResolveResult:    out.ResolveResult,
		CatalogLookup:    catalogLookup,
		PluginPathLookup: lookup,
		ProviderVersion:  providerVersion,
	}, recipeLookup, factory)
}

// stepFindings collects every categorised diagnostic a halt carries, walking both
// a single wrapped cause and a joined set of them.
//
// The render reports one finding as itself and joins several under an envelope
// whose own position is empty, so a proof that reads a position off the halt has
// to reach the members either way. Reading them structurally rather than
// searching the rendered text is the other half: a categorised diagnostic renders
// only its own frame in Error, so a string search over the halt reports nothing
// whichever shape the chain has.
func stepFindings(err error) []*cerrors.CompilerError {
	var out []*cerrors.CompilerError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if joined, isMulti := e.(interface{ Unwrap() []error }); isMulti {
			for _, member := range joined.Unwrap() {
				walk(member)
			}
			return
		}
		var finding *cerrors.CompilerError
		if !errors.As(e, &finding) {
			return
		}
		// An envelope over a joined set carries no position of its own; it is
		// the members beneath it that are positioned.
		if finding.Source().Locator != "" || finding.Source().File != "" {
			out = append(out, finding)
		}
		walk(finding.Unwrap())
	}
	walk(err)
	return out
}

// stageStepPlugin stages this file's fixture catalog with its step plugin's
// artifact written beside the plugin's own entity, and returns the copied
// catalog's path.
func stageStepPlugin(t *testing.T) string {
	t.Helper()
	return stageColocatedStepPlugin(t, stepCatalogPath, stepPluginURI, stepPluginArtifact)
}

// artifactReferenceOf reads the artifact reference one Plugin entity declares,
// through the catalog's own accessor, so the staging key is the key the cascade
// resolves by rather than one spelled out here.
func artifactReferenceOf(t *testing.T, cat *catalog.Catalog, uri string) string {
	t.Helper()
	entity, held := cat.CatalogLookup().Plugin(uri)
	if !held {
		t.Fatalf("the catalog holds no plugin entity at %q", uri)
	}
	return entity.ArtifactReference()
}

// stepAxes is a second, independent account of the axes the step rules are
// invoked once per: a pipeline's steps, the boundary set, and a boundary's own
// attachments.
//
// The render derives these axes itself and is what the rules are invoked over in
// production, so this is deliberately NOT the thing under test — it is the
// cross-check against it. Keeping a separate account is the point: a proof that
// stated what the enumeration contains by asking the enumeration would agree with
// itself whatever the enumeration did, so the set each axis is asserted to hold
// is spelled out here and read from the compiled products directly. Anyone
// tempted to collapse the two into one reader would remove the only thing that
// can disagree.
//
// The node axis and the per-node attachment axis are NOT here: the compilation
// itself enumerates both, the facts are keyed by what it produced, and taking
// them from anywhere else would present the rules a coordinate set the facts were
// not keyed by. Each axis that IS here is read from the same product the
// presentation reads the matching fact from, so a rule is invoked for exactly the
// coordinates it can then be told something about.
type stepAxes struct {
	// graph is the finalised graph, read for the attachment-to-FeatureRecipe
	// binding, which is what the presentation reads it for too.
	graph *types.IRGraph

	// recipeLookup is the binding the compilation resolved, so a pipeline
	// enumerated here is the pipeline the render ran.
	recipeLookup renderer.RecipeLookup

	// release is the deployment document. The boundary set is read from it and
	// not from the graph's projection of boundary attachments: that projection
	// records a boundary only once something is attached at it, so an axis over it
	// would be narrower than the boundary set an implementation is asked for, and
	// would narrow silently — every boundary it dropped contributes no subject, so
	// nothing would fail.
	release *types.Release

	// releaseFile positions a boundary coordinate at the document the boundary was
	// declared in.
	releaseFile string
}

// PipelineSteps returns the steps of a node's own Recipe pipeline, in the order
// that Recipe declares them, keyed by the identifier each step declares.
//
// The Recipe is resolved through the binding the compilation made, keyed by the
// identity the composed graph gives the node — so a component a sub-module
// contributed is reached here exactly as it is reached by the facts about it. A
// reading keyed by the name a root document typed would answer nothing for such a
// component and present its steps to no rule.
func (a stepAxes) PipelineSteps(node string) []constraint.AuthoredCoordinate {
	recipe := recipeOfNode(a.graph, a.recipeLookup, node)
	if recipe == nil {
		return nil
	}
	return stepCoordinates(recipe.Spec.Pipeline, recipe.Metadata.Name)
}

// AttachmentPipelineSteps returns the steps of the pipeline one attachment's
// FeatureRecipe carries on a node, in the order that FeatureRecipe declares them.
//
// Each coordinate carries the FeatureRecipe's own document, because that is the
// document an author edits to change the step — not the one the attachment was
// written in.
func (a stepAxes) AttachmentPipelineSteps(node string, attachment string) []constraint.AuthoredCoordinate {
	fr, held := a.attachmentFeatureRecipe(node, attachment)
	if !held {
		return nil
	}
	return stepCoordinates(fr.Spec.Pipeline, fr.Metadata.Name)
}

// Boundaries returns every boundary the deployment declares, at every depth,
// enclosing scope before the scopes nested inside it.
//
// A scope normalization synthesized carries no path of its own and contributes
// none: the operator declared no such scope, so a coordinate naming one would
// name a key the document does not contain.
func (a stepAxes) Boundaries() []constraint.AuthoredCoordinate {
	var out []constraint.AuthoredCoordinate
	a.walkBoundaries(func(path string, _ types.Boundary) {
		out = append(out, constraint.AuthoredCoordinate{Value: path, SourceFile: a.releaseFile})
	})
	return out
}

// BoundaryAttachments returns the attachments one boundary declares on itself, in
// the order the deployment declares them.
func (a stepAxes) BoundaryAttachments(boundary string) []string {
	var out []string
	a.walkBoundaries(func(path string, b types.Boundary) {
		if path == boundary {
			out = b.Features.Keys()
		}
	})
	return out
}

// BoundaryAttachmentPipelineSteps returns the steps of the pipeline one
// boundary-scope attachment's FeatureRecipe carries, in the order that
// FeatureRecipe declares them.
func (a stepAxes) BoundaryAttachmentPipelineSteps(
	boundary string, attachment string,
) []constraint.AuthoredCoordinate {
	fr, held := a.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !held {
		return nil
	}
	return stepCoordinates(fr.Spec.Pipeline, fr.Metadata.Name)
}

// walkBoundaries visits every declared boundary with the path it is named by,
// descending into nested scopes, in the deployment's own order.
func (a stepAxes) walkBoundaries(visit func(path string, b types.Boundary)) {
	if a.release == nil {
		return
	}
	rel := types.NormalizeRelease(a.release).Release()
	if rel == nil {
		return
	}
	var walk func(path string, b types.Boundary)
	walk = func(path string, b types.Boundary) {
		if !b.Synthesized {
			visit(path, b)
		}
		for name, child := range b.Boundaries.All() {
			next := name
			if path != "" {
				next = path + "/" + name
			}
			walk(next, child)
		}
	}
	for name, b := range rel.Spec.Boundaries.All() {
		start := name
		if b.Synthesized {
			start = ""
		}
		walk(start, b)
	}
}

// attachmentFeatureRecipe resolves the FeatureRecipe one attachment binds on a
// node, through the same binding and the same instance-plus-type key the render
// resolves attachments with.
func (a stepAxes) attachmentFeatureRecipe(
	node string, attachment string,
) (*types.FeatureRecipe, bool) {
	if a.recipeLookup.FeatureRecipe == nil || a.graph == nil {
		return nil, false
	}
	entries, held := a.graph.Spec.FeatureValuesMapping.Get(node)
	if !held {
		return nil, false
	}
	entry, held := entries.Get(attachment)
	if !held {
		return nil, false
	}
	fr, found := a.recipeLookup.FeatureRecipe(attachment, entry.Type)
	if !found || fr == nil {
		return nil, false
	}
	return fr, true
}

// boundaryAttachmentFeatureRecipe resolves the FeatureRecipe one boundary-scope
// attachment binds, through the same binding the render resolves it with.
func (a stepAxes) boundaryAttachmentFeatureRecipe(
	boundary string, attachment string,
) (*types.FeatureRecipe, bool) {
	if a.recipeLookup.FeatureRecipe == nil || a.graph == nil {
		return nil, false
	}
	feats, held := a.graph.Spec.BoundaryFeatures.Get(boundary)
	if !held {
		return nil, false
	}
	feat, held := feats.Get(attachment)
	if !held {
		return nil, false
	}
	fr, found := a.recipeLookup.FeatureRecipe(attachment, feat.Type)
	if !found || fr == nil {
		return nil, false
	}
	return fr, true
}

// stepCoordinates presents a declared pipeline as the coordinate shape the step
// traversals take, keeping the order the document declares.
func stepCoordinates(steps []types.PipelineStep, sourceFile string) []constraint.AuthoredCoordinate {
	if len(steps) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(steps))
	for _, s := range steps {
		out = append(out, constraint.AuthoredCoordinate{Value: s.ID, SourceFile: sourceFile})
	}
	return out
}

// The axes are held to the enumerator contracts they satisfy, so an axis that
// stops satisfying one fails to compile here rather than at the invocation that
// needed it.
var (
	_ constraint.PipelineStepEnumerator                   = stepAxes{}
	_ constraint.AttachmentPipelineStepEnumerator         = stepAxes{}
	_ constraint.BoundaryEnumerator                       = stepAxes{}
	_ constraint.BoundaryAttachmentEnumerator             = stepAxes{}
	_ constraint.BoundaryAttachmentPipelineStepEnumerator = stepAxes{}
)

// TestThePipelineStepAxesComeFromTheCompiledBuild is the enumeration proof for
// the three axes the provider-compatibility rules are invoked once per.
//
// An axis that answered emptily would present its rule no subject at all, which
// reads exactly like a rule that ran and approved — so what each axis answers is
// asserted before any rule is asked anything, and the case that separates a
// correct reading from one blind to a component no root document declares is
// asserted by name.
func TestThePipelineStepAxesComeFromTheCompiledBuild(t *testing.T) {
	build := buildStepFixture(t, stepManifestPath, stepReleasePath)
	if build.renderErr != nil {
		t.Fatalf("the render refused a build this proof needs completed: %+v", build.renderErr)
	}

	t.Run("a node's own pipeline is enumerated for every component the graph holds", func(t *testing.T) {
		if got := build.axes.PipelineSteps("api"); !stepIDsEqual(got, []string{"annotate_own_output"}) {
			t.Errorf("PipelineSteps(%q) = %v, want the one step its Recipe declares", "api", stepIDs(got))
		}
		// The case the fixture exists for. This component is contributed by a
		// sub-module, so it is reachable only through the identity the composed
		// graph gives it; a reading keyed by an authored name holds no entry for it.
		if _, held := build.out.IRGraph.Spec.Nodes.Get(contributedNode); !held {
			t.Fatalf("the fixture no longer contributes %q; this proof would assert nothing", contributedNode)
		}
		if got := build.axes.PipelineSteps(contributedNode); !stepIDsEqual(got, []string{"annotate_contributed_output"}) {
			t.Errorf("PipelineSteps(%q) = %v, want the one step the contributed component's Recipe declares",
				contributedNode, stepIDs(got))
		}
		if got := build.axes.PipelineSteps("no-such-node"); len(got) != 0 {
			t.Errorf("PipelineSteps for a component the graph does not hold = %v", stepIDs(got))
		}
	})

	t.Run("an attachment's pipeline is enumerated per node and attachment", func(t *testing.T) {
		attachments := build.coordinates.Attachments("api")
		if !slices.Contains(attachments, "request-guard") {
			t.Fatalf("the compilation attaches %v to %q; the attachment proof needs its own attachment", attachments, "api")
		}
		got := build.axes.AttachmentPipelineSteps("api", "request-guard")
		if !stepIDsEqual(got, []string{"annotate_attachment_output"}) {
			t.Errorf("AttachmentPipelineSteps(%q, %q) = %v, want the one step its FeatureRecipe declares",
				"api", "request-guard", stepIDs(got))
		}
		if got := build.axes.AttachmentPipelineSteps("api", "no-such-attachment"); len(got) != 0 {
			t.Errorf("AttachmentPipelineSteps for an attachment the node does not carry = %v", stepIDs(got))
		}
	})

	t.Run("the boundary set is every declared boundary, not only the attached ones", func(t *testing.T) {
		// The discriminating assertion for this axis. The fixture declares two
		// boundaries and attaches at one, so a reading taken from the graph's
		// projection of boundary attachments would answer one — and would answer it
		// without failing anything, because a boundary carrying no attachment
		// contributes no step subject either way.
		var paths []string
		for _, b := range build.axes.Boundaries() {
			paths = append(paths, b.Value)
			if b.SourceFile == "" {
				t.Errorf("boundary %q carries no source file", b.Value)
			}
		}
		if !slices.Equal(paths, []string{"main", "store", "spare"}) {
			t.Errorf("Boundaries() = %v, want every boundary the deployment declares", paths)
		}
		if projected := build.out.IRGraph.Spec.BoundaryFeatures.Len(); projected >= len(paths) {
			t.Errorf("the graph projects %d boundary attachment set(s) against %d declared boundaries; the axis is no longer distinguishable from that projection",
				projected, len(paths))
		}
		if got := build.axes.BoundaryAttachments("main"); !slices.Equal(got, []string{"boundary-guard"}) {
			t.Errorf("BoundaryAttachments(%q) = %v, want the one attachment the boundary declares", "main", got)
		}
		for _, bare := range []string{"store", "spare"} {
			if got := build.axes.BoundaryAttachments(bare); len(got) != 0 {
				t.Errorf("BoundaryAttachments(%q) = %v for a boundary that declares none", bare, got)
			}
		}
		got := build.axes.BoundaryAttachmentPipelineSteps("main", "boundary-guard")
		if !stepIDsEqual(got, []string{"annotate_boundary_output"}) {
			t.Errorf("BoundaryAttachmentPipelineSteps(%q, %q) = %v, want the one step its FeatureRecipe declares",
				"main", "boundary-guard", stepIDs(got))
		}
	})

	t.Run("the view answers for every coordinate the axes present", func(t *testing.T) {
		// The property that makes these axes usable rather than merely populated: a
		// rule invoked for a coordinate the presentation cannot answer about reads a
		// zero value and approves. Each enumerated step is therefore checked against
		// the plugin self-report the presentation holds for it.
		view := build.result.DescribedView
		if view == nil {
			t.Fatal("the render published no view of what its plugins report")
		}
		checked := 0
		for _, node := range build.coordinates.Nodes() {
			for _, step := range build.axes.PipelineSteps(node.ID) {
				if caps := view.StepPluginCompatibleCapabilities(node.ID, step.Value); len(caps) == 0 {
					t.Errorf("the view reports no declared provider for step %q of %q", step.Value, node.ID)
				}
				checked++
			}
			for _, attachment := range build.coordinates.Attachments(node.ID) {
				for _, step := range build.axes.AttachmentPipelineSteps(node.ID, attachment) {
					if caps := view.AttachmentStepPluginCompatibleCapabilities(
						node.ID, attachment, step.Value,
					); len(caps) == 0 {
						t.Errorf("the view reports no declared provider for step %q of attachment %q on %q",
							step.Value, attachment, node.ID)
					}
					checked++
				}
			}
		}
		for _, boundary := range build.axes.Boundaries() {
			for _, attachment := range build.axes.BoundaryAttachments(boundary.Value) {
				for _, step := range build.axes.BoundaryAttachmentPipelineSteps(boundary.Value, attachment) {
					if caps := view.BoundaryAttachmentStepPluginCompatibleCapabilities(
						boundary.Value, attachment, step.Value,
					); len(caps) == 0 {
						t.Errorf("the view reports no declared provider for step %q of boundary attachment %q at %q",
							step.Value, attachment, boundary.Value)
					}
					checked++
				}
			}
		}
		if checked == 0 {
			t.Fatal("the axes presented no coordinate at all; every assertion above passed on an empty set")
		}
		t.Logf("the view answered a declared provider set for all %d enumerated step(s)", checked)
	})
}

// TestTheProviderCompatibilityRulesAreInvokedOverTheCompiledBuild invokes each of
// the three rules through the framework's traversal over the presentation the
// render published, and states what each was invoked over.
//
// The build the fixture compiles is one the rules admit, and the render completed
// — so this is the admission half: each rule is asked about every step the build
// really declares, over the presentation the build really published, and raises
// nothing. The refusal half is the proof below it, and the two together are what
// keeps either from passing on a rule that answers one way whatever it is asked.
func TestTheProviderCompatibilityRulesAreInvokedOverTheCompiledBuild(t *testing.T) {
	build := buildStepFixture(t, stepManifestPath, stepReleasePath)
	if build.renderErr != nil {
		t.Fatalf("the render refused a build this proof needs completed: %+v", build.renderErr)
	}
	if build.result == nil || build.result.DescribedView == nil {
		t.Fatal("the render published no view of what its plugins report; nothing can be invoked through it")
	}
	view := build.result.DescribedView

	t.Run("a node's own pipeline is admitted at every enumerated step", func(t *testing.T) {
		visited := 0
		for _, node := range build.coordinates.Nodes() {
			visited += len(build.axes.PipelineSteps(node.ID))
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no step; a verdict from it would certify nothing")
		}

		objections, err := constraint.RunOnNodePipelineSteps(
			nil, build.coordinates, build.axes, view,
			renderer.PipelineStepProviderCompatible,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}

		t.Logf("rule invoked over %d step(s) across %d component(s); every one admitted",
			visited, len(build.coordinates.Nodes()))
	})

	t.Run("an attachment's pipeline is admitted at every enumerated step", func(t *testing.T) {
		visited := 0
		for _, node := range build.coordinates.Nodes() {
			for _, attachment := range build.coordinates.Attachments(node.ID) {
				visited += len(build.axes.AttachmentPipelineSteps(node.ID, attachment))
			}
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no attachment step; a verdict from it would certify nothing")
		}

		objections, err := constraint.RunOnNodeAttachmentPipelineSteps(
			nil, build.coordinates, build.coordinates, build.axes, view,
			renderer.AttachmentPipelineStepProviderCompatible,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}

		t.Logf("rule invoked over %d attachment step(s); every one admitted", visited)
	})

	t.Run("a boundary attachment's pipeline is admitted at every enumerated step", func(t *testing.T) {
		visited := 0
		for _, boundary := range build.axes.Boundaries() {
			for _, attachment := range build.axes.BoundaryAttachments(boundary.Value) {
				visited += len(build.axes.BoundaryAttachmentPipelineSteps(boundary.Value, attachment))
			}
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no boundary-attachment step; a verdict from it would certify nothing")
		}

		objections, err := constraint.RunOnBoundaryAttachmentPipelineSteps(
			nil, build.axes, build.axes, build.axes, view,
			renderer.BoundaryAttachmentPipelineStepProviderCompatible,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}

		t.Logf("rule invoked over %d boundary-attachment step(s); every one admitted", visited)
	})

	t.Run("one authored step reaches two axes, and the scope fact decides which rule answers", func(t *testing.T) {
		// An attachment declared at a boundary reaches every component that boundary
		// holds, so it appears in each of those components' merged attachment sets.
		// Both axes therefore present the same authored step — the per-component one
		// through every component that inherited the attachment, the boundary-rooted
		// one once.
		//
		// The build evaluates it once: the pipeline of an attachment whose
		// FeatureRecipe declares the boundary scope runs on the boundary's own path,
		// and the per-component path ends on it. The rule over a component's
		// attachments reads that same scope and ends on it too, so the declared set
		// reports the step once rather than once per component. This asserts the
		// shared reach — which is real and is what makes the scope read necessary —
		// and that the scope fact answers the boundary scope on every appearance.
		const declaredAtBoundary = "boundary-guard"
		carrying := 0
		for _, node := range build.coordinates.Nodes() {
			if !slices.Contains(build.coordinates.Attachments(node.ID), declaredAtBoundary) {
				continue
			}
			carrying++
			if got := view.AttachmentExecutionScope(node.ID, declaredAtBoundary); got != "boundary" {
				t.Errorf("the scope of %q as %q carries it = %q, want the boundary scope",
					declaredAtBoundary, node.ID, got)
			}
		}
		if carrying < 2 {
			t.Fatalf("the attachment %q is carried by %d component(s); the shared reach this asserts needs more than one",
				declaredAtBoundary, carrying)
		}
		viaComponent := build.axes.AttachmentPipelineSteps("api", declaredAtBoundary)
		viaBoundary := build.axes.BoundaryAttachmentPipelineSteps("main", declaredAtBoundary)
		if len(viaComponent) == 0 || !slices.Equal(stepIDs(viaComponent), stepIDs(viaBoundary)) {
			t.Errorf("the same authored step reaches the two axes as %v and %v", stepIDs(viaComponent), stepIDs(viaBoundary))
		}
		t.Logf("the authored step %v is presented through %d component(s) and once at its boundary; "+
			"the scope fact reports the boundary scope at every appearance",
			stepIDs(viaBoundary), carrying)
	})
}

// TestTheProviderCompatibilityRuleRefusesAnUndeclaredProvider reads the halt a
// real build returns when a component's own pipeline step is matched against a
// provider its plugin does not declare.
//
// # Nothing is substituted here
//
// The rule runs inside the render, ahead of the component walk, so a build
// carrying the refused shape halts and publishes no presentation — and the halt
// IS the verdict, read from a compilation of authored documents with every fact
// the build itself produced. The deployment differs from the admitted one in a
// single binding: which Recipe the component type binds, and therefore which
// provider renders the component's own documents.
//
// # Why the finding is attributable, and why it is not the halt itself
//
// The provider the deployment names is one the step plugin does not declare, and
// the plugin's declared set is read off the build and asserted — so a plugin that
// came to declare that provider fails this proof rather than making it vacuous.
//
// The step plugin declares the provider the ATTACHMENT on the same component
// renders through, which is what makes the finding say which documents' provider
// the step was matched against: it must name the component's own provider and
// must not name the attachment's. A rule that raised for any reason at all would
// satisfy "the build halts" and could not satisfy that.
//
// # Why the refused pipeline carries two steps
//
// A finding per authored step and a finding per component are the same count on a
// one-step pipeline, so the arity claim would hold for an implementation that
// reported once per component. The refused Recipe declares two steps and the
// locators are asserted as a SET, which separates them and names whichever is
// missing.
func TestTheProviderCompatibilityRuleRefusesAnUndeclaredProvider(t *testing.T) {
	build := buildStepFixture(t, stepIncompatibleOwnManifestPath, stepIncompatibleOwnReleasePath)

	recipe := recipeOfNode(build.out.IRGraph, build.recipeLookup, stepNode)
	if recipe == nil || len(recipe.Spec.Pipeline) == 0 {
		t.Fatalf("the compilation bound no pipeline-carrying Recipe to %q; there is nothing to refuse", stepNode)
	}
	fr, held := build.axes.attachmentFeatureRecipe(stepNode, stepAttachment)
	if !held || fr == nil {
		t.Fatalf("the compilation bound no FeatureRecipe to attachment %q on %q", stepAttachment, stepNode)
	}
	nodeProvider := renderer.ProviderEngineKind(recipe.Spec.Capability)
	attachmentProvider := renderer.ProviderEngineKind(fr.Spec.Capability)

	t.Run("the exercise contains two own-addressed steps whose provider the plugin does not declare", func(t *testing.T) {
		// The enumeration, stated as a set rather than a count: a count is
		// satisfied by the wrong coordinate as readily as the right one.
		enumerated := stepIDs(build.axes.PipelineSteps(stepNode))
		want := []string{"annotate_own_output", "annotate_own_output_again"}
		if !slices.Equal(enumerated, want) {
			t.Fatalf("the component's own pipeline enumerates as %v; want %v", enumerated, want)
		}
		if nodeProvider == "" {
			t.Fatal("the component reports no provider; the rule ends on that and would raise nothing")
		}
		if attachmentProvider == nodeProvider {
			t.Fatalf("the component and its attachment both render through %q; a finding naming it would not say which was read",
				nodeProvider)
		}
		t.Logf("two own-addressed steps %v: the component renders through %q and its attachment through %q",
			want, nodeProvider, attachmentProvider)
	})

	t.Run("the build halts once per authored step, naming the component's provider", func(t *testing.T) {
		if build.renderErr == nil {
			t.Fatal("the render admitted a step matched against a provider its plugin does not declare")
		}
		if !errors.Is(build.renderErr, renderer.ErrPluginProviderIncompatible()) {
			t.Fatalf("the halt does not carry the incompatibility condition: %+v", build.renderErr)
		}
		// The one condition the halt must NOT carry. Every other declared rule
		// over these axes is satisfied by this deployment, so a second sentinel
		// here would mean the refusal is not attributable to this one.
		if errors.Is(build.renderErr, renderer.ErrPipelinePatchPreAssembly()) {
			t.Errorf("the halt also carries the no-merged-target condition; the refusal is not attributable: %+v",
				build.renderErr)
		}

		findings := stepFindings(build.renderErr)
		if len(findings) != len(authoredOwnSteps(build)) {
			t.Fatalf("the halt carries %d positioned finding(s) over a %d-step pipeline; want one per step",
				len(findings), len(authoredOwnSteps(build)))
		}

		// A set difference rather than a count, so a finding landing twice on one
		// step and never on the other is named rather than absorbed.
		positioned := map[string]int{}
		for _, finding := range findings {
			// Where an author fixes it: the authored step inside the entity that
			// declares it, which is neither the Aggregate nor the deployment.
			if finding.Source().File != recipe.Metadata.Name {
				t.Errorf("finding positioned in %q; want the entity that declares the step, %q",
					finding.Source().File, recipe.Metadata.Name)
			}
			positioned[finding.Source().Locator]++

			// The attribution. The step was matched against the component's own
			// provider, so the finding names that one and not the attachment's.
			if !strings.Contains(finding.Message(), `"`+nodeProvider+`"`) {
				t.Errorf("finding %q does not name the component's provider %q", finding.Message(), nodeProvider)
			}
			if strings.Contains(finding.Message(), `"`+attachmentProvider+`"`) {
				t.Errorf("finding %q names the attachment's provider %q; it was matched against the wrong documents' provider",
					finding.Message(), attachmentProvider)
			}
		}
		for _, step := range authoredOwnSteps(build) {
			locator := ".spec.pipeline." + step
			if got := positioned[locator]; got != 1 {
				t.Errorf("the halt positions %d finding(s) at %q; want exactly one", got, locator)
			}
		}
		t.Logf("the build halted with %d finding(s) in %q, one per authored step, naming %q",
			len(findings), recipe.Metadata.Name, nodeProvider)
	})
}

// authoredOwnSteps is the component's own authored step set, read from the same
// axis the rule is invoked over so an assertion against it cannot drift from what
// the build enumerates.
func authoredOwnSteps(build stepBuild) []string {
	return stepIDs(build.axes.PipelineSteps(stepNode))
}

// TestTheOwnScopeMergedTargetRuleRefusesAStepWithNoTarget reads the halt a real
// build returns when an attachment's own-scope pipeline step stands against a
// component whose provider folds its dispatch group into one document.
//
// This is the finding the three compatibility rules do not carry, and it is why
// four rules stand over these axes rather than three. An attachment's own output
// is written under its own position as discrete files whichever provider the
// component uses, so a step addressing that output against a component whose
// provider merges its dispatch group has nothing to address by the time it runs.
//
// # Nothing is substituted in either direction
//
// The two facts the rule pairs are the step's declared scope, which is the
// plugin's own self-report, and how the component's provider assembles a dispatch
// group, which is the provider guest's own self-report. Both are read from the
// build through the presentation the render published, and the deployment decides
// the second by binding a different Recipe — the merging provider in the refusal,
// the non-merging one in the admission — so each direction is a compilation of
// authored documents rather than a value chosen here.
//
// That is what makes the admission attributable as well as the refusal. A build
// rendered through a stand-in provider makes no assembly claim at all and answers
// the empty string, which is not "does not merge": an admission read from it would
// hold for a rule that never read the fact.
//
// # Why the division of labour is asserted on the halt itself
//
// The reason this rule exists rather than a branch on the compatibility rule is
// that the compatibility rule is silent on this input. Asserting that the halt
// carries THIS condition and does not carry the compatibility one says it over the
// whole declared set at once: had any other rule raised, its sentinel would be in
// the halt and the finding count would exceed the authored step count.
func TestTheOwnScopeMergedTargetRuleRefusesAStepWithNoTarget(t *testing.T) {
	t.Run("it admits a build whose component's documents are not merged", func(t *testing.T) {
		build := buildStepFixture(t, stepManifestPath, stepReleasePath)
		if build.renderErr != nil {
			t.Fatalf("the render refused a build this proof needs completed: %+v", build.renderErr)
		}
		if build.result == nil || build.result.DescribedView == nil {
			t.Fatal("the render published no view of what its plugins report")
		}
		view := build.result.DescribedView

		steps := build.axes.AttachmentPipelineSteps(stepNode, stepAttachment)
		if !slices.Equal(stepIDs(steps), []string{stepAttachmentStep}) {
			t.Fatalf("the attachment's pipeline enumerates as %v; want the one step its FeatureRecipe declares, %q",
				stepIDs(steps), stepAttachmentStep)
		}
		if scope := view.AttachmentStepExportScope(stepNode, stepAttachment, stepAttachmentStep); scope == types.ScopeHost {
			t.Fatalf("the step addresses the component's documents rather than the attachment's; this rule is about the latter")
		}

		// The fact this rule turns on, read off the real build: this component's
		// provider declares that it does NOT merge. The assertion is against that
		// claim by name rather than against "not merge", because a provider making
		// no claim at all also satisfies the negative form.
		assembly := view.ProviderBoundaryAssembly(stepNode)
		if assembly != wasm.BoundaryAssemblyNone {
			t.Fatalf("the component's provider reports the assembly claim %q; want %q — the admission below is attributable only against a real claim",
				assembly, wasm.BoundaryAssemblyNone)
		}

		objections, err := constraint.RunOnNodeAttachmentPipelineSteps(
			nil, singleNode(build.coordinates, stepNode), build.coordinates, build.axes, view,
			renderer.AttachmentPipelineStepOwnScopeHasAMergedTarget,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}
		t.Logf("the component's provider reports %q and the attachment's own-scope step %q is admitted",
			assembly, stepAttachmentStep)
	})

	t.Run("the build halts on an own-scope step against merged documents", func(t *testing.T) {
		build := buildStepFixture(t, stepMergedTargetManifestPath, stepMergedTargetReleasePath)

		fr, held := build.axes.attachmentFeatureRecipe(stepNode, stepAttachment)
		if !held || fr == nil {
			t.Fatalf("the compilation bound no FeatureRecipe to attachment %q on %q", stepAttachment, stepNode)
		}
		steps := build.axes.AttachmentPipelineSteps(stepNode, stepAttachment)
		if !slices.Equal(stepIDs(steps), []string{stepAttachmentStep}) {
			t.Fatalf("the attachment's pipeline enumerates as %v; want the one step its FeatureRecipe declares, %q",
				stepIDs(steps), stepAttachmentStep)
		}

		// The half of the condition the deployment moves, and the half it does not.
		// The component's provider merges; the attachment still renders through a
		// provider its step plugin declares, which is what leaves the compatibility
		// rule silent and this one the only rule that can raise.
		recipe := recipeOfNode(build.out.IRGraph, build.recipeLookup, stepNode)
		if recipe == nil {
			t.Fatalf("the compilation bound no Recipe to %q; the component would render no documents", stepNode)
		}
		nodeProvider := renderer.ProviderEngineKind(recipe.Spec.Capability)
		attachmentProvider := renderer.ProviderEngineKind(fr.Spec.Capability)
		if nodeProvider == attachmentProvider {
			t.Fatalf("the component and its attachment both render through %q; the two halves of the condition are not separable here",
				nodeProvider)
		}

		if build.renderErr == nil {
			t.Fatal("the render admitted an own-scope step standing against a component whose provider merges its dispatch group")
		}
		if !errors.Is(build.renderErr, renderer.ErrPipelinePatchPreAssembly()) {
			t.Fatalf("the halt does not carry the no-merged-target condition: %+v", build.renderErr)
		}

		// The division of labour, read off the halt: the compatibility rule is
		// silent on this input, so without this rule the declared set would report
		// nothing where the build stops.
		if errors.Is(build.renderErr, renderer.ErrPluginProviderIncompatible()) {
			t.Errorf("the halt also carries the incompatibility condition; the division of labour between the two rules has moved: %+v",
				build.renderErr)
		}

		findings := stepFindings(build.renderErr)
		if len(findings) != len(steps) {
			t.Fatalf("the halt carries %d positioned finding(s) over %d own-scope step(s); want one per step",
				len(findings), len(steps))
		}
		finding := findings[0]

		// Where an author fixes it: the authored step inside the FeatureRecipe that
		// declares it — not the document the attachment was written in, and not the
		// Recipe whose provider merges.
		if finding.Source().File != fr.Metadata.Name {
			t.Errorf("finding positioned in %q; want the entity that declares the step, %q",
				finding.Source().File, fr.Metadata.Name)
		}
		if wantLocator := ".spec.pipeline." + stepAttachmentStep; finding.Source().Locator != wantLocator {
			t.Errorf("finding positioned at %q; want the authored step %q", finding.Source().Locator, wantLocator)
		}
		if !strings.Contains(finding.Message(), `"`+stepAttachment+`"`) {
			t.Errorf("finding %q does not name the attachment that bound the pipeline", finding.Message())
		}

		t.Logf("the component renders through %q and its attachment through %q; the build halted at %q in %q with %q",
			nodeProvider, attachmentProvider, finding.Source().Locator, finding.Source().File, finding.Message())
	})
}

// stepIDs projects enumerated coordinates to the identifiers they carry.
func stepIDs(coordinates []constraint.AuthoredCoordinate) []string {
	out := make([]string, 0, len(coordinates))
	for _, c := range coordinates {
		out = append(out, c.Value)
	}
	return out
}

// stepIDsEqual reports whether the enumerated coordinates are exactly want, in
// order.
func stepIDsEqual(coordinates []constraint.AuthoredCoordinate, want []string) bool {
	return slices.Equal(stepIDs(coordinates), want)
}
