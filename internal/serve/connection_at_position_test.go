// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// These tests cover the request that reads a position in an authored document
// back to the connection written there: the graph node and the connection, or
// the reason none is reported.
//
// They run over the connection fixture the candidacy and container requests are
// measured over, so the three are held to one set of documents. The copy's
// Release is replaced by one that also writes a per-edge entry for a connection
// the Aggregate drew, which is the one Release entry the committed fixture does
// not carry and the one an operator writes to attach to such a connection. The
// tool protocol's tests read the same file.

// positionReleaseFixture is the fixture's Release with a per-edge entry added for
// the Aggregate-drawn connection models.gpt, carrying an attachment of its own.
const positionReleaseFixture = "testdata/connection-site-release-with-edge-entry.yaml"

// positionWorkspace is the connection fixture with positionReleaseFixture in
// place of its Release.
func positionWorkspace(t *testing.T) (ws, release string) {
	t.Helper()
	ws, release = connectionSiteWorkspace(t)
	b, err := os.ReadFile(positionReleaseFixture)
	if err != nil {
		t.Fatalf("read the Release fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, release), b, 0o644); err != nil {
		t.Fatalf("write the Release: %v", err)
	}
	return ws, release
}

// positionReasonsOverlay is laid over the connection fixture for the Release
// entries a build accepts and the graph answers no connection for. Its Aggregate
// switches one component off and has an attachment's dependency block draw two
// connections through a socket the Component declares too; its Feature declares
// that socket; its Release writes an entry for each of those connections and one
// for a connection nothing draws. The tool protocol's tests read the same files.
const positionReasonsOverlay = "testdata/connection-site-reasons"

// positionReasonsWorkspace is the connection fixture with positionReasonsOverlay
// laid over it.
func positionReasonsWorkspace(t *testing.T) (ws, release string) {
	t.Helper()
	ws, release = connectionSiteWorkspace(t)
	copyConnectionSiteFixture(t, positionReasonsOverlay, ws)
	return ws, release
}

// positionOf locates text on the one line of content whose trimmed text starts
// with linePrefix, and returns the position offset characters past where text
// starts on that line. A prefix matching no line or more than one is a broken
// case rather than a position, so it fails.
func positionOf(t *testing.T, content, linePrefix, text string, offset int) yamlpos.Position {
	t.Helper()
	found := -1
	for i, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), linePrefix) {
			continue
		}
		if found >= 0 {
			t.Fatalf("more than one line starts with %q, so the case names no single place", linePrefix)
		}
		found = i
	}
	if found < 0 {
		t.Fatalf("no line starts with %q", linePrefix)
	}
	line := strings.Split(content, "\n")[found]
	at := strings.Index(line, text)
	if at < 0 {
		t.Fatalf("line %d (%q) does not carry %q", found, line, text)
	}
	return yamlpos.Position{Line: found, Character: at + offset}
}

// connectionAtPositionRequest drives one real aggrega/connectionAtPosition
// request through the server's own message loop and method table. Any
// publication ahead of the answer is passed over: what this request is about is
// its answer.
func connectionAtPositionRequest(t *testing.T, srv *Server, params any) jsonRPCResponse {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/connectionAtPosition", params))
	resp, _ := runOneRequestPastPushes(t, srv, in, &bytes.Buffer{})
	return resp
}

// connectionAtPositionCase is one position and what the request answers for it:
// a connection, or the one reason none is reported.
type connectionAtPositionCase struct {
	name       string
	file       string
	linePrefix string
	text       string
	offset     int
	want       ConnectionAtPositionResult
}

