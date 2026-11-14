// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a LangGraph application directory imported through the reference LangGraph
// importer laid in a catalog folder: `aggrega import`, the assistant's import tool
// through `aggrega mcp`, and the session's aggrega/import method through `aggrega
// serve`, each driven at the real command tree and held to the shared
// operation's own result over an identically laid workspace, so the three draft
// one application alike — one LangGraph agent per graph, wired to its datastore,
// and the environment file beside it, which a directory never hands the
// importer, named as not handed.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// langgraphApplicationFixture is the committed application the surfaces import:
// a configuration whose checkpointer names MongoDB and whose env names the
// environment file beside it.
const langgraphApplicationFixture = "../../internal/importops/testdata/langgraph/envfile"

// layLangGraphApplication lays the committed application down at dir, every
// file at its place in the application.
func layLangGraphApplication(t *testing.T, dir string) {
	t.Helper()
	layCommittedDirectory(t, langgraphApplicationFixture, dir)
}

// sharedLangGraphImport is the expectation every surface is held to: the shared
// operation over a workspace laid out like the one under test, reading the
// application laid in a directory of its own, asked to report rather than write.
// It refuses an expectation that would measure nothing.
func sharedLangGraphImport(t *testing.T) importops.Result {
	t.Helper()
	root, _ := catalogFolderWorkspace(t, true)
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "application")
	layLangGraphApplication(t, dir)
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    dir,
		SourceName:    "application",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import of the application was refused, so there is no expectation: %v", err)
	}
	if res.Halted() || len(res.Files) == 0 || res.Format != "langgraph" {
		t.Fatalf("the shared import read the application as %q, halted or drafted nothing, so the comparison would "+
			"measure nothing: %+v", res.Format, res.Diagnostics)
	}
	return res
}

// TestALangGraphApplicationIsDraftedAlikeAtEverySurface imports the envfile
// application's directory, naming no format, at the command under both
// deliveries, at the assistant's tool and at the session, and holds each to the
// shared operation's result: the command's commentary names the langgraph format
// and its drafts are the shared ones, and the tool and the session write exactly
// the shared drafts, byte for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/langgraph-importer/main.go), write
// `{Path: "graphs", Is: "array"}` in the langgraph.json signature: no signature
// recognises the application, so the shared operation is refused asking for the
// format and the case fails before any surface is compared.
func TestALangGraphApplicationIsDraftedAlikeAtEverySurface(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	shared := sharedLangGraphImport(t)

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), "application")
		layLangGraphApplication(t, external)
		assertImportCommandDraftsTheSharedResult(t, root, external, "langgraph", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		layLangGraphApplication(t, filepath.Join(root, "application"))
		answer := callAssistantTool(t, root, "import", map[string]any{"source": "application"})
		assertImportToolDrafted(t, answer)
		var payload struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		if payload.Format != "langgraph" {
			t.Errorf("the tool read the application as %q, want langgraph", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		layLangGraphApplication(t, filepath.Join(root, "application"))
		frame, stderr := callSession(t, root, "aggrega/import", map[string]any{"source": "application",
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
		if result.Halted || result.Format != "langgraph" {
			t.Fatalf("the session read the application as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
