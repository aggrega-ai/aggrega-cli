// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderedCoordinates supplies the boundary and provider axes for the traversal
// below, derived from the attribution the render published.
//
// Only these two coordinates are supplied here, because no production
// implementation of either exists yet — supplying the enumerators the declared
// rules need is the work of the items this presentation unblocks, so nothing here
// should be read as a production enumeration. The third axis is not supplied at
// all: the render's own result already answers it in exactly the shape the
// framework asks for, so the traversal below takes the result itself for that one.
//
// Both axes are derived from what the render produced rather than from what the
// test expects, and both are sorted, so the enumeration is a function of the build
// and the same on every run.
type renderedCoordinates struct {
	boundaries []constraint.AuthoredCoordinate
	byBoundary map[string][]string
}

func newRenderedCoordinates(res *renderer.RenderResult, sourceFile string) renderedCoordinates {
	byBoundary := map[string][]string{}
	for _, c := range res.Contributions {
		for _, boundary := range c.Boundaries {
			if !slices.Contains(byBoundary[boundary], c.Provider) {
				byBoundary[boundary] = append(byBoundary[boundary], c.Provider)
			}
		}
	}
	names := make([]string, 0, len(byBoundary))
	for boundary := range byBoundary {
		names = append(names, boundary)
		sort.Strings(byBoundary[boundary])
	}
	sort.Strings(names)

	coordinates := renderedCoordinates{byBoundary: byBoundary}
	for _, name := range names {
		coordinates.boundaries = append(coordinates.boundaries, constraint.AuthoredCoordinate{
			Value: name, SourceFile: sourceFile,
		})
	}
	return coordinates
}

func (c renderedCoordinates) Boundaries() []constraint.AuthoredCoordinate { return c.boundaries }

func (c renderedCoordinates) Providers(boundary string) []string { return c.byBoundary[boundary] }

// projectedArtifactAxisOver builds the production artifact axis that enumerates
// what a render actually projected, rather than what the Recipe declared.
//
// That distinction is the point of it. The set the Recipe declares and the set the
// render projected are different: an entry whose template evaluates to nothing is
// declared and never projected. An axis read from the Recipe would present such an
// entry to a rule and the rule would ask about bytes that were never written,
// which is precisely the over-reporting the name-set fact exists to avoid.
func projectedArtifactAxisOver(
	t *testing.T, res *renderer.RenderResult, graph *types.IRGraph, lookup renderer.RecipeLookup,
) *renderer.ArtifactAxis {
	t.Helper()
	axis := res.RenderedArtifactAxis(func(node string) string {
		recipe := recipeOfNode(graph, lookup, node)
		if recipe == nil {
			return ""
		}
		return recipe.Metadata.Name
	})
	if axis == nil {
		t.Fatal("the render published no presentation, so no projected-artifact axis can be built over it")
	}
	return axis
}

// renderedContentSubstituted answers the assembled-file bytes with chosen content
// and leaves every other fact to the presentation the render published.
//
// It exists because the input the rendered stage's declared rule is there to
// refuse is exactly an input whose render never completes: the path that runs
// today scans the assembled output for the same marker and halts on it, so a build
// carrying one publishes no result and there is no presentation to read it from.
// Substituting the one fact under test keeps every other value the real render
// produced, so the objections raised below follow from a real traversal over real
// coordinates and only the offending bytes are chosen.
type renderedContentSubstituted struct {
	constraint.RenderedView
	content []byte
}

func (v renderedContentSubstituted) AssembledFileContent(_ string, _ string, _ string) []byte {
	return v.content
}

// aNodeThatProjectedArtifacts returns a node whose render actually projected at
// least one artifact, read from the presentation rather than from the documents,
// so the assertions below read facts the build produced.
func aNodeThatProjectedArtifacts(
	t *testing.T, graph *types.IRGraph, view constraint.RenderedView,
) (string, []string) {
	t.Helper()
	for _, node := range graph.Spec.Nodes.Keys() {
		if names := view.ArtifactProjectionNameSet(node); len(names) > 0 {
			return node, names
		}
	}
	t.Fatal("no node of the compiled graph projected an artifact; every artifact assertion below would be vacuous")
	return "", nil
}