// connectionAtPositionCases enumerates the positions, each answer written out in
// full rather than derived, because a derived expectation would be a second
// implementation of the thing under test.
func connectionAtPositionCases() []connectionAtPositionCase {
	gptInAggregate := ConnectionAtPositionResult{GraphNode: "gateway",
		Connection: &ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "aggregate"}}
	gptInRelease := ConnectionAtPositionResult{GraphNode: "gateway",
		Connection: &ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "release"}}
	outInRelease := ConnectionAtPositionResult{GraphNode: "gateway",
		Connection: &ConnectionCoordinate{Socket: "logs", Alias: "out", Document: "release"}}
	none := ConnectionAtPositionResult{NoConnectionReason: NoConnectionReasonNoEntry}

	return []connectionAtPositionCase{
		{name: "the alias key of a connection a component draws", file: "aggregate.yaml",
			linePrefix: "gpt:", text: "gpt", want: gptInAggregate},
		{name: "inside that entry's attachments", file: "aggregate.yaml",
			linePrefix: "key: gpt-key", text: "gpt-key", offset: 2, want: gptInAggregate},
		{name: "the end of that entry's last line", file: "aggregate.yaml",
			linePrefix: "key: gpt-key", text: "gpt-key", offset: len("gpt-key"), want: gptInAggregate},
		{name: "a connection through a socket the Component does not declare", file: "aggregate.yaml",
			linePrefix: "side:", text: "side", want: ConnectionAtPositionResult{GraphNode: "gateway",
				Connection: &ConnectionCoordinate{Socket: "extra", Alias: "side", Document: "aggregate"}}},
		{name: "the socket's own key, above the entries under it", file: "aggregate.yaml",
			linePrefix: "models:", text: "models", want: none},
		{name: "the indentation before an alias key", file: "aggregate.yaml",
			linePrefix: "gpt:", text: "gpt", offset: -4, want: none},
		{name: "a connection a Feature's own block draws", file: "aggregate.yaml",
			linePrefix: "p1:", text: "p1", want: none},
		{name: "a component that draws nothing", file: "aggregate.yaml",
			linePrefix: "model:", text: "model", want: none},
		{name: "the alias key of a connection the Release draws", file: "release.yaml",
			linePrefix: "out:", text: "out", want: outInRelease},
		{name: "that connection's target", file: "release.yaml",
			linePrefix: "target: sink", text: "sink", want: outInRelease},
		{name: "the Release's own entry for a connection the Aggregate drew", file: "release.yaml",
			linePrefix: "gpt:", text: "gpt", want: gptInRelease},
		{name: "inside the Release's attachments on that entry", file: "release.yaml",
			linePrefix: "type: feature.conn/audit", text: "audit", want: gptInRelease},
		{name: "a component entry that writes no connection", file: "release.yaml",
			linePrefix: "model: {}", text: "model", want: none},
	}
}

// TestConnectionAtPositionAnswersForAnEntryInEachDocument drives the registered
// wire method once per position and pins the whole answer, in both documents —
// and, on the bytes, that exactly one half of it is sent: the node and the
// connection with no reason, or the reason alone.
func TestConnectionAtPositionAnswersForAnEntryInEachDocument(t *testing.T) {
	ws, release := positionWorkspace(t)
	for _, tc := range connectionAtPositionCases() {
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(ws, tc.file))
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			pos := positionOf(t, string(content), tc.linePrefix, tc.text, tc.offset)
			srv := connectionSiteServer(t, ws)
			resp := connectionAtPositionRequest(t, srv, connectionAtPositionParams{
				File: tc.file, Position: &pos, Release: release,
			})
			if resp.Error != nil {
				t.Fatalf("the request was refused: %+v", resp.Error)
			}
			want, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatalf("marshal the expected answer: %v", err)
			}
			if string(resp.Result) != string(want) {
				t.Fatalf("answer at line %d, character %d:\n  got:  %s\n  want: %s", pos.Line, pos.Character,
					resp.Result, want)
			}
			wantKeys := "graphNode,connection"
			if tc.want.Connection == nil {
				wantKeys = "noConnectionReason"
			}
			if keys := strings.Join(topLevelKeys(t, resp.Result), ","); keys != wantKeys {
				t.Errorf("the answer carries keys %s, want exactly %s", keys, wantKeys)
			}
		})
	}
}

