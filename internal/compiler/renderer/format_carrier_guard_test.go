// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"strings"
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

	// RenderResult: {Files, Warnings, Diagnostics, Contributions}, with Files
	// valued by AssembledFile. Warnings and Diagnostics are advisory streams, not
	// per-file carriers; Contributions carries per-group coordinates and is held
	// to that below. A reintroduced `Formats map[string]string` sidecar keyed
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
	want := map[string]bool{"Files": true, "Warnings": true, "Diagnostics": true, "Contributions": true}
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

	// AssemblyContribution: coordinates only. It is the one exported field of
	// RenderResult that is keyed by the same paths the bytes are, so it is the
	// place a sidecar would most plausibly reappear — as a format string, or as a
	// second copy of the content living beside the coordinates. Neither belongs:
	// it names output paths and lets Files hold what stands at them. The check is
	// structural rather than a name list, so a differently-named carrier of
	// either shape trips it too.
	ac := reflect.TypeOf(AssemblyContribution{})
	for i := 0; i < ac.NumField(); i++ {
		f := ac.Field(i)
		switch f.Type {
		case assembledFile, reflect.SliceOf(assembledFile), reflect.TypeOf([]byte(nil)),
			reflect.MapOf(reflect.TypeOf(""), assembledFile),
			reflect.MapOf(reflect.TypeOf(""), reflect.TypeOf([]byte(nil))),
			reflect.MapOf(reflect.TypeOf(""), reflect.TypeOf("")):
			t.Errorf("AssemblyContribution field %q of type %v carries file content or format beside the "+
				"coordinates; it must name output paths and leave the bytes in RenderResult.Files", f.Name, f.Type)
		}
		if strings.Contains(strings.ToLower(f.Name), "format") {
			t.Errorf("AssemblyContribution field %q reintroduces a format carrier; format belongs on the "+
				"AssembledFile record", f.Name)
		}
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
