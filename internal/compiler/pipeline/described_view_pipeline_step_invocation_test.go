// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The workspace these proofs compile. It is its own fixture because no other one
// carries what they need together: a component a sub-module contributes whose
// Recipe declares a pipeline, an attachment on a component whose FeatureRecipe
// declares one, and an attachment declared at a boundary whose FeatureRecipe
// declares one. Before it, the third of those existed nowhere in the tree at all,
// so a rule over it could be invoked and would report on nothing.
const (
	stepAggregatePath = "../../../internal/compiler/testdata/pipeline-provider-compat/aggregate.yaml"
	stepReleasePath   = "../../../internal/compiler/testdata/pipeline-provider-compat/release.yaml"
	stepManifestPath  = "../../../internal/compiler/testdata/pipeline-provider-compat/manifest.yaml"
	stepCatalogPath   = "../../../internal/compiler/testdata/pipeline-provider-compat/catalog"

	// The plugin every pipeline in the fixture names, and the artifact behind it.
	// Its own self-report declares compatibility with the provider the fixture's
	// entities declare, so an admitted build is admitted because the declaration
	// matches rather than because nothing was read.
	stepPluginURI      = "pipeline-step/patch:1.0.0"
	stepPluginArtifact = "frp-step-patch-rust.wasm"

	// The component the sub-module contributes, under the identity the composed
	// graph gives it. Spelled here so a fixture that stopped contributing it fails
	// loudly rather than leaving a proof asserting nothing.
	contributedNode = "data.main-db"
)

// stepBuild is one compiled and rendered fixture, with everything both readings
// of provider compatibility need to be asked the same question.
type stepBuild struct {
	out           *stepsOneToFourOutput
	catalogLookup resolver.CatalogLookup
	recipeLookup  renderer.RecipeLookup
	lookup        pluginpath.Lookup
	result        *renderer.RenderResult
	coordinates   *ir.GraphStageCoordinates
	axes          stepAxes
}

// buildStepFixture compiles the fixture, resolves its pipeline-step plugin
// through the real resolution cascade, and renders it.
//
// The plugin resolution is the part that cannot be left out. A render wired with
// no resolution runs no pipeline and describes no plugin, so the reading under
// comparison never executes and the presentation answers every plugin fact with
// its zero value — a state in which both readings agree on silence and the
// comparison certifies nothing.
func buildStepFixture(t *testing.T) stepBuild {
	t.Helper()

	catalogPath := stageStepPlugin(t)
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: stepAggregatePath,
		releasePath:   stepReleasePath,
		manifestPath:  stepManifestPath,
		catalogPath:   catalogPath,
	}, bytesOf(t, stepAggregatePath))

	manifest, err := resolver.LoadManifest(stepManifestPath)
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
		Inputs{WorkspaceConfig: workspaceconfig.Default()}, catalogLookup,
	)

	result, err := renderStepFixture(out, catalogLookup, recipeLookup, lookup)
	if err != nil {
		t.Fatalf("render: %+v", err)
	}

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
		coordinates:   coordinatesOf(t, out, catalogLookup),
		axes: stepAxes{
			graph:        out.IRGraph,
			recipeLookup: recipeLookup,
			release:      release,
			releaseFile:  stepReleasePath,
		},
	}
}

