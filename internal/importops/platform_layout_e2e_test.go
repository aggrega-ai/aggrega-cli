// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end coverage of the reference importers' two layouts over every
// Langflow and Flowise source committed in this module, each run through the
// real Import over a workspace whose catalog folder holds the reference importers
// and a JSON parser beside their entities — the folder importopstest lays down,
// and the path every surface reaches.
//
// The default layout drafts the platform the flow runs on carrying the flow as it
// was exported, and decomposes into Components of their own only the parts of the
// flow a socket of the platform takes; the node-by-node layout drafts every node
// as a Component, and is held here to what it drafts from each source, byte for
// byte. Where a case needs the platform's sockets, the catalog folder also holds
// the platform Components with the sockets the standard library declares for
// them, and the Components the flows' parts resemble, with the labels the
// standard library gives them.

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// flowFixture is one committed Langflow or Flowise source and what the default
// layout drafts from it over a catalog folder holding platformComponents.
type flowFixture struct {
	// path is where the source sits, relative to this package.
	path string
	// golden names its node-by-node goldens under testdata/nodes-layout.
	golden string
	format string
	// platform is the platform Component the draft names, key the instance's key
	// in the drafted Aggregate, and ident the value naming the flow on it.
	platform, key, ident string
	// ownID is the identifier the source gives its flow, empty where it gives none
	// and the draft derives one.
	ownID string
	// wired is each socket of the platform that draws a connection, and the keys
	// of the parts it reaches.
	wired map[string][]string
}

const (
	langflowPlatform = "core/ai-agent/langflow"
	flowisePlatform  = "core/ai-agent/flowise"
)

func langflowFixture(path, golden, ownID string, wired map[string][]string) flowFixture {
	return flowFixture{path: path, golden: golden, format: "langflow", platform: langflowPlatform, key: "langflow",
		ident: "flow_id", ownID: ownID, wired: wired}
}

func flowiseFixture(path, golden string, wired map[string][]string) flowFixture {
	return flowFixture{path: path, golden: golden, format: "flowise", platform: flowisePlatform, key: "flowise",
		ident: "chatflow_id", wired: wired}
}

// flowFixtures is every committed Langflow and Flowise source in this module: the
// hand-written fixtures, the escape fixture, and the real exports.
var flowFixtures = []flowFixture{
	flowiseFixture("../../cmd/aggrega/testdata/flowise-chatflow.json", "cmd_aggrega_testdata_flowise-chatflow",
		map[string][]string{"model": {"chatopenai-0"}}),
	langflowFixture("../../cmd/aggrega/testdata/langflow-flow.json", "cmd_aggrega_testdata_langflow-flow",
		"e2c1b6f0-1111-4a2b-8c3d-000000000001", map[string][]string{"model": {"openaimodel-abc12"}}),
	flowiseFixture("../compiler/testdata/flow-definition-escape/exports/flowise-chatflow.json",
		"internal_compiler_testdata_flow-definition-escape_exports_flowise-chatflow",
		map[string][]string{"model": {"chatopenai-0"}}),
	langflowFixture("../compiler/importersocket/testdata/langflow-flow-b.json",
		"internal_compiler_importersocket_testdata_langflow-flow-b", "", map[string][]string{"model": {"anthropicmodel-z9"}}),
	flowiseFixture("../compiler/importersocket/testdata/flowise-flow.json",
		"internal_compiler_importersocket_testdata_flowise-flow", map[string][]string{"model": {"chatopenai-0"}}),
	langflowFixture("../compiler/importersocket/testdata/langflow-flow.json",
		"internal_compiler_importersocket_testdata_langflow-flow", "", map[string][]string{"model": {"openaimodel-a1b2"}}),
	flowiseFixture(realExportPath(flowiseAgentflow),
		"internal_compiler_importersocket_testdata_real-exports_flowise_agentflowsv2_Agents_Handoff", nil),
	flowiseFixture(realExportPath(flowiseChatflow),
		"internal_compiler_importersocket_testdata_real-exports_flowise_chatflows_Multiple_Documents_QnA",
		map[string][]string{"model": {"chatopenai-0"}}),
	// Its Language Model node selects no model provider, so it is wired to the
	// model socket and written with none of the variables the platform delivers:
	// the flow is carried as it was exported.
	langflowFixture(realExportPath(langflowPromptingProject),
		"internal_compiler_importersocket_testdata_real-exports_langflow_Basic_Prompting",
		"c1cb7b3c-2589-4754-ac61-8cf88df4a07e", map[string][]string{"model": {"languagemodelcomponent-fleyf"}}),
	langflowFixture(realExportPath(langflowStarterProject),
		"internal_compiler_importersocket_testdata_real-exports_langflow_Research_Translation_Loop",
		"7eb7c081-888c-4f25-ba81-c84e6aab2778", map[string][]string{"model": {"languagemodelcomponent-ps5yd"}}),
	langflowFixture(realExportPath(langflowRemixProject),
		"internal_compiler_importersocket_testdata_real-exports_langflow_Nvidia_Remix",
		"faac3377-a15b-459b-a6bd-c47c318dcc78", nil),
	// Its Language Model node runs on OpenAI and names its key as the global
	// variable OPENAI_API_KEY, the variable the platform delivers for the model
	// connection: wired to the model socket, the node is written with that name and
	// load_from_db true, which it already holds, so the flow is carried as it was
	// exported.
	langflowFixture(realExportPath(langflowSelectingProject),
		"internal_compiler_importersocket_testdata_real-exports_langflow_Basic_Prompting_1.6.0",
		"0dbee653-41ae-4e51-af2e-55757fb24be3", map[string][]string{"model": {"languagemodelcomponent-kboja"}}),
}

// platformSockets are the sockets the standard library declares on both flow
// platforms, as its catalog writes them.
const platformSockets = `  usesSchema:
    model:
      selector: "component.aggrega.ai/category in (ai-model, gateway), model.aggrega.ai/api"
      required: true
    tools:
      selector: "component.aggrega.ai/category=ai-tool, tool.aggrega.ai/transport=http"
    vector_store:
      selector: "datastore.aggrega.ai/engine in (vector, semantic-memory, search)"
`

// partComponent writes a Component carrying labels and one value key.
func partComponent(name, labels, value string) catalogComponent {
	return catalogComponent{name: name, version: "1.0.0", body: "apiVersion: aggrega.ai/v1beta1\nkind: Component\n" +
		"metadata:\n  name: " + name + "\n  version: 1.0.0\n  labels:\n" + labels +
		"spec:\n  schema:\n    values:\n      " + value + ": { type: string }\n"}
}

// platformComponents are the two flow platforms, with the sockets the standard
// library declares for them, and the Components the flows' parts resemble, with
// the labels the standard library gives them — a generative and an embedding
// model, each naming the model API it speaks, two tools no platform socket takes
// since neither is reached over HTTP — and a tool reached over HTTP and a vector
// store, which a mapping override can name.
var platformComponents = []catalogComponent{
	{name: langflowPlatform, version: "1.0.0", body: "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n" +
		"  name: core/ai-agent/langflow\n  version: 1.0.0\n  labels:\n    component.aggrega.ai/type: server\n" +
		"    component.aggrega.ai/category: ai-agent\nspec:\n" + platformSockets +
		"  schema:\n    values:\n      flow_id: { type: string }\n      flow_definition: { type: string }\n"},
	{name: flowisePlatform, version: "1.0.0", body: "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n" +
		"  name: core/ai-agent/flowise\n  version: 1.0.0\n  labels:\n    component.aggrega.ai/type: server\n" +
		"    component.aggrega.ai/category: ai-agent\nspec:\n" + platformSockets +
		"  schema:\n    values:\n      chatflow_id: { type: string }\n      flow_definition: { type: string }\n"},
	partComponent("core/ai-model/openai", "    component.aggrega.ai/type: resource\n    component.aggrega.ai/category: ai-model\n"+
		"    model.aggrega.ai/capability: generative\n    model.aggrega.ai/api: openai\n    component.aggrega.ai/hosting: managed\n", "model"),
	partComponent("core/ai-model/anthropic", "    component.aggrega.ai/type: resource\n    component.aggrega.ai/category: ai-model\n"+
		"    model.aggrega.ai/capability: generative\n    model.aggrega.ai/api: anthropic\n    component.aggrega.ai/hosting: managed\n", "model"),
	partComponent("core/ai-model/openai-embedding", "    component.aggrega.ai/type: resource\n    component.aggrega.ai/category: ai-model\n"+
		"    model.aggrega.ai/capability: embedding\n    model.aggrega.ai/api: openai\n    component.aggrega.ai/hosting: managed\n", "model"),
	partComponent("core/ai-tool/retrieval", "    component.aggrega.ai/type: function\n    component.aggrega.ai/category: ai-tool\n"+
		"    tool.aggrega.ai/impact: read-only\n", "top_k"),
	partComponent("core/ai-tool/http-request", "    component.aggrega.ai/type: function\n    component.aggrega.ai/category: ai-tool\n"+
		"    tool.aggrega.ai/impact: mutating\n", "method"),
	partComponent("acme/ai-tool/web-fetch", "    component.aggrega.ai/type: function\n    component.aggrega.ai/category: ai-tool\n"+
		"    tool.aggrega.ai/transport: http\n    tool.aggrega.ai/impact: read-only\n", "url"),
	partComponent("acme/datastore/vectors", "    component.aggrega.ai/type: resource\n    component.aggrega.ai/category: datastore\n"+
		"    datastore.aggrega.ai/engine: vector\n", "index"),
}

