// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// declaredAtEmittedStage returns the accessor names the framework declares at the
// emitted stage, derived from the generated views rather than listed here.
//
// The derivation is the difference between the stage's own view and the view it
// extends: a stage's view embeds the one before it, so every name present in the
// first and absent from the second is declared AT this stage. A name added at an
// earlier stage therefore does not perturb this, and a name added at this stage
// appears here with no edit.
func declaredAtEmittedStage(t *testing.T) []string {
	t.Helper()
	emitted := reflect.TypeOf((*constraint.EmittedView)(nil)).Elem()
	hydrated := reflect.TypeOf((*constraint.HydratedView)(nil)).Elem()

	inherited := map[string]bool{}
	for i := 0; i < hydrated.NumMethod(); i++ {
		inherited[hydrated.Method(i).Name] = true
	}
	var own []string
	for i := 0; i < emitted.NumMethod(); i++ {
		if name := emitted.Method(i).Name; !inherited[name] {
			own = append(own, name)
		}
	}
	sort.Strings(own)
	return own
}

// TestRetainedEmissionCoversEveryDeclaredEmittedFact holds this stage's answering
// source to the framework's declarations instead of to a list somebody maintains.
//
// A name declared at this stage with no field to hold it would leave the retention
// silently behind — the declaration would exist, nothing would carry its value,
// and no build would fail. So the two sets are compared in both directions and the
// failure names the offending side.
//
// Every fact here is answered from a retention and none from a seam over what the
// render already holds, which is the opposite split from the stage after this one.
// That follows from what this stage answers rather than from a preference: the
// emitted set is a loop-local the render keeps nothing of, and the assembled output
// that does survive is keyed by paths an assembly composed rather than by the
// names a node chose. A seam taken over the result later is a second answering
// source, and this guard reports a fact answered from it as unretained until the
// source is read here too.
func TestRetainedEmissionCoversEveryDeclaredEmittedFact(t *testing.T) {
	t.Parallel()

	declared := declaredAtEmittedStage(t)

	var retained []string
	typ := reflect.TypeOf(emittedProjection{})
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			retained = append(retained, f.Name)
		}
	}
	sort.Strings(retained)

	// A comparison of two empty sets would pass while proving nothing, so the
	// non-emptiness of each is asserted before the comparison rather than assumed.
	if len(declared) == 0 {
		t.Fatalf("no accessor is declared at this stage; the derivation from the generated views is broken, not the retention")
	}
	if len(retained) == 0 {
		t.Fatalf("the retention carries no field at all")
	}

	held := map[string]bool{}
	for _, name := range retained {
		held[name] = true
	}
	for _, name := range declared {
		if !held[name] {
			t.Errorf("nothing retains %q: it is declared at this stage and no field carries it", name)
		}
	}

	declaredSet := map[string]bool{}
	for _, name := range declared {
		declaredSet[name] = true
	}
	for _, name := range retained {
		if !declaredSet[name] {
			t.Errorf("the retention carries %q, which is declared at no stage past the one before this; "+
				"retain what the declarations name and nothing else", name)
		}
	}

	t.Logf("declared at this stage %v; retained %v", declared, retained)
}

// TestTheEmittedPresentationRefusesAPartialConstruction asserts the presentation
// is absent rather than partial when a source it reads is missing.
//
// An accessor returning a plausible zero value is silent where an absent
// presentation is loud, so the constructor refuses instead of answering a whole
// class of facts from nothing — and a rule reading an emptily-answering
// presentation cannot tell an empty answer from a real one.
func TestTheEmittedPresentationRefusesAPartialConstruction(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		earlier   constraint.HydratedView
		emissions *emittedProjections
	}{
		{"no earlier presentation", nil, newEmittedProjections()},
		{"no retention", stubHydratedForEmitted{}, nil},
		{"neither source", nil, nil},
	} {
		if got := newEmittedView(tc.earlier, tc.emissions); got != nil {
			t.Errorf("%s: a presentation was built anyway", tc.name)
		}
	}

	// The positive control. Without it a constructor that refused everything would
	// satisfy every row above while presenting nothing at all.
	if got := newEmittedView(stubHydratedForEmitted{}, newEmittedProjections()); got == nil {
		t.Error("the presentation refused both sources present, so the refusals above prove nothing")
	}
}

// stubHydratedForEmitted stands in for the earlier presentation where only this
// stage's own construction is under test. It answers nothing, so reading an
// earlier fact through it is a nil dereference rather than a wrong answer — which
// is the point: a test that reached for one would be reaching past what this file
// covers.
type stubHydratedForEmitted struct {
	constraint.HydratedView
}

