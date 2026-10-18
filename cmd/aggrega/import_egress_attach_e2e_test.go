// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// the attach_egress_allowlist importer setting: `aggrega import`, the assistant's
// import tool through `aggrega mcp`, and the session's aggrega/import method
// through `aggrega serve`, each driven at the real command tree over a workspace
// whose importer attachments hand the setting true. No surface names the setting
// in its own arguments: the attachment's settings are the one place it is read.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// egressAttachingWorkspace declares an isolated workspace whose conventional
// catalog folder holds the importers and the parser with their modules, each
// importer attached with attach_egress_allowlist true, and returns its root. A
// case calls isolateMachineConfiguration first.
func egressAttachingWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	importopstest.LayAttachedCatalogFolder(t, root, filepath.Join(root, workspaceconfig.ConventionalCatalogDir), true,
		map[string]any{"attach_egress_allowlist": true})
	return resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}).Root()
}

// egressAttachmentBlock is what the drafted platform's instance opens with when
// the setting is true.
const egressAttachmentBlock = "      features:\n" +
	"        # Lets langflow connect only to the components it is wired to, through a network policy; compose has no " +
	"network policy, so a compose build halts naming it.\n" +
	"        egress-allowlist:\n" +
	"          type: core/egress-allowlist\n"

// TestEveryImportSurfaceAttachesTheOutboundOnlyFeatureAlike drives each surface
// over a workspace whose importer attachments hand attach_egress_allowlist true,
// with the committed Langflow fixture, and holds each to the shared operation's
// result over a workspace laid out alike: the same drafts, byte for byte, in which
// the platform carries core/egress-allowlist, and the same report naming the
// attachment, which the assistant's tool and the session answer among their
// diagnostics.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixtures from its source: in
// parseSettings (aggrega-pdk/go/pkg/importer/settings.go), add
// `s.AttachEgressAllowlist = false` before `return s`: the shared expectation
// carries no attachment and this test fails before any surface is driven.
func TestEveryImportSurfaceAttachesTheOutboundOnlyFeatureAlike(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	src := catalogFolderImportSources[0]
	if src.format != "langflow" {
		t.Fatalf("the first committed fixture is %s; this case reads the Langflow platform's key", src.format)
	}
	body := readCatalogFolderSource(t, src.path)

	sharedRoot := egressAttachingWorkspace(t)
	rw, err := workspaceconfig.Resolve(sharedRoot, sharedRoot)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	shared, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    importopstest.WriteSource(t, filepath.Join(t.TempDir(), "flow.json"), string(body)),
		SourceName:    "flow.json",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import was refused, so there is no expectation: %v", err)
	}
	attached := false
	for _, f := range shared.Files {
		attached = attached || (f.Path == "aggregate.yaml" && strings.Contains(f.Content, egressAttachmentBlock))
	}
	reported := slices.ContainsFunc(shared.Diagnostics, func(d abi.Diagnostic) bool {
		return d.Code == "feature-attached"
	})
	if shared.Halted() || !attached || !reported {
		t.Fatalf("the shared import halted, drafted no attachment or reported none, so the comparison would measure "+
			"nothing: attached %v, reported %v, %+v", attached, reported, shared.Diagnostics)
	}

	t.Run("the command", func(t *testing.T) {
		root := egressAttachingWorkspace(t)
		external := filepath.Join(t.TempDir(), "flow.json")
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, src.format, false, shared)
	})

	codes := func(raw json.RawMessage) []string {
		var answer struct {
			Diagnostics []struct {
				Code string `json:"code"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatalf("decode the answer's diagnostics: %v (body %s)", err, raw)
		}
		var out []string
		for _, d := range answer.Diagnostics {
			out = append(out, d.Code)
		}
		return out
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root := egressAttachingWorkspace(t)
		seedSource(t, root, "flow.json", body)
		answer := callAssistantTool(t, root, "import", map[string]any{"source": "flow.json"})
		if answer.isError {
			t.Fatalf("the import was refused: %s", answer.text)
		}
		var payload struct {
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
		if got := codes(answer.structured); !slices.Contains(got, "feature-attached") {
			t.Errorf("the tool answered the diagnostics %v, none of them feature-attached", got)
		}
	})

	t.Run("the session", func(t *testing.T) {
		root := egressAttachingWorkspace(t)
		seedSource(t, root, "flow.json", body)
		frame, stderr := callSession(t, root, "aggrega/import", map[string]any{"source": "flow.json", "outputDir": "drafts"})
		if raw, refused := frame["error"]; refused {
			t.Fatalf("aggrega/import was refused: %s\nstderr:\n%s", raw, stderr)
		}
		var result struct {
			Written []string `json:"written"`
			Halted  bool     `json:"halted"`
		}
		if err := json.Unmarshal(frame["result"], &result); err != nil {
			t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
		}
		if result.Halted {
			t.Fatalf("the importer halted: %s", frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
		if got := codes(frame["result"]); !slices.Contains(got, "feature-attached") {
			t.Errorf("the session answered the diagnostics %v, none of them feature-attached", got)
		}
	})
}
