// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What stayed on this surface when the edit itself moved into the shared layer:
// the argument decoding, the resolution of the document through this surface's
// one path helper, the delivery it selects, and the shape of the answer. The
// edit — where an entry goes, how it is rendered, the splice, and the validation
// the write is gated on — is covered where it lives, and this surface's
// agreement with it is covered by the parity assertions beside this file.
//
// 🔴 That split is deliberate. A subsystem's tests follow the subsystem, which
// leaves the CALL SITE uncovered while every moved test passes.

// authoredAggregateForToolEdit is the document the cases below are applied to.
//
// It carries what a full re-serialize destroys — a leading comment block, a
// hand-aligned trailing comment, a blank line inside a block, a literal block
// scalar indented deeper than its key, a single-quoted scalar, and a template
// expression that only survives as text — so an edit that reformatted the
// document would fail the preservation assertion rather than passing it.
//
// It is deliberately the same authored document the command line's own leg of
// this parity is driven over, so the two legs are over one request in fact and
// not only in principle. Nothing enforces that, and nothing needs to: what makes
// the two surfaces agree is that each is held to the shared operation's own
// result, not that they were handed the same bytes.
const authoredAggregateForToolEdit = `# The storefront application.
#
# The ordering below is deliberate: the web tier reads first.
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: storefront        # the module's own name
  version: 1.4.2
spec:
  parameters:
    db_size:
      type: string
      default: '10Gi'

  components:

    web:
      type: core/api-server:1.0.0
      description: |
        The public web tier.
          This line is indented inside a literal block.
      values:
        replicas: 3
      uses:
        database:
          primary:
            target: db

    # The data tier is intentionally last.
    db:
      type: core/postgres:2.1.0
      values:
        size: '{{ .parameters.db_size }}'
`

// authoredReleaseForToolEdit is the Release-side fixture. The authoring
// schema gives a Release's component surface operator concerns only — no
// developer values — so it is the document the invariant refusal is driven
// against.
const authoredReleaseForToolEdit = `# The production Release.
apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: storefront-prod
  environment: production
  version: 1.4.2
spec:
  aggregate: storefront
  manifest: prod-manifest
  components:
    web:
      infra:
        replicas: 5
`

// editToolWorkspace lays a fixture down in a workspace of its own and returns
// the root, the document's workspace-relative name, and its absolute path.
//
// The workspace carries its own configuration file so that resolution settles on
// this directory. Without one it would begin at this directory and walk upward
// until it found a configured workspace, reaching the checkout the suite happens
// to run inside — and the case would then edit a document nobody wrote for it.
func editToolWorkspace(t *testing.T, rel, content string) (root, relPath, absPath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\noffline: true\n"), 0o644); err != nil {
		t.Fatalf("write the workspace config: %v", err)
	}
	abs := filepath.Join(root, rel)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write the fixture document: %v", err)
	}
	return root, rel, abs
}

// editToolServer builds a session over the given workspace root, reached over
// the process's own streams.
func editToolServer(t *testing.T, root string) *server {
	t.Helper()
	return &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root}
}