// TestTheConnectionAtAPositionIsTheOneTheContainerRequestPlacesAnAttachmentOn is
// the round trip that makes the answer worth handing on: for every position
// answered with a connection, the container the attachment-container request
// then names for that connection, in that document, is inside the very entry the
// position fell in.
func TestTheConnectionAtAPositionIsTheOneTheContainerRequestPlacesAnAttachmentOn(t *testing.T) {
	ws, release := positionWorkspace(t)
	answered := 0
	for _, tc := range connectionAtPositionCases() {
		if tc.want.Connection == nil {
			continue
		}
		answered++
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(ws, tc.file))
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			pos := positionOf(t, string(content), tc.linePrefix, tc.text, tc.offset)
			srv := connectionSiteServer(t, ws)
			resp := connectionAtPositionRequest(t, srv, connectionAtPositionParams{
				File: tc.file, Position: &pos, Release: release,
			})
			if resp.Error != nil {
				t.Fatalf("the request was refused: %+v", resp.Error)
			}
			var got ConnectionAtPositionResult
			if err := json.Unmarshal(resp.Result, &got); err != nil {
				t.Fatalf("decode the answer: %v (%s)", err, resp.Result)
			}
			if got.Connection == nil {
				t.Fatalf("no connection was answered: %s", resp.Result)
			}

			where := decodeAttachmentContainer(t, attachmentContainerRequest(t, connectionSiteServer(t, ws),
				attachmentContainerParams{GraphNode: got.GraphNode, Release: release, Connection: got.Connection}))
			if where.File != tc.file {
				t.Fatalf("the container request places the attachment in %s, not in the document the position "+
					"was read in (%s)", where.File, tc.file)
			}
			var doc yaml.Node
			if err := yaml.Unmarshal(content, &doc); err != nil {
				t.Fatalf("parse %s: %v", tc.file, err)
			}
			entry := where.Container[:len(where.Container)-1]
			span, ok := yamlpos.EntryRange(&doc, yamlpos.BuildLineIndex(content), entry)
			if !ok {
				t.Fatalf("the entry the container sits in (%v) is not in the document", entry)
			}
			if !span.Contains(pos) {
				t.Fatalf("the position (%+v) is not inside the entry %v the container request names (%+v)",
					pos, entry, span)
			}
		})
	}
	if answered == 0 {
		t.Fatal("no case answers a connection, so the round trip measured nothing")
	}
}

// TestTheDaemonsConnectionAtPositionIsTheSharedLayers is the daemon's half of the
// cross-surface proof: what the wire method answers is exactly what the shared
// entry point answers over the same graph, compared byte for byte.
func TestTheDaemonsConnectionAtPositionIsTheSharedLayers(t *testing.T) {
	ws, release := positionWorkspace(t)
	srv := connectionSiteServer(t, ws)
	content, err := os.ReadFile(filepath.Join(ws, "release.yaml"))
	if err != nil {
		t.Fatalf("read the Release: %v", err)
	}
	pos := positionOf(t, string(content), "gpt:", "gpt", 0)

	resp := connectionAtPositionRequest(t, srv, connectionAtPositionParams{File: "release.yaml", Position: &pos,
		Release: release})
	if resp.Error != nil {
		t.Fatalf("the wire method refused: %+v", resp.Error)
	}

	preview, envelope := srv.previewRelease(context.Background(), release,
		pipeline.Options{IncludeTrace: false, SkipRender: true})
	if envelope != nil {
		t.Fatalf("preview the Release: %+v", envelope)
	}
	shared, err := ConnectionAtPosition(srv.workspaceRoot, preview.pipeline.Completed().IRGraph,
		preview.resolved.Release, preview.releasePath, "release.yaml", pos, yamlpos.PositionEncodingUTF16)
	if err != nil {
		t.Fatalf("the shared entry point refused what the method answered: %v", err)
	}
	sharedJSON, err := json.Marshal(shared)
	if err != nil {
		t.Fatalf("marshal the shared answer: %v", err)
	}
	if string(sharedJSON) != string(resp.Result) {
		t.Fatalf("the wire method's answer and the shared layer's differ:\n  method: %s\n  shared: %s",
			resp.Result, sharedJSON)
	}
	if shared.Connection == nil {
		t.Fatal("the comparison is between two answers naming no connection, so it compared nothing")
	}
}

