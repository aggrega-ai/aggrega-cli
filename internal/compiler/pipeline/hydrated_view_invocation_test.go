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
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// hydratedRenderEngine is a synthetic provider engine emitting one file per
// node, declaring the usual delimiter pair for its artifact entries.
//
// The pair is what the rule proven below reads its references with, and the
// fixture's artifact entries are authored with it — so declaring it is what makes
// the rule read the same text the render evaluates rather than opaque bytes. The
// engine seam is an injection point by design, and the facts proven below are
// carried by the Recipe and by the render's own working state, so routing through
// a guest would add a dependency without adding evidence.
type hydratedRenderEngine struct{}

func (e *hydratedRenderEngine) Render(
	_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext,
) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{{
		Path:    nodeCtx.NodeID + ".yaml",
		Content: []byte("instance: " + nodeCtx.NodeID + "\n"),
	}}, nil
}

func (e *hydratedRenderEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (e *hydratedRenderEngine) SynthesizeWorkload(
	_ context.Context, _ *types.TemplateContext, _ *types.Workload, nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	return []renderer.EmittedFile{{
		Name: nodeCtx.NodeID + "-workload.yaml",
		Body: []byte("workload: " + nodeCtx.NodeID + "\n"),
	}}, nil
}

func (e *hydratedRenderEngine) EmitArtifacts(
	_ context.Context, proj renderer.ArtifactsProjection, _ renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	out := make([]renderer.EmittedFile, 0, len(proj.Entries))
	for _, entry := range proj.Entries {
		out = append(out, renderer.EmittedFile{Name: entry.Name, Body: entry.Content})
	}
	return out, nil
}

func (e *hydratedRenderEngine) ArtifactTemplateDelimiters() (left, right string) {
	return "{{", "}}"
}

func hydratedRenderFactory(string) (renderer.Provider, error) {
	return &hydratedRenderEngine{}, nil
}

// artifactAxisOver builds the production artifact axis over one render, labelling
// each coordinate with the file the node's Recipe was authored in.
//
// Both axes below come from the render itself rather than from an enumeration
// assembled here, so what a rule is invoked for and what it then reads are one
// reading of one build. The axis is the mid-render one: it enumerates every entry
// whose text the walk recorded, which is the set a rule admitting or refusing
// authoring has to see, because an entry such a rule refuses is exactly one whose
// bytes are never produced.
func artifactAxisOver(
	t *testing.T, res *renderer.RenderResult, graph *types.IRGraph, lookup renderer.RecipeLookup,
) *renderer.ArtifactAxis {
	t.Helper()
	axis := res.HydratedArtifactAxis(func(node string) string {
		recipe := recipeOfNode(graph, lookup, node)
		if recipe == nil {
			return ""
		}
		return recipe.Metadata.Name
	})
	if axis == nil {
		t.Fatal("the render retained no mid-render state, so no artifact axis can be built over it")
	}
	return axis
}

// hydratedTextSubstituted answers the artifact text with a chosen template and
// leaves every other fact to the presentation the render published.
//
// It exists because the inputs this stage's rule is there to refuse are exactly
// the inputs whose render never completes: a template naming an absent pillar is
// refused inside the artifacts walk, so a build carrying one publishes no result
// and there is no presentation to read it from. Substituting the one fact under
// test keeps the pillar-presence facts and the delimiter pair the values the real
// render retained — so the objections raised below follow from what the
// compilation actually produced, and only the offending text is chosen. The
// hand-written path's verdict on that same text is read separately, from a build.
type hydratedTextSubstituted struct {
	constraint.HydratedView
	text string
}

func (v hydratedTextSubstituted) ArtifactTemplateText(_ string, _ string) string { return v.text }

// renderCompiledFixtureHydrated renders the compiled fixture through the engine
// above and returns the render's own result, so a caller reads the presentation
// the render published rather than one assembled beside it.
func renderCompiledFixtureHydrated(
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
	}, recipeLookup, hydratedRenderFactory)
	if err != nil {
		t.Fatalf("render: %+v", err)
	}
	return res
}

// hydratedFixture compiles the workspace once and returns everything the arms
// below read from it.
func hydratedFixture(t *testing.T) (*stepsOneToFourOutput, resolver.CatalogLookup, renderer.RecipeLookup) {
	t.Helper()
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}, readAggregate(t))
	return out, catalogLookup, fixtureRecipeLookup(t)
}

