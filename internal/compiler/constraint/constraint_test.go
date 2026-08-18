// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"errors"
	"testing"
)

var errRuleBroken = errors.New("rule broken")

// TestRunOnDocumentRunsOnceAgainstThePresentedDocument pins the push model:
// the caller presents one document, the check runs exactly once, and the
// subject it is handed carries that document's provenance. Nothing enumerates
// a document set, and no set is materialised for the check to be invoked over.
//
// The check reaches the entry as a [Check] value, which is the push side of the
// property every pull traversal holds: the invocation site takes declarations
// and not bare predicates.
func TestRunOnDocumentRunsOnceAgainstThePresentedDocument(t *testing.T) {
	t.Parallel()

	type view struct{ paramNames []string }

	invocations := 0
	var seenView view
	var seenFile, seenLocator string

	got := RunOnDocument("catalog/aggregate.yaml", view{paramNames: []string{"db_size"}},
		Check[view, DocumentSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityFatal,
			Run: func(v view, s DocumentSubject) []Objection {
				invocations++
				seenView = v
				seenFile = s.SourceFile()
				seenLocator = s.Locator()
				return []Objection{s.Raise(errRuleBroken, "db_size")}
			},
		})

	if invocations != 1 {
		t.Errorf("check invocations = %d; want exactly 1", invocations)
	}
	if len(seenView.paramNames) != 1 || seenView.paramNames[0] != "db_size" {
		t.Errorf("view reached the check as %+v; want the declared facts unchanged", seenView)
	}
	if seenFile != "catalog/aggregate.yaml" {
		t.Errorf("subject source file = %q; want %q", seenFile, "catalog/aggregate.yaml")
	}
	if seenLocator != "" {
		t.Errorf("document subject locator = %q; want empty — a document is the whole file", seenLocator)
	}
	if len(got) != 1 {
		t.Fatalf("objections returned = %d; want 1", len(got))
	}
	if !errors.Is(got[0].Sentinel(), errRuleBroken) {
		t.Errorf("objection sentinel = %v; want %v", got[0].Sentinel(), errRuleBroken)
	}
	if got[0].SourceFile() != "catalog/aggregate.yaml" {
		t.Errorf("objection source file = %q; want the presented document's path", got[0].SourceFile())
	}
	if got[0].Locator() != "" {
		t.Errorf("objection locator = %q; want empty", got[0].Locator())
	}
	if d := got[0].Detail(); len(d) != 1 || d[0] != "db_size" {
		t.Errorf("objection detail = %v; want [db_size]", d)
	}
}

// TestRunOnDocumentReturnsNilWhenTheCheckRaisesNothing pins that a clean
// document produces no objection rather than an empty-but-present one.
func TestRunOnDocumentReturnsNilWhenTheCheckRaisesNothing(t *testing.T) {
	t.Parallel()

	got := RunOnDocument("aggregate.yaml", struct{}{},
		Check[struct{}, DocumentSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityWarning,
			Run:      func(struct{}, DocumentSubject) []Objection { return nil },
		})
	if got != nil {
		t.Errorf("objections = %v; want nil", got)
	}
}