// TestConnectionAtPositionReadsTheBufferTheEditorHolds pins that a session reads
// the position against what the editor is holding rather than what was last
// written to disk. The buffer moves the entry down eight lines, so the line its
// alias sits on in the buffer is, on disk, the next socket's own key: one
// request, at one position, answers the connection with the buffer held and no
// connection with nothing held. Each answer is pinned whole, on the bytes the
// daemon sent for that request alone.
func TestConnectionAtPositionReadsTheBufferTheEditorHolds(t *testing.T) {
	ws, release := positionWorkspace(t)
	abs := filepath.Join(ws, "aggregate.yaml")
	onDisk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read the Aggregate: %v", err)
	}
	buffer := strings.Replace(string(onDisk), "        models:\n",
		"        models:\n"+strings.Repeat("          # an unsaved line\n", 8), 1)
	if buffer == string(onDisk) {
		t.Fatal("the buffer is the document on disk, so the case measures nothing")
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(buffer), &parsed); err != nil {
		t.Fatalf("parse the buffer: %v", err)
	}

	// The disk answer is pinned to no connection because of what this line is on
	// disk, so the case first checks that it still is.
	inBuffer := positionOf(t, buffer, "gpt:", "gpt", 0)
	diskLines := strings.Split(string(onDisk), "\n")
	if inBuffer.Line >= len(diskLines) || !strings.HasPrefix(strings.TrimSpace(diskLines[inBuffer.Line]), "extra:") {
		t.Fatalf("on disk, line %d is not the next socket's own key, so what the disk read answers there is not "+
			"the no-connection answer the control pins", inBuffer.Line)
	}

	answerAt := func(srv *Server) string {
		t.Helper()
		resp := connectionAtPositionRequest(t, srv, connectionAtPositionParams{File: "aggregate.yaml",
			Position: &inBuffer, Release: release})
		if resp.Error != nil {
			t.Fatalf("the request was refused: %+v", resp.Error)
		}
		return string(resp.Result)
	}
	encode := func(r ConnectionAtPositionResult) string {
		t.Helper()
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal the expected answer: %v", err)
		}
		return string(b)
	}
	wantHeld := encode(ConnectionAtPositionResult{GraphNode: "gateway",
		Connection: &ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "aggregate"}})
	wantDisk := encode(ConnectionAtPositionResult{NoConnectionReason: NoConnectionReasonNoEntry})

	held := connectionSiteServer(t, ws)
	held.parseCache.Put(abs, &parsed, []byte(buffer))
	if got := answerAt(held); got != wantHeld {
		t.Fatalf("with the buffer held, line %d, character %d — the alias's key in the buffer — answered:\n"+
			"  got:  %s\n  want: %s", inBuffer.Line, inBuffer.Character, got, wantHeld)
	}

	// The control: the same request on a session holding nothing reads the disk.
	if got := answerAt(connectionSiteServer(t, ws)); got != wantDisk {
		t.Fatalf("with nothing held, line %d, character %d — the next socket's own key on disk — answered:\n"+
			"  got:  %s\n  want: %s", inBuffer.Line, inBuffer.Character, got, wantDisk)
	}
}

