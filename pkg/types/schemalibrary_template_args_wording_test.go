// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"testing"
)

// TestTypecheckParam_NamesTheShapeWritten pins the wording of a template argument
// of the wrong type: the shape the argument was written in, in the author's words,
// never the Go type it was decoded into.
func TestTypecheckParam_NamesTheShapeWritten(t *testing.T) {
	t.Parallel()
	cases := []struct {
		typ  string
		arg  any
		want string
	}{
		{"string", 3, `"p" expected string, got a number`},
		{"integer", "3", `"p" expected integer, got text`},
		{"number", true, `"p" expected number, got a true/false value`},
		{"boolean", map[string]any{"a": 1}, `"p" expected boolean, got a map`},
	}
	for _, tc := range cases {
		err := typecheckParam("p", ParamDecl{Type: tc.typ}, tc.arg)
		if err == nil {
			t.Errorf("%s: a %T argument must be refused", tc.typ, tc.arg)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.typ, err.Error(), tc.want)
		}
	}
}
