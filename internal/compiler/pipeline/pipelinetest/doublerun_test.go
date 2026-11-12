// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipelinetest

import "testing"

// TestCompareArtifactMaps_CatchesDifference proves the double-run guard's
// comparison core bites: it reports an error on differing contents, a differing
// file set, and a count mismatch, and reports nil on identical maps. The guard's
// value is catching a difference, which is provable here without a
// nondeterministic pipeline.
func TestCompareArtifactMaps_CatchesDifference(t *testing.T) {
	t.Parallel()

	base := map[string][]byte{
		"a.yaml": []byte("x: 1\n"),
		"b.yaml": []byte("y: 2\n"),
	}

	t.Run("identical-maps-pass", func(t *testing.T) {
		same := map[string][]byte{
			"a.yaml": []byte("x: 1\n"),
			"b.yaml": []byte("y: 2\n"),
		}
		if err := compareArtifactMaps(base, same); err != nil {
			t.Fatalf("identical maps should compare equal; got %v", err)
		}
	})

	t.Run("differing-content-fails", func(t *testing.T) {
		diff := map[string][]byte{
			"a.yaml": []byte("x: 999\n"),
			"b.yaml": []byte("y: 2\n"),
		}
		if err := compareArtifactMaps(base, diff); err == nil {
			t.Fatalf("expected a difference to be caught; got nil")
		}
	})

	t.Run("missing-file-fails", func(t *testing.T) {
		missing := map[string][]byte{
			"a.yaml": []byte("x: 1\n"),
			"c.yaml": []byte("y: 2\n"),
		}
		if err := compareArtifactMaps(base, missing); err == nil {
			t.Fatalf("expected a missing-file mismatch to be caught; got nil")
		}
	})

	t.Run("count-mismatch-fails", func(t *testing.T) {
		fewer := map[string][]byte{"a.yaml": []byte("x: 1\n")}
		if err := compareArtifactMaps(base, fewer); err == nil {
			t.Fatalf("expected a count mismatch to be caught; got nil")
		}
	})
}
