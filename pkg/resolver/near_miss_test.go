// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import "testing"

// The near-miss predicate is tested here, beside its own declaration, rather
// than through either reading that consumes it. Both readings share it, so a
// test reaching it through one of them would leave the other's behaviour
// attributed to a case this table already covers.
func TestEditDistanceWithin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b string
		max  int
		want bool
	}{
		{"db_password", "db_password", 1, true},
		// A transposition is the commonest typo and must cost ONE edit, or the
		// reading misses the case it primarily exists to catch.
		{"db_passwrod", "db_password", 1, true},
		{"db_passwor", "db_password", 1, true},   // one deletion
		{"db_passwordd", "db_password", 1, true}, // one insertion
		{"db_pussword", "db_password", 1, true},  // one substitution
		{"api_key", "api_url", 1, false},         // two substitutions
		{"short", "muchlongername", 1, false},    // length gap alone rejects
		{"db_pasword", "db_passwrod", 1, false},  // two edits apart
	}
	for _, c := range cases {
		if got := editDistanceWithin(c.a, c.b, c.max); got != c.want {
			t.Errorf("editDistanceWithin(%q, %q, %d) = %v; want %v", c.a, c.b, c.max, got, c.want)
		}
	}
}