// anyNodeWithArtifacts returns a node of the compiled graph whose bound Recipe
// declares at least one artifact, so the assertions below read facts the
// documents actually carry.
func anyNodeWithArtifacts(
	t *testing.T, graph *types.IRGraph, recipeLookup renderer.RecipeLookup,
) (string, *types.Recipe) {
	t.Helper()
	for _, node := range graph.Spec.Nodes.Keys() {
		recipe := recipeOfNode(graph, recipeLookup, node)
		if recipe != nil && recipe.Spec.Artifacts.Len() > 0 && recipe.Spec.Provides.Len() > 0 {
			return node, recipe
		}
	}
	t.Fatal("the fixture carries no node whose Recipe declares both an artifact and a provides key; the proof would assert nothing")
	return "", nil
}

// TestHydratedPresentationAnswersFromTheCompiledBuild is the invocation proof
// for the presentation of the state each node reaches before its artifact bytes
// exist.
//
// It compiles a real workspace, renders it, takes the presentation the render
// itself published, and asks a declared rule of it. The rule reads three facts of
// this stage and one of the stage below, so a value that answered only its own
// stage, or answered emptily, would fail the assertions below before any rule was
// asked anything.
func TestHydratedPresentationAnswersFromTheCompiledBuild(t *testing.T) {
	t.Run("the presentation is built on the production path", func(t *testing.T) {
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, recipeLookup)
		if res.HydratedView == nil {
			t.Fatal("the render published no presentation of the state its nodes reached; nothing can be invoked through it")
		}
		view := res.HydratedView
		node, recipe := anyNodeWithArtifacts(t, out.IRGraph, recipeLookup)

		// The artifact text, against the entry the Recipe authored. An accessor
		// answering empty would satisfy every rule invoked through it, so the
		// value is asserted before any rule is asked.
		name := recipe.Spec.Artifacts.Keys()[0]
		entry, _ := recipe.Spec.Artifacts.Get(name)
		if got := view.ArtifactTemplateText(node, name); got != entry.Inline {
			t.Errorf("ArtifactTemplateText(%q, %q) = %q, want the entry's authored text %q", node, name, got, entry.Inline)
		}

		// The provides value is the RESOLVED one, which is what separates this
		// fact from the authored text the stage below carries. The fixture
		// authors host as a template, so a presentation handing back the authored
		// form would still be a plausible string — the assertion is that the
		// template is gone.
		key := recipe.Spec.Provides.Keys()[0]
		authored, _ := recipe.Spec.Provides.Get(key)
		if !strings.Contains(authored, "{{") {
			t.Fatalf("the fixture's %q provides entry is not a template, so the assertion below cannot separate resolved from authored", key)
		}
		resolved, held := view.ResolvedNodeProvides(node, key)
		if !held {
			t.Fatalf("ResolvedNodeProvides(%q, %q) reports the key unresolved; the Recipe declares it", node, key)
		}
		if strings.Contains(resolved, "{{") {
			t.Errorf("ResolvedNodeProvides(%q, %q) = %q, want the resolved value rather than the authored template", node, key, resolved)
		}
		if _, held := view.ResolvedNodeProvides(node, "not-a-declared-key"); held {
			t.Errorf("ResolvedNodeProvides(%q, \"not-a-declared-key\") reports a key no Recipe declares as resolved", node)
		}

		// The two yes-or-no facts, each read against what the bound document
		// carries rather than against a constant. The fixture's Recipes declare a
		// workload and no source, so this arm fixes one direction of each; the
		// arm below fixes the other.
		if !view.ParsedNodeWorkloadPresent(node) {
			t.Errorf("ParsedNodeWorkloadPresent(%q) is false for a Recipe declaring a workload body", node)
		}
		if view.ResolvedNodeSourceDeclared(node) {
			t.Errorf("ResolvedNodeSourceDeclared(%q) is true for a Recipe declaring no source", node)
		}

		// A mapping authored as no inline text is ABSENT rather than empty, and
		// the second result is what carries the difference.
		if paths, inline := view.ValuesMappingTemplateRefs(node); inline {
			t.Errorf("ValuesMappingTemplateRefs(%q) = %v/true, want absent for a Recipe authoring no inline mapping", node, paths)
		}

		// The embedded presentations answer here too, which is what makes this a
		// stage view rather than a bag of this stage's own facts: the authored
		// provides text belongs to the stage below, the node's labels to the one
		// below that.
		if got := view.AuthoredNodeProvides(node, key); got != authored {
			t.Errorf("AuthoredNodeProvides(%q, %q) = %q, want the earlier stage's authored text %q; the embedded presentation is not being delegated to", node, key, got, authored)
		}
		if labels := view.NodeLabels(node); labels.Len() == 0 {
			t.Errorf("NodeLabels(%q) is empty; the graph presentation is not being delegated to", node)
		}

		// A node the render never reached answers with each fact's zero value
		// rather than with another node's.
		if got := view.DeclaredLocalsKeys("no-such-node"); got != nil {
			t.Errorf("DeclaredLocalsKeys(\"no-such-node\") = %v, want no keys", got)
		}
		if got := view.ArtifactTemplateText("no-such-node", name); got != "" {
			t.Errorf("ArtifactTemplateText(\"no-such-node\", %q) = %q, want empty", name, got)
		}
	})

	t.Run("the facts the fixture's documents leave absent are answered in their other direction", func(t *testing.T) {
		// The fixture's manifest binds Recipe variants that declare no locals, no
		// inline values mapping and no source, so the arm above proves three facts
		// at one value only — and an accessor returning that value as a constant
		// would satisfy it. This binds Recipes that declare all three. They are
		// real documents rendered through the real path: what changes is which
		// Recipe each node binds, which is what a different manifest would change.
		out, catalogLookup, base := hydratedFixture(t)
		enriched := enrichedRecipeLookup(base)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, enriched)
		if res.HydratedView == nil {
			t.Fatal("the render published no presentation")
		}
		view := res.HydratedView
		node, recipe := anyNodeWithArtifacts(t, out.IRGraph, enriched)

		if got, want := view.DeclaredLocalsKeys(node), recipe.Spec.Locals.Keys(); !slices.Equal(got, want) {
			t.Errorf("DeclaredLocalsKeys(%q) = %v, want the Recipe's authored order %v", node, got, want)
		}
		if len(recipe.Spec.Locals.Keys()) < 2 {
			t.Fatal("the enriched Recipe declares fewer than two locals; author order would not be observable")
		}
		paths, inline := view.ValuesMappingTemplateRefs(node)
		if !inline {
			t.Errorf("ValuesMappingTemplateRefs(%q) reports no inline mapping for a Recipe that authors one", node)
		}
		if !slices.Contains(paths, ".context.instance_id") {
			t.Errorf("ValuesMappingTemplateRefs(%q) = %v, want the path the inline mapping reads", node, paths)
		}
		if !view.ResolvedNodeSourceDeclared(node) {
			t.Errorf("ResolvedNodeSourceDeclared(%q) is false for a Recipe whose source survived resolution", node)
		}
	})

	t.Run("a rule invoked through it reads the compiled artifacts", func(t *testing.T) {
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, recipeLookup)

		coordinates := coordinatesOf(t, out, catalogLookup)
		artifacts := artifactAxisOver(t, res, out.IRGraph, recipeLookup)

		// The traversal must actually visit something. A filter that selected no
		// subject would report no objection and read exactly like a rule that ran
		// and approved, so the subject count is measured rather than assumed.
		visited := 0
		for _, n := range coordinates.Nodes() {
			visited += len(artifacts.Artifacts(n.ID))
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no artifact; a finding from it would certify nothing")
		}

		// And the text the rule reads through the presentation must be the real
		// one: a rule handed an empty template collects no reference and admits
		// whatever it is asked about.
		node, _ := anyNodeWithArtifacts(t, out.IRGraph, recipeLookup)
		first := artifacts.Artifacts(node)[0].Value
		if text := res.HydratedView.ArtifactTemplateText(node, first); !strings.Contains(text, "{{") {
			t.Fatalf("the presentation answers artifact %q with %q, which carries no template reference; the admission below would be vacuous", first, text)
		}

		objections, err := constraint.RunOnNodeArtifacts(
			context.Background().Err, coordinates, artifacts, res.HydratedView,
			renderer.ArtifactPillarDeclared,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}

		// The render completed, so the path that runs today admitted every
		// artifact entry this build carries. The rule reading the same entries
		// through the presentation must admit them too — the two implementations
		// compared over one document rather than reasoned about.
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}
		t.Logf("rule invoked over %d artifact(s) across %d node(s); no objection, agreeing with the render",
			visited, len(coordinates.Nodes()))
	})

	t.Run("the same rule raises on text the render refuses", func(t *testing.T) {
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, recipeLookup)
		coordinates := coordinatesOf(t, out, catalogLookup)
		node, _ := anyNodeWithArtifacts(t, out.IRGraph, recipeLookup)

		// The pillar this text names is absent from the compiled build, and the
		// presentation reports that from what the render retained. So the
		// objection follows from a production fact rather than a chosen one.
		if res.HydratedView.ResolvedNodeSourceDeclared(node) {
			t.Fatal("the fixture declares the pillar the offending template names; the raise below would not be attributable")
		}

		const offending = "chart: {{ .source.chart }}\n"
		var substituted constraint.HydratedView = hydratedTextSubstituted{
			HydratedView: res.HydratedView, text: offending,
		}
		oneNode := singleNode(coordinates, node)
		artifacts := artifactAxisOver(t, res, out.IRGraph, recipeLookup)
		want := len(artifacts.Artifacts(node))
		if want == 0 {
			t.Fatal("the node projects no artifact; the traversal would visit nothing")
		}

		raised, err := constraint.RunOnNodeArtifacts(
			context.Background().Err, oneNode, artifacts, substituted, renderer.ArtifactPillarDeclared,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(raised) != want {
			t.Fatalf("rule raised %d objection(s) over %d artifact(s) naming an absent pillar, want %d", len(raised), want, want)
		}
		for _, o := range raised {
			if !errors.Is(o.Sentinel(), renderer.ErrArtifactPillarNotDeclared()) {
				t.Errorf("objection sentinel = %v, want the undeclared-pillar condition", o.Sentinel())
			}
			if detail := o.Detail(); len(detail) == 0 || detail[0] != ".source.chart" {
				t.Errorf("objection detail = %v, want the offending expression %q", detail, ".source.chart")
			}
		}
	})

	t.Run("the path that runs refuses the same text end to end", func(t *testing.T) {
		// The rule's finding is only worth reading if the hand-written path it
		// stands beside reaches the same verdict. This renders the same compiled
		// graph with one node's artifacts replaced by the offending entry and
		// requires the build to halt on the same condition.
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		node, _ := anyNodeWithArtifacts(t, out.IRGraph, recipeLookup)

		entries := types.OrderedMap[types.ArtifactEntry]{}
		entries.Set("offending.yaml", types.ArtifactEntry{Inline: "chart: {{ .source.chart }}\n"})
		doctored := replaceArtifacts(recipeLookup, node, entries)

		_, err := renderer.RenderAll(context.Background(), renderer.RendererInput{
			Graph:         out.IRGraph,
			GraphView:     out.GraphView,
			FeatureOrder:  out.FeatureOrder,
			ResolveResult: out.ResolveResult,
			CatalogLookup: catalogLookup,
		}, doctored, hydratedRenderFactory)
		if err == nil {
			t.Fatal("the render admitted a template naming an absent pillar; the comparison above would be vacuous")
		}
		if !errors.Is(err, renderer.ErrArtifactPillarNotDeclared()) {
			t.Errorf("render halted with %v, want the undeclared-pillar condition", err)
		}
	})
}

