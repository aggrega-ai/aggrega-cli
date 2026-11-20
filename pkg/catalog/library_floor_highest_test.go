// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"testing"
)

// TestLibraryFloorSelectsHighestAdmitted holds that a library floor reads as
// `>=floor` within the floor's major and selects the highest held version it
// admits, as a bare reference selects the highest held version.
func TestLibraryFloorSelectsHighestAdmitted(t *testing.T) {
	held := [][2]string{
		{"BaseComponents", "1.0.0"}, {"BaseComponents", "1.1.0"}, {"BaseComponents", "1.2.0"},
	}

	t.Run("a floor selects the highest held version of its major", func(t *testing.T) {
		got, ok, err := selectLibraryVersion(libraryIndex(t, held...), "BaseComponents:1.1.0", mappingLibraryMeta)
		if err != nil || !ok || got != "BaseComponents:1.2.0" {
			t.Fatalf("got (%q, %v, %v), want BaseComponents:1.2.0", got, ok, err)
		}
	})

	t.Run("a floor stops at the next major", func(t *testing.T) {
		withNext := append(append([][2]string{}, held...), [2]string{"BaseComponents", "2.0.0"})
		got, ok, err := selectLibraryVersion(libraryIndex(t, withNext...), "BaseComponents:1.1.0", mappingLibraryMeta)
		if err != nil || !ok || got != "BaseComponents:1.2.0" {
			t.Fatalf("got (%q, %v, %v), want BaseComponents:1.2.0", got, ok, err)
		}
	})

	t.Run("a floor no held version of its major meets stays unsatisfiable", func(t *testing.T) {
		_, ok, err := selectLibraryVersion(libraryIndex(t, held...), "BaseComponents:3.0.0", mappingLibraryMeta)
		if ok || !errors.Is(err, ErrUnsatisfiableEntityVersion()) {
			t.Fatalf("got ok=%v err=%v, want ErrUnsatisfiableEntityVersion", ok, err)
		}
	})

	t.Run("a bare reference selects as it does without a floor", func(t *testing.T) {
		got, ok, err := selectLibraryVersion(libraryIndex(t, held...), "BaseComponents", mappingLibraryMeta)
		if err != nil || !ok || got != "BaseComponents:1.2.0" {
			t.Fatalf("got (%q, %v, %v), want BaseComponents:1.2.0", got, ok, err)
		}
	})
}

// TestLibraryIdentityReadsTheNamedVersion holds that a reference naming a held
// version reads that version, so a recorded version reads back as itself while a
// floor selects the highest version it admits.
func TestLibraryIdentityReadsTheNamedVersion(t *testing.T) {
	idx := libraryIndex(t, [2]string{"Lib", "1.0.0"}, [2]string{"Lib", "1.1.0"}, [2]string{"Lib", "1.2.0"})
	for ref, want := range map[string]string{"Lib:1.1.0": "Lib:1.1.0", "Lib:1.1": "Lib:1.2.0", "Lib": "Lib:1.2.0"} {
		if _, got, ok, err := selectLibrary(idx, ref, mappingLibraryMeta); err != nil || !ok || got != want {
			t.Errorf("selectLibrary(%q) = (%q, %v, %v), want %q", ref, got, ok, err, want)
		}
	}
	if _, got, _, _ := selectLibraryFloor(idx, "Lib:1.1.0", mappingLibraryMeta); got != "Lib:1.2.0" {
		t.Errorf("selectLibraryFloor(Lib:1.1.0) = %q, want Lib:1.2.0", got)
	}
}
