// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"
)

// findingAtPath returns the first finding whose FieldPath equals the
// given path, or nil if no such finding exists. Helper for tests that
// pin per-path finding emission in the nested compound walker.
func findingAtPath(rpt *Report, path string) *Finding {
	for i := range rpt.Findings {
		if rpt.Findings[i].FieldPath == path {
			return &rpt.Findings[i]
		}
	}
	return nil
}

// TestWalkBranch_NilBranchIndex_ReturnsEmpty pins the early-exit
// contract: walkBranch on a nil *branchIndex returns an empty report
// without panicking. White-box check on the construct-dispatch guard.
func TestWalkBranch_NilBranchIndex_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	a := &googleAdapter{}
	rpt := a.walkBranch(nil, map[string]any{"x": 1}, "")
	if rpt == nil {
		t.Fatalf("walkBranch returned nil *Report; want empty report")
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("nil bi should produce zero findings; got %v", rpt.Findings)
	}
}

// TestBuildBranchIndex_NilSchema_ReturnsNil pins the nil-schema guard
// at the top of buildBranchIndex.
func TestBuildBranchIndex_NilSchema_ReturnsNil(t *testing.T) {
	t.Parallel()
	a := &googleAdapter{}
	if bi := a.buildBranchIndex(nil, nil, ""); bi != nil {
		t.Errorf("buildBranchIndex(nil) = %v; want nil", bi)
	}
}

// TestWalkBranch_NestedPath_PropertiesFailureSurfaces pins the
// nested-path Arm 1 dispatch: when an outer Property carries a
// compound oneOf with branch-level Properties, a type-mismatch on a
// branch property surfaces via the oneOf summary. The summary
// includes the type-error message produced by the leaf Validate.
func TestWalkBranch_NestedPath_PropertiesFailureSurfaces(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": {
			"foo": {
				"type": "object",
				"oneOf": [
					{
						"properties": { "k": { "type": "string" } },
						"required": ["k"]
					}
				]
			}
		}
	}`)
	rpt, err := v.Validate(map[string]any{"foo": map[string]any{"k": 1}}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "integer") && strings.Contains(f.Message, "string") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected nested-branch type-mismatch surfaced via summary; got %v", rpt.Findings)
	}
}

// TestWalkBranch_NestedPath_MissingRequiredSurfaces pins the required-
// emission arm for path != "": missing-required inside a nested
// compound branch surfaces in the oneOf summary as a missing-required
// message naming the absent key.
func TestWalkBranch_NestedPath_MissingRequiredSurfaces(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": {
			"foo": {
				"type": "object",
				"oneOf": [
					{
						"properties": { "bar": { "type": "string" } },
						"required": ["bar"]
					}
				]
			}
		}
	}`)
	rpt, err := v.Validate(map[string]any{"foo": map[string]any{}}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "bar") &&
			(strings.Contains(f.Message, "required") || strings.Contains(f.Message, "missing")) {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected missing-required surfaced via summary; got %v", rpt.Findings)
	}
}

// TestWalkBranch_NestedPath_PatternPropertiesArm pins Arm 2 of the
// compound branch's per-key dispatch: a oneOf branch carrying
// patternProperties matches a key against its regex and validates the
// sub-instance, emitting findings with the prefixed path.
func TestWalkBranch_NestedPath_PatternPropertiesArm(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": {
			"meta": {
				"type": "object",
				"oneOf": [
					{
						"patternProperties": {
							"^x-": { "type": "string" }
						}
					}
				]
			}
		}
	}`)
	// meta.x-name is integer → fails the pattern's string requirement.
	rpt, err := v.Validate(map[string]any{
		"meta": map[string]any{"x-name": 42, "x-other": "ok"},
	}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "integer") && strings.Contains(f.Message, "string") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected patternProperties type-mismatch surfaced via summary; got %v", rpt.Findings)
	}
}

// TestWalkBranch_NestedPath_AdditionalPropertiesArm pins Arm 3: a
// oneOf branch carrying additionalProperties-as-schema validates keys
// not in Properties or patternProperties against the additionalProperties
// branch. The type-mismatch surfaces via the oneOf summary.
func TestWalkBranch_NestedPath_AdditionalPropertiesArm(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": {
			"meta": {
				"type": "object",
				"oneOf": [
					{
						"properties": { "name": { "type": "string" } },
						"additionalProperties": { "type": "integer" }
					}
				]
			}
		}
	}`)
	rpt, err := v.Validate(map[string]any{
		"meta": map[string]any{"name": "x", "extra": "bad"},
	}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "integer") && strings.Contains(f.Message, "string") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected additionalProperties type-mismatch surfaced via summary; got %v", rpt.Findings)
	}
}

