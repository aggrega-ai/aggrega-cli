// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"
)

// TestValidatePredicateRefusesUnknownRoot pins the refusal a validate: predicate
// raises when it names something the predicate context does not define.
//
// Each row is one authored predicate carrying one mistake, asserted against the
// parameter-contract identity and against the two things the diagnostic has to
// name: the reference the author wrote, and the reference the context defines.
// Without the refusal none of these rows produces a message naming either — the
// first renders neither true nor false, and the second renders "false" and is
// reported as the VALUE failing validation, which is a message about the wrong
// thing entirely.
func TestValidatePredicateRefusesUnknownRoot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		predicate string
		wantRef   string
	}{
		{"bare reference to an undefined root", `{{ .valu }}`, `".valu"`},
		{"undefined root behind a helper", `{{ gt (.valeu | int) 0 }}`, `".valeu"`},
		{"undefined root as a comparison operand", `{{ eq .valu 1 }}`, `".valu"`},
		{"undefined root reached through the root sigil", `{{ $.valu }}`, `"$.valu"`},
		{"undefined root bound to a variable", `{{ $v := .valu }}{{ gt ($v | int) 0 }}`, `".valu"`},
		{"undefined root rebinding the dot", `{{ with .valu }}{{ . }}{{ end }}`, `".valu"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			contract := buildContract(t,
				"replicas", map[string]any{
					"type":     "integer",
					"validate": tc.predicate,
				},
			)
			errs := ValidateParametersAgainstContract(
				contract, map[string]any{"replicas": 3}, "test.yaml")
			if len(errs) != 1 {
				t.Fatalf("expected 1 error; got %d: %v", len(errs), errs)
			}
			if !errors.Is(errs[0].Err, ErrParameterContractViolation()) {
				t.Errorf("expected the parameter-contract identity; got %v", errs[0].Err)
			}
			if !strings.Contains(errs[0].Message, tc.wantRef) {
				t.Errorf("expected the refused reference %s quoted; got %q", tc.wantRef, errs[0].Message)
			}
			if !strings.Contains(errs[0].Message, ".value") {
				t.Errorf("expected the admitted reference .value named; got %q", errs[0].Message)
			}
		})
	}
}

// TestValidatePredicateAdmitsLegalAuthoring is the positive control for the
// refusal above: every shape here reaches the value under validation legally and
// must still be evaluated rather than refused.
//
// The rows past the direct spelling are the ones that decide the reference walk's
// polarity. A walk that attributed every field read to the context root would
// refuse a read inside a range or with body, and the trailing key of a chained
// fallback — all of which are legal authoring against a value whose shape the
// parameter contract does not declare.
func TestValidatePredicateAdmitsLegalAuthoring(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		predicate string
		value     any
	}{
		{"direct read", `{{ gt (.value | int) 0 }}`, 3},
		{"read through the root sigil", `{{ gt ($.value | int) 0 }}`, 3},
		{"read through a bound variable", `{{ $v := .value }}{{ gt ($v | int) 0 }}`, 3},
		{"bare whole-root read", `{{ if .value }}true{{ else }}false{{ end }}`, 3},
		{"field read inside a range body", `{{ range .value }}{{ if .nope }}x{{ end }}{{ end }}true`,
			[]any{map[string]any{"ok": 1}}},
		{"field read inside a with body", `{{ with .value }}{{ if .nope }}x{{ end }}{{ end }}true`,
			map[string]any{"ok": 1}},
		{"trailing key of a chained fallback", `{{ if (default dict .value).nope }}false{{ else }}true{{ end }}`,
			map[string]any{"ok": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			contract := buildContract(t,
				"subject", map[string]any{"validate": tc.predicate},
			)
			errs := ValidateParametersAgainstContract(
				contract, map[string]any{"subject": tc.value}, "test.yaml")
			for _, e := range errs {
				if strings.Contains(e.Message, "does not define") {
					t.Errorf("legal authoring was refused: %q", e.Message)
				}
			}
		})
	}
}
