// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"strings"
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

// describedProvidesKeys supplies the provides-key axis for the traversals below.
//
// The production enumeration supplies this axis too, and is what the render
// invokes the rule through; it is unexported by the package that builds it, so a
// traversal driven from here supplies its own. It reads the keys from the Recipe
// the compilation bound, in the order that document declares them, so the order is
// the authored one rather than one this test invents — and the arm that exercises
// the production axis drives the render instead of the traversal.
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
		// The rule's finding must be observable, not merely its silence, and the
		// arm above establishes only the silence. The pair discriminates: the same
		// rule, invoked through the same traversal over the same subject shape,
		// admits what the corpus carries and refuses a template reaching outside
		// the self-contained data.
		//
		// A build carrying the offending text halts before the render can publish
		// anything, so the text is substituted into a presentation answering that
		// one fact rather than compiled — which is why the corpus arm above is what
		// exercises the production presentation.
		const offending = `{{ .metadata.name }}`
		objections, err := constraint.RunOnNodeProvidesKeys(
			context.Background().Err,
			staticNodes{{ID: "api", SourceFile: "aggregate.yaml", AuthoredName: "api"}},
			describedProvidesKeys{recipeFor: func(string) *types.Recipe {
				provides := types.OrderedMap[string]{}
				provides.Set("endpoint", offending)
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec:     types.RecipeSpec{Provides: provides},
				}
			}},
			constraint.DescribedView(describedTextOnlyView{text: offending}),
			renderer.ProvidesTemplateSelfContained,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 1 {
			t.Fatalf("rule raised %d objection(s) on a template outside the self-contained data, want exactly 1", len(objections))
		}
		if !errors.Is(objections[0].Sentinel(), renderer.ErrProvidesSelfContainment()) {
			t.Errorf("objection sentinel = %v, want the self-containment condition", objections[0].Sentinel())
		}
		if detail := objections[0].Detail(); len(detail) == 0 || detail[0] != ".metadata.name" {
			t.Errorf("objection detail = %v, want the offending expression %q", detail, ".metadata.name")
		}
		// The position an author navigates to: the Recipe, and the entry inside it.
		if got := objections[0].SourceFile(); got != "api-recipe" {
			t.Errorf("objection position = %q, want the Recipe declaring the entry", got)
		}
		if got := objections[0].Locator(); got != ".spec.provides.endpoint" {
			t.Errorf("objection locator = %q, want the authored entry", got)
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

// renderEnumeratedFixture renders the compiled fixture with the enumeration the
// compilation produced wired in, which is what the build itself does.
//
// It is separate from the helper beside it because the two answer different
// questions. That one presents a render given no enumeration, which is the shape
// most of this package's proofs need; this one is the production wiring, and it is
// the only shape under which the rules declared over the presentation are invoked
// at all.
func renderEnumeratedFixture(
	t *testing.T,
	out *stepsOneToFourOutput,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
) (*renderer.RenderResult, error) {
	t.Helper()
	if out.GraphCoordinates == nil {
		t.Fatal("the compilation produced no enumeration of the finalised graph; no rule over the presentation can be invoked")
	}
	return renderer.RenderAll(context.Background(), renderer.RendererInput{
		Graph:            out.IRGraph,
		GraphView:        out.GraphView,
		GraphCoordinates: out.GraphCoordinates,
		FeatureOrder:     out.FeatureOrder,
		ResolveResult:    out.ResolveResult,
		CatalogLookup:    catalogLookup,
	}, recipeLookup, describedRenderFactory)
}

// withExtraProvidesEntry binds every node to its own Recipe with one further
// provides entry carrying the given template, leaving the bound document itself
// untouched.
//
// The entry is added rather than an existing one rewritten, so the Recipe still
// publishes everything its Component declares and the only thing the render sees
// differently is the entry under test.
func withExtraProvidesEntry(base renderer.RecipeLookup, key, template string) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(instance, componentType string) (*types.Recipe, bool) {
			recipe, found := base.Recipe(instance, componentType)
			if !found || recipe == nil {
				return recipe, found
			}
			substituted := *recipe
			provides := types.OrderedMap[string]{}
			for k, v := range recipe.Spec.Provides.All() {
				provides.Set(k, v)
			}
			provides.Set(key, template)
			substituted.Spec.Provides = provides
			return &substituted, true
		},
		FeatureRecipe: base.FeatureRecipe,
	}
}