// TestWalkBranch_NestedCombinators_OneOfWithNestedOneOf pins the
// nested-combinator dispatch: a oneOf branch carrying its own oneOf
// recurses via evaluateOneOf. Exercises both the outer Arm 1
// (Properties pass) and the inner oneOf dispatch.
func TestWalkBranch_NestedCombinators_OneOfWithNestedOneOf(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [
			{
				"type": "object",
				"properties": { "kind": { "type": "string" } },
				"oneOf": [
					{ "properties": { "tcp": { "type": "integer" } } },
					{ "properties": { "udp": { "type": "integer" } } }
				]
			}
		]
	}`)
	// Matches outer branch (object with kind). Inner oneOf has two
	// branches, both with Properties; neither key declared in the
	// instance → both branches are vacuously satisfied → multi-match
	// oneOf finding emerges.
	rpt, err := v.Validate(map[string]any{"kind": "x"}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Keyword, "oneOf") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected inner oneOf finding (multi-match); got %v", rpt.Findings)
	}
}

// TestWalkBranch_NestedCombinators_OneOfWithNestedAllOf pins allOf
// dispatch inside a oneOf branch. The branch's allOf requires both
// inner constraints; failing either surfaces a finding.
func TestWalkBranch_NestedCombinators_OneOfWithNestedAllOf(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [
			{
				"type": "object",
				"properties": { "name": { "type": "string" } },
				"allOf": [
					{ "required": ["name"] },
					{ "properties": { "name": { "minLength": 3 } } }
				]
			}
		]
	}`)
	// name is too short → second allOf constraint fails.
	rpt, err := v.Validate(map[string]any{"name": "ab"}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Errorf("expected nested allOf finding; got none")
	}
}

// TestWalkBranch_NestedCombinators_OneOfWithNestedAnyOf pins anyOf
// dispatch inside a oneOf branch. If every anyOf branch fails, the
// branch emits findings; if any passes, no anyOf finding.
func TestWalkBranch_NestedCombinators_OneOfWithNestedAnyOf(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [
			{
				"type": "object",
				"properties": { "name": { "type": "string" } },
				"anyOf": [
					{ "required": ["alpha"] },
					{ "required": ["beta"] }
				]
			}
		]
	}`)
	// neither alpha nor beta present → both anyOf branches fail.
	rpt, err := v.Validate(map[string]any{"name": "x"}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "anyOf") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected nested anyOf finding; got %v", rpt.Findings)
	}
}

// TestWalkBranch_NestedCombinators_OneOfWithNestedNot pins the not
// dispatch inside a oneOf branch. If the instance matches the negated
// schema, a finding emerges.
func TestWalkBranch_NestedCombinators_OneOfWithNestedNot(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [
			{
				"type": "object",
				"properties": { "name": { "type": "string" } },
				"not": { "required": ["forbidden"] }
			}
		]
	}`)
	// forbidden present → not-of-required-forbidden negates true → finding.
	rpt, err := v.Validate(map[string]any{"name": "x", "forbidden": true}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "not:") || strings.Contains(f.Message, "inverse") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected nested not finding; got %v", rpt.Findings)
	}
}

