// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestValuesMapping_RoundTripInlineMode pins mode 1: a string-scalar
// value parses into ValuesMapping.Inline and re-emits as a string
// scalar.
func TestValuesMapping_RoundTripInlineMode(t *testing.T) {
	t.Parallel()
	in := `valuesMapping: |
  image:
    tag: {{ .values.tag }}
`
	type wrap struct {
		ValuesMapping ValuesMapping `yaml:"valuesMapping"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if w.ValuesMapping.Inline == "" {
		t.Fatal("expected Inline populated for mode 1")
	}
	if w.ValuesMapping.Import != "" {
		t.Errorf("expected Import empty for mode 1; got %q", w.ValuesMapping.Import)
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), ".values.tag") {
		t.Errorf("round-trip lost inline content; got:\n%s", out)
	}
}

// TestValuesMapping_RoundTripFileImportMode pins mode 2: a {$import:
// <local-path>} value parses into ValuesMapping.Import (no oci://
// prefix).
func TestValuesMapping_RoundTripFileImportMode(t *testing.T) {
	t.Parallel()
	in := `valuesMapping:
  $import: ./mappings/mariadb.tmpl
`
	type wrap struct {
		ValuesMapping ValuesMapping `yaml:"valuesMapping"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if w.ValuesMapping.Inline != "" {
		t.Errorf("expected Inline empty for mode 2; got %q", w.ValuesMapping.Inline)
	}
	if w.ValuesMapping.Import != "./mappings/mariadb.tmpl" {
		t.Errorf("expected Import = './mappings/mariadb.tmpl'; got %q", w.ValuesMapping.Import)
	}
	if w.ValuesMapping.HasOCIPrefix() {
		t.Error("expected HasOCIPrefix false for mode 2")
	}
	if w.ValuesMapping.IsLibraryImport() {
		t.Error("expected IsLibraryImport false for mode 2 (local file path)")
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "$import: ./mappings/mariadb.tmpl") {
		t.Errorf("round-trip lost $import; got:\n%s", out)
	}
}

// TestValuesMapping_RoundTripLibraryImportMode pins mode 3: a {$import:
// <Library>#/mappings/<entry>} bare-name identity value parses into
// ValuesMapping.Import, is recognized as a library import (no registry host),
// and round-trips unchanged.
func TestValuesMapping_RoundTripLibraryImportMode(t *testing.T) {
	t.Parallel()
	in := `valuesMapping:
  $import: BitnamiHelpers#/mappings/MariaDBBitnamiCore
`
	type wrap struct {
		ValuesMapping ValuesMapping `yaml:"valuesMapping"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !w.ValuesMapping.IsLibraryImport() {
		t.Error("expected IsLibraryImport true for mode 3 (bare-name identity)")
	}
	if w.ValuesMapping.HasOCIPrefix() {
		t.Error("expected HasOCIPrefix false for a host-less identity reference")
	}
	if w.ValuesMapping.Import != "BitnamiHelpers#/mappings/MariaDBBitnamiCore" {
		t.Errorf("expected Import to carry the identity reference; got %q", w.ValuesMapping.Import)
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "$import: BitnamiHelpers#/mappings/MariaDBBitnamiCore") {
		t.Errorf("round-trip lost the identity $import; got:\n%s", out)
	}
}

// TestValuesMapping_HostBearingImportIsNotLibraryImport pins the rejected form:
// a host-bearing oci:// $import is recognized by HasOCIPrefix (for rejection at
// the consumption site) and is NOT classified as a library import — a library is
// addressed by identity, never by registry host.
func TestValuesMapping_HostBearingImportIsNotLibraryImport(t *testing.T) {
	t.Parallel()
	in := `valuesMapping:
  $import: oci://hub.aggrega.ai/aggrega/bitnami-helpers@1.0.0#/mappings/MariaDBBitnamiCore
`
	type wrap struct {
		ValuesMapping ValuesMapping `yaml:"valuesMapping"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !w.ValuesMapping.HasOCIPrefix() {
		t.Error("expected HasOCIPrefix true for the host-bearing form")
	}
	if w.ValuesMapping.IsLibraryImport() {
		t.Error("expected IsLibraryImport false for a host-bearing reference")
	}
}

// TestValuesMapping_RejectsMapWithoutImport pins the error path: a
// mapping value with no $import key produces ErrValuesMappingShape.
func TestValuesMapping_RejectsMapWithoutImport(t *testing.T) {
	t.Parallel()
	in := `valuesMapping:
  somekey: value
`
	type wrap struct {
		ValuesMapping ValuesMapping `yaml:"valuesMapping"`
	}
	var w wrap
	err := yaml.Unmarshal([]byte(in), &w)
	if err == nil {
		t.Fatal("expected error on map-without-$import")
	}
	if !errors.Is(err, ErrValuesMappingShape()) {
		t.Errorf("expected ErrValuesMappingShape in chain; got %v", err)
	}
}

// TestValuesMapping_RejectsMapWithExtraKeys pins the error path: a
// mapping with $import plus other keys is rejected.
func TestValuesMapping_RejectsMapWithExtraKeys(t *testing.T) {
	t.Parallel()
	in := `valuesMapping:
  $import: ./mappings/x.tmpl
  extra: not-allowed
`
	type wrap struct {
		ValuesMapping ValuesMapping `yaml:"valuesMapping"`
	}
	var w wrap
	err := yaml.Unmarshal([]byte(in), &w)
	if err == nil {
		t.Fatal("expected error on map-with-extra-keys")
	}
	if !errors.Is(err, ErrValuesMappingShape()) {
		t.Errorf("expected ErrValuesMappingShape in chain; got %v", err)
	}
}

// TestValuesMapping_EmptyOmitemptyElides pins yaml.v3's omitempty
// behavior for the discriminator type: an empty ValuesMapping
// (both Inline and Import empty) on a field tagged omitempty does
// not emit the key.
func TestValuesMapping_EmptyOmitemptyElides(t *testing.T) {
	t.Parallel()
	type wrap struct {
		Name          string        `yaml:"name"`
		ValuesMapping ValuesMapping `yaml:"valuesMapping,omitempty"`
	}
	w := wrap{Name: "no-mapping"}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "valuesMapping") {
		t.Errorf("expected valuesMapping elided; got:\n%s", out)
	}
}

// TestValuesMapping_IsEmpty pins the consumer helper used at validator
// + renderer dispatch sites.
func TestValuesMapping_IsEmpty(t *testing.T) {
	t.Parallel()
	var zero ValuesMapping
	if !zero.IsEmpty() {
		t.Error("zero-value ValuesMapping should be empty")
	}
	inline := ValuesMapping{Inline: "x"}
	if inline.IsEmpty() {
		t.Error("inline-populated ValuesMapping should not be empty")
	}
	imp := ValuesMapping{Import: "x"}
	if imp.IsEmpty() {
		t.Error("import-populated ValuesMapping should not be empty")
	}
}
