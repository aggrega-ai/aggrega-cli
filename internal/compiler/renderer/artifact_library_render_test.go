// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
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

// TestArtifactLibraryImport_ByteIdenticalToInline is the load-bearing proof
// (Invariant 8): a bundle imported at one artifacts entry expands into N outputs
// whose CONTENT is byte-identical to the equivalent inline artifacts. The library
// indirection resolves in buildArtifactsProjection — the resolver returns each
// bundle file body verbatim, then the SAME preCheck + EvalTemplate runs per file —
// so the projection entries match byte-for-byte in the same order.
//
// Content-identity and name-identity are two claims and only the first holds. A
// bundle's output is nested under the key it was consumed at, because one key
// expands into many files and the key alone cannot name them; an inline entry's
// key names exactly one file. So the names differ by construction and the bytes do
// not, and a proof conflating the two would fail for the wrong reason.
func TestArtifactLibraryImport_ByteIdenticalToInline(t *testing.T) {
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
	libState.Recipe.Spec.Artifacts.Set("widgets", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	libState.ArtifactLibraries = map[string]types.ArtifactLibrary{
		"SharedCRDs": artifactLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
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
		if string(in.Content) != string(lib.Content) {
			t.Errorf("entry[%d] %q not byte-identical:\n--- inline ---\n%s\n--- library ---\n%s", i, in.Name, in.Content, lib.Content)
		}
		// Content-identity is only a proof if the bodies are the evaluated ones: two
		// unevaluated templates are also byte-identical, and so are two empty
		// strings.
		if !strings.Contains(string(lib.Content), "name: api-") {
			t.Errorf("entry[%d] body was not evaluated against the render context:\n%s", i, lib.Content)
		}
	}
	// The names differ, and they differ in the stated way: each bundle file is
	// nested under the key the bundle was consumed at, while the inline equivalent
	// is named by its own key.
	wantInline := []string{"widget-crd.yaml", "widget-rbac.yaml"}
	wantLib := []string{"widgets/widget-crd.yaml", "widgets/widget-rbac.yaml"}
	for i := range wantLib {
		if got := inlineProj.Entries[i].Name; got != wantInline[i] {
			t.Errorf("inline entry[%d] name = %q; want %q", i, got, wantInline[i])
		}
		if got := libProj.Entries[i].Name; got != wantLib[i] {
			t.Errorf("bundle entry[%d] name = %q; want %q (nested under the consumption key)", i, got, wantLib[i])
		}
	}
}

// artifactLibraryFixtureOrdered builds a one-bundle library with the given file
// order (map iteration is unordered, so tests that assert output order set the
// slice form explicitly).
func artifactLibraryFixtureOrdered(lib, bundle string, names, bodies []string) types.ArtifactLibrary {
	entry := types.ArtifactLibraryEntry{}
	for i, n := range names {
		entry.Files.Set(n, bodies[i])
	}
	spec := types.ArtifactLibrarySpec{}
	spec.Artifacts.Set(bundle, entry)
	return types.ArtifactLibrary{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "ArtifactLibrary",
		Metadata:   types.ArtifactLibraryMetadata{Name: lib, Version: "1.0.0"},
		Spec:       spec,
	}
}

// TestArtifactLibraryImport_LoudFailures proves the error paths through the real
// buildArtifactsProjection path: a host-bearing oci:// reference, an unknown
// library, an unknown bundle, and a nested field-root $import inside a bundle file
// each halt loudly with the mirrored sentinel.
func TestArtifactLibraryImport_LoudFailures(t *testing.T) {
	t.Parallel()

	libs := map[string]types.ArtifactLibrary{
		"SharedCRDs": artifactLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{albCRDBody}),
		"Nested": artifactLibraryFixtureOrdered("Nested", "Bad",
			[]string{"f.yaml"}, []string{"$import: SharedCRDs#/artifacts/WidgetCRD"}),
	}
	cases := []struct {
		name string
		imp  string
		want error
	}{
		{"oci host rejected", "oci://hub/SharedCRDs#/artifacts/WidgetCRD", ErrHostInLibraryImport()},
		{"unknown library", "Absent#/artifacts/WidgetCRD", ErrArtifactLibraryNotFound()},
		{"unknown bundle", "SharedCRDs#/artifacts/Missing", ErrArtifactLibraryEntryNotFound()},
		{"nested import in bundle file", "Nested#/artifacts/Bad", ErrArtifactImportShape()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmplCtx, state, nodeCtx := newArtifactsCtx()
			resetArtifacts(state)
			state.Recipe.Spec.Artifacts.Set("slot", types.ArtifactEntry{Import: tc.imp})
			state.ArtifactLibraries = libs
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

// TestArtifactLibraryImport_InlineAndBundleFileDoNotCollide proves the input that
// used to be the collision no longer is one: an inline entry whose key is a bundle
// file's filename, beside an entry importing that bundle, emits BOTH files.
//
// The two names are distinguishable because the bundle's file is nested under the
// key it was consumed at, so the inline entry's own key is free to be that
// filename. This is the collision reached by construction rather than by refusal;
// the refusal is retained for what the naming does not make impossible, which is
// proved separately.
func TestArtifactLibraryImport_InlineAndBundleFileDoNotCollide(t *testing.T) {
	t.Parallel()

	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	// An inline artifact named widget-crd.yaml, plus a bundle that also carries a
	// widget-crd.yaml file — the same filename authored twice, at two forms.
	state.Recipe.Spec.Artifacts.Set("widget-crd.yaml", types.ArtifactEntry{Inline: albCRDBody})
	state.Recipe.Spec.Artifacts.Set("slot", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	state.ArtifactLibraries = map[string]types.ArtifactLibrary{
		"SharedCRDs": artifactLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{albCRDBody}),
	}
	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("an inline entry beside a bundle carrying the same filename halted: %v", err)
	}
	got := []string{}
	for _, ent := range proj.Entries {
		got = append(got, ent.Name)
	}
	want := []string{"widget-crd.yaml", "slot/widget-crd.yaml"}
	if len(got) != len(want) {
		t.Fatalf("emitted %v; want both outputs %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry[%d] = %q; want %q", i, got[i], want[i])
		}
	}
	// A pair of empty bodies also fails to collide, because a body that renders to
	// nothing is skipped before any name is registered. Both entries carrying real
	// bytes is what makes the absence of a halt a statement about the names.
	for _, ent := range proj.Entries {
		if len(ent.Content) == 0 {
			t.Errorf("entry %q carries no bytes, so it never reached the name registration", ent.Name)
		}
	}
}