// renderStepFixture renders the compiled fixture with the plugin resolution
// wired, and returns the render's own error rather than failing, so a proof about
// a build the render refuses can read that refusal.
func renderStepFixture(
	out *stepsOneToFourOutput,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
	lookup pluginpath.Lookup,
) (*renderer.RenderResult, error) {
	return renderer.RenderAll(context.Background(), renderer.RendererInput{
		Graph:            out.IRGraph,
		GraphView:        out.GraphView,
		FeatureOrder:     out.FeatureOrder,
		ResolveResult:    out.ResolveResult,
		CatalogLookup:    catalogLookup,
		PluginPathLookup: lookup,
	}, recipeLookup, describedRenderFactory)
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

// stepAxes supplies the axes the three provider-compatibility rules are invoked
// once per that no production enumeration answers.
//
// The node axis and the per-node attachment axis are NOT here: the compilation
// itself enumerates both, and taking them from anywhere else would present the
// rules a coordinate set the facts were not keyed by. What is here is what the
// production enumerations do not reach — a pipeline's steps, the boundary set,
// and a boundary's own attachments — and each is read from the product the
// presentation reads the matching fact from, so a rule is invoked for exactly the
// coordinates it can then be told something about.
//
// Nothing here should be read as a production enumeration. Supplying these axes
// in production is separate work; these exist so the rules can be asked the same
// question the reading they stand beside is asked.
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
	build := buildStepFixture(t)

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

	t.Run("the presentation answers for every coordinate the axes present", func(t *testing.T) {
		// The property that makes these axes usable rather than merely populated: a
		// rule invoked for a coordinate the presentation cannot answer about reads a
		// zero value and approves. Each enumerated step is therefore checked against
		// the plugin self-report the presentation holds for it.
		view := build.result.DescribedView
		if view == nil {
			t.Fatal("the render published no presentation of what its plugins report")
		}
		checked := 0
		for _, node := range build.coordinates.Nodes() {
			for _, step := range build.axes.PipelineSteps(node.ID) {
				if caps := view.StepPluginCompatibleCapabilities(node.ID, step.Value); len(caps) == 0 {
					t.Errorf("the presentation reports no declared provider for step %q of %q", step.Value, node.ID)
				}
				checked++
			}
			for _, attachment := range build.coordinates.Attachments(node.ID) {
				for _, step := range build.axes.AttachmentPipelineSteps(node.ID, attachment) {
					if caps := view.AttachmentStepPluginCompatibleCapabilities(
						node.ID, attachment, step.Value,
					); len(caps) == 0 {
						t.Errorf("the presentation reports no declared provider for step %q of attachment %q on %q",
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
						t.Errorf("the presentation reports no declared provider for step %q of boundary attachment %q at %q",
							step.Value, attachment, boundary.Value)
					}
					checked++
				}
			}
		}
		if checked == 0 {
			t.Fatal("the axes presented no coordinate at all; every assertion above passed on an empty set")
		}
		t.Logf("the presentation answered a declared provider set for all %d enumerated step(s)", checked)
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
	build := buildStepFixture(t)
	view := build.result.DescribedView
	if view == nil {
		t.Fatal("the render published no presentation of what its plugins report; nothing can be invoked through it")
	}

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

// TestTheProviderCompatibilityRuleRefusesAnUndeclaredProvider reads the rule's
// verdict on a build whose step is matched against a provider its plugin does not
// declare, and states where the finding lands.
//
// The refused input cannot be reached through a completed render: the build halts
// on the same mismatch, so it publishes no presentation to ask a rule about. One
// fact — the provider rendering the node's own documents — is therefore
// substituted; every other value the rule reads is the one the real build
// produced, the plugin's own self-report above all.
func TestTheProviderCompatibilityRuleRefusesAnUndeclaredProvider(t *testing.T) {
	build := buildStepFixture(t)
	view := build.result.DescribedView

	// A provider no step plugin in this build declares. It is derived from what the
	// build reports rather than chosen blind: the assertion below fails if the
	// plugin ever comes to declare it, so the refusal cannot become vacuous.
	const foreign = "no-such-provider"
	recipe := recipeOfNode(build.out.IRGraph, build.recipeLookup, "api")
	if recipe == nil || len(recipe.Spec.Pipeline) == 0 {
		t.Fatal("the fixture's component declares no pipeline; there is nothing to refuse")
	}
	step := recipe.Spec.Pipeline[0]
	declared := view.StepPluginCompatibleCapabilities("api", step.ID)
	if len(declared) == 0 {
		t.Fatal("the presentation reports no declared provider for the step; a refusal would not be attributable")
	}
	if slices.Contains(declared, foreign) {
		t.Fatalf("the step's plugin declares %q; the refusal below would not follow from the mismatch", foreign)
	}

	var substituted constraint.DescribedView = stepProviderSubstituted{
		DescribedView: view, provider: foreign,
	}
	objections, err := constraint.RunOnNodePipelineSteps(
		nil, singleNode(build.coordinates, "api"), build.axes, substituted,
		renderer.PipelineStepProviderCompatible,
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	if len(objections) == 0 {
		t.Fatal("the rule admitted a step matched against a provider its plugin does not declare")
	}
	for _, o := range objections {
		if !errors.Is(o.Sentinel(), renderer.ErrPluginProviderIncompatible()) {
			t.Errorf("objection sentinel = %v, want the incompatibility condition", o.Sentinel())
		}
	}

	// The rule is invoked once per step and reports each, so its arity is asserted
	// against the pipeline rather than against a fixed number: a one-step pipeline
	// would make one-per-step and one-in-total the same count.
	if len(objections) != len(recipe.Spec.Pipeline) {
		t.Errorf("rule raised %d objection(s) over a %d-step pipeline, want one per step",
			len(objections), len(recipe.Spec.Pipeline))
	}

	// Where the finding lands: on the authored step inside the document that
	// declares it, naming the provider it was matched against in the detail.
	for _, o := range objections {
		if o.Locator() == "" {
			t.Error("objection carries no locator; it cannot be positioned on the authored step")
		}
		if o.SourceFile() == "" {
			t.Error("objection names no source file")
		}
		if detail := o.Detail(); len(detail) == 0 || detail[0] != foreign {
			t.Errorf("objection detail = %v, want the provider that was matched against %q", detail, foreign)
		}
	}
	t.Logf("rule raised %d objection(s) at locator %q in %q naming %q",
		len(objections), objections[0].Locator(), objections[0].SourceFile(), foreign)
}

// TestTheOwnScopeMergedTargetRuleRefusesAStepWithNoTarget invokes the rule over an
// attachment's own-scope step against a component whose documents are merged.
//
// This is the finding the three compatibility rules do not carry, and it is why
// four rules stand over these axes rather than three. An attachment's own output
// is written under its own position as discrete files whichever provider the
// component uses, so a step addressing that output against a component whose
// provider folds its dispatch group into one document has nothing to address by
// the time it runs.
//
// The assertion at the end is what pins the division of labour: on this same input
// the compatibility rule over the same coordinate raises nothing at all, so
// without this rule the declared set would be silent where the build halts.
func TestTheOwnScopeMergedTargetRuleRefusesAStepWithNoTarget(t *testing.T) {
	build := buildStepFixture(t)
	view := build.result.DescribedView

	const node, attachment = "api", "request-guard"
	fr, held := build.axes.attachmentFeatureRecipe(node, attachment)
	if !held || len(fr.Spec.Pipeline) == 0 {
		t.Fatalf("the fixture's attachment %q on %q declares no pipeline; there is nothing to judge", attachment, node)
	}
	steps := build.axes.AttachmentPipelineSteps(node, attachment)
	if len(steps) == 0 {
		t.Fatal("the axis enumerates no step; a verdict would certify nothing")
	}
	if scope := view.AttachmentStepExportScope(node, attachment, steps[0].Value); scope == types.ScopeHost {
		t.Fatalf("the step addresses the component's documents rather than the attachment's; this rule is about the latter")
	}

	t.Run("it admits a build whose component's documents are not merged", func(t *testing.T) {
		// The fact this rule turns on, read off the real build before anything is
		// chosen: this component's provider does not merge, so nothing is refused.
		if assembly := view.ProviderBoundaryAssembly(node); assembly == wasm.BoundaryAssemblyMerge {
			t.Fatalf("the fixture's provider reports %q; the admission below would not be attributable", assembly)
		}
		objections, err := constraint.RunOnNodeAttachmentPipelineSteps(
			nil, singleNode(build.coordinates, node), build.coordinates, build.axes, view,
			renderer.AttachmentPipelineStepOwnScopeHasAMergedTarget,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}
	})

	t.Run("it refuses an own-scope step against merged documents", func(t *testing.T) {
		var merged constraint.DescribedView = stepAssemblySubstituted{
			DescribedView: view, assembly: wasm.BoundaryAssemblyMerge,
		}
		objections, err := constraint.RunOnNodeAttachmentPipelineSteps(
			nil, singleNode(build.coordinates, node), singleAttachment(attachment), build.axes, merged,
			renderer.AttachmentPipelineStepOwnScopeHasAMergedTarget,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != len(steps) {
			t.Fatalf("rule raised %d objection(s) over %d own-scope step(s), want one per step", len(objections), len(steps))
		}
		for _, o := range objections {
			if !errors.Is(o.Sentinel(), renderer.ErrPipelinePatchPreAssembly()) {
				t.Errorf("objection sentinel = %v, want the no-merged-target condition", o.Sentinel())
			}
			if o.Locator() == "" || o.SourceFile() == "" {
				t.Error("objection carries no position; it cannot be pointed at the authored step")
			}
			if detail := o.Detail(); len(detail) == 0 || detail[0] != attachment {
				t.Errorf("objection detail = %v, want the attachment %q", detail, attachment)
			}
		}

		// The reason this rule exists rather than a branch on the compatibility
		// rule: on this same input the compatibility rule raises nothing, so the
		// declared set without this rule is silent where the build halts.
		silent, err := constraint.RunOnNodeAttachmentPipelineSteps(
			nil, singleNode(build.coordinates, node), singleAttachment(attachment), build.axes, merged,
			renderer.AttachmentPipelineStepProviderCompatible,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(silent) != 0 {
			t.Errorf("the compatibility rule now raises %v on this input; the division of labour between the two rules has moved", silent)
		}
		t.Logf("the rule raised %d objection(s) at %q in %q; the compatibility rule over the same coordinate raised none",
			len(objections), objections[0].Locator(), objections[0].SourceFile())
	})
}

// singleAttachment narrows the attachment axis to one name, so a proof about one
// attachment's steps is not also a proof about every other attachment the node
// carries.
type singleAttachment string

func (a singleAttachment) Attachments(_ string) []string { return []string{string(a)} }

// stepAssemblySubstituted answers how the provider rendering a component's own
// documents combines its dispatch group's output with a chosen value, and leaves
// every other fact to the presentation the render published.
//
// It exists for the same reason its sibling below does: the shape the rule refuses
// is one the reading that runs today halts the build on, so a build carrying it
// publishes no result. Substituting the one value keeps every other fact the real
// render produced — the step's declared scope above all, which is the other half
// of the condition — and the same shape is handed to the hand-written reading
// through the arguments the deferral gives it.
type stepAssemblySubstituted struct {
	constraint.DescribedView
	assembly string
}

func (v stepAssemblySubstituted) ProviderBoundaryAssembly(_ string) string { return v.assembly }

// stepProviderSubstituted answers the provider rendering a component's own
// documents with a chosen value and leaves every other fact to the presentation
// the render published.
//
// It exists because the input the rule is there to refuse is exactly an input
// whose render never completes: the hand-written reading halts the build on the
// same mismatch, so a build carrying one publishes no result and there is no
// presentation to read the rule's verdict from. Substituting the one value keeps
// every other fact the real render produced — the plugin's own declared providers
// above all — and that value is handed to the hand-written reading as its own
// argument, so the two are compared on one input rather than on two.
type stepProviderSubstituted struct {
	constraint.DescribedView
	provider string
}

func (v stepProviderSubstituted) NodeRenderingProvider(_ string) string { return v.provider }

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

// The scope constant both readings compare an export against is one value rather
// than two, so this assertion is a compile-time statement that the two readings
// cannot disagree about what "the host documents" means.
var _ = map[bool]struct{}{wasm.ScopeHost == types.ScopeHost: {}}
