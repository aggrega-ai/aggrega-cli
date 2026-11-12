// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestRegisteredShapeRefusalsNameWhatTheAuthorWrote holds every registered shape
// condition whose detail named a YAML node-kind number to the author's words: the
// line, the shape written and the shape the position takes, under the same
// condition as before. One fault per row.
func TestRegisteredShapeRefusalsNameWhatTheAuthorWrote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		doc       string
		into      func() any
		condition error
		want      string
	}{
		{"parameters written as a list", "- a\n", func() any { return &ParametersBlock{} }, ErrParametersShape(),
			"line 1: a list is written where a map is expected"},
		{"valuesMapping written as a list", "- a\n", func() any { return &ValuesMapping{} }, ErrValuesMappingShape(),
			"line 1: a list is written where text or a map is expected"},
		{"workload written as a list", "- a\n", func() any { return &WorkloadTemplate{} }, ErrWorkloadTemplateShape(),
			"line 1: a list is written where text or a map is expected"},
		{"an artifacts entry written as a list", "- a\n", func() any { return &ArtifactEntry{} }, ErrArtifactEntryShape(),
			"line 1: a list is written where text or a map is expected"},
		{"a mapping library entry written as a list", "- a\n", func() any { return &MappingEntry{} }, ErrMappingEntryNotScalar(),
			"line 1: a list is written where text is expected"},
		{"a workload library entry written as a map", "a: 1\n", func() any { return &WorkloadEntry{} }, ErrWorkloadEntryNotScalar(),
			"line 1: a map is written where text is expected"},
		{"an artifact library entry written as a list", "- a\n", func() any { return &ArtifactLibraryEntry{} }, ErrArtifactLibraryEntryShape(),
			"line 1: a list is written where a map is expected"},
		{"an artifact library file body written as a list", "f.yaml:\n  - a\n", func() any { return &ArtifactLibraryEntry{} }, ErrArtifactLibraryEntryShape(),
			`file "f.yaml": line 2: a list is written where text is expected`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("decoding panicked: %v", r)
					}
				}()
				err = yaml.Unmarshal([]byte(tc.doc), tc.into())
			}()
			if !errors.Is(err, tc.condition) {
				t.Fatalf("error %v is not the condition %v", err, tc.condition)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "got kind") {
				t.Errorf("error %q still names a YAML node-kind number", err)
			}
		})
	}
}
