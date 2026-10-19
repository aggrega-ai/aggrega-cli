// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a Compose application imported through the reference Compose importer laid in a
// catalog folder: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`,
// each driven at the real command tree and held to the shared operation's own
// result over an identically laid workspace, so the three draft one application
// alike — the Aggregate, the Release beside it and the drafted model Component.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// composeApplication is the committed compose-for-agents LangGraph application the
// surfaces import, under the name Compose reads it by: a database, services built
// from the project, an MCP gateway and a model.
const composeApplication = "../../internal/importops/testdata/compose/compose-for-agents/langgraph.compose.yaml"

// TestAComposeApplicationIsDraftedAlikeAtEverySurface imports the LangGraph
// application, naming no format, at the command under both deliveries, at the
// assistant's tool and at the session, and holds each to the shared operation's
// result: the command's commentary names the compose format and its drafts are
// the shared ones, and the tool and the session write exactly the shared drafts,
// the Release included, byte for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/compose-importer/main.go), write
// `Files: composeimport.OverrideFileNames`: no signature recognises compose.yaml,
// so the shared operation is refused asking for the format and the case fails
// before any surface is compared.
func TestAComposeApplicationIsDraftedAlikeAtEverySurface(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, composeApplication)
	const name = "compose.yaml"
	shared := sharedCatalogFolderImport(t, body, name)

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, "compose", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			t.Fatalf("seed the served workspace: %v", err)
		}
		answer := callAssistantTool(t, root, "import", map[string]any{"source": name})
		if answer.isError {
			t.Fatalf("the import was refused: %s", answer.text)
		}
		var payload struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		if payload.Format != "compose" {
			t.Errorf("the tool read the application as %q, want compose", payload.Format)
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
		if result.Halted || result.Format != "compose" {
			t.Fatalf("the session read the application as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
