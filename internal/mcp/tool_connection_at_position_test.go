// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
)

// These tests hold the connection-at-position tool to the daemon's terms: the
// same documents — the daemon's connection fixture with the Release its own
// tests read for this request — the same positions, and the same answer, byte
// for byte in the fields the two surfaces share.

// connectionAtPositionReleaseFixture is the Release the daemon's tests for this
// request read, from its own package so the two surfaces cannot come to be tested
// over different documents.
var connectionAtPositionReleaseFixture = filepath.Join("..", "serve", "testdata",
	"connection-site-release-with-edge-entry.yaml")

// connectionAtPositionWorkspace is the daemon's connection fixture with that
// Release in place of its own.
func connectionAtPositionWorkspace(t *testing.T) (root, release string) {
	t.Helper()
	root, release = connectionSiteWorkspace(t)
	b, err := os.ReadFile(connectionAtPositionReleaseFixture)
	if err != nil {
		t.Fatalf("read the Release fixture: %v", err)
	}
	if err := os.WriteFile(release, b, 0o644); err != nil {
		t.Fatalf("write the Release: %v", err)
	}
	return root, release
}

// placeOf locates text on the one line of content whose trimmed text starts with
// linePrefix, as a position argument.
func placeOf(t *testing.T, content, linePrefix, text string) map[string]any {
	t.Helper()
	lines := strings.Split(content, "\n")
	found := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), linePrefix) {
			if found >= 0 {
				t.Fatalf("more than one line starts with %q", linePrefix)
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("no line starts with %q", linePrefix)
	}
	at := strings.Index(lines[found], text)
	if at < 0 {
		t.Fatalf("line %d does not carry %q", found, text)
	}
	return map[string]any{"line": found, "character": at}
}

// TestTheConnectionAtPositionToolAnswersAsTheDaemonDoes drives the registered
// tool at positions in both documents and compares the shared fields of its
// structured answer, byte for byte, with the answer the daemon gives for the same
// position — each written out once below, in the shape the daemon's own tests pin
// for the same rows.
func TestTheConnectionAtPositionToolAnswersAsTheDaemonDoes(t *testing.T) {
	root, release := connectionAtPositionWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	for _, tc := range []struct {
		name       string
		file       string
		linePrefix string
		text       string
		want       serve.ConnectionAtPositionResult
	}{
		{name: "the alias key of a connection a component draws", file: "aggregate.yaml", linePrefix: "gpt:",
			text: "gpt", want: serve.ConnectionAtPositionResult{GraphNode: "gateway",
				Connection: &serve.ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "aggregate"}}},
		{name: "the Release's own entry for that connection", file: "release.yaml", linePrefix: "gpt:",
			text: "gpt", want: serve.ConnectionAtPositionResult{GraphNode: "gateway",
				Connection: &serve.ConnectionCoordinate{Socket: "models", Alias: "gpt", Document: "release"}}},
		{name: "a connection the Release draws", file: "release.yaml", linePrefix: "target: sink",
			text: "sink", want: serve.ConnectionAtPositionResult{GraphNode: "gateway",
				Connection: &serve.ConnectionCoordinate{Socket: "logs", Alias: "out", Document: "release"}}},
		{name: "a socket's own key", file: "aggregate.yaml", linePrefix: "models:", text: "models",
			want: serve.ConnectionAtPositionResult{NoConnectionReason: serve.NoConnectionReasonNoEntry}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, tc.file))
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			res := callDiscoveryTool(t, s, connectionAtPositionToolName, map[string]any{
				"workspace_root": root,
				"file":           tc.file,
				"position":       placeOf(t, string(content), tc.linePrefix, tc.text),
				"release":        release,
			})
			var got connectionAtPositionPayload
			decodeStructured(t, res, &got)

			shared, err := json.Marshal(got.ConnectionAtPositionResult)
			if err != nil {
				t.Fatalf("marshal the shared fields: %v", err)
			}
			want, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatalf("marshal the expected answer: %v", err)
			}
			if string(shared) != string(want) {
				t.Fatalf("the tool's answer differs from the daemon's:\n  tool:   %s\n  daemon: %s", shared, want)
			}
			if tc.want.Connection == nil {
				if !strings.Contains(res.Content[0].Text, tc.want.NoConnectionReason.Sentence()) {
					t.Errorf("the readable answer does not say why no connection is reported:\n%s",
						res.Content[0].Text)
				}
				if strings.Contains(string(res.StructuredContent), `"graphNode"`) {
					t.Errorf("a graph node is sent beside the reason: %s", res.StructuredContent)
				}
				return
			}
			if !strings.Contains(res.Content[0].Text, "attach-candidates") {
				t.Errorf("the readable answer does not name the call the coordinate is for:\n%s", res.Content[0].Text)
			}
			if strings.Contains(string(res.StructuredContent), `"noConnectionReason"`) {
				t.Errorf("a reason is sent beside a connection: %s", res.StructuredContent)
			}
		})
	}
}

// TestTheConnectionAtPositionToolRefusesWhatItCannotRead drives each refusal with
// an answerable call first as the positive control.
func TestTheConnectionAtPositionToolRefusesWhatItCannotRead(t *testing.T) {
	root, release := connectionAtPositionWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}
	at := map[string]any{"line": 0, "character": 0}
	if err := os.WriteFile(filepath.Join(root, "broken.yaml"), []byte("spec: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write the unparseable document: %v", err)
	}

	for _, tc := range []struct {
		name    string
		args    map[string]any
		refuses string
	}{
		{name: "a document the Release's graph reads",
			args: map[string]any{"workspace_root": root, "file": "aggregate.yaml", "position": at, "release": release}},
		{name: "no position",
			args:    map[string]any{"workspace_root": root, "file": "aggregate.yaml", "release": release},
			refuses: "requires a 'position'"},
		{name: "a position missing its character",
			args: map[string]any{"workspace_root": root, "file": "aggregate.yaml", "position": map[string]any{"line": 1},
				"release": release},
			refuses: "requires a 'position'"},
		{name: "no Release",
			args:    map[string]any{"workspace_root": root, "file": "aggregate.yaml", "position": at},
			refuses: "requires a 'release'"},
		{name: "an encoding this surface does not read",
			args: map[string]any{"workspace_root": root, "file": "aggregate.yaml", "position": at, "release": release,
				"position_encoding": "utf-7"},
			refuses: `does not read positions in "utf-7"`},
		{name: "a document that is not well-formed",
			args:    map[string]any{"workspace_root": root, "file": "broken.yaml", "position": at, "release": release},
			refuses: "is not well-formed YAML"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callDiscoveryTool(t, s, connectionAtPositionToolName, tc.args)
			switch {
			case tc.refuses == "" && res.IsError:
				t.Fatalf("an answerable call was refused: %s", res.Content[0].Text)
			case tc.refuses == "":
				return
			case !res.IsError:
				t.Fatalf("a call that should have been refused was answered: %s", res.Content[0].Text)
			case !strings.Contains(res.Content[0].Text, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q", tc.refuses, res.Content[0].Text)
			}
		})
	}
}