// platformCatalogWorkspace is catalogFolderImportWorkspaceAttaching with the
// platform Components and the parts' Components beside the importers.
func platformCatalogWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	writeCatalogComponents(t, folder.Dir, platformComponents...)
	return rw
}

// importFlow runs Import over the source at path through rw, naming no format and
// reporting rather than writing.
func importFlow(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, path string) Result {
	t.Helper()
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    path,
		SourceName:    filepath.Base(path),
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import of %s was refused: %v", path, err)
	}
	return res
}

// draftedAggregateOf returns the content of the Aggregate an import drafted.
func draftedAggregateOf(t *testing.T, res Result) string {
	t.Helper()
	for _, f := range res.Files {
		if f.Path == "aggregate.yaml" {
			return f.Content
		}
	}
	t.Fatalf("the import drafted no Aggregate: %v", draftPaths(res))
	return ""
}

// platformAggregate is the part of a drafted Aggregate the cases below read.
type platformAggregate struct {
	Spec struct {
		Components map[string]struct {
			Type   string                                        `yaml:"type"`
			Values map[string]any                                `yaml:"values"`
			Uses   map[string]map[string]struct{ Target string } `yaml:"uses"`
		} `yaml:"components"`
	} `yaml:"spec"`
}

// readAsTheCompilerDoes reads a drafted Aggregate as the compiler reads one: its
// raw text parsed as YAML — the compiler finds an Aggregate by reading it before
// it interpolates — then its whole text evaluated as a template, through the
// compiler's own interpolation, and the result parsed as YAML.
func readAsTheCompilerDoes(t *testing.T, content string) platformAggregate {
	t.Helper()
	var raw platformAggregate
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		t.Fatalf("the drafted Aggregate's own text is not YAML: %v\n%s", err, content)
	}
	interpolated, err := resolver.InterpolateParameters([]byte(content), nil)
	if err != nil {
		t.Fatalf("the drafted Aggregate does not interpolate: %v", err)
	}
	var agg platformAggregate
	if err := yaml.Unmarshal(interpolated, &agg); err != nil {
		t.Fatalf("the interpolated Aggregate is not YAML: %v", err)
	}
	return agg
}

// asTheEditorSpells returns a Langflow export as the platform draft carries it:
// each edge's handle text written as Langflow's editor writes it — the handle
// read back as JSON with every "œ" a double quote, written with its keys sorted
// and no whitespace, every double quote then written "œ" — and every other byte
// as the export holds it. The spelling is computed here with the JSON encoder
// rather than with the importer's own writer, so a case comparing the two holds
// the importer to what the editor reads. A handle already spelled so is left as
// written, and so, as the importer leaves them, is one that does not read as the
// editor reads it and one holding a number.
func asTheEditorSpells(t *testing.T, export []byte) []byte {
	t.Helper()
	var doc struct {
		Data struct {
			Edges []map[string]any `json:"edges"`
		} `json:"data"`
	}
	if err := json.Unmarshal(export, &doc); err != nil {
		t.Fatalf("decode the export's edges: %v", err)
	}
	encode := func(v any) string {
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			t.Fatalf("encode %v: %v", v, err)
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
	var edits []importer.Edit
	for i, edge := range doc.Data.Edges {
		for _, key := range []string{"sourceHandle", "targetHandle"} {
			text, ok := edge[key].(string)
			if !ok {
				continue
			}
			dec := json.NewDecoder(strings.NewReader(strings.ReplaceAll(text, "œ", `"`)))
			dec.UseNumber()
			var handle any
			if dec.Decode(&handle) != nil || holdsNumber(handle) {
				continue
			}
			spelled := strings.ReplaceAll(encode(handle), `"`, "œ")
			if spelled == text {
				continue
			}
			edits = append(edits, importer.Edit{Pointer: importer.Pointer("data", "edges", strconv.Itoa(i), key),
				Value: json.RawMessage(encode(spelled))})
		}
	}
	out, err := importer.Rewrite(export, edits)
	if err != nil {
		t.Fatalf("write the export's handles as the editor spells them: %v", err)
	}
	return out
}

// holdsNumber reports whether a decoded JSON value holds a number anywhere.
func holdsNumber(v any) bool {
	switch v := v.(type) {
	case json.Number:
		return true
	case []any:
		return slices.ContainsFunc(v, holdsNumber)
	case map[string]any:
		for _, e := range v {
			if holdsNumber(e) {
				return true
			}
		}
	}
	return false
}

// uuidOfAPlatform is the shape a flow platform takes a flow identifier in.
var uuidOfAPlatform = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestEveryLangflowAndFlowiseFixtureIsDraftedAsThePlatformCarryingIt imports
// every committed Langflow and Flowise source under the default layout and the
// hints-only reuse mode, over a catalog holding the two platforms and the
// Components their parts resemble, and reads the drafted Aggregate as the
// compiler reads one. For each:
//
//   - the Aggregate holds one instance of the source's platform, and its
//     definition value, once interpolated, is the source's own bytes — double
//     braces, a missing final line break, every credential value it holds and
//     all — since the default writes no credential value empty and what a wired
//     part of these sources is written with is what its node already holds —
//     but for a Langflow source's edge handles, written as Langflow's editor
//     spells them (asTheEditorSpells);
//   - the flow's identifier is the source's own where it gives one, and a UUID of
//     the shape the platforms take where it gives none;
//   - each socket draws exactly the connections the table names, each reaching a
//     part drafted as a Component of its own, and nothing else is a Component;
//   - every node keeps a decision: a part's says where it is wired, and every
//     other node's chooses nothing and says the platform's flow keeps it.
func TestEveryLangflowAndFlowiseFixtureIsDraftedAsThePlatformCarryingIt(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	for _, fx := range flowFixtures {
		t.Run(fx.golden, func(t *testing.T) {
			t.Parallel()
			res := importFlow(t, platformCatalogWorkspace(t, map[string]any{"reuse_mode": "hints-only"}), fx.path)
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if res.Format != fx.format {
				t.Errorf("the source was read as the %q format, want %q", res.Format, fx.format)
			}
			export, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatalf("read the source: %v", err)
			}
			agg := readAsTheCompilerDoes(t, draftedAggregateOf(t, res))

			var platforms, parts []string
			for key, c := range agg.Spec.Components {
				if c.Type == fx.platform {
					platforms = append(platforms, key)
				} else {
					parts = append(parts, key)
				}
			}
			if !slices.Equal(platforms, []string{fx.key}) {
				t.Fatalf("the Aggregate holds the platform as %v, want one instance keyed %s", platforms, fx.key)
			}
			inst := agg.Spec.Components[fx.key]
			carried := export
			if fx.format == "langflow" {
				carried = asTheEditorSpells(t, export)
			}
			if got, _ := inst.Values["flow_definition"].(string); got != string(carried) {
				t.Errorf("the definition the platform carries is not the source's bytes (%d bytes carried, %d expected)",
					len(got), len(carried))
			}
			id, _ := inst.Values[fx.ident].(string)
			switch {
			case fx.ownID != "" && id != fx.ownID:
				t.Errorf("the flow is named %q, want the source's own %q", id, fx.ownID)
			case fx.ownID == "" && !uuidOfAPlatform.MatchString(id):
				t.Errorf("the flow is named %q, which is not a UUID of the shape a platform takes", id)
			}

			wired := map[string][]string{}
			var targets []string
			for socket, connections := range inst.Uses {
				for alias, c := range connections {
					wired[socket] = append(wired[socket], alias)
					targets = append(targets, c.Target)
					if c.Target != alias {
						t.Errorf("the %s connection %s reaches %s, not the part it is named after", socket, alias, c.Target)
					}
				}
				slices.Sort(wired[socket])
			}
			want := map[string][]string{}
			var wantParts []string
			for socket, keys := range fx.wired {
				want[socket] = slices.Sorted(slices.Values(keys))
				wantParts = append(wantParts, keys...)
			}
			if !maps.EqualFunc(wired, want, slices.Equal[[]string]) {
				t.Errorf("the platform's sockets draw %v, want %v", wired, want)
			}
			slices.Sort(parts)
			slices.Sort(targets)
			slices.Sort(wantParts)
			if !slices.Equal(parts, wantParts) || !slices.Equal(targets, wantParts) {
				t.Errorf("the Aggregate's other components are %v and the connections reach %v; want exactly the parts %v",
					parts, targets, wantParts)
			}
			if components := len(draftPaths(res)) - 2; components != len(wantParts) {
				t.Errorf("the import drafted %d Component(s) beside the Aggregate and the Release, want one per part, %d",
					components, len(wantParts))
			}

			chosen := 0
			for _, d := range res.Decisions {
				if d.Chosen != nil {
					chosen++
					if !strings.Contains(d.Rationale, "; wired to the ") {
						t.Errorf("the part %s does not say where it is wired: %q", d.SourceNode.ID, d.Rationale)
					}
					continue
				}
				if prefix := "kept in the flow " + fx.platform + " carries, with no Component of its own: "; !strings.HasPrefix(d.Rationale, prefix) {
					t.Errorf("%s chooses nothing and says %q; want it to say the flow keeps it", d.SourceNode.ID, d.Rationale)
				}
			}
			if chosen != len(wantParts) {
				t.Errorf("%d decisions choose a Component, want one per part, %d", chosen, len(wantParts))
			}
		})
	}
}