// TestRunOnDocumentParametersInvokesOncePerDeclaredParameter pins the invoked
// count against the enumeration total, and does it that way round deliberately.
//
// A walk that yielded nothing would return no objections, and no objections is
// exactly what a clean document returns — so asserting that nothing was raised
// cannot tell an enforced rule from an unreachable one. The count is therefore
// compared against the number of names presented, and the presented set is
// checked for emptiness first: over an empty set every assertion below holds
// vacuously, which is the shape that reads as a pass while proving nothing.
//
// The transcript is asserted alongside the count, because the two catch
// different failures. A count alone passes a walk that visited the right number
// of wrong instances; a transcript alone passes a walk that visited a subset in
// the right order.
func TestRunOnDocumentParametersInvokesOncePerDeclaredParameter(t *testing.T) {
	t.Parallel()

	const sourceFile = "catalog/aggregate.yaml"

	// Author order rather than sorted order, so a walk that sorted what it was
	// given is visible in the transcript instead of agreeing with it by accident.
	declared := []string{"db_size", "replica_count", "az"}

	if len(declared) == 0 {
		t.Fatalf("the presented parameter set is empty, so every assertion here holds over nothing")
	}

	type view struct{}

	var transcript []string

	got := RunOnDocumentParameters(sourceFile, declared, view{},
		Check[view, DocumentParameterSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityFatal,
			Run: func(_ view, s DocumentParameterSubject) []Objection {
				transcript = append(transcript, s.SourceFile()+"|"+s.Locator()+"|"+s.ParameterName())
				return []Objection{s.Raise(errRuleBroken, s.ParameterName())}
			},
		})

	if len(transcript) != len(declared) {
		t.Fatalf("the check was invoked %d time(s) over %d declared parameter(s).\n"+
			"INVARIANT: a check declaring the parameter kind of a presented document is invoked exactly "+
			"once per parameter that document's contract declares.\n"+
			"CONSEQUENCE: too few and the rule is unenforced over the parameters that were skipped, with "+
			"nothing to distinguish that from a document nothing was wrong with; too many and one "+
			"violation is reported several times.\ngot: %v", len(transcript), len(declared), transcript)
	}

	want := []string{
		sourceFile + "|.spec.parameters.db_size|db_size",
		sourceFile + "|.spec.parameters.replica_count|replica_count",
		sourceFile + "|.spec.parameters.az|az",
	}
	for i := range want {
		if transcript[i] != want[i] {
			t.Errorf("invocation %d carried %q; want %q.\n"+
				"INVARIANT: the order is the order the names were presented in, and the subject's file and "+
				"locator are assembled by the framework.\n"+
				"CONSEQUENCE: a check raises in the order it is invoked, so an order this entry imposed "+
				"rather than preserved changes the order diagnostics appear in. A wrong locator points a "+
				"reader at a key the file does not contain.", i, transcript[i], want[i])
		}
	}

	// Every invocation's objection reaches the caller, so the returned count is
	// the invoked count and not a subset of it.
	if len(got) != len(declared) {
		t.Fatalf("objections returned = %d; want %d, one per invocation", len(got), len(declared))
	}
	for i, o := range got {
		if !errors.Is(o.Sentinel(), errRuleBroken) {
			t.Errorf("objection %d sentinel = %v; want %v", i, o.Sentinel(), errRuleBroken)
		}
		if o.SourceFile() != sourceFile {
			t.Errorf("objection %d source file = %q; want the presented document's path", i, o.SourceFile())
		}
		if d := o.Detail(); len(d) != 1 || d[0] != declared[i] {
			t.Errorf("objection %d detail = %v; want [%s]", i, d, declared[i])
		}
	}
}

// TestRunOnDocumentParametersOverNoDeclaredParametersInvokesNothing is the other
// half of the count assertion above, and it is what makes that one readable. It
// establishes that an empty result means an empty contract rather than a walk
// that never ran, so a zero count is attributable.
func TestRunOnDocumentParametersOverNoDeclaredParametersInvokesNothing(t *testing.T) {
	t.Parallel()

	invocations := 0

	got := RunOnDocumentParameters("catalog/aggregate.yaml", nil, struct{}{},
		Check[struct{}, DocumentParameterSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityFatal,
			Run: func(struct{}, DocumentParameterSubject) []Objection {
				invocations++
				return []Objection{}
			},
		})

	if invocations != 0 {
		t.Errorf("check invocations over a document declaring no parameter = %d; want 0.\n"+
			"CONSEQUENCE: an invocation here would hand the check a subject naming a parameter the "+
			"contract does not declare, and a rule would be raised about something that does not exist.",
			invocations)
	}
	if got != nil {
		t.Errorf("objections = %v; want nil", got)
	}
}

// TestZeroObjectionIsNotARaisedObjection pins the acknowledged bound of the
// unforgeable-currency property. The zero value of a Go struct is always
// constructible, so an Objection can be brought into existence outside this
// package — but only the empty one, which carries no sentinel and no
// provenance and is distinguishable from a raised objection by exactly that.
func TestZeroObjectionIsNotARaisedObjection(t *testing.T) {
	t.Parallel()

	var zero Objection
	if zero.Sentinel() != nil {
		t.Errorf("zero objection sentinel = %v; want nil", zero.Sentinel())
	}
	if zero.SourceFile() != "" || zero.Locator() != "" {
		t.Errorf("zero objection provenance = (%q, %q); want both empty", zero.SourceFile(), zero.Locator())
	}
	if zero.Detail() != nil {
		t.Errorf("zero objection detail = %v; want nil", zero.Detail())
	}
}