// singleNode narrows the production node enumeration to one node, keeping the
// instance the production path built rather than assembling another.
func singleNode(coordinates constraint.NodeEnumerator, node string) staticNodes {
	for _, n := range coordinates.Nodes() {
		if n.ID == node {
			return staticNodes{n}
		}
	}
	return nil
}

// enrichedRecipeLookup binds each node to its own Recipe carrying locals, an
// inline values mapping and a source, so the facts the fixture's own documents
// leave absent are observable in their other direction. Everything else about
// each document is the one the compilation bound.
func enrichedRecipeLookup(base renderer.RecipeLookup) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(nodeID, componentType string) (*types.Recipe, bool) {
			recipe, found := base.Recipe(nodeID, componentType)
			if !found || recipe == nil {
				return recipe, found
			}
			locals := types.OrderedMap[string]{}
			locals.Set("release", "r-1")
			locals.Set("slug", "{{ .context.instance_id }}")
			enriched := *recipe
			enriched.Spec.Locals = locals
			enriched.Spec.ValuesMapping = types.ValuesMapping{
				Inline: "image: \"{{ .context.instance_id }}\"\n",
			}
			enriched.Spec.Source = "./chart"
			return &enriched, true
		},
		FeatureRecipe: base.FeatureRecipe,
	}
}

// replaceArtifacts binds one node to its own Recipe carrying the given artifact
// entries in place of its own, leaving every other node and every other field as
// the compilation bound them.
func replaceArtifacts(
	base renderer.RecipeLookup, node string, entries types.OrderedMap[types.ArtifactEntry],
) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(nodeID, componentType string) (*types.Recipe, bool) {
			recipe, found := base.Recipe(nodeID, componentType)
			if !found || recipe == nil || nodeID != node {
				return recipe, found
			}
			doctored := *recipe
			doctored.Spec.Artifacts = entries
			return &doctored, true
		},
		FeatureRecipe: base.FeatureRecipe,
	}
}