// TestAPartIsWiredToTheSocketItsComponentSatisfies hands the Flowise importer
// mapping overrides naming a tool reached over HTTP for the HTTP request node and
// a vector store for the Pinecone nodes: each part is wired to the socket whose
// selector its Component satisfies — the tools socket, the vector_store socket —
// the sockets are written in the order the platform declares them, and a part no
// socket takes is reported and stays in the flow, as is each embedding model the
// model socket's single connection does not reach.
func TestAPartIsWiredToTheSocketItsComponentSatisfies(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	overrides := map[string]any{"mapping_overrides": map[string]any{"flowise": map[string]any{
		"httpRequest": map[string]any{"component": "acme/ai-tool/web-fetch", "category": "ai-tool"},
		"pinecone":    map[string]any{"component": "acme/datastore/vectors", "category": "datastore"},
	}}}
	// The model connection's credential, for an import naming no target, is shown
	// with its own key for the compose target, commented out; no other connection
	// carries one.
	model := "      uses:\n        model:\n          chatopenai-0:\n            target: chatopenai-0\n" +
		"            # For the compose target only, uncomment the lines below, which declare credential on " +
		"chatopenai-0: " + importer.DeclarationWhy + ".\n" +
		"            # features:\n            #   credential:\n            #     type: core/secret-value\n" +
		"            #     values:\n            #       key: OPENAI_API_KEY\n"
	for _, tc := range []struct {
		name, source, uses string
		reported           []string
	}{
		{name: "a tool reached over HTTP", source: "../compiler/importersocket/testdata/flowise-flow.json",
			uses: model + "        tools:\n          httprequest-0:\n            target: httprequest-0\n"},
		{name: "a vector store", source: realExportPath(flowiseChatflow),
			uses: model + "        vector_store:\n          pinecone-0:\n            target: pinecone-0\n" +
				"          pinecone-1:\n            target: pinecone-1\n",
			reported: []string{"openAIEmbeddings_0", "openAIEmbeddings_1", "retrieverTool_2", "retrieverTool_1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := importFlow(t, platformCatalogWorkspace(t, overrides), tc.source)
			if agg := draftedAggregateOf(t, res); !strings.Contains(agg, tc.uses) {
				t.Errorf("the platform does not draw\n%s\nit reads\n%s", tc.uses, agg)
			}
			var reported []string
			for _, d := range res.Diagnostics {
				if d.Code == "part-not-placed" {
					reported = append(reported, d.SourceRef)
				}
			}
			if !slices.Equal(reported, tc.reported) {
				t.Errorf("the parts reported as wired to no socket are %v, want %v", reported, tc.reported)
			}
		})
	}
}

// TestTheNodesLayoutDraftsEveryFixtureAsItsGolden imports every committed
// Langflow and Flowise source with the node-by-node layout and holds what it
// returns to its goldens under testdata/nodes-layout, byte for byte: the drafts
// in the reported form the command writes under --dry-run, and the decision line
// of every node, in the importer's order. No diagnostic is raised.
//
// The goldens are the node-by-node drafts of these sources over a catalog holding
// no Component; a change to what that layout drafts moves them, and the moved
// goldens are reviewed as the change they are.
func TestTheNodesLayoutDraftsEveryFixtureAsItsGolden(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	for _, fx := range flowFixtures {
		t.Run(fx.golden, func(t *testing.T) {
			t.Parallel()
			rw, _ := catalogFolderImportWorkspaceAttaching(t, importopstest.NodesLayout())
			res := importFlow(t, rw, fx.path)
			if got := diagnosticsBesideTheOwed(res); len(got) != 0 {
				t.Errorf("the import raised %+v; the node-by-node draft of this source raises none but the values "+
					"its drafts owe", got)
			}

			// The goldens hold what the importer drafts; the record the import writes
			// beside the drafts is a digest of them, held by the sync's own cases.
			docs := make([]authoredcontent.Document, 0, len(res.Files))
			for _, f := range res.Files {
				if f.Path == RecordFileName {
					continue
				}
				docs = append(docs, authoredcontent.Document{Path: f.Path, Content: f.Content})
			}
			var reported bytes.Buffer
			if err := authoredcontent.Report(&reported, docs); err != nil {
				t.Fatalf("compose the reported drafts: %v", err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "nodes-layout", fx.golden+".reported.yaml"))
			if err != nil {
				t.Fatalf("read the golden drafts: %v", err)
			}
			if !bytes.Equal(reported.Bytes(), want) {
				t.Errorf("the node-by-node drafts differ from the golden:\n%s\nwant\n%s", reported.Bytes(), want)
			}

			var lines strings.Builder
			for _, d := range res.Decisions {
				lines.WriteString(DecisionLine(d) + "\n")
			}
			wantLines, err := os.ReadFile(filepath.Join("testdata", "nodes-layout", fx.golden+".decisions.txt"))
			if err != nil {
				t.Fatalf("read the golden decisions: %v", err)
			}
			if lines.String() != string(wantLines) {
				t.Errorf("the node-by-node decisions read\n%s\nwant\n%s", lines.String(), wantLines)
			}
		})
	}
}

// TestAnAttachmentSettingNoLayoutDraftsWhatLayoutPlatformDrafts imports every
// committed Langflow and Flowise source twice over the platform catalog — once
// handed no settings, once handed layout: platform — and requires the two to
// return the same drafts, decisions and diagnostics, byte for byte.
func TestAnAttachmentSettingNoLayoutDraftsWhatLayoutPlatformDrafts(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	for _, fx := range flowFixtures {
		t.Run(fx.golden, func(t *testing.T) {
			t.Parallel()
			none := importFlow(t, platformCatalogWorkspace(t, nil), fx.path)
			named := importFlow(t, platformCatalogWorkspace(t, map[string]any{"layout": "platform"}), fx.path)
			for _, pair := range []struct {
				what        string
				none, named any
			}{
				{"drafts", none.Files, named.Files},
				{"decisions", none.Decisions, named.Decisions},
				{"diagnostics", none.Diagnostics, named.Diagnostics},
			} {
				a, _ := json.Marshal(pair.none)
				b, _ := json.Marshal(pair.named)
				if !bytes.Equal(a, b) {
					t.Errorf("the %s differ:\n     none: %s\n platform: %s", pair.what, a, b)
				}
			}
		})
	}
}

// TestThePlatformDraftOfEveryFixtureIsByteIdenticalAcrossRuns imports every
// committed Langflow and Flowise source twice under the default layout, over the
// platform catalog, and requires byte-identical results.
func TestThePlatformDraftOfEveryFixtureIsByteIdenticalAcrossRuns(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	for _, fx := range flowFixtures {
		t.Run(fx.golden, func(t *testing.T) {
			t.Parallel()
			rw := platformCatalogWorkspace(t, nil)
			a, _ := json.Marshal(importFlow(t, rw, fx.path))
			b, _ := json.Marshal(importFlow(t, rw, fx.path))
			if !bytes.Equal(a, b) {
				t.Errorf("two imports of one source differ:\n first: %s\nsecond: %s", a, b)
			}
		})
	}
}

