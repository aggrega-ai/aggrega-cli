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

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// insertionWorkspace writes a document into a fresh workspace root and returns
// both, so each case addresses a real file through the real path resolution.
func insertionWorkspace(t *testing.T, rel, content string) (root, abs string) {
	t.Helper()
	root = t.TempDir()
	abs = filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("create the fixture directory: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}
	return root, abs
}

// insertionOverTheWire drives the real aggrega/insertionPoint dispatch and
// returns the decoded result, failing on a refusal.
//
// It goes through Server.dispatch rather than calling the handler, so what the
// case exercises is the route the availability table registers.
func insertionOverTheWire(t *testing.T, s *Server, params insertionPointParams) InsertionPointResult {
	t.Helper()
	body := insertionWireBody(t, s, params)
	var envelope struct {
		Result InsertionPointResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode the response: %v\n%s", err, body)
	}
	if envelope.Error != nil {
		t.Fatalf("aggrega/insertionPoint refused: %d %s", envelope.Error.Code, envelope.Error.Message)
	}
	return envelope.Result
}

// insertionWireBody drives the dispatch and returns the raw response body,
// which is what a refusal case reads.
func insertionWireBody(t *testing.T, s *Server, params insertionPointParams) []byte {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	sink := &requestSink{}
	ctx := withRequestSink(context.Background(), sink)
	req := &jsonRPCRequest{JSONRPC: "2.0", ID: []byte("1"), Method: "aggrega/insertionPoint", Params: raw}
	if derr := s.dispatch(ctx, req); derr != nil {
		t.Fatalf("dispatch: %v", derr)
	}
	return sink.response
}

// applyOverTheWire composes the text the answer calls for and splices it in at
// the reported position, the way a client holding the buffer would.
//
// It works in bytes, converting the reported character offset through an index
// built with the encoding the answer names — which is the whole reason the
// answer names one.
func applyOverTheWire(t *testing.T, src string, result InsertionPointResult, entry []string) string {
	t.Helper()

	var b strings.Builder
	if result.PrecededByLineBreak {
		b.WriteString("\n")
	}
	outermost := result.EntryIndent - len(result.MissingContainerKeys)*result.IndentStep
	for i, key := range result.MissingContainerKeys {
		b.WriteString(strings.Repeat(" ", outermost+i*result.IndentStep))
		b.WriteString(key)
		b.WriteString(":\n")
	}
	for i, line := range entry {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Repeat(" ", result.EntryIndent))
		b.WriteString(line)
	}

	idx := yamlpos.BuildLineIndexWithEncoding([]byte(src),
		yamlpos.PositionEncoding(result.PositionEncoding))
	byteInLine, err := idx.LSPCharToByte(result.Position.Line, result.Position.Character)
	if err != nil {
		t.Fatalf("convert the reported position: %v", err)
	}
	at := lineStartOffset(src, result.Position.Line) + byteInLine
	if at > len(src) {
		t.Fatalf("the reported position resolves to byte %d in a document of %d bytes", at, len(src))
	}
	return src[:at] + b.String() + src[at:]
}

// lineStartOffset returns the byte offset of a line's first byte, counted the
// way a client that never parsed the document would count it.
func lineStartOffset(src string, line int) int {
	offset := 0
	for i := 0; i < line; i++ {
		next := strings.IndexByte(src[offset:], '\n')
		if next < 0 {
			return len(src)
		}
		offset += next + 1
	}
	return offset
}

// scalarAtInDocument walks a parsed document to the scalar at a structural path.
func scalarAtInDocument(t *testing.T, content string, path []string) (string, bool) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("the document does not parse: %v\n---\n%s---", err, content)
	}
	n := &doc
	if len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return "", false
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == seg {
				next = n.Content[i+1]
				break
			}
		}
		if next == nil {
			return "", false
		}
		n = next
	}
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

const insertionAbsentBlockDoc = "kind: Aggregate\n" +
	"metadata:\n" +
	"  name: web-app\n" +
	"spec:\n" +
	"  components:\n" +
	"    web:\n" +
	"      type: core/service\n"

const insertionExistingBlockDoc = "kind: Aggregate\n" +
	"metadata:\n" +
	"  name: web-app\n" +
	"spec:\n" +
	"  components:\n" +
	"    web:\n" +
	"      type: core/service\n" +
	"      features:\n" +
	"        metrics:\n" +
	"          type: core/metrics\n"

