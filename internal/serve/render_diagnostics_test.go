// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

func layeredServer() *Server {
	return &Server{
		diagnostics: map[string][]Diagnostic{},
		renderDiags: map[string][]Diagnostic{},
	}
}

func messages(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Message)
	}
	return out
}

// TestDiagnosticLayers_ComposeAndSurviveEachOther is the load-bearing guard on
// the two-layer split. The semantic layer refreshes on every buffer validation
// and the render layer only when an inspection renders, so a single map would
// make each request erase the other's findings — the render advisory would
// vanish on the next keystroke and the semantic errors would vanish on the next
// inspection. The test rewrites each layer in turn and asserts the other
// survives intact.
func TestDiagnosticLayers_ComposeAndSurviveEachOther(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	const file = "/w/aggregate.yaml"

	s.setDiagnostics(file, []Diagnostic{{Line: 3, Column: 5, Severity: "error", Message: "semantic"}})
	s.setRenderDiagnostics(map[string][]Diagnostic{
		file: {{Line: 1, Column: 1, Severity: "warning", Message: "render"}},
	})

	if got, want := messages(s.snapshotDiagnostics(file)), []string{"semantic", "render"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("composed snapshot = %v, want %v", got, want)
	}

	// A fresh validation rewrites the semantic layer; the render advisory stands.
	s.setDiagnostics(file, []Diagnostic{{Line: 4, Column: 1, Severity: "error", Message: "semantic-2"}})
	if got, want := messages(s.snapshotDiagnostics(file)), []string{"semantic-2", "render"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after semantic rewrite = %v, want %v", got, want)
	}

	// A fresh render rewrites the render layer; the semantic finding stands.
	s.setRenderDiagnostics(map[string][]Diagnostic{
		file: {{Line: 1, Column: 1, Severity: "warning", Message: "render-2"}},
	})
	if got, want := messages(s.snapshotDiagnostics(file)), []string{"semantic-2", "render-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after render rewrite = %v, want %v", got, want)
	}
}

// TestSetRenderDiagnostics_ClearsAndReportsTouchedFiles pins the clearing half.
// A render that raises nothing must retract what the previous render published,
// and the returned file set must name the retracted file so the caller pushes
// the now-empty state rather than leaving a stale advisory in the editor.
func TestSetRenderDiagnostics_ClearsAndReportsTouchedFiles(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	const a = "/w/a.yaml"
	const b = "/w/b.yaml"

	touched := s.setRenderDiagnostics(map[string][]Diagnostic{
		a: {{Line: 1, Column: 1, Severity: "warning", Message: "one"}},
	})
	if !reflect.DeepEqual(touched, []string{a}) {
		t.Fatalf("first publish touched %v, want %v", touched, []string{a})
	}

	touched = s.setRenderDiagnostics(map[string][]Diagnostic{
		b: {{Line: 1, Column: 1, Severity: "warning", Message: "two"}},
	})
	if !reflect.DeepEqual(touched, []string{a, b}) {
		t.Errorf("second publish touched %v, want the union %v", touched, []string{a, b})
	}
	if s.snapshotDiagnostics(a) != nil {
		t.Errorf("file a still carries %v after a render that raised nothing for it", s.snapshotDiagnostics(a))
	}
	if got, want := messages(s.snapshotDiagnostics(b)), []string{"two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("file b = %v, want %v", got, want)
	}

	touched = s.setRenderDiagnostics(nil)
	if !reflect.DeepEqual(touched, []string{b}) {
		t.Errorf("clearing publish touched %v, want %v", touched, []string{b})
	}
	if s.snapshotDiagnostics(b) != nil {
		t.Errorf("file b still carries %v after the layer was cleared", s.snapshotDiagnostics(b))
	}
}

// TestDiagnosticsFiles_WalksBothLayers pins that the startup flush sees a file
// carrying only render advisories. A single-layer walk would skip it and the
// editor would never learn the file has a problem.
func TestDiagnosticsFiles_WalksBothLayers(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	s.setDiagnostics("/w/a.yaml", []Diagnostic{{Message: "semantic"}})
	s.setRenderDiagnostics(map[string][]Diagnostic{
		"/w/a.yaml": {{Message: "render"}},
		"/w/b.yaml": {{Message: "render-only"}},
	})
	if got, want := s.diagnosticsFiles(), []string{"/w/a.yaml", "/w/b.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("diagnosticsFiles = %v, want %v", got, want)
	}
}

