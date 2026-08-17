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

	// RenderResult: {Files, Warnings, Diagnostics, Contributions, DescribedView,
	// HydratedView, RenderedView}, with Files valued by AssembledFile. Warnings and
	// Diagnostics are advisory streams, not per-file carriers; Contributions
	// carries per-group coordinates and is held to that below; the three views are
	// stage presentations. A reintroduced `Formats map[string]string` sidecar keyed
	// like the bytes shows up as an unexpected exported field and trips the set
	// check.
	//
	// DescribedView and HydratedView are keyed by node, boundary, pipeline step,
	// provides key and artifact name rather than by output path, so neither is a
	// carrier of anything a file is keyed by. HydratedView's artifact axis is a
	// close call and passes: an artifact is a Recipe's authored entry, which is a
	// coordinate of the node that declares it and not of the assembled output path
	// the bytes end up under.
	//
	// RenderedView is the closest call of the three, because one of its facts IS
	// keyed by output path and answers with bytes — and it still passes, for a
	// reason worth stating rather than leaving to be re-derived. It holds no
	// structure keyed by path. It answers that fact by delegating to Files and
	// Contributions, so the bytes have exactly one home and the format travels on
	// the record that holds them. Its own retention is keyed by node and artifact
	// name and carries no format at all. What would trip this guard is a view that
	// grew a path-keyed field of its own — which is why the check is over the field
	// set rather than over what any accessor returns.
	rr := reflect.TypeOf(RenderResult{})
	exported := map[string]reflect.Type{}
	for i := 0; i < rr.NumField(); i++ {
		f := rr.Field(i)
		if f.PkgPath == "" {
			exported[f.Name] = f.Type
		}
	}
	want := map[string]bool{
		"Files": true, "Warnings": true, "Diagnostics": true,
		"Contributions": true, "DescribedView": true, "HydratedView": true,
		"RenderedView": true,
	}
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
