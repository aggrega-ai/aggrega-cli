// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// A refusal raised while rendering, as the daemon reports it: in its error answer
// and as an entry on the build layer, filed on the document the refusal points at.
//
// Every expectation below is derived from the fixture or from the condition's own
// registration — the token the fixture authors, the line its key sits on, the
// identifiers the condition is registered under — and never from the code under
// test, so a defect planted on both sides of a comparison cannot satisfy one.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// refusalAnswer is one response decoded with the structured part of its error,
// which the build tests elsewhere in this package do not read.
type refusalAnswer struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		Data    *errorData `json:"data"`
	} `json:"error"`
}

// driveRefusalRequest runs ONE request to completion on srv and returns its answer
// together with every build-layer publication the request pushed, in the order it
// pushed them.
func driveRefusalRequest(t *testing.T, srv *Server, method string, params any) (refusalAnswer, []publishDiagnosticsParams) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, method, params))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)

	var answer refusalAnswer
	answered := false
	var pushed []publishDiagnosticsParams
	r := bufio.NewReader(bytes.NewReader(out.Bytes()))
	for {
		frame, rerr := readMessage(r)
		if rerr != nil {
			break
		}
		var probe struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(frame, &probe) != nil {
			continue
		}
		if probe.ID == nil {
			if probe.Method == "aggrega/publishDiagnostics" {
				var p publishDiagnosticsParams
				if err := json.Unmarshal(probe.Params, &p); err != nil {
					t.Fatalf("decode a publication: %v (%s)", err, probe.Params)
				}
				pushed = append(pushed, p)
			}
			continue
		}
		if err := json.Unmarshal(frame, &answer); err != nil {
			t.Fatalf("decode the answer: %v (%s)", err, frame)
		}
		answered = true
	}
	if !answered {
		t.Fatalf("%s wrote no answer; the wire carried:\n%s", method, out.String())
	}
	return answer, pushed
}

// lastPublicationFor returns the entries the last publication for file carried,
// and whether any publication named file.
func lastPublicationFor(pushed []publishDiagnosticsParams, file string) ([]Diagnostic, bool) {
	var found []Diagnostic
	ok := false
	for _, p := range pushed {
		if filepath.Clean(p.File) == filepath.Clean(file) {
			found, ok = p.Diagnostics, true
		}
	}
	return found, ok
}

// keyPosition returns the one-based line and column the first line of content
// whose text, indentation aside, begins with key sits at. It reads the fixture,
// so the position a test expects is where the author wrote the key.
func keyPosition(t *testing.T, content, key string) (line, column int) {
	t.Helper()
	for i, text := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(text, " ")
		if strings.HasPrefix(trimmed, key) {
			return i + 1, len(text) - len(trimmed) + 1
		}
	}
	t.Fatalf("the fixture carries no line beginning with %q:\n%s", key, content)
	return 0, 0
}

// rewriteFixture replaces anchor with replacement in the file at path, failing the
// test when the anchor is gone, so a fixture that moves cannot leave the fault
// silently unwritten and the row passing over a workspace that renders.
func rewriteFixture(t *testing.T, path, anchor, replacement string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(body), anchor) {
		t.Fatalf("%s no longer carries %q; the fault would not be written", path, anchor)
	}
	rewritten := strings.Replace(string(body), anchor, replacement, 1)
	if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return rewritten
}

// renderRefusalFault is one way the fixture's render is made to refuse, and where
// the refusal must then point.
type renderRefusalFault struct {
	name string
	// write puts the fault into the workspace at ws and returns the document the
	// refusal must point at, the key it must point at there ("" for none), and the
	// token the fixture authored that the refusal's text must carry.
	write func(t *testing.T, ws string) (document, key, token string)
	// condition is the registered condition the refusal must name, or nil for the
	// control.
	condition error
}