// plantedSource is a source carrying key-shaped values, keys, every one of one
// class: in fields its format marks as credentials or in an MCP server's
// members, which the empty_credentials setting writes empty at marked and at
// all; or, where unmarked is set, in inputs its format marks as no credential
// that can carry one, which it writes empty at all alone. Every other setting
// value carries them as written. fields are the fields, by node and field, the
// default layout locates holding a value — every planted one, and any other
// value of that class the source holds — in the order the import reports them.
type plantedSource struct {
	name     string
	body     []byte
	keys     []string
	fields   []string
	unmarked bool
}

// plantedSources plants key-shaped values in copies of the real exports — a
// Langflow field marked password, the credential a Flowise chat model selects and
// an input of type password beside it, the credential an agentflow agent keeps in
// its model configuration — and in a Langflow flow whose MCP Tools nodes carry a
// server configuration with an address, a key in the query of one and in the user
// part of the other, and header and environment values, or with a command and
// arguments starting a server: a list of them, and a single one written as text.
// It plants them too in a Langflow flow AUTHORED here, after the layout of an MCP
// Tools node that carries no mcp_server field and writes its server in fields of
// its own, which no committed export holds: a key in the address in sse_url, in
// its query and in its user part; in a header value of headers_input, written as
// a list of key and value items and as an object; in an entry of env, written as
// a list and as one text; and in the command line in command. It plants them in a
// Langflow flow AUTHORED here whose MCP Tools nodes carry the headers field
// Langflow gives the node beside its server, which no committed export holds with
// a value: written as an object, as a list of key and value items, as a list of
// objects Langflow merges into one, and as text, on nodes naming their server in
// mcp_server and on one carrying no mcp_server, beside an empty object and null,
// which are left as written and not reported. And it plants them in a Langflow
// flow AUTHORED here holding a node of each of Langflow's deprecated MCP
// components, which no committed export holds: the address in the url of an
// MCPSse node and the command line in the command of an MCPStdio node. And it
// plants them in every input either format marks as no credential that can carry
// one, as unmarkedPlantedSources and queryPlantedSources describe. The catalog
// the planted sources are imported over holds no platform, so no node of any of
// these flows is wired to anything.
func plantedSources(t *testing.T) []plantedSource {
	t.Helper()
	decode := func(rel string) map[string]any {
		var doc map[string]any
		if err := json.Unmarshal(readRealExport(t, rel), &doc); err != nil {
			t.Fatalf("decode %s: %v", rel, err)
		}
		return doc
	}
	encode := func(doc any) []byte {
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatalf("encode the planted source: %v", err)
		}
		return append(b, '\n')
	}
	asMap := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	const (
		langflowKey = "sk-plantedLANGFLOWkey0123456789abcdef"
		flowiseCred = "sk-plantedFLOWISEcredential0123456789"
		flowisePass = "sk-plantedFLOWISEpassword0123456789ab"
		agentKey    = "sk-plantedAGENTFLOWkey0123456789abcd"
		mcpHeader   = "Bearer sk-plantedMCPheader012345678"
		mcpEnv      = "sk-plantedMCPenv0123456789abcdef01"
		mcpArg      = "sk-plantedMCParg0123456789abcdef01"
		mcpArgText  = "--token=sk-plantedMCPargTEXT01234567"
		mcpQuery    = "sk-plantedMCPaddressQUERY0123456789"
		mcpUser     = "sk-plantedMCPaddressUSER01234567890"

		fieldsQuery       = "sk-plantedFIELDSaddressQUERY01234567"
		fieldsUser        = "sk-plantedFIELDSaddressUSER012345678"
		fieldsHeader      = "Bearer sk-plantedFIELDSheader012345678"
		fieldsHeaderValue = "sk-plantedFIELDSheaderOBJECT0123456"
		fieldsEnv         = "sk-plantedFIELDSenv0123456789abcdef"
		fieldsEnvText     = "sk-plantedFIELDSenvTEXT0123456789ab"
		fieldsCommand     = "sk-plantedFIELDScommand0123456789ab"
		fieldsCommandText = "sk-plantedFIELDScommandTEXT01234567"

		headersObject  = "Bearer sk-plantedHEADERSobject01234567"
		headersItem    = "sk-plantedHEADERSitem0123456789abcde"
		headersMerged  = "Bearer sk-plantedHEADERSmerged0123456"
		headersText    = "Authorization: Bearer sk-plantedHEADERStext0123"
		deactivatedURL = "sk-plantedSSEaddress0123456789abcdef"
		deactivatedCmd = "sk-plantedSTDIOcommand0123456789abcd"
	)

	lf := decode(langflowPromptingProject)
	for _, n := range asMap(lf["data"])["nodes"].([]any) {
		template := asMap(asMap(asMap(asMap(n)["data"])["node"])["template"])
		if field := asMap(template["api_key"]); field != nil && field["password"] == true {
			field["value"] = langflowKey
		}
	}

	fw := decode(flowiseChatflow)
	for _, n := range fw["nodes"].([]any) {
		if asMap(n)["id"] != "chatOpenAI_0" {
			continue
		}
		data := asMap(asMap(n)["data"])
		data["credential"] = flowiseCred
		data["inputParams"] = append(data["inputParams"].([]any),
			map[string]any{"label": "API Key Override", "name": "apiKeyOverride", "type": "password"})
		asMap(data["inputs"])["apiKeyOverride"] = flowisePass
	}

	af := decode(flowiseAgentflow)
	for _, n := range af["nodes"].([]any) {
		if config := asMap(asMap(asMap(asMap(n)["data"])["inputs"])["agentModelConfig"]); config != nil && asMap(n)["id"] == "agentAgentflow_0" {
			config["credential"] = agentKey
		}
	}

	server := func(name, address string, headers any, env any) map[string]any {
		config := map[string]any{"url": address, "headers": headers}
		if env != nil {
			config["env"] = env
		}
		return map[string]any{"data": map[string]any{"type": "MCPTools", "node": map[string]any{"template": map[string]any{
			"mcp_server": map[string]any{"type": "mcp", "value": map[string]any{"name": name, "config": config}},
		}}}}
	}
	docs := server("docs", "http://docs.example/mcp?key="+mcpQuery,
		map[string]any{"Authorization": mcpHeader, "X-Empty": ""}, map[string]any{"API_KEY": mcpEnv})
	docs["id"] = "MCPTools-a1"
	search := server("search", "http://"+mcpUser+"@search.example/mcp",
		[]any{map[string]any{"key": "Authorization", "value": mcpHeader}}, nil)
	search["id"] = "MCPTools-b2"
	started := func(id, name string, args any) map[string]any {
		return map[string]any{"id": id, "data": map[string]any{"type": "MCPTools", "node": map[string]any{"template": map[string]any{
			"mcp_server": map[string]any{"type": "mcp", "value": map[string]any{"name": name,
				"config": map[string]any{"command": "uvx", "args": args}}},
		}}}}
	}
	fetch := started("MCPTools-c3", "fetch", []any{"--api-key", mcpArg})
	files := started("MCPTools-d4", "files", mcpArgText)
	mcp := map[string]any{"id": "0a0b0c0d-1111-4222-8333-444455556666",
		"data": map[string]any{"nodes": []any{docs, search, fetch, files}, "edges": []any{}}}

	// inFields AUTHORS an MCP Tools node in the layout that carries no mcp_server
	// field, which no committed export holds: its server in the fields mode,
	// sse_url, headers_input, env and command, each holding its value in a value
	// member.
	inFields := func(id, mode, address string, headers, env any, command string) map[string]any {
		field := func(typ string, value any) map[string]any { return map[string]any{"type": typ, "value": value} }
		return map[string]any{"id": id, "data": map[string]any{"type": "MCPTools", "node": map[string]any{"template": map[string]any{
			"mode": field("tab", mode), "sse_url": field("str", address), "headers_input": field("table", headers),
			"env": field("str", env), "command": field("str", command), "tool": field("str", "fetch"),
		}}}}
	}
	sse := inFields("MCPTools-e5", "SSE", "http://mcp.example/sse?token="+fieldsQuery,
		[]any{map[string]any{"key": "Authorization", "value": fieldsHeader}},
		[]any{"DEBUG=true", "API_KEY=" + fieldsEnv}, "uvx mcp-server-fetch --api-key="+fieldsCommand)
	stdio := inFields("MCPTools-f6", "Stdio", "http://"+fieldsUser+"@mcp.example/sse",
		map[string]any{"X-Api-Key": fieldsHeaderValue}, "TOKEN="+fieldsEnvText, "npx -y mcp-server --token "+fieldsCommandText)
	fieldsFlow := map[string]any{"id": "0a0b0c0d-2222-4333-8444-555566667777",
		"data": map[string]any{"nodes": []any{sse, stdio}, "edges": []any{}}}

	// withHeaders AUTHORS an MCP Tools node carrying the headers field Langflow
	// gives the node beside its server — a dict field holding the headers as
	// written — which no committed export holds with a value. A node naming its
	// server in mcp_server names it alone, so nothing else of it is written
	// empty; one carrying no mcp_server holds an empty address and an empty
	// command line in fields of its own, which are left as written.
	withHeaders := func(id, server string, headers any) map[string]any {
		template := map[string]any{"headers": map[string]any{"type": "dict", "list": true, "value": headers},
			"tool": map[string]any{"type": "str", "value": "fetch"}}
		if server != "" {
			template["mcp_server"] = map[string]any{"type": "mcp", "value": server}
		} else {
			template["sse_url"] = map[string]any{"type": "str", "value": ""}
			template["command"] = map[string]any{"type": "str", "value": ""}
		}
		return map[string]any{"id": id, "data": map[string]any{"type": "MCPTools", "node": map[string]any{"template": template}}}
	}
	headersFlow := map[string]any{"id": "0a0b0c0d-3333-4444-8555-666677778888", "data": map[string]any{"nodes": []any{
		withHeaders("MCPTools-g7", "docs", map[string]any{"Authorization": headersObject}),
		withHeaders("MCPTools-h8", "search", []any{map[string]any{"key": "X-Api-Key", "value": headersItem}}),
		withHeaders("MCPTools-i9", "", []any{map[string]any{"Authorization": headersMerged}}),
		withHeaders("MCPTools-j0", "files", headersText),
		withHeaders("MCPTools-k1", "fetch", map[string]any{}),
		withHeaders("MCPTools-l2", "notes", nil),
	}, "edges": []any{}}}

	// deactivated AUTHORS a node of one of Langflow's deprecated MCP components,
	// which no committed export holds: its one field, holding the server's
	// address or the command line starting it.
	deactivated := func(id, typ, field, value string) map[string]any {
		return map[string]any{"id": id, "data": map[string]any{"type": typ, "node": map[string]any{"template": map[string]any{
			field: map[string]any{"type": "str", "value": value},
		}}}}
	}
	deactivatedFlow := map[string]any{"id": "0a0b0c0d-4444-4555-8666-777788889999", "data": map[string]any{"nodes": []any{
		deactivated("MCPSse-m3", "MCPSse", "url", "http://mcp.example/sse?token="+deactivatedURL),
		deactivated("MCPStdio-n4", "MCPStdio", "command", "uvx mcp-server-fetch --api-key "+deactivatedCmd),
	}, "edges": []any{}}}

	return append([]plantedSource{
		{name: "langflow-password.json", body: encode(lf), keys: []string{langflowKey},
			fields: []string{"LanguageModelComponent-FLeYF api_key"}},
		{name: "flowise-credential.json", body: encode(fw), keys: []string{flowiseCred, flowisePass},
			fields: []string{"chatOpenAI_0 credential", "chatOpenAI_0 apiKeyOverride"}},
		{name: "agentflow-credential.json", body: encode(af), keys: []string{agentKey},
			fields: []string{"agentAgentflow_0 agentModelConfig.credential"}},
		{name: "langflow-mcp-server.json", body: encode(mcp),
			keys: []string{mcpQuery, mcpUser, mcpHeader, mcpEnv, mcpArg, mcpArgText},
			fields: []string{"MCPTools-a1 mcp_server.config.headers.Authorization", "MCPTools-a1 mcp_server.config.env.API_KEY",
				"MCPTools-a1 mcp_server.config.url", "MCPTools-b2 mcp_server.config.headers.Authorization",
				"MCPTools-b2 mcp_server.config.url", "MCPTools-c3 mcp_server.config.command",
				"MCPTools-c3 mcp_server.config.args.0", "MCPTools-c3 mcp_server.config.args.1",
				"MCPTools-d4 mcp_server.config.command", "MCPTools-d4 mcp_server.config.args"}},
		{name: "langflow-mcp-fields.json", body: encode(fieldsFlow),
			keys: []string{fieldsQuery, fieldsUser, fieldsHeader, fieldsHeaderValue, fieldsEnv, fieldsEnvText, fieldsCommand,
				fieldsCommandText},
			fields: []string{"MCPTools-e5 headers_input.Authorization", "MCPTools-e5 env.0", "MCPTools-e5 env.1",
				"MCPTools-e5 sse_url", "MCPTools-e5 command", "MCPTools-f6 headers_input.X-Api-Key", "MCPTools-f6 env",
				"MCPTools-f6 sse_url", "MCPTools-f6 command"}},
		{name: "langflow-mcp-headers.json", body: encode(headersFlow),
			keys:   []string{headersObject, headersItem, headersMerged, headersText},
			fields: []string{"MCPTools-g7 headers", "MCPTools-h8 headers", "MCPTools-i9 headers", "MCPTools-j0 headers"}},
		{name: "langflow-mcp-deactivated.json", body: encode(deactivatedFlow),
			keys:   []string{deactivatedURL, deactivatedCmd},
			fields: []string{"MCPSse-m3 url", "MCPStdio-n4 command"}},
	}, append(unmarkedPlantedSources(encode), queryPlantedSources(encode)...)...)
}

