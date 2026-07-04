// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "testing"

// TestFoldFirstWin pins the single-source precedence primitive: a fresh identity
// is stored, runs onWin, and returns true; an already-claimed identity leaves the
// map untouched, runs onShadow, and returns false. This is the one decision every
// catalog loading strategy folds through, so its behavior is asserted directly
// rather than only through its three consumers.
func TestFoldFirstWin(t *testing.T) {
	t.Parallel()

	t.Run("absent identity wins and stores the value", func(t *testing.T) {
		t.Parallel()
		claimed := map[string]string{}
		var wonCalled, shadowCalled bool
		got := foldFirstWin(claimed, "id", "first",
			func() { wonCalled = true },
			func() { shadowCalled = true })
		if !got {
			t.Errorf("first occurrence: want true, got false")
		}
		if !wonCalled {
			t.Errorf("first occurrence: onWin must run")
		}
		if shadowCalled {
			t.Errorf("first occurrence: onShadow must not run")
		}
		if claimed["id"] != "first" {
			t.Errorf("first occurrence: want stored %q, got %q", "first", claimed["id"])
		}
	})

	t.Run("present identity shadows and leaves the value unchanged", func(t *testing.T) {
		t.Parallel()
		claimed := map[string]string{"id": "first"}
		var wonCalled, shadowCalled bool
		got := foldFirstWin(claimed, "id", "second",
			func() { wonCalled = true },
			func() { shadowCalled = true })
		if got {
			t.Errorf("duplicate occurrence: want false, got true")
		}
		if wonCalled {
			t.Errorf("duplicate occurrence: onWin must not run")
		}
		if !shadowCalled {
			t.Errorf("duplicate occurrence: onShadow must run")
		}
		if claimed["id"] != "first" {
			t.Errorf("duplicate occurrence: value must stay %q, got %q", "first", claimed["id"])
		}
	})

	t.Run("nil callbacks are tolerated on both branches", func(t *testing.T) {
		t.Parallel()
		claimed := map[string]string{}
		if got := foldFirstWin(claimed, "id", "v", nil, nil); !got {
			t.Errorf("absent + nil callbacks: want true, got false")
		}
		if got := foldFirstWin(claimed, "id", "w", nil, nil); got {
			t.Errorf("present + nil callbacks: want false, got true")
		}
		if claimed["id"] != "v" {
			t.Errorf("value must stay %q, got %q", "v", claimed["id"])
		}
	})
}