// readEditedDocument reads the document back off disk, which is what a caller
// inspects after an edit and what every assertion here is made against.
func readEditedDocument(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// editToolPayload decodes the structured answer, failing when the tool reported
// a failure instead.
func editToolPayload(t *testing.T, res *callToolResult) documentEditPayload {
	t.Helper()
	if res.IsError {
		t.Fatalf("the edit was refused: %s", res.Content[0].Text)
	}
	var p documentEditPayload
	if err := json.Unmarshal(res.StructuredContent, &p); err != nil {
		t.Fatalf("decode the structured answer: %v (body %s)", err, res.StructuredContent)
	}
	return p
}

// TestTheEditToolLandsTheEntryAndPreservesEveryOtherByte drives the tool through
// the real tools/call dispatch and asserts against the bytes on disk rather than
// against the value it returned, because what a caller inspects afterwards is
// the file.
func TestTheEditToolLandsTheEntryAndPreservesEveryOtherByte(t *testing.T) {
	root, rel, abs := editToolWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForToolEdit)
	s := editToolServer(t, root)

	res := callTool(t, s, editDocumentToolName, map[string]any{
		"edit":  "add-component",
		"file":  rel,
		"node":  []string{"spec", "components"},
		"name":  "checkout",
		"value": "core/api-server:1.0.0",
	})
	p := editToolPayload(t, res)

	if got := p.Written; len(got) != 1 || got[0] != rel {
		t.Errorf("the answer names %v as what reached disk, want exactly [%q]", got, rel)
	}
	if p.Content != "" {
		t.Errorf("a write handed the whole document back as content as well, which spends a caller's budget "+
			"on bytes it asked to have written: %q", p.Content)
	}

	onDisk := readEditedDocument(t, abs)
	if !strings.Contains(onDisk, "    checkout:\n      type: core/api-server:1.0.0\n") {
		t.Errorf("the entry did not land\n--- document ---\n%s", onDisk)
	}

	// Preservation is asserted by requiring the specific lines the author wrote
	// to survive. A surface that re-serialized the document would still land the
	// entry, so an assertion about the entry alone would pass for it.
	for _, line := range []string{
		"# The storefront application.\n",
		"  name: storefront        # the module's own name\n",
		"          This line is indented inside a literal block.\n",
		"    # The data tier is intentionally last.\n",
		"        size: '{{ .parameters.db_size }}'\n",
		"\n  components:\n\n",
	} {
		if !strings.Contains(onDisk, line) {
			t.Errorf("the edit did not preserve %q\n--- document ---\n%s", line, onDisk)
		}
	}
}

// TestTheEditToolReportsTheContainerItLandedInAndWhatItCreated pins the
// placement facts the answer carries, over an edit that has to bring two
// container levels into being.
//
// The row that creates nothing is the control: without it a surface that named
// created containers unconditionally would satisfy the first half.
func TestTheEditToolReportsTheContainerItLandedInAndWhatItCreated(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          map[string]any
		container     []string
		createdLevels int
	}{
		{
			name: "wiring inside a socket under a node carrying no connections at all",
			args: map[string]any{
				"edit": "wire-edge", "node": []string{"spec", "components", "db"},
				"socket": "storage", "name": "volume", "value": "web",
			},
			container:     []string{"spec", "components", "db", "uses", "storage"},
			createdLevels: 2,
		},
		{
			name: "adding a component to a container the document already carries",
			args: map[string]any{
				"edit": "add-component", "node": []string{"spec", "components"},
				"name": "checkout", "value": "core/api-server:1.0.0",
			},
			container:     []string{"spec", "components"},
			createdLevels: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, rel, _ := editToolWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForToolEdit)
			s := editToolServer(t, root)
			args := map[string]any{"file": rel}
			for k, v := range tc.args {
				args[k] = v
			}

			p := editToolPayload(t, callTool(t, s, editDocumentToolName, args))
			if strings.Join(p.Container, ".") != strings.Join(tc.container, ".") {
				t.Errorf("the entry is reported under %v, want %v", p.Container, tc.container)
			}
			if got := len(p.CreatedContainers); got != tc.createdLevels {
				t.Errorf("the answer names %d created container level(s) (%v), want %d",
					got, p.CreatedContainers, tc.createdLevels)
			}
			if p.CreatedContainers == nil || p.Written == nil || p.Container == nil {
				t.Error("a list field arrived absent rather than empty, so a client reading it as a list cannot")
			}
		})
	}
}