// TestTheRenderInvokesTheAuthoredProvidesRule is the production-path proof: the
// render itself, given the enumeration the compilation produced, asks the rule
// declared over what a Recipe wrote for each of its provides keys.
//
// It is the arm no unit-level invocation can stand in for. Everything else about
// this rule can be exercised by driving the traversal directly, and none of that
// establishes that the BUILD reaches it — which is the whole difference between a
// declared rule and an enforced one.
func TestTheRenderInvokesTheAuthoredProvidesRule(t *testing.T) {
	clean := readAggregate(t)
	recipeLookup := fixtureRecipeLookup(t)
	fixture := workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}

	t.Run("the fixture as authored renders", func(t *testing.T) {
		out, catalogLookup := compileWorkspace(t, fixture, clean)
		res, err := renderEnumeratedFixture(t, out, catalogLookup, recipeLookup)
		if err != nil {
			t.Fatalf("the render refused a workspace whose provides entries are self-contained: %+v", err)
		}
		if res == nil {
			t.Fatal("the render produced no result")
		}
	})

	t.Run("an entry reaching outside the self-contained data halts the render", func(t *testing.T) {
		out, catalogLookup := compileWorkspace(t, fixture, clean)
		_, err := renderEnumeratedFixture(t, out, catalogLookup,
			withExtraProvidesEntry(recipeLookup, "reaches_outside", `{{ .metadata.name }}`))
		if err == nil {
			t.Fatal("the render admitted a provides entry reaching outside the data it is evaluated against")
		}
		if !errors.Is(err, renderer.ErrProvidesSelfContainment()) {
			t.Fatalf("halt condition = %+v, want the self-containment condition", err)
		}
		// The position an author navigates to, carried through the whole render.
		var chain []string
		for e := err; e != nil; e = errors.Unwrap(e) {
			chain = append(chain, e.Error())
		}
		text := strings.Join(chain, " | ")
		for _, want := range []string{".metadata.name", ".spec.provides.reaches_outside"} {
			if !strings.Contains(text, want) {
				t.Errorf("the halt omits %q: %s", want, text)
			}
		}
	})
}

// TestTheRenderPresentsTheAssembledStageOnTheProductionPath pins the wiring the
// rule over assembled output depends on.
//
// That rule reads facts answered by the presentation of what the render produced,
// so a render publishing none leaves it uninvoked — and the fault it refuses, a
// build-internal link placeholder surviving into output, has no second reading to
// fall back on. The dependency is therefore load-bearing rather than incidental,
// and it is a property of what the caller supplies rather than of the render, so
// it is pinned here where the production wiring is available.
//
// A render given no presentation of the finalised graph publishes none of its own,
// which is the state the assertion below exists to keep out of the build path.
func TestTheRenderPresentsTheAssembledStageOnTheProductionPath(t *testing.T) {
	clean := readAggregate(t)
	recipeLookup := fixtureRecipeLookup(t)
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}, clean)

	// The compilation's own products, which the render is handed.
	if out.GraphView == nil {
		t.Fatal("the compilation published no presentation of the finalised graph; every stage above it presents nothing")
	}
	if out.GraphCoordinates == nil {
		t.Fatal("the compilation published no enumeration of the finalised graph; no rule over a later stage is invoked")
	}

	res, err := renderEnumeratedFixture(t, out, catalogLookup, recipeLookup)
	if err != nil {
		t.Fatalf("render: %+v", err)
	}
	if res.DescribedView == nil {
		t.Fatal("the render published no presentation of what its plugins report")
	}
	if res.RenderedView == nil {
		t.Fatal("the render published no presentation of what it assembled; the rule over assembled output would not be invoked")
	}
	// And the attribution the enumeration is derived from, without which the rule
	// is invoked over no subject at all.
	if len(res.Contributions) == 0 {
		t.Fatal("the render recorded no assembly attribution; the rule over assembled output would reach no output path")
	}
	if len(res.Files) == 0 {
		t.Fatal("the render wrote no file; a rule over assembled output would certify nothing")
	}
}
