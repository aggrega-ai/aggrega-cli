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
// The four cases are the four failure returns ValidateBytes has: a document
// that will not parse, an empty document, a document that violates its
// schema, and an engine that could not run. A fifth return path added later
// with no sentinel is caught here only if it is also added to this table, so
// the table is deliberately exhaustive over the function's returns rather
// than over interesting inputs.
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
			wantMessageFragment: "violates its envelope schema",
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
		})
	}
}

// TestValidateBytes_FindinglessFailuresKeepTheirOwnMessage pins the other half
// of the coverage contract. ErrEntityEnvelope's text advises correcting the
// document, which is right for a malformed document and wrong for an engine
// failure — the document there may be flawless. So attaching the sentinel must
// not put that advice in front of a reader: the message a findingless failure
// reports stays the underlying failure's own, and the specific cause stays
// matchable alongside the sentinel.
func TestValidateBytes_FindinglessFailuresKeepTheirOwnMessage(t *testing.T) {
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
	if strings.Contains(err.Error(), "correct the document") {
		t.Errorf("an engine failure must not tell an author to correct a document that is fine; got %v", err)
	}
	if !strings.Contains(err.Error(), "no embedded schema for kind") {
		t.Errorf("the underlying failure's own message must survive; got %v", err)
	}
}
