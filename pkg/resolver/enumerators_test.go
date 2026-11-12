// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheNodeAxisIsInstanceNameOrderRatherThanDeclarationOrder pins the order
// the node axis promises, using a document whose declaration order is not the
// promised one — so the assertion cannot pass by accident.
//
// INVARIANT: the node axis returns instances in name order whatever order the
// document declares them in.
// CONSEQUENCE: were this to fail, two builds of one document could report the
// same violations in different orders, which is the byte-identical-output
// contract broken at the diagnostic surface.
func TestTheNodeAxisIsInstanceNameOrderRatherThanDeclarationOrder(t *testing.T) {
	t.Parallel()

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI})
	components.Set("api", types.ComponentInstance{Type: componentTypeURI})
	components.Set("db", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "ordering"},
		Spec:     types.AggregateSpec{Components: components},
	})

	got := nodeIDs(newCoordinateSet(newNormalizedView(na, nil, CatalogLookup{}, nil)))
	want := []string{"api", "db", "web"}
	if !equalStrings(got, want) {
		t.Fatalf("the node axis returned %v; want %v.\n"+
			"INVARIANT: the axis promises instance-name order, and the document above "+
			"declares web, api, db.\n"+
			"CONSEQUENCE: enumeration order reaches diagnostics, so a document would "+
			"report its violations in an order that varies between builds.", got, want)
	}
}

// TestTheConnectionAxisCarriesBothAuthoringDocuments is the axis's defining
// property, and the reason it is not simply the developer document's edge set.
//
// The rules over a connection are split across the two documents: one measures
// what the developer wrote against the socket's contract, its sibling measures
// what the Release wrote against a separate contract for the same socket. An
// axis carrying one document's connections leaves the other rule invoked for
// none of the connections it exists to judge.
//
// INVARIANT: a connection either document wrote is enumerated, and a connection
// both wrote is enumerated exactly once.
// CONSEQUENCE: were the Release's connections missing, the rule over what an
// operator wrote would never be asked about them — raising nothing, failing no
// test, and leaving the build green over unchecked input.
func TestTheConnectionAxisCarriesBothAuthoringDocuments(t *testing.T) {
	t.Parallel()

	// The developer wires one connection on `backend` and one on `shared`.
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("primary", types.VerboseUsesEntry{Target: "db"})
	shared := types.OrderedMap[types.VerboseUsesEntry]{}
	shared.Set("both", types.VerboseUsesEntry{Target: "db"})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("backend", types.UsesSocketValue{Verbose: verbose})
	uses.Set("shared", types.UsesSocketValue{Verbose: shared})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI, Uses: uses})
	components.Set("db", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "both-documents"},
		Spec:     types.AggregateSpec{Components: components},
	})

	// The Release configures the connection the developer already wrote on
	// `shared`, and wires one the developer did not write at all on `cache`.
	relShared := types.OrderedMap[types.ReleaseUsesEntry]{}
	relShared.Set("both", types.ReleaseUsesEntry{Target: "db"})
	relCache := types.OrderedMap[types.ReleaseUsesEntry]{}
	relCache.Set("operator-only", types.ReleaseUsesEntry{Target: "db"})
	relUses := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	relUses.Set("shared", relShared)
	relUses.Set("cache", relCache)
	relComponents := types.OrderedMap[types.BoundaryComponent]{}
	relComponents.Set("web", types.BoundaryComponent{Uses: relUses})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: relComponents})
	release := &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}}

	coordinates := newCoordinateSet(newNormalizedView(na, release, CatalogLookup{}, nil))

	got := make([]string, 0, len(coordinates.Edges()))
	for _, e := range coordinates.Edges() {
		got = append(got, e.Node.ID+"/"+e.Socket+"/"+e.Alias)
	}
	want := []string{
		"web/backend/primary",
		"web/shared/both",
		"web/cache/operator-only",
	}
	if !equalStrings(got, want) {
		t.Fatalf("the connection axis returned %v; want %v.\n"+
			"INVARIANT: every connection either document wrote appears once, developer "+
			"document first.\n"+
			"CONSEQUENCE: a missing Release connection leaves the operator-side rule "+
			"never invoked for it; a duplicated one reports a single authoring fault twice.",
			got, want)
	}
}