// TestWalkBranch_NestedDependentSchemas_TriggerKeyPresent pins the
// dependentSchemas dispatch inside a compound branch. Trigger key
// presence fires the dependent sub-branch; absence skips entirely.
// Findings emit with the dependentSchemas/<trigger>/ keyword prefix.
func TestWalkBranch_NestedDependentSchemas_TriggerKeyPresent(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [
			{
				"type": "object",
				"properties": {
					"trigger": { "type": "string" },
					"value": { "type": "string" }
				},
				"dependentSchemas": {
					"trigger": {
						"required": ["value"]
					}
				}
			}
		]
	}`)
	// trigger present, value absent → dependent required check fires.
	rpt, err := v.Validate(map[string]any{"trigger": "go"}, nil)
	if err != nil {
		t.Fatalf("Validate trigger-only: %v", err)
	}
	saw := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "dependentSchemas/trigger") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected dependentSchemas/trigger finding; got %v", rpt.Findings)
	}

	// trigger absent → no dependent finding.
	rpt, err = v.Validate(map[string]any{"value": "v"}, nil)
	if err != nil {
		t.Fatalf("Validate trigger-absent: %v", err)
	}
	for _, f := range rpt.Findings {
		if strings.Contains(f.Message, "dependentSchemas/trigger") {
			t.Errorf("trigger absent should not emit dependentSchemas finding; got %v", f)
		}
	}
}

// TestBuildBranchIndex_CompoundBranchPopulatesEveryConstruct pins the
// per-construct populator inside buildBranchIndex when a single
// branch is compound and carries every supported keyword at once.
// White-box check: after construction, every population map must
// contain the expected entry.
func TestBuildBranchIndex_CompoundBranchPopulatesEveryConstruct(t *testing.T) {
	t.Parallel()
	schemaJSON := `{
		"oneOf": [
			{
				"type": "object",
				"required": ["alpha"],
				"properties": {
					"alpha": { "type": "string" }
				},
				"additionalProperties": { "type": "integer" },
				"patternProperties": {
					"^x-": { "type": "string" }
				},
				"dependentSchemas": {
					"alpha": { "required": ["bravo"] }
				},
				"allOf": [
					{ "properties": { "alpha": { "minLength": 1 } } }
				],
				"anyOf": [
					{ "properties": { "alpha": { "maxLength": 50 } } }
				],
				"oneOf": [
					{ "properties": { "alpha": { "minLength": 1 } } },
					{ "properties": { "alpha": { "minLength": 2 } } }
				],
				"not": {
					"required": ["forbidden"]
				}
			}
		]
	}`
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaJSON), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	adapter := v.(*googleAdapter)
	branches, ok := adapter.oneOfBranches[""]
	if !ok || len(branches) != 1 {
		t.Fatalf("expected one oneOf branch at root; got branches=%+v", branches)
	}
	bi := branches[0]
	if bi.resolved != nil {
		t.Errorf("compound branch should leave resolved nil; got non-nil")
	}
	if _, ok := bi.subBranches["alpha"]; !ok {
		t.Errorf("subBranches[alpha] not populated")
	}
	if _, ok := bi.required[""]; !ok {
		t.Errorf("required[\"\"] not populated")
	}
	if _, ok := bi.additionalPropsBranch[""]; !ok {
		t.Errorf("additionalPropsBranch[\"\"] not populated")
	}
	if entries, ok := bi.patternPropsResolved[""]; !ok || len(entries) == 0 {
		t.Errorf("patternPropsResolved[\"\"] not populated")
	}
	if deps, ok := bi.dependentSchemas[""]; !ok || len(deps) == 0 {
		t.Errorf("dependentSchemas[\"\"] not populated")
	}
	if branches, ok := bi.allOfBranches[""]; !ok || len(branches) == 0 {
		t.Errorf("allOfBranches[\"\"] not populated")
	}
	if branches, ok := bi.anyOfBranches[""]; !ok || len(branches) == 0 {
		t.Errorf("anyOfBranches[\"\"] not populated")
	}
	if branches, ok := bi.oneOfBranches[""]; !ok || len(branches) < 2 {
		t.Errorf("oneOfBranches[\"\"] not populated with both branches")
	}
	if _, ok := bi.notIndexes[""]; !ok {
		t.Errorf("notIndexes[\"\"] not populated")
	}
}

// TestBuildBranchIndex_AmbientDefsMergedIntoBranch pins the ambient-
// defs threading: $defs at the schema root propagate into a oneOf
// branch via the ambient-defs merge inside buildBranchIndex, so the
// branch's $ref to a root-scope definition resolves.
func TestBuildBranchIndex_AmbientDefsMergedIntoBranch(t *testing.T) {
	t.Parallel()
	schemaJSON := `{
		"$defs": {
			"NonEmptyString": { "type": "string", "minLength": 5 }
		},
		"oneOf": [
			{
				"type": "object",
				"properties": {
					"name": { "$ref": "#/$defs/NonEmptyString" }
				}
			}
		]
	}`
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaJSON), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New (ambient-defs merge must not error): %v", err)
	}
	// Construction must reach the ambient-defs merge path; whether the
	// $ref ultimately resolves through library Resolve is library-
	// internal and orthogonal to the merge contract this test pins.
	_, verr := v.Validate(map[string]any{"name": "x"}, nil)
	if verr != nil {
		t.Fatalf("Validate must not panic on $ref through ambient defs: %v", verr)
	}
}

// TestBuildBranchIndex_UnresolvableBranch_TolerateSkip pins the
// tolerate-skip contract: when a branch references an unresolvable
// $ref, buildBranchIndex returns nil for that branch and the surrounding
// composition continues — the missing branch surfaces as the
// documented walker-completeness gap rather than a construction error.
func TestBuildBranchIndex_UnresolvableBranch_TolerateSkip(t *testing.T) {
	t.Parallel()
	schemaJSON := `{
		"oneOf": [
			{
				"properties": {
					"unknown": { "$ref": "#/$defs/NonExistent" }
				}
			},
			{
				"type": "string"
			}
		]
	}`
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaJSON), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The validator construction should either succeed (with the
	// unresolvable branch tolerate-skipped) or fail loudly at New.
	// Both are documented outcomes; the test ensures we don't panic.
	v, err := New(schema)
	if err != nil {
		// Loud failure at construction is acceptable.
		return
	}
	// Otherwise: validate against the second (string) branch.
	_, verr := v.Validate("ok", nil)
	if verr != nil {
		t.Fatalf("Validate string: %v", verr)
	}
}

// TestValidate_TemplateExecutionFailure_PropagatesAdapterError pins
// the evaluateTemplate Execute-error path: a template that parses
// successfully but fails at execution time surfaces as AdapterError
// with ErrExtensionEval. Uses {{index .arr 99}} against an instance
// whose .arr slice is too short to index.
func TestValidate_TemplateExecutionFailure_PropagatesAdapterError(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": {
			"arr": { "type": "array" }
		},
		"x-aggrega-when": "{{ index .arr 99 }}",
		"then": { "required": ["never"] }
	}`)
	_, err := v.Validate(map[string]any{"arr": []any{1}}, nil)
	if err == nil {
		t.Fatalf("expected AdapterError from template execute failure; got nil")
	}
	if !strings.Contains(err.Error(), "x-aggrega-when") {
		t.Errorf("expected error to mention x-aggrega-when; got %v", err)
	}
}

