// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end coverage of how the flow a drafted platform carries reaches each
// part of it the draft wires to a socket of the platform, run through the real
// Import over a workspace whose catalog folder holds the reference importers and
// a JSON parser beside their entities, the two flow platforms and the Components
// their parts resemble — the folder importopstest lays down, and the path every
// surface reaches.
//
// The sources below are committed real exports with authored parts added — MCP
// Tools nodes naming servers, a model whose address and key are written as
// literals, a Custom MCP tool's server configuration — since no export Langflow
// or Flowise publish carries those parts with values to plant. Each says so where
// it is built; none is presented as an export.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// mcpTool is a Component carrying the labels the standard library gives
// core/ai-tool/mcp: a tool reached over HTTP that may change what it acts on.
var mcpTool = partComponent("core/ai-tool/mcp", "    component.aggrega.ai/type: server\n"+
	"    component.aggrega.ai/category: ai-tool\n    tool.aggrega.ai/transport: http\n    tool.aggrega.ai/impact: mutating\n",
	"server_name")

// wiringWorkspace is platformCatalogWorkspace with mcpTool beside the platforms
// and the parts' Components.
func wiringWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	writeCatalogComponents(t, folder.Dir, append(append([]catalogComponent{}, platformComponents...), mcpTool)...)
	return rw
}

// writtenImport imports body, written to a file named name, through rw, naming no
// format, and writes the drafts; it returns the result and the directory they
// were written under.
func writtenImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, name string, body []byte) (Result, string) {
	t.Helper()
	path := importopstest.WriteSource(t, filepath.Join(t.TempDir(), name), string(body))
	outputDir := filepath.Join(rw.Root(), "drafts")
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    path,
		SourceName:    name,
		OutputDir:     outputDir,
		Delivery:      authoredcontent.DeliveryFor(false),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import of %s was refused: %v", name, err)
	}
	if len(res.Written) == 0 {
		t.Fatal("the import wrote nothing")
	}
	return res, outputDir
}

// assertNoneReaches requires that none of planted is in any file the import
// wrote under outputDir, nor in its decisions or diagnostics, after the needle:
// each is in the source, which is what says the search fires.
func assertNoneReaches(t *testing.T, planted []string, source []byte, res Result, outputDir string) {
	t.Helper()
	for _, p := range planted {
		if !strings.Contains(string(source), p) {
			t.Fatalf("the needle: %q is not in the source, so the search finds nothing", p)
		}
	}
	holds := func(where string, b []byte) {
		for _, p := range planted {
			if strings.Contains(string(b), p) {
				t.Errorf("%s holds the planted %q", where, p)
			}
		}
	}
	for _, rel := range res.Written {
		content, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the written %s: %v", rel, err)
		}
		holds("the written "+rel, content)
	}
	decisions, _ := json.Marshal(res.Decisions)
	holds("the decisions", decisions)
	diagnostics, _ := json.Marshal(res.Diagnostics)
	holds("the diagnostics", diagnostics)
}

// carriedFlow reads the drafted Aggregate written under outputDir as the compiler
// reads one, and returns the definition the platform instance keyed key carries,
// decoded, and the Aggregate's text.
func carriedFlow(t *testing.T, outputDir, key string) (map[string]any, string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(outputDir, "aggregate.yaml"))
	if err != nil {
		t.Fatalf("read the drafted Aggregate: %v", err)
	}
	agg := readAsTheCompilerDoes(t, string(content))
	definition, _ := agg.Spec.Components[key].Values["flow_definition"].(string)
	var flow map[string]any
	if err := json.Unmarshal([]byte(definition), &flow); err != nil {
		t.Fatalf("the carried definition does not read as JSON: %v", err)
	}
	return flow, string(content)
}

// decoded decodes a JSON source.
func decoded(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode the source: %v", err)
	}
	return doc
}

