// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeContractAggregate lays down an Aggregate whose spec.parameters: block is
// body, indented as it sits under that key, and returns its path.
func writeContractAggregate(t *testing.T, body string) string {
	t.Helper()
	src := "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Aggregate\n" +
		"metadata:\n" +
		"  name: test\n" +
		"  version: 1.0.0\n" +
		"spec:\n" +
		"  parameters:\n" +
		body
	path := filepath.Join(t.TempDir(), "aggregate.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadAggregateParameterContract_RefusesAContractThatCannotConstrain drives
// each shape of a contract that constrains nothing through the contract's load,
// with no Release value anywhere, and asserts the gate that refuses it by its
// identifier.
//
// A negative length bound is the schema's to refuse, so its rows assert the
// envelope's condition; a pattern that does not compile is the load-time pass's,
// so its rows assert the contract condition and the key the refusal points at.
// The nested rows cover every place a sub-contract sits — under properties:,
// items: and valuesType: — plus one sub-contract nested inside another.
func TestLoadAggregateParameterContract_RefusesAContractThatCannotConstrain(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name      string
		body      string
		want      error
		wantField string   // the Field of the one finding, where the row pins one
		wantInMsg []string // substrings the rendered error must carry
	}{
		{
			name:      "negative minLength",
			body:      "    name:\n      type: string\n      minLength: -5\n",
			want:      ErrYAMLLoad(),
			wantInMsg: []string{"name", "minLength"},
		},
		{
			name:      "negative maxLength",
			body:      "    name:\n      type: string\n      maxLength: -1\n",
			want:      ErrYAMLLoad(),
			wantInMsg: []string{"name", "maxLength"},
		},
		{
			name:      "pattern that does not compile",
			body:      "    name:\n      type: string\n      pattern: \"([a-z\"\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.name.pattern",
			wantInMsg: []string{`parameter "name"`, `pattern "([a-z"`, "does not compile", "missing closing ]"},
		},
		{
			name:      "pattern that does not compile, under properties",
			body:      "    db:\n      type: object\n      properties:\n        host:\n          type: string\n          pattern: \"([a-z\"\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.db.properties.host.pattern",
			wantInMsg: []string{`parameter "db.properties.host"`, `pattern "([a-z"`, "does not compile"},
		},
		{
			name:      "pattern that does not compile, under items",
			body:      "    names:\n      type: array\n      items:\n        type: string\n        pattern: \"a{2,1}\"\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.names.items.pattern",
			wantInMsg: []string{`parameter "names.items"`, `pattern "a{2,1}"`, "invalid repeat count"},
		},
		{
			name:      "pattern that does not compile, under valuesType",
			body:      "    tags:\n      type: object\n      valuesType:\n        type: string\n        pattern: \"([a-z\"\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.tags.valuesType.pattern",
			wantInMsg: []string{`parameter "tags.valuesType"`, "does not compile"},
		},
		{
			name: "pattern that does not compile, under items inside properties",
			body: "    db:\n      type: object\n      properties:\n        hosts:\n          type: array\n" +
				"          items:\n            type: string\n            pattern: \"([a-z\"\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.db.properties.hosts.items.pattern",
			wantInMsg: []string{`parameter "db.properties.hosts.items"`, "does not compile"},
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			var contractErr error
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("the contract load panicked on a malformed contract: %v", p)
					}
				}()
				_, contractErr = LoadAggregateParameterContract(writeContractAggregate(t, r.body))
			}()
			if contractErr == nil {
				t.Fatalf("expected the contract load to refuse; got nil")
			}
			if !errors.Is(contractErr, r.want) {
				t.Fatalf("want errors.Is(err, %v); got %v", r.want, contractErr)
			}
			if r.wantField != "" {
				var re *ResolverError
				if !errors.As(contractErr, &re) {
					t.Fatalf("expected one *ResolverError finding; got %T: %v", contractErr, contractErr)
				}
				if re.Field != r.wantField {
					t.Errorf("Field = %q; want %q", re.Field, r.wantField)
				}
			}
			msg := contractErr.Error()
			for _, s := range r.wantInMsg {
				if !strings.Contains(msg, s) {
					t.Errorf("error does not carry %q:\n  %s", s, msg)
				}
			}
		})
	}
}

// TestLoadAggregateParameterContract_ReportsEveryBrokenPatternInAuthorOrder pins
// that one load reports every pattern that does not compile, in the order the
// author declared them, rather than stopping at the first.
func TestLoadAggregateParameterContract_ReportsEveryBrokenPatternInAuthorOrder(t *testing.T) {
	t.Parallel()
	body := "    first:\n      type: string\n      pattern: \"([a-z\"\n" +
		"    ok:\n      type: string\n      pattern: \"^[a-z]+$\"\n" +
		"    second:\n      type: string\n      pattern: \"a{2,1}\"\n"
	_, err := LoadAggregateParameterContract(writeContractAggregate(t, body))
	if err == nil {
		t.Fatal("expected the contract load to refuse; got nil")
	}
	var multi *MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("expected a grouped report; got %T: %v", err, err)
	}
	if !errors.Is(err, ErrParameterContractViolation()) {
		t.Errorf("want errors.Is(err, ErrParameterContractViolation()); got %v", err)
	}
	var fields []string
	for _, v := range multi.Violations {
		fields = append(fields, v.Field)
	}
	want := []string{".spec.parameters.first.pattern", ".spec.parameters.second.pattern"}
	if strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Errorf("violation fields = %v; want %v", fields, want)
	}
}

