// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// an Azure Resource Manager template imported through the arm importer the binary
// carries: `aggrega import`, the assistant's import tool through `aggrega mcp`,
// and the session's aggrega/import method through `aggrega serve`, each driven at
// the real command tree, naming no format and no target, and held to the shared
// operation's own result over an identically laid workspace, so the three draft
// one Aggregate, one Release, one Manifest and the catalog entities of the
// resource no Component maps alike. The editor sends aggrega/import to the session
// as it is, in aggrega-vscode-extension/src/extension.ts.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// armSurfaceWorkspace lays a workspace whose catalog folder is empty, so every
// resource of the template is drafted from the source, and returns its root.
func armSurfaceWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	importopstest.WriteOfflineConfiguration(t, root)
	return root
}

// sharedArmImport is the expectation every surface is held to: the shared
// operation over the workspace at root, naming no format and no target and asked
// to report rather than write. It refuses an expectation that would measure
// nothing.
func sharedArmImport(t *testing.T, root, source string) importops.Result {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    source,
		SourceName:    filepath.Base(source),
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import was refused, so there is no expectation: %v", err)
	}
	if res.Halted() || len(res.Files) == 0 {
		t.Fatalf("the shared import halted or drafted nothing: %+v", res.Diagnostics)
	}
	return res
}

// seedArmTemplates copies the template directory the surfaces import into root,
// under name, as a source that sits in the workspace.
func seedArmTemplates(t *testing.T, root, name string) {
	t.Helper()
	if err := os.CopyFS(filepath.Join(root, name), os.DirFS(armBuildBackFixture)); err != nil {
		t.Fatalf("copy the templates into the workspace: %v", err)
	}
}

// TestAnArmTemplateIsDraftedAlikeAtEverySurface imports the build-back template
// directory, naming no format and no target, at the command under both
// deliveries, at the assistant's tool and at the session, and holds each to the
// shared operation's result: the command's commentary names the arm format and
// its drafts are the shared ones, and the tool and the session write exactly the
// shared drafts, byte for byte.
func TestAnArmTemplateIsDraftedAlikeAtEverySurface(t *testing.T) {
	embeddedentities.Require(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	importopstest.CarryImporters(t, armImporterURI)
	isolateFromThisMachine(t)
	const name = "stack"

	for _, dryRun := range []bool{true, false} {
		root := armSurfaceWorkspace(t)
		external := filepath.Join(t.TempDir(), name)
		if err := os.CopyFS(external, os.DirFS(armBuildBackFixture)); err != nil {
			t.Fatal(err)
		}
		shared := sharedArmImport(t, root, external)
		if shared.Format != "arm" {
			t.Fatalf("the shared import read the template as %q, want arm", shared.Format)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, "arm", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root := armSurfaceWorkspace(t)
		seedArmTemplates(t, root, name)
		shared := sharedArmImport(t, root, filepath.Join(root, name))
		answer := callAssistantTool(t, root, "import", map[string]any{"source": name})
		assertImportToolDrafted(t, answer)
		var payload struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		if payload.Format != "arm" {
			t.Errorf("the tool read the template as %q, want arm", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root := armSurfaceWorkspace(t)
		seedArmTemplates(t, root, name)
		shared := sharedArmImport(t, root, filepath.Join(root, name))
		frame, stderr := callSession(t, root, "aggrega/import", map[string]any{"source": name, "outputDir": "drafts"})
		if raw, refused := frame["error"]; refused {
			t.Fatalf("aggrega/import was refused: %s\nstderr:\n%s", raw, stderr)
		}
		var result struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
			Halted  bool     `json:"halted"`
		}
		if err := json.Unmarshal(frame["result"], &result); err != nil {
			t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
		}
		if result.Halted || result.Format != "arm" {
			t.Fatalf("the session read the template as %q, halted %v: %s", result.Format, result.Halted, frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
