// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"
)

// TestInterpolateParameters_NamesARootTheAggregateDoesNotDefine pins what a
// failed render of an Aggregate's text says when the template read through a root
// other than .parameters: the reference, as the author spelled it, and the one root
// an Aggregate's text defines, ahead of the engine's own report. A failure on a
// .parameters read says nothing of the kind, because there it would point the
// author at a root they did not write.
func TestInterpolateParameters_NamesARootTheAggregateDoesNotDefine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantNot []string
	}{
		{
			name: "a value read, as a later stage's template would read it",
			raw:  "token: \"{{ .values.app_token }}\"\n",
			want: []string{
				"the template reads .values.app_token, and an Aggregate's text defines no root but .parameters",
				"template execution error",
			},
		},
		{
			name: "the same read through the root variable",
			raw:  "token: \"{{ $.values.app_token }}\"\n",
			want: []string{"the template reads $.values.app_token, and an Aggregate's text defines no root but .parameters"},
		},
		{
			name: "every such reference is named, in the order written",
			raw:  "a: \"{{ .metadata.name }}\"\nb: \"{{ .values.app_token }}\"\n",
			want: []string{"the template reads .metadata.name, .values.app_token, and an Aggregate's text defines no root but .parameters"},
		},
		{
			name:    "a parameter read through an absent parent names no other root",
			raw:     "host: \"{{ .parameters.db.host }}\"\n",
			want:    []string{"template execution error"},
			wantNot: []string{"the template reads", "defines no root"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("interpolation panicked: %v", r)
					}
				}()
				_, err = InterpolateParameters([]byte(tc.raw), map[string]any{})
			}()
			if err == nil {
				t.Fatal("the render must fail")
			}
			if !errors.Is(err, ErrInvalidParameterRef()) {
				t.Errorf("the failure must carry the invalid-parameter-reference condition, got %v", err)
			}
			msg := err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("the failure must read %q:\n%s", w, msg)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(msg, w) {
					t.Errorf("the failure must not read %q:\n%s", w, msg)
				}
			}
		})
	}
}
