// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importersocket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// The tests below exercise the real agent-flow importer guest end-to-end through
// the import-socket dispatcher: a stub resolver maps the langflow and flowise
// formats to the built import-langflow guest, and the REAL embedded JSON parser
// serves the guest its source tree through the parser socket. So the guest obtains
// its tree host-mediated (never re-parsing), decomposes each node, scores it
// against its embedded candidate knowledge, and drafts or references components —
// the whole semantic path, proven against a genuine parsed tree.

// agentFlowResolver resolves the langflow and flowise formats to the import-langflow
// guest fixture. It stands in for the catalog resolver a populated workspace
// supplies; any other format halts loudly.
type agentFlowResolver struct{}

func (agentFlowResolver) ImporterPluginForFormat(format string) (types.Plugin, error) {
	if format != "langflow" && format != "flowise" {
		return types.Plugin{}, errNoImporterForFormat
	}
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "import-langflow", Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Capabilities: []string{"import:langflow", "import:flowise"}},
	}, nil
}

func agentFlowLoader(fixturePath string) importersocket.ImporterLoader {
	return func(p types.Plugin) (wasm.PluginSpec, error) {
		return wasm.PluginSpec{Name: p.Coordinate(), Path: fixturePath}, nil
	}
}

// realJSONParserSeam wires the parser socket to the real embedded JSON parser: it
// writes the embedded json.wasm bytes to a temp file and resolves the "json"
// format to it, recording every resolve into the returned probe so a test can
// confirm one host-mediated parse per source. This is the genuine JSON parser the
// product ships, so the guest walks a real canonical tree rather than an echo.
func realJSONParserSeam(t *testing.T) (parsersocket.ParserResolver, parsersocket.ParserLoader, *parserSeamProbe) {
	t.Helper()
	raw, ok := embedded.Lookup("parser/json")
	if !ok {
		t.Fatal("embedded parser/json not found")
	}
	path := filepath.Join(t.TempDir(), "json-parser.wasm")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write embedded json parser: %v", err)
	}
	probe := &parserSeamProbe{}
	res := parserResolverFunc(func(format string) (types.Plugin, error) {
		probe.resolvedFormats = append(probe.resolvedFormats, format)
		return types.Plugin{
			Metadata: types.PluginMetadata{Name: "json", Version: "1.0.0", PluginType: "parser"},
			Spec:     types.PluginSpec{Capabilities: []string{"parse:json"}},
		}, nil
	})
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		probe.loadedCoords = append(probe.loadedCoords, p.Coordinate())
		return wasm.PluginSpec{Name: p.Coordinate(), Path: path}, nil
	}
	return res, load, probe
}