// TestObjectionDetailIsACopy pins that a consumer cannot reach back into an
// objection through the slice it is handed.
func TestObjectionDetailIsACopy(t *testing.T) {
	t.Parallel()

	o := provenance{file: "aggregate.yaml"}.Raise(errRuleBroken, "primary", "cache")
	first := o.Detail()
	first[0] = "mutated"
	second := o.Detail()
	if second[0] != "primary" {
		t.Errorf("detail after caller mutation = %v; want the objection unchanged", second)
	}
}

// TestAuthoredLocatorDialectAndTheEmptyRule pins both halves of the locator
// contract: the dialect a consumer recovering a position splits on, and the
// rule that the framework never invents a locator it cannot derive.
//
// The two component rows are the cases the rule exists for. A component
// authored at the top level of an Aggregate is reached through the
// compiler-synthesized default scope — a container the author never typed — so
// that container contributes no segment and the locator names the shape the
// author did type. A component in an author-declared subsystem contributes its
// scope, because the author typed it. Neither case invents a segment, which is
// the whole rule; the traversal that supplies these segments per component is
// not part of this mechanism.
func TestAuthoredLocatorDialectAndTheEmptyRule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		segments []string
		want     string
	}{
		{
			name:     "nothing authored yields the empty locator",
			segments: nil,
			want:     "",
		},
		{
			name:     "only unauthored containers yields the empty locator",
			segments: []string{"", ""},
			want:     "",
		},
		{
			name:     "component authored at the top level: the synthesized scope contributes no segment",
			segments: []string{"spec", "components", "web"},
			want:     ".spec.components.web",
		},
		{
			name:     "component in an author-declared subsystem contributes its scope",
			segments: []string{"spec", "subsystems", "data", "components", "web"},
			want:     ".spec.subsystems.data.components.web",
		},
		{
			name:     "an unauthored segment mid-path is dropped, not rendered as a gap",
			segments: []string{"spec", "", "components", "web"},
			want:     ".spec.components.web",
		},
		{
			// A key carrying the separator cannot be joined in without the result
			// reading equally as two keys, so the locator names the deepest site
			// that can be walked to and stops. The row after it is what makes the
			// difference from the empty rule visible: an unauthored container is
			// dropped and the walk continues, because a nameable site remains
			// beneath it; an unaddressable key ends the locator, because nothing
			// beneath it can be named relative to a key that cannot be reached.
			name:     "a key carrying the separator ends the locator at its container",
			segments: []string{"spec", "artifacts", "policy.yaml"},
			want:     ".spec.artifacts",
		},
		{
			name:     "everything after an unaddressable key is dropped with it",
			segments: []string{"spec", "components", "web.api", "uses", "database"},
			want:     ".spec.components",
		},
		{
			name:     "a leading unaddressable key leaves no locator at all",
			segments: []string{"a.b", "components"},
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := authoredLocator(tc.segments...); got != tc.want {
				t.Errorf("authoredLocator(%q) = %q; want %q", tc.segments, got, tc.want)
			}
		})
	}
}

