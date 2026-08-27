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
	// HydratedView, EmittedView, DispatchedView, RenderedView}, with Files valued by
	// AssembledFile. Warnings and Diagnostics are advisory streams, not per-file
	// carriers; Contributions carries per-group coordinates and is held to that
	// below; the rest are stage presentations. That last set is described rather
	// than counted, because a count sitting beside the list it counts is the field
	// that goes stale first while everything around it still reads as current. A
	// reintroduced `Formats map[string]string` sidecar keyed like the bytes shows up
	// as an unexpected exported field and trips the set check.
	//
	// DescribedView and HydratedView are keyed by node, boundary, pipeline step,
	// provides key and artifact name rather than by output path, so neither is a
	// carrier of anything a file is keyed by. HydratedView's artifact axis is a
	// close call and passes: an artifact is a Recipe's authored entry, which is a
	// coordinate of the node that declares it and not of the assembled output path
	// the bytes end up under.
	//
	// EmittedView is the closest call of all on the KEY axis, and the argument for
	// it has to be made on both axes rather than on one. Its retention IS keyed by
	// an output name — per node, then per name — which is the nearest any view here
	// comes to being keyed the way the bytes are.
	//
	// On the KEY axis it passes because the name it is keyed by is not a key of
	// Files. Files holds whole output paths: a dispatch prefix, and for a nesting
	// assembler a per-node segment, composed onto the name a node's own render
	// chose. A node's own name is a fragment those paths are built from rather than
	// one of them — and for a node whose provider assembles, the name never reaches
	// Files at all, because the assembler consumes the node's files and emits its
	// own. So the two key spaces are related by composition in one direction and by
	// replacement in the other, and neither makes them the same key.
	//
	// On the CARRIER axis it passes decisively, and that is the half worth stating
	// because it is the half this guard exists for. The retention holds names and
	// the authored input block each name came from, which is a closed vocabulary of
	// block names — no bytes, no format, and nothing derived from either. The
	// emitted bytes are read to build nothing and are not retained, so there is no
	// per-file format for a sidecar to hide in and no second copy of the content
	// beside the names. What would trip this guard is a retention that carried the
	// bytes beside the names it is keyed by, which is exactly the shape this
	// projection refuses.
	//
	// DispatchedView is the next closest call on the KEY axis, and it passes for the
	// same reason RenderedView does. Both its facts are keyed by the evaluated
	// assembly path — a directory several boundaries collapse into — which is a
	// prefix of the paths Files is keyed by rather than the same key: Files holds
	// whole output paths, and no assembly path is one. More to the point it holds
	// no structure of its own at all. It answers by walking Contributions, which is
	// allowlisted above and held to its per-group coordinates below, so there is
	// nothing here keyed like the bytes and nothing carrying a format. What it
	// returns is provider and boundary names, which are neither.
	//
	// RenderedView is the closest call on the ANSWER axis, because one of its facts IS
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
		"EmittedView": true, "DispatchedView": true, "RenderedView": true,
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
