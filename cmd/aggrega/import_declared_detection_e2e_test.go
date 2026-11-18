// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end coverage of how an import naming no format settles one: by the
// recognition signatures the importers in the workspace's catalog record, matched
// against the source without the host knowing what any format is. Every case is
// driven at the three real entry points that reach the import operation — `aggrega
// import`, the assistant's import tool through `aggrega mcp`, and the session's
// aggrega/import method through `aggrega serve` — over a workspace whose catalog
// folder holds real Plugin entities generated from the importers' built modules,
// so the signatures read are the ones those modules declare. Nothing points the
// import seam or stands in for the plugin resolution.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// everyAgentFlowFixture is every Langflow, Flowise and n8n export committed in
// this module, keyed by its format — the Langflow and Flowise ones by the format
// the host recognised them as before the formats were declared by their
// importers — the census a change to recognition is held to.
var everyAgentFlowFixture = []struct {
	format string
	path   string
}{
	{"flowise", "testdata/flowise-chatflow.json"},
	{"langflow", "testdata/langflow-flow.json"},
	{"langflow", "../../internal/compiler/importersocket/testdata/langflow-flow-b.json"},
	{"flowise", "../../internal/compiler/importersocket/testdata/flowise-flow.json"},
	{"langflow", "../../internal/compiler/importersocket/testdata/langflow-flow.json"},
	{"flowise", "../../internal/compiler/importersocket/testdata/real-exports/flowise/agentflowsv2/Agents Handoff.json"},
	{"flowise", "../../internal/compiler/importersocket/testdata/real-exports/flowise/chatflows/Multiple Documents QnA.json"},
	{"langflow", "../../internal/compiler/importersocket/testdata/real-exports/langflow/Basic Prompting.json"},
	{"langflow", "../../internal/compiler/importersocket/testdata/real-exports/langflow/Research Translation Loop.json"},
	{"langflow", "../../internal/compiler/importersocket/testdata/real-exports/langflow/Nvidia Remix.json"},
	{"langflow", "../../internal/compiler/importersocket/testdata/real-exports/langflow/Basic Prompting 1.6.0.json"},
	{"flowise", "../../internal/compiler/testdata/flow-definition-escape/exports/flowise-chatflow.json"},
	{"n8n", "../../internal/importops/testdata/n8n/agent-workflow.json"},
	{"n8n", "../../internal/importops/testdata/n8n/http-workflow.json"},
}

// toolImportAnswer is what the assistant's import tool answered one call with.
type toolImportAnswer struct {
	refused bool
	text    string
	format  string
}

