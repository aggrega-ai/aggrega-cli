// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package libraryimport

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
		APIVersion: "aggrega.ai/v1beta1",
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

// TestMapping_SingleLevel pins the base case: a bare-name
// reference resolves to its entry's template fragment verbatim.
func TestMapping_SingleLevel(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{
			"Core": `{"image": "x:{{ .values.tag }}"}`,
		}),
	}
	got, err := Mapping("Helpers#/mappings/Core", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `{"image": "x:{{ .values.tag }}"}`; got != want {
		t.Errorf("fragment = %q; want %q", got, want)
	}
}

// TestMapping_VersionFloorStripped pins that a `:version`
// floor in the reference is stripped — the version is supplied by the resolved
// set (keyed by bare name), so the floor does not affect render-time lookup.
func TestMapping_VersionFloorStripped(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{"Core": "BODY"}),
	}
	got, err := Mapping("Helpers:1.2.0#/mappings/Core", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "BODY" {
		t.Errorf("fragment = %q; want BODY", got)
	}
}

// TestMapping_Recursive pins the recursive case: an entry
// whose whole body is a field-root $import of another library entry resolves to
// that target's terminal fragment.
func TestMapping_Recursive(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{
			"Core": "$import: Base#/mappings/Real",
		}),
		"Base": mappingLibraryFixture("Base", map[string]string{
			"Real": `{"final": true}`,
		}),
	}
	got, err := Mapping("Helpers#/mappings/Core", libs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `{"final": true}`; got != want {
		t.Errorf("fragment = %q; want the recursively-resolved terminal %q", got, want)
	}
}

// TestMapping_RecursiveTargetCarryingHostHalts pins the one
// route a host-bearing reference can still travel. A consuming entity cannot
// author one: every `$import:` position a Recipe or FeatureRecipe carries
// refuses a registry host as the document is read, so such a document never
// reaches a resolver at all. What no schema can describe is a library entry's
// own body, which is declared as a plain string — so a host written there
// arrives here on the recursive hop, and this is where it has to be refused.
//
// Both ends of that route are asserted, because the resolver is reachable only
// behind a host-less reference and a direct call alone would not show that. The
// target library is present and resolvable, so accepting the host would produce
// its fragment and a nil error rather than a different failure — which is what
// makes the refusal the property under test rather than a lookup miss.
func TestMapping_RecursiveTargetCarryingHostHalts(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{
			"Core": "$import: oci://hub.example.test/Base:1.0.0#/mappings/Real",
		}),
		"Base": mappingLibraryFixture("Base", map[string]string{
			"Real": `{"final": true}`,
		}),
	}

	// The recursive hop itself: the reference is host-less, the body it names
	// is not.
	if _, err := Mapping("Helpers#/mappings/Core", libs, nil); !errors.Is(err, ErrHostInLibraryImport()) {
		t.Errorf("error = %v; want ErrHostInLibraryImport from the recursive hop", err)
	}
}

// TestMapping_CycleHalts pins the cycle guard on the $import
// surface: a library↔library import cycle halts with ErrCircularImport — the
// shared refcycle.ErrCycle sentinel — naming the chain, rather than recursing
// without bound. This runs against the UNMUTATED guard (it terminates); the
// guard's logic is proven non-vacuous at the unit level in pkg/refcycle, never
// by disabling it against this recursive fixture.
func TestMapping_CycleHalts(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"A": mappingLibraryFixture("A", map[string]string{"X": "$import: B#/mappings/Y"}),
		"B": mappingLibraryFixture("B", map[string]string{"Y": "$import: A#/mappings/X"}),
	}
	_, err := Mapping("A#/mappings/X", libs, nil)
	if err == nil {
		t.Fatal("expected a cycle error; got nil")
	}
	if !errors.Is(err, refcycle.ErrCycle()) {
		t.Errorf("error = %v; want the shared refcycle.ErrCycle identity", err)
	}
}