// TestTheDaemonLocatesWhereAnAttachmentBelongs drives the wire method over both
// cases the naive answer gets wrong, applies each answer to the document, and
// re-loads the result.
//
// The assertion is the re-load and the read-back, not the coordinate: a position
// nothing can apply satisfies an assertion about its numbers and produces a
// document that no longer parses.
func TestTheDaemonLocatesWhereAnAttachmentBelongs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		doc         string
		wantMissing []string
		wantShape   string
	}{
		{
			name:        "the features block is absent and has to be created",
			doc:         insertionAbsentBlockDoc,
			wantMissing: []string{"features"},
			wantShape:   string(yamlpos.EntryShapeUnknown),
		},
		{
			name:        "the features block is there and has to be extended",
			doc:         insertionExistingBlockDoc,
			wantMissing: nil,
			wantShape:   string(yamlpos.EntryShapeMapping),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := insertionWorkspace(t, "aggregates/web.yaml", tc.doc)
			s := newTestServer(t, root, bytes.NewReader(nil), &bytes.Buffer{})

			result := insertionOverTheWire(t, s, insertionPointParams{
				File:      "aggregates/web.yaml",
				Container: []string{"spec", "components", "web", "features"},
			})

			if result.File != "aggregates/web.yaml" {
				t.Errorf("file = %q, want the workspace-relative path", result.File)
			}
			if strings.Join(result.MissingContainerKeys, ".") != strings.Join(tc.wantMissing, ".") {
				t.Errorf("missing container keys = %v, want %v", result.MissingContainerKeys, tc.wantMissing)
			}
			if string(result.EntryShape) != tc.wantShape {
				t.Errorf("entry shape = %q, want %q", result.EntryShape, tc.wantShape)
			}

			edited := applyOverTheWire(t, tc.doc, result, []string{"logging:", "  type: core/logging"})
			got, ok := scalarAtInDocument(t, edited,
				[]string{"spec", "components", "web", "features", "logging", "type"})
			if !ok || got != "core/logging" {
				t.Fatalf("the attachment is not where it belongs after the edit (%q, found %v)\n---\n%s---",
					got, ok, edited)
			}
			// The document the author already had is untouched by the edit.
			if kept, ok := scalarAtInDocument(t, edited,
				[]string{"spec", "components", "web", "type"}); !ok || kept != "core/service" {
				t.Fatalf("the edit disturbed what the author wrote\n---\n%s---", edited)
			}
			if tc.wantMissing == nil {
				if kept, ok := scalarAtInDocument(t, edited,
					[]string{"spec", "components", "web", "features", "metrics", "type"}); !ok || kept != "core/metrics" {
					t.Fatalf("the edit disturbed the attachment already there\n---\n%s---", edited)
				}
			}
		})
	}
}