// encoded encodes an authored source, indented, ending in a line feed.
func encoded(t *testing.T, doc any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("encode the source: %v", err)
	}
	return append(b, '\n')
}

func asObject(v any) map[string]any { m, _ := v.(map[string]any); return m }

// langflowNode returns the template of the Langflow node whose id is id.
func langflowNode(t *testing.T, flow map[string]any, id string) map[string]any {
	t.Helper()
	for _, n := range asObject(flow["data"])["nodes"].([]any) {
		if asObject(n)["id"] == id {
			return asObject(asObject(asObject(asObject(n)["data"])["node"])["template"])
		}
	}
	t.Fatalf("the flow holds no node %s", id)
	return nil
}

// flowiseNode returns the data of the Flowise node whose id is id.
func flowiseNode(t *testing.T, flow map[string]any, id string) map[string]any {
	t.Helper()
	for _, n := range flow["nodes"].([]any) {
		if asObject(n)["id"] == id {
			return asObject(asObject(n)["data"])
		}
	}
	t.Fatalf("the flow holds no node %s", id)
	return nil
}

// mcpToolsNode is an authored Langflow MCP Tools node whose mcp_server field
// holds value.
func mcpToolsNode(id string, value any) map[string]any {
	return map[string]any{"id": id, "type": "genericNode", "data": map[string]any{"id": id, "type": "MCPTools",
		"node": map[string]any{"template": map[string]any{
			"_type":      "Component",
			"mcp_server": map[string]any{"_input_type": "McpInput", "name": "mcp_server", "type": "mcp", "value": value},
			"tool":       map[string]any{"_input_type": "DropdownInput", "name": "tool", "type": "str", "value": "search"},
		}}}}
}

// The values the authored MCP Tools nodes carry: two server addresses, one
// carrying a key in its query, a request header and an environment value.
const (
	remixAddress = "http://remix-mcp.internal.example:8000/sse?key=sk-plantedURLkey0123456789abcd"
	docsAddress  = "https://docs-mcp.internal.example/mcp"
	mcpHeader    = "Bearer sk-plantedMCPheaderVALUE0123456789"
	mcpEnv       = "sk-plantedMCPenvVALUE0123456789abcd"
)

// mcpSource is the committed Langflow export Basic Prompting with five authored
// MCP Tools nodes added: two naming the server remix at an address, the first
// with a request header; one naming "Docs Server", which a connection's alias
// cannot be, with an environment value; one naming fetch whose configuration
// starts a server of its own; and one naming no server.
func mcpSource(t *testing.T) []byte {
	t.Helper()
	flow := decoded(t, readRealExport(t, langflowPromptingProject))
	data := asObject(flow["data"])
	data["nodes"] = append(data["nodes"].([]any),
		mcpToolsNode("MCPTools-remix1", map[string]any{"name": "remix", "config": map[string]any{
			"url": remixAddress, "headers": map[string]any{"Authorization": mcpHeader}}}),
		mcpToolsNode("MCPTools-remix2", map[string]any{"name": "remix", "config": map[string]any{"url": remixAddress}}),
		mcpToolsNode("MCPTools-docs", map[string]any{"name": "Docs Server", "config": map[string]any{
			"url": docsAddress, "env": map[string]any{"TOKEN": mcpEnv}}}),
		mcpToolsNode("MCPTools-fetch", map[string]any{"name": "fetch", "config": map[string]any{
			"command": "uvx", "args": []any{"mcp-server-fetch"}}}),
		mcpToolsNode("MCPTools-empty", map[string]any{}),
	)
	return encoded(t, flow)
}