// TestAShortFormConnectionCarriesNoAuthoredAlias pins the flag the framework
// reads to decide whether an alias contributes a locator segment.
//
// The short authoring form lists targets and gives each connection the target's
// own name as its alias, so no key in the file spells that alias. The long form
// writes the alias as a key.
//
// INVARIANT: a connection's synthesized-alias flag reflects the form its socket
// was authored in.
// CONSEQUENCE: were a short-form connection marked as carrying an authored
// alias, its diagnostics would point at a key the source file does not contain,
// sending an author to a locator they cannot find.
func TestAShortFormConnectionCarriesNoAuthoredAlias(t *testing.T) {
	t.Parallel()

	long := types.OrderedMap[types.VerboseUsesEntry]{}
	long.Set("written", types.VerboseUsesEntry{Target: "db"})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("listed", types.UsesSocketValue{Compact: []string{"db"}})
	uses.Set("keyed", types.UsesSocketValue{Verbose: long})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI, Uses: uses})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "forms"},
		Spec:     types.AggregateSpec{Components: components},
	})

	coordinates := newCoordinateSet(newNormalizedView(na, nil, CatalogLookup{}, nil))
	synthesized := map[string]bool{}
	for _, e := range coordinates.Edges() {
		synthesized[e.Socket] = e.SynthesizedAlias
	}

	if len(synthesized) != 2 {
		t.Fatalf("the fixture produced %d sockets; both authoring forms must be present.\n"+
			"INVARIANT: the case compares the two forms against each other.\n"+
			"CONSEQUENCE: a one-form fixture asserts the flag in one direction only, "+
			"which the zero value satisfies for free.", len(synthesized))
	}
	if !synthesized["listed"] {
		t.Error("a connection from the short authoring form must carry no authored alias.\n" +
			"INVARIANT: the short form spells no alias key in the file.\n" +
			"CONSEQUENCE: its diagnostics name a locator segment the document does not contain.")
	}
	if synthesized["keyed"] {
		t.Error("a connection from the long authoring form must carry its authored alias.\n" +
			"INVARIANT: the long form writes the alias as a key.\n" +
			"CONSEQUENCE: the locator stops naming the entry the author actually wrote.")
	}
}

