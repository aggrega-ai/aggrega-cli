// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package editops

import (
	"errors"
	"strings"
	"testing"
)

// TestCheckRequest_RefusedValueNamesTheShapeGiven pins the wording of a refused
// value: the shape the caller gave, in an author's words, never the Go type the
// request decoded it into.
func TestCheckRequest_RefusedValueNamesTheShapeGiven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts Options
		sent error
		want string
	}{
		{
			name: "a map where a value is set",
			opts: Options{File: "a.yaml", Kind: KindSetValue, Node: []string{"spec"}, Name: "limits",
				Value: map[string]any{"cpu": "2"}},
			sent: errValueNotScalar,
			want: ": given a map",
		},
		{
			name: "a number where a reference is recorded",
			opts: Options{File: "a.yaml", Kind: KindAddComponent, Node: []string{"spec", "components"}, Name: "x",
				Value: 7},
			sent: errValueNotAReference,
			want: ": given a number",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkRequest(tc.opts)
			if !errors.Is(err, tc.sent) {
				t.Fatalf("got %v, want %v", err, tc.sent)
			}
			if !strings.HasSuffix(err.Error(), tc.want) {
				t.Errorf("the refusal must end %q: %q", tc.want, err.Error())
			}
			for _, leak := range []string{"map[", "interface", ": int"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("the refusal still names a Go type (%q): %q", leak, err.Error())
				}
			}
		})
	}
}