// TestAnMCPServerAFlowNamesIsWiredByNameAndItsAddressStaysOutOfTheDraft imports
// mcpSource under the default layout, with the reuse mode drafting every part and
// with the one referencing a HIGH match, and writes the drafts:
//
//   - each server an MCP Tools node names is one Component wired to the
//     platform's tools socket under the connection the flow names it by — the
//     two nodes naming remix reach one part, remix, through one connection — and
//     "Docs Server", which an alias cannot be, is wired as docs-server, the name
//     the carried flow then gives that node's server; under auto-apply each part
//     is core/ai-tool/mcp;
//   - the carried flow is the export with exactly these values written: each
//     wired node's server address empty, docs-server's name, and the request
//     header and the environment value empty; the node starting a server of its
//     own and the one naming none are carried as written, wired to nothing, and
//     reported — the first with the halt the platform's build makes on it;
//   - no address and no credential the export holds is in any file the import
//     wrote, nor in its decisions or diagnostics — the search finds each in the
//     source, which says it fires — and the Aggregate leaves each wired server's
//     infra.endpoint for the Release.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source:
//
//   - in importPlatform (aggrega-pdk/go/pkg/importer/platform.go), replace
//     `edits = append(edits, wired[i].wiring.Edits...)` with
//     `_ = wired[i].wiring.Edits`: both rows fail, each server address in the
//     drafted aggregate.yaml and docs keeping the name "Docs Server";
//   - in wireServer (plugins/langflow-importer/wire.go), replace
//     `if server.hasURL {` with `if server.hasURL && false {`: both rows fail,
//     the server addresses in the drafted aggregate.yaml.
func TestAnMCPServerAFlowNamesIsWiredByNameAndItsAddressStaysOutOfTheDraft(t *testing.T) {
	t.Parallel()
	source := mcpSource(t)
	for _, tc := range []struct {
		name     string
		settings map[string]any
		partType string
	}{
		{name: "drafting every part", partType: "imported/ai-tool/mcptools"},
		{name: "referencing a HIGH match", settings: map[string]any{"reuse_mode": "auto-apply-above-threshold"},
			partType: "core/ai-tool/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, outputDir := writtenImport(t, wiringWorkspace(t, tc.settings), "mcp-flow.json", source)
			assertNoneReaches(t, []string{remixAddress, "remix-mcp.internal.example", docsAddress,
				"docs-mcp.internal.example", mcpHeader, mcpEnv}, source, res, outputDir)

			flow, text := carriedFlow(t, outputDir, "langflow")
			want := decoded(t, source)
			server := func(f map[string]any, id string) map[string]any {
				return asObject(asObject(langflowNode(t, f, id)["mcp_server"])["value"])
			}
			for _, id := range []string{"MCPTools-remix1", "MCPTools-remix2"} {
				asObject(server(want, id)["config"])["url"] = ""
			}
			asObject(asObject(server(want, "MCPTools-remix1")["config"])["headers"])["Authorization"] = ""
			server(want, "MCPTools-docs")["name"] = "docs-server"
			asObject(server(want, "MCPTools-docs")["config"])["url"] = ""
			asObject(asObject(server(want, "MCPTools-docs")["config"])["env"])["TOKEN"] = ""
			if !reflect.DeepEqual(flow, want) {
				got, _ := json.Marshal(langflowNode(t, flow, "MCPTools-docs"))
				t.Errorf("the carried flow is not the export with exactly the expected values written; its docs node "+
					"reads %s", got)
			}

			agg := readAsTheCompilerDoes(t, text)
			uses := map[string]string{}
			for alias, c := range agg.Spec.Components["langflow"].Uses["tools"] {
				uses[alias] = c.Target
			}
			if want := map[string]string{"remix": "remix", "docs-server": "docs-server"}; !reflect.DeepEqual(uses, want) {
				t.Errorf("the tools socket draws %v, want %v", uses, want)
			}
			var parts []string
			for key, c := range agg.Spec.Components {
				if key == "langflow" {
					continue
				}
				parts = append(parts, key)
				if c.Type != tc.partType {
					t.Errorf("the part %s is a %s, want a %s", key, c.Type, tc.partType)
				}
			}
			if slices.Sort(parts); !slices.Equal(parts, []string{"docs-server", "remix"}) {
				t.Errorf("the Aggregate's parts are %v, want the two servers", parts)
			}
			for _, line := range []string{
				"    #   - infra.endpoint of remix, the address of the MCP server the flow names remix\n",
				"    #   - infra.endpoint of docs-server, the address of the MCP server the flow names docs-server\n",
			} {
				if !strings.Contains(text, line) {
					t.Errorf("the Aggregate does not leave for the Release\n%s", line)
				}
			}

			reported := map[string][]string{}
			for _, d := range res.Diagnostics {
				reported[d.Code] = append(reported[d.Code], d.SourceRef)
				if d.SourceRef == "MCPTools-fetch" && !strings.Contains(d.Message, "starts a server of its own") {
					t.Errorf("the node starting a server is reported as %q", d.Message)
				}
				if d.SourceRef == "MCPTools-fetch" && !strings.Contains(d.Message, "the platform's build halts on it") {
					t.Errorf("the report of the node starting a server does not say the build halts on it: %q", d.Message)
				}
			}
			if got := reported["part-not-placed"]; !slices.Equal(got, []string{"MCPTools-fetch", "MCPTools-empty"}) {
				t.Errorf("the nodes reported as wired to no socket are %v, want the one starting a server and the one "+
					"naming none", got)
			}
			if got := reported["part-reads-connection"]; !slices.Equal(got,
				[]string{"MCPTools-remix1", "MCPTools-remix2", "MCPTools-docs"}) {
				t.Errorf("the nodes reported as reaching their connection are %v", got)
			}
			for _, d := range res.Decisions {
				switch d.SourceNode.ID {
				case "MCPTools-remix2":
					if !strings.Contains(d.Rationale, "which node MCPTools-remix1 reaches too") {
						t.Errorf("the second node naming remix does not say it shares the first's part: %q", d.Rationale)
					}
				case "MCPTools-docs":
					if !strings.Contains(d.Rationale, `the flow names it "Docs Server", which cannot name its connection, `+
						"so it is named docs-server in the flow the draft carries") {
						t.Errorf("the renamed server's decision does not say so: %q", d.Rationale)
					}
				case "MCPTools-fetch", "MCPTools-empty":
					if d.Chosen != nil {
						t.Errorf("%s, wired to nothing, chose %+v", d.SourceNode.ID, d.Chosen)
					}
				}
			}
		})
	}
}

