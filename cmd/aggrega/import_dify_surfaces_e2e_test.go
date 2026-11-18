// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a Dify application imported through the reference Dify importer laid in a
// catalog folder: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`,
// each driven at the real command tree and held to the shared operation's own
// result over an identically laid workspace, so the three draft one application
// alike — the Aggregate composing the Dify installation carrying it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// difyApplication is the committed authored workflow application the surfaces
// import, under a name of its author's choosing.
const difyApplication = "../../internal/importops/testdata/dify/authored/workflow.yml"

// TestADifyApplicationIsDraftedAlikeAtEverySurface imports the workflow
// application, naming no format, at the command under both deliveries, at the
// assistant's tool and at the session, and holds each to the shared operation's
// result: the command's commentary names the dify format and its drafts are the
// shared ones, and the tool and the session write exactly the shared drafts,
// byte for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/dify-importer/main.go), write
// `{Path: "app.mode", Is: "absent"}`: no signature recognises the application,
// so the shared operation is refused asking for the format and the case fails
// before any surface is compared.
func TestADifyApplicationIsDraftedAlikeAtEverySurface(t *testing.T) {
	embeddedentities.Require(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, difyApplication)
	const name = "support-answers.yml"
	shared := sharedCatalogFolderImport(t, body, name)

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, "dify", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			t.Fatalf("seed the served workspace: %v", err)
		}
		answer := callAssistantTool(t, root, "import", map[string]any{"source": name})
		assertImportToolDrafted(t, answer)
		var payload struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		if payload.Format != "dify" {
			t.Errorf("the tool read the application as %q, want dify", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			t.Fatalf("seed the served workspace: %v", err)
		}
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
		if result.Halted || result.Format != "dify" {
			t.Fatalf("the session read the application as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
