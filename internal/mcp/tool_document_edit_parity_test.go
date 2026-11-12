// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// Structured-edit single-source parity, tool-protocol leg. The edit tool runs
// the shared editing layer and adds only its own argument binding, its path
// resolution and its result shaping. What these prove is that the adapter drives
// that layer faithfully — the same bytes on disk, the same refusal, the same
// document left untouched — for the same request.
//
// The oracle is the shared operation run over a second, identically seeded
// workspace, and its result IS the expectation. That is what keeps the two
// halves independent: an expectation drawn from the tool's own answer would move
// with the tool, so a surface that stopped applying the edit would keep passing
// while the assertion followed it down.
//
// 🔴 The command line is held to THAT SAME ORACLE, in its own package, over the
// same authored document. So the two surfaces produce the same bytes for one
// request without either test reaching into the other's package — which is also
// the only arrangement available, since neither package can call the other's
// entry point. What that arrangement cannot catch is a defect in the shared
// layer itself, which moves both surfaces together; that is why the layer has
// its own tests, and why the value typing below is asserted against the
// command's spelling rather than against the layer's behaviour alone.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/editops"
)

// editParityCase is one request in two halves that must agree: the shared
// operation, whose result is the whole expectation, and the tool that drives it.
//
// value and toolValue are the same value in two spellings. toolValue is what a
// caller sends as JSON; value is what the command's own resolution of the same
// characters produces, which is the typed value the shared layer is handed
// there. Holding the tool to an oracle run with THAT value is what makes the two
// surfaces agree on a typed value rather than on a piece of text — a tool that
// stringified what it received would still land an entry, and would land a
// different one.
type editParityCase struct {
	name      string
	document  string
	content   string
	edit      string
	node      []string
	socket    string
	entry     string
	value     any
	toolValue any
	// writes is a control ON THE ROW rather than the assertion: it keeps a row
	// from silently ceasing to cover the case it was written for.
	writes bool
}

// editParityCases exercise each admissible edit once, and the value typing
// twice: a bare word, which is text on both surfaces, and an unquoted number,
// which is a number on both.
func editParityCases() []editParityCase {
	return []editParityCase{
		{
			name:      "adding a component, whose value is a reference on both surfaces",
			document:  "storefront.aggregate.yaml",
			content:   authoredAggregateForToolEdit,
			edit:      "add-component",
			node:      []string{"spec", "components"},
			entry:     "checkout",
			value:     "core/api-server:1.0.0",
			toolValue: "core/api-server:1.0.0",
			writes:    true,
		},
		{
			name:      "attaching a Feature",
			document:  "storefront.aggregate.yaml",
			content:   authoredAggregateForToolEdit,
			edit:      "attach-feature",
			node:      []string{"spec", "components", "db"},
			entry:     "tls",
			value:     "core/tls:1.0.0",
			toolValue: "core/tls:1.0.0",
			writes:    true,
		},
		{
			name:      "wiring an edge inside a socket the node does not carry yet",
			document:  "storefront.aggregate.yaml",
			content:   authoredAggregateForToolEdit,
			edit:      "wire-edge",
			node:      []string{"spec", "components", "db"},
			socket:    "storage",
			entry:     "volume",
			value:     "web",
			toolValue: "web",
			writes:    true,
		},
		{
			// The typed-value row. A command line carries characters and resolves
			// them through the document's own language, so `--value 4` is the
			// number 4; a tool call carries JSON and holds the number already. The
			// oracle is run with the value the COMMAND's resolution produces, so
			// this fails if the tool narrows a JSON number to text.
			name:      "setting a value the command spells as characters and a call sends as a number",
			document:  "storefront.aggregate.yaml",
			content:   authoredAggregateForToolEdit,
			edit:      "set-value",
			node:      []string{"spec", "components", "web"},
			entry:     "max_replicas",
			value:     4,
			toolValue: float64(4),
			writes:    true,
		},
		{
			// A caller that sends nothing at all is a different request from one
			// that sends the empty string, and the layer distinguishes them. This
			// row pins the second, since the first is what an omitted argument
			// already produces everywhere else.
			name:      "setting a value to the empty string, which is not the same as sending none",
			document:  "storefront.aggregate.yaml",
			content:   authoredAggregateForToolEdit,
			edit:      "set-value",
			node:      []string{"spec", "components", "web"},
			entry:     "note",
			value:     "",
			toolValue: "",
			writes:    true,
		},
	}
}

