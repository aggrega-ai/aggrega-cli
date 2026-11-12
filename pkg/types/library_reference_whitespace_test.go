// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"errors"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestIdentityRefCarriesWhitespace pins the whitespace rule over every shape the
// three `$import:` surfaces admit, plus the two shapes that are deliberately out
// of its reach.
//
// The two rows that matter are the ones whose RAW TEXT differs from their own
// trimmed first line: a reference broken by a line break, and one carrying a
// space inside the library half. Each is a spelling whose leading text is itself
// a resolvable reference, which is what let two readers of one value disagree.
func TestIdentityRefCarriesWhitespace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"line break before the fragment", "LibBeta:1.0.0\n#/workloads/Web", true},
		{"space before the fragment", "LibBeta:1.0.0 #/workloads/Web", true},
		{"tab before the fragment", "LibBeta:1.0.0\t#/mappings/Web", true},
		{"space inside the library name", "Lib Beta#/artifacts/Web", true},
		{"line break after the fragment", "LibBeta#/schemas/Web\ntrailing", true},
		{"leading space", " LibBeta#/mappings/Web", true},
		{"trailing space", "LibBeta#/mappings/Web ", true},

		{"well-formed, no floor", "LibBeta#/workloads/Web", false},
		{"well-formed, with floor", "LibBeta:1.0.0#/workloads/Web", false},
		{"empty", "", false},
		{"in-document anchor", "#/$defs/PortSpec", false},
		{"local file path carrying a space", "./mappings/my values.tmpl", false},
		{"local file path", "./mappings/values.tmpl", false},
		{"host-bearing form, whitespace and all", "oci://hub.example.test/LibBeta:1.0.0 #/workloads/Web", false},
		{"inline body, no fragment", "image: proof-service:v1\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IdentityRefCarriesWhitespace(tc.value); got != tc.want {
				t.Errorf("IdentityRefCarriesWhitespace(%q) = %v; want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestImportTargetCarryingWhitespaceIsRefusedAtTheDocumentRead asserts the
// refusal fires where the author's line is still known, on each of the three
// fields that admit an `$import:`, and that it reports the condition naming the
// whitespace rather than the one naming a wrong shape — the shape here is one of
// the two the field admits, so reporting a shape fault would send the author
// looking at the wrong thing.
func TestImportTargetCarryingWhitespaceIsRefusedAtTheDocumentRead(t *testing.T) {
	t.Parallel()

	type valuesMappingWrap struct {
		Field ValuesMapping `yaml:"field"`
	}
	type workloadWrap struct {
		Field WorkloadTemplate `yaml:"field"`
	}
	type artifactWrap struct {
		Field ArtifactEntry `yaml:"field"`
	}

	cases := []struct {
		name string
		doc  string
		into func() any
	}{
		{
			"valuesMapping, line break",
			"field:\n  $import: \"LibBeta:1.0.0\\n#/mappings/Web\"\n",
			func() any { return &valuesMappingWrap{} },
		},
		{
			"valuesMapping, space",
			"field:\n  $import: \"LibBeta:1.0.0 #/mappings/Web\"\n",
			func() any { return &valuesMappingWrap{} },
		},
		{
			"workload, line break",
			"field:\n  $import: \"LibBeta:1.0.0\\n#/workloads/Web\"\n",
			func() any { return &workloadWrap{} },
		},
		{
			"workload, space",
			"field:\n  $import: \"LibBeta:1.0.0 #/workloads/Web\"\n",
			func() any { return &workloadWrap{} },
		},
		{
			"artifacts entry, line break",
			"field:\n  $import: \"LibBeta:1.0.0\\n#/artifacts/Web\"\n",
			func() any { return &artifactWrap{} },
		},
		{
			"artifacts entry, space",
			"field:\n  $import: \"LibBeta:1.0.0 #/artifacts/Web\"\n",
			func() any { return &artifactWrap{} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := yaml.Unmarshal([]byte(tc.doc), tc.into())
			if err == nil {
				t.Fatalf("a malformed reference was accepted; document:\n%s", tc.doc)
			}
			if !errors.Is(err, ErrLibraryReferenceWhitespace()) {
				t.Errorf("refused under the wrong condition: %v", err)
			}
		})
	}
}

// TestWellFormedImportTargetStillLoads is the positive control beside the
// refusals above: the same three fields accept the reference the refusals are
// one edit away from, and a local-file path carrying a space stays a path.
func TestWellFormedImportTargetStillLoads(t *testing.T) {
	t.Parallel()

	type valuesMappingWrap struct {
		Field ValuesMapping `yaml:"field"`
	}
	var vm valuesMappingWrap
	if err := yaml.Unmarshal([]byte("field:\n  $import: \"LibBeta:1.0.0#/mappings/Web\"\n"), &vm); err != nil {
		t.Fatalf("well-formed library import refused: %v", err)
	}
	if got, want := vm.Field.Import, "LibBeta:1.0.0#/mappings/Web"; got != want {
		t.Errorf("import target = %q; want %q", got, want)
	}

	var path valuesMappingWrap
	if err := yaml.Unmarshal([]byte("field:\n  $import: \"./mappings/my values.tmpl\"\n"), &path); err != nil {
		t.Fatalf("local-file import carrying a space refused: %v", err)
	}
	if got, want := path.Field.Import, "./mappings/my values.tmpl"; got != want {
		t.Errorf("import target = %q; want %q", got, want)
	}
}