// TestEachPartWiredToAPlatformReadsItsConnectionOrIsReported reads, for a part
// of each kind the two platforms reach differently, how the carried flow reaches
// it and what the import says of it:
//
//   - a Langflow OpenAI chat model whose address and key the export writes as
//     literals has both fields written as the names of the variables the platform
//     delivers for its model connection, read as global variables, and neither
//     literal reaches the draft;
//   - a Langflow model of a type whose fields the importer does not know is
//     reported as reached other than through its connection;
//   - a Flowise OpenAI chat model whose Base Path the export holds has it written
//     empty, so its client reads the address the platform delivers;
//   - a Flowise vector store and tool are reported with the variable the platform
//     delivers each under and the Runtime variable a chatflow reads it through.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source:
//
//   - in wireFields (plugins/langflow-importer/wire.go), replace
//     `importer.Edit{Pointer: at(f.field, "value"), Value: jsonString(variable)},`
//     with `importer.Edit{Pointer: at(f.field, "value"), Value: jsonString("")},`:
//     the Langflow model row fails, its fields carrying no variable's name;
//   - in wireOf (plugins/flowise-importer/wire.go), replace
//     `string(basepath) != "null"` with `string(basepath) == "never"`: the
//     Flowise model row fails, the planted Base Path in the drafted aggregate.yaml.
func TestEachPartWiredToAPlatformReadsItsConnectionOrIsReported(t *testing.T) {
	t.Parallel()
	const (
		openAIBase = "https://planted-openai-base.example/v1"
		openAIKey  = "sk-plantedOPENAIkeyVALUE0123456789abcd"
		basePath   = "https://planted-flowise-base.example/v1"
	)

	t.Run("a Langflow OpenAI chat model whose address and key are literals", func(t *testing.T) {
		t.Parallel()
		flow := decoded(t, readRealExport(t, langflowPromptingProject))
		data := asObject(flow["data"])
		data["nodes"] = append(data["nodes"].([]any), map[string]any{"id": "OpenAIModel-pl4nt", "type": "genericNode",
			"data": map[string]any{"id": "OpenAIModel-pl4nt", "type": "OpenAIModel", "node": map[string]any{"template": map[string]any{
				"_type": "Component",
				"openai_api_base": map[string]any{"_input_type": "StrInput", "name": "openai_api_base", "type": "str",
					"load_from_db": false, "value": openAIBase},
				"api_key": map[string]any{"_input_type": "SecretStrInput", "name": "api_key", "type": "str", "password": true,
					"load_from_db": false, "value": openAIKey},
			}}}})
		source := encoded(t, flow)
		res, outputDir := writtenImport(t, wiringWorkspace(t, nil), "model-flow.json", source)
		assertNoneReaches(t, []string{openAIBase, openAIKey}, source, res, outputDir)

		carried, _ := carriedFlow(t, outputDir, "langflow")
		template := langflowNode(t, carried, "OpenAIModel-pl4nt")
		for field, variable := range map[string]string{"openai_api_base": "OPENAI_BASE_URL", "api_key": "OPENAI_API_KEY"} {
			f := asObject(template[field])
			if f["value"] != variable || f["load_from_db"] != true {
				t.Errorf("the field %s carries %v (load_from_db %v), want the variable %s read from the global variables",
					field, f["value"], f["load_from_db"], variable)
			}
		}
		want := "node OpenAIModel-pl4nt reaches openaimodel-pl4nt through the model connection openaimodel-pl4nt of " +
			"core/ai-agent/langflow: its field openai_api_base names the variable OPENAI_BASE_URL, and its field api_key " +
			"names the variable OPENAI_API_KEY, which the platform delivers for this connection and Langflow reads as a " +
			"global variable"
		if !slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool {
			return d.Code == "part-reads-connection" && d.Message == want
		}) {
			t.Errorf("the import does not report\n%s\nit raised %+v", want, res.Diagnostics)
		}
		for _, d := range res.Diagnostics {
			if d.Code == "credential-emptied" && d.SourceRef == "OpenAIModel-pl4nt" {
				t.Errorf("the key named as a variable is reported as emptied: %q", d.Message)
			}
		}
	})

	t.Run("a Langflow model whose fields the importer does not know", func(t *testing.T) {
		t.Parallel()
		res := importFlow(t, wiringWorkspace(t, nil), "../compiler/importersocket/testdata/langflow-flow-b.json")
		want := "node AnthropicModel-z9 reaches anthropicmodel-z9, wired to the model socket of core/ai-agent/langflow as " +
			"anthropicmodel-z9, other than through that connection: the importer knows no field of a Langflow " +
			"AnthropicModel node that names a variable the platform delivers for its model connection (OPENAI_BASE_URL, " +
			"OPENAI_API_KEY), so the node reaches the part at the address its own fields or its component's defaults give"
		if !slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool {
			return d.Code == "part-address-in-flow" && d.Message == want
		}) {
			t.Errorf("the import does not report\n%s\nit raised %+v", want, res.Diagnostics)
		}
	})

	t.Run("a Flowise OpenAI chat model whose Base Path is a literal", func(t *testing.T) {
		t.Parallel()
		flow := decoded(t, readRealExport(t, flowiseChatflow))
		for _, n := range flow["nodes"].([]any) {
			if asObject(n)["id"] == "chatOpenAI_0" {
				asObject(asObject(asObject(n)["data"])["inputs"])["basepath"] = basePath
			}
		}
		source := encoded(t, flow)
		res, outputDir := writtenImport(t, wiringWorkspace(t, nil), "chatflow.json", source)
		assertNoneReaches(t, []string{basePath}, source, res, outputDir)
		carried, _ := carriedFlow(t, outputDir, "flowise")
		if got := asObject(flowiseNode(t, carried, "chatOpenAI_0")["inputs"])["basepath"]; got != "" {
			t.Errorf("the chat model's Base Path is carried as %v, want it empty", got)
		}
		want := "node chatOpenAI_0 reaches chatopenai-0 through the model connection chatopenai-0 of " +
			"core/ai-agent/flowise: the Base Path the exported chatflow held is written empty, so the OpenAI client " +
			"Flowise runs it on reads the address the platform delivers for this connection, OPENAI_BASE_URL, and, since " +
			"it selects no credential, the key delivered as OPENAI_API_KEY"
		if !slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool {
			return d.Code == "part-reads-connection" && d.Message == want
		}) {
			t.Errorf("the import does not report\n%s\nit raised %+v", want, res.Diagnostics)
		}
	})

	t.Run("a Flowise vector store and tool", func(t *testing.T) {
		t.Parallel()
		overrides := map[string]any{"mapping_overrides": map[string]any{"flowise": map[string]any{
			"httpRequest": map[string]any{"component": "acme/ai-tool/web-fetch", "category": "ai-tool"},
			"pinecone":    map[string]any{"component": "acme/datastore/vectors", "category": "datastore"},
		}}}
		for _, row := range []struct{ source, ref, variable string }{
			{realExportPath(flowiseChatflow), "pinecone_0", "AGGREGA_VECTOR_STORE_PINECONE_0_URL"},
			{"../compiler/importersocket/testdata/flowise-flow.json", "httpRequest_0", "AGGREGA_TOOL_HTTPREQUEST_0_URL"},
		} {
			res := importFlow(t, wiringWorkspace(t, overrides), row.source)
			want := "Flowise reads " + row.variable + ", the address the platform delivers for this connection, only " +
				"through a Runtime variable of that name, which nothing creates at startup: create the Runtime variable " +
				row.variable + " on Flowise's Variables page and read it in the node as $vars." + row.variable +
				"; until then the node reaches the part at the address the chatflow holds"
			if !slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool {
				return d.Code == "part-address-in-flow" && d.SourceRef == row.ref && strings.HasSuffix(d.Message, want)
			}) {
				t.Errorf("the import does not report %s with\n%s\nit raised %+v", row.ref, want, res.Diagnostics)
			}
		}
	})
}

