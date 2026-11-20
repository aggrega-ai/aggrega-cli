// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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

// TestLibraryFloorRegistryAgreesWithCatalog holds that a floor records the same
// version whichever tier serves it: the registry's discovery selects, among the
// tags it lists, what a catalog holding the same versions selects.
func TestLibraryFloorRegistryAgreesWithCatalog(t *testing.T) {
	const reference = "reg.test/mappinglibrary/Lib"
	tags := []string{"1.0.0", "1.1.0", "1.2.0", "2.0.0", "latest"}
	listed := func() ([]string, error) { return tags, nil }
	var held [][2]string
	for _, tag := range tags[:4] {
		held = append(held, [2]string{"Lib", tag})
	}
	catalogPick, ok, err := selectLibraryVersion(libraryIndex(t, held...), "Lib:1.1.0", mappingLibraryMeta)
	if err != nil || !ok || catalogPick != "Lib:1.2.0" {
		t.Fatalf("catalog selects (%q, %v, %v), want Lib:1.2.0", catalogPick, ok, err)
	}

	t.Run("a floor selects the highest tag of its major", func(t *testing.T) {
		got, err := DefaultOCIPuller{}.discoverLibraryVersion(reference, "1.1.0", listed)
		if err != nil || "Lib:"+got != catalogPick {
			t.Fatalf("registry selects (%q, %v), want the catalog's %q", got, err, catalogPick)
		}
	})

	t.Run("a withdrawn highest tag is not selected", func(t *testing.T) {
		p := DefaultOCIPuller{retraction: &fixedScreen{withdrawn: map[string]string{"1.2.0": "withdrawn"}}}
		got, err := p.discoverLibraryVersion(reference, "1.1.0", listed)
		if err != nil || got != "1.1.0" {
			t.Fatalf("got (%q, %v), want 1.1.0", got, err)
		}
	})

	t.Run("a floor no tag of its major meets is not found", func(t *testing.T) {
		_, err := DefaultOCIPuller{}.discoverLibraryVersion(reference, "3.0.0", listed)
		if !errors.Is(err, oci.ErrFetchNotFound()) {
			t.Fatalf("err = %v, want ErrFetchNotFound", err)
		}
	})

	t.Run("a listing failure is returned as it is", func(t *testing.T) {
		boom := errors.New("registry unreachable")
		_, err := DefaultOCIPuller{}.discoverLibraryVersion(reference, "1.1.0", func() ([]string, error) { return nil, boom })
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the listing failure", err)
		}
	})

	t.Run("a withdrawn tag named by the floor stays named", func(t *testing.T) {
		screen := &fixedScreen{withdrawn: map[string]string{"1.1.0": "withdrawn"}}
		p := DefaultOCIPuller{retraction: screen}
		got, err := p.discoverLibraryVersion(reference, "1.1.0", func() ([]string, error) { return []string{"1.1.0"}, nil })
		if err != nil || got != "1.1.0" || len(screen.pinned) != 1 {
			t.Fatalf("got (%q, %v), pinned %v, want 1.1.0 announced", got, err, screen.pinned)
		}
	})
}