// queryPlantedSources plants key-shaped values in a Langflow flow and a Flowise
// flow AUTHORED here, which no committed export holds with a value, in every input
// either format sends as a request's query parameters, in each shape the input
// takes. In the Langflow flow: an API Request node's query parameters as an
// object and as text, a Google Serper node's as an object, a SerpAPI node's as
// rows of a key and a value, a Serp node's as an object, a SearchApi node's as an
// object Langflow merges whole and a second SearchApi node's as an object, each
// beside a parameter holding no secret, which is written empty too; the default
// query of an OpenAI Embeddings node; and, left as written and not reported, an
// API Request node's body and a ClickHouse node's index settings, which are no
// query parameters. In the Flowise flow: an HTTP node's query parameters as rows,
// beside its body, which is left as written; a SearchApi loader's custom
// parameters as JSON text and as an object; and, left as written, a Spider
// loader's parameters, which it sends as a request body.
func queryPlantedSources(encode func(any) []byte) []plantedSource {
	const (
		lfObject   = "sk-plantedQUERYapiREQUEST0123456789"
		lfText     = "sk-plantedQUERYapiREQUESTtext012345"
		lfSerper   = "sk-plantedQUERYserper0123456789abcd"
		lfSerpAPI  = "sk-plantedQUERYserpAPI0123456789abc"
		lfSerp     = "sk-plantedQUERYserp0123456789abcdef"
		lfMerged   = "sk-plantedQUERYsearchAPImerged01234"
		lfSearch   = "sk-plantedQUERYsearchCOMPONENT01234"
		lfEmbedQry = "sk-plantedQUERYembedDEFAULT01234567"

		fwHTTP       = "sk-plantedQUERYhttpAGENTFLOW012345"
		fwCustomText = "sk-plantedQUERYsearchApiTEXT012345"
		fwCustomObj  = "sk-plantedQUERYsearchApiOBJECT0123"
	)
	field := func(typ string, value any) map[string]any { return map[string]any{"type": typ, "value": value} }
	lfNode := func(id, typ string, template map[string]any) map[string]any {
		return map[string]any{"id": id, "data": map[string]any{"type": typ, "node": map[string]any{"template": template}}}
	}
	langflow := map[string]any{"id": "0a0b0c0d-6666-4777-8888-9999aaaabbbb", "data": map[string]any{"nodes": []any{
		lfNode("APIRequest-q1", "APIRequest", map[string]any{
			"query_params": field("other", map[string]any{"api_key": lfObject, "page": "2"}),
			"body":         field("table", []any{map[string]any{"key": "topic", "value": "left as written"}})}),
		lfNode("APIRequest-q2", "APIRequest", map[string]any{"query_params": field("other", "api_key="+lfText+"&page=2")}),
		lfNode("GoogleSerperAPI-q3", "GoogleSerperAPI", map[string]any{
			"query_params": field("dict", map[string]any{"gl": "us", "hl": "en", "api_key": lfSerper})}),
		lfNode("SerpAPI-q4", "SerpAPI", map[string]any{"search_params": field("dict", []any{
			map[string]any{"key": "api_key", "value": lfSerpAPI}, map[string]any{"key": "engine", "value": "google"}})}),
		lfNode("Serp-q5", "Serp", map[string]any{"search_params": field("dict", map[string]any{"api_key": lfSerp})}),
		lfNode("SearchAPI-q6", "SearchAPI", map[string]any{"search_params": field("dict",
			[]any{map[string]any{"api_key": lfMerged}})}),
		lfNode("SearchComponent-q7", "SearchComponent", map[string]any{"search_params": field("dict",
			map[string]any{"api_key": lfSearch, "location": "Rome"})}),
		lfNode("OpenAIEmbeddings-q8", "OpenAIEmbeddings", map[string]any{
			"default_query": field("dict", map[string]any{"api-version": "1", "api_key": lfEmbedQry})}),
		lfNode("Clickhouse-q9", "Clickhouse", map[string]any{
			"index_query_params": field("dict", map[string]any{"allow_experimental_annoy_index": "1"})}),
	}, "edges": []any{}}}

	fwNode := func(id, name string, inputs map[string]any) map[string]any {
		return map[string]any{"id": id, "data": map[string]any{"id": id, "name": name, "inputs": inputs}}
	}
	flowise := map[string]any{"nodes": []any{
		fwNode("httpAgentflow_0", "httpAgentflow", map[string]any{
			"queryParams": []any{map[string]any{"key": "api_key", "value": fwHTTP}, map[string]any{"key": "page", "value": "2"}},
			"body":        []any{map[string]any{"key": "topic", "value": "left as written"}}}),
		fwNode("searchApi_0", "searchApi", map[string]any{
			"customParameters": `{"engine":"google","api_key":"` + fwCustomText + `"}`}),
		fwNode("searchApi_1", "searchApi", map[string]any{"customParameters": map[string]any{"api_key": fwCustomObj}}),
		fwNode("spiderDocumentLoaders_0", "spiderDocumentLoaders", map[string]any{"params": `{"limit":5}`}),
	}, "edges": []any{}}

	return []plantedSource{
		{name: "langflow-query-parameters.json", body: encode(langflow), unmarked: true,
			keys: []string{lfObject, lfText, lfSerper, lfSerpAPI, lfSerp, lfMerged, lfSearch, lfEmbedQry},
			fields: []string{"APIRequest-q1 query_params.api_key", "APIRequest-q1 query_params.page",
				"APIRequest-q2 query_params", "GoogleSerperAPI-q3 query_params.api_key", "GoogleSerperAPI-q3 query_params.gl",
				"GoogleSerperAPI-q3 query_params.hl", "SerpAPI-q4 search_params.api_key", "SerpAPI-q4 search_params.engine",
				"Serp-q5 search_params.api_key", "SearchAPI-q6 search_params.api_key",
				"SearchComponent-q7 search_params.api_key", "SearchComponent-q7 search_params.location",
				"OpenAIEmbeddings-q8 default_query"}},
		{name: "flowise-query-parameters.json", body: encode(flowise), unmarked: true,
			keys: []string{fwHTTP, fwCustomText, fwCustomObj},
			fields: []string{"httpAgentflow_0 queryParams.api_key", "httpAgentflow_0 queryParams.page",
				"searchApi_0 customParameters", "searchApi_1 customParameters.api_key"}},
	}
}

