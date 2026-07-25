// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"testing"
)

// TestSingleFormatCarrier locks the single per-file format carrier in place. A
// file's producer-declared render format lives ON its content record
// (AssembledFile.Format), never in a parallel path→format sidecar map keyed the
// same way its bytes are. Parallel maps re-create the drift this shape removed:
// two structures keyed identically that agree today and diverge on the next
// edge case. The reflection assertions fail the moment a sidecar format map is
// reintroduced on either the pipeline-facing RenderResult or the internal
// assembledGroup, so the collapse cannot silently regrow a fourth copy.
func TestSingleFormatCarrier(t *testing.T) {
	t.Parallel()

	// RenderResult: {Files, Warnings, UnsupportedFieldWarnings}, with Files valued by
	// AssembledFile. Warnings and UnsupportedFieldWarnings are advisory streams, not
	// per-file carriers; a reintroduced `Formats map[string]string` sidecar keyed
	// like the bytes shows up as an unexpected exported field and trips the set
	// check.
	rr := reflect.TypeOf(RenderResult{})
	exported := map[string]reflect.Type{}
	for i := 0; i < rr.NumField(); i++ {
		f := rr.Field(i)
		if f.PkgPath == "" {
			exported[f.Name] = f.Type
		}
	}
	want := map[string]bool{"Files": true, "Warnings": true, "UnsupportedFieldWarnings": true}
	for name := range exported {
		if !want[name] {
			t.Errorf("RenderResult has unexpected exported field %q; a per-file format sidecar map must not reappear", name)
		}
	}
	for name := range want {
		if _, ok := exported[name]; !ok {
			t.Errorf("RenderResult is missing expected exported field %q", name)
		}
	}
	assembledFile := reflect.TypeOf(AssembledFile{})
	if got := exported["Files"]; got != reflect.MapOf(reflect.TypeOf(""), assembledFile) {
		t.Errorf("RenderResult.Files must be map[string]AssembledFile (content and format on one record), got %v", got)
	}

	// assembledGroup: no `formats` field, and files valued by AssembledFile.
	ag := reflect.TypeOf(assembledGroup{})
	if _, ok := ag.FieldByName("formats"); ok {
		t.Error("assembledGroup reintroduced a `formats` sidecar map; format belongs on the AssembledFile record in files")
	}
	filesField, ok := ag.FieldByName("files")
	if !ok {
		t.Fatal("assembledGroup has no files field")
	}
	if filesField.Type != reflect.MapOf(reflect.TypeOf(""), assembledFile) {
		t.Errorf("assembledGroup.files must be map[string]AssembledFile, got %v", filesField.Type)
	}
}
