// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// Foreign-description import single-source parity, tool-protocol leg. The import
// tool runs the shared import layer and adds only its own argument binding, its
// path resolution and its result shaping; it names no destination. What these
// prove is that the adapter drives that layer faithfully — the same drafted file
// set, the same bytes, the same refusal — for the same request.
//
// The oracle is the shared operation run over a second, identically seeded
// workspace, and it is run as THE COMMAND runs it when given no destination:
// handed none, so the operation's own default decides. The process is sitting in
// a folder outside both workspaces, so a default read from that folder rather
// than from the workspace lands somewhere the comparison can see. Both halves are
// then required to have written beneath their own workspace's root — one
// destination rule — and everything else about the answer to be identical. Its
// result IS the expectation, which is what keeps the halves independent: an
// expectation drawn from the tool's own answer would move with the tool, so a
// surface that stopped drafting would keep passing while the assertion followed
// it down.
//
// 🔴 The command line is held to THAT SAME ORACLE, in its own package, over the
// same guest — its drafted file and its reported document are both derived from
// the shared operation's own result rather than from the command's rendering. So
// the two surfaces produce the same bytes for one request without either test
// reaching into the other's package, which is also the only arrangement
// available, since neither package can call the other's entry point. What that
// arrangement cannot catch is a defect in the shared layer itself, which moves
// both surfaces together; that is why the layer has its own tests.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheImportToolDraftsWhatTheSharedOperationDoes is the parity proof.
//
// Two workspaces are seeded per run from one helper, so the two halves see the
// same source without either half having seen the other's output. The shared
// half runs first, handed no destination as the command hands none, and the
// files it left beneath its workspace root are the expectation; the tool then
// imports into its own workspace and the files IT left are read back against
// them.
//
// 🔴 PLANTED DEFECTS:
//   - have the import tool hand the layer a destination of its own that differs
//     from the layer's default (internal/mcp/tool_import.go, add
//     OutputDir: tw.root + string(os.PathSeparator) + "drafts" to the
//     importops.Options literal): the tool's files are not at its root and its
//     output_dir is not its workspace root, so this reddens.
//   - have outputDirFor (internal/importops/import.go) return "." for an empty
//     name: both halves write into the folder the process is sitting in, which
//     the last assertion reads, so this reddens.
func TestTheImportToolDraftsWhatTheSharedOperationDoes(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	source := readImportFixture(t, langflowFixture)

	// A folder outside both workspaces for the process to sit in, so a default
	// read from the process's folder is told apart from one read from the
	// workspace.
	runDir := t.TempDir()
	t.Chdir(runDir)

	oracleRoot, _ := importSourceWorkspace(t, "flow.json", source)
	rw, err := workspaceconfig.Resolve(oracleRoot, oracleRoot)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	shared, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    filepath.Join(oracleRoot, "flow.json"),
		SourceName:    "flow.json",
		ResolvePlugin: importToolResolver,
	})
	if err != nil {
		t.Fatalf("the shared import refused, so it produced no expectation to compare against: %v", err)
	}
	if len(shared.Written) == 0 {
		t.Fatal("the shared import wrote nothing, so the comparison below would hold two empty sets against " +
			"each other and measure nothing")
	}

	toolRoot, rel := importSourceWorkspace(t, "flow.json", source)
	p := importToolPayload(t, callTool(t, importToolServer(t, toolRoot), importToolName,
		map[string]any{"source": rel}))

	if p.Format != shared.Format {
		t.Errorf("the tool read the source as the %q format and the shared operation read it as %q",
			p.Format, shared.Format)
	}
	if !slices.Equal(p.Written, shared.Written) {
		t.Fatalf("the tool drafted %v and the shared operation drafted %v", p.Written, shared.Written)
	}
	for _, name := range shared.Written {
		want, rerr := os.ReadFile(filepath.Join(oracleRoot, filepath.FromSlash(name)))
		if rerr != nil {
			t.Fatalf("read the shared operation's %s beneath its workspace root: %v", name, rerr)
		}
		got, rerr := os.ReadFile(filepath.Join(toolRoot, filepath.FromSlash(name)))
		if rerr != nil {
			t.Fatalf("read the tool's %s beneath its workspace root: %v", name, rerr)
		}
		if string(got) != string(want) {
			t.Errorf("the tool's %s is not the shared operation's.\n--- tool ---\n%s\n--- shared ---\n%s",
				name, got, want)
		}
	}

	// ONE destination, asserted on both halves: each wrote beneath the root of
	// its own workspace and names that root as the directory its paths are
	// relative to. The command naming no destination and the tool, which names
	// none, are the same rule.
	if told(p.WorkspaceRoot) != toolRoot {
		t.Errorf("the tool resolved the workspace %q rather than the one it was launched over, %q",
			told(p.WorkspaceRoot), toolRoot)
	}
	if told(p.OutputDir) != toolRoot {
		t.Errorf("the tool drafted relative to %q rather than to the root of the workspace it resolved, %q",
			told(p.OutputDir), toolRoot)
	}
	if shared.OutputDir != rw.Root() {
		t.Errorf("the shared operation, handed no destination, drafted relative to %q rather than to the root "+
			"of its workspace, %q", shared.OutputDir, rw.Root())
	}
	if entries := treeUnder(t, runDir); len(entries) != 0 {
		t.Errorf("an import wrote into the folder the process is sitting in, which neither half names: %v", entries)
	}
}

// TestAnImportBothSurfacesRefuseLeavesBothWorkspacesUntouched is the refusal
// half, which a parity over successful imports cannot see.
//
// Two surfaces can agree on every file they produce and still differ on what
// they do with a source they will not convert — and the difference that matters
// is whether anything reached disk before the refusal was reported. So the
// shared operation and the tool are each driven at the same refused request, and
// each tree is required to be exactly what it was.
func TestAnImportBothSurfacesRefuseLeavesBothWorkspacesUntouched(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	mystery := []byte(`{"unrecognised":"shape"}`)

	runDir := t.TempDir()
	t.Chdir(runDir)

	sharedRoot, _ := importSourceWorkspace(t, "mystery.json", mystery)
	sharedBefore := treeUnder(t, sharedRoot)
	rw, err := workspaceconfig.Resolve(sharedRoot, sharedRoot)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	if _, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    filepath.Join(sharedRoot, "mystery.json"),
		SourceName:    "mystery.json",
		ResolvePlugin: importToolResolver,
	}); err == nil {
		t.Fatal("the shared operation converted a source whose shape it recognises none of, so there is no " +
			"refusal to compare")
	}
	if after := treeUnder(t, sharedRoot); !slices.Equal(after, sharedBefore) {
		t.Errorf("the shared operation wrote into the workspace it refused\n--- before ---\n%v\n--- after ---\n%v",
			sharedBefore, after)
	}
	if entries := treeUnder(t, runDir); len(entries) != 0 {
		t.Errorf("the shared operation wrote into the folder the process is sitting in: %v", entries)
	}

	toolRoot, rel := importSourceWorkspace(t, "mystery.json", mystery)
	toolBefore := treeUnder(t, toolRoot)
	res := callTool(t, importToolServer(t, toolRoot), importToolName, map[string]any{"source": rel})
	if !res.IsError {
		t.Fatalf("the tool converted a source the shared operation refuses: %s", res.Content[0].Text)
	}
	if after := treeUnder(t, toolRoot); !slices.Equal(after, toolBefore) {
		t.Errorf("the tool wrote into the workspace it refused\n--- before ---\n%v\n--- after ---\n%v",
			toolBefore, after)
	}
}