// unmarkedPlantedSources plants key-shaped values in a Langflow flow and a Flowise
// flow AUTHORED here, which no committed export holds with a value, in every input
// either format marks as no credential that can carry one, in each shape the
// input takes. In the Langflow flow: request headers as rows of a key and a value
// (beside a header holding no secret, an empty one and a row naming a header
// alone), as an object, as objects Langflow merges whole and as text; the default
// headers of an OpenAI Embeddings node as an object and as a list; a request
// written as a command line, a Webhook node's command line (and its declared
// default, written empty too though it holds no key); an Olivya API key; a Mem0
// configuration; a Composio verification token; and two inputs named like
// credentials that hold none — an Astra key's name and a user name — left as
// written. In the Flowise flow: headers as JSON text, as rows, as an object and as
// text that is not JSON; the default headers of an OpenAI chat model, which no
// connection reaches here, and of an Azure one; a Start node's two secrets; a
// Teradata MCP tool's bearer token; and, inside an agentflow agent's
// configurations of the model and the tool it selects, the default headers of an
// OpenAI chat model and a Requests tool's headers.
func unmarkedPlantedSources(encode func(any) []byte) []plantedSource {
	const (
		lfAuthorization = "Bearer sk-plantedUNMARKEDrows0123456789"
		lfCurl          = "sk-plantedUNMARKEDcurl0123456789abc"
		lfMerged        = "sk-plantedUNMARKEDmerged0123456789a"
		lfItem          = "Authorization: Bearer sk-plantedUNMARKEDitem012"
		lfDocling       = "sk-plantedUNMARKEDdocling0123456789"
		lfCloudflare    = "Bearer sk-plantedUNMARKEDcloudflare012"
		lfKafka         = "trace=sk-plantedUNMARKEDkafka01234567"
		lfEmbedObject   = "Bearer sk-plantedUNMARKEDembedOBJECT01"
		lfEmbedList     = "Bearer sk-plantedUNMARKEDembedLIST0123"
		lfWebhook       = "sk-plantedUNMARKEDwebhook012345678"
		lfOlivya        = "sk-plantedUNMARKEDolivya0123456789a"
		lfMem0          = "sk-plantedUNMARKEDmem0password01234"
		lfComposio      = "sk-plantedUNMARKEDcomposio01234567"

		fwRequestsGet = "Bearer sk-plantedUNMARKEDrequestsGET0"
		fwHTTP        = "sk-plantedUNMARKEDhttpAGENTFLOW0123"
		fwLoader      = "sk-plantedUNMARKEDapiLOADER01234567"
		fwToolkit     = "sk-plantedUNMARKEDtoolkitTEXT01234"
		fwChatOpenAI  = "Bearer sk-plantedUNMARKEDbaseOPTIONS0"
		fwAzure       = "sk-plantedUNMARKEDazureBASE01234567"
		fwWebhook     = "sk-plantedUNMARKEDwebhookSECRET0123"
		fwCallback    = "sk-plantedUNMARKEDcallbackSECRET012"
		fwTeradata    = "sk-plantedUNMARKEDteradataBEARER012"
		fwNestedModel = "Bearer sk-plantedUNMARKEDnestedMODEL0"
		fwNestedTool  = "sk-plantedUNMARKEDnestedTOOL0123456"
	)
	field := func(typ string, value any) map[string]any { return map[string]any{"type": typ, "value": value} }
	lfNode := func(id, typ string, template map[string]any) map[string]any {
		return map[string]any{"id": id, "data": map[string]any{"type": typ, "node": map[string]any{"template": template}}}
	}
	langflow := map[string]any{"id": "0a0b0c0d-5555-4666-8777-88889999aaaa", "data": map[string]any{"nodes": []any{
		lfNode("APIRequest-a1", "APIRequest", map[string]any{
			"headers": field("table", []any{map[string]any{"key": "Authorization", "value": lfAuthorization},
				map[string]any{"key": "User-Agent", "value": "Langflow/1.0"}, map[string]any{"key": "X-Empty", "value": ""},
				map[string]any{"key": "X-Name-Only"}}),
			"curl_input": field("str", "curl -H 'Authorization: Bearer "+lfCurl+"' https://api.example/v1/items")}),
		lfNode("URLComponent-b2", "URLComponent", map[string]any{
			"headers": field("table", []any{map[string]any{"X-Api-Key": lfMerged}, lfItem})}),
		lfNode("DoclingRemote-c3", "DoclingRemote", map[string]any{
			"api_headers": field("table", []any{map[string]any{"key": "Authorization", "value": lfDocling}})}),
		lfNode("CloudflareWorkersAIEmbeddings-d4", "CloudflareWorkersAIEmbeddings", map[string]any{
			"headers": field("dict", map[string]any{"Authorization": lfCloudflare})}),
		lfNode("ConfluentKafkaProducer-e5", "ConfluentKafkaProducer", map[string]any{"headers": field("dict", lfKafka)}),
		lfNode("OpenAIEmbeddings-f6", "OpenAIEmbeddings", map[string]any{
			"default_headers": field("dict", map[string]any{"Authorization": lfEmbedObject})}),
		lfNode("OpenAIEmbeddings-g7", "OpenAIEmbeddings", map[string]any{
			"default_headers": field("dict", []any{map[string]any{"key": "Authorization", "value": lfEmbedList}})}),
		lfNode("Webhook-h8", "Webhook", map[string]any{"curl": field("str", "curl -X POST -u hook:"+lfWebhook+
			" https://hooks.example/run")}),
		lfNode("Webhook-h9", "Webhook", map[string]any{"curl": field("str", "CURL_WEBHOOK")}),
		lfNode("OlivyaComponent-i10", "OlivyaComponent", map[string]any{"api_key": field("str", lfOlivya)}),
		lfNode("mem0_chat_memory-j11", "mem0_chat_memory", map[string]any{"mem0_config": field("NestedDict",
			map[string]any{"graph_store": map[string]any{"config": map[string]any{"username": "neo4j", "password": lfMem0}}})}),
		lfNode("ComposioGmailAPIComponent-k12", "ComposioGmailAPIComponent", map[string]any{
			"verification_token": field("str", lfComposio)}),
		lfNode("AstraVectorize-l13", "AstraVectorize", map[string]any{"api_key_name": field("str", "PROVIDER_KEY_NAME")}),
		lfNode("Elasticsearch-m14", "Elasticsearch", map[string]any{"username": field("str", "elastic")}),
	}, "edges": []any{}}}

	fwNode := func(id, name string, inputs map[string]any) map[string]any {
		return map[string]any{"id": id, "data": map[string]any{"id": id, "name": name, "inputs": inputs}}
	}
	flowise := map[string]any{"nodes": []any{
		fwNode("requestsGet_0", "requestsGet", map[string]any{
			"requestsGetHeaders": `{"Authorization":"` + fwRequestsGet + `","Accept":"application/json"}`}),
		fwNode("httpAgentflow_0", "httpAgentflow", map[string]any{
			"headers": []any{map[string]any{"key": "Authorization", "value": fwHTTP}}}),
		fwNode("apiLoader_0", "apiLoader", map[string]any{"headers": map[string]any{"X-Api-Key": fwLoader}}),
		fwNode("openAPIToolkit_0", "openAPIToolkit", map[string]any{"headers": "token " + fwToolkit}),
		fwNode("chatOpenAI_0", "chatOpenAI", map[string]any{"baseOptions": map[string]any{"Authorization": fwChatOpenAI}}),
		fwNode("azureChatOpenAI_0", "azureChatOpenAI", map[string]any{"baseOptions": `{"api-key":"` + fwAzure + `"}`}),
		fwNode("startAgentflow_0", "startAgentflow", map[string]any{"webhookSecret": fwWebhook,
			"callbackSecret": fwCallback, "webhookSignatureHeader": "x-hub-signature-256"}),
		fwNode("teradataMCP_0", "teradataMCP", map[string]any{"bearerToken": fwTeradata}),
		fwNode("agentAgentflow_0", "agentAgentflow", map[string]any{
			"agentModel": "chatOpenAI",
			"agentModelConfig": map[string]any{"agentModel": "chatOpenAI",
				"baseOptions": `{"Authorization":"` + fwNestedModel + `"}`},
			"agentTools": []any{map[string]any{"agentSelectedTool": "requestsPost",
				"agentSelectedToolConfig": map[string]any{"agentSelectedTool": "requestsPost",
					"requestsPostHeaders": `{"X-Key":"` + fwNestedTool + `"}`}}},
		}),
	}, "edges": []any{}}

	return []plantedSource{
		{name: "langflow-unmarked-inputs.json", body: encode(langflow), unmarked: true,
			keys: []string{lfAuthorization, lfCurl, lfMerged, lfItem, lfDocling, lfCloudflare, lfKafka, lfEmbedObject,
				lfEmbedList, lfWebhook, lfOlivya, lfMem0, lfComposio},
			fields: []string{"APIRequest-a1 curl_input", "APIRequest-a1 headers.Authorization",
				"APIRequest-a1 headers.User-Agent", "URLComponent-b2 headers.X-Api-Key", "URLComponent-b2 headers.1",
				"DoclingRemote-c3 api_headers.Authorization", "CloudflareWorkersAIEmbeddings-d4 headers.Authorization",
				"ConfluentKafkaProducer-e5 headers", "OpenAIEmbeddings-f6 default_headers",
				"OpenAIEmbeddings-g7 default_headers", "Webhook-h8 curl", "Webhook-h9 curl", "OlivyaComponent-i10 api_key",
				"mem0_chat_memory-j11 mem0_config", "ComposioGmailAPIComponent-k12 verification_token"}},
		{name: "flowise-unmarked-inputs.json", body: encode(flowise), unmarked: true,
			keys: []string{fwRequestsGet, fwHTTP, fwLoader, fwToolkit, fwChatOpenAI, fwAzure, fwWebhook, fwCallback,
				fwTeradata, fwNestedModel, fwNestedTool},
			fields: []string{"requestsGet_0 requestsGetHeaders", "httpAgentflow_0 headers.Authorization",
				"apiLoader_0 headers.X-Api-Key", "openAPIToolkit_0 headers", "chatOpenAI_0 baseOptions",
				"azureChatOpenAI_0 baseOptions", "startAgentflow_0 callbackSecret", "startAgentflow_0 webhookSecret",
				"teradataMCP_0 bearerToken", "agentAgentflow_0 agentModelConfig.baseOptions",
				"agentAgentflow_0 agentTools.0.agentSelectedToolConfig.requestsPostHeaders"}},
	}
}