// TestMapping_Errors pins the resolution-failure surface:
// unknown library, unknown entry, host-bearing reference, and malformed shape.
func TestMapping_Errors(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{"Core": "BODY"}),
	}
	cases := []struct {
		name string
		ref  string
		want error
	}{
		{"unknown library", "Absent#/mappings/Core", ErrMappingLibraryNotFound()},
		{"unknown entry", "Helpers#/mappings/Missing", ErrMappingEntryNotFound()},
		{"host-bearing", "oci://hub/Helpers:1.0.0#/mappings/Core", ErrHostInLibraryImport()},
		{"stale @ version separator", "Helpers@1.0.0#/mappings/Core", ErrMappingImportShape()},
		{"non-mappings fragment", "Helpers#/schemas/Core", ErrMappingImportShape()},
		{"no fragment", "Helpers", ErrMappingImportShape()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Mapping(tc.ref, libs, nil)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v; want %v", err, tc.want)
			}
		})
	}
}

// artifactLibraryFixture builds a one-bundle library with the given file names
// and bodies, in that order.
func artifactLibraryFixture(lib, bundle string, names, bodies []string) types.ArtifactLibrary {
	entry := types.ArtifactLibraryEntry{}
	for i, n := range names {
		entry.Files.Set(n, bodies[i])
	}
	spec := types.ArtifactLibrarySpec{}
	spec.Artifacts.Set(bundle, entry)
	return types.ArtifactLibrary{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "ArtifactLibrary",
		Metadata:   types.ArtifactLibraryMetadata{Name: lib, Version: "1.0.0"},
		Spec:       spec,
	}
}

// TestArtifact_Errors pins the resolution-failure surface of
// the artifacts resolver directly, mirroring the mapping and workload resolvers'
// own error tables: unknown library, unknown bundle, host-bearing reference,
// stale version separator, and malformed shape.
//
// The host-bearing case is the reason this test exists as a direct call rather
// than through the renderer's artifacts projection. The projection gates HasOCIPrefix ahead
// of its library-import branch, so no caller can hand this resolver a
// host-bearing reference — the arm's conformance therefore has to be asserted
// against its specification rather than through a consumer, and asserting it
// here is what keeps the three resolvers' shared error identity true of all
// three rather than of the two that happen to be reachable.
func TestArtifact_Errors(t *testing.T) {
	t.Parallel()
	libs := map[string]types.ArtifactLibrary{
		"SharedCRDs": artifactLibraryFixture("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{"BODY"}),
	}
	cases := []struct {
		name string
		ref  string
		want error
	}{
		{"unknown library", "Absent#/artifacts/WidgetCRD", ErrArtifactLibraryNotFound()},
		{"unknown bundle", "SharedCRDs#/artifacts/Missing", ErrArtifactLibraryEntryNotFound()},
		{"host-bearing", "oci://hub/SharedCRDs:1.0.0#/artifacts/WidgetCRD", ErrHostInLibraryImport()},
		{"stale @ version separator", "SharedCRDs@1.0.0#/artifacts/WidgetCRD", ErrArtifactImportShape()},
		{"non-artifacts fragment", "SharedCRDs#/mappings/WidgetCRD", ErrArtifactImportShape()},
		{"no fragment", "SharedCRDs", ErrArtifactImportShape()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Artifact(tc.ref, libs)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v; want %v", err, tc.want)
			}
		})
	}
}

// TestArtifact_BareNameResolves is the positive counterpart:
// a bare-name reference with an optional version floor resolves to the bundle,
// so the error table above is measuring refusals against a resolver that does
// accept the authored form.
func TestArtifact_BareNameResolves(t *testing.T) {
	t.Parallel()
	libs := map[string]types.ArtifactLibrary{
		"SharedCRDs": artifactLibraryFixture("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{"BODY"}),
	}
	for _, ref := range []string{
		"SharedCRDs#/artifacts/WidgetCRD",
		"SharedCRDs:1.0.0#/artifacts/WidgetCRD",
	} {
		bundle, err := Artifact(ref, libs)
		if err != nil {
			t.Fatalf("Artifact(%q) = %v; want nil", ref, err)
		}
		body, ok := bundle.Files.Get("widget-crd.yaml")
		if !ok || body != "BODY" {
			t.Errorf("bundle file for %q = (%q, %v); want (\"BODY\", true)", ref, body, ok)
		}
	}
}