// mixedOriginCorpus assembles a two-node render whose nodes emit under names of
// their own, one node's entries authored inline and the other's supplied by an
// imported file — so the origin the accessor answers with differs between the two
// and a presentation answering one constant for everything fails.
//
// It also gives one node two names and the other one, so the name axis is not a
// single-element set whose ordering claim nothing could contradict.
func mixedOriginCorpus(t *testing.T) (RendererInput, RecipeLookup, EngineFactory) {
	t.Helper()

	dir := t.TempDir()
	first := writeArtifactFile(t, dir, "imported-one.yaml", "kind: One\n")
	second := writeArtifactFile(t, dir, "imported-two.yaml", "kind: Two\n")

	inlineArtifacts := types.OrderedMap[types.ArtifactEntry]{}
	inlineArtifacts.Set("zeta.yaml", types.ArtifactEntry{Inline: "kind: Zeta\n"})
	inlineArtifacts.Set("alpha.yaml", types.ArtifactEntry{Inline: "kind: Alpha\n"})
	inlineNode := sourceRecipe("inline-emitter", nil)
	inlineNode.Spec.Artifacts = inlineArtifacts

	importedArtifacts := types.OrderedMap[types.ArtifactEntry]{}
	importedArtifacts.Set("imported-one.yaml", types.ArtifactEntry{Import: first})
	importedArtifacts.Set("imported-two.yaml", types.ArtifactEntry{Import: second})
	importedNode := sourceRecipe("import-emitter", nil)
	importedNode.Spec.Artifacts = importedArtifacts

	input, lookup, factory := corpusFromRecipes(map[string]*types.Recipe{
		"inline-emitter": inlineNode,
		"import-emitter": importedNode,
	})
	// The presentation chain refuses to assemble with a hole in it: without a
	// graph-stage presentation to build over, the render publishes none of the
	// later ones and every assertion below would be unreachable for a reason
	// unrelated to what this file covers.
	input.GraphView = emittedGraphFacts{}
	return input, lookup, factory
}

// emittedGraphFacts stands in for the presentation the graph finalisation
// produces, which the render's later presentations are built over.
//
// It carries no value behind the interface: no fact read in this file is a
// graph-stage one, so a read panics rather than answering a zero value — which is
// the point, since an assertion here reaching a graph-stage fact would be reaching
// past what this file covers.
type emittedGraphFacts struct{ constraint.GraphView }

// TestTheEmittedPresentationAnswersFromTheCompiledBuild is the invocation proof
// for the presentation of what each node's own render emitted.
//
// It renders a real corpus, takes the presentation the render itself published,
// and reads the fact for coordinates the build produced. A declaration is free at
// this layer — the accessor lands on the interface whether or not anything answers
// it, and an unanswered one returns an empty slice and fails no build — so the
// assertions below are against real values rather than against the accessor's
// presence.
func TestTheEmittedPresentationAnswersFromTheCompiledBuild(t *testing.T) {
	t.Parallel()

	input, lookup, factory := mixedOriginCorpus(t)
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if res.EmittedView == nil {
		t.Fatal("the render published no presentation of what its nodes emitted; nothing can be read through it")
	}
	view := res.EmittedView

	// The retention's own non-emptiness first: an accessor answering empty would
	// satisfy every assertion made through it.
	if got := res.emitted.recordedNodes(); got != 2 {
		t.Fatalf("the render retained %d node emission(s), want 2; every reading below would be over an empty or partial set", got)
	}

	for _, tc := range []struct {
		node   string
		names  []string
		origin string
	}{
		// Name order, not the order the entries were authored in: the inline node
		// declares zeta before alpha, so a retention holding authoring order would
		// answer the other way round here.
		{"inline-emitter", []string{"alpha.yaml", "zeta.yaml"}, "artifacts-inline"},
		{"import-emitter", []string{"imported-one.yaml", "imported-two.yaml"}, "artifacts-import"},
	} {
		coordinates := newEmittedStageCoordinates(res.emitted)
		if coordinates == nil {
			t.Fatal("no coordinate enumeration was built over a retention the render populated")
		}
		if got := coordinates.NodeEmittedPaths(tc.node); !slices.Equal(got, tc.names) {
			t.Errorf("NodeEmittedPaths(%q) = %v, want %v in name order", tc.node, got, tc.names)
		}
		for _, name := range tc.names {
			origins := view.EmittedFileOriginSet(tc.node, name)
			if len(origins) == 0 {
				t.Fatalf("EmittedFileOriginSet(%q, %q) is empty for a name the same node emitted", tc.node, name)
			}
			if !slices.Equal(origins, []string{tc.origin}) {
				t.Errorf("EmittedFileOriginSet(%q, %q) = %v, want the one block that produced it, %q",
					tc.node, name, origins, tc.origin)
			}
		}
	}

	// The two nodes answer differently, which is what separates a presentation
	// reading the retention from one answering a constant. Both rows above would
	// pass against a presentation that returned whichever origin it was asked for,
	// so the discriminator is stated as its own comparison.
	inline := view.EmittedFileOriginSet("inline-emitter", "alpha.yaml")
	imported := view.EmittedFileOriginSet("import-emitter", "imported-one.yaml")
	if slices.Equal(inline, imported) {
		t.Errorf("both nodes answer %v; the presentation is not discriminating by node", inline)
	}

	// The embedded presentation answers here too, which is what makes this a stage
	// view rather than a bag of this stage's own facts: the locals belong to the
	// stage below.
	if keys := view.DeclaredLocalsKeys("inline-emitter"); len(keys) == 0 {
		t.Error("DeclaredLocalsKeys is empty; the earlier presentation is not being delegated to")
	}

	// A coordinate the render did not reach answers with the fact's zero value
	// rather than with another coordinate's.
	if got := view.EmittedFileOriginSet("no-such-node", "alpha.yaml"); got != nil {
		t.Errorf("EmittedFileOriginSet(\"no-such-node\", \"alpha.yaml\") = %v, want no blocks", got)
	}
	if got := view.EmittedFileOriginSet("inline-emitter", "no-such-name"); got != nil {
		t.Errorf("EmittedFileOriginSet(\"inline-emitter\", \"no-such-name\") = %v, want no blocks", got)
	}
}

