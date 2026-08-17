// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// describedRenderEngine is a synthetic provider engine emitting one file per
// node. The engine seam is an injection point by design, and the facts proven
// below are the ones a Recipe and a plugin self-report carry — so routing the
// render through a guest would add a dependency without adding evidence.
type describedRenderEngine struct{}

func (e *describedRenderEngine) Render(
	_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext,
) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{{
		Path:    nodeCtx.NodeID + ".yaml",
		Content: []byte("instance: " + nodeCtx.NodeID + "\n"),
	}}, nil
}

func (e *describedRenderEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (e *describedRenderEngine) SynthesizeWorkload(
	_ context.Context, _ *types.TemplateContext, _ *types.Workload, nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	return []renderer.EmittedFile{{
		Name: nodeCtx.NodeID + "-workload.yaml",
		Body: []byte("workload: " + nodeCtx.NodeID + "\n"),
	}}, nil
}

func (e *describedRenderEngine) EmitArtifacts(
	_ context.Context, proj renderer.ArtifactsProjection, _ renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	out := make([]renderer.EmittedFile, 0, len(proj.Entries))
	for _, entry := range proj.Entries {
		out = append(out, renderer.EmittedFile{Name: entry.Name, Body: entry.Content})
	}
	return out, nil
}

// describedDelimiters is the pair this engine declares. It is deliberately not
// the usual one, so an accessor reporting the usual pair is distinguishable from
// one that read the provider's own claim — a fallback and a real answer would
// otherwise be the same value.
var describedDelimiters = [2]string{"[[", "]]"}

func (e *describedRenderEngine) ArtifactTemplateDelimiters() (left, right string) {
	return describedDelimiters[0], describedDelimiters[1]
}

func describedRenderFactory(string) (renderer.Provider, error) {
	return &describedRenderEngine{}, nil
}

// describedProvidesKeys supplies the provides-key axis for the traversal below.
//
// The node axis is the enumeration the production path itself builds; only this
// one coordinate is supplied here, because no production implementation of it
// exists yet. It reads the keys from the Recipe the compilation bound, in the
// order that document declares them, so the order is the authored one rather
// than one this test invents.
type describedProvidesKeys struct {
	recipeFor func(node string) *types.Recipe
}

func (p describedProvidesKeys) ProvidesKeys(node string) []constraint.AuthoredCoordinate {
	recipe := p.recipeFor(node)
	if recipe == nil {
		return nil
	}
	keys := recipe.Spec.Provides.Keys()
	out := make([]constraint.AuthoredCoordinate, 0, len(keys))
	for _, k := range keys {
		out = append(out, constraint.AuthoredCoordinate{Value: k, SourceFile: recipe.Metadata.Name})
	}
	return out
}

// renderCompiledFixture renders the compiled fixture and returns the render's
// own result, so a caller reads the presentation the render published rather
// than one assembled beside it.
func renderCompiledFixture(
	t *testing.T,
	out *stepsOneToFourOutput,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
) *renderer.RenderResult {
	t.Helper()
	res, err := renderer.RenderAll(context.Background(), renderer.RendererInput{
		Graph:         out.IRGraph,
		GraphView:     out.GraphView,
		FeatureOrder:  out.FeatureOrder,
		ResolveResult: out.ResolveResult,
		CatalogLookup: catalogLookup,
	}, recipeLookup, describedRenderFactory)
	if err != nil {
		t.Fatalf("render: %+v", err)
	}
	return res
}

// fixtureRecipeLookup is the recipe binding the fixture's own manifest produces.
func fixtureRecipeLookup(t *testing.T) renderer.RecipeLookup {
	t.Helper()
	manifest, err := resolver.LoadManifest(invocationManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", invocationCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	return cat.RecipeLookupWithManifest(manifest)
}

// TestDescribedPresentationAnswersARuleFromTheCompiledBuild is the invocation
// proof for the presentation of what a build's plugins and providers report.
//
// It compiles a real workspace, renders it, takes the presentation the render
// itself published, and asks a declared rule of it. The rule reads the text a
// Recipe authored for a provides key — a fact of this stage — while the same
// presentation answers the stages below it through the presentation it embeds,
// so a value that answered only its own stage, or answered emptily, would fail
// the assertions below before any rule was asked anything.
func TestDescribedPresentationAnswersARuleFromTheCompiledBuild(t *testing.T) {
	clean := readAggregate(t)
	recipeLookup := fixtureRecipeLookup(t)

	t.Run("the presentation is built on the production path", func(t *testing.T) {
		out, catalogLookup := compileWorkspace(t, workspaceFixture{
			aggregatePath: invocationAggregatePath,
			releasePath:   invocationReleasePath,
			manifestPath:  invocationManifestPath,
			catalogPath:   invocationCatalogPath,
		}, clean)
		res := renderCompiledFixture(t, out, catalogLookup, recipeLookup)
		if res.DescribedView == nil {
			t.Fatal("the render published no presentation of what its plugins report; nothing can be invoked through it")
		}

		// This stage's own fact, read against the Recipe the build bound. An
		// accessor answering its zero value would satisfy every rule invoked
		// through it, so the value is asserted before any rule is asked.
		node, recipe := anyNodeWithProvides(t, out.IRGraph, recipeLookup)
		key := recipe.Spec.Provides.Keys()[0]
		authored, _ := recipe.Spec.Provides.Get(key)
		if got := res.DescribedView.AuthoredNodeProvides(node, key); got != authored {
			t.Errorf("AuthoredNodeProvides(%q, %q) = %q, want the Recipe's authored text %q", node, key, got, authored)
		}
		if got := res.DescribedView.NodeRenderingProvider(node); got == "" {
			t.Errorf("NodeRenderingProvider(%q) is empty for a node whose Recipe declares a capability", node)
		}

		// The provider's own declared pair, read through the engine the render
		// resolved. A presentation that answered the usual pair here would be
		// reporting its fallback rather than the provider's claim.
		delims := res.DescribedView.ProviderArtifactTemplateDelimiters(node)
		if delims.Left != describedDelimiters[0] || delims.Right != describedDelimiters[1] {
			t.Errorf("ProviderArtifactTemplateDelimiters(%q) = %q/%q, want the provider's declared %q/%q",
				node, delims.Left, delims.Right, describedDelimiters[0], describedDelimiters[1])
		}

		// The embedded earlier presentation answers here too, which is what
		// makes this a stage view rather than a bag of this stage's own facts.
		if labels := res.DescribedView.NodeLabels(node); labels.Len() == 0 {
			t.Errorf("NodeLabels(%q) is empty; the earlier presentation is not being delegated to", node)
		}
	})

	t.Run("a rule invoked through it reads the compiled document", func(t *testing.T) {
		out, catalogLookup := compileWorkspace(t, workspaceFixture{
			aggregatePath: invocationAggregatePath,
			releasePath:   invocationReleasePath,
			manifestPath:  invocationManifestPath,
			catalogPath:   invocationCatalogPath,
		}, clean)
		res := renderCompiledFixture(t, out, catalogLookup, recipeLookup)

		coordinates := coordinatesOf(t, out, catalogLookup)
		provides := describedProvidesKeys{recipeFor: func(node string) *types.Recipe {
			return recipeOfNode(out.IRGraph, recipeLookup, node)
		}}

		// The traversal must actually visit something. A filter that selected no
		// subject would report no objection and read exactly like a rule that ran
		// and approved, so the subject count is measured rather than assumed.
		visited := 0
		for _, n := range coordinates.Nodes() {
			visited += len(provides.ProvidesKeys(n.ID))
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no provides key; a finding from it would certify nothing")
		}

		objections, err := constraint.RunOnNodeProvidesKeys(
			context.Background().Err, coordinates, provides, res.DescribedView,
			renderer.ProvidesTemplateSelfContained,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}

		// The render completed, so the path that runs today admitted every
		// provides entry this build carries. The rule reading the same entries
		// through the presentation must admit them too — the two implementations
		// compared over one document rather than reasoned about.
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}
		t.Logf("rule invoked over %d provides key(s) across %d node(s); no objection, agreeing with the render",
			visited, len(coordinates.Nodes()))
	})

	t.Run("the same rule raises on a document the render refuses", func(t *testing.T) {
		// The rule's finding must be observable, not merely its silence. This
		// arm reads the hand-written path's verdict off the real build and the
		// rule's verdict off the same text, and requires both to refuse.
		const offending = `{{ .metadata.name }}`
		if renderer.CheckProvidesSelfContainment(offending, "api-recipe") == nil {
			t.Fatal("the corpus's offending template is admitted by the path that runs; the comparison below would be vacuous")
		}
		objections := renderer.ProvidesTemplateSelfContained.Run(
			describedTextOnlyView{text: offending}, providesKeySubjectFor(t),
		)
		if len(objections) != 1 {
			t.Fatalf("rule raised %d objection(s) on a template the running path refuses, want exactly 1", len(objections))
		}
		if !errors.Is(objections[0].Sentinel(), renderer.ErrProvidesSelfContainment()) {
			t.Errorf("objection sentinel = %v, want the self-containment condition", objections[0].Sentinel())
		}
		if detail := objections[0].Detail(); len(detail) == 0 || detail[0] != ".metadata.name" {
			t.Errorf("objection detail = %v, want the offending expression %q", detail, ".metadata.name")
		}
	})
}

// anyNodeWithProvides returns a node of the compiled graph whose bound Recipe
// declares at least one provides key, so the assertions above read a fact the
// document actually carries.
func anyNodeWithProvides(
	t *testing.T, graph *types.IRGraph, recipeLookup renderer.RecipeLookup,
) (string, *types.Recipe) {
	t.Helper()
	for _, node := range graph.Spec.Nodes.Keys() {
		recipe := recipeOfNode(graph, recipeLookup, node)
		if recipe != nil && recipe.Spec.Provides.Len() > 0 {
			return node, recipe
		}
	}
	t.Fatal("the fixture carries no node whose Recipe declares a provides key; the proof would assert nothing")
	return "", nil
}

// recipeOfNode binds a node to its Recipe through the same lookup and the same
// instance-plus-type key the render binds it with.
func recipeOfNode(
	graph *types.IRGraph, recipeLookup renderer.RecipeLookup, node string,
) *types.Recipe {
	if recipeLookup.Recipe == nil {
		return nil
	}
	n, ok := graph.Spec.Nodes.Get(node)
	if !ok {
		return nil
	}
	recipe, found := recipeLookup.Recipe(node, n.Component)
	if !found {
		return nil
	}
	return recipe
}

// describedTextOnlyView answers the one fact the self-containment rule reads and
// refuses every other, so the refusal arm above exercises the rule's predicate
// against a chosen text without standing in for the presentation under proof.
// The production presentation is proven by the two arms above; this value exists
// only because a build carrying the offending text halts before the render can
// publish anything to read.
type describedTextOnlyView struct {
	constraint.DescribedView
	text string
}

func (v describedTextOnlyView) AuthoredNodeProvides(_ string, _ string) string { return v.text }

// providesKeySubjectFor produces the subject the rule is invoked with, through
// the framework's own traversal so the subject is minted where every subject is.
func providesKeySubjectFor(t *testing.T) constraint.NodeProvidesKeySubject {
	t.Helper()
	var captured constraint.NodeProvidesKeySubject
	_, err := constraint.RunOnNodeProvidesKeys(
		nil,
		staticNodes{{ID: "api", SourceFile: "aggregate.yaml", AuthoredName: "api"}},
		describedProvidesKeys{recipeFor: func(string) *types.Recipe {
			provides := types.OrderedMap[string]{}
			provides.Set("endpoint", "")
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec:     types.RecipeSpec{Provides: provides},
			}
		}},
		constraint.DescribedView(nil),
		constraint.Check[constraint.DescribedView, constraint.NodeProvidesKeySubject]{
			Sentinel: renderer.ErrProvidesSelfContainment(),
			Run: func(_ constraint.DescribedView, s constraint.NodeProvidesKeySubject) []constraint.Objection {
				captured = s
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("subject traversal: %v", err)
	}
	if captured == nil {
		t.Fatal("the traversal minted no subject")
	}
	return captured
}

// staticNodes is a fixed node axis, used only where the subject rather than the
// graph is the thing under construction.
type staticNodes []constraint.NodeInstance

func (n staticNodes) Nodes() []constraint.NodeInstance { return n }

// TestGraphStageCoordinatesSupplyTheNodeAxis pins that the node axis the proof
// above traverses is the production enumeration rather than one the test wrote,
// so a change that stopped the pipeline building it fails here rather than
// silently narrowing every traversal to nothing.
func TestGraphStageCoordinatesSupplyTheNodeAxis(t *testing.T) {
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}, readAggregate(t))

	var _ constraint.NodeEnumerator = ir.NewGraphStageCoordinates(
		out.IRGraph, out.GraphView, catalogLookup, releaseFileOf(out.ResolveResult),
		out.AttachmentSites,
	)
	if n := len(coordinatesOf(t, out, catalogLookup).Nodes()); n == 0 {
		t.Fatal("the compilation enumerated no node; every traversal over it would be silent")
	}
}
