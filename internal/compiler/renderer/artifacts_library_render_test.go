// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The bundle files carry a template expression (.context.instance_id) so the
// proof exercises the full resolve → EvalTemplate path, not static strings.
const (
	albCRDBody = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: {{ .context.instance_id }}-widgets.example.com
`
	albRBACBody = `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ .context.instance_id }}-widget-operator
`
)

// TestArtifactsLibraryImport_ByteIdenticalToInline is the load-bearing proof (R5,
// Invariant 8): a bundle imported at one artifacts entry expands into N outputs
// that are byte-identical to the equivalent inline artifacts. The library
// indirection resolves in buildArtifactsProjection — the resolver returns each
// bundle file body verbatim, then the SAME preCheck + EvalTemplate runs per file —
// so the projection entries match name-for-name and byte-for-byte.
func TestArtifactsLibraryImport_ByteIdenticalToInline(t *testing.T) {
	t.Parallel()

	// Inline: two artifacts entries authored directly, keyed by output filename.
	tmplCtx, inlineState, nodeCtx := newArtifactsCtx()
	resetArtifacts(inlineState)
	inlineState.Recipe.Spec.Artifacts.Set("widget-crd.yaml", types.ArtifactEntry{Inline: albCRDBody})
	inlineState.Recipe.Spec.Artifacts.Set("widget-rbac.yaml", types.ArtifactEntry{Inline: albRBACBody})
	inlineProj, err := buildArtifactsProjection(nil, tmplCtx, inlineState, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("inline projection: %v", err)
	}

	// Library: one artifacts entry importing a bundle of the same two files.
	tmplCtx2, libState, nodeCtx2 := newArtifactsCtx()
	resetArtifacts(libState)
	libState.Recipe.Spec.Artifacts.Set("ignored-slot-key", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	libState.ArtifactsLibraries = map[string]types.ArtifactsLibrary{
		"SharedCRDs": artifactsLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml", "widget-rbac.yaml"},
			[]string{albCRDBody, albRBACBody}),
	}
	libProj, err := buildArtifactsProjection(nil, tmplCtx2, libState, nodeCtx2, "{{", "}}")
	if err != nil {
		t.Fatalf("library projection: %v", err)
	}

	if len(inlineProj.Entries) != len(libProj.Entries) || len(libProj.Entries) != 2 {
		t.Fatalf("entry counts: inline=%d library=%d (want 2 each)", len(inlineProj.Entries), len(libProj.Entries))
	}
	for i := range inlineProj.Entries {
		in, lib := inlineProj.Entries[i], libProj.Entries[i]
		if in.Name != lib.Name {
			t.Errorf("entry[%d] name: inline=%q library=%q", i, in.Name, lib.Name)
		}
		if string(in.Content) != string(lib.Content) {
			t.Errorf("entry[%d] %q not byte-identical:\n--- inline ---\n%s\n--- library ---\n%s", i, in.Name, in.Content, lib.Content)
		}
	}
	// Sanity: the template evaluated (.context.instance_id → api), so byte-identity
	// is over rendered content, and each bundle file emits under its own filename
	// (OUTPUT-NAMING = filenames-as-authored), not the consumption slot key.
	if libProj.Entries[0].Name != "widget-crd.yaml" {
		t.Errorf("bundle file 0 name = %q; want widget-crd.yaml (not the consumption key)", libProj.Entries[0].Name)
	}
}

// artifactsLibraryFixtureOrdered builds a one-bundle library with the given file
// order (map iteration is unordered, so tests that assert output order set the
// slice form explicitly).
func artifactsLibraryFixtureOrdered(lib, bundle string, names, bodies []string) types.ArtifactsLibrary {
	entry := types.ArtifactsLibraryEntry{}
	for i, n := range names {
		entry.Files.Set(n, bodies[i])
	}
	spec := types.ArtifactsLibrarySpec{}
	spec.Artifacts.Set(bundle, entry)
	return types.ArtifactsLibrary{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "ArtifactsLibrary",
		Metadata:   types.ArtifactsLibraryMetadata{Name: lib, Version: "1.0.0"},
		Spec:       spec,
	}
}

// TestArtifactsLibraryImport_LoudFailures proves the error paths through the real
// buildArtifactsProjection path: a host-bearing oci:// reference, an unknown
// library, an unknown bundle, and a nested field-root $import inside a bundle file
// each halt loudly with the mirrored sentinel.
func TestArtifactsLibraryImport_LoudFailures(t *testing.T) {
	t.Parallel()

	libs := map[string]types.ArtifactsLibrary{
		"SharedCRDs": artifactsLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{albCRDBody}),
		"Nested": artifactsLibraryFixtureOrdered("Nested", "Bad",
			[]string{"f.yaml"}, []string{"$import: SharedCRDs#/artifacts/WidgetCRD"}),
	}
	cases := []struct {
		name string
		imp  string
		want error
	}{
		{"oci host rejected", "oci://hub/SharedCRDs#/artifacts/WidgetCRD", ErrHostInLibraryImport()},
		{"unknown library", "Absent#/artifacts/WidgetCRD", ErrArtifactsLibraryNotFound()},
		{"unknown bundle", "SharedCRDs#/artifacts/Missing", ErrArtifactsLibraryEntryNotFound()},
		{"nested import in bundle file", "Nested#/artifacts/Bad", ErrArtifactImportShape()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmplCtx, state, nodeCtx := newArtifactsCtx()
			resetArtifacts(state)
			state.Recipe.Spec.Artifacts.Set("slot", types.ArtifactEntry{Import: tc.imp})
			state.ArtifactsLibraries = libs
			_, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
			if err == nil {
				t.Fatalf("expected a loud failure for %q; got nil", tc.imp)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v; want %v", err, tc.want)
			}
		})
	}
}

// TestArtifactsLibraryImport_OutputCollisionHalts proves that two artifact outputs
// resolving to the same filename halt loudly rather than silently overwriting: an
// inline entry named the same as a bundle file collides.
func TestArtifactsLibraryImport_OutputCollisionHalts(t *testing.T) {
	t.Parallel()

	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	// An inline artifact named widget-crd.yaml, plus a bundle that also emits a
	// widget-crd.yaml file → the same output filename twice.
	state.Recipe.Spec.Artifacts.Set("widget-crd.yaml", types.ArtifactEntry{Inline: albCRDBody})
	state.Recipe.Spec.Artifacts.Set("slot", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	state.ArtifactsLibraries = map[string]types.ArtifactsLibrary{
		"SharedCRDs": artifactsLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{albCRDBody}),
	}
	_, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if !errors.Is(err, ErrArtifactOutputCollision()) {
		t.Fatalf("expected ErrArtifactOutputCollision; got %v", err)
	}
}