// TestTheDaemonsAnswerIsTheSharedLayers pins that the wire method reports what
// the shared entry point returns rather than a second derivation that agrees
// today. It is one half of the cross-surface proof; the tool protocol's half
// compares the same shared entry point from its own side.
func TestTheDaemonsAnswerIsTheSharedLayers(t *testing.T) {
	root, _ := insertionWorkspace(t, "aggregates/web.yaml", insertionAbsentBlockDoc)
	s := newTestServer(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	container := []string{"spec", "components", "web", "features"}

	overTheWire := insertionOverTheWire(t, s, insertionPointParams{
		File:      "aggregates/web.yaml",
		Container: container,
	})
	shared, err := InsertionPoint(root, "aggregates/web.yaml", container, yamlpos.PositionEncodingUTF16)
	if err != nil {
		t.Fatalf("the shared entry point refused: %v", err)
	}

	wireJSON, err := json.Marshal(overTheWire)
	if err != nil {
		t.Fatalf("marshal the wire answer: %v", err)
	}
	sharedJSON, err := json.Marshal(shared)
	if err != nil {
		t.Fatalf("marshal the shared answer: %v", err)
	}
	if string(wireJSON) != string(sharedJSON) {
		t.Fatalf("the daemon's answer and the shared layer's differ:\n  wire:   %s\n  shared: %s",
			wireJSON, sharedJSON)
	}
}

// TestTheDaemonAnswersAgainstAnUnsavedBuffer pins that a session reports against
// what the editor is holding rather than what was last written to disk. Without
// it the capability would send a client to a position in a document it is not
// looking at, which is precisely the divergence a session exists to close.
func TestTheDaemonAnswersAgainstAnUnsavedBuffer(t *testing.T) {
	root, abs := insertionWorkspace(t, "aggregates/web.yaml", insertionAbsentBlockDoc)
	s := newTestServer(t, root, bytes.NewReader(nil), &bytes.Buffer{})

	var buffered yaml.Node
	if err := yaml.Unmarshal([]byte(insertionExistingBlockDoc), &buffered); err != nil {
		t.Fatalf("parse the buffer: %v", err)
	}
	s.parseCache.Put(abs, &buffered, []byte(insertionExistingBlockDoc))

	result := insertionOverTheWire(t, s, insertionPointParams{
		File:      "aggregates/web.yaml",
		Container: []string{"spec", "components", "web", "features"},
	})
	if len(result.MissingContainerKeys) != 0 {
		t.Fatalf("the answer reports %v as absent; the buffer the editor is holding declares it",
			result.MissingContainerKeys)
	}
	if result.EntryShape != yamlpos.EntryShapeMapping {
		t.Fatalf("entry shape = %q; the buffer carries the container as a mapping", result.EntryShape)
	}

	edited := applyOverTheWire(t, insertionExistingBlockDoc, result,
		[]string{"logging:", "  type: core/logging"})
	if got, ok := scalarAtInDocument(t, edited,
		[]string{"spec", "components", "web", "features", "logging", "type"}); !ok || got != "core/logging" {
		t.Fatalf("the attachment is not where it belongs in the buffer after the edit\n---\n%s---", edited)
	}
}

// TestTheDaemonRefusesWhatItCannotLocate drives every refusal the surface
// carries, with an answerable request first as the positive control.
func TestTheDaemonRefusesWhatItCannotLocate(t *testing.T) {
	root, _ := insertionWorkspace(t, "aggregates/web.yaml", insertionAbsentBlockDoc)
	s := newTestServer(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	if err := os.WriteFile(filepath.Join(root, "broken.yaml"), []byte("kind: [\n"), 0o644); err != nil {
		t.Fatalf("write the malformed fixture: %v", err)
	}

	for _, tc := range []struct {
		name    string
		params  insertionPointParams
		refuses string
	}{
		{
			name: "a request the workspace can answer",
			params: insertionPointParams{
				File:      "aggregates/web.yaml",
				Container: []string{"spec", "components", "web", "features"},
			},
			refuses: "",
		},
		{
			name:    "no document named",
			params:  insertionPointParams{Container: []string{"spec"}},
			refuses: "no document was named",
		},
		{
			name:    "no container path named",
			params:  insertionPointParams{File: "aggregates/web.yaml"},
			refuses: "container path names nothing",
		},
		{
			name: "a document outside the workspace",
			params: insertionPointParams{
				File:      "../elsewhere/web.yaml",
				Container: []string{"spec"},
			},
			refuses: "not inside the workspace",
		},
		{
			name: "a document that is not there",
			params: insertionPointParams{
				File:      "aggregates/absent.yaml",
				Container: []string{"spec"},
			},
			refuses: "could not be read",
		},
		{
			name: "a document that does not parse",
			params: insertionPointParams{
				File:      "broken.yaml",
				Container: []string{"spec"},
			},
			refuses: "not well-formed YAML",
		},
		{
			name: "a container path running through a scalar",
			params: insertionPointParams{
				File:      "aggregates/web.yaml",
				Container: []string{"spec", "components", "web", "type", "features"},
			},
			refuses: "carries as a scalar",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := string(insertionWireBody(t, s, tc.params))
			switch {
			case tc.refuses == "" && strings.Contains(body, `"error"`):
				t.Fatalf("an answerable request was refused: %s", body)
			case tc.refuses == "":
				return
			case !strings.Contains(body, `"error"`):
				t.Fatalf("a request that should have been refused was answered: %s", body)
			case !strings.Contains(body, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %s", tc.refuses, body)
			}
		})
	}
}