// TestAFlowWrittenWithCarriageReturnsIsCarriedAsTheExportReads imports a copy of
// the committed Langflow export Basic Prompting with every line break written CR
// LF: the platform carries the export itself, byte for byte — each CR LF written
// as the line feed the export holds — and one warning says how many carriage
// returns were written so.
//
// 🔴 PLANTED DEFECT: in carry (aggrega-pdk/go/pkg/importer/platform.go), replace
// `text, returns := lineFeedsFor(string(scrubbed))` with
// `text, returns := string(scrubbed), 0`: the case fails, the flow not carried.
func TestAFlowWrittenWithCarriageReturnsIsCarriedAsTheExportReads(t *testing.T) {
	t.Parallel()
	export := readRealExport(t, langflowPromptingProject)
	crlf := []byte(strings.ReplaceAll(string(export), "\n", "\r\n"))
	returns := strings.Count(string(crlf), "\r")
	if returns == 0 {
		t.Fatal("the copy holds no carriage return, so the case measures nothing")
	}
	res, outputDir := writtenImport(t, platformCatalogWorkspace(t, nil), "crlf-flow.json", crlf)
	content, err := os.ReadFile(filepath.Join(outputDir, "aggregate.yaml"))
	if err != nil {
		t.Fatalf("read the drafted Aggregate: %v", err)
	}
	agg := readAsTheCompilerDoes(t, string(content))
	if got, _ := agg.Spec.Components["langflow"].Values["flow_definition"].(string); got != string(export) {
		t.Errorf("the carried flow is not the export with each CR LF written as its line feed (%d bytes carried, %d "+
			"exported)", len(got), len(export))
	}
	var codes []string
	for _, d := range res.Diagnostics {
		codes = append(codes, d.Code)
	}
	if !slices.Equal(codes, []string{"carriage-returns-rewritten"}) {
		t.Fatalf("the import raised %+v, want the one warning naming the carriage returns", res.Diagnostics)
	}
	if want := "the flow holds " + strconv.Itoa(returns) +
		" carriage return(s), each written as a line feed in values.flow_definition, since a YAML literal block cannot " +
		"carry one: a JSON reader reads the carried flow as the same value"; res.Diagnostics[0].Message != want {
		t.Errorf("the warning reads %q, want %q", res.Diagnostics[0].Message, want)
	}
}