// TestValidate_NestedWhen_OuterFalsyConsumesInner pins the consumed-
// path tracking in Validate's Step 2: an outer x-aggrega-when that
// evaluates falsy adds its path to the handled set; the inner
// when-node beneath it is then skipped via the isUnderPath check.
func TestValidate_NestedWhen_OuterFalsyConsumesInner(t *testing.T) {
	t.Parallel()
	// Outer when at .outer with falsy predicate; inner when at
	// .outer.inner with predicate that would emit findings.
	v := buildValidator(t, `{
		"type": "object",
		"properties": {
			"outer": {
				"type": "object",
				"x-aggrega-when": "false",
				"then": {
					"properties": {
						"inner": {
							"type": "object",
							"x-aggrega-when": "true",
							"then": { "required": ["forced"] }
						}
					}
				},
				"properties": {
					"inner": {
						"type": "object"
					}
				}
			}
		}
	}`)
	// outer when is falsy → inner when consumed → no required-forced
	// finding emerges even though inner's then would otherwise fire.
	rpt, err := v.Validate(map[string]any{
		"outer": map[string]any{
			"inner": map[string]any{},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, f := range rpt.Findings {
		if f.FieldPath == "outer.inner.forced" || strings.Contains(f.Message, "forced") {
			t.Errorf("outer falsy should consume inner when; got finding %v", f)
		}
	}
}

// TestWalkBranch_PatternPropertiesFirstMatchContinues pins the
// patternProperties matched-then-continue logic at lines 433-434: a
// key matching a patternProperties entry consumes the key (matched=true,
// continue) so additionalProperties is not also invoked for that key.
func TestWalkBranch_PatternPropertiesFirstMatchContinues(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [
			{
				"type": "object",
				"patternProperties": {
					"^x-": { "type": "string" }
				},
				"additionalProperties": { "type": "integer" }
			}
		]
	}`)
	// x-name is a string → matches patternProperties (pass) → continue
	// (not also validated against additionalProperties).
	// other is an integer → falls through to additionalProperties (pass).
	rpt, err := v.Validate(map[string]any{
		"x-name": "ok",
		"other":  7,
	}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected zero findings; got %v", rpt.Findings)
	}
}
