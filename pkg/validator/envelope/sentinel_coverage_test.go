// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateBytes_EveryFailureCarriesTheSentinel pins the property that
// makes ErrEntityEnvelope safe to gate on: every error ValidateBytes returns
// matches it. The property belongs to ValidateBytes, not to its callers'
// habits — a caller written as `if errors.Is(err, ErrEntityEnvelope())` treats a
// non-matching error as success, so a return path added without the sentinel
// would be silently skipped at every such call site. This test is what fails
// instead.
//
// The five cases are the five failure returns ValidateBytes has: a document
// that will not parse, an empty document, a document that violates its
// schema, an engine that could not run, and a document the schema accepts
// whose pipeline repeats a step id. A sixth return path added later with no
// sentinel is caught here only if it is also added to this table, so the
// table is deliberately exhaustive over the function's returns rather than
// over interesting inputs.
//
// Every return opens with the sentence of the condition it reports. For four
// of them that is ErrEntityEnvelope's; the repeated step id reports a
// condition of its own, whose sentence it opens with instead, and it matches
// ErrEntityEnvelope all the same.
func TestValidateBytes_EveryFailureCarriesTheSentinel(t *testing.T) {
	t.Parallel()

	valid := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: 1.0.0
spec:
  schema:
    values:
      port: { type: integer }
`)

	cases := []struct {
		name string
		kind Kind
		doc  []byte
		// wantMessageFragment is asserted so a case cannot silently start
		// exercising a different return path than the one it names.
		wantMessageFragment string
		// wantCondition is the condition the return reports, when it is not
		// ErrEntityEnvelope itself.
		wantCondition error
	}{
		{
			// Mixed tabs and spaces defeats both the generic decode and the
			// node re-parse, which is what reaches the parse return.
			name:                "parse failure",
			kind:                KindComponent,
			doc:                 []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n\t- mixed tabs and spaces"),
			wantMessageFragment: "envelope: parse",
		},
		{
			name:                "empty document",
			kind:                KindComponent,
			doc:                 []byte("---\n"),
			wantMessageFragment: "document is empty",
		},
		{
			name:                "schema findings",
			kind:                KindComponent,
			doc:                 []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nstrayTopLevelKey: nope\n"),
			wantMessageFragment: adviceCorrectDocument,
		},
		{
			// A kind with no embedded schema fails inside the validator
			// lookup, which is the same return the engine's own decode,
			// compile, and run failures take. Reaching it through a kind
			// keeps the test honest: no production code is altered to
			// manufacture the failure, and the document itself is valid, so
			// the case cannot pass by accident through the findings path.
			name:                "engine failure",
			kind:                Kind("KindWithNoEmbeddedSchema"),
			doc:                 valid,
			wantMessageFragment: "no embedded schema for kind",
		},
		{
			name:                "repeated pipeline step id",
			kind:                KindRecipe,
			doc:                 []byte(duplicateStepRecipe),
			wantMessageFragment: adviceDistinctStepIDs,
			wantCondition:       ErrDuplicatePipelineStepID(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateBytes(tc.kind, tc.doc, "subject.yaml")
			if err == nil {
				t.Fatalf("%s: expected an error", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantMessageFragment) {
				t.Fatalf("%s: expected the %q return path, got %v", tc.name, tc.wantMessageFragment, err)
			}
			if !errors.Is(err, ErrEntityEnvelope()) {
				t.Errorf("%s: error must match ErrEntityEnvelope; got %v", tc.name, err)
			}
			// Every return path opens with the registered sentence of the
			// condition it reports, whichever of them a document reaches.
			// Asserted here rather than in a test of its own because this is the
			// one place that enumerates every return: a further return added
			// without its sentence fails here, where a separate test exercising
			// one path would not see it.
			condition := ErrEntityEnvelope()
			if tc.wantCondition != nil {
				condition = tc.wantCondition
				if !errors.Is(err, tc.wantCondition) {
					t.Errorf("%s: error must match its own condition %v; got %v", tc.name, tc.wantCondition, err)
				}
			}
			if want := condition.Error(); !strings.HasPrefix(err.Error(), want) {
				t.Errorf("%s: every failure must open with the registered sentence %q; got %v", tc.name, want, err)
			}
		})
	}
}

// TestValidateBytes_FindinglessFailuresCarryNoDocumentAdvice pins the other
// half of the coverage contract, and it is the reason ErrEntityEnvelope's
// sentence states the condition and stops there. Advice to correct the
// document is right for a malformed document and wrong for an engine failure,
// where the document may be flawless — so the advice is supplied by the
// schema-violation path rather than carried in the sentence every path opens
// with. This asserts the outcome: a findingless failure reports the sentence
// and its own cause, and no advice about correcting anything.
//
// The distinction is worth keeping in a test rather than in a comment, because
// the tempting simplification is to move the advice into the sentence, where
// every path would then pick it up.
func TestValidateBytes_FindinglessFailuresCarryNoDocumentAdvice(t *testing.T) {
	t.Parallel()

	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: 1.0.0
`)
	_, err := ValidateBytes(Kind("KindWithNoEmbeddedSchema"), doc, "subject.yaml")
	if err == nil {
		t.Fatal("expected an engine failure for a kind with no embedded schema")
	}
	if strings.Contains(err.Error(), adviceCorrectDocument) {
		t.Errorf("an engine failure must not tell an author to correct a document that is fine; got %v", err)
	}
	if !strings.Contains(err.Error(), "no embedded schema for kind") {
		t.Errorf("the underlying failure's own message must survive; got %v", err)
	}
	if want := ErrEntityEnvelope().Error(); !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the condition's registered sentence must still lead; got %v", err)
	}
}