// renderRefusalFaults is the table every test in this file reads, control first.
// Each fault is one change to the fixture and nothing else.
func renderRefusalFaults() []renderRefusalFault {
	recipe := func(ws string) string { return filepath.Join(ws, "catalog", "recipes", "server.yaml") }
	return []renderRefusalFault{
		{
			name: "a Release that renders",
			write: func(t *testing.T, ws string) (string, string, string) {
				return "", "", ""
			},
		},
		{
			name: "a Workload naming a slot its Component does not declare",
			write: func(t *testing.T, ws string) (string, string, string) {
				rewriteFixture(t, recipe(ws), "        image: demo-server:latest\n",
					"        image: demo-server:latest\n"+
						"        attachments:\n"+
						"          ADMIN_TOKEN:\n"+
						"            from: admin_keyy\n"+
						"            as: env\n")
				return recipe(ws), ".spec.workload", "admin_keyy"
			},
			condition: validator.ErrWorkloadFeaturesSchema(),
		},
		{
			name: "a Recipe assertion that fails",
			write: func(t *testing.T, ws string) (string, string, string) {
				rewriteFixture(t, recipe(ws), "  provides:\n",
					"  assertions:\n"+
						"    port-is-declared: '{{ fail \"the port of this server must be declared\" }}'\n"+
						"  provides:\n")
				return recipe(ws), ".spec.assertions.port-is-declared", "the port of this server must be declared"
			},
			condition: renderer.ErrAssertionFailed(),
		},
		{
			// A Component's assertion is raised before the render, by a stage that
			// holds no file for the Component, so its refusal names no document and
			// is filed on the Aggregate, at the head of the file.
			name: "a Component assertion that fails",
			write: func(t *testing.T, ws string) (string, string, string) {
				rewriteFixture(t, filepath.Join(ws, "catalog", "components", "svc.yaml"), "spec:\n",
					"spec:\n"+
						"  assertions:\n"+
						"    host-is-declared: '{{ fail \"the host of this server must be declared\" }}'\n")
				return filepath.Join(ws, "app-agg.yaml"), "", "the host of this server must be declared"
			},
			condition: renderer.ErrAssertionFailed(),
		},
	}
}

// assertRefusalReported holds one answer and its publications to the fault that
// produced them: the answer carries the refusal with its causes and where it
// points, and the build layer carries the same refusal as one entry on that
// document, at that key.
func assertRefusalReported(t *testing.T, fault renderRefusalFault, answer refusalAnswer,
	pushed []publishDiagnosticsParams, document, key, token string) {
	t.Helper()
	if fault.condition == nil {
		if answer.Error != nil {
			t.Fatalf("the control refused: code %d: %s", answer.Error.Code, answer.Error.Message)
		}
		for _, p := range pushed {
			for _, d := range p.Diagnostics {
				if d.Severity == editorSeverityError {
					t.Errorf("a Release that renders published an error entry on %s: %+v", p.File, d)
				}
			}
		}
		return
	}
	if answer.Error == nil {
		t.Fatalf("the fault did not refuse, so nothing below is about a refusal; result: %s", answer.Result)
	}

	sentence, _ := cerrors.MessageOf(fault.condition)
	code, _ := cerrors.CodeOf(fault.condition)
	shortCode, _ := cerrors.ShortCodeOf(fault.condition)
	content, err := os.ReadFile(document)
	if err != nil {
		t.Fatalf("read the document the refusal must point at: %v", err)
	}
	wantLine, wantColumn := 1, 1
	if key != "" {
		segments := strings.Split(key, ".")
		wantLine, wantColumn = keyPosition(t, string(content), segments[len(segments)-1]+":")
	}

	// The answer.
	if answer.Error.Code != jsonRPCInternalError {
		t.Errorf("the answer carries code %d; a refusal from inside the pipeline answers %d",
			answer.Error.Code, jsonRPCInternalError)
	}
	for _, want := range []string{token, sentence} {
		if !strings.Contains(answer.Error.Message, want) {
			t.Errorf("the answer's message drops %q — it carries the outer frame and not the refusal "+
				"beneath it:\n%s", want, answer.Error.Message)
		}
	}
	data := answer.Error.Data
	switch {
	case data == nil:
		t.Errorf("the answer names no document; the refusal points at %s", document)
	case filepath.Clean(data.File) != filepath.Clean(document):
		t.Errorf("the answer names %s; the refusal points at %s", data.File, document)
	case key == "" && (data.Line != 0 || data.Column != 0):
		t.Errorf("the answer carries %d:%d for a refusal that names no key; a file-level refusal carries "+
			"no position rather than the head of the file standing in for one", data.Line, data.Column)
	case key != "" && (data.Line != wantLine || data.Column != wantColumn):
		t.Errorf("the answer places the refusal at %d:%d; the key %s is at %d:%d",
			data.Line, data.Column, key, wantLine, wantColumn)
	}

	// The build layer.
	entries, published := lastPublicationFor(pushed, document)
	if !published {
		t.Fatalf("nothing was published on %s, the document the refusal points at; published on: %v",
			document, publishedFiles(pushed))
	}
	var refusals []Diagnostic
	for _, d := range entries {
		if d.Severity == editorSeverityError {
			refusals = append(refusals, d)
		}
	}
	if len(refusals) != 1 {
		t.Fatalf("the layer carries %d error entries on %s; want the one refusal: %+v",
			len(refusals), document, entries)
	}
	got := refusals[0]
	if got.Source != DiagnosticSourceBuild {
		t.Errorf("the entry's source is %q; want the build layer's %q", got.Source, DiagnosticSourceBuild)
	}
	if got.Line != wantLine || got.Column != wantColumn {
		t.Errorf("the entry is at %d:%d; want %d:%d", got.Line, got.Column, wantLine, wantColumn)
	}
	if got.KeyPath != key {
		t.Errorf("the entry names key %q; want %q", got.KeyPath, key)
	}
	if got.Code != code || got.ShortCode != shortCode || got.CodeDescription == nil {
		t.Errorf("the entry names code %q, short code %q, pointer %v; the condition is %q, %q, with a pointer",
			got.Code, got.ShortCode, got.CodeDescription, code, shortCode)
	}
	if got.Message != answer.Error.Message {
		t.Errorf("the entry and the answer word one refusal differently:\nentry:  %s\nanswer: %s",
			got.Message, answer.Error.Message)
	}
}

