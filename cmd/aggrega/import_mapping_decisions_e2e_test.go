// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage of the importer's mapping decisions on every surface that
// reaches the import operation: `aggrega import` writes one line per decision on
// its error stream, the assistant's import tool through `aggrega mcp` carries them
// in its structured answer and its text, and the session's aggrega/import method
// through `aggrega serve` carries them in its result. Each is driven at the real
// command tree over a workspace whose catalog folder holds the reference importers
// as real Plugin entities with their modules beside them — nothing points the
// import seam or stands in for the plugin resolution — and two catalog Components
// the Langflow fixture's nodes are scored against, so a decision carries a score
// to compare.
//
// Every surface, and the shared operation itself, is held to the decisions this
// import returns as the shared case manifest writes them down, and the three
// surfaces are held to one another through it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// decisionLineCasesFile is the case manifest the mapping-decision line is held to
// on both sides of the language boundary, relative to this package. Its Langflow
// entries are the decisions the Langflow fixture imports to over the workspace
// this file lays, and the test below requires the live answer to equal them —
// which is what lets the editor's own spec render real decisions from it.
const decisionLineCasesFile = "../../internal/importops/testdata/decision_line_cases.json"

// mappingDecisionComponents are the Components the workspace's catalog folder
// holds beside the importers: a model Component the fixture's OpenAI node shares a
// category, labels and a value key with, and a store it resembles on no signal but
// a name. Their name, version and the per-version path each is laid at follow the
// catalog layout.
var mappingDecisionComponents = []struct{ name, version, body string }{
	{"acme/ai-model/internal-llm", "1.2.0", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/ai-model/internal-llm
  version: 1.2.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: ai-model
    model.aggrega.ai/capability: generative
    model.aggrega.ai/api: openai
    component.aggrega.ai/hosting: self-hosted
spec:
  schema:
    values:
      model: { type: string }
      temperature: { type: number }
      max_tokens: { type: integer }
    provides:
      endpoint: { type: string }
      model: { type: string }
`},
	{"acme/datastore/kv", "0.3.0", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/datastore/kv
  version: 0.3.0
  labels:
    component.aggrega.ai/type: resource
    component.aggrega.ai/category: datastore
spec:
  schema:
    values:
      ttl: { type: integer }
      size: { type: string }
`},
}

// mappingDecisionWorkspace lays the catalog folder workspace with the Components
// beside the importers, seeds the Langflow fixture at its root as flow.json, and
// returns the root and the source's bytes. A case calls
// isolateMachineConfiguration first.
func mappingDecisionWorkspace(t *testing.T) (string, []byte) {
	t.Helper()
	root, folder := catalogFolderWorkspace(t, true)
	for _, c := range mappingDecisionComponents {
		path := filepath.Join(folder.Dir, "component", filepath.FromSlash(c.name), c.version, "component.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", c.name, err)
		}
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatalf("write %s: %v", c.name, err)
		}
	}
	source := readCatalogFolderSource(t, catalogFolderImportSources[0].path)
	if err := os.WriteFile(filepath.Join(root, "flow.json"), source, 0o644); err != nil {
		t.Fatalf("seed the source: %v", err)
	}
	return root, source
}

// decisionFacts is what every surface is compared on for one decision: the node,
// the choice, its tier, and the top candidate's score as the surface wrote it.
// score is empty where the decision weighed no candidate.
type decisionFacts struct {
	id, nodeType, target, mode, tier, score string
}

// factsOf reads the facts off a decision in the importer's own type. The score is
// the highest among its candidates, written as the shortest decimal that reads
// back as the same number — the form each protocol answer encodes it in.
func factsOf(d abi.ImportDecision) decisionFacts {
	f := decisionFacts{id: d.SourceNode.ID, nodeType: d.SourceNode.Type}
	if d.Chosen != nil {
		f.target, f.mode, f.tier = d.Chosen.Target, d.Chosen.Mode, d.Chosen.Tier
	}
	if len(d.Candidates) > 0 {
		top := d.Candidates[0].Score
		for _, c := range d.Candidates[1:] {
			top = max(top, c.Score)
		}
		f.score = strconv.FormatFloat(top, 'f', -1, 64)
	}
	return f
}