// emptiedField, emptiedWhole and emptiedParts read the node and the field a
// credential-emptied warning names, and nothing else of it: the first for a
// field the format marks as a credential, the second for one it writes empty
// whole for the reason the warning gives, the third for one it writes with what
// the warning names of it emptied. carriedWhole and carriedParts read the node,
// the field and the setting value a credential-carried warning names: the first
// for a value carried whole, the second for one carried with what the warning
// names of it as written.
var (
	emptiedField = regexp.MustCompile(`^the value of (\S+), which node (\S+) marks as a credential, is written empty in the flow the draft carries$`)
	emptiedWhole = regexp.MustCompile(`^the whole value of (\S+) of node (\S+) is written empty in the flow the draft carries, since [^:]+$`)
	emptiedParts = regexp.MustCompile(`^(\S+) of node (\S+) is written with its [^:]+ empty in the flow the draft carries$`)
	carriedWhole = regexp.MustCompile(`^the value of (\S+) of node (\S+) is carried as written in the flow the draft carries: [^:]+, and setting empty_credentials to (marked or all|all) writes it empty$`)
	carriedParts = regexp.MustCompile(`^(\S+) of node (\S+) is carried with its [^:]+ as written in the flow the draft carries: [^:]+, and setting empty_credentials to (marked or all|all) writes its [^:]+ empty$`)
)

