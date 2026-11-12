// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestValidateEntry_TypeMismatchNamesTheShapeWritten pins the wording of a
// parameter whose Release value is not of the declared type: the shape the value
// was written in, in the author's words, never the Go type it was decoded into.
func TestValidateEntry_TypeMismatchNamesTheShapeWritten(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		typ   string
		value any
		want  string
	}{
		{"text for an integer", "integer", "abc", `parameter "p" expected type integer; got text`},
		{"a map for a string", "string", map[string]any{"a": 1}, `parameter "p" expected type string; got a map`},
		{"a list for a boolean", "boolean", []any{1}, `parameter "p" expected type boolean; got a list`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := validateEntry("p", types.ParameterContract{Type: tc.typ}, tc.value)
			if len(errs) != 1 {
				t.Fatalf("want one violation, got %v", errs)
			}
			msg := errs[0].Message
			if msg != tc.want {
				t.Errorf("got  %q\nwant %q", msg, tc.want)
			}
		})
	}
}