// TestTheServerConfigurationOfAFlowiseCustomMCPToolIsWrittenEmpty plants a
// request header, an environment value and a server address in the server
// configuration of Flowise's Custom MCP tool — written as JSON text on a
// chatflow's node, as text in the looser form Flowise also reads, and inside the
// configuration of a tool an agentflow's agent keeps beside a stored credential's
// id — and imports each under the default layout:
//
//   - none of the planted values is in any file the import wrote, nor in its
//     decisions or diagnostics, and the search finds each in its source;
//   - the JSON text is carried with exactly the header value, the environment
//     value and the address written empty, every other member kept; the looser
//     text, which does not read as JSON, is carried empty; the agent's
//     configuration is carried with the address and the credential's id empty;
//   - one warning names each field by its node and says what of it was emptied.
//
// 🔴 PLANTED DEFECT: in serverConfigCredentials (plugins/flowise-importer/walk.go),
// replace `edits, what := serverConfigEdits(written)` with
// `edits, what := []importer.Edit(nil), ""`, rebuilt into the importer fixture:
// the chatflow row fails, the header, the environment value and the address in
// the drafted aggregate.yaml.
func TestTheServerConfigurationOfAFlowiseCustomMCPToolIsWrittenEmpty(t *testing.T) {
	t.Parallel()
	const (
		address = "https://flowise-mcp.internal.example/mcp?token=sk-plantedFWurl0123456789"
		header  = "Bearer sk-plantedFWheader0123456789abc"
		env     = "sk-plantedFWenv0123456789abcdef"
		loose   = "sk-plantedFWloose0123456789abcd"
		agent   = "https://agent-mcp.internal.example/mcp"
		credID  = "planted-credential-id-0123456789abcdef"
	)
	customMCP := func(id string, config string) map[string]any {
		return map[string]any{"id": id, "type": "customNode", "position": map[string]any{"x": 0, "y": 0},
			"data": map[string]any{"id": id, "name": "customMCP", "label": "Custom MCP", "type": "Custom MCP Tool",
				"inputParams":  []any{map[string]any{"label": "MCP Server Config", "name": "mcpServerConfig", "type": "code"}},
				"inputAnchors": []any{}, "inputs": map[string]any{"mcpServerConfig": config, "mcpActions": `["search"]`}}}
	}
	config, _ := json.MarshalIndent(map[string]any{"url": address, "headers": map[string]any{"Authorization": header},
		"env": map[string]any{"API_KEY": env}, "transport": "http"}, "", "    ")

	t.Run("a chatflow", func(t *testing.T) {
		t.Parallel()
		flow := decoded(t, readRealExport(t, flowiseChatflow))
		flow["nodes"] = append(flow["nodes"].([]any), customMCP("customMCP_0", string(config)),
			customMCP("customMCP_1", "{\n  url: 'https://loose.example/mcp',\n  headers: {Authorization: '"+loose+"'},\n}"))
		source := encoded(t, flow)
		res, outputDir := writtenImport(t, wiringWorkspace(t, nil), "custom-mcp.json", source)
		assertNoneReaches(t, []string{address, "flowise-mcp.internal.example", header, env, loose}, source, res, outputDir)

		carried, _ := carriedFlow(t, outputDir, "flowise")
		var got map[string]any
		text, _ := asObject(flowiseNode(t, carried, "customMCP_0")["inputs"])["mcpServerConfig"].(string)
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("the carried configuration does not read as JSON: %v\n%s", err, text)
		}
		want := map[string]any{"url": "", "headers": map[string]any{"Authorization": ""}, "env": map[string]any{"API_KEY": ""},
			"transport": "http"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the carried configuration reads %v, want %v", got, want)
		}
		if got := asObject(flowiseNode(t, carried, "customMCP_1")["inputs"])["mcpServerConfig"]; got != "" {
			t.Errorf("the configuration that does not read as JSON is carried as %v, want it empty", got)
		}
		const tail = " in the flow the draft carries: no credential from the source reaches the draft"
		for _, msg := range []string{
			"mcpServerConfig of node customMCP_0 is written with its header values, environment values and server " +
				"address empty" + tail,
			"the whole value of mcpServerConfig of node customMCP_1 is written empty in the flow the draft carries, since " +
				"it does not read as a JSON object, so nothing inside it can be located: no credential from the source " +
				"reaches the draft",
		} {
			if !slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool {
				return d.Code == "credential-emptied" && d.Message == msg
			}) {
				t.Errorf("the import does not report\n%s\nit raised %+v", msg, res.Diagnostics)
			}
		}
	})

	t.Run("an agentflow agent's tool", func(t *testing.T) {
		t.Parallel()
		flow := decoded(t, readRealExport(t, flowiseAgentflow))
		inner, _ := json.Marshal(map[string]any{"url": agent})
		for _, n := range flow["nodes"].([]any) {
			if asObject(n)["id"] == "agentAgentflow_0" {
				asObject(asObject(asObject(n)["data"])["inputs"])["agentTools"] = []any{map[string]any{
					"agentSelectedTool": "customMCP", "agentSelectedToolConfig": map[string]any{
						"agentSelectedTool": "customMCP", "FLOWISE_CREDENTIAL_ID": credID,
						"mcpServerConfig": string(inner), "mcpActions": `["search"]`}}}
			}
		}
		source := encoded(t, flow)
		res, outputDir := writtenImport(t, wiringWorkspace(t, nil), "agentflow.json", source)
		assertNoneReaches(t, []string{agent, "agent-mcp.internal.example", credID}, source, res, outputDir)

		carried, _ := carriedFlow(t, outputDir, "flowise")
		tools, _ := asObject(flowiseNode(t, carried, "agentAgentflow_0")["inputs"])["agentTools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("the agent carries the tools %v, want the one", tools)
		}
		cfg := asObject(asObject(tools[0])["agentSelectedToolConfig"])
		if cfg["FLOWISE_CREDENTIAL_ID"] != "" || cfg["mcpServerConfig"] != `{"url":""}` {
			t.Errorf("the agent's tool configuration is carried as %v, want the credential's id and the address empty", cfg)
		}
		const tail = " in the flow the draft carries: no credential from the source reaches the draft"
		for _, msg := range []string{
			"the value of agentTools.0.agentSelectedToolConfig.FLOWISE_CREDENTIAL_ID, which node agentAgentflow_0 marks " +
				"as a credential, is written empty" + tail,
			"agentTools.0.agentSelectedToolConfig.mcpServerConfig of node agentAgentflow_0 is written with its server " +
				"address empty" + tail,
		} {
			if !slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool {
				return d.Code == "credential-emptied" && d.Message == msg
			}) {
				t.Errorf("the import does not report\n%s\nit raised %+v", msg, res.Diagnostics)
			}
		}
	})
}