// TestRenderedPresentationAnswersFromTheCompiledBuild is the invocation proof for
// the presentation of what a render produced.
//
// It compiles a real workspace, renders it, takes the presentation the render
// itself published, and asks a declared rule of it. Two of the four facts this
// stage declares are answered from the attribution the assembly retained and two
// from the retention the artifacts walk fills, so a value that answered only one
// pair, or answered emptily, would fail the assertions below before any rule was
// asked anything.
func TestRenderedPresentationAnswersFromTheCompiledBuild(t *testing.T) {
	t.Run("the presentation is built on the production path", func(t *testing.T) {
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, recipeLookup)
		if res.RenderedView == nil {
			t.Fatal("the render published no presentation of what it produced; nothing can be invoked through it")
		}
		view := res.RenderedView

		// The assembled-output facts, against the attribution the render published.
		// The attribution's own non-emptiness is asserted first: an accessor
		// answering empty would satisfy every rule invoked through it.
		if len(res.Contributions) == 0 {
			t.Fatal("the render recorded no assembly attribution; the two facts below would answer from nothing")
		}
		contribution := res.Contributions[0]
		if len(contribution.Boundaries) == 0 || len(contribution.OutputPaths) == 0 {
			t.Fatalf("the first attribution names %d boundaries and %d paths; the assertions below need both",
				len(contribution.Boundaries), len(contribution.OutputPaths))
		}
		boundary, provider := contribution.Boundaries[0], contribution.Provider

		paths := view.EmittedFilePathSet(boundary, provider)
		if len(paths) == 0 {
			t.Fatalf("EmittedFilePathSet(%q, %q) is empty for a pair the attribution names", boundary, provider)
		}
		if !sort.StringsAreSorted(paths) {
			t.Errorf("EmittedFilePathSet(%q, %q) = %v, want path order", boundary, provider, paths)
		}
		for _, path := range paths {
			if _, held := res.Files[path]; !held {
				t.Errorf("EmittedFilePathSet(%q, %q) names %q, which the result's file map does not hold", boundary, provider, path)
			}
		}

		path := paths[0]
		content := view.AssembledFileContent(boundary, provider, path)
		if len(content) == 0 {
			t.Fatalf("AssembledFileContent(%q, %q, %q) is empty for a path the same pair wrote", boundary, provider, path)
		}
		if want := res.Files[path].Content; string(content) != string(want) {
			t.Errorf("AssembledFileContent(%q, %q, %q) = %q, want the bytes the result holds %q", boundary, provider, path, content, want)
		}

		// The artifact facts, against what the render projected. The name set is
		// read first because the content fact is unanswerable without a name.
		node, names := aNodeThatProjectedArtifacts(t, out.IRGraph, view)
		artifact := names[0]
		projected := view.ArtifactProjectionContent(node, artifact)
		if len(projected) == 0 {
			t.Fatalf("ArtifactProjectionContent(%q, %q) is empty for an artifact the same node projected", node, artifact)
		}
		// The bytes are the EVALUATED ones, which is what separates this fact from
		// the text the stage below carries. The fixture authors its artifact bodies
		// as templates, so a presentation handing back the authored form would still
		// be a plausible document — the assertion is that the template is gone.
		authored := view.ArtifactTemplateText(node, artifact)
		if !strings.Contains(authored, "{{") {
			t.Fatalf("the fixture's %q entry is not a template, so the assertion below cannot separate evaluated from authored", artifact)
		}
		if strings.Contains(string(projected), "{{") {
			t.Errorf("ArtifactProjectionContent(%q, %q) = %q, want the evaluated bytes rather than the authored template", node, artifact, projected)
		}

		// The embedded presentations answer here too, which is what makes this a
		// stage view rather than a bag of this stage's own facts: the artifact text
		// belongs to the stage below, the node's labels to the graph's own.
		if labels := view.NodeLabels(node); labels.Len() == 0 {
			t.Errorf("NodeLabels(%q) is empty; the graph presentation is not being delegated to", node)
		}

		// A coordinate the render did not reach answers with each fact's zero value
		// rather than with another coordinate's.
		if got := view.EmittedFilePathSet("no-such-boundary", provider); got != nil {
			t.Errorf("EmittedFilePathSet(\"no-such-boundary\", %q) = %v, want no paths", provider, got)
		}
		if got := view.AssembledFileContent(boundary, provider, "no-such-path"); got != nil {
			t.Errorf("AssembledFileContent(%q, %q, \"no-such-path\") = %q, want no bytes", boundary, provider, got)
		}
		if got := view.ArtifactProjectionNameSet("no-such-node"); got != nil {
			t.Errorf("ArtifactProjectionNameSet(\"no-such-node\") = %v, want no names", got)
		}
		if got := view.ArtifactProjectionContent(node, "no-such-artifact"); got != nil {
			t.Errorf("ArtifactProjectionContent(%q, \"no-such-artifact\") = %q, want no bytes", node, got)
		}

		t.Logf("presentation answers %d path(s) for %q/%q and %d artifact(s) for %q",
			len(paths), boundary, provider, len(names), node)
	})

	t.Run("a rule declared at that stage is invoked through it", func(t *testing.T) {
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, recipeLookup)
		if res.RenderedView == nil {
			t.Fatal("the render published no presentation")
		}
		coordinates := newRenderedCoordinates(res, invocationReleasePath)

		// The traversal must actually visit something. A filter that selected no
		// subject would report no objection and read exactly like a rule that ran
		// and approved, so the subject count is measured rather than assumed.
		visited := 0
		for _, boundary := range coordinates.Boundaries() {
			for _, provider := range coordinates.Providers(boundary.Value) {
				visited += len(res.EmittedPaths(boundary.Value, provider))
			}
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no emitted path; a finding from it would certify nothing")
		}

		objections, err := constraint.RunOnBoundaryProviderPaths(
			context.Background().Err, coordinates, coordinates, res, res.RenderedView,
			renderer.CrossBoundaryLinkPlaceholderResolved,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}

		// The render completed, so the path that runs today admitted every assembled
		// file this build carries. The rule reading the same bytes through the
		// presentation must admit them too — the two implementations compared over
		// one build rather than reasoned about.
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}
		t.Logf("rule invoked over %d emitted path(s) across %d boundary(ies); no objection, agreeing with the render",
			visited, len(coordinates.Boundaries()))
	})

	t.Run("the same rule raises on bytes the render refuses", func(t *testing.T) {
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, recipeLookup)
		coordinates := newRenderedCoordinates(res, invocationReleasePath)

		visited := 0
		for _, boundary := range coordinates.Boundaries() {
			for _, provider := range coordinates.Providers(boundary.Value) {
				visited += len(res.EmittedPaths(boundary.Value, provider))
			}
		}
		if visited == 0 {
			t.Fatal("the traversal would visit no emitted path")
		}

		offending := []byte("wired: " + rendercore.CrossBoundaryLinkSentinelPrefix + "api/host\n")
		var substituted constraint.RenderedView = renderedContentSubstituted{
			RenderedView: res.RenderedView, content: offending,
		}
		raised, err := constraint.RunOnBoundaryProviderPaths(
			context.Background().Err, coordinates, coordinates, res, substituted,
			renderer.CrossBoundaryLinkPlaceholderResolved,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(raised) != visited {
			t.Fatalf("rule raised %d objection(s) over %d path(s) carrying an unresolved marker, want %d", len(raised), visited, visited)
		}
		for _, o := range raised {
			if !errors.Is(o.Sentinel(), renderer.ErrOrphanCrossBoundaryLinkSentinel()) {
				t.Errorf("objection sentinel = %v, want the unresolved cross-boundary marker condition", o.Sentinel())
			}
			if detail := o.Detail(); len(detail) != 2 {
				t.Errorf("objection detail = %v, want the provider and the emitted path", detail)
			}
		}
	})

	t.Run("the artifact name set drives a real traversal", func(t *testing.T) {
		// The name-set fact is what a rule needs in hand before it can ask about any
		// single artifact, so this invokes a declared rule over exactly the axis that
		// fact enumerates. The rule reads through the same presentation, which is
		// what makes this an invocation over the published value rather than over an
		// axis assembled beside it.
		out, catalogLookup, recipeLookup := hydratedFixture(t)
		res := renderCompiledFixtureHydrated(t, out, catalogLookup, recipeLookup)
		if res.RenderedView == nil {
			t.Fatal("the render published no presentation")
		}
		coordinates := coordinatesOf(t, out, catalogLookup)
		artifacts := projectedArtifactAxisOver(t, res, out.IRGraph, recipeLookup)

		visited := 0
		for _, n := range coordinates.Nodes() {
			visited += len(artifacts.Artifacts(n.ID))
		}
		if visited == 0 {
			t.Fatal("the name-set fact enumerated no artifact; a finding from the traversal would certify nothing")
		}

		// The rule is declared at the stage below this one, so the presentation is
		// handed over as that stage's view. It is the same value either way — stage
		// containment is what makes the narrower type satisfiable by the wider
		// presentation — and naming the narrower one is what lets a rule declared
		// earlier be invoked through a later stage's published presentation without
		// either stage producing the other's fact.
		var earlier constraint.HydratedView = res.RenderedView
		objections, err := constraint.RunOnNodeArtifacts(
			context.Background().Err, coordinates, artifacts, earlier,
			renderer.ArtifactPillarDeclared,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("rule raised %d objection(s) over a build the render admitted: %v", len(objections), objections)
		}
		t.Logf("the name-set fact enumerated %d artifact(s) across %d node(s), each read through the published presentation",
			visited, len(coordinates.Nodes()))
	})
}
