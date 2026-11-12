// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package truthy

import "testing"

func TestBool(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"false", false},
		{"False", false},
		{"FALSE", false},
		{"", false},
		{"0", false},
		{"1", true},
		{"3.5", true},
		{"-0", false}, // negative zero equals zero
		{"-1", true},
		{"hello", true}, // arbitrary non-empty string is truthy
		{"  true  ", true},
		{"  false  ", false},
		{"   ", false}, // whitespace-only trims to empty
		{"\t0\n", false},
	}
	for _, c := range cases {
		if got := Bool(c.in); got != c.want {
			t.Errorf("Bool(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}
