// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// an MCP client's configuration imported through the reference MCP importer laid
// in a catalog folder: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`,
// each driven at the real command tree and held to the shared operation's own
// result over an identically laid workspace, so the three draft one configuration
// alike — the Aggregate and the Release beside it. Visual Studio Code's
// configuration written as JSON with comments is recognised at each, and drafted
// as its copy without comments is.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// mcpFixtureDir holds the committed MCP fixtures.
const mcpFixtureDir = "../../internal/importops/testdata/mcp"

// mcpSurfaceConfigurations are the committed configurations the surfaces
// import, each under the name its client reads it by: Claude Desktop's, with a
// search server holding a key in its environment and a database server whose
// connection string, an argument, holds a password; Visual Studio Code's,
// written as JSON with comments — line and block comments and trailing commas —
// with the copy of it without them its drafts are held to; and Cursor's, with a
// remote server whose address carries a key in its user part, its query and its
// fragment, each carried in the comment above the server's entry.
var mcpSurfaceConfigurations = []struct {
	fixture, name, uncommented string
}{
	{fixture: "claude_desktop_config.json", name: "claude_desktop_config.json"},
	{fixture: "vscode-commented.mcp.json", name: ".vscode/mcp.json", uncommented: "vscode-uncommented.mcp.json"},
	{fixture: "cursor.mcp.json", name: ".cursor/mcp.json"},
}

// TestAnMCPConfigurationIsDraftedAlikeAtEverySurface imports each of
// mcpSurfaceConfigurations, naming no format, at the command under both
// deliveries, at the assistant's tool and at the session, and holds each to the
// shared operation's result: the command's commentary names the mcp format and
// its drafts are the shared ones, and the tool and the session write exactly the
// shared drafts, the Release included, byte for byte. A configuration written
// with comments is held, beforehand, to drafting exactly what its copy without
// them drafts.
//
// 🔴 PLANTED DEFECT: in detect (plugins/mcp-importer/main.go), write
// `Files: []string{"claude_desktop.json"}` in the Claude Desktop signature: no
// signature recognises the configuration, so the shared operation is refused
// asking for the format and the case fails before any surface is compared. Read
// the .vscode/mcp.json signature as mcpimport.Syntax instead, and the commented
// configuration fails the same way.
func TestAnMCPConfigurationIsDraftedAlikeAtEverySurface(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	for _, c := range mcpSurfaceConfigurations {
		t.Run(c.name, func(t *testing.T) {
			assertMCPConfigurationDraftedAlikeAtEverySurface(t, c.fixture, c.name, c.uncommented)
		})
	}
}

// assertMCPConfigurationDraftedAlikeAtEverySurface imports the committed fixture
// under name at every surface and holds each to the shared operation's result,
// after holding that result to the one the fixture uncommented drafts, where it
// names one.
func assertMCPConfigurationDraftedAlikeAtEverySurface(t *testing.T, fixture, name, uncommented string) {
	body := readCatalogFolderSource(t, filepath.Join(mcpFixtureDir, fixture))
	shared := sharedCatalogFolderImport(t, body, name)
	if uncommented != "" {
		copied := sharedCatalogFolderImport(t, readCatalogFolderSource(t, filepath.Join(mcpFixtureDir, uncommented)), name)
		if len(copied.Files) != len(shared.Files) {
			t.Fatalf("the configuration drafts %d files and its copy without comments %d", len(shared.Files),
				len(copied.Files))
		}
		for i, f := range shared.Files {
			if f.Path != copied.Files[i].Path || f.Content != copied.Files[i].Content {
				t.Errorf("the configuration's %s differs from its copy's %s:\n%s\nwant\n%s", f.Path,
					copied.Files[i].Path, f.Content, copied.Files[i].Content)
			}
		}
	}

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := importopstest.WriteSource(t, filepath.Join(t.TempDir(), filepath.FromSlash(name)), string(body))
		assertImportCommandDraftsTheSharedResult(t, root, external, "mcp", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		importopstest.WriteSource(t, filepath.Join(root, filepath.FromSlash(name)), string(body))
		answer := callAssistantTool(t, root, "import", map[string]any{"source": name})
		assertImportToolDrafted(t, answer)
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
		importopstest.WriteSource(t, filepath.Join(root, filepath.FromSlash(name)), string(body))
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