// sameDecisions compares two decision lists by the importer's own encoding of
// them, so a list decoded with an absent candidate list and one carrying an empty
// one — the same decision on the wire — compare equal, and every value the
// importer wrote is compared.
func sameDecisions(t *testing.T, a, b []abi.ImportDecision) bool {
	t.Helper()
	encode := func(list []abi.ImportDecision) string {
		raw, err := json.Marshal(append([]abi.ImportDecision{}, list...))
		if err != nil {
			t.Fatalf("encode decisions for comparison: %v", err)
		}
		return string(raw)
	}
	return encode(a) == encode(b)
}

// mappedLine reads the facts back off a line the command or the tool's text
// wrote. It is written here, independently of the formatter, so a line that
// re-worded or dropped a fact fails to match rather than agreeing with itself.
var mappedLine = regexp.MustCompile(`^mapped (\S+) \((\S+)\) to (\S+) as a (\S+), (\S+)(?: at (\S+))?$`)

func factsOfLine(t *testing.T, line string) decisionFacts {
	t.Helper()
	m := mappedLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("%q is not a decision line of the documented form", line)
	}
	return decisionFacts{id: m[1], nodeType: m[2], target: m[3], mode: m[4], tier: m[5], score: m[6]}
}

// toolDecision is one decision as the assistant's tool answers it, keyed as that
// surface keys it. It is decoded with unknown keys refused, so a key outside the
// approved set fails rather than being ignored.
type toolDecision struct {
	SourceNode struct {
		ID            string `json:"id"`
		Type          string `json:"type"`
		ParamsSummary string `json:"params_summary"`
	} `json:"source_node"`
	Candidates []struct {
		Component string  `json:"component"`
		Score     float64 `json:"score"`
		Tier      string  `json:"tier"`
		Signals   struct {
			Table        float64 `json:"table"`
			Category     float64 `json:"category"`
			DomainLabels float64 `json:"domain_labels"`
			Shape        float64 `json:"shape"`
			Name         float64 `json:"name"`
		} `json:"signals"`
	} `json:"candidates"`
	Chosen *struct {
		Target string `json:"target"`
		Mode   string `json:"mode"`
		Tier   string `json:"tier"`
	} `json:"chosen"`
	Rationale string `json:"rationale"`
	SwapHint  string `json:"swap_hint"`
}

// asImporterDecision restates a tool decision in the importer's own type, field
// by field, so it can be compared whole with the expected decisions.
func (d toolDecision) asImporterDecision() abi.ImportDecision {
	out := abi.ImportDecision{
		SourceNode: abi.DecisionSourceNode{ID: d.SourceNode.ID, Type: d.SourceNode.Type,
			ParamsSummary: d.SourceNode.ParamsSummary},
		Rationale: d.Rationale,
		SwapHint:  d.SwapHint,
	}
	for _, c := range d.Candidates {
		out.Candidates = append(out.Candidates, abi.ImportCandidate{
			ComponentName: c.Component, Score: c.Score, Tier: c.Tier,
			Signals: abi.CandidateSignals{Table: c.Signals.Table, Category: c.Signals.Category,
				DomainLabels: c.Signals.DomainLabels, Shape: c.Signals.Shape, Name: c.Signals.Name},
		})
	}
	if d.Chosen != nil {
		out.Chosen = &abi.DecisionChoice{Target: d.Chosen.Target, Mode: d.Chosen.Mode, Tier: d.Chosen.Tier}
	}
	return out
}

// strictDecode decodes raw into v, refusing a key v does not declare.
func strictDecode(t *testing.T, what string, raw []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s: %v\n%s", what, err, raw)
	}
}

