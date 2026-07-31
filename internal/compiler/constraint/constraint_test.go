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
		Check[view, NoHostPowers, DocumentSubject]{
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
		Check[struct{}, NoHostPowers, DocumentSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityWarning,
			Run:      func(struct{}, DocumentSubject) []Objection { return nil },
		})
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
