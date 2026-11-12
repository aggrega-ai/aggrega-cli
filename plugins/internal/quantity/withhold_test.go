// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package quantity

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// TestWithhold_ReportsNoPartOfTheRefusedValue parses one refused value per row and
// holds the reworded refusal to the contract: no part of the value reaches it.
// Where the placeholder is refused for the same reason, the reason survives with
// the placeholder in the value's place; otherwise the reason is withheld with the
// value. Each row first proves the plain refusal does carry the value, so a row
// cannot pass over a refusal that never quoted it.
func TestWithhold_ReportsNoPartOfTheRefusedValue(t *testing.T) {
	t.Parallel()
	withheldReason := "not a quantity; " + ValueWithheld + ", and so is what made it not one"

	cases := []struct {
		name string
		// value is the refused input; quoted is the part of it the plain refusal
		// carries and the reworded one must not.
		value  string
		quoted []string
		want   string
	}{
		{
			name:   "a word refused for the reason the placeholder is refused keeps the reason",
			value:  "s3cret-cpu",
			quoted: []string{"s3cret-cpu"},
			want:   `"` + redact.Placeholder() + `" is not a quantity: it does not begin with a number`,
		},
		{
			name:   "a reason quoting part of the value is withheld with it",
			value:  "512Zsecret",
			quoted: []string{"512Zsecret", "Zsecret"},
			want:   withheldReason,
		},
		{
			name:   "a fractional exponent is withheld",
			value:  "1e1.5",
			quoted: []string{"1e1.5"},
			want:   withheldReason,
		},
		{
			name:   "leading whitespace is withheld",
			value:  " 1",
			quoted: []string{`" 1"`},
			want:   withheldReason,
		},
		{
			name:  "an empty value is withheld",
			value: "",
			want:  withheldReason,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(tc.value)
			if err == nil {
				t.Fatalf("%q parsed; the row needs a refused value", tc.value)
			}
			for _, q := range tc.quoted {
				if !strings.Contains(err.Error(), q) {
					t.Fatalf("the plain refusal does not carry %q, so this row proves nothing: %v", q, err)
				}
			}
			got := Withhold(err).Error()
			if got != tc.want {
				t.Errorf("withheld refusal:\n got: %s\nwant: %s", got, tc.want)
			}
			for _, q := range tc.quoted {
				if strings.Contains(got, q) {
					t.Errorf("the withheld refusal carries %q: %s", q, got)
				}
			}
		})
	}

	t.Run("an error that is not a syntax refusal is withheld whole", func(t *testing.T) {
		t.Parallel()
		got := Withhold(errors.New("something quoting PLANTED-VALUE")).Error()
		if got != withheldReason {
			t.Errorf("got %q, want %q", got, withheldReason)
		}
	})
}
