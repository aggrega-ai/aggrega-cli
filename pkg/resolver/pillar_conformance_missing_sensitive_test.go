// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// missingSensitiveSchema declares a sensitive leaf the enclosing object requires,
// and a constraint on the leaf whose refusal would quote its value.
const missingSensitiveSchema = `values:
  auth:
    type: object
    properties:
      password: { type: string, sensitive: true, pattern: "^[a-z]+$" }
    required: [password]
`

// TestPillarConformance_MissingSensitiveFieldIsNamed pins the one finding a
// sensitive leaf keeps its sentence for: a required field the instance does not
// write. There is no value to withhold, so the refusal says which field is missing;
// a value that IS written and refused at the same leaf stays withheld, and so does
// its plaintext.
func TestPillarConformance_MissingSensitiveFieldIsNamed(t *testing.T) {
	t.Parallel()
	const field = `.spec.components["svc"].values.auth.password`
	const secret = "SECRET-PLAINTEXT-123"

	auth := func(password any) types.OrderedMap[any] {
		inner := types.OrderedMap[any]{}
		if password != nil {
			inner.Set("password", password)
		}
		vals := types.OrderedMap[any]{}
		vals.Set("auth", inner)
		return vals
	}

	cases := []struct {
		name     string
		instance types.OrderedMap[any]
		want     []string
		wantNot  []string
	}{
		{
			name:     "the required sensitive field is absent",
			instance: auth(nil),
			want:     []string{"missing required property: password", "(required)"},
			wantNot:  []string{redact.Placeholder()},
		},
		{
			name:     "the sensitive field is written and refused",
			instance: auth(secret),
			want:     []string{redact.Placeholder()},
			wantNot:  []string{secret, "missing required property"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := messagesByField(t, missingSensitiveSchema, "values", tc.instance)
			msg, ok := got[field]
			if !ok {
				t.Fatalf("no finding at %s; got %v", field, got)
			}
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("the finding must read %q: %q", w, msg)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(msg, w) {
					t.Errorf("the finding must not read %q: %q", w, msg)
				}
			}
		})
	}
}