// TestRaisingAtALeafPositionsInsideTheSubject pins the raise a rule uses when its
// objection is about a named element inside the subject rather than about the
// subject as a whole.
//
// INVARIANT: the leaf lands on the locator and on the detail, from one call, so a
// reader of the position and a reader of the values cannot be told two different
// things about which element the objection is about.
// CONSEQUENCE: were the leaf carried on the detail alone, a front end would
// position every such finding at the element's owner, and the author would be
// told which component is wrong without being told which key.
func TestRaisingAtALeafPositionsInsideTheSubject(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		segments    []string
		leaf        string
		wantLocator string
	}{
		{
			name:        "a single authored key",
			segments:    []string{"spec", "components", "web"},
			leaf:        "values",
			wantLocator: ".spec.components.web.values",
		},
		{
			// The shape every rule that opts in actually supplies: the contract
			// reports a path several keys deep inside the payload, and it arrives
			// as one already-dotted value rather than as separate segments.
			name:        "a leaf several authored keys deep arrives already joined",
			segments:    []string{"spec", "components", "web"},
			leaf:        "values.replicas",
			wantLocator: ".spec.components.web.values.replicas",
		},
		{
			name:        "a subject inside a declared scope keeps its scope",
			segments:    []string{"spec", "subsystems", "data", "components", "web"},
			leaf:        "infra.size",
			wantLocator: ".spec.subsystems.data.components.web.infra.size",
		},
		{
			// A subject the source file does not name has nothing for the leaf to
			// be named relative to, so the leaf is dropped rather than rooted on
			// nothing. Emitting it alone would name ".values.replicas", a path no
			// file contains.
			name:        "a subject with no authored locator gets none from the leaf either",
			segments:    nil,
			leaf:        "values.replicas",
			wantLocator: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newProvenance("aggregate.yaml", tc.segments...)
			at := p.RaiseAt(errRuleBroken, tc.leaf, "minimum", "expected at least 1")

			if got := at.Locator(); got != tc.wantLocator {
				t.Errorf("locator = %q; want %q", got, tc.wantLocator)
			}
			want := []string{tc.leaf, "minimum", "expected at least 1"}
			if got := at.Detail(); !equalDetail(got, want) {
				t.Errorf("detail = %v; want %v, the leaf first and the rule's own values after it",
					got, want)
			}

			// The control. Raise is the same call without the positioning, so if
			// RaiseAt silently stopped composing, every assertion above would
			// still hold for a subject whose locator happens to be the empty one
			// — and this row is what makes that visible everywhere else.
			plain := p.Raise(errRuleBroken, tc.leaf, "minimum", "expected at least 1")
			if plain.Locator() != p.Locator() {
				t.Errorf("Raise moved the locator to %q; want the subject's own %q — "+
					"positioning belongs to RaiseAt alone", plain.Locator(), p.Locator())
			}
			if tc.wantLocator != "" && at.Locator() == plain.Locator() {
				t.Errorf("RaiseAt and Raise both positioned at %q.\n"+
					"INVARIANT: raising at a leaf positions deeper than raising at the subject.\n"+
					"CONSEQUENCE: the composition has stopped happening and every rule that "+
					"opted in has quietly gone back to naming the element's owner.",
					at.Locator())
			}
		})
	}
}

// TestRaisingAtALeafDoesNotDisturbTheSubject pins that the composition is per
// objection rather than to the subject the framework handed the check.
//
// A check is invoked once per subject and may raise many times, so a composition
// writing through to the subject would make the second objection's position
// depend on the first one's leaf. Nothing about that failure is loud: every
// finding still carries a plausible path.
func TestRaisingAtALeafDoesNotDisturbTheSubject(t *testing.T) {
	t.Parallel()

	p := newProvenance("aggregate.yaml", "spec", "components", "web")

	first := p.RaiseAt(errRuleBroken, "values.replicas")
	second := p.RaiseAt(errRuleBroken, "values.image")

	if first.Locator() != ".spec.components.web.values.replicas" {
		t.Errorf("first locator = %q; want .spec.components.web.values.replicas", first.Locator())
	}
	if second.Locator() != ".spec.components.web.values.image" {
		t.Errorf("second locator = %q; want .spec.components.web.values.image — the second "+
			"raise composed onto the first one's leaf rather than onto the subject",
			second.Locator())
	}
	if p.Locator() != ".spec.components.web" {
		t.Errorf("the subject's own locator is now %q; want .spec.components.web unchanged",
			p.Locator())
	}
}

