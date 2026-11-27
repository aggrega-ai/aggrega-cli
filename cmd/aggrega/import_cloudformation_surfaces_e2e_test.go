// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a CloudFormation template imported through the cloudformation importer the
// binary carries: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega
// serve`, each driven at the real command tree, naming no format and no target,
// and held to the shared operation's own result over an identically laid
// workspace, so the three draft one Aggregate, one Release, one Manifest and the
// catalog entities of the resources no Component maps alike. The editor sends
// aggrega/import to the session as it is, in
// aggrega-vscode-extension/src/extension.ts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// TestACloudFormationTemplateIsDraftedAlikeAtEverySurface imports the build-back
// template directory, naming no format and no target, at the command under both
// deliveries, at the assistant's tool and at the session, and holds each to the
// shared operation's result: the command's commentary names the cloudformation
// format and its drafts are the shared ones, and the tool and the session write
// exactly the shared drafts, byte for byte.
func TestACloudFormationTemplateIsDraftedAlikeAtEverySurface(t *testing.T) {
	embeddedentities.Require(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	importopstest.CarryImporters(t, cfnImporterURI)
	isolateFromThisMachine(t)
	const name = "stack"

	seed := func(t *testing.T, root string) {
		t.Helper()
		if err := os.CopyFS(filepath.Join(root, name), os.DirFS(cfnBuildBackFixture)); err != nil {
			t.Fatalf("copy the template into the workspace: %v", err)
		}
	}

	for _, dryRun := range []bool{true, false} {
		root := armSurfaceWorkspace(t)
		external := filepath.Join(t.TempDir(), name)
		if err := os.CopyFS(external, os.DirFS(cfnBuildBackFixture)); err != nil {
			t.Fatal(err)
		}
		shared := sharedArmImport(t, root, external)
		if shared.Format != "cloudformation" {
			t.Fatalf("the shared import read the template as %q, want cloudformation", shared.Format)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, "cloudformation", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root := armSurfaceWorkspace(t)
		seed(t, root)
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
		if payload.Format != "cloudformation" {
			t.Errorf("the tool read the template as %q, want cloudformation", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root := armSurfaceWorkspace(t)
		seed(t, root)
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
		if result.Halted || result.Format != "cloudformation" {
			t.Fatalf("the session read the template as %q, halted %v: %s", result.Format, result.Halted, frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
