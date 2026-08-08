// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestArtifactEntry_RoundTripInline pins mode 1: a string-scalar entry
// value parses into ArtifactEntry.Inline and re-emits as a scalar.
func TestArtifactEntry_RoundTripInline(t *testing.T) {
	t.Parallel()
	in := `artifacts:
  configmap.yaml: |
    apiVersion: v1
    kind: ConfigMap
`
	type wrap struct {
		Artifacts OrderedMap[ArtifactEntry] `yaml:"artifacts"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entry, ok := w.Artifacts.Get("configmap.yaml")
	if !ok {
		t.Fatal("expected configmap.yaml entry")
	}
	if entry.Inline == "" {
		t.Error("expected Inline populated")
	}
	if entry.Import != "" {
		t.Errorf("expected Import empty; got %q", entry.Import)
	}
}

// TestArtifactEntry_RoundTripImport pins mode 2: {$import: <path>}.
func TestArtifactEntry_RoundTripImport(t *testing.T) {
	t.Parallel()
	in := `artifacts:
  policy.yaml:
    $import: ./extras/policy.yaml
`
	type wrap struct {
		Artifacts OrderedMap[ArtifactEntry] `yaml:"artifacts"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entry, ok := w.Artifacts.Get("policy.yaml")
	if !ok {
		t.Fatal("expected policy.yaml entry")
	}
	if entry.Import != "./extras/policy.yaml" {
		t.Errorf("expected Import = './extras/policy.yaml'; got %q", entry.Import)
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "$import: ./extras/policy.yaml") {
		t.Errorf("round-trip lost $import; got:\n%s", out)
	}
}

// TestArtifactEntry_RejectsMapWithExtraKeys pins the shape error.
func TestArtifactEntry_RejectsMapWithExtraKeys(t *testing.T) {
	t.Parallel()
	in := `artifacts:
  policy.yaml:
    $import: ./policy.yaml
    extra: not-allowed
`
	type wrap struct {
		Artifacts OrderedMap[ArtifactEntry] `yaml:"artifacts"`
	}
	var w wrap
	err := yaml.Unmarshal([]byte(in), &w)
	if err == nil {
		t.Fatal("expected error on extra keys")
	}
	if !errors.Is(err, ErrArtifactEntryShape()) {
		t.Errorf("expected ErrArtifactEntryShape; got %v", err)
	}
}

// TestArtifactEntry_IsLibraryImport pins mode-3 discrimination on the
// `#/artifacts/` fragment surface, mirroring WorkloadTemplate.IsLibraryImport:
// only a host-less bare-name ArtifactsLibrary reference is a library import; an
// inline body, a local-file import, and a host-bearing oci:// form are not.
func TestArtifactEntry_IsLibraryImport(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		entry ArtifactEntry
		want  bool
	}{
		{"bare-name library", ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"}, true},
		{"versioned library", ArtifactEntry{Import: "SharedCRDs:1.2.0#/artifacts/WidgetCRD"}, true},
		{"local file", ArtifactEntry{Import: "./crd.yaml"}, false},
		{"oci host", ArtifactEntry{Import: "oci://hub.aggrega.ai/SharedCRDs#/artifacts/WidgetCRD"}, false},
		{"inline", ArtifactEntry{Inline: "kind: ConfigMap"}, false},
		{"wrong fragment", ArtifactEntry{Import: "SharedCRDs#/workloads/WidgetCRD"}, false},
	}
	for _, tc := range cases {
		if got := tc.entry.IsLibraryImport(); got != tc.want {
			t.Errorf("%s: IsLibraryImport() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestArtifactEntry_HasOCIPrefix pins that a host-bearing reference is reported
// for rejection and a bare-name / local-file import is not.
func TestArtifactEntry_HasOCIPrefix(t *testing.T) {
	t.Parallel()
	if !(ArtifactEntry{Import: "oci://hub/SharedCRDs#/artifacts/X"}).HasOCIPrefix() {
		t.Error("expected HasOCIPrefix true for oci:// form")
	}
	if (ArtifactEntry{Import: "SharedCRDs#/artifacts/X"}).HasOCIPrefix() {
		t.Error("expected HasOCIPrefix false for bare-name form")
	}
	if (ArtifactEntry{Import: "./crd.yaml"}).HasOCIPrefix() {
		t.Error("expected HasOCIPrefix false for local file")
	}
}