// TestDiagnosticSource_BothLayersInOnePayload is the acceptance for the layer
// channel on the wire: one published payload for one file must carry an entry
// from each layer, each naming its own source. One layer carrying the field and
// the other not is the failure mode — a client then cannot tell "no source" from
// "the other layer" and silently mis-buckets — so the assertion is on both
// values in the same marshalled payload, not on either alone.
//
// The values are asserted as literals rather than against the constants: a
// comparison against the constants would pass for whatever they happened to
// hold, and the wire shape is a contract with a client that ships separately.
func TestDiagnosticSource_BothLayersInOnePayload(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	const file = "/w/aggregate.yaml"

	s.setDiagnostics(file, []Diagnostic{{Line: 3, Column: 5, Severity: "error", Message: "semantic"}})
	s.setRenderDiagnostics(map[string][]Diagnostic{
		file: {{Line: 1, Column: 1, Severity: "warning", Message: "render"}},
	})

	composed := s.snapshotDiagnostics(file)
	if len(composed) != 2 {
		t.Fatalf("composed snapshot carries %d entries, want 2 (one per layer)", len(composed))
	}
	raw, err := json.Marshal(publishDiagnosticsParams{File: file, Diagnostics: composed})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	payload := string(raw)
	for _, want := range []string{`"message":"semantic","source":"aggrega"`, `"message":"render","source":"aggrega-build"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload does not carry %s\npayload: %s", want, payload)
		}
	}

	// The layer is authoritative: an entry constructed with some other source is
	// overwritten by the layer that publishes it, so a construction site cannot
	// mislabel which layer a finding came from.
	s.setRenderDiagnostics(map[string][]Diagnostic{
		file: {{Line: 1, Column: 1, Severity: "warning", Message: "render", Source: "something-else"}},
	})
	for _, d := range s.snapshotDiagnostics(file) {
		if d.Message == "render" && d.Source != "aggrega-build" {
			t.Errorf("render-layer source = %q, want aggrega-build (the layer overrides)", d.Source)
		}
	}
}

// TestEditorSeverity_TranslatesTheTwoVocabularies is the guard on the one place a
// severity crosses from the plugin wire to the editor wire. The two vocabularies
// are deliberately separate — the plugin wire reports halt/warn/info, the editor
// wire error/warning/info — and this bridge is the only site that converts one
// into the other, so a copy in place of a translation would show a render
// advisory in the editor as an error with nothing reporting it.
//
// The plugin-wire inputs are the generated consts rather than literals, so a
// future change to the closed set fails here instead of silently falling through
// to the default arm.
func TestEditorSeverity_TranslatesTheTwoVocabularies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		pluginWire string
		wantEditor string
	}{
		{"halt becomes the editor error tier", abi.SeverityHalt, "error"},
		{"warn becomes the editor warning tier", abi.SeverityWarn, "warning"},
		{"info is spelled the same on both wires", abi.SeverityInfo, "info"},
		{"a severity the plugin wire does not declare surfaces visibly", "not-a-severity", "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := editorSeverity(tc.pluginWire); got != tc.wantEditor {
				t.Errorf("editorSeverity(%q) = %q; want %q", tc.pluginWire, got, tc.wantEditor)
			}
		})
	}

	// Every member of the closed plugin-wire set maps to an editor severity the
	// extension's own normalizer recognizes. A new member added to the schema
	// without a case here would land on the default arm and be reported as an
	// error, so the set is walked rather than spot-checked.
	editorTiers := map[string]bool{"error": true, "warning": true, "info": true}
	for _, s := range []string{abi.SeverityHalt, abi.SeverityWarn, abi.SeverityInfo} {
		if !editorTiers[editorSeverity(s)] {
			t.Errorf("plugin-wire severity %q maps to %q, which is not an editor severity tier", s, editorSeverity(s))
		}
	}
}

// TestPublishRenderDiagnostics_TranslatesRatherThanCopies drives the bridge end to
// end: a plugin-wire advisory goes in and the published editor entry must carry the
// editor wire's own spelling, not the plugin wire's. It reads the published layer
// rather than the helper, so a future refactor that bypasses the translation at the
// construction site fails here.
func TestPublishRenderDiagnostics_TranslatesRatherThanCopies(t *testing.T) {
	t.Parallel()
	// The bridge publishes a notification on its way out, so it runs under the
	// production serializer path rather than a bypass. The assertion reads the
	// published layer; the notification bytes are incidental here.
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"

	s.publishRenderDiagnostics(ctx, file, []renderer.Diagnostic{
		{Severity: abi.SeverityWarn, Message: "values.legacyPort is deprecated"},
	})
	flush()

	got := s.snapshotDiagnostics(file)
	if len(got) != 1 {
		t.Fatalf("published %d diagnostics; want 1", len(got))
	}
	if got[0].Severity != "warning" {
		t.Errorf("published severity = %q; want the editor wire's %q (a copy of the plugin wire's %q is the defect)",
			got[0].Severity, "warning", abi.SeverityWarn)
	}
}
