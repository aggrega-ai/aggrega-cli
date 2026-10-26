// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateParametersAgainstContract_ArrayMinMaxBoundTheItemCount drives a
// Release list against an array parameter's `min` and `max`, which the compact
// form defines as the item count. Each refused row carries one fault — a count
// outside one bound — and pins that the refusal is the parameter-contract
// condition naming the parameter, the count and the bound; each row that passes
// is the control for the refusal beside it. The bound holds on a parameter's own
// declaration and on an array nested under properties and under items.
func TestValidateParametersAgainstContract_ArrayMinMaxBoundTheItemCount(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name     string
		body     string
		value    any
		wantPath string
		wantText string
	}{
		{
			name:  "control: min met",
			body:  "    p:\n      type: array\n      items: { type: string }\n      min: 1\n",
			value: []any{"a"},
		},
		{
			name:     "an empty list under min 1",
			body:     "    p:\n      type: array\n      items: { type: string }\n      min: 1\n",
			value:    []any{},
			wantPath: "p",
			wantText: `parameter "p" holds 0 item(s), below min 1`,
		},
		{
			name:  "control: max met",
			body:  "    p:\n      type: array\n      max: 1\n",
			value: []any{"a"},
		},
		{
			name:     "two items under max 1",
			body:     "    p:\n      type: array\n      max: 1\n",
			value:    []any{"a", "b"},
			wantPath: "p",
			wantText: `parameter "p" holds 2 item(s), above max 1`,
		},
		{
			name:  "control: a count inside both bounds",
			body:  "    p:\n      type: array\n      min: 2\n      max: 3\n",
			value: []any{"a", "b"},
		},
		{
			name:     "an empty list on an array nested under properties",
			body:     "    p:\n      type: object\n      properties:\n        hosts: { type: array, items: { type: string }, min: 1 }\n",
			value:    map[string]any{"hosts": []any{}},
			wantPath: "p.hosts",
			wantText: `parameter "p.hosts" holds 0 item(s), below min 1`,
		},
		{
			name:     "an empty list on an array nested under items",
			body:     "    p:\n      type: array\n      items: { type: array, min: 1 }\n",
			value:    []any{[]any{"a"}, []any{}},
			wantPath: "p.1",
			wantText: `parameter "p.1" holds 0 item(s), below min 1`,
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			contract, err := LoadAggregateParameterContract(writeContractAggregate(t, r.body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			errs := ValidateParametersAgainstContract(contract, map[string]any{"p": r.value}, "test.yaml")
			if r.wantPath == "" {
				if len(errs) != 0 {
					t.Fatalf("a list inside its bounds was refused: %v", errs)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("want exactly one violation; got %v", errs)
			}
			if !errors.Is(errs[0], ErrParameterContractViolation()) {
				t.Errorf("want ErrParameterContractViolation; got %v", errs[0])
			}
			if errs[0].Field != ".spec.parameters."+r.wantPath {
				t.Errorf("Field = %q; want %q", errs[0].Field, ".spec.parameters."+r.wantPath)
			}
			if !strings.Contains(errs[0].Message, r.wantText) {
				t.Errorf("message does not carry %q: %s", r.wantText, errs[0].Message)
			}
		})
	}
}

// TestValidateParametersAgainstContract_StringMinMaxBoundTheLength drives a
// Release string against a string parameter's `min` and `max`, which the compact
// form defines as its length. One fault per refused row; each control is the
// same declaration with a value inside the bound.
func TestValidateParametersAgainstContract_StringMinMaxBoundTheLength(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name     string
		body     string
		value    string
		wantText string
	}{
		{name: "control: min met", body: "    p:\n      type: string\n      min: 3\n", value: "abc"},
		{
			name: "a string shorter than min", body: "    p:\n      type: string\n      min: 3\n", value: "ab",
			wantText: `parameter "p" length 2, below min 3`,
		},
		{name: "control: max met", body: "    p:\n      type: string\n      max: 2\n", value: "ab"},
		{
			name: "a string longer than max", body: "    p:\n      type: string\n      max: 2\n", value: "abc",
			wantText: `parameter "p" length 3, above max 2`,
		},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			contract, err := LoadAggregateParameterContract(writeContractAggregate(t, r.body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			errs := ValidateParametersAgainstContract(contract, map[string]any{"p": r.value}, "test.yaml")
			if r.wantText == "" {
				if len(errs) != 0 {
					t.Fatalf("a string inside its bound was refused: %v", errs)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("want exactly one violation; got %v", errs)
			}
			if !errors.Is(errs[0], ErrParameterContractViolation()) {
				t.Errorf("want ErrParameterContractViolation; got %v", errs[0])
			}
			if errs[0].Field != ".spec.parameters.p" {
				t.Errorf("Field = %q; want %q", errs[0].Field, ".spec.parameters.p")
			}
			if !strings.Contains(errs[0].Message, r.wantText) {
				t.Errorf("message does not carry %q: %s", r.wantText, errs[0].Message)
			}
		})
	}
}

// TestLoadAggregateParameterContract_RefusesMinMaxWhereTheTypeGivesThemNoMeaning
// pins the declaration refusal: `min` or `max` on a boolean or an object
// parameter bounds nothing, so the contract is refused as it loads, naming the
// key — at a parameter's own declaration and nested alike. The controls are the
// types the pair does mean something on, and an untyped declaration, which
// applies no constraint at all.
func TestLoadAggregateParameterContract_RefusesMinMaxWhereTheTypeGivesThemNoMeaning(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name      string
		body      string
		wantField string
		wantText  string
	}{
		{name: "control: integer", body: "    p:\n      type: integer\n      min: 1\n"},
		{name: "control: string", body: "    p:\n      type: string\n      min: 1\n"},
		{name: "control: array", body: "    p:\n      type: array\n      max: 1\n"},
		{name: "control: no type", body: "    p:\n      min: 1\n"},
		{
			name: "min on a boolean", body: "    p:\n      type: boolean\n      min: 1\n",
			wantField: ".spec.parameters.p.min", wantText: "parameter \"p\" of type boolean declares `min`, which bounds nothing on that type",
		},
		{
			name: "max on an object", body: "    p:\n      type: object\n      max: 3\n",
			wantField: ".spec.parameters.p.max", wantText: "parameter \"p\" of type object declares `max`, which bounds nothing on that type",
		},
		{
			name:      "min on a boolean nested under properties",
			body:      "    p:\n      type: object\n      properties:\n        on: { type: boolean, min: 1 }\n",
			wantField: ".spec.parameters.p.properties.on.min", wantText: "of type boolean declares `min`",
		},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadAggregateParameterContract(writeContractAggregate(t, r.body))
			if r.wantText == "" {
				if err != nil {
					t.Fatalf("a bound the type gives a meaning was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a bound that bounds nothing loaded")
			}
			if !errors.Is(err, ErrParameterContractViolation()) {
				t.Errorf("want ErrParameterContractViolation; got %v", err)
			}
			var re *ResolverError
			if !errors.As(err, &re) {
				t.Fatalf("want one *ResolverError; got %T: %v", err, err)
			}
			if re.Field != r.wantField {
				t.Errorf("Field = %q; want %q", re.Field, r.wantField)
			}
			if !strings.Contains(re.Message, r.wantText) {
				t.Errorf("message does not carry %q: %s", r.wantText, re.Message)
			}
		})
	}
}
