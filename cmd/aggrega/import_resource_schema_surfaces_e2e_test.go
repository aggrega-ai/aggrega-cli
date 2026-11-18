// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a resource schema imported through the resource-schema importer the binary
// carries for its format: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`,
// each driven at the real command tree, naming no format, and held to the shared
// operation's own result over an identically laid workspace, so the three draft
// one Component and one Recipe alike, for each of the three formats.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// schemaSourceName is the name a surface is handed the case's schema under: the
// name a Bicep types file is recognised by, and the fixture's own otherwise.
func schemaSourceName(c schemaBuildCase) string {
	if c.format == "arm-schema" {
		return "types.json"
	}
	return filepath.Base(c.fixture)
}

// sharedSchemaImport is the expectation every surface is held to: the shared
// operation over the workspace at root, naming no format and asked to report
// rather than write. It refuses an expectation that would measure nothing.
func sharedSchemaImport(t *testing.T, root, source, name string) importops.Result {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    source,
		SourceName:    name,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import was refused, so there is no expectation: %v", err)
	}
	if res.Halted() || len(res.Files) == 0 {
		t.Fatalf("the shared import halted or drafted nothing: %+v", res.Diagnostics)
	}
	return res
}

// TestResourceSchemaImportSurfacesAgree imports each format's fixture, naming no
// format, at the command under both deliveries, at the assistant's tool and at
// the session, and holds each to the shared operation's result: the command's
// commentary names the format and its drafts are the shared ones, and the tool
// and the session write exactly the shared drafts, byte for byte.
func TestResourceSchemaImportSurfacesAgree(t *testing.T) {
	carrySchemaImportersIsolated(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	for _, c := range schemaBuildCases {
		body, err := os.ReadFile(schemaFixtures + c.fixture)
		if err != nil {
			t.Fatalf("read the fixture: %v", err)
		}
		name := schemaSourceName(c)

		t.Run(c.format+", the command", func(t *testing.T) {
			for _, dryRun := range []bool{true, false} {
				root := schemaWorkspaceForCase(t, c)
				external := filepath.Join(t.TempDir(), name)
				if err := os.WriteFile(external, body, 0o644); err != nil {
					t.Fatal(err)
				}
				shared := sharedSchemaImport(t, root, external, name)
				if shared.Format != c.format {
					t.Fatalf("the shared import read the source as %q, want %s", shared.Format, c.format)
				}
				assertImportCommandDraftsTheSharedResult(t, root, external, c.format, dryRun, shared)
			}
		})

		t.Run(c.format+", the assistant's tool", func(t *testing.T) {
			root := schemaWorkspaceForCase(t, c)
			seedSource(t, root, name, body)
			shared := sharedSchemaImport(t, root, filepath.Join(root, name), name)
			answer := callAssistantTool(t, root, "import", map[string]any{"source": name})
			assertImportToolDrafted(t, answer)
			var payload struct {
				Format  string   `json:"format"`
				Written []string `json:"written"`
			}
			if err := json.Unmarshal(answer.structured, &payload); err != nil {
				t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
			}
			if payload.Format != c.format {
				t.Errorf("the tool read the source as %q, want %s", payload.Format, c.format)
			}
			assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
		})

		t.Run(c.format+", the session", func(t *testing.T) {
			root := schemaWorkspaceForCase(t, c)
			seedSource(t, root, name, body)
			shared := sharedSchemaImport(t, root, filepath.Join(root, name), name)
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
			if result.Halted || result.Format != c.format {
				t.Fatalf("the session read the source as %q, halted %v: %s", result.Format, result.Halted, frame["result"])
			}
			assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
		})
	}
}