func settingsJSON(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return raw
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// findEntity returns the first entity matching kind (and mode, if non-empty) whose
// name contains nameSubstr, or fails.
func findEntity(t *testing.T, out abi.ImportOutput, kind, mode, nameSubstr string) abi.EmittedEntity {
	t.Helper()
	for _, e := range out.Entities {
		if e.Kind == kind && (mode == "" || e.Mode == mode) && strings.Contains(e.Name, nameSubstr) {
			return e
		}
	}
	t.Fatalf("no %s entity (mode %q) with name containing %q in %+v", kind, mode, nameSubstr, out.Entities)
	return abi.EmittedEntity{}
}

func decisionForType(t *testing.T, out abi.ImportOutput, nodeType string) abi.ImportDecision {
	t.Helper()
	for _, d := range out.Decisions {
		if d.SourceNode.Type == nodeType {
			return d
		}
	}
	t.Fatalf("no decision for node type %q", nodeType)
	return abi.ImportDecision{}
}

// TestImportLangflow_AutoApplyReferencesAndDrafts is the headline end-to-end proof:
// a real Langflow flow, imported in auto-apply mode, references its high-confidence
// nodes (OpenAI model, retrieval tool) at the standard-library components and
// drafts a new component for the node that matches nothing (a custom Python
// component). It prints the real scaffolded output for inspection.
func TestImportLangflow_AutoApplyReferencesAndDrafts(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-langflow-go.wasm")
	parserRes, parserLoad, probe := realJSONParserSeam(t)
	source := readFixture(t, "langflow-flow.json")

	settings := settingsJSON(t, map[string]any{"reuse_mode": "auto-apply-above-threshold"})
	out, err := importersocket.ImportArtifact(context.Background(), agentFlowResolver{}, agentFlowLoader(fixture), parserRes, parserLoad, "langflow", source, nil, settings)
	if err != nil {
		t.Fatalf("ImportArtifact: %v", err)
	}

	// The guest obtained its tree host-mediated: exactly one parse for the one
	// source, resolved as json.
	if len(probe.resolvedFormats) != 1 || probe.resolvedFormats[0] != "json" {
		t.Fatalf("parser resolved formats = %v; want exactly [json]", probe.resolvedFormats)
	}

	// HIGH matches are referenced under auto-apply.
	openai := findEntity(t, out, "Component", "reference", "core/ai-model/openai")
	if openai.Body != "" {
		t.Errorf("referenced entity should carry no body, got %q", openai.Body)
	}
	findEntity(t, out, "Component", "reference", "core/ai-tool/retrieval")

	// The unmatched custom node is drafted new.
	drafted := findEntity(t, out, "Component", "draft", "imported/component/custompythoncomponent")
	if !strings.Contains(drafted.Body, "kind: Component") {
		t.Errorf("drafted component body missing kind: %q", drafted.Body)
	}

	openaiDec := decisionForType(t, out, "OpenAIModel")
	if openaiDec.Chosen == nil || openaiDec.Chosen.Mode != "reference" || openaiDec.Chosen.Tier != "HIGH" {
		t.Errorf("OpenAIModel decision = %+v; want reference/HIGH", openaiDec.Chosen)
	}
	if openaiDec.SwapHint == "" {
		t.Error("OpenAIModel reference should record a runner-up swap hint")
	}
	customDec := decisionForType(t, out, "CustomPythonComponent")
	if customDec.Chosen == nil || customDec.Chosen.Mode != "draft" || customDec.Chosen.Tier != "NONE" {
		t.Errorf("CustomPythonComponent decision = %+v; want draft/NONE", customDec.Chosen)
	}

	// Print the real scaffolded output for inspection.
	agg := findEntity(t, out, "Aggregate", "", "")
	t.Logf("\n===== DRAFTED AGGREGATE (auto-apply) =====\n%s", agg.Body)
	t.Logf("\n===== DRAFTED COMPONENT (fallback) =====\n%s", drafted.Body)
	for _, d := range out.Decisions {
		raw, _ := json.MarshalIndent(d, "", "  ")
		t.Logf("\n===== DECISION: %s (%s) =====\n%s", d.SourceNode.Type, d.SourceNode.ID, raw)
	}
	for _, f := range out.Files {
		t.Logf("\n----- FILE %s -----\n%s", f.Path, f.Content)
	}
}

// TestImportLangflow_HintsOnlyDraftsEverything proves the locked default posture:
// with reuse_mode unset (hints-only), the importer references nothing — every node
// is drafted — but a HIGH match still records a strong swap hint the author can act
// on. Nothing is silently referenced.
func TestImportLangflow_HintsOnlyDraftsEverything(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-langflow-go.wasm")
	parserRes, parserLoad, _ := realJSONParserSeam(t)
	source := readFixture(t, "langflow-flow.json")

	out, err := importersocket.ImportArtifact(context.Background(), agentFlowResolver{}, agentFlowLoader(fixture), parserRes, parserLoad, "langflow", source, nil, nil)
	if err != nil {
		t.Fatalf("ImportArtifact: %v", err)
	}

	for _, e := range out.Entities {
		if e.Kind == "Component" && e.Mode == "reference" {
			t.Errorf("hints-only referenced %q silently; want draft only", e.Name)
		}
	}
	openaiDec := decisionForType(t, out, "OpenAIModel")
	if openaiDec.Chosen == nil || openaiDec.Chosen.Mode != "draft" || openaiDec.Chosen.Tier != "HIGH" {
		t.Errorf("OpenAIModel decision = %+v; want draft/HIGH under hints-only", openaiDec.Chosen)
	}
	if !strings.Contains(openaiDec.SwapHint, "core/ai-model/openai") {
		t.Errorf("OpenAIModel hints-only should record a swap hint toward openai, got %q", openaiDec.SwapHint)
	}
}

// TestImportLangflow_Deterministic pins Invariant 8 across the whole semantic path:
// two independent imports of the same source yield a byte-identical ImportOutput,
// scores and all.
func TestImportLangflow_Deterministic(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-langflow-go.wasm")
	source := readFixture(t, "langflow-flow.json")

	run := func() []byte {
		parserRes, parserLoad, _ := realJSONParserSeam(t)
		out, err := importersocket.ImportArtifact(context.Background(), agentFlowResolver{}, agentFlowLoader(fixture), parserRes, parserLoad, "langflow", source, nil, nil)
		if err != nil {
			t.Fatalf("ImportArtifact: %v", err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}
	if first, second := run(), run(); !bytes.Equal(first, second) {
		t.Fatalf("non-deterministic import output:\n first: %s\nsecond: %s", first, second)
	}
}

// TestImportLangflow_MultiFile drives more than one source through one import: the
// guest walks each source, asking the host to parse each one, and namespaces node
// references by source so two flows compose into one aggregate without collision.
func TestImportLangflow_MultiFile(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-langflow-go.wasm")
	parserRes, parserLoad, probe := realJSONParserSeam(t)

	sources := []abi.ImportSource{
		{Name: "flow-a.json", Bytes: readFixture(t, "langflow-flow.json")},
		{Name: "flow-b.json", Bytes: readFixture(t, "langflow-flow-b.json")},
	}
	out, err := importersocket.ImportArtifact(context.Background(), agentFlowResolver{}, agentFlowLoader(fixture), parserRes, parserLoad, "langflow", sources[0].Bytes, sources, nil)
	if err != nil {
		t.Fatalf("ImportArtifact: %v", err)
	}

	// One host-mediated parse per source.
	if len(probe.resolvedFormats) != 2 {
		t.Fatalf("parser resolved %d times; want 2 (one per source): %v", len(probe.resolvedFormats), probe.resolvedFormats)
	}
	// Nodes from both sources appear, referenced back to their source.
	var sawA, sawB bool
	for _, d := range out.Decisions {
		if strings.HasPrefix(d.SourceNode.ID, "flow-a.json#") {
			sawA = true
		}
		if strings.HasPrefix(d.SourceNode.ID, "flow-b.json#") {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("multi-file import missing a source: sawA=%v sawB=%v decisions=%+v", sawA, sawB, out.Decisions)
	}
	decisionForType(t, out, "AnthropicModel") // the node unique to flow-b
}

// TestImportLangflow_TwoRenderersOneRecord proves the one-record/two-renderer seam:
// the human renderer and the stub LLM renderer both consume the identical
// decisions[] record, differing only in the files they produce. The decision
// record is byte-identical across the two runs; the files are not.
func TestImportLangflow_TwoRenderersOneRecord(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-langflow-go.wasm")
	source := readFixture(t, "langflow-flow.json")

	do := func(mode string) abi.ImportOutput {
		parserRes, parserLoad, _ := realJSONParserSeam(t)
		out, err := importersocket.ImportArtifact(context.Background(), agentFlowResolver{}, agentFlowLoader(fixture), parserRes, parserLoad, "langflow", source, nil, settingsJSON(t, map[string]any{"render_mode": mode}))
		if err != nil {
			t.Fatalf("ImportArtifact(%s): %v", mode, err)
		}
		return out
	}
	human := do("human")
	llm := do("llm")

	// The shared decision record is identical — the renderer is the only thing
	// that varies.
	hd, _ := json.Marshal(human.Decisions)
	ld, _ := json.Marshal(llm.Decisions)
	if !bytes.Equal(hd, ld) {
		t.Fatalf("decision records differ between renderers:\nhuman: %s\nllm:   %s", hd, ld)
	}

	// The human renderer chunks per entity; the llm renderer serves one document.
	var humanHasAggregate, llmHasDoc bool
	for _, f := range human.Files {
		if f.Path == "aggregate.yaml" {
			humanHasAggregate = true
		}
	}
	for _, f := range llm.Files {
		if f.Path == "import-decisions.json" {
			llmHasDoc = true
		}
	}
	if !humanHasAggregate {
		t.Errorf("human renderer files = %+v; want an aggregate.yaml", human.Files)
	}
	if !llmHasDoc || len(llm.Files) != 1 {
		t.Errorf("llm renderer files = %+v; want a single import-decisions.json", llm.Files)
	}
}

// TestImportLangflow_TieBrokenByName pins the deterministic tie-break: a node whose
// only signal is its inferred category ties every ai-model candidate at the same
// score, and the candidates come back ordered by component name.
func TestImportLangflow_TieBrokenByName(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-langflow-go.wasm")
	parserRes, parserLoad, _ := realJSONParserSeam(t)

	// A generic model node: "Model" in the type infers the ai-model category, but
	// there is no table entry, no traits, no vendor token, and a field name no
	// candidate shares — so every ai-model candidate scores exactly the category
	// weight and ties.
	tie := []byte(`{"data":{"nodes":[{"id":"GenericModel-1","data":{"type":"GenericModel","node":{"template":{"unshared_param":{"type":"str"}}}}}],"edges":[]}}`)
	out, err := importersocket.ImportArtifact(context.Background(), agentFlowResolver{}, agentFlowLoader(fixture), parserRes, parserLoad, "langflow", tie, nil, nil)
	if err != nil {
		t.Fatalf("ImportArtifact: %v", err)
	}
	d := decisionForType(t, out, "GenericModel")
	if len(d.Candidates) < 2 {
		t.Fatalf("want at least two tied candidates, got %+v", d.Candidates)
	}
	want := []string{"core/ai-model/anthropic", "core/ai-model/openai", "core/ai-model/openai-embedding"}
	for i, name := range want {
		if i >= len(d.Candidates) {
			break
		}
		if d.Candidates[i].ComponentName != name {
			t.Errorf("candidate[%d] = %q; want %q (name-order tie-break)", i, d.Candidates[i].ComponentName, name)
		}
	}
	if d.Candidates[0].Score != d.Candidates[1].Score {
		t.Errorf("candidates not tied: %v vs %v", d.Candidates[0].Score, d.Candidates[1].Score)
	}
}

// TestImportLangflow_DraftedYAMLValidates proves the drafted authoring is what the
// CLI accepts: the drafted Component passes the single-entity validation path
// (schema envelope + pillar structure), and the drafted Aggregate passes the same
// envelope schema gate the catalog loader runs at each parse boundary.
func TestImportLangflow_DraftedYAMLValidates(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-langflow-go.wasm")
	parserRes, parserLoad, _ := realJSONParserSeam(t)
	source := readFixture(t, "flowise-flow.json")

	out, err := importersocket.ImportArtifact(context.Background(), agentFlowResolver{}, agentFlowLoader(fixture), parserRes, parserLoad, "flowise", source, nil, nil)
	if err != nil {
		t.Fatalf("ImportArtifact: %v", err)
	}

	// Every drafted component passes the envelope schema gate.
	var draftedCount int
	for _, e := range out.Entities {
		if e.Kind == "Component" && e.Mode == "draft" {
			draftedCount++
			if _, verr := envelope.ValidateBytes(envelope.KindComponent, []byte(e.Body), "drafted-"+e.Name); verr != nil {
				t.Errorf("drafted component %q failed the envelope schema gate:\n%s\nerr: %v", e.Name, e.Body, verr)
			}
			// The pillar tier the single-entity validate path adds for a Component:
			// spec.schema must compile to JSON Schema 2020-12.
			var comp types.Component
			if uerr := yaml.Unmarshal([]byte(e.Body), &comp); uerr != nil {
				t.Errorf("drafted component %q did not unmarshal: %v", e.Name, uerr)
				continue
			}
			if perr := validator.ValidateSchemaPillars("Component", comp.Metadata.Name, "drafted-"+e.Name, &comp.Spec.Schema, nil); perr != nil {
				t.Errorf("drafted component %q failed pillar validation:\n%s\nerr: %v", e.Name, e.Body, perr)
			}
		}
	}
	if draftedCount == 0 {
		t.Fatal("no drafted components to validate")
	}

	agg := findEntity(t, out, "Aggregate", "", "")
	if _, verr := envelope.ValidateBytes(envelope.KindAggregate, []byte(agg.Body), "drafted-aggregate"); verr != nil {
		t.Errorf("drafted aggregate failed the envelope schema gate:\n%s\nerr: %v", agg.Body, verr)
	}
}
