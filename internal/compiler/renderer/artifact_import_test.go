// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestResolveArtifactLibraryImport_Errors pins the resolution-failure surface of
// the artifacts resolver directly, mirroring the mapping and workload resolvers'
// own error tables: unknown library, unknown bundle, host-bearing reference,
// stale version separator, and malformed shape.
//
// The host-bearing case is the reason this test exists as a direct call rather
// than through buildArtifactsProjection. The projection gates HasOCIPrefix ahead
// of its library-import branch, so no caller can hand this resolver a
// host-bearing reference — the arm's conformance therefore has to be asserted
// against its specification rather than through a consumer, and asserting it
// here is what keeps the three resolvers' shared error identity true of all
// three rather than of the two that happen to be reachable.
func TestResolveArtifactLibraryImport_Errors(t *testing.T) {
	t.Parallel()
	libs := map[string]types.ArtifactLibrary{
		"SharedCRDs": artifactLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
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
			_, err := resolveArtifactLibraryImport(tc.ref, libs)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v; want %v", err, tc.want)
			}
		})
	}
}

// TestResolveArtifactLibraryImport_BareNameResolves is the positive counterpart:
// a bare-name reference with an optional version floor resolves to the bundle,
// so the error table above is measuring refusals against a resolver that does
// accept the authored form.
func TestResolveArtifactLibraryImport_BareNameResolves(t *testing.T) {
	t.Parallel()
	libs := map[string]types.ArtifactLibrary{
		"SharedCRDs": artifactLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{"BODY"}),
	}
	for _, ref := range []string{
		"SharedCRDs#/artifacts/WidgetCRD",
		"SharedCRDs:1.0.0#/artifacts/WidgetCRD",
	} {
		bundle, err := resolveArtifactLibraryImport(ref, libs)
		if err != nil {
			t.Fatalf("resolveArtifactLibraryImport(%q) = %v; want nil", ref, err)
		}
		body, ok := bundle.Files.Get("widget-crd.yaml")
		if !ok || body != "BODY" {
			t.Errorf("bundle file for %q = (%q, %v); want (\"BODY\", true)", ref, body, ok)
		}
	}
}