// TestConnectionAtPositionRefusesWhatItCannotRead drives each refusal with an
// answerable request first as the positive control.
func TestConnectionAtPositionRefusesWhatItCannotRead(t *testing.T) {
	ws, release := positionWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "broken.yaml"), []byte("spec: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write the unparseable document: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "aggregate.yaml")
	if err := os.WriteFile(outside, []byte("spec: {}\n"), 0o644); err != nil {
		t.Fatalf("write the document outside the workspace: %v", err)
	}
	at := map[string]any{"line": 0, "character": 0}

	for _, tc := range []struct {
		name    string
		params  map[string]any
		code    int
		refuses string
	}{
		{name: "a document the Release's graph reads", params: map[string]any{"file": "aggregate.yaml",
			"position": at, "release": release}},
		{name: "no position", params: map[string]any{"file": "aggregate.yaml", "release": release},
			code: jsonRPCInvalidParams, refuses: "params.position"},
		{name: "no document", params: map[string]any{"position": at, "release": release},
			code: jsonRPCInvalidParams, refuses: errInsertionNoFile.Error()},
		{name: "a document outside the workspace", params: map[string]any{"file": outside, "position": at,
			"release": release}, code: jsonRPCInvalidParams, refuses: errInsertionFileOutsideWorkspace.Error()},
		{name: "a document that is not well-formed", params: map[string]any{"file": "broken.yaml", "position": at,
			"release": release}, code: jsonRPCInvalidParams, refuses: errInsertionFileUnparseable.Error()},
		{name: "no Release", params: map[string]any{"file": "aggregate.yaml", "position": at},
			code: jsonRPCInvalidParams, refuses: "the request named no Release"},
		{name: "a field the request does not take", params: map[string]any{"file": "aggregate.yaml",
			"position": at, "release": release, "graphNode": "gateway"},
			code: jsonRPCInvalidParams, refuses: `does not accept "graphNode"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := connectionAtPositionRequest(t, connectionSiteServer(t, ws), tc.params)
			if tc.refuses == "" {
				if resp.Error != nil {
					t.Fatalf("the positive control was refused: %+v", resp.Error)
				}
				return
			}
			switch {
			case resp.Error == nil:
				t.Fatalf("a request that should have been refused was answered: %s", resp.Result)
			case resp.Error.Code != tc.code:
				t.Fatalf("refused with code %d, want %d: %s", resp.Error.Code, tc.code, resp.Error.Message)
			case !strings.Contains(resp.Error.Message, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q", tc.refuses, resp.Error.Message)
			}
		})
	}
}

// TestConnectionAtPositionAnswersTheReasonsOnAReleaseThatBuilds drives the wire
// method at each Release entry a build accepts that names no connection an
// attachment is written on: an entry for a connection nothing draws, configuring
// nothing; an entry for a connection of a component an active toggle switched
// off; and two entries for connections an attachment's dependency block drew, one
// configuring nothing and one carrying per-edge infra.
//
// The Release is built first, so every answer is one a Release that builds
// reaches, and the graph is read for the fact each reason states, so a fixture
// that stopped reaching its shape fails here rather than passing on another
// shape. Each answer is pinned on the bytes, behind a positive control in the
// same Release, and the shared entry point the tool protocol calls answers the
// same bytes over the same graph.
func TestConnectionAtPositionAnswersTheReasonsOnAReleaseThatBuilds(t *testing.T) {
	ws, release := positionReasonsWorkspace(t)
	srv := connectionSiteServer(t, ws)
	preview, envelope := srv.previewRelease(context.Background(), release,
		pipeline.Options{IncludeTrace: false, SkipRender: true})
	if envelope != nil {
		t.Fatalf("the Release does not build, so no answer below is one a building Release reaches: %+v", envelope)
	}
	graph := preview.pipeline.Completed().IRGraph

	edgesAt := func(node, socket, alias string) []types.IREdge {
		var out []types.IREdge
		for _, e := range graph.Spec.Edges {
			if e.Source == node && e.Socket == socket && e.Alias == alias {
				out = append(out, e)
			}
		}
		return out
	}
	if _, held := graph.Spec.Nodes.Get("standby"); held {
		t.Fatal("the graph holds the component its active toggle switches off")
	}
	for _, c := range [][3]string{{"gateway", "models", "ghost"}, {"standby", "logs", "l1"}} {
		if got := edgesAt(c[0], c[1], c[2]); len(got) != 0 {
			t.Fatalf("the graph carries %s/%s/%s, which the case needs absent: %+v", c[0], c[1], c[2], got)
		}
	}
	for _, alias := range []string{"p8", "p9"} {
		got := edgesAt("gateway", "logs", alias)
		if len(got) != 1 || got[0].OriginFeature == "" {
			t.Fatalf("gateway/logs/%s is not one connection an attachment drew: %+v", alias, got)
		}
	}

	content, err := os.ReadFile(filepath.Join(ws, release))
	if err != nil {
		t.Fatalf("read the Release: %v", err)
	}
	reason := func(r NoConnectionReason) ConnectionAtPositionResult {
		return ConnectionAtPositionResult{NoConnectionReason: r}
	}
	for _, tc := range []struct {
		name       string
		linePrefix string
		text       string
		want       ConnectionAtPositionResult
	}{
		{name: "the positive control: the Release's own entry for a connection the Aggregate drew",
			linePrefix: "gpt:", text: "gpt", want: ConnectionAtPositionResult{GraphNode: "gateway",
				Connection: &ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "release"}}},
		{name: "an entry for a connection nothing draws, configuring nothing", linePrefix: "ghost: {}",
			text: "ghost", want: reason(NoConnectionReasonUnknownConnection)},
		{name: "an entry for a connection of a component switched off", linePrefix: "l1:", text: "l1",
			want: reason(NoConnectionReasonUnknownConnection)},
		{name: "inside the per-edge infra of the switched-off component's entry", linePrefix: "tier: silver",
			text: "silver", want: reason(NoConnectionReasonUnknownConnection)},
		{name: "an entry for a connection an attachment drew, configuring nothing", linePrefix: "p8: {}",
			text: "p8", want: reason(NoConnectionReasonFeatureCreated)},
		{name: "an entry for a connection an attachment drew, carrying per-edge infra", linePrefix: "p9:",
			text: "p9", want: reason(NoConnectionReasonFeatureCreated)},
		{name: "inside the per-edge infra of that entry", linePrefix: "tier: gold", text: "gold",
			want: reason(NoConnectionReasonFeatureCreated)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pos := positionOf(t, string(content), tc.linePrefix, tc.text, 0)
			resp := connectionAtPositionRequest(t, connectionSiteServer(t, ws), connectionAtPositionParams{
				File: release, Position: &pos, Release: release,
			})
			if resp.Error != nil {
				t.Fatalf("the request was refused: %+v", resp.Error)
			}
			want, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatalf("marshal the expected answer: %v", err)
			}
			if string(resp.Result) != string(want) {
				t.Fatalf("answer at line %d, character %d:\n  got:  %s\n  want: %s", pos.Line, pos.Character,
					resp.Result, want)
			}
			shared, err := ConnectionAtPosition(srv.workspaceRoot, graph, preview.resolved.Release,
				preview.releasePath, release, pos, yamlpos.PositionEncodingUTF16)
			if err != nil {
				t.Fatalf("the shared entry point refused what the method answered: %v", err)
			}
			sharedJSON, err := json.Marshal(shared)
			if err != nil {
				t.Fatalf("marshal the shared answer: %v", err)
			}
			if string(sharedJSON) != string(resp.Result) {
				t.Fatalf("the wire method's answer and the shared layer's differ:\n  method: %s\n  shared: %s",
					resp.Result, sharedJSON)
			}
		})
	}

	for _, r := range []NoConnectionReason{NoConnectionReasonNoEntry, NoConnectionReasonFeatureCreated,
		NoConnectionReasonUnknownConnection} {
		if strings.TrimSpace(r.Sentence()) == "" {
			t.Errorf("the %q reason renders no sentence", r)
		}
	}
	if NoConnectionReason("").Sentence() != "" {
		t.Error("an empty reason renders a sentence")
	}
}

// TestConnectionAtPositionAnswersForAnImportedModulesOwnDocument covers what the
// fixture's documents cannot reach through the wire, over a graph built here: an
// entry in the document of a module the Aggregate imports, which the graph
// records its node as authored in, and the same place in the Aggregate, which the
// graph records no such node in.
func TestConnectionAtPositionAnswersForAnImportedModulesOwnDocument(t *testing.T) {
	ws := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const releaseDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: r
spec:
  boundaries:
    cluster:
      components:
        gateway: {}
`
	const moduleDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: edge
spec:
  components:
    web:
      type: conn/gateway
      uses:
        db:
          primary:
            target: store
`
	write("release.yaml", releaseDoc)
	write("modules/edge.yaml", moduleDoc)
	write("aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nspec: {}\n")

	var rel types.Release
	if err := yaml.Unmarshal([]byte(releaseDoc), &rel); err != nil {
		t.Fatalf("decode the Release: %v", err)
	}
	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("gateway", types.IRNode{Component: "conn/gateway", SourceFile: "aggregate.yaml",
		SynthesizedSubsystem: true})
	graph.Spec.Nodes.Set("edge.web", types.IRNode{Component: "conn/gateway", SourceFile: "modules/edge.yaml",
		SynthesizedSubsystem: true})
	graph.Spec.Edges = []types.IREdge{
		{Source: "edge.web", Target: "edge.store", Socket: "db", Alias: "primary"},
	}
	releasePath := filepath.Join(ws, "release.yaml")

	for _, tc := range []struct {
		name       string
		file       string
		content    string
		linePrefix string
		text       string
		want       ConnectionAtPositionResult
	}{
		{name: "an entry in an imported module's own document", file: "modules/edge.yaml", content: moduleDoc,
			linePrefix: "target: store", text: "store",
			want: ConnectionAtPositionResult{GraphNode: "edge.web",
				Connection: &ConnectionCoordinate{Socket: "db", Alias: "primary", Document: "aggregate"}}},
		{name: "the same place in the Aggregate, which the graph records no such node in", file: "aggregate.yaml",
			content: "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nspec: {}\n", linePrefix: "spec:", text: "spec",
			want: ConnectionAtPositionResult{NoConnectionReason: NoConnectionReasonNoEntry}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pos := positionOf(t, tc.content, tc.linePrefix, tc.text, 0)
			got, err := ConnectionAtPosition(ws, graph, &rel, releasePath, tc.file, pos, yamlpos.PositionEncodingUTF16)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("answer:\n  got:  %s\n  want: %s", gotJSON, wantJSON)
			}
		})
	}

	// The module's answer is the coordinate the container request places an
	// attachment by, in the module's own document.
	where, refusal := ConnectionAttachmentContainer(ws, graph, &rel, releasePath, "edge.web",
		ConnectionCoordinate{Socket: "db", Alias: "primary", Document: "aggregate"})
	if refusal != ConnectionAnswered {
		t.Fatalf("the container request refused the module's connection: %s", refusal.Reason())
	}
	if where.File != "modules/edge.yaml" {
		t.Fatalf("the container is placed in %s, not in the module's own document", where.File)
	}
}

// TestTheRequestDeclaresThePositionTheDaemonAccepts is the schema → Go direction
// for the three fields the request takes: the published contract declares each,
// and the shape the daemon decodes the request into declares each, so a client
// typed through the published binding is never refused a field the contract
// offers.
func TestTheRequestDeclaresThePositionTheDaemonAccepts(t *testing.T) {
	t.Parallel()

	const def = "#/$defs/ConnectionAtPositionParams"
	for _, field := range []string{"file", "position", "release"} {
		name := wireOptionalProperty(t, def, field)
		if !declaredParamFields(connectionAtPositionParams{})[name] {
			t.Errorf("the shape the daemon decodes %s into declares no %q, so the field the contract offers is "+
				"refused", def, name)
		}
	}
	position, err := resolveSchemaPointer(wireSchemaDocument(t), def+"/properties/position")
	if err != nil {
		t.Fatalf("%s: %v", def, err)
	}
	if position["$ref"] != "#/$defs/Position" {
		t.Errorf("%s declares position as %v, not by reference to the one position shape", def, position["$ref"])
	}
}
