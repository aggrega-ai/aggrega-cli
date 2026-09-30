// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// Foreign-description import single-source parity, tool-protocol leg. The import
// tool runs the shared import layer and adds only its own argument binding, its
// path resolution, the destination it chooses and its result shaping. What these
// prove is that the adapter drives that layer faithfully — the same drafted file
// set, the same bytes, the same refusal — for the same request.
//
// The oracle is the shared operation run over a second, identically seeded
// workspace, and it is run with the destination THE COMMAND supplies: the
// directory the process is sitting in. So the two halves differ in exactly the
// one way this item settles they should, and everything else about the answer is
// required to be identical. Its result IS the expectation, which is what keeps
// the halves independent: an expectation drawn from the tool's own answer would
// move with the tool, so a surface that stopped drafting would keep passing
// while the assertion followed it down.
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
// half runs first, into the directory the process is sitting in — which is the
// destination the command supplies — and the files it left there are the
// expectation; the tool then imports into its own workspace and the files IT
// left are read back against them.
func TestTheImportToolDraftsWhatTheSharedOperationDoes(t *testing.T) {
	pointImportSeamAtTheDemoGuest(t)
	source := readImportFixture(t, langflowFixture)

	// The command's destination, reproduced exactly: a run directory the process
	// is sitting in, with the output root spelled as the command spells it.
	runDir := t.TempDir()
	t.Chdir(runDir)

	oracleRoot, _ := importSourceWorkspace(t, "flow.json", source)
	rw, err := workspaceconfig.Resolve(oracleRoot, oracleRoot)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	shared, err := importops.Import(context.Background(), rw, importops.Options{
		Source:        source,
		SourceName:    "flow.json",
		OutputDir:     ".",
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
		want, rerr := os.ReadFile(filepath.Join(runDir, filepath.FromSlash(name)))
		if rerr != nil {
			t.Fatalf("read the shared operation's %s: %v", name, rerr)
		}
		got, rerr := os.ReadFile(filepath.Join(toolRoot, filepath.FromSlash(name)))
		if rerr != nil {
			t.Fatalf("read the tool's %s: %v", name, rerr)
		}
		if string(got) != string(want) {
			t.Errorf("the tool's %s is not the shared operation's.\n--- tool ---\n%s\n--- shared ---\n%s",
				name, got, want)
		}
	}

	// The one intended divergence, named rather than left implicit: the shared
	// half was told where to write and the tool was not, so the two destinations
	// differ while everything compared above does not. Asserting they differ is
	// what keeps the comparison honest — two halves that happened to write into
	// one directory would agree trivially.
	if told(p.WorkspaceRoot) != toolRoot {
		t.Errorf("the tool drafted relative to %q rather than to the workspace it resolved, %q",
			told(p.WorkspaceRoot), toolRoot)
	}
	if told(p.WorkspaceRoot) == shared.OutputDir {
		t.Errorf("both halves named %q as their destination, so the comparison above did not span the "+
			"divergence it was written for", told(p.WorkspaceRoot))
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
	sharedBefore := treeUnder(t, runDir)
	rw, err := workspaceconfig.Resolve(sharedRoot, sharedRoot)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	if _, err := importops.Import(context.Background(), rw, importops.Options{
		Source:        mystery,
		SourceName:    "mystery.json",
		OutputDir:     ".",
		ResolvePlugin: importToolResolver,
	}); err == nil {
		t.Fatal("the shared operation converted a source whose shape it recognises none of, so there is no " +
			"refusal to compare")
	}
	if after := treeUnder(t, runDir); !slices.Equal(after, sharedBefore) {
		t.Errorf("the shared operation wrote into the destination it refused\n--- before ---\n%v\n--- after ---\n%v",
			sharedBefore, after)
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