// TestTheMappingDecisionsReachEverySurfaceAlike imports the Langflow fixture
// through the command, the assistant's tool and the session, each under the
// reporting delivery, and holds every surface's decisions to one expectation and
// so to one another: the same nodes, in the importer's order, with the same
// choice, tier and top score on every surface.
//
// The expectation is the manifest's Langflow entries — the decisions this import
// returns, written down — rather than the shared operation's result, so it is
// independent of the code under test and each surface is held to it on its own.
// The shared operation is held to it too, which is what keeps the manifest true:
// it is what the editor's own spec renders its channel lines from.
//
//   - The command writes exactly the preamble, the diagnostics and one line per
//     decision on its error stream — each line the manifest's written literal —
//     and its standard output is exactly the reported drafts, nothing else, so a
//     caller parsing it is not disturbed.
//   - The tool's structured answer carries every decision whole under the
//     approved snake_case keys, a candidate's Component under `component`, and
//     no other key; its text answer carries the same lines after the diagnostics.
//   - The session's result carries every decision whole, exactly as the importer
//     returned it, and the result validates against the published wire contract.
//
// 🔴 PLANTED DEFECT: drop `Decisions: imported.Decisions` from the Result literal in
// importops.Import (internal/importops/import.go). Every surface then answers with
// no decision, and the shared operation's check and all three surface subtests
// redden, each naming what its surface carried. Restore the field and drop it from
// one surface instead — the reportImportDecisions call (cmd/aggrega/import.go), the
// Decisions field of the tool's payload literal (internal/mcp/tool_import.go) or of
// the session's result literal (internal/serve/import.go) — and that surface's
// subtest alone reddens.
func TestTheMappingDecisionsReachEverySurfaceAlike(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	root, _ := mappingDecisionWorkspace(t)

	raw, err := os.ReadFile(decisionLineCasesFile)
	if err != nil {
		t.Fatalf("read the decision-line cases: %v", err)
	}
	var manifest struct {
		Langflow []struct {
			Decision abi.ImportDecision `json:"decision"`
			Line     string             `json:"line"`
		} `json:"langflow"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode the decision-line cases: %v", err)
	}
	wantDecisions := make([]abi.ImportDecision, 0, len(manifest.Langflow))
	wantLines := make([]string, 0, len(manifest.Langflow))
	want := make([]decisionFacts, 0, len(manifest.Langflow))
	scored := false
	for _, c := range manifest.Langflow {
		wantDecisions = append(wantDecisions, c.Decision)
		wantLines = append(wantLines, c.Line)
		want = append(want, factsOf(c.Decision))
		scored = scored || len(c.Decision.Candidates) > 0
	}
	if len(wantDecisions) == 0 || !scored {
		t.Fatalf("the manifest holds %d Langflow decision(s), scored=%v; the comparison needs decisions and at "+
			"least one carrying a score, or it would measure nothing", len(wantDecisions), scored)
	}

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	shared, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    filepath.Join(root, "flow.json"),
		SourceName:    "flow.json",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import was refused, so there are no drafts to compare: %v", err)
	}
	if !sameDecisions(t, shared.Decisions, wantDecisions) {
		t.Errorf("the shared operation's decisions are not the manifest's Langflow decisions: either the "+
			"operation dropped or reordered them, or the importer changed what it decides and the manifest — "+
			"which the editor's spec renders — must be regenerated from this answer, with its revision bumped "+
			"on both sides\n--- manifest ---\n%+v\n--- import ---\n%+v", wantDecisions, shared.Decisions)
	}

	t.Run("the command's error stream", func(t *testing.T) {
		code, stdout, stderr, process := runImportAtTheRoot(t, root, filepath.Join(root, "flow.json"), "--dry-run")
		if code != 0 {
			t.Fatalf("aggrega import exited %d\nstdout:\n%s\ncommand error writer:\n%s\nprocess error stream:\n%s",
				code, stdout, stderr, process)
		}
		var head strings.Builder
		head.WriteString("read as the langflow format, with drafted paths relative to " + root + "\n")
		reportImportDiagnostics(&head, shared.Diagnostics)
		rest, found := strings.CutPrefix(stderr, head.String())
		if !found {
			t.Fatalf("the error stream does not open with the preamble and the diagnostics:\n%s\nwant it to "+
				"open with\n%s", stderr, head.String())
		}
		lines := strings.Split(strings.TrimSuffix(rest, "\n"), "\n")
		if !reflect.DeepEqual(lines, wantLines) {
			t.Errorf("the error stream carries the decision lines\n  %q\nwant exactly\n  %q", lines, wantLines)
		}
		for i, line := range lines {
			if i < len(want) && mappedLine.MatchString(line) {
				if got := factsOfLine(t, line); got != want[i] {
					t.Errorf("line %d reads %+v; the decision is %+v", i, got, want[i])
				}
			}
		}

		docs := make([]authoredcontent.Document, 0, len(shared.Files))
		for _, f := range shared.Files {
			docs = append(docs, authoredcontent.Document{Path: f.Path, Content: f.Content})
		}
		var reported bytes.Buffer
		if err := authoredcontent.Report(&reported, docs); err != nil {
			t.Fatalf("compose the reported expectation: %v", err)
		}
		if stdout != reported.String() {
			t.Errorf("standard output under --dry-run carried\n%s\nwant the reported drafts and nothing else\n%s",
				stdout, reported.String())
		}
	})

	t.Run("the assistant tool's answer", func(t *testing.T) {
		var stdin, stdout, stderr bytes.Buffer
		stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
		stdin.WriteString(rpcLine(2, "tools/call", `{"name":"import","arguments":{"source":"flow.json","dry_run":true}}`))
		cmd := newRootCmd()
		cmd.SetIn(&stdin)
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"mcp", "--workspace-root", root})
		if code := executeRoot(context.Background(), cmd); code != 0 {
			t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
		}
		answer := answersByID(t, stdout.String(), 1, 2)[2]
		if answer.Error != nil {
			t.Fatalf("the import call was not dispatched: %s", answer.Error.Message)
		}
		var call struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent struct {
				Decisions json.RawMessage `json:"decisions"`
			} `json:"structuredContent"`
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(answer.Result, &call); err != nil {
			t.Fatalf("decode the import answer: %v (body %s)", err, answer.Result)
		}
		if call.IsError || len(call.Content) == 0 {
			t.Fatalf("the import was refused or answered no text: %s", answer.Result)
		}
		var decisions []toolDecision
		strictDecode(t, "the tool's decisions", call.StructuredContent.Decisions, &decisions)
		got := make([]abi.ImportDecision, 0, len(decisions))
		for _, d := range decisions {
			got = append(got, d.asImporterDecision())
		}
		if !sameDecisions(t, got, wantDecisions) {
			t.Errorf("the tool's decisions are\n%+v\nwant\n%+v", got, wantDecisions)
		}

		var textLines []string
		for _, line := range strings.Split(call.Content[0].Text, "\n") {
			if strings.HasPrefix(line, "  mapped ") || strings.HasPrefix(line, "  made no choice for ") {
				textLines = append(textLines, strings.TrimPrefix(line, "  "))
			}
		}
		if !reflect.DeepEqual(textLines, wantLines) {
			t.Errorf("the tool's text answer carries the decision lines\n  %q\nwant exactly\n  %q\ntext:\n%s",
				textLines, wantLines, call.Content[0].Text)
		}
	})

	t.Run("the session's result", func(t *testing.T) {
		request, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "aggrega/import",
			"params": map[string]any{"workspaceRoot": root, "source": "flow.json", "outputDir": "drafts",
				"dryRun": true},
		})
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
		if refusal, refused := frame["error"]; refused {
			t.Fatalf("aggrega/import was refused: %s\nstderr:\n%s", refusal, stderr.String())
		}

		var result struct {
			Decisions []abi.ImportDecision `json:"decisions"`
		}
		if err := json.Unmarshal(frame["result"], &result); err != nil {
			t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
		}
		if !sameDecisions(t, result.Decisions, wantDecisions) {
			t.Errorf("the session's decisions are\n%+v\nwant\n%+v", result.Decisions, wantDecisions)
		}
		gotFacts := make([]decisionFacts, 0, len(result.Decisions))
		for _, d := range result.Decisions {
			gotFacts = append(gotFacts, factsOf(d))
		}
		if !reflect.DeepEqual(gotFacts, want) {
			t.Errorf("the session's decisions read %+v; want %+v", gotFacts, want)
		}

		// The answer validates against the published wire contract's import
		// result, every decision through the definitions it names.
		resource, err := jsonschema.UnmarshalJSON(strings.NewReader(schemas.ServeWireV1alpha1))
		if err != nil {
			t.Fatalf("the shipped session wire schema is not a usable schema resource: %v", err)
		}
		const contract = "mem://serve-wire.json"
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource(contract, resource); err != nil {
			t.Fatalf("add the session wire schema as a resource: %v", err)
		}
		position, err := compiler.Compile(contract + "#/$defs/ImportResult")
		if err != nil {
			t.Fatalf("compile the import result's position: %v", err)
		}
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(frame["result"]))
		if err != nil {
			t.Fatalf("decode the answer for validation: %v", err)
		}
		if err := position.Validate(instance); err != nil {
			t.Errorf("the session's import result does not validate against the published contract:\n%v\n%s",
				err, frame["result"])
		}
	})
}
