// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
	langflowFixture(realExportPath(langflowPromptingProject),
		"internal_compiler_importersocket_testdata_real-exports_langflow_Basic_Prompting",
		"c1cb7b3c-2589-4754-ac61-8cf88df4a07e", nil),
	langflowFixture(realExportPath(langflowStarterProject),
		"internal_compiler_importersocket_testdata_real-exports_langflow_Research_Translation_Loop",
		"7eb7c081-888c-4f25-ba81-c84e6aab2778", nil),
	langflowFixture(realExportPath(langflowRemixProject),
		"internal_compiler_importersocket_testdata_real-exports_langflow_Nvidia_Remix",
		"faac3377-a15b-459b-a6bd-c47c318dcc78", nil),
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
	return catalogComponent{name: name, version: "1.0.0", body: "apiVersion: aggrega.ai/v1alpha1\nkind: Component\n" +
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
	{name: langflowPlatform, version: "1.0.0", body: "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n" +
		"  name: core/ai-agent/langflow\n  version: 1.0.0\n  labels:\n    component.aggrega.ai/type: server\n" +
		"    component.aggrega.ai/category: ai-agent\nspec:\n" + platformSockets +
		"  schema:\n    values:\n      flow_id: { type: string }\n      flow_definition: { type: string }\n"},
	{name: flowisePlatform, version: "1.0.0", body: "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n" +
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

// uuidOfAPlatform is the shape a flow platform takes a flow identifier in.
var uuidOfAPlatform = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestEveryLangflowAndFlowiseFixtureIsDraftedAsThePlatformCarryingIt imports
// every committed Langflow and Flowise source under the default layout, over a
// catalog holding the two platforms and the Components their parts resemble, and
// reads the drafted Aggregate as the compiler reads one. For each:
//
//   - the Aggregate holds one instance of the source's platform, and its
//     definition value, once interpolated, is the source's own bytes — double
//     braces, a missing final line break and all;
//   - the flow's identifier is the source's own where it gives one, and a UUID of
//     the shape the platforms take where it gives none;
//   - each socket draws exactly the connections the table names, each reaching a
//     part drafted as a Component of its own, and nothing else is a Component;
//   - every node keeps a decision: a part's says where it is wired, and every
//     other node's chooses nothing and says the platform's flow keeps it.
func TestEveryLangflowAndFlowiseFixtureIsDraftedAsThePlatformCarryingIt(t *testing.T) {
	t.Parallel()
	for _, fx := range flowFixtures {
		t.Run(fx.golden, func(t *testing.T) {
			t.Parallel()
			res := importFlow(t, platformCatalogWorkspace(t, nil), fx.path)
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
			if got, _ := inst.Values["flow_definition"].(string); got != string(export) {
				t.Errorf("the definition the platform carries is not the source's bytes (%d bytes carried, %d exported)",
					len(got), len(export))
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
			if components := len(res.Files) - 1; components != len(wantParts) {
				t.Errorf("the import drafted %d Component(s) beside the Aggregate, want one per part, %d", components,
					len(wantParts))
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
	t.Parallel()
	overrides := map[string]any{"mapping_overrides": map[string]any{"flowise": map[string]any{
		"httpRequest": map[string]any{"component": "acme/ai-tool/web-fetch", "category": "ai-tool"},
		"pinecone":    map[string]any{"component": "acme/datastore/vectors", "category": "datastore"},
	}}}
	for _, tc := range []struct {
		name, source, uses string
		reported           []string
	}{
		{name: "a tool reached over HTTP", source: "../compiler/importersocket/testdata/flowise-flow.json",
			uses: "      uses:\n        model:\n          chatopenai-0:\n            target: chatopenai-0\n" +
				"        tools:\n          httprequest-0:\n            target: httprequest-0\n"},
		{name: "a vector store", source: realExportPath(flowiseChatflow),
			uses: "      uses:\n        model:\n          chatopenai-0:\n            target: chatopenai-0\n" +
				"        vector_store:\n          pinecone-0:\n            target: pinecone-0\n" +
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
	t.Parallel()
	for _, fx := range flowFixtures {
		t.Run(fx.golden, func(t *testing.T) {
			t.Parallel()
			rw, _ := catalogFolderImportWorkspaceAttaching(t, importopstest.NodesLayout())
			res := importFlow(t, rw, fx.path)
			if len(res.Diagnostics) != 0 {
				t.Errorf("the import raised %+v; the node-by-node draft of this source raises none", res.Diagnostics)
			}

			docs := make([]authoredcontent.Document, 0, len(res.Files))
			for _, f := range res.Files {
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

// plantedSource is a source carrying key-shaped values in fields its format marks
// as credentials, and the fields, by node and field, the default layout writes
// empty.
type plantedSource struct {
	name   string
	body   []byte
	keys   []string
	fields []string
}

// plantedSources plants key-shaped values in copies of the real exports — a
// Langflow field marked password, the credential a Flowise chat model selects and
// an input of type password beside it, the credential an agentflow agent keeps in
// its model configuration — and in a Langflow flow whose MCP Tools nodes carry a
// server configuration with header and environment values, or with a command and
// arguments starting a server: a list of them, and a single one written as text.
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

	server := func(name string, headers any, env any) map[string]any {
		config := map[string]any{"url": "http://" + name + ".example/mcp", "headers": headers}
		if env != nil {
			config["env"] = env
		}
		return map[string]any{"data": map[string]any{"type": "MCPTools", "node": map[string]any{"template": map[string]any{
			"mcp_server": map[string]any{"type": "mcp", "value": map[string]any{"name": name, "config": config}},
		}}}}
	}
	docs := server("docs", map[string]any{"Authorization": mcpHeader, "X-Empty": ""}, map[string]any{"API_KEY": mcpEnv})
	docs["id"] = "MCPTools-a1"
	search := server("search", []any{map[string]any{"key": "Authorization", "value": mcpHeader}}, nil)
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

	return []plantedSource{
		{name: "langflow-password.json", body: encode(lf), keys: []string{langflowKey},
			fields: []string{"LanguageModelComponent-FLeYF api_key"}},
		{name: "flowise-credential.json", body: encode(fw), keys: []string{flowiseCred, flowisePass},
			fields: []string{"chatOpenAI_0 credential", "chatOpenAI_0 apiKeyOverride"}},
		{name: "agentflow-credential.json", body: encode(af), keys: []string{agentKey},
			fields: []string{"agentAgentflow_0 agentModelConfig.credential"}},
		{name: "langflow-mcp-server.json", body: encode(mcp), keys: []string{mcpHeader, mcpEnv, mcpArg, mcpArgText},
			fields: []string{"MCPTools-a1 mcp_server.config.headers.Authorization", "MCPTools-a1 mcp_server.config.env.API_KEY",
				"MCPTools-b2 mcp_server.config.headers.Authorization", "MCPTools-c3 mcp_server.config.command",
				"MCPTools-c3 mcp_server.config.args.0", "MCPTools-c3 mcp_server.config.args.1",
				"MCPTools-d4 mcp_server.config.command", "MCPTools-d4 mcp_server.config.args"}},
	}
}

// emptiedField and emptiedWhole read the node and the field a credential-emptied
// warning names, and nothing else of it: the first for a field the format marks
// as a credential, the second for one it writes empty whole for the reason the
// warning gives.
var (
	emptiedField = regexp.MustCompile(`^the value of (\S+), which node (\S+) marks as a credential, is written empty in the flow the draft carries: no credential from the source reaches the draft$`)
	emptiedWhole = regexp.MustCompile(`^the whole value of (\S+) of node (\S+) is written empty in the flow the draft carries, since [^:]+: no credential from the source reaches the draft$`)
)

// TestNoCredentialValueAnExportHoldsReachesTheDraft plants key-shaped values in
// the fields Langflow and Flowise mark as credentials — a field marked password,
// the credential a node's credential parameter selects, an input of type password,
// an agentflow model's credential, an MCP server's header and environment values —
// and in the arguments an MCP server is started with, which neither marks, and
// imports each planted source under both layouts, writing the drafts. The
// search below finds every planted value in its planted source, which is what says
// it fires; it finds none in any file the import wrote, nor in the decisions or the
// diagnostics. Under the default layout one warning names each field it wrote empty
// by its node and field, and the warning carries no value.
//
// 🔴 PLANTED DEFECT: in carry (aggrega-pdk/go/pkg/importer/platform.go), replace
// `scrubbed, emptied, err := rewriteSource(src.Bytes, edits, creds)` with
// `scrubbed, emptied, err := src.Bytes, []Credential(nil), error(nil); _, _ = edits, creds`.
// The importer fixtures are rebuilt from the kit's sources when they change, and every
// row under the default layout fails: each planted value is in the drafted
// aggregate.yaml, and no warning names its field.
func TestNoCredentialValueAnExportHoldsReachesTheDraft(t *testing.T) {
	t.Parallel()
	for _, src := range plantedSources(t) {
		for _, layout := range []string{"platform", "nodes"} {
			t.Run(src.name+", "+layout, func(t *testing.T) {
				t.Parallel()
				holds := func(b []byte) []string {
					var found []string
					for _, key := range src.keys {
						if bytes.Contains(b, []byte(key)) {
							found = append(found, key)
						}
					}
					return found
				}
				if found := holds(src.body); len(found) != len(src.keys) {
					t.Fatalf("the needle: the search finds %d of the %d values planted in the source, so it does not fire",
						len(found), len(src.keys))
				}

				rw, _ := catalogFolderImportWorkspaceAttaching(t, map[string]any{"layout": layout})
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
				for _, rel := range res.Written {
					content, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
					if err != nil {
						t.Fatalf("read the written %s: %v", rel, err)
					}
					if found := holds(content); len(found) != 0 {
						t.Errorf("the written %s holds the planted values %q", rel, found)
					}
				}
				for _, part := range []struct {
					what  string
					value any
				}{{"decisions", res.Decisions}, {"diagnostics", res.Diagnostics}} {
					raw, _ := json.Marshal(part.value)
					if found := holds(raw); len(found) != 0 {
						t.Errorf("the import's %s hold the planted values %q", part.what, found)
					}
				}

				if layout != "platform" {
					return
				}
				var fields []string
				for _, d := range res.Diagnostics {
					if d.Code != "credential-emptied" {
						continue
					}
					m := emptiedField.FindStringSubmatch(d.Message)
					if m == nil {
						m = emptiedWhole.FindStringSubmatch(d.Message)
					}
					if m == nil {
						t.Errorf("the warning %q is not of the form that names a node and a field alone", d.Message)
						continue
					}
					fields = append(fields, m[2]+" "+m[1])
				}
				if !slices.Equal(fields, src.fields) {
					t.Errorf("the warnings name the emptied fields %q, want %q", fields, src.fields)
				}
			})
		}
	}
}
