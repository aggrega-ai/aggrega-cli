// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// a Compose application imported through the reference Compose importer laid in a
// catalog folder: `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`,
// each driven at the real command tree and held to the shared operation's own
// result over an identically laid workspace, so the three draft one application
// alike — the Aggregate, the Release beside it and the drafted model Component,
// or, over a catalog folder also holding the standard catalog's Docker Model
// Runner runtime, the reference to it in place of a drafted model.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// composeApplication is the committed compose-for-agents LangGraph application the
// surfaces import, under the name Compose reads it by: a database, services built
// from the project, an MCP gateway and a model.
const composeApplication = "../../internal/importops/testdata/compose/compose-for-agents/langgraph.compose.yaml"

// longFormModelsReport is what the shared operation reports of the LangGraph
// agent's long-form models entry, which names the variables its model's address
// and identifier reach it under.
const longFormModelsReport = "he service agent reads the address of the model qwen3 as MODEL_RUNNER_URL and its " +
	"identifier as MODEL_RUNNER_MODEL, the names its models entry gives them; no key of the draft holds either name"

// assertTheLongFormModelsEntryIsReported holds the shared operation's result to
// the report of the agent's long-form models entry: in the comment above the
// agent's connection to the model, and in the agent's decision. Every surface is
// then held to the shared drafts byte for byte, so each carries the comment.
func assertTheLongFormModelsEntryIsReported(t *testing.T, shared importops.Result) {
	t.Helper()
	var aggregate string
	for _, f := range shared.Files {
		if f.Path == "aggregate.yaml" {
			aggregate = f.Content
		}
	}
	if !strings.Contains(aggregate, "        # T"+longFormModelsReport+".\n        qwen3:\n") {
		t.Errorf("the shared Aggregate does not report the agent's long-form models entry above its connection:\n%s",
			aggregate)
	}
	reported := false
	for _, d := range shared.Decisions {
		reported = reported || (d.SourceNode.ID == "agent" && strings.HasSuffix(d.Rationale, "; t"+longFormModelsReport))
	}
	if !reported {
		t.Errorf("no decision of the shared operation reports the agent's long-form models entry: %+v", shared.Decisions)
	}
}

// TestAComposeApplicationIsDraftedAlikeAtEverySurface imports the LangGraph
// application, naming no format, at the command under both deliveries, at the
// assistant's tool and at the session, and holds each to the shared operation's
// result: the command's commentary names the compose format and its drafts are
// the shared ones, and the tool and the session write exactly the shared drafts,
// the Release included, byte for byte. The shared result reports the agent's
// long-form models entry, so every surface carries that report too.
//
// 🔴 PLANTED DEFECT: in detect (plugins/compose-importer/main.go), write
// `Files: composeimport.OverrideFileNames`: no signature recognises compose.yaml,
// so the shared operation is refused asking for the format and the case fails
// before any surface is compared.
func TestAComposeApplicationIsDraftedAlikeAtEverySurface(t *testing.T) {
	embeddedentities.Require(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, composeApplication)
	shared := sharedCatalogFolderImport(t, body, "compose.yaml")
	assertTheLongFormModelsEntryIsReported(t, shared)
	assertEverySurfaceDraftsTheSharedComposeResult(t, body, nil, shared)
}

// composeModelRuntimeApplication is the committed authored application whose two
// models the standard catalog's Docker Model Runner runtime serves: llm, with a
// context size and runtime flags, which app names in the short form, and embed,
// which worker names in the long form.
const composeModelRuntimeApplication = "../../internal/importops/testdata/compose/authored/model-runtime.compose.yaml"