// emittedNodeAxis enumerates the nodes of a compiled graph for the traversal
// below.
//
// It is the one axis this file supplies rather than reads from the render. The
// name axis is production — it is the enumeration the render publishes, over the
// retention the presentation answers the fact from — and the node axis is not,
// because the render takes its node enumeration from the compilation rather than
// deriving one, and no rule is declared at this stage yet to be handed one.
// Supplying the production node axis is the work of the change that declares such
// a rule, so nothing here should be read as a production enumeration.
type emittedNodeAxis struct {
	nodes []constraint.NodeInstance
}

func newEmittedNodeAxis(graph *types.IRGraph, sourceFile string) emittedNodeAxis {
	var axis emittedNodeAxis
	for _, id := range graph.Spec.Nodes.Keys() {
		axis.nodes = append(axis.nodes, constraint.NodeInstance{
			ID: id, SourceFile: sourceFile, AuthoredName: id,
		})
	}
	return axis
}

func (a emittedNodeAxis) Nodes() []constraint.NodeInstance { return a.nodes }

// twoBlocksOnOneName answers the origin set with two blocks and leaves every other
// fact to the presentation the render published.
//
// It exists because the state a rule over this fact is there to refuse cannot come
// out of a completed render: the per-node collision detection that runs today
// halts the build on a name two blocks produced, so such a build publishes no
// result and there is no presentation to read one from. Substituting the one fact
// under test keeps every other value the real render produced, so the objections
// below follow from a real traversal over real coordinates and only the offending
// answer is chosen.
type twoBlocksOnOneName struct {
	constraint.EmittedView
	origins []string
}

func (v twoBlocksOnOneName) EmittedFileOriginSet(_ string, _ string) []string { return v.origins }

// nameClaimedOnce is a proof instrument and not a declared rule.
//
// It is here to establish that the coordinates the render publishes reach a
// predicate through the framework's own traversal, and that the predicate reads
// the fact for the subject it was invoked for. Declaring the rule this stands in
// for — and moving a halt onto it — is a separate act with its own equivalence to
// establish against the detection that runs today; this raises nothing in any
// build.
var nameClaimedOnce = constraint.Check[constraint.EmittedView, constraint.NodePathSubject]{
	Sentinel: ErrArtifactOutputCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.EmittedView,
		s constraint.NodePathSubject,
	) []constraint.Objection {
		if len(v.EmittedFileOriginSet(s.NodeID(), s.EmittedPath())) < 2 {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(
			ErrArtifactOutputCollision(),
			constraint.Captioned("emitted name", s.EmittedPath()),
		)}
	},
}

