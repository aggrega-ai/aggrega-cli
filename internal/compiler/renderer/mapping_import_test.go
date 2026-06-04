// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// mappingLibraryFixture builds a MappingLibrary from entry-name → template-body
// pairs, inserting in sorted-name order for stable iteration.
func mappingLibraryFixture(name string, entries map[string]string) types.MappingLibrary {
	ml := types.MappingLibrary{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "MappingLibrary",
		Metadata:   types.MappingLibraryMetadata{Name: name, Version: "1.0.0"},
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ml.Spec.Mappings.Set(k, types.MappingEntry{Body: entries[k]})
	}
	return ml
}

// TestResolveMappingLibraryImport_SingleLevel pins the base case: a bare-name
// reference resolves to its entry's template fragment verbatim.
func TestResolveMappingLibraryImport_SingleLevel(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{
			"Core": `{"image": "x:{{ .values.tag }}"}`,
		}),
	}
	got, err := resolveMappingLibraryImport("Helpers#/mappings/Core", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `{"image": "x:{{ .values.tag }}"}`; got != want {
		t.Errorf("fragment = %q; want %q", got, want)
	}
}

// TestResolveMappingLibraryImport_VersionFloorStripped pins that a `@version`
// floor in the reference is stripped — the version is supplied by the resolved
// set (keyed by bare name), so the floor does not affect render-time lookup.
func TestResolveMappingLibraryImport_VersionFloorStripped(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{"Core": "BODY"}),
	}
	got, err := resolveMappingLibraryImport("Helpers@1.2.0#/mappings/Core", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "BODY" {
		t.Errorf("fragment = %q; want BODY", got)
	}
}

// TestResolveMappingLibraryImport_Recursive pins the recursive case: an entry
// whose whole body is a field-root $import of another library entry resolves to
// that target's terminal fragment.
func TestResolveMappingLibraryImport_Recursive(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{
			"Core": "$import: Base#/mappings/Real",
		}),
		"Base": mappingLibraryFixture("Base", map[string]string{
			"Real": `{"final": true}`,
		}),
	}
	got, err := resolveMappingLibraryImport("Helpers#/mappings/Core", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `{"final": true}`; got != want {
		t.Errorf("fragment = %q; want the recursively-resolved terminal %q", got, want)
	}
}

// TestResolveMappingLibraryImport_CycleHalts pins the cycle guard on the $import
// surface: a library↔library import cycle halts with ErrCircularImport — the
// shared refcycle.ErrCycle sentinel — naming the chain, rather than recursing
// without bound. This runs against the UNMUTATED guard (it terminates); the
// guard's logic is proven non-vacuous at the unit level in pkg/refcycle, never
// by disabling it against this recursive fixture.
func TestResolveMappingLibraryImport_CycleHalts(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"A": mappingLibraryFixture("A", map[string]string{"X": "$import: B#/mappings/Y"}),
		"B": mappingLibraryFixture("B", map[string]string{"Y": "$import: A#/mappings/X"}),
	}
	_, err := resolveMappingLibraryImport("A#/mappings/X", libs, nil)
	if err == nil {
		t.Fatal("expected a cycle error; got nil")
	}
	if !errors.Is(err, ErrCircularImport) {
		t.Errorf("error = %v; want ErrCircularImport", err)
	}
	// Shared-primitive conformance: the $import cycle sentinel IS refcycle.ErrCycle,
	// the same identity the $ref surface reports — one detector guards both.
	if !errors.Is(err, refcycle.ErrCycle) {
		t.Errorf("error = %v; want the shared refcycle.ErrCycle identity", err)
	}
}

// TestResolveMappingLibraryImport_Errors pins the resolution-failure surface:
// unknown library, unknown entry, host-bearing reference, and malformed shape.
func TestResolveMappingLibraryImport_Errors(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{"Core": "BODY"}),
	}
	cases := []struct {
		name string
		ref  string
		want error
	}{
		{"unknown library", "Absent#/mappings/Core", ErrMappingLibraryNotFound},
		{"unknown entry", "Helpers#/mappings/Missing", ErrMappingEntryNotFound},
		{"host-bearing", "oci://hub/Helpers@1.0.0#/mappings/Core", ErrHostInLibraryImport},
		{"non-mappings fragment", "Helpers#/schemas/Core", ErrMappingImportShape},
		{"no fragment", "Helpers", ErrMappingImportShape},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveMappingLibraryImport(tc.ref, libs, nil)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v; want %v", err, tc.want)
			}
		})
	}
}
