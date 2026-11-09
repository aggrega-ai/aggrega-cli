// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"strings"
	"testing"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// TestEveryRaisePathOpensWithTheSameSentence is the convergence pin for this
// condition.
//
// One condition is reported by two different composers: the structured
// violation, which carries per-finding positions, and the findingless failure,
// which carries a cause and nothing else. A reader meeting the condition
// through either must be told which condition it is in the same words, or the
// condition has no wording of its own and only a wording per composer.
//
// What makes that convergence possible is that the sentence states the
// condition and stops there. Advice belongs to a path: telling an author to correct the
// document is right where the schema rejected it and wrong where the engine
// could not run, so it is the schema-violation path that supplies it. That is
// the property this test is really protecting, and the two assertions below are
// its two halves — one sentence on every path, and the advice on the one path
// where it is true.
func TestEveryRaisePathOpensWithTheSameSentence(t *testing.T) {
	t.Parallel()

	sentence := ErrEntityEnvelope().Error()

	// The structured-violation path: a well-formed document the schema rejects.
	_, schemaErr := ValidateBytes(KindComponent, []byte(
		"apiVersion: aggrega.ai/v1beta1\nkind: Component\nstrayTopLevelKey: nope\n",
	), "subject.yaml")
	if schemaErr == nil {
		t.Fatal("a document carrying an unknown top-level key must fail the envelope")
	}

	// The findingless path: a valid document whose kind has no embedded schema,
	// so the engine cannot run. The document itself is beyond reproach, which
	// is exactly why advice about correcting it would be wrong here.
	_, engineErr := ValidateBytes(Kind("KindWithNoEmbeddedSchema"), []byte(
		"apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: core/compute/api\n  version: 1.0.0\n",
	), "subject.yaml")
	if engineErr == nil {
		t.Fatal("a kind with no embedded schema must fail the envelope")
	}

	t.Logf("structured violation: %s", schemaErr)
	t.Logf("findingless failure:  %s", engineErr)

	for name, err := range map[string]error{
		"structured violation": schemaErr,
		"findingless failure":  engineErr,
	} {
		if !strings.HasPrefix(err.Error(), sentence) {
			t.Errorf("%s: must open with %q; got %q", name, sentence, err.Error())
		}
	}

	if !strings.Contains(schemaErr.Error(), adviceCorrectDocument) {
		t.Errorf("the schema-violation path must carry the advice that is true of it; got %q", schemaErr.Error())
	}
	if strings.Contains(engineErr.Error(), adviceCorrectDocument) {
		t.Errorf("the findingless path must not advise correcting a document that is fine; got %q", engineErr.Error())
	}
}

// TestErrorRendersOccurrenceAndFindingsAfterTheSentence pins the grouped shape:
// the sentence leads, the kind and file follow it as the occurrence reference,
// each finding keeps its position and key path, and the advice closes.
//
// The structured detail is the part most at risk when a composition changes, so
// it is asserted rather than assumed — a sentence that led while losing the
// positions would read correctly and tell a reader less than before.
func TestErrorRendersOccurrenceAndFindingsAfterTheSentence(t *testing.T) {
	t.Parallel()

	_, err := ValidateBytes(KindComponent, []byte(
		"apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: core/probe\n  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: server\nspec:\n  secretNames: [old_key]\n",
	), "component.yaml")
	if err == nil {
		t.Fatal("the retired key must fail the envelope")
	}
	got := err.Error()

	if want := ErrEntityEnvelope().Error() + " (Component, at component.yaml)"; !strings.HasPrefix(got, want) {
		t.Errorf("the header must be the sentence then the occurrence reference %q; got %q", want, got)
	}
	if !strings.Contains(got, "; 9:3: spec.secretNames:") {
		t.Errorf("each finding must keep its position and key path; got %q", got)
	}
	if !strings.HasSuffix(got, ". "+adviceCorrectDocument) {
		t.Errorf("the advice for this path must close the report; got %q", got)
	}
	if n := strings.Count(got, ErrEntityEnvelope().Error()); n != 1 {
		t.Errorf("the sentence must be said once, not once per finding; said %d times in %q", n, got)
	}
}

// TestErrorOmitsAnOccurrenceValueItWasNotGiven pins the sparse shape. The
// daemon validates an editor buffer that has no path of its own, so the empty
// path is a real call rather than a defensive one, and a report reading
// "(Component, at )" would be a coordinate a reader has to distrust.
func TestErrorOmitsAnOccurrenceValueItWasNotGiven(t *testing.T) {
	t.Parallel()

	_, err := ValidateBytes(KindComponent, []byte(
		"apiVersion: aggrega.ai/v1beta1\nkind: Component\nstrayTopLevelKey: nope\n",
	), "")
	if err == nil {
		t.Fatal("a document carrying an unknown top-level key must fail the envelope")
	}
	got := err.Error()
	if want := ErrEntityEnvelope().Error() + " (Component)"; !strings.HasPrefix(got, want) {
		t.Errorf("with no path supplied the occurrence reference must name the kind alone as %q; got %q", want, got)
	}
	if strings.Contains(got, "at )") {
		t.Errorf("an absent path must be omitted rather than rendered empty; got %q", got)
	}
}

// TestErrorKeepsTheAdviceAfterATruncatedFindingList pins the interaction
// between the finding cap and the trailing advice. The report stops after four
// findings with a count of the rest, so the advice has to survive that early
// break — a reader who hits the cap is the reader most in need of being told
// what to do.
func TestErrorKeepsTheAdviceAfterATruncatedFindingList(t *testing.T) {
	t.Parallel()

	findings := make([]jsonvalidator.Finding, 0, 6)
	for _, key := range []string{"a", "b", "c", "d", "e", "f"} {
		findings = append(findings, jsonvalidator.Finding{FieldPath: "spec." + key, Message: "unexpected property"})
	}
	e := &Error{Kind: KindComponent, Path: "component.yaml", Findings: findings}
	got := e.Error()

	if !strings.Contains(got, "; (+2 more)") {
		t.Errorf("the report must stop after four findings with a count of the rest; got %q", got)
	}
	if !strings.HasSuffix(got, ". "+adviceCorrectDocument) {
		t.Errorf("the advice must survive the truncation; got %q", got)
	}
	if !strings.HasPrefix(got, ErrEntityEnvelope().Error()) {
		t.Errorf("the sentence must still lead; got %q", got)
	}
	if strings.Contains(got, "spec.f") {
		t.Errorf("a finding past the cap must not render; got %q", got)
	}
}