// TestTheScopeAxesOmitScopesNormalizationSynthesized pins both scope axes
// against the one case that separates a declared scope from a produced one.
//
// Normalization folds an Aggregate's root components into a synthesized scope
// and a Release's root components into a synthesized boundary. Neither was
// declared by anybody, so neither has an authored site an attachment of it could
// be reported at.
//
// INVARIANT: a scope axis returns the scopes an author declared, in author
// order, and omits the ones normalization produced.
// CONSEQUENCE: were a synthesized scope enumerated, a rule over it would raise
// against a key no file contains.
func TestTheScopeAxesOmitScopesNormalizationSynthesized(t *testing.T) {
	t.Parallel()

	// The synthesized scope and a declared one cannot coexist in one Aggregate:
	// normalization folds the root surface into a synthesized scope only when the
	// author declared no scope at all. So the two halves are separate documents,
	// and authoring them as one would produce a shape normalization never emits.
	rootOnly := types.OrderedMap[types.ComponentInstance]{}
	rootOnly.Set("api", types.ComponentInstance{Type: componentTypeURI})
	folded := newCoordinateSet(newNormalizedView(
		normalized(t, &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "folded"},
			Spec:     types.AggregateSpec{Components: rootOnly},
		}), nil, CatalogLookup{}, nil))

	// The document is not empty — its component was folded INTO the synthesized
	// scope — so the empty scope axis below is a decision about the axis rather
	// than a document with nothing in it.
	if len(folded.Nodes()) == 0 {
		t.Fatalf("the folded fixture produced no nodes.\n" +
			"INVARIANT: the root component is folded into the synthesized scope and " +
			"still enumerated as a node.\n" +
			"CONSEQUENCE: the scope assertion below passes over an empty document and " +
			"proves nothing about synthesized scopes.")
	}
	if got := coordinateValues(folded.Subsystems()); len(got) != 0 {
		t.Errorf("the Aggregate's scope axis returned %v; want no scopes.\n"+
			"INVARIANT: a scope normalization synthesized is not a scope an author declared.\n"+
			"CONSEQUENCE: a rule over it reports at a key no file contains.", got)
	}

	inner := types.OrderedMap[types.Subsystem]{}
	inner.Set("deep", types.Subsystem{})
	declared := types.OrderedMap[types.Subsystem]{}
	declared.Set("edge", types.Subsystem{Subsystems: inner})
	declared.Set("core", types.Subsystem{})
	nested := newCoordinateSet(newNormalizedView(
		normalized(t, &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "scopes"},
			Spec:     types.AggregateSpec{Subsystems: declared},
		}), nil, CatalogLookup{}, nil))

	gotSubsystems := coordinateValues(nested.Subsystems())
	wantSubsystems := []string{"edge", "edge.deep", "core"}
	if !equalStrings(gotSubsystems, wantSubsystems) {
		t.Errorf("the Aggregate's scope axis returned %v; want %v.\n"+
			"INVARIANT: author order, with an enclosing scope before the scopes nested "+
			"inside it.\n"+
			"CONSEQUENCE: enumeration order reaches diagnostics, so one document reports "+
			"its scope violations in an order that varies between builds.",
			gotSubsystems, wantSubsystems)
	}

	nestedBoundary := types.OrderedMap[types.Boundary]{}
	nestedBoundary.Set("eu", types.Boundary{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Boundaries: nestedBoundary})
	boundaries.Set("synthetic", types.Boundary{Synthesized: true})
	release := &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}}
	deployed := newCoordinateSet(newNormalizedView(
		normalized(t, &types.Aggregate{Metadata: types.AggregateMetadata{Name: "scopes"}}),
		release, CatalogLookup{}, nil))

	gotBoundaries := coordinateValues(deployed.Boundaries())
	wantBoundaries := []string{"prod", "prod/eu"}
	if !equalStrings(gotBoundaries, wantBoundaries) {
		t.Errorf("the Release's scope axis returned %v; want %v.\n"+
			"INVARIANT: declared scopes only, outer before nested, in author order.\n"+
			"CONSEQUENCE: a rule over a synthesized boundary reports at a key no file contains.",
			gotBoundaries, wantBoundaries)
	}
}