// TestAPredicateIsReachedOverTheEmittedNamesOfACompiledBuild proves the fact is
// answered THROUGH the framework rather than merely present on an interface: the
// traversal walks the names the render published, and a predicate reads the fact
// for each name it was invoked for.
//
// The two directions are both asserted. Over the build as rendered every name is
// claimed once, so nothing is raised — which alone would read exactly like a
// traversal that visited nothing, so the subject count is measured first. With the
// one fact answered as two blocks, the same traversal raises once per visited name,
// which is what shows the predicate is reading the fact rather than ignoring it.
func TestAPredicateIsReachedOverTheEmittedNamesOfACompiledBuild(t *testing.T) {
	t.Parallel()

	input, lookup, factory := mixedOriginCorpus(t)
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if res.EmittedView == nil {
		t.Fatal("the render published no presentation")
	}
	nodes := newEmittedNodeAxis(input.Graph, "aggregate.yaml")
	names := newEmittedStageCoordinates(res.emitted)
	if names == nil {
		t.Fatal("no coordinate enumeration was built over the render's retention")
	}

	// The traversal must actually visit something. An enumeration that selected no
	// subject would report no objection and read exactly like a predicate that ran
	// and approved, so the count is measured rather than assumed.
	visited := 0
	for _, node := range nodes.Nodes() {
		visited += len(names.NodeEmittedPaths(node.ID))
	}
	if visited != 4 {
		t.Fatalf("the traversal would visit %d emitted name(s), want 4; a finding from it would certify nothing", visited)
	}

	objections, err := constraint.RunOnNodeEmittedPaths(
		context.Background().Err, nodes, names, res.EmittedView, nameClaimedOnce,
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	if len(objections) != 0 {
		t.Fatalf("the predicate raised %d objection(s) over a build whose every name is claimed once: %v",
			len(objections), objections)
	}

	// The same traversal over the same coordinates, with the one fact answered as
	// two blocks.
	substituted := twoBlocksOnOneName{
		EmittedView: res.EmittedView,
		origins:     []string{"source", "artifacts-inline"},
	}
	raised, err := constraint.RunOnNodeEmittedPaths(
		context.Background().Err, nodes, names, constraint.EmittedView(substituted), nameClaimedOnce,
	)
	if err != nil {
		t.Fatalf("traversal (substituted): %v", err)
	}
	if len(raised) != visited {
		t.Fatalf("the predicate raised %d objection(s) over %d name(s) answered as claimed twice, want %d",
			len(raised), visited, visited)
	}
	for _, o := range raised {
		if o.Sentinel() == nil {
			t.Error("an objection carries no condition, so the subject it was raised from is unusable")
		}
		if detail := o.Detail(); len(detail) != 1 {
			t.Errorf("objection detail = %v, want the emitted name alone", detail)
		}
	}

	t.Logf("predicate reached over %d emitted name(s) across %d node(s)", visited, len(nodes.Nodes()))
}

// TestTheRetainedEmissionDeduplicatesAndOrdersBlocks pins what the retention does
// with an emission no completed render can produce.
//
// A name two blocks produced halts today, before the projection is taken, so the
// multi-block answer the fact exists for is unreachable from a render — and a
// projection that dropped the second block, or ordered the two by whichever sorted
// first, would look correct in every reading over a real build. So the recorder is
// driven directly, which is the only seam at which the shape is observable.
func TestTheRetainedEmissionDeduplicatesAndOrdersBlocks(t *testing.T) {
	t.Parallel()

	retention := newEmittedProjections()
	retention.recordNode(&PhaseBState{
		NodeID: "web",
		EmittedFiles: []EmittedFile{
			// Reverse-alphabetical, so a retention holding accumulation order rather
			// than name order is visible in the key order below.
			{Name: "zeta.yaml", Origin: EmitOriginSource},
			// One name reached by two blocks, the source first. The second entry is
			// what makes the answer a set rather than a value.
			{Name: "shared.yaml", Origin: EmitOriginSource},
			{Name: "shared.yaml", Origin: EmitOriginPipeline},
			// The same block twice under one name: one contributor, not two.
			{Name: "alpha.yaml", Origin: EmitOriginWorkload},
			{Name: "alpha.yaml", Origin: EmitOriginWorkload},
		},
	})

	p, ok := retention.projection("web")
	if !ok {
		t.Fatal("the node has no retained projection")
	}

	if got, want := p.EmittedFileOriginSet.Keys(), []string{"alpha.yaml", "shared.yaml", "zeta.yaml"}; !slices.Equal(got, want) {
		t.Errorf("retained names = %v, want %v in name order", got, want)
	}

	for _, tc := range []struct {
		name string
		want []string
	}{
		// The source before the pipeline, which is the order the passes ran in and
		// not the order the two names sort in.
		{"shared.yaml", []string{"source", "pipeline"}},
		{"alpha.yaml", []string{"workload"}},
		{"zeta.yaml", []string{"source"}},
	} {
		got, held := p.EmittedFileOriginSet.Get(tc.name)
		if !held {
			t.Errorf("the retention holds no entry for %q", tc.name)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("retained blocks for %q = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The presentation answers the same values, so the retention and the accessor
	// are not two readings of one recording.
	view := newEmittedView(stubHydratedForEmitted{}, retention)
	if view == nil {
		t.Fatal("no presentation was built over a populated retention")
	}
	if got, want := view.EmittedFileOriginSet("web", "shared.yaml"), []string{"source", "pipeline"}; !slices.Equal(got, want) {
		t.Errorf("the presentation answers %v for a name two blocks produced, want %v", got, want)
	}
}