// TestTheEmptyCredentialsSettingDecidesWhatAnExportCarries plants key-shaped
// values in the fields Langflow and Flowise mark as credentials — a field marked
// password, the credential a node's credential parameter selects, an input of
// type password, an agentflow model's credential — in an MCP server's members —
// its header and environment values, its address and the arguments it is
// started with, the address, the header values, the environment entries and the
// command line an MCP Tools node writes in fields of its own, the headers an MCP
// Tools node sends its server, and the address and the command line of
// Langflow's deprecated MCP components — and in every input either format marks
// as no credential that can carry one, the default headers and query of an
// OpenAI node no connection reaches included, and imports each planted source
// under both layouts, with the empty_credentials setting absent, none, marked
// and all, writing the drafts, as plantedCredentialRow holds each:
//
//   - absent and none write nothing empty: every planted value is carried as
//     written, and one credential-carried warning names each located field, by
//     node and field, with the setting value that writes it empty;
//   - marked writes empty the values of the sources planting in marked fields
//     and in MCP servers' members, each reported under credential-emptied, and
//     carries the others as the default does;
//   - all writes every planted value empty.
//
// No planted value reaches a decision, a diagnostic, a comment of the drafted
// aggregate.yaml or any other file the import wrote, and the node-by-node draft,
// which reads no value, carries none and reports none.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source. A
// row named below is the source imported under the default layout:
//
//   - in carryCredentials (aggrega-pdk/go/pkg/importer/credentials.go), replace
//     `case removal.empties(c.Class):` with `case true:` — a marked value
//     emptied under the default: every absent and none row fails, its values
//     missing from the drafted aggregate.yaml and reported as emptied;
//   - in carryCredentials, replace
//     `case verdicts[i] == carried && holdsValue(tree, c):` with `case false:` —
//     the new warning dropped: every absent and none row fails, no warning naming
//     its fields;
//   - in readCredentialRemoval (credentials.go), replace
//     `return credentialRemoval{level: level}, nil` with
//     `return credentialRemoval{level: emptyNone}, nil` — the setting read as
//     none: every marked and all row fails, its values in the drafted
//     aggregate.yaml;
//   - in serverCredentials (plugins/langflow-importer/walk.go), replace
//     `c := importer.Credential{Node: nodeID, Class: importer.CredentialServerMember,`
//     with `c := importer.Credential{Node: nodeID, Class: importer.CredentialUnmarked,`
//     — an MCP server's members classed as unmarked: the row
//     langflow-mcp-server.json under marked fails, its values in the drafted
//     aggregate.yaml;
//   - in credentialsOf (plugins/langflow-importer/walk.go), replace
//     `if node.typ == mcpToolsType && node.template.member("mcp_server") == nil {`
//     with `if node.typ == mcpToolsType && node.template.member("mcp_server") == nil && false {`:
//     the row langflow-mcp-fields.json fails under every setting, no warning
//     naming any of its fields, and under marked and all its values in the
//     drafted aggregate.yaml;
//   - in FieldMembers (plugins/internal/mcpconfig/mcpconfig.go), replace
//     `out = append(out, listMembers(Env, v, "env", "value")...)` with `_ = v`:
//     the row langflow-mcp-fields.json fails, no warning naming env of either
//     node;
//   - in unmarkedOf (plugins/langflow-importer/walk.go), replace
//     `if u, ok := unmarkedFields[typ][field]; ok {` with
//     `if u, ok := unmarkedFields[typ][field]; ok && false {`: the rows
//     langflow-mcp-headers.json, langflow-mcp-deactivated.json,
//     langflow-unmarked-inputs.json and langflow-query-parameters.json fail, no
//     warning naming their fields;
//   - in unmarkedOf (plugins/langflow-importer/walk.go), replace
//     `u, ok := unmarkedAnyType[field]` with `u, ok := unmarkedAnyType[field+"-"]`:
//     the row langflow-unmarked-inputs.json fails, the Composio token named in no
//     warning;
//   - in locate (plugins/langflow-importer/walk.go), replace each of
//     `case headerValues:`, `case wholeKept:` and `case wholeText:` with the same
//     case plus 100: the rows holding a value of that treatment fail;
//   - in HeaderValues (plugins/internal/mcpconfig/mcpconfig.go), replace
//     `case item.Key == nil:` with `case item.Key == nil && false:`, or
//     `if !holdsNothing(raw) {` with `if !holdsNothing(raw) && false {`: the row
//     langflow-unmarked-inputs.json fails, the URL node's merged header, or its
//     header written as text, named in no warning;
//   - in unmarkedOf (plugins/flowise-importer/walk.go), replace
//     `u, ok := unmarkedInputs[typ][input]` with `u, ok := unmarkedInputs[typ][input+"-"]`:
//     the row flowise-unmarked-inputs.json fails;
//   - in selectedType (plugins/flowise-importer/walk.go), replace
//     `if selectorKeys[e.Key] && json.Unmarshal(e.Value, &selected) == nil && selected != "" {`
//     with the same condition `&& false`: the row flowise-unmarked-inputs.json
//     fails, both values planted in the agent's configurations named in no
//     warning;
//   - in unmarkedInputs (plugins/flowise-importer/walk.go), delete the entry
//     `"queryParams": queryParameters` and the row of searchApi: the row
//     flowise-query-parameters.json fails, no warning naming its fields.
func TestTheEmptyCredentialsSettingDecidesWhatAnExportCarries(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	for _, src := range plantedSources(t) {
		for _, setting := range []string{"absent", "none", "marked", "all"} {
			for _, layout := range []string{"platform", "nodes"} {
				t.Run(src.name+", "+setting+", "+layout, func(t *testing.T) {
					t.Parallel()
					plantedCredentialRow(t, src, setting, layout)
				})
			}
		}
	}
}

// plantedCredentialRow imports one planted source with the empty_credentials
// setting at the value given, or absent, and the layout given, writing the
// drafts, and holds them to the source's expectations: under the default layout,
// every value the setting writes empty is in no file the import wrote, and every
// value it carries is in the drafted aggregate.yaml as written, in the flow and
// in no comment; one warning names each located field — under credential-emptied
// where the setting writes it empty, under credential-carried, with the setting
// value that writes it empty, where it does not — by its node and field,
// carrying no value; under the node-by-node layout no planted value is in any
// file the import wrote and no warning names one. No planted value is in the
// decisions or the diagnostics. The search finds every planted value in the
// source, which is what says it fires.
func plantedCredentialRow(t *testing.T, src plantedSource, setting, layout string) {
	t.Helper()
	settings := map[string]any{"layout": layout}
	if setting != "absent" {
		settings["empty_credentials"] = setting
	}
	emptiedHere := setting == "all" || (setting == "marked" && !src.unmarked)
	level := "marked or all"
	if src.unmarked {
		level = "all"
	}
	holds := func(b []byte, keys []string) []string {
		var found []string
		for _, key := range keys {
			if bytes.Contains(b, []byte(key)) {
				found = append(found, key)
			}
		}
		return found
	}
	if found := holds(src.body, src.keys); len(found) != len(src.keys) {
		t.Fatalf("the needle: the search finds %d of the %d values planted in the source, so it does not fire",
			len(found), len(src.keys))
	}

	rw, _ := catalogFolderImportWorkspaceAttaching(t, settings)
	path := importopstest.WriteSource(t, filepath.Join(t.TempDir(), src.name), string(src.body))
	outputDir := filepath.Join(rw.Root(), "drafts")
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    path,
		SourceName:    src.name,
		OutputDir:     outputDir,
		Delivery:      authoredcontent.DeliveryFor(false),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if len(res.Written) == 0 {
		t.Fatal("the import wrote nothing to search")
	}
	carriedInFlow := layout == "platform" && !emptiedHere
	for _, rel := range res.Written {
		content, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the written %s: %v", rel, err)
		}
		if rel != "aggregate.yaml" || !carriedInFlow {
			if found := holds(content, src.keys); len(found) != 0 {
				t.Errorf("the written %s holds the planted values %q", rel, found)
			}
			continue
		}
		if found := holds(content, src.keys); len(found) != len(src.keys) {
			t.Errorf("the drafted aggregate.yaml carries %q of the planted values, want every one %q", found, src.keys)
		}
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				if found := holds([]byte(line), src.keys); len(found) != 0 {
					t.Errorf("a comment of the drafted aggregate.yaml holds the planted values %q: %q", found, line)
				}
			}
		}
	}
	for _, part := range []struct {
		what  string
		value any
	}{{"decisions", res.Decisions}, {"diagnostics", res.Diagnostics}} {
		raw, _ := json.Marshal(part.value)
		if found := holds(raw, src.keys); len(found) != 0 {
			t.Errorf("the import's %s hold the planted values %q", part.what, found)
		}
	}

	var emptied, carried []string
	for _, d := range res.Diagnostics {
		switch d.Code {
		case "credential-emptied":
			m := emptiedField.FindStringSubmatch(d.Message)
			if m == nil {
				m = emptiedWhole.FindStringSubmatch(d.Message)
			}
			if m == nil {
				m = emptiedParts.FindStringSubmatch(d.Message)
			}
			if m == nil {
				t.Errorf("the warning %q is not of the form that names a node and a field alone", d.Message)
				continue
			}
			emptied = append(emptied, m[2]+" "+m[1])
		case "credential-carried":
			m := carriedWhole.FindStringSubmatch(d.Message)
			if m == nil {
				m = carriedParts.FindStringSubmatch(d.Message)
			}
			if m == nil {
				t.Errorf("the warning %q is not of the form that names a node, a field and a setting value alone",
					d.Message)
				continue
			}
			if m[3] != level {
				t.Errorf("the warning %q names %q as what writes the value empty, want %q", d.Message, m[3], level)
			}
			carried = append(carried, m[2]+" "+m[1])
		}
	}
	var wantEmptied, wantCarried []string
	if layout == "platform" {
		if emptiedHere {
			wantEmptied = src.fields
		} else {
			wantCarried = src.fields
		}
	}
	if !slices.Equal(emptied, wantEmptied) {
		t.Errorf("the warnings name the emptied fields %q, want %q", emptied, wantEmptied)
	}
	if !slices.Equal(carried, wantCarried) {
		t.Errorf("the warnings name the carried fields %q, want %q", carried, wantCarried)
	}
}
