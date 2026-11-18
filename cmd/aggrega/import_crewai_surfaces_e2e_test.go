// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a CrewAI project directory imported through the reference CrewAI importer laid
// in a catalog folder: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`,
// each driven at the real command tree and held to the shared operation's own
// result over an identically laid workspace, so the three draft one project
// alike — one CrewAI agent per declaration, wired to its model.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// crewaiProjectFixture is the committed project in CrewAI's JSON-first layout the
// surfaces import, written with comments and trailing commas.
const crewaiProjectFixture = "../../internal/importops/testdata/crewai/jsonc"

// layCrewAIProject lays the committed project down at dir, every file at its
// place in the project.
func layCrewAIProject(t *testing.T, dir string) {
	t.Helper()
	layCommittedDirectory(t, crewaiProjectFixture, dir)
}

// sharedCrewAIImport is the expectation every surface is held to: the shared
// operation over a workspace laid out like the one under test, reading the
// project laid in a directory of its own, asked to report rather than write. It
// refuses an expectation that would measure nothing.
func sharedCrewAIImport(t *testing.T) importops.Result {
	t.Helper()
	root, _ := catalogFolderWorkspace(t, true)
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "project")
	layCrewAIProject(t, dir)
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    dir,
		SourceName:    "project",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import of the project was refused, so there is no expectation: %v", err)
	}
	if res.Halted() || len(res.Files) == 0 || res.Format != "crewai" {
		t.Fatalf("the shared import read the project as %q, halted or drafted nothing, so the comparison would "+
			"measure nothing: %+v", res.Format, res.Diagnostics)
	}
	return res
}

// TestACrewAIProjectIsDraftedAlikeAtEverySurface imports the JSON-first project's
// directory, naming no format, at the command under both deliveries, at the
// assistant's tool and at the session, and holds each to the shared operation's
// result: the command's commentary names the crewai format and its drafts are the
// shared ones, and the tool and the session write exactly the shared drafts, byte
// for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/crewai-importer/main.go), write
// `{Path: "agents", Is: "object"}` in the crew.jsonc signature and
// `{Path: "role", Is: "absent"}` in the agents/*.jsonc one: no signature
// recognises the project, so the shared operation is refused asking for the
// format and the case fails before any surface is compared.
func TestACrewAIProjectIsDraftedAlikeAtEverySurface(t *testing.T) {
	embeddedentities.Require(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	shared := sharedCrewAIImport(t)

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), "project")
		layCrewAIProject(t, external)
		assertImportCommandDraftsTheSharedResult(t, root, external, "crewai", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		layCrewAIProject(t, filepath.Join(root, "project"))
		answer := callAssistantTool(t, root, "import", map[string]any{"source": "project"})
		assertImportToolDrafted(t, answer)
		var payload struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		if payload.Format != "crewai" {
			t.Errorf("the tool read the project as %q, want crewai", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		layCrewAIProject(t, filepath.Join(root, "project"))
		frame, stderr := callSession(t, root, "aggrega/import", map[string]any{"source": "project", "outputDir": "drafts"})
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
		if result.Halted || result.Format != "crewai" {
			t.Fatalf("the session read the project as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
