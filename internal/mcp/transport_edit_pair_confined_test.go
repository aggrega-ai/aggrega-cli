// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheEditPairStaysConfinedWhereANamedPathIsHonoured holds the one exception to
// what a caller at this machine's keyboard may name.
//
// Over the process's own streams a path a call names is honoured as named, as a
// path typed on the command line is — the control below proves it for the same
// value, so a refusal further down cannot be a transport that stopped honouring
// paths. The document edit is the exception, because the command line's own edit
// is held to the workspace it is run in: edit-document applies only to a document
// the workspace carries, and insertion-point, which locates where that edit lands,
// reports positions only in such a document. So both are refused for the same
// value the control honours, and for a document in the workspace that a link
// carries out of it, and the document outside is left exactly as it was.
//
// The assertion is on the failing result a caller reads and on the bytes of the
// document outside, because a refused edit that wrote first and a refused edit
// that wrote nothing read the same from the result alone.
func TestTheEditPairStaysConfinedWhereANamedPathIsHonoured(t *testing.T) {
	served := rootedWorkspace(t)
	outside := filepath.Join(rootedWorkspace(t), theLaunchedWorkspacesRelease)
	before := readEditedDocument(t, outside)
	linked := filepath.Join(served, "parts", "linked.yaml")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: served, transport: transportStdio}

	var got validateResultPayload
	decodeStructured(t, callDiscoveryTool(t, s, "validate", map[string]any{"entity": outside}), &got)
	if told(got.Target) != outside {
		t.Fatalf("the control failed: over this process's own streams validate checked %q, not the file the "+
			"call named, %q — so the refusals below would not be about the edit pair", told(got.Target), outside)
	}

	for _, row := range []struct {
		name    string
		tool    string
		args    func(file string) map[string]any
		refuses string
	}{
		{
			name: "edit-document",
			tool: editDocumentToolName,
			args: func(file string) map[string]any {
				return map[string]any{
					"edit": "set-value", "file": file, "node": []string{"spec"}, "name": "note", "value": "edited",
				}
			},
			refuses: "an edit is only applied to a document the workspace carries",
		},
		{
			name: "insertion-point",
			tool: "insertion-point",
			args: func(file string) map[string]any {
				return map[string]any{"file": file, "container": []string{"spec"}}
			},
			refuses: "a position is only reported for a document the workspace carries",
		},
	} {
		for _, value := range []struct {
			name string
			file string
		}{
			{name: "the absolute path the control honoured", file: outside},
			{name: "a document in the workspace that a link carries out of it", file: "parts/linked.yaml"},
		} {
			t.Run(row.name+" refuses "+value.name, func(t *testing.T) {
				res := callDiscoveryTool(t, s, row.tool, row.args(value.file))
				text := res.Content[0].Text
				if !res.IsError {
					t.Fatalf("%s was answered over this process's own streams for a document outside the "+
						"workspace: %q", row.tool, text)
				}
				if !strings.Contains(text, row.refuses) {
					t.Fatalf("%s failed, but not because the document is outside the workspace, so this row "+
						"proves nothing about confinement: %q", row.tool, text)
				}
				if after := readEditedDocument(t, outside); after != before {
					t.Errorf("%s changed the document outside the workspace\n--- before ---\n%s\n--- after ---\n%s",
						row.tool, before, after)
				}
			})
		}
	}
}
