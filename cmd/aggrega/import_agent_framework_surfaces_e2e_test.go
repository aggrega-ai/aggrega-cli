// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// an Agent Framework declarative agent imported through the reference Agent
// Framework importer laid in a catalog folder: `aggrega import`, the assistant's
// import tool through `aggrega mcp`, and the session's aggrega/import method
// through `aggrega serve`, each driven at the real command tree and held to the
// shared operation's own result over an identically laid workspace, so the three
// draft one declarative agent alike — one Agent Framework agent, wired to the
// OpenAI model it names, its MCP server named in a comment, and the Release beside
// it carrying the model's endpoint and, in a comment, the server's url.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// agentFrameworkFixture is the committed declarative agent the surfaces import,
// and agentFrameworkSourceName the name each is handed it under: one its author
// chose, since Agent Framework loads a declarative agent from whatever file its
// application names.
const (
	agentFrameworkFixture    = "../../internal/importops/testdata/agent-framework/learn-agent.yaml"
	agentFrameworkSourceName = "agents/learn.yaml"
)

// TestAnAgentFrameworkDeclarativeAgentIsDraftedAlikeAtEverySurface imports the
// learn agent, naming no format, at the command under both deliveries, at the
// assistant's tool and at the session, and holds each to the shared operation's
// result: the command's commentary names the agent-framework format and its
// drafts are the shared ones, and the tool and the session write exactly the
// shared drafts, the Release included, byte for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/agent-framework-importer/main.go), write
// `Equals: "Agent"`: no signature recognises the declarative agent, so the shared
// operation is refused asking for the format and the case fails before any
// surface is compared.
func TestAnAgentFrameworkDeclarativeAgentIsDraftedAlikeAtEverySurface(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, agentFrameworkFixture)
	shared := sharedCatalogFolderImport(t, body, agentFrameworkSourceName)
	if shared.Format != "agent-framework" {
		t.Fatalf("the shared import read the declarative agent as %q, want agent-framework", shared.Format)
	}

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := importopstest.WriteSource(t, filepath.Join(t.TempDir(), filepath.FromSlash(agentFrameworkSourceName)),
			string(body))
		assertImportCommandDraftsTheSharedResult(t, root, external, "agent-framework", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		importopstest.WriteSource(t, filepath.Join(root, filepath.FromSlash(agentFrameworkSourceName)), string(body))
		answer := callAssistantTool(t, root, "import", map[string]any{"source": agentFrameworkSourceName})
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
		if payload.Format != "agent-framework" {
			t.Errorf("the tool read the declarative agent as %q, want agent-framework", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		importopstest.WriteSource(t, filepath.Join(root, filepath.FromSlash(agentFrameworkSourceName)), string(body))
		frame, stderr := callSession(t, root, "aggrega/import", map[string]any{"source": agentFrameworkSourceName,
			"outputDir": "drafts"})
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
		if result.Halted || result.Format != "agent-framework" {
			t.Fatalf("the session read the declarative agent as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