func equalDetail(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestNestedScopeSegmentsRepeatTheContainerKeyPerLevel pins the segment rule for a
// scope that nests, which the traversal transcript exercises for one shape per
// axis only.
//
// A scope's path is one joined string and the authored document does not spell it
// that way: each level sits under the container's own key again. The nested rows
// are the case the rule exists for — joining the path in as a single segment would
// produce a key no file contains — and the empty row is the arm the traversal
// cannot reach, because a scope with no path is not something any enumerator over
// a normalized document hands over. It is asserted here rather than left as an
// unexercised defensive branch.
//
// # Why both axes are in one table
//
// The two authoring documents each declare a nesting scope, and they are joined
// with different separators: a subsystem path with a dot, a boundary path with a
// slash. One rule serves both, so both belong in the table that pins it — a table
// covering one axis would pass unchanged if the other axis's separator were wrong,
// because splitting on a separator a path does not contain yields the whole path
// as a single level and never an error.
func TestNestedScopeSegmentsRepeatTheContainerKeyPerLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		nesting   scopeNesting
		scopePath string
		tail      []string
		want      string
	}{
		{
			name:      "one level names the scope once",
			nesting:   subsystemNesting,
			scopePath: "billing",
			tail:      []string{"features", "tls"},
			want:      ".spec.subsystems.billing.features.tls",
		},
		{
			name:      "two levels repeat the container key between them",
			nesting:   subsystemNesting,
			scopePath: "billing.payments",
			tail:      []string{"features", "tls"},
			want:      ".spec.subsystems.billing.subsystems.payments.features.tls",
		},
		{
			name:      "three levels keep repeating it",
			nesting:   subsystemNesting,
			scopePath: "a.b.c",
			tail:      []string{"features", "tls"},
			want:      ".spec.subsystems.a.subsystems.b.subsystems.c.features.tls",
		},
		{
			name:      "no tail names the scope alone",
			nesting:   subsystemNesting,
			scopePath: "billing.payments",
			tail:      nil,
			want:      ".spec.subsystems.billing.subsystems.payments",
		},
		{
			name:      "no path invents no locator",
			nesting:   subsystemNesting,
			scopePath: "",
			tail:      []string{"features", "tls"},
			want:      "",
		},
		{
			name:      "a boundary at one level names the scope once",
			nesting:   boundaryNesting,
			scopePath: "prod",
			tail:      []string{"features", "shared-db"},
			want:      ".spec.boundaries.prod.features.shared-db",
		},
		{
			// The row the slash separator exists for. A boundary path is joined with
			// a slash, so splitting it on a dot would leave "prod/edge" as one level
			// and render .spec.boundaries.prod/edge — a key the Release does not have,
			// and one that reads as a plausible locator.
			name:      "a nested boundary repeats the container key between its levels",
			nesting:   boundaryNesting,
			scopePath: "prod/edge",
			tail:      []string{"features", "shared-db"},
			want:      ".spec.boundaries.prod.boundaries.edge.features.shared-db",
		},
		{
			name:      "a boundary three levels deep keeps repeating it",
			nesting:   boundaryNesting,
			scopePath: "prod/edge/eu",
			tail:      nil,
			want:      ".spec.boundaries.prod.boundaries.edge.boundaries.eu",
		},
		{
			name:      "a boundary with no path invents no locator",
			nesting:   boundaryNesting,
			scopePath: "",
			tail:      []string{"features", "shared-db"},
			want:      "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			segments := nestedScopeSegments(tc.nesting, tc.scopePath, tc.tail...)
			if got := authoredLocator(segments...); got != tc.want {
				t.Errorf("the segments for %s scope path %q with tail %q render as %q; want %q.\n"+
					"INVARIANT: a locator names the authored site, and a nesting scope's site spells every "+
					"level under the container's own key.\n"+
					"CONSEQUENCE: a locator that joins the path in as one segment points a reader at a key the "+
					"file does not contain, and one invented for a scope with no path points at nothing at all.",
					tc.nesting.containerKey, tc.scopePath, tc.tail, got, tc.want)
			}
		})
	}
}

// TestEachNestingAxisIsSpelledWithItsOwnSeparator pins the pairing rather than the
// two values, because the pairing is what a call site could get wrong and what
// nothing else would catch.
//
// Splitting a path on a separator it does not contain does not fail: it yields the
// whole path as one level, so a boundary spelled with the subsystem separator
// renders a single flattened segment and reads as a locator. The two axes are
// therefore asserted to disagree, and each is asserted to split the path its own
// document's compiler joins.
func TestEachNestingAxisIsSpelledWithItsOwnSeparator(t *testing.T) {
	t.Parallel()

	if boundaryNesting.separator == subsystemNesting.separator {
		t.Fatalf("both nesting axes split on %q; a shared separator makes one of the two documents' "+
			"paths render as a single flattened segment",
			boundaryNesting.separator)
	}
	if boundaryNesting.containerKey == subsystemNesting.containerKey {
		t.Fatalf("both nesting axes write levels under %q; the container key is the authored key of one "+
			"document's scope block and the two documents do not share it",
			boundaryNesting.containerKey)
	}
	if got, want := boundaryNesting.containerKey, authoredKeyBoundaries; got != want {
		t.Errorf("the boundary axis writes levels under %q; want %q", got, want)
	}
	if got, want := subsystemNesting.containerKey, authoredKeySubsystems; got != want {
		t.Errorf("the subsystem axis writes levels under %q; want %q", got, want)
	}
}
