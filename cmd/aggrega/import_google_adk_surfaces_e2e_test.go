// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a Google ADK application directory imported through the reference Google ADK
// importer laid in a catalog folder: `aggrega import`, the assistant's import tool
// through `aggrega mcp`, and the session's aggrega/import method through `aggrega
// serve`, each driven at the real command tree and held to the shared
// operation's own result over an identically laid workspace, so the three draft
// one application alike — one ADK agent for its tree, read from the
// root_agent.yaml the directory hands, wired to the Gemini model it names, the
// sub-agents' configurations it names, which a directory never hands, named as
// not handed, and its tools reported.

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// googleADKApplicationFixture is the committed application the surfaces import:
// a root configuration naming two sub-agents' configurations and one built by
// code, beside the application's code and a file no configuration names.
const googleADKApplicationFixture = "../../internal/importops/testdata/google-adk/tutor"

// layGoogleADKApplication lays the committed application down at dir, every file
// at its place in the application.
func layGoogleADKApplication(t *testing.T, dir string) {
	t.Helper()
	layCommittedDirectory(t, googleADKApplicationFixture, dir)
}

// layCommittedDirectory lays the committed fixture directory src down at dir,
// every file at its place beneath src.
func layCommittedDirectory(t *testing.T, src, dir string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		importopstest.WriteSource(t, filepath.Join(dir, rel), string(readCatalogFolderSource(t, path)))
		return nil
	})
	if err != nil {
		t.Fatalf("lay the committed directory %s down: %v", src, err)
	}
}

// sharedGoogleADKImport is the expectation every surface is held to: the shared
// operation over a workspace laid out like the one under test, reading the
// application laid in a directory of its own, asked to report rather than write.
// It refuses an expectation that would measure nothing.
func sharedGoogleADKImport(t *testing.T) importops.Result {
	t.Helper()
	root, _ := catalogFolderWorkspace(t, true)
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "application")
	layGoogleADKApplication(t, dir)
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    dir,
		SourceName:    "application",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import of the application was refused, so there is no expectation: %v", err)
	}
	if res.Halted() || len(res.Files) == 0 || res.Format != "google-adk" {
		t.Fatalf("the shared import read the application as %q, halted or drafted nothing, so the comparison would "+
			"measure nothing: %+v", res.Format, res.Diagnostics)
	}
	return res
}

// TestAGoogleADKApplicationIsDraftedAlikeAtEverySurface imports the tutor
// application's directory, naming no format, at the command under both
// deliveries, at the assistant's tool and at the session, and holds each to the
// shared operation's result: the command's commentary names the google-adk
// format and its drafts are the shared ones, and the tool and the session write
// exactly the shared drafts, byte for byte.
//
// 🔴 PLANTED DEFECT: in detect (plugins/google-adk-importer/main.go), write
// `{Path: "instruction", Is: "absent"}` in the first signature: no signature
// recognises the application, so the shared operation is refused asking for the
// format and the case fails before any surface is compared.
func TestAGoogleADKApplicationIsDraftedAlikeAtEverySurface(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	shared := sharedGoogleADKImport(t)

	for _, dryRun := range []bool{true, false} {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), "application")
		layGoogleADKApplication(t, external)
		assertImportCommandDraftsTheSharedResult(t, root, external, "google-adk", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		layGoogleADKApplication(t, filepath.Join(root, "application"))
		answer := callAssistantTool(t, root, "import", map[string]any{"source": "application"})
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
		if payload.Format != "google-adk" {
			t.Errorf("the tool read the application as %q, want google-adk", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		layGoogleADKApplication(t, filepath.Join(root, "application"))
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
		if result.Halted || result.Format != "google-adk" {
			t.Fatalf("the session read the application as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