// modelRuntimeEntities are the standard catalog's entities a workspace lays to
// hold the Docker Model Runner runtime: its Component, and the schema library its
// values read.
var modelRuntimeEntities = []string{
	"component/core/ai-model/docker-model-runner/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// assertTheModelsReferenceTheRuntime holds the shared operation's result over a
// catalog holding the runtime to what the import drafts there: each model an
// instance of core/ai-model/docker-model-runner, its context size written as
// context_window, an empty entry for it in the Release, no model Component file,
// and each model's decision a reference to the runtime. Every surface is then
// held to the shared drafts byte for byte, so each carries all of it.
func assertTheModelsReferenceTheRuntime(t *testing.T, shared importops.Result) {
	t.Helper()
	const runtime = "core/ai-model/docker-model-runner"
	files := map[string]string{}
	for _, f := range shared.Files {
		files[f.Path] = f.Content
		if strings.HasPrefix(f.Path, "components/") {
			t.Errorf("the shared operation drafted %s, but the catalog holds the runtime", f.Path)
		}
	}
	for _, want := range []string{
		"\n    llm:\n      type: " + runtime + "\n      values:\n        model: \"ai/smollm2\"\n        context_window: 4096\n",
		"\n    embed:\n      type: " + runtime + "\n      values:\n        model: \"ai/mxbai-embed-large\"\n",
	} {
		if !strings.Contains(files["aggregate.yaml"], want) {
			t.Errorf("the shared Aggregate does not hold\n%s\nin\n%s", want, files["aggregate.yaml"])
		}
	}
	if !strings.HasSuffix(files["release.yaml"], "\n    llm: {}\n    embed: {}\n") {
		t.Errorf("the shared Release does not place each model's instance:\n%s", files["release.yaml"])
	}
	referenced := map[string]bool{}
	for _, d := range shared.Decisions {
		if d.SourceNode.Type == "model" && d.Chosen != nil && d.Chosen.Target == runtime && d.Chosen.Mode == "reference" {
			referenced[d.SourceNode.ID] = true
		}
	}
	if !referenced["llm"] || !referenced["embed"] {
		t.Errorf("the shared decisions do not reference the runtime for llm and embed: %+v", shared.Decisions)
	}
}

// TestAComposeModelReferencesTheRuntimeAlikeAtEverySurface imports the authored
// model-runtime application, naming no format, over workspaces whose catalog
// folder holds the standard catalog's Docker Model Runner runtime: at the command
// under both deliveries, at the assistant's tool and at the session, each held to
// the shared operation's result, which references the runtime for both models.
// So the three surfaces draft the reference, the Release's entries and the
// decisions alike.
//
// 🔴 PLANTED DEFECT: in Import (plugins/internal/composeimport/composeimport.go),
// write `held := false`: the shared result drafts both models as Components of
// their own and the case fails on it before any surface is compared.
func TestAComposeModelReferencesTheRuntimeAlikeAtEverySurface(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, composeModelRuntimeApplication)
	shared := sharedCatalogFolderImportHolding(t, body, "compose.yaml", modelRuntimeEntities)
	assertTheModelsReferenceTheRuntime(t, shared)
	assertEverySurfaceDraftsTheSharedComposeResult(t, body, modelRuntimeEntities, shared)
}

// assertEverySurfaceDraftsTheSharedComposeResult imports body as compose.yaml,
// naming no format, at the command under both deliveries, at the assistant's tool
// and at the session, each over a workspace laid as the shared operation's was —
// its catalog folder holding the standard catalog's entities named — and holds
// each to the shared result: the command's commentary names the compose format
// and its drafts are the shared ones, and the tool and the session write exactly
// the shared drafts, the Release included, byte for byte.
func assertEverySurfaceDraftsTheSharedComposeResult(t *testing.T, body []byte, hubEntities []string,
	shared importops.Result) {
	t.Helper()
	const name = "compose.yaml"
	for _, dryRun := range []bool{true, false} {
		root := catalogFolderWorkspaceHolding(t, hubEntities)
		external := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, "compose", dryRun, shared)
	}

	t.Run("the assistant's tool", func(t *testing.T) {
		root := catalogFolderWorkspaceHolding(t, hubEntities)
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			t.Fatalf("seed the served workspace: %v", err)
		}
		answer := callAssistantTool(t, root, "import", map[string]any{"source": name})
		assertImportToolDrafted(t, answer)
		var payload struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		if payload.Format != "compose" {
			t.Errorf("the tool read the application as %q, want compose", payload.Format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root := catalogFolderWorkspaceHolding(t, hubEntities)
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
		if result.Halted || result.Format != "compose" {
			t.Fatalf("the session read the application as %q, halted %v: %s", result.Format, result.Halted,
				frame["result"])
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}