// oracle runs the shared operation over a workspace of its own and returns the
// document it produced. Its bytes are the whole expectation.
func (tc editParityCase) oracle(t *testing.T) string {
	t.Helper()
	root, rel, abs := editToolWorkspace(t, tc.document, tc.content)
	res, err := editops.Apply(t.Context(), root, editops.Options{
		File:   rel,
		Kind:   editops.Kind(tc.edit),
		Node:   tc.node,
		Socket: tc.socket,
		Name:   tc.entry,
		Value:  tc.value,
	})
	if err != nil {
		t.Fatalf("the shared edit refused, so it produced no expectation to compare against: %v", err)
	}
	if wrote := len(res.Written) != 0; wrote != tc.writes {
		t.Fatalf("the shared edit wrote=%v and this row was written for wrote=%v, so it no longer covers its case",
			wrote, tc.writes)
	}
	return readEditedDocument(t, abs)
}

// TestTheEditToolProducesTheBytesTheSharedOperationDoes is the parity proof.
//
// Two workspaces are seeded per row from one helper, so the two halves see the
// same document without either half having seen the other's. The shared half
// runs first and its bytes are the expectation; the tool then edits the other
// workspace and the file it left behind is read back against them.
func TestTheEditToolProducesTheBytesTheSharedOperationDoes(t *testing.T) {
	for _, tc := range editParityCases() {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.oracle(t)

			root, rel, abs := editToolWorkspace(t, tc.document, tc.content)
			s := editToolServer(t, root)
			args := map[string]any{
				"edit": tc.edit, "file": rel, "node": tc.node, "name": tc.entry, "value": tc.toolValue,
			}
			if tc.socket != "" {
				args["socket"] = tc.socket
			}
			editToolPayload(t, callTool(t, s, editDocumentToolName, args))

			if got := readEditedDocument(t, abs); got != want {
				t.Errorf("the tool's document is not the shared operation's.\n--- tool ---\n%s\n--- shared ---\n%s",
					got, want)
			}
		})
	}
}

// TestAnEditBothSurfacesRefuseLeavesBothDocumentsUntouched is the refusal half,
// which a parity over successful edits cannot see.
//
// Two surfaces can agree on every document they produce and still differ on what
// they do with one they will not produce — and the difference that matters is
// whether the file was written before the failure was reported. So the shared
// operation and the tool are each driven at the same refused request, and each
// document is required to be byte-identical to what it was.
func TestAnEditBothSurfacesRefuseLeavesBothDocumentsUntouched(t *testing.T) {
	refused := func(rel string) editops.Options {
		return editops.Options{
			File: rel, Kind: editops.KindSetValue,
			Node: []string{"spec", "components", "web"}, Name: "replicas", Value: 9,
		}
	}

	sharedRoot, sharedRel, sharedAbs := editToolWorkspace(t, "prod.release.yaml", authoredReleaseForToolEdit)
	sharedBefore := readEditedDocument(t, sharedAbs)
	if _, err := editops.Apply(t.Context(), sharedRoot, refused(sharedRel)); err == nil {
		t.Fatal("the shared operation applied an edit its own schema gate refuses, so there is no refusal to compare")
	}
	if after := readEditedDocument(t, sharedAbs); after != sharedBefore {
		t.Errorf("the shared operation wrote to the document it refused")
	}

	toolRoot, toolRel, toolAbs := editToolWorkspace(t, "prod.release.yaml", authoredReleaseForToolEdit)
	toolBefore := readEditedDocument(t, toolAbs)
	s := editToolServer(t, toolRoot)
	res := callTool(t, s, editDocumentToolName, map[string]any{
		"edit": "set-value", "file": toolRel, "node": []string{"spec", "components", "web"},
		"name": "replicas", "value": 9,
	})
	if !res.IsError {
		t.Fatalf("the tool applied an edit the shared operation refuses: %s", res.Content[0].Text)
	}
	if after := readEditedDocument(t, toolAbs); after != toolBefore {
		t.Errorf("the tool wrote to the document it refused\n--- before ---\n%s\n--- after ---\n%s",
			toolBefore, after)
	}

	// Both documents are the same bytes they started as, which is the property
	// under test; asserting them against each other as well is what catches a
	// fixture that stopped being the same document on the two sides.
	if sharedBefore != toolBefore {
		t.Fatalf("the two halves were seeded from different documents, so the comparison above measured nothing")
	}
}

// TestTheEditToolWritesNoTemporaryFileBeside pins that the atomic write leaves
// nothing behind in the directory it wrote into.
//
// It is asserted here rather than in the layer's own tests because this is the
// surface a remote caller reaches, and a stray temporary file under the served
// workspace is something that caller would then be shown by the enumeration
// tools.
func TestTheEditToolWritesNoTemporaryFileBeside(t *testing.T) {
	root, rel, _ := editToolWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForToolEdit)
	s := editToolServer(t, root)

	editToolPayload(t, callTool(t, s, editDocumentToolName, map[string]any{
		"edit": "add-component", "file": rel, "node": []string{"spec", "components"},
		"name": "checkout", "value": "core/api-server:1.0.0",
	}))

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the workspace back: %v", err)
	}
	var left []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			left = append(left, e.Name())
		}
	}
	if len(left) != 0 {
		t.Errorf("the write left %v behind in the served workspace", left)
	}
}