// importThroughTheTool makes one import call at `aggrega mcp`, over root, with the
// arguments given, and returns the answer.
func importThroughTheTool(t *testing.T, root string, args map[string]any) toolImportAnswer {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"name": "import", "arguments": args})
	if err != nil {
		t.Fatalf("marshal the tool call: %v", err)
	}
	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", string(raw)))
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"mcp", "--workspace-root", root})
	if code := executeRoot(context.Background(), cmd); code != 0 {
		t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2)
	if answers[2].Error != nil {
		t.Fatalf("the import call was not dispatched: %s", answers[2].Error.Message)
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Format string `json:"format"`
		} `json:"structuredContent"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(answers[2].Result, &call); err != nil {
		t.Fatalf("decode the import answer: %v (body %s)", err, answers[2].Result)
	}
	answer := toolImportAnswer{refused: call.IsError, format: call.StructuredContent.Format}
	if len(call.Content) > 0 {
		answer.text = call.Content[0].Text
	}
	return answer
}

// sessionImportAnswer is what the session's aggrega/import method answered one
// request with: its refusal's message, or the format the result reports.
type sessionImportAnswer struct {
	refusal string
	format  string
}

// importThroughTheSession sends one aggrega/import request at `aggrega serve`, over
// root, with the parameters given, and returns the answer.
func importThroughTheSession(t *testing.T, root string, params map[string]any) sessionImportAnswer {
	t.Helper()
	params["workspaceRoot"] = root
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "aggrega/import", "params": params})
	if err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var stdin, stdout, stderr bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(request))
	stdin.Write(request)
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "--workspace-root", root})
	if code := executeRoot(context.Background(), cmd); code != 0 {
		t.Fatalf("aggrega serve exited %d\nstderr:\n%s", code, stderr.String())
	}
	frame := daemonFrameAnswering(t, stdout.Bytes(), 1)
	if raw, refused := frame["error"]; refused {
		var refusal struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &refusal); err != nil {
			t.Fatalf("decode the refusal: %v (%s)", err, raw)
		}
		return sessionImportAnswer{refusal: refusal.Message}
	}
	var result struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(frame["result"], &result); err != nil {
		t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
	}
	return sessionImportAnswer{format: result.Format}
}

// seedSource writes body into the workspace rooted at root under name, where the
// tool, which reads only inside the workspace, and the other two surfaces all
// reach it by the same name.
func seedSource(t *testing.T, root, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
		t.Fatalf("seed the source %s: %v", name, err)
	}
}

// commentaryFormat returns the format the command's commentary line names, or ""
// where it printed none.
func commentaryFormat(commentary string) string {
	const lead = "read as the "
	i := strings.Index(commentary, lead)
	if i < 0 {
		return ""
	}
	rest := commentary[i+len(lead):]
	format, _, _ := strings.Cut(rest, " format")
	return format
}

// TestEveryAgentFlowFixtureIsRecognisedByItsImportersDeclaration holds every
// Langflow, Flowise and n8n export committed in this module to its format — the
// Langflow and Flowise ones to the format the host recognised them as before the
// shapes moved into the importers' declarations — at the command, the tool and
// the session, with no format named. The folder's
// importer entities are generated from the reference importers' built modules, so
// what recognises each source is the signature each module declares and nothing
// the host spells.
func TestEveryAgentFlowFixtureIsRecognisedByItsImportersDeclaration(t *testing.T) {
	embeddedentities.Require(t)
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)

	for _, fx := range everyAgentFlowFixture {
		t.Run(filepath.Base(fx.path), func(t *testing.T) {
			root, _ := catalogFolderWorkspace(t, true)
			name := filepath.Base(fx.path)
			seedSource(t, root, name, readCatalogFolderSource(t, fx.path))

			_, _, commentary, process := runImportAtTheRoot(t, root, filepath.Join(root, name), "--dry-run")
			if got := commentaryFormat(commentary); got != fx.format {
				t.Errorf("the command read %s as %q; want %q\ncommentary:\n%s\nprocess error stream:\n%s",
					name, got, fx.format, commentary, process)
			}
			if answer := importThroughTheTool(t, root, map[string]any{"source": name, "dry_run": true}); answer.format != fx.format {
				t.Errorf("the tool read %s as %q; want %q: %s", name, answer.format, fx.format, answer.text)
			}
			answer := importThroughTheSession(t, root, map[string]any{"source": name, "outputDir": "drafts", "dryRun": true})
			if answer.format != fx.format {
				t.Errorf("the session read %s as %q; want %q: %s", name, answer.format, fx.format, answer.refusal)
			}
		})
	}
}

// TestAYAMLSourceIsRecognisedByAnImportersDeclarationWithNoHostChange is the case
// the host's own shapes never reached: an importer declaring a YAML signature —
// the demo importer, whose top-level kind must be the string DemoSource — beside a
// YAML parser in the catalog folder. A YAML source carrying that kind is read as
// the demo format at every surface, and drafted by the demo importer; nothing in
// the host names YAML, the demo format or the kind it asks for.
func TestAYAMLSourceIsRecognisedByAnImportersDeclarationWithNoHostChange(t *testing.T) {
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, folder := catalogFolderWorkspace(t, true)
	importopstest.LayDemoImporter(t, root, folder.Dir)
	importopstest.LayYAMLParser(t, root, folder.Dir)
	seedSource(t, root, "demo-source.yaml", []byte("kind: DemoSource\nname: example\n"))

	code, stdout, commentary, process := runImportAtTheRoot(t, root, filepath.Join(root, "demo-source.yaml"), "--dry-run")
	if code != 0 || commentaryFormat(commentary) != "demo" {
		t.Errorf("the command exited %d reading the source as %q; want the demo format\nstdout:\n%s\nprocess error stream:\n%s",
			code, commentaryFormat(commentary), stdout, process)
	}
	if answer := importThroughTheTool(t, root, map[string]any{"source": "demo-source.yaml", "dry_run": true}); answer.refused || answer.format != "demo" {
		t.Errorf("the tool answered refused=%v reading the source as %q; want the demo format: %s",
			answer.refused, answer.format, answer.text)
	}
	if answer := importThroughTheSession(t, root, map[string]any{
		"source": "demo-source.yaml", "outputDir": "drafts", "dryRun": true}); answer.format != "demo" {
		t.Errorf("the session read the source as %q; want the demo format: %s", answer.format, answer.refusal)
	}

	// The control: the same folder over a YAML source whose kind is another string
	// recognises nothing, so the case above is attributable to the predicate.
	seedSource(t, root, "other.yaml", []byte("kind: Other\n"))
	if code, _, _, process := runImportAtTheRoot(t, root, filepath.Join(root, "other.yaml"), "--dry-run"); code == 0 ||
		!strings.Contains(process, "E0350") || !strings.Contains(process, "no importer recognises the source") {
		t.Errorf("a YAML source of another kind was not refused as unrecognised (exit %d):\n%s", code, process)
	}
}

// TestASourceTwoFormatsRecogniseIsRefusedNamingBoth holds a source the signatures
// of two formats both match to the ambiguous-import-format refusal at every
// surface, naming both formats and asking for the format rather than choosing.
// The second importer is an entity recording a signature that also matches a
// Langflow export; it advertises a format of its own and has no module, which the
// refusal makes irrelevant — it is refused before any importer runs.
//
// 🔴 PLANTED DEFECT: in detectFormat (internal/importops/detect.go), return
// recognised[0] in place of the AmbiguousFormatError in the switch's default
// arm, and every surface reddens — the source is read as langflow, the first
// format the catalog lists, rather than refused.
func TestASourceTwoFormatsRecogniseIsRefusedNamingBoth(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, folder := catalogFolderWorkspace(t, true)
	importopstest.WriteImporterEntity(t, folder.Dir, "import-nested", []string{"nested-flow"},
		[]types.PluginDetectSignature{{
			Syntax: "json",
			Match:  []types.PluginPathMatch{{Path: "data.nodes", Is: types.IsArray}},
		}})
	seedSource(t, root, "flow.json", readCatalogFolderSource(t, catalogFolderImportSources[0].path))
	short, ok := cerrors.ShortCodeOf(importops.ErrAmbiguousImportFormat())
	if !ok {
		t.Fatal("the ambiguity condition carries no short code, so its refusal cannot be named")
	}
	message, _ := cerrors.MessageOf(importops.ErrAmbiguousImportFormat())

	code, stdout, _, process := runImportAtTheRoot(t, root, filepath.Join(root, "flow.json"), "--dry-run")
	if code == 0 {
		t.Fatalf("a source two formats recognise was imported:\n%s", stdout)
	}
	for _, want := range []string{"[" + short + "]", message, `"langflow"`, `"nested-flow"`, "--format"} {
		if !strings.Contains(process, want) {
			t.Errorf("the command's refusal does not carry %q:\n%s", want, process)
		}
	}

	answer := importThroughTheTool(t, root, map[string]any{"source": "flow.json", "dry_run": true})
	if !answer.refused {
		t.Fatalf("the tool imported a source two formats recognise: %s", answer.text)
	}
	for _, want := range []string{short, `"langflow"`, `"nested-flow"`, "'format'"} {
		if !strings.Contains(answer.text, want) {
			t.Errorf("the tool's refusal does not carry %q: %s", want, answer.text)
		}
	}

	refused := importThroughTheSession(t, root, map[string]any{"source": "flow.json", "outputDir": "drafts", "dryRun": true})
	for _, want := range []string{message, "langflow", "nested-flow", "send format"} {
		if !strings.Contains(refused.refusal, want) {
			t.Errorf("the session's refusal does not carry %q: %q", want, refused.refusal)
		}
	}
}

// TestASourceNoImporterRecognisesIsRefusedNamingTheAdvertisedFormats holds a source
// no signature matches to the unrecognised refusal at every surface, offering the
// formats the catalog's importers advertise — read off the catalog, in the order
// it lists them — as the ones to name.
func TestASourceNoImporterRecognisesIsRefusedNamingTheAdvertisedFormats(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, _ := catalogFolderWorkspace(t, true)
	seedSource(t, root, "mystery.json", []byte(`{"foo":"bar"}`))

	code, _, _, process := runImportAtTheRoot(t, root, filepath.Join(root, "mystery.json"), "--dry-run")
	if code == 0 || !strings.Contains(process,
		"(for example --format agent-framework, --format compose, --format crewai, --format dify, --format flowise, "+
			"--format google-adk, --format langflow, --format langgraph, --format mcp or --format n8n)") {
		t.Errorf("the command's refusal (exit %d) does not offer the advertised formats:\n%s", code, process)
	}
	if answer := importThroughTheTool(t, root, map[string]any{"source": "mystery.json"}); !answer.refused ||
		!strings.Contains(answer.text, `one of "agent-framework", "compose", "crewai", "dify", "flowise", "google-adk", "langflow", "langgraph", "mcp", "n8n"`) {
		t.Errorf("the tool's refusal (refused=%v) does not offer the advertised formats: %s", answer.refused, answer.text)
	}
	if answer := importThroughTheSession(t, root, map[string]any{"source": "mystery.json", "outputDir": "drafts"}); !strings.Contains(
		answer.refusal, "(for example agent-framework or compose or crewai or dify or flowise or google-adk or langflow or langgraph or mcp or n8n)") {
		t.Errorf("the session's refusal does not offer the advertised formats: %q", answer.refusal)
	}
}

// rewriteImporterDetect rewrites the recognition record of the entity laid at
// entityDir with edit, as an author editing the machine-owned field would.
func rewriteImporterDetect(t *testing.T, entityDir string, edit func(*types.PluginImporter)) {
	t.Helper()
	path := filepath.Join(entityDir, "plugin.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the entity: %v", err)
	}
	var entity types.Plugin
	if err := yaml.Unmarshal(raw, &entity); err != nil {
		t.Fatalf("parse the entity: %v", err)
	}
	if entity.Spec.Importer == nil || len(entity.Spec.Importer.Detect) == 0 {
		t.Fatalf("the generated entity records no recognition signature, so editing it measures nothing:\n%s", raw)
	}
	edit(entity.Spec.Importer)
	body, err := yaml.Marshal(entity)
	if err != nil {
		t.Fatalf("encode the entity: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write the entity: %v", err)
	}
}

// TestAnEntityWhoseSignaturesDifferFromItsImporterIsRefusedAtLoad holds the
// importer's load to the recognition record its entity carries: an entity whose
// record differs from what the importer's describe() reports is refused at every
// surface, naming the field, even with the format named so recognition does not
// read the record at all. An entity recording no signatures — the shape of one
// generated before the record existed — makes no claim and keeps loading.
//
// 🔴 PLANTED DEFECT: in CrossCheckContract (internal/wasm/capability_crosscheck.go),
// return nil in place of the crossCheckDetect call, and the three refused rows
// redden — each import drafts as though the record matched.
func TestAnEntityWhoseSignaturesDifferFromItsImporterIsRefusedAtLoad(t *testing.T) {
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	short, ok := cerrors.ShortCodeOf(wasm.ErrWasmContractMismatch())
	if !ok {
		t.Fatal("the contract-mismatch condition carries no short code, so its refusal cannot be named")
	}
	src := catalogFolderImportSources[0]

	root, folder := catalogFolderWorkspace(t, true)
	rewriteImporterDetect(t, folder.ImporterFor(t, src.format).EntityDir, func(imp *types.PluginImporter) {
		imp.Detect[0].Match[0].Is = types.IsPresent
	})
	seedSource(t, root, "flow.json", readCatalogFolderSource(t, src.path))

	code, stdout, _, process := runImportAtTheRoot(t, root, filepath.Join(root, "flow.json"), "--dry-run", "--format", src.format)
	if code == 0 {
		t.Fatalf("an importer whose entity's record differs from its describe() was run:\n%s", stdout)
	}
	for _, want := range []string{short, "importer.detect"} {
		if !strings.Contains(process, want) {
			t.Errorf("the command's refusal does not carry %q:\n%s", want, process)
		}
	}
	if answer := importThroughTheTool(t, root, map[string]any{"source": "flow.json", "format": src.format}); !answer.refused ||
		!strings.Contains(answer.text, "importer.detect") {
		t.Errorf("the tool's answer (refused=%v) does not refuse the load naming the field: %s", answer.refused, answer.text)
	}
	if answer := importThroughTheSession(t, root, map[string]any{
		"source": "flow.json", "format": src.format, "outputDir": "drafts", "dryRun": true}); !strings.Contains(
		answer.refusal, "importer.detect") {
		t.Errorf("the session's answer does not refuse the load naming the field: %q", answer.refusal)
	}

	// An entity recording no signatures makes no claim: the load runs the importer.
	root, folder = catalogFolderWorkspace(t, true)
	rewriteImporterDetect(t, folder.ImporterFor(t, src.format).EntityDir, func(imp *types.PluginImporter) {
		imp.Detect = nil
	})
	seedSource(t, root, "flow.json", readCatalogFolderSource(t, src.path))
	code, _, commentary, process := runImportAtTheRoot(t, root, filepath.Join(root, "flow.json"), "--dry-run", "--format", src.format)
	if code != 0 || commentaryFormat(commentary) != src.format {
		t.Errorf("an entity recording no signatures did not keep loading (exit %d):\n%s\n%s", code, commentary, process)
	}
}
