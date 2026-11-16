// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"slices"
	"testing"
)

// TestTopLevelSectionsAccountForEveryKey holds the list to the template syntax's
// own order and to one disposition per key.
func TestTopLevelSectionsAccountForEveryKey(t *testing.T) {
	t.Parallel()
	want := []struct {
		key  string
		disp disposition
	}{
		{"$schema", dispositionScope},
		{"languageVersion", dispositionStampedChecked},
		{"contentVersion", dispositionStampedReported},
		{"apiProfile", dispositionRefused},
		{"definitions", dispositionCarried},
		{"parameters", dispositionCarried},
		{"variables", dispositionCarried},
		{"functions", dispositionCarriedByMember},
		{"resources", dispositionMergedBySymbol},
		{"outputs", dispositionScopedByNode},
		{"metadata", dispositionCarried},
	}
	if len(topLevelSections) != len(want) {
		t.Fatalf("topLevelSections has %d keys, the template syntax lists %d", len(topLevelSections), len(want))
	}
	var seen []string
	for i, w := range want {
		got := topLevelSections[i]
		if got.key != w.key || got.disposition != w.disp {
			t.Errorf("entry %d is %q/%d, want %q/%d", i, got.key, got.disposition, w.key, w.disp)
		}
		if slices.Contains(seen, got.key) {
			t.Errorf("key %q is listed twice, so it has two dispositions", got.key)
		}
		seen = append(seen, got.key)
		if d, ok := dispositionOf(w.key); !ok || d != w.disp {
			t.Errorf("dispositionOf(%q) = %d, %v", w.key, d, ok)
		}
	}
	if _, ok := dispositionOf("notASection"); ok {
		t.Error("an unlisted key has a disposition")
	}
}
