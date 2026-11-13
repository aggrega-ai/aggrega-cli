// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a credential value an import carries as written: `aggrega import`, the
// assistant's import tool through `aggrega mcp`, and the session's aggrega/import
// method through `aggrega serve`, each driven at the real command tree over a
// workspace whose importer attachments hand the importers no settings, so the
// empty_credentials setting is at its default and writes nothing empty.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// carriedKey is the key-shaped value planted in the committed Langflow fixture's
// field marked password.
const carriedKey = "sk-plantedSURFACEcarried0123456789ab"

// carriedKeyField is the fixture's field marked password, empty as committed.
const carriedKeyField = `"api_key": { "type": "str", "value": "", "password": true }`

// TestEveryImportSurfaceReportsACarriedCredentialValueAlike plants carriedKey in
// the field the committed Langflow fixture marks as a credential and drives each
// surface over a workspace whose attachments hand no settings, holding each to
// the shared operation's result over a workspace laid out alike: the same drafts,
// byte for byte, carrying the value as written, and the same credential-carried
// warning naming the node and the field — on the command's error stream as
// `warn: <message>`, and among the diagnostics the assistant's tool and the
// session answer, with its code. No surface's report holds the value.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixtures from its source: in
// carryCredentials (aggrega-pdk/go/pkg/importer/credentials.go), replace
// `case verdicts[i] == carried && holdsValue(tree, c):` with `case false:`: the
// shared expectation carries no warning and this test fails before any surface is
// driven.
func TestEveryImportSurfaceReportsACarriedCredentialValueAlike(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	src := catalogFolderImportSources[0]
	if src.format != "langflow" {
		t.Fatalf("the first committed fixture is %s; this case plants a Langflow field", src.format)
	}
	committed := string(readCatalogFolderSource(t, src.path))
	if strings.Count(committed, carriedKeyField) != 1 {
		t.Fatalf("the committed fixture does not hold %s exactly once", carriedKeyField)
	}
	body := []byte(strings.Replace(committed, carriedKeyField,
		`"api_key": { "type": "str", "value": "`+carriedKey+`", "password": true }`, 1))

	sharedRoot, _ := catalogFolderWorkspace(t, true)
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
	var want []abi.Diagnostic
	for _, d := range shared.Diagnostics {
		if d.Code == "credential-carried" {
			want = append(want, d)
		}
	}
	carriedInDraft := false
	for _, f := range shared.Files {
		carriedInDraft = carriedInDraft || (f.Path == "aggregate.yaml" && strings.Contains(f.Content, carriedKey))
	}
	wantMessage := "the value of api_key of node OpenAIModel-a1b2 is carried as written in the flow the draft carries: " +
		"its format marks the field as a credential, and setting empty_credentials to marked or all writes it empty"
	if shared.Halted() || !carriedInDraft || len(want) != 1 || want[0].Message != wantMessage ||
		want[0].SourceRef != "OpenAIModel-a1b2" {
		t.Fatalf("the shared import halted, carried no value or reported it otherwise, so the comparison would measure "+
			"nothing: carried %v, %+v", carriedInDraft, shared.Diagnostics)
	}

	t.Run("the command", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
		external := filepath.Join(t.TempDir(), "flow.json")
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, src.format, false, shared)
	})

	// answered is a diagnostic as a surface's answer carries it.
	type answered struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Code     string `json:"code"`
	}
	wantAnswered := []answered{{Severity: want[0].Severity, Message: want[0].Message, Code: want[0].Code}}
	carried := func(raw json.RawMessage) []answered {
		var answer struct {
			Diagnostics json.RawMessage `json:"diagnostics"`
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatalf("decode the answer: %v (body %s)", err, raw)
		}
		if strings.Contains(string(answer.Diagnostics), carriedKey) {
			t.Errorf("the answer's diagnostics hold the planted value: %s", answer.Diagnostics)
		}
		var diagnostics []answered
		if err := json.Unmarshal(answer.Diagnostics, &diagnostics); err != nil {
			t.Fatalf("decode the answer's diagnostics: %v (body %s)", err, answer.Diagnostics)
		}
		var out []answered
		for _, d := range diagnostics {
			if d.Code == "credential-carried" {
				out = append(out, d)
			}
		}
		return out
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
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
		if got := carried(answer.structured); !reflect.DeepEqual(got, wantAnswered) {
			t.Errorf("the tool answered the credential-carried diagnostics %+v, want %+v", got, wantAnswered)
		}
	})

	t.Run("the session", func(t *testing.T) {
		root, _ := catalogFolderWorkspace(t, true)
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
		if got := carried(frame["result"]); !reflect.DeepEqual(got, wantAnswered) {
			t.Errorf("the session answered the credential-carried diagnostics %+v, want %+v", got, wantAnswered)
		}
	})
}
