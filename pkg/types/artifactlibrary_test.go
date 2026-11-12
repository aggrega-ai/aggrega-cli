// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types_test

import (
	"errors"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

const artifactLibraryYAML = `apiVersion: aggrega.ai/v1beta1
kind: ArtifactLibrary
metadata:
  name: SharedCRDs
  version: 1.0.0
spec:
  artifacts:
    WidgetCRD:
      widget-crd.yaml: |
        apiVersion: apiextensions.k8s.io/v1
        kind: CustomResourceDefinition
        metadata:
          name: widgets.example.com
      widget-rbac.yaml: |
        apiVersion: rbac.authorization.k8s.io/v1
        kind: ClusterRole
        metadata:
          name: widget-operator
      widget-config.yaml: "kind: ConfigMap"
`

// TestArtifactLibrary_LoadBundleValidatesAndPreservesFileOrder asserts a
// multi-file bundle entry passes envelope validation, unmarshals with each file
// body captured verbatim, and preserves author file order (determinism-load-
// bearing per Invariant 8).
func TestArtifactLibrary_LoadBundleValidatesAndPreservesFileOrder(t *testing.T) {
	t.Parallel()

	if _, err := envelope.ValidateBytes(envelope.KindArtifactLibrary, []byte(artifactLibraryYAML), "test.yaml"); err != nil {
		t.Fatalf("envelope validation of a valid multi-file bundle failed: %v", err)
	}

	var lib types.ArtifactLibrary
	if err := yaml.Unmarshal([]byte(artifactLibraryYAML), &lib); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if lib.Kind != "ArtifactLibrary" || lib.Metadata.Name != "SharedCRDs" {
		t.Fatalf("unexpected envelope: kind=%q name=%q", lib.Kind, lib.Metadata.Name)
	}
	entry, ok := lib.Spec.Artifacts.Get("WidgetCRD")
	if !ok {
		t.Fatalf("bundle WidgetCRD not found; keys=%v", lib.Spec.Artifacts.Keys())
	}
	wantOrder := []string{"widget-crd.yaml", "widget-rbac.yaml", "widget-config.yaml"}
	if got := entry.Files.Keys(); len(got) != len(wantOrder) {
		t.Fatalf("file count: got %v want %v", got, wantOrder)
	}
	for i, name := range entry.Files.Keys() {
		if name != wantOrder[i] {
			t.Errorf("file order[%d]: got %q want %q", i, name, wantOrder[i])
		}
	}
	crd, _ := entry.Files.Get("widget-crd.yaml")
	if want := "apiVersion: apiextensions.k8s.io/v1\n"; len(crd) == 0 || crd[:len(want)] != want {
		t.Errorf("verbatim body not captured for widget-crd.yaml; got %q", crd)
	}
	cfg, _ := entry.Files.Get("widget-config.yaml")
	if cfg != "kind: ConfigMap" {
		t.Errorf("single-line body: got %q", cfg)
	}
}

// TestArtifactLibrary_RejectsNonMappingEntry asserts a bundle entry that is a
// scalar (not a filename→body mapping) fails the Go-side typed unmarshal with the
// shape sentinel.
func TestArtifactLibrary_RejectsNonMappingEntry(t *testing.T) {
	t.Parallel()

	const src = `apiVersion: aggrega.ai/v1beta1
kind: ArtifactLibrary
metadata:
  name: Bad
  version: 1.0.0
spec:
  artifacts:
    Bundle: "not a mapping"
`
	var lib types.ArtifactLibrary
	err := yaml.Unmarshal([]byte(src), &lib)
	if !errors.Is(err, types.ErrArtifactLibraryEntryShape()) {
		t.Fatalf("expected ErrArtifactLibraryEntryShape, got %v", err)
	}
}

// TestArtifactLibrary_RoundTrip asserts a loaded bundle re-marshals with
// multi-line file bodies in literal block style so newlines survive round-trip.
func TestArtifactLibrary_RoundTrip(t *testing.T) {
	t.Parallel()

	var lib types.ArtifactLibrary
	if err := yaml.Unmarshal([]byte(artifactLibraryYAML), &lib); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&lib)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round types.ArtifactLibrary
	if err := yaml.Unmarshal(out, &round); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	orig, _ := lib.Spec.Artifacts.Get("WidgetCRD")
	got, _ := round.Spec.Artifacts.Get("WidgetCRD")
	for _, name := range orig.Files.Keys() {
		ob, _ := orig.Files.Get(name)
		gb, _ := got.Files.Get(name)
		if ob != gb {
			t.Errorf("round-trip body drift for %q:\n orig=%q\n got =%q", name, ob, gb)
		}
	}
}
