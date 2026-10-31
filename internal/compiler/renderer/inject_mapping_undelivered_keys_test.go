// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"
)

// TestUndeliveredInjectMappingKeys pins which parsed injectMapping outputs name a
// key the merge does not apply, read off the same parse the merge is given.
//
// The all view is rebuilt from the mechanisms rather than merged, so a delivery
// written there is the one an output can make that reaches nothing. The rows that
// name it include the output writing only all, which the parse returns rather than
// dropping, since the fallback cannot read a dictionary under one key as a flat
// delivery. The rows that name nothing are the controls: every typed mechanism, a
// custom mechanism, a flat dictionary — whose key all is one environment delivery
// — and an all view written empty, which loses nothing.
func TestUndeliveredInjectMappingKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		output string
		want   bool
	}{
		{name: "only all", output: "all:\n  K: v\n", want: true},
		{name: "env beside all", output: "env:\n  K: v\nall:\n  L: w\n", want: true},
		{name: "a custom mechanism beside all", output: `{"vault": {"K": "v"}, "all": {"L": "w"}}`, want: true},

		{name: "the typed mechanisms", output: "env:\n  K: v\nfile:\n  F: /x\narg:\n  A: a\n"},
		{name: "a custom mechanism", output: "vault:\n  K: v\n"},
		{name: "a flat dictionary with a key named all", output: "all: v\nK: w\n"},
		{name: "an all view written empty", output: "env:\n  K: v\nall: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			overlay, _, err := ParseInjectMappingOutput(tc.output)
			if err != nil {
				t.Fatalf("output %q parses; got %v", tc.output, err)
			}
			got := undeliveredInjectMappingKeys(overlay, "the-recipe")
			if !tc.want {
				if len(got) != 0 {
					t.Errorf("every key of %q is delivered; got %v", tc.output, got)
				}
				return
			}
			if len(got) != 1 || got[0].Key != "all" || got[0].Recipe != "the-recipe" {
				t.Errorf("output %q writes deliveries under all; got %v", tc.output, got)
			}
		})
	}
}