// TestLoadAggregateParameterContract_WellFormedContractsStillLoad is the positive
// control for the refusals above: the same keys, well formed, at every nesting,
// load cleanly. A zero bound is the floor and is legal.
//
// The last rows pin the coverage bound the refusals above stop at. The schema
// describes a parameter's own declaration and does not describe a sub-contract, so
// a negative length bound nested under properties:, items: or valuesType: is not
// reached by the envelope and still loads. A change that makes the envelope reach
// sub-contracts turns these rows into refusals, and they move with it.
func TestLoadAggregateParameterContract_WellFormedContractsStillLoad(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		body string
	}{
		{name: "zero bounds and a compiling pattern", body: "    name:\n      type: string\n      minLength: 0\n      maxLength: 0\n      pattern: \"^$\"\n"},
		{name: "positive bounds", body: "    name:\n      type: string\n      minLength: 1\n      maxLength: 63\n"},
		{name: "no pattern at all", body: "    name:\n      type: string\n      default: x\n"},
		{name: "compiling pattern under properties", body: "    db:\n      type: object\n      properties:\n        host:\n          type: string\n          pattern: \"^[a-z.]+$\"\n"},
		{name: "compiling pattern under items", body: "    names:\n      type: array\n      items:\n        type: string\n        pattern: \"^[a-z]+$\"\n"},
		{name: "compiling pattern under valuesType", body: "    tags:\n      type: object\n      valuesType:\n        type: string\n        pattern: \"^[a-z]+$\"\n"},
		{name: "coverage bound: negative minLength under properties", body: "    db:\n      type: object\n      properties:\n        host:\n          type: string\n          minLength: -5\n"},
		{name: "coverage bound: negative maxLength under items", body: "    names:\n      type: array\n      items:\n        type: string\n        maxLength: -1\n"},
		{name: "coverage bound: negative minLength under valuesType", body: "    tags:\n      type: object\n      valuesType:\n        type: string\n        minLength: -5\n"},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadAggregateParameterContract(writeContractAggregate(t, r.body)); err != nil {
				t.Fatalf("expected the contract to load; got %v", err)
			}
		})
	}
}

// TestValidateParametersAgainstContract_MatchesThePatternCompiledAtLoad pins that
// the per-value check matches against the expression the contract load compiled,
// and does not compile the pattern a second time.
//
// The load must leave the compiled expression behind under the pattern's text. The
// test then puts a DIFFERENT expression in its place and validates: a value only the
// substitute accepts passing, and one only the authored pattern accepts failing, is
// what shows the value check read what the load stored rather than compiling the
// text again. Each row's pattern text is unique to this test, so no other test's
// compiles can stand in for the load's.
func TestValidateParametersAgainstContract_MatchesThePatternCompiledAtLoad(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name     string
		body     string
		pattern  string
		param    string
		supplied func(v string) any
	}{
		{
			name:     "parameter's own declaration",
			body:     "    name:\n      type: string\n      pattern: \"^load-compiled-own$\"\n",
			pattern:  "^load-compiled-own$",
			param:    "name",
			supplied: func(v string) any { return v },
		},
		{
			name:     "under items",
			body:     "    names:\n      type: array\n      items:\n        type: string\n        pattern: \"^load-compiled-items$\"\n",
			pattern:  "^load-compiled-items$",
			param:    "names",
			supplied: func(v string) any { return []any{v} },
		},
		{
			name:     "under properties",
			body:     "    db:\n      type: object\n      properties:\n        host:\n          type: string\n          pattern: \"^load-compiled-properties$\"\n",
			pattern:  "^load-compiled-properties$",
			param:    "db",
			supplied: func(v string) any { return map[string]any{"host": v} },
		},
		{
			name:     "under valuesType",
			body:     "    tags:\n      type: object\n      valuesType:\n        type: string\n        pattern: \"^load-compiled-values-type$\"\n",
			pattern:  "^load-compiled-values-type$",
			param:    "tags",
			supplied: func(v string) any { return map[string]any{"team": v} },
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			contract, err := LoadAggregateParameterContract(writeContractAggregate(t, r.body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if _, ok := parameterPatterns.Load(r.pattern); !ok {
				t.Fatalf("the contract load did not compile %q, so the value check would compile it itself", r.pattern)
			}

			parameterPatterns.Store(r.pattern, regexp.MustCompile(`^substitute$`))

			if errs := ValidateParametersAgainstContract(contract,
				map[string]any{r.param: r.supplied("substitute")}, "test.yaml"); len(errs) != 0 {
				t.Errorf("a value the stored expression accepts was refused, so the value check "+
					"compiled %q again rather than reading the load's compile: %v", r.pattern, errs)
			}
			authored := strings.TrimSuffix(strings.TrimPrefix(r.pattern, "^"), "$")
			if errs := ValidateParametersAgainstContract(contract,
				map[string]any{r.param: r.supplied(authored)}, "test.yaml"); len(errs) != 1 ||
				!errors.Is(errs[0], ErrParameterContractViolation()) {
				t.Errorf("a value only the authored pattern accepts should be refused against the stored "+
					"expression; got %v", errs)
			}
		})
	}
}
