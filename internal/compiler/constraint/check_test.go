// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"errors"
	"reflect"
	"testing"
)

// TestCheckCarriesNoStageNoCallerListAndNoTraversal is the guard on the
// framework's central property. A check's placement, its caller coverage and
// what it iterates are computed from its declaration; the moment any of the
// three becomes a field, a declaration and a computation can disagree about the
// same check and the disagreement is invisible.
//
// So the field set is asserted exactly rather than by absence of particular
// names: a guard listing forbidden names would pass the next field somebody
// invents.
func TestCheckCarriesNoStageNoCallerListAndNoTraversal(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(Check[DocumentView, DocumentSubject]{})

	got := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}
	want := []string{"Sentinel", "Severity", "Run"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check's fields are %v; want exactly %v.\n"+
			"INVARIANT: a check declares the facts it reads and what it is invoked once per, and "+
			"nothing else. It carries no stage, no caller list and no traversal.\n"+
			"CONSEQUENCE: the point a check runs at follows from its view, its caller coverage follows "+
			"from that point, and what it iterates follows from its subject. A field asserting any of "+
			"the three can disagree with the value computed from the declaration, and nothing detects "+
			"the disagreement — which is the hand-wired arrangement this framework replaces, "+
			"reintroduced one field at a time.", got, want)
	}
}

// TestCheckRunSignatureIsExactlyTheViewAndTheSubject pins the other half of the
// declaration: a check's predicate receives the facts and one subject instance,
// and hands back objections. Anything else it might receive would be a fact the
// view does not name, or a host power — which no check declares at all, because
// a power belongs to the stage whose fact production exercises it.
func TestCheckRunSignatureIsExactlyTheViewAndTheSubject(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(Check[DocumentView, DocumentSubject]{})
	run, ok := typ.FieldByName("Run")
	if !ok {
		t.Fatalf("Check has no Run field, so there is no predicate to check")
	}
	if run.Type.Kind() != reflect.Func {
		t.Fatalf("Run is a %s; want a func", run.Type.Kind())
	}
	if n := run.Type.NumIn(); n != 2 {
		t.Errorf("Run takes %d argument(s); want exactly 2 — the view and the subject", n)
	}
	if n := run.Type.NumOut(); n != 1 {
		t.Fatalf("Run returns %d value(s); want exactly 1", n)
	}
	if got, want := run.Type.In(0), reflect.TypeOf((*DocumentView)(nil)).Elem(); got != want {
		t.Errorf("Run's first argument is %s; want the declared view %s", got, want)
	}
	if got, want := run.Type.In(1), reflect.TypeOf((*DocumentSubject)(nil)).Elem(); got != want {
		t.Errorf("Run's second argument is %s; want the declared subject %s", got, want)
	}
	if got, want := run.Type.Out(0), reflect.TypeOf([]Objection(nil)); got != want {
		t.Errorf("Run returns %s; want %s — a check raises objections and never a diagnostic", got, want)
	}
}

// TestCheckDeclaredOverTheDocumentKindRunsThroughThePushEntry exercises a whole
// declaration end to end: the check is a value, the framework supplies the
// subject, and the objection carries the provenance the check never assembled.
func TestCheckDeclaredOverTheDocumentKindRunsThroughThePushEntry(t *testing.T) {
	t.Parallel()

	type view struct{ declaredParameters []string }

	unread := Check[view, DocumentSubject]{
		Sentinel: errRuleBroken,
		Severity: SeverityWarning,
		Run: func(v view, s DocumentSubject) []Objection {
			var out []Objection
			for _, name := range v.declaredParameters {
				out = append(out, s.Raise(errRuleBroken, name))
			}
			return out
		},
	}

	got := RunOnDocument("catalog/aggregate.yaml", view{declaredParameters: []string{"db_size"}}, unread)

	if len(got) != 1 {
		t.Fatalf("objections = %d; want 1", len(got))
	}
	if !errors.Is(got[0].Sentinel(), unread.Sentinel) {
		t.Errorf("objection sentinel = %v; want the check's declared sentinel %v", got[0].Sentinel(), unread.Sentinel)
	}
	if got[0].SourceFile() != "catalog/aggregate.yaml" {
		t.Errorf("objection source file = %q; want the presented document's path", got[0].SourceFile())
	}
	if unread.Severity != SeverityWarning {
		t.Errorf("check severity = %q; want %q", unread.Severity, SeverityWarning)
	}
}

// TestSeverityZeroValueNamesNoSeverity pins the documented property that an
// omitted severity reads as omitted. A zero value equal to either of the two
// would make a check that declared none silently acquire one.
func TestSeverityZeroValueNamesNoSeverity(t *testing.T) {
	t.Parallel()

	var zero Severity
	if zero == SeverityFatal || zero == SeverityWarning {
		t.Errorf("the zero Severity equals %q; want a value equal to neither declared severity", zero)
	}
	if SeverityFatal == SeverityWarning {
		t.Errorf("the two severities are equal, so the vocabulary distinguishes nothing")
	}
}
