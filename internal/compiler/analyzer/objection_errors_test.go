// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "testing"

// TestAnAdvisoryPositionReadsAsAFileAndAKey pins the composition of the two
// halves an advisory's position is built from.
//
// A warning is a plain string, so its position is composed into the text rather
// than carried on fields of its own — which is exactly why the join matters: a
// locator opens with a separator, so running the pair together produces one
// string that reads as a single file name with a nonsense extension and gives a
// reader no way to see where the document ends and the authored key begins.
func TestAnAdvisoryPositionReadsAsAFileAndAKey(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		sourceFile string
		locator    string
		want       string
	}{
		{
			"a finding carrying both names the key inside the document",
			"/workspace/aggregate.yaml",
			".spec.components.backend.uses.databases",
			"/workspace/aggregate.yaml at .spec.components.backend.uses.databases",
		},
		{
			"a finding whose authored key cannot be named still names the document",
			"/workspace/aggregate.yaml",
			"",
			"/workspace/aggregate.yaml",
		},
		{
			"a finding with no document names the key alone",
			"",
			".spec.components.backend",
			".spec.components.backend",
		},
		{
			"a finding carrying neither has no position at all",
			"",
			"",
			"",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := advisoryPosition(c.sourceFile, c.locator); got != c.want {
				t.Errorf("position = %q, want %q", got, c.want)
			}
		})
	}
}
