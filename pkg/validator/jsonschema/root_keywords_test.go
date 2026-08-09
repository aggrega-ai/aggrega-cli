// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"testing"
)

// The walk covers a position from the level above it: a value is checked
// against its own constraints by the per-key dispatch of its parent, just
// before the walk descends into it. Every position therefore has exactly
// one checker, and it is never itself — which leaves the instance root, the
// one position with no level above it, checked by nobody unless the walk's
// caller checks it.
//
// The constraints that go missing when it does not are the root's own: the
// families a schema states about the document as a whole rather than about
// any one of its properties. They fail silently and permissively, which is
// the direction no passing document can reveal — a schema in that state
// reads as stricter than one enforcing the same constraints, and every
// document it was written to reject is accepted instead.
//
// Two root families survive without that check and are covered separately
// below, because their presence is what makes the gap read as narrower than
// it is: someone finding required enforced at the root reasonably concludes
// the root is enforced.
var rootKeywordCases = []struct {
	name string
	// schema declares one constraint at its root, alongside a property so
	// the root is a position the index reaches at all.
	schema string
	// violating breaches that root constraint while satisfying everything
	// the schema says about its properties, so nothing but the root
	// constraint can account for a finding.
	violating any
	// satisfying breaches nothing, and is what keeps the case from being
	// satisfied by a validator that rejects everything.
	satisfying any
}{
	{
		name:       "type",
		schema:     `{"type":"object","properties":{"a":{"type":"string"}}}`,
		violating:  "a bare string where an object is declared",
		satisfying: map[string]any{"a": "x"},
	},
	{
		name:       "minProperties",
		schema:     `{"type":"object","minProperties":2,"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
		violating:  map[string]any{"a": "x"},
		satisfying: map[string]any{"a": "x", "b": "y"},
	},
	{
		name:       "maxProperties",
		schema:     `{"type":"object","maxProperties":1,"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
		violating:  map[string]any{"a": "x", "b": "y"},
		satisfying: map[string]any{"a": "x"},
	},
	{
		name:       "propertyNames",
		schema:     `{"type":"object","propertyNames":{"pattern":"^keep"},"properties":{"keepMe":{"type":"string"}}}`,
		violating:  map[string]any{"dropMe": "x"},
		satisfying: map[string]any{"keepMe": "x"},
	},
	{
		name:       "enum",
		schema:     `{"type":"object","enum":[{"a":"only"}],"properties":{"a":{"type":"string"}}}`,
		violating:  map[string]any{"a": "other"},
		satisfying: map[string]any{"a": "only"},
	},
	{
		name:       "const",
		schema:     `{"type":"object","const":{"a":"only"},"properties":{"a":{"type":"string"}}}`,
		violating:  map[string]any{"a": "other"},
		satisfying: map[string]any{"a": "only"},
	},
	{
		name:       "dependentRequired",
		schema:     `{"type":"object","dependentRequired":{"a":["b"]},"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
		violating:  map[string]any{"a": "x"},
		satisfying: map[string]any{"a": "x", "b": "y"},
	},
}

// TestTheInstanceRootIsCheckedAgainstItsOwnConstraints pins each root
// keyword family in both directions.
//
// Both directions are the point. A validator that has stopped checking the
// root accepts the violating instance, and one that rejects everything
// rejects the satisfying one, so either assertion alone is passed by a
// broken validator. Only the pair distinguishes a working check.
func TestTheInstanceRootIsCheckedAgainstItsOwnConstraints(t *testing.T) {
	t.Parallel()

	for _, tc := range rootKeywordCases {
		t.Run(tc.name, func(t *testing.T) {
			validator := newValidatorFromJSON(t, tc.schema)

			report, err := validator.Validate(tc.violating, nil)
			if err != nil {
				t.Fatalf("validate the violating instance: %v", err)
			}
			if len(report.Findings) == 0 {
				t.Fatalf("an instance breaching the %s the root declares produced no findings; "+
					"a root constraint that is declared and never applied accepts every document "+
					"it was written to reject, and does so silently", tc.name)
			}
			for _, f := range report.Findings {
				if f.FieldPath != "" {
					t.Errorf("the root's own %s was reported at %q rather than at the root, so a "+
						"reader is sent to a property that is not what failed: %+v",
						tc.name, f.FieldPath, f)
				}
			}

			report, err = validator.Validate(tc.satisfying, nil)
			if err != nil {
				t.Fatalf("validate the satisfying instance: %v", err)
			}
			if len(report.Findings) != 0 {
				t.Errorf("an instance satisfying the %s the root declares was rejected, so the "+
					"root check refuses more than the constraint it enforces: %+v",
					tc.name, report.Findings)
			}
		})
	}
}

// TestTheTwoRootFamiliesTheWalkAlreadyCarried names the two families that
// reach the root without the caller's check, so their coverage is recorded
// as belonging to a different mechanism rather than to the one above.
//
// required is checked in the walk's own body, and closedness is checked by
// the per-key dispatch, which is keyed on the parent path and so does fire
// for keys sitting directly at the root. Both would keep passing if the
// root check were removed entirely — which is exactly why they are pinned
// apart from it: read together with the others they would suggest the root
// is covered by one mechanism, and a later reader removing that mechanism
// would see two families still passing and conclude nothing was lost.
func TestTheTwoRootFamiliesTheWalkAlreadyCarried(t *testing.T) {
	t.Parallel()

	t.Run("required", func(t *testing.T) {
		validator := newValidatorFromJSON(t,
			`{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`)
		report, err := validator.Validate(map[string]any{}, nil)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if len(report.Findings) == 0 {
			t.Fatal("a document missing a root-required key produced no findings")
		}
		if got := report.Findings[0].Keyword; got != "required" {
			t.Errorf("expected the missing key to be reported by the walk's own required check; "+
				"got keyword %q", got)
		}
	})

	t.Run("closedness", func(t *testing.T) {
		validator := newValidatorFromJSON(t,
			`{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"}}}`)
		report, err := validator.Validate(map[string]any{"undeclared": 1}, nil)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if len(report.Findings) == 0 {
			t.Fatal("an undeclared key against a closed root produced no findings")
		}
	})
}
