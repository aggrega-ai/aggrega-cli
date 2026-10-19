// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// an MCP client's configuration imported through the reference MCP importer laid
// in a catalog folder: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`,
// each driven at the real command tree and held to the shared operation's own
// result over an identically laid workspace, so the three draft one configuration
// alike — the Aggregate and the Release beside it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// mcpDesktopConfiguration is the committed Claude Desktop configuration the
// surfaces import, under the name Claude Desktop reads it by: a search server
// holding a key in its environment and a database server whose connection
// string, an argument, holds a password.
const mcpDesktopConfiguration = "../../internal/importops/testdata/mcp/claude_desktop_config.json"

// TestAnMCPConfigurationIsDraftedAlikeAtEverySurface imports the Claude Desktop
// configuration, naming no format, at the command under both deliveries, at the
// assistant's tool and at the session, and holds each to the shared operation's
// result: the command's commentary names the mcp format and its drafts are the
// shared ones, and the tool and the session write exactly the shared drafts, the
// Release included, byte for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/mcp-importer/main.go), write
// `Files: []string{"claude_desktop.json"}` in the Claude Desktop signature: no
// signature recognises the configuration, so the shared operation is refused
// asking for the format and the case fails before any surface is compared.
func TestAnMCPConfigurationIsDraftedAlikeAtEverySurface(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, mcpDesktopConfiguration)
	name := filepath.Base(mcpDesktopConfiguration)
	shared := sharedCatalogFolderImport(t, body, name)

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, "mcp", dryRun, shared)
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
		if payload.Format != "mcp" {
			t.Errorf("the tool read the configuration as %q, want mcp", payload.Format)
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
		if result.Halted || result.Format != "mcp" {
			t.Fatalf("the session read the configuration as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
