// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// an exported n8n workflow imported through the reference n8n importer laid in a
// catalog folder: `aggrega import`, the assistant's import tool through `aggrega
// mcp`, and the session's aggrega/import method through `aggrega serve`, each
// driven at the real command tree and held to the shared operation's own result
// over an identically laid workspace, so the three draft one workflow alike.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// n8nAgentWorkflow is the committed n8n workflow the surfaces import: an AI agent
// with a chat model, an MCP client tool, a vector store and an HTTP request tool,
// authored here to the shape n8n exports a workflow in.
const n8nAgentWorkflow = "../../internal/importops/testdata/n8n/agent-workflow.json"

// TestAnN8nWorkflowIsDraftedAlikeAtEverySurface imports the agent workflow, naming
// no format, at the command under both deliveries, at the assistant's tool and at
// the session, and holds each to the shared operation's result: the command's
// commentary names the n8n format and its drafts are the shared ones, and the tool
// and the session write exactly the shared drafts, byte for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/n8n-importer/format.go), write
// `{Path: "connections", Is: "array"}` in place of `Is: "object"`: no signature
// recognises the workflow, so the shared operation is refused asking for the
// format and the case fails before any surface is compared.
func TestAnN8nWorkflowIsDraftedAlikeAtEverySurface(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, n8nAgentWorkflow)
	name := filepath.Base(n8nAgentWorkflow)
	shared := sharedCatalogFolderImport(t, body, name)

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, "n8n", dryRun, shared)
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
		if payload.Format != "n8n" {
			t.Errorf("the tool read the workflow as %q, want n8n", payload.Format)
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
		if result.Halted || result.Format != "n8n" {
			t.Fatalf("the session read the workflow as %q, halted %v: %s", result.Format, result.Halted, frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
