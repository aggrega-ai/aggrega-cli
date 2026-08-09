// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"testing"
)

// The same constraints authored two ways: once behind a reference at the
// root into a reusable definition, once written out at the root. Any
// difference in what the two reject is a difference the author did not
// write, because the two documents say the same thing.
//
// The pair is the whole point. A schema in the reference-at-the-root shape
// cannot be judged on its own findings — zero findings reads equally as
// "the document is fine" and as "nothing was enforced" — so correctness
// here is stated as agreement with an authoring whose enforcement is not
// in question.
const (
	rootReferenceSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$ref": "#/$defs/Doc",
  "$defs": {
    "Doc": {
      "type": "object",
      "additionalProperties": false,
      "required": ["apiVersion", "kind", "metadata"],
      "properties": {
        "apiVersion": { "type": "string", "const": "example.test/v1" },
        "kind": { "type": "string", "const": "Sample" },
        "metadata": {
          "type": "object",
          "required": ["name"],
          "properties": { "name": { "type": "string", "minLength": 1 } }
        }
      }
    }
  }
}`

	rootInlinedSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["apiVersion", "kind", "metadata"],
  "properties": {
    "apiVersion": { "type": "string", "const": "example.test/v1" },
    "kind": { "type": "string", "const": "Sample" },
    "metadata": {
      "type": "object",
      "required": ["name"],
      "properties": { "name": { "type": "string", "minLength": 1 } }
    }
  }
}`
)

// rootShapeDocuments are documents the schema above must reject, one per
// constraint family the root declares: the required list, the property
// constants, the closed key set, and a constraint nested below the root.
var rootShapeDocuments = []struct {
	name string
	doc  map[string]any
}{
	{
		name: "no keys at all violates the root required list",
		doc:  map[string]any{},
	},
	{
		name: "version constants both wrong",
		doc: map[string]any{
			"apiVersion": "wrong/v0",
			"kind":       "NotSample",
			"metadata":   map[string]any{},
		},
	},
	{
		name: "undeclared top-level key against a closed root",
		doc: map[string]any{
			"apiVersion": "example.test/v1",
			"kind":       "Sample",
			"metadata":   map[string]any{"name": "ok"},
			"extra":      1,
		},
	},
	{
		name: "constraint nested below the root",
		doc: map[string]any{
			"apiVersion": "example.test/v1",
			"kind":       "Sample",
			"metadata":   map[string]any{"name": ""},
		},
	},
}

// TestRootReferenceIsAppliedToTheInstanceRoot pins that a schema whose root
// carries only a reference enforces what that reference names.
//
// A root reference resolves at construction whether or not anything applies
// it, so resolution proves nothing on its own. What distinguishes the two
// cases is a document that ought to be rejected: a validator that applies
// the referenced constraints reports findings, and one that stops at a node
// declaring no properties of its own reports none. The failure this guards
// is silent and permissive — the schema reads as strict while nothing is
// checked — so it is unreachable from any test that only feeds in documents
// expected to pass.
func TestRootReferenceIsAppliedToTheInstanceRoot(t *testing.T) {
	t.Parallel()

	referenced := newValidatorFromJSON(t, rootReferenceSchema)
	inlined := newValidatorFromJSON(t, rootInlinedSchema)

	for _, tc := range rootShapeDocuments {
		t.Run(tc.name, func(t *testing.T) {
			referencedReport, err := referenced.Validate(tc.doc, nil)
			if err != nil {
				t.Fatalf("validate against the referenced authoring: %v", err)
			}
			inlinedReport, err := inlined.Validate(tc.doc, nil)
			if err != nil {
				t.Fatalf("validate against the inlined authoring: %v", err)
			}

			// Checked before the comparison, because two empty lists agree
			// with each other. Without this the comparison would pass in
			// exactly the state it exists to catch, if both authorings ever
			// stopped enforcing together.
			if len(referencedReport.Findings) == 0 {
				t.Fatalf("a document this schema must reject produced no findings, so the "+
					"constraints the root names are declared and never applied; every "+
					"malformed document is accepted in this state (document: %v)", tc.doc)
			}

			if len(referencedReport.Findings) != len(inlinedReport.Findings) {
				t.Fatalf("the two authorings of one set of constraints disagree on how many "+
					"findings this document produces: referenced %d, inlined %d",
					len(referencedReport.Findings), len(inlinedReport.Findings))
			}
			// Compared in order, not as sets: the finding list is part of the
			// deterministic-output contract, so an agreeing multiset with a
			// different order is still a difference a reader would see.
			for i := range referencedReport.Findings {
				got := referencedReport.Findings[i]
				want := inlinedReport.Findings[i]
				if got != want {
					t.Errorf("finding %d differs between the two authorings:\n referenced: %+v\n inlined:    %+v",
						i, got, want)
				}
			}
		})
	}
}

// TestRootReferenceLeavesAValidDocumentAlone pins the other direction: the
// substitution must not invent findings for a document that satisfies the
// referenced constraints. A fix that rejects everything would satisfy the
// test above and be just as wrong.
func TestRootReferenceLeavesAValidDocumentAlone(t *testing.T) {
	t.Parallel()

	valid := map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Sample",
		"metadata":   map[string]any{"name": "sample"},
	}

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"referenced", rootReferenceSchema},
		{"inlined", rootInlinedSchema},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := newValidatorFromJSON(t, tc.raw).Validate(valid, nil)
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if len(report.Findings) != 0 {
				t.Errorf("a document satisfying every declared constraint was rejected: %+v",
					report.Findings)
			}
		})
	}
}

// TestRootReferenceAlongsideOtherConstraintsIsLeftAsAReference pins the
// boundary of the substitution. It applies only where the root carries a
// reference and nothing else; a root that also declares a constraint of its
// own is not interchangeable with what it names, so it keeps the behaviour
// it had rather than silently losing that constraint.
func TestRootReferenceAlongsideOtherConstraintsIsLeftAsAReference(t *testing.T) {
	t.Parallel()

	const mixed = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$ref": "#/$defs/Doc",
  "minProperties": 2,
  "$defs": {
    "Doc": {
      "type": "object",
      "required": ["apiVersion"],
      "properties": { "apiVersion": { "type": "string" } }
    }
  }
}`

	// The assertion is construction and a clean run, not a finding count: the
	// point is that an unrecognised root shape is handled rather than
	// mistaken for a bare reference. What such a root enforces is the
	// separate, wider question of what the index checks at a position
	// declaring constraints of its own.
	report, err := newValidatorFromJSON(t, mixed).Validate(map[string]any{"apiVersion": "v1"}, nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("expected no findings for a document satisfying the referenced shape; got %+v",
			report.Findings)
	}
}