// TestEveryAxisReturnsOneSequenceAcrossRepeatedConstructions is the guard
// against the failure the enumerator contract exists to prevent: an axis backed
// by a Go map, whose iteration order changes per run.
//
// It builds the coordinate set repeatedly from one pair of documents and
// compares every axis against the first construction. A map-backed axis fails
// this within a handful of iterations; a slice-backed one cannot fail it at all.
//
// INVARIANT: for one input, every axis returns the same sequence every time.
// CONSEQUENCE: were an axis map-backed, two builds of one document would emit
// their diagnostics in different orders — the byte-identical-output contract
// broken by something no single-run test can observe.
func TestEveryAxisReturnsOneSequenceAcrossRepeatedConstructions(t *testing.T) {
	t.Parallel()

	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("metrics", types.FeatureInstance{Type: "core/metrics:1.0.0"})
	features.Set("audit", types.FeatureInstance{Type: "core/audit:1.0.0"})

	inject := &types.InjectBlock{Env: []any{
		map[string]any{"in": "SVC_HOST", "value": "x"},
		map[string]any{"in": "SVC_PORT", "value": "y"},
	}}

	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("primary", types.VerboseUsesEntry{Target: "db"})
	verbose.Set("replica", types.VerboseUsesEntry{Target: "db"})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("backend", types.UsesSocketValue{Verbose: verbose})

	// The components live inside a declared scope rather than at the root,
	// because normalization folds the root surface only for an Aggregate that
	// declares no scope — so a document carrying both would leave its root
	// components outside every scope, which is a shape normalization never emits
	// and which no walk reaches.
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{
		Type: componentTypeURI, Features: features, Inject: inject, Uses: uses,
	})
	components.Set("db", types.ComponentInstance{Type: componentTypeURI})

	scopeFeatures := types.OrderedMap[types.FeatureInstance]{}
	scopeFeatures.Set("tracing", types.FeatureInstance{Type: "core/tracing:1.0.0"})
	scopeFeatures.Set("logging", types.FeatureInstance{Type: "core/logging:1.0.0"})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("edge", types.Subsystem{Components: components, Features: scopeFeatures})
	subsystems.Set("core", types.Subsystem{})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "determinism"},
		Spec:     types.AggregateSpec{Subsystems: subsystems},
	}

	boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
	boundaryFeatures.Set("policy", types.BoundaryFeature{Type: "core/policy:1.0.0"})
	boundaryFeatures.Set("budget", types.BoundaryFeature{Type: "core/budget:1.0.0"})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Features: boundaryFeatures})
	boundaries.Set("stage", types.Boundary{})
	release := &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}}

	// A positive control: the reading below must actually observe something on
	// every axis, or an all-empty comparison would agree with itself and be
	// reported as determinism.
	first := axisReading(t, agg, release)
	for axis, values := range first {
		if len(values) == 0 {
			t.Fatalf("axis %q read empty from the fixture.\n"+
				"INVARIANT: the fixture populates every axis, so each comparison has "+
				"something to compare.\n"+
				"CONSEQUENCE: that axis's comparison succeeds over two empty sequences "+
				"and the agreement is reported as coverage of an axis nothing exercised.", axis)
		}
	}

	for i := 1; i < 24; i++ {
		next := axisReading(t, agg, release)
		for axis, want := range first {
			if !equalStrings(next[axis], want) {
				t.Fatalf("axis %q returned %v on construction %d and %v on the first.\n"+
					"INVARIANT: one input yields one sequence, every time.\n"+
					"CONSEQUENCE: the axis is backed by something unordered, so two builds "+
					"of one document emit diagnostics in different orders.",
					axis, next[axis], i, want)
			}
		}
	}
}

// axisReading reads every axis of a freshly derived coordinate set into
// comparable strings, keyed by axis name.
//
// The documents are re-normalized per call so each reading walks its own
// presentation rather than a shared one, which is what makes the comparison a
// statement about the derivation instead of about one cached result.
func axisReading(t *testing.T, agg *types.Aggregate, release *types.Release) map[string][]string {
	t.Helper()
	view := newNormalizedView(normalized(t, agg), release, CatalogLookup{}, nil)
	c := newCoordinateSet(view)

	out := map[string][]string{}
	out["nodes"] = nodeIDs(c)
	for _, e := range c.Edges() {
		out["edges"] = append(out["edges"], e.Node.ID+"/"+e.Socket+"/"+e.Alias)
	}
	out["subsystems"] = coordinateValues(c.Subsystems())
	out["boundaries"] = coordinateValues(c.Boundaries())
	for _, node := range out["nodes"] {
		out["attachments"] = append(out["attachments"], c.Attachments(node)...)
		for _, mechanism := range c.Mechanisms(node) {
			out["mechanisms"] = append(out["mechanisms"], mechanism)
			out["deliveryKeys"] = append(out["deliveryKeys"], c.DeliveryKeys(node, mechanism)...)
		}
	}
	for _, subsystem := range out["subsystems"] {
		out["subsystemAttachments"] = append(
			out["subsystemAttachments"], c.SubsystemAttachments(subsystem)...)
	}
	for _, boundary := range out["boundaries"] {
		out["boundaryAttachments"] = append(
			out["boundaryAttachments"], c.BoundaryAttachments(boundary)...)
	}
	return out
}

func nodeIDs(c *coordinateSet) []string {
	out := make([]string, 0, len(c.Nodes()))
	for _, n := range c.Nodes() {
		out = append(out, n.ID)
	}
	return out
}

func coordinateValues(coordinates []constraint.AuthoredCoordinate) []string {
	out := make([]string, 0, len(coordinates))
	for _, c := range coordinates {
		out = append(out, c.Value)
	}
	return out
}