// publishedFiles names the files a request published on, for a failure message.
func publishedFiles(pushed []publishDiagnosticsParams) []string {
	out := make([]string, 0, len(pushed))
	for _, p := range pushed {
		out = append(out, p.File)
	}
	return out
}

// TestARefusalRaisedWhileRenderingIsAnsweredAndPublishedWhereItPoints drives the
// graph request with the render on — the request an editor sends with
// build_on_inspect set — over one fixture per fault, and holds its answer and its
// publication to where each refusal points.
func TestARefusalRaisedWhileRenderingIsAnsweredAndPublishedWhereItPoints(t *testing.T) {
	for _, fault := range renderRefusalFaults() {
		fault := fault
		t.Run(fault.name, func(t *testing.T) {
			ws, releaseRel := renderDialWorkspace(t)
			document, key, token := fault.write(t, ws)
			var in, out bytes.Buffer
			srv := newPinIsolationServer(t, ws, &in, &out, 1)
			srv.buildOnInspect = true

			answer, pushed := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
			assertRefusalReported(t, fault, answer, pushed, document, key, token)
		})
	}
}

// TestARefusalRaisedWhileBuildingIsAnsweredAndPublishedWhereItPoints drives the two
// build requests over the same faults. A build that fills its lockfile first renders
// against a lockfile derived from its own closure, so its render is reached; the
// report of what a build would write is then sent over the lockfile that fill wrote.
func TestARefusalRaisedWhileBuildingIsAnsweredAndPublishedWhereItPoints(t *testing.T) {
	for _, fault := range renderRefusalFaults() {
		fault := fault
		t.Run(fault.name, func(t *testing.T) {
			ws, releaseRel := autoPullWorkspace(t)
			document, key, token := fault.write(t, ws)

			answer, pushed := driveRefusalRequest(t, newFillCapableServer(t, ws), "aggrega/build",
				buildParams{Release: releaseRel, AutoPull: true})
			assertRefusalReported(t, fault, answer, pushed, document, key, token)

			answer, pushed = driveRefusalRequest(t, newFillCapableServer(t, ws), "aggrega/buildPreview",
				buildPreviewParams{Release: releaseRel})
			assertRefusalReported(t, fault, answer, pushed, document, key, token)
		})
	}
}

// TestTheNextRenderClearsARefusalItNoLongerRaises proves the entry is part of the
// layer the next run replaces, and not a publication of its own that nothing
// withdraws: the refusal is corrected on disk, the session is told the workspace
// changed, and the next request that renders publishes the refusal's document with
// no entry on it.
func TestTheNextRenderClearsARefusalItNoLongerRaises(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)
	fault := renderRefusalFaults()[1]
	document, _, _ := fault.write(t, ws)
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	srv.buildOnInspect = true

	answer, _ := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
	if answer.Error == nil {
		t.Fatal("precondition: the fault did not refuse, so there is no entry to clear")
	}
	if got := srv.snapshotDiagnostics(document); len(got) == 0 {
		t.Fatalf("precondition: the refusal left no entry on %s, so clearing it proves nothing", document)
	}

	rewriteFixture(t, document,
		"        attachments:\n          ADMIN_TOKEN:\n            from: admin_keyy\n            as: env\n", "")
	srv.handleInvalidate(invalidateParams{})

	answer, pushed := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
	if answer.Error != nil {
		t.Fatalf("the corrected workspace refused: %s", answer.Error.Message)
	}
	if got := srv.snapshotDiagnostics(document); len(got) != 0 {
		t.Errorf("the corrected render left the refusal standing on %s: %+v", document, got)
	}
	entries, published := lastPublicationFor(pushed, document)
	if !published {
		t.Fatalf("the corrected render did not publish %s, so a client is never told the entry is gone; "+
			"published on: %v", document, publishedFiles(pushed))
	}
	if len(entries) != 0 {
		t.Errorf("the clearing publication on %s still carries entries: %+v", document, entries)
	}
}