// TestTheEditToolsDryRunWritesNothingAndHandsBackTheDocument pins the reporting
// delivery: the whole edited document as content, and a file left untouched.
//
// Both halves are asserted. A delivery that wrote nothing and also reported
// nothing would satisfy a check on the file alone, and a delivery that reported
// the document and wrote it too would satisfy a check on the content alone.
func TestTheEditToolsDryRunWritesNothingAndHandsBackTheDocument(t *testing.T) {
	root, rel, abs := editToolWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForToolEdit)
	before := readEditedDocument(t, abs)
	s := editToolServer(t, root)

	p := editToolPayload(t, callTool(t, s, editDocumentToolName, map[string]any{
		"edit": "add-component", "file": rel, "node": []string{"spec", "components"},
		"name": "checkout", "value": "core/api-server:1.0.0", "dry_run": true,
	}))

	if len(p.Written) != 0 {
		t.Errorf("a reporting delivery named %v as written; nothing reaches disk under it", p.Written)
	}
	if !strings.Contains(p.Content, "    checkout:\n      type: core/api-server:1.0.0\n") {
		t.Errorf("the reported document does not carry the entry the edit composed:\n%s", p.Content)
	}
	if after := readEditedDocument(t, abs); after != before {
		t.Errorf("a reporting delivery wrote to the file\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestAnEditTheSchemaRefusesLeavesTheFileByteIdenticalOnThisSurface is the
// invariant proof at this surface.
//
// The assertion is byte-identity of the file rather than the presence of a
// failure, and that is the half that matters: a surface which composed the
// document, wrote it, and then reported a failure would satisfy a
// failure-is-reported assertion and fail this one.
//
// The refusal is also required to carry the short code of the registered
// condition, because that is what a caller branches on to tell "your request was
// malformed" from "your request was fine and the document would not have been".
func TestAnEditTheSchemaRefusesLeavesTheFileByteIdenticalOnThisSurface(t *testing.T) {
	root, rel, abs := editToolWorkspace(t, "prod.release.yaml", authoredReleaseForToolEdit)
	before := readEditedDocument(t, abs)
	s := editToolServer(t, root)

	res := callTool(t, s, editDocumentToolName, map[string]any{
		"edit": "set-value", "file": rel, "node": []string{"spec", "components", "web"},
		"name": "replicas", "value": 9,
	})
	if !res.IsError {
		t.Fatalf("a Release document asked to carry a developer value must be refused: %s", res.Content[0].Text)
	}
	if after := readEditedDocument(t, abs); after != before {
		t.Errorf("the refusal wrote to the file\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if !strings.Contains(res.Content[0].Text, "byte-for-byte as it was") {
		t.Errorf("the refusal does not tell the caller the document is untouched: %q", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "E0275") {
		t.Errorf("the refusal carries no short code, so a caller cannot tell a malformed request from an "+
			"admissible one the document would not accept: %q", res.Content[0].Text)
	}
}

// TestTheReleaseFixtureAcceptsALegitimateEditOnThisSurfaceFirst is the positive
// control for the refusal above.
//
// Without it the refusal could be reported for a document that was never valid,
// and the case above would pass while proving nothing about the edit.
func TestTheReleaseFixtureAcceptsALegitimateEditOnThisSurfaceFirst(t *testing.T) {
	root, rel, _ := editToolWorkspace(t, "prod.release.yaml", authoredReleaseForToolEdit)
	s := editToolServer(t, root)

	p := editToolPayload(t, callTool(t, s, editDocumentToolName, map[string]any{
		"edit": "attach-feature", "file": rel, "node": []string{"spec", "components", "web"},
		"name": "budget-alert", "value": "core/finops/budget-alert:1.0.0",
	}))
	if len(p.Written) != 1 {
		t.Errorf("the control edit wrote %v; the fixture must accept a legitimate edit for the refusal beside "+
			"it to be attributable", p.Written)
	}
}

// TestTheEditToolRefusesWithoutNamingAnEditAndSaysWhatMayBeNamed pins the one
// refusal this surface composes for itself, and that it reads the bound from the
// shared enumeration rather than restating it.
func TestTheEditToolRefusesWithoutNamingAnEditAndSaysWhatMayBeNamed(t *testing.T) {
	root, rel, _ := editToolWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForToolEdit)
	s := editToolServer(t, root)

	res := callTool(t, s, editDocumentToolName, map[string]any{"file": rel})
	if !res.IsError {
		t.Fatal("a call naming no edit was answered rather than refused")
	}
	if got, want := res.Content[0].Text, editDocumentKindList(); !strings.Contains(got, want) {
		t.Errorf("the refusal does not name the edits the shared layer applies (%q): %q", want, got)
	}
}
