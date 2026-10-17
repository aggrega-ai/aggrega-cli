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
// Some cases import a committed real export as it is: the one MCP Tools node the
// Langflow repository's own flows carry, which has no server chosen, and the
// Language Model and Embedding Model nodes Langflow's own flows carry. Every
// other source below is a committed real export with authored parts added — MCP
// Tools nodes naming servers, an MCP Tools node keeping its server outside an
// mcp_server field, a model whose address and key are written as literals, a
// model running on another provider, copies of a flow's model nodes, a Custom
// MCP tool's server configuration — since no export Langflow or Flowise publish
// carries those parts with values to plant. Each says so where it is built; none
// is presented as an export.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

// The values the authored MCP Tools nodes carry: three server addresses, two
// carrying a key in their query, a request header, an environment value, and an
// argument a server is started with carrying a key.
const (
	remixAddress = "http://remix-mcp.internal.example:8000/sse?key=sk-plantedURLkey0123456789abcd"
	docsAddress  = "https://docs-mcp.internal.example/mcp"
	fetchAddress = "https://fetch-mcp.internal.example/mcp?token=sk-plantedFETCHurl0123456789ab"
	mcpHeader    = "Bearer sk-plantedMCPheaderVALUE0123456789"
	mcpEnv       = "sk-plantedMCPenvVALUE0123456789abcd"
	mcpArgument  = "--api-key=sk-plantedMCPargVALUE0123456789ab"
)

// mcpSource is the committed Langflow export Basic Prompting with five authored
// MCP Tools nodes added: two naming the server remix at an address, the first
// with a request header; one naming "Docs Server", which a connection's alias
// cannot be, with an environment value; one naming fetch whose configuration
// starts a server of its own, with a command and two arguments, the second
// carrying a key, beside an address carrying a key; and one naming no server.
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
			"url": fetchAddress, "command": "uvx", "args": []any{"mcp-server-fetch", mcpArgument}}}),
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
//     wired node's server address empty, docs-server's name, the request header
//     and the environment value empty, and the address, the command and each
//     argument of the node starting a server of its own empty, each reported by
//     its node and field; that node and the one naming no server are otherwise
//     carried as written, wired to nothing, and reported — the first with the
//     halt the platform's build makes on it, its command member kept, so the flow
//     it carries still reads as starting a server of its own;
//   - a wired node's address is reported once, with how the node reaches its
//     connection, and never as an emptied credential;
//   - no address and no credential the export holds is in any file the import
//     wrote, nor in its decisions or diagnostics — the search finds each in the
//     source, which says it fires — and the Aggregate leaves each wired server's
//     infra.endpoint for the Release.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source:
//
//   - in importPlatform (aggrega-pdk/go/pkg/importer/platform.go), replace
//     `edits = append(edits, wired[i].wiring.Edits...)` with
//     `_ = wired[i].wiring.Edits`: both rows fail, docs keeping the name
//     "Docs Server" and each wired node's address reported as an emptied
//     credential;
//   - in wireServer (plugins/langflow-importer/wire.go), replace
//     `if server.hasURL {` with `if server.hasURL && false {`: both rows fail,
//     each wired node's address reported as an emptied credential, and its
//     report of how it reaches its connection not saying the address is written
//     empty — the credential scrub still writes the address empty, so none
//     reaches the drafts;
//   - in serverCredentials (plugins/langflow-importer/walk.go), replace
//     `case mcpconfig.Argument:` with `case mcpconfig.Argument + 1:`: both rows
//     fail, the planted argument in the drafted aggregate.yaml;
//   - in serverCredentials, replace `case mcpconfig.Address:` with
//     `case mcpconfig.Address + 100:`: both rows fail, the fetch node's planted
//     address in the drafted aggregate.yaml.
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
				"docs-mcp.internal.example", fetchAddress, "fetch-mcp.internal.example", mcpHeader, mcpEnv, mcpArgument},
				source, res, outputDir)

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
			asObject(server(want, "MCPTools-fetch")["config"])["url"] = ""
			asObject(server(want, "MCPTools-fetch")["config"])["command"] = ""
			asObject(server(want, "MCPTools-fetch")["config"])["args"] = []any{"", ""}
			if !reflect.DeepEqual(flow, want) {
				docs, _ := json.Marshal(langflowNode(t, flow, "MCPTools-docs"))
				fetch, _ := json.Marshal(langflowNode(t, flow, "MCPTools-fetch"))
				t.Errorf("the carried flow is not the export with exactly the expected values written; its docs node "+
					"reads %s and its fetch node %s", docs, fetch)
			}
			if _, kept := asObject(server(flow, "MCPTools-fetch")["config"])["command"]; !kept {
				t.Error("the carried flow drops the fetch node's command, so it no longer reads as starting a server of " +
					"its own, which is what the platform's build halts on")
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
			var fetchEmptied []string
			wired := map[string]bool{"MCPTools-remix1": true, "MCPTools-remix2": true, "MCPTools-docs": true}
			for _, d := range res.Diagnostics {
				reported[d.Code] = append(reported[d.Code], d.SourceRef)
				if d.SourceRef == "MCPTools-fetch" && d.Code == "credential-emptied" {
					fetchEmptied = append(fetchEmptied, d.Message)
				}
				switch {
				case !wired[d.SourceRef]:
				case d.Code == "credential-emptied" && strings.Contains(d.Message, "mcp_server.config.url"):
					t.Errorf("the address of the wired node %s is reported as an emptied credential, beside how the "+
						"node reaches its connection: %q", d.SourceRef, d.Message)
				case d.Code == "part-reads-connection" &&
					!strings.Contains(d.Message, "the address the exported flow held there is written empty"):
					t.Errorf("the wired node %s's report of how it reaches its connection does not say the address "+
						"it held is written empty: %q", d.SourceRef, d.Message)
				}
				if d.SourceRef != "MCPTools-fetch" || d.Code != "part-not-placed" {
					continue
				}
				if !strings.Contains(d.Message, "starts a server of its own") {
					t.Errorf("the node starting a server is reported as %q", d.Message)
				}
				if !strings.Contains(d.Message, "the platform's build halts on it") {
					t.Errorf("the report of the node starting a server does not say the build halts on it: %q", d.Message)
				}
			}
			const since = " of node MCPTools-fetch is written empty in the flow the draft carries, since "
			const none = ", though Langflow marks it as none: no credential from the source reaches the draft"
			if want := []string{
				"the whole value of mcp_server.config.url" + since + "the address an MCP server is reached at can carry " +
					"a credential in its user part, its path or its query" + none,
				"the whole value of mcp_server.config.command" + since + "the command an MCP server is started with can " +
					"carry a credential" + none,
				"the whole value of mcp_server.config.args.0" + since + "an argument an MCP server is started with can " +
					"carry a credential" + none,
				"the whole value of mcp_server.config.args.1" + since + "an argument an MCP server is started with can " +
					"carry a credential" + none,
			}; !slices.Equal(fetchEmptied, want) {
				t.Errorf("the fetch node's emptied fields are reported as\n%q\nwant\n%q", fetchEmptied, want)
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

// TestTheMCPToolsNodeOfARealExportNamingNoServerIsReportedAndNeverWired imports
// the committed real export Nvidia Remix — a Langflow starter project whose MCP
// Tools node has no MCP server chosen — under the default layout, with the reuse
// mode drafting every part and with the one referencing a HIGH match, over a
// catalog holding core/ai-tool/mcp, which the node's type maps to:
//
//   - the node's decision chooses nothing and says the flow keeps it because it
//     names no server, and one warning says so by the node's id;
//   - no connection of the platform's tools socket is drawn and no Component
//     stands for the node;
//   - the flow the platform carries is the export, byte for byte, the node
//     included as the export writes it.
//
// 🔴 PLANTED DEFECT: in reachOf (plugins/langflow-importer/wire.go), replace
// `case server.name == "":` with `case server.name == "\x00":`, rebuilt into the
// importer fixture: both rows fail, the node wired to the tools socket.
func TestTheMCPToolsNodeOfARealExportNamingNoServerIsReportedAndNeverWired(t *testing.T) {
	t.Parallel()
	const (
		node   = "MCPTools-beHVJ"
		reason = "it names no MCP server, so no tools connection can reach it"
	)
	export := readRealExport(t, langflowRemixProject)
	for _, tc := range []struct {
		name     string
		settings map[string]any
	}{
		{name: "drafting every part"},
		{name: "referencing a HIGH match", settings: map[string]any{"reuse_mode": "auto-apply-above-threshold"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := importFlow(t, wiringWorkspace(t, tc.settings), realExportPath(langflowRemixProject))
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}

			decided := false
			for _, d := range res.Decisions {
				if d.SourceNode.ID != node {
					continue
				}
				decided = true
				if want := "kept in the flow core/ai-agent/langflow carries, with no Component of its own: " + reason; d.Chosen != nil || d.Rationale != want {
					t.Errorf("the node was decided %+v: %q, want no choice and %q", d.Chosen, d.Rationale, want)
				}
			}
			if !decided {
				t.Fatalf("no decision was recorded for %s", node)
			}
			want := "node " + node + " (MCPTools) is wired to no socket of core/ai-agent/langflow: " + reason + "; it stays in the flow"
			var reported []abi.Diagnostic
			for _, d := range res.Diagnostics {
				if d.SourceRef == node {
					reported = append(reported, d)
				}
			}
			if len(reported) != 1 || reported[0].Code != "part-not-placed" || reported[0].Message != want {
				t.Errorf("the node is reported as %+v, want one part-not-placed warning reading\n%s", reported, want)
			}

			agg := readAsTheCompilerDoes(t, draftedAggregateOf(t, res))
			if tools := agg.Spec.Components["langflow"].Uses["tools"]; len(tools) != 0 {
				t.Errorf("the platform's tools socket draws %v, want nothing", tools)
			}
			for key, c := range agg.Spec.Components {
				if key == "mcptools-behvj" || c.Type == "core/ai-tool/mcp" || c.Type == "imported/ai-tool/mcptools" {
					t.Errorf("a Component stands for the node naming no server: %s, a %s", key, c.Type)
				}
			}
			if got, _ := agg.Spec.Components["langflow"].Values["flow_definition"].(string); got != string(export) {
				t.Errorf("the flow the platform carries is not the export, byte for byte (%d bytes carried, %d exported)",
					len(got), len(export))
			}
		})
	}
}

// langflowToolsRefusal is why the Langflow importer refuses the tools socket to
// a node whose template carries no mcp_server field.
const langflowToolsRefusal = "Langflow reaches a tools connection only through an MCP Tools node naming it in its " +
	"mcp_server field, which this node does not carry, so a tools connection wired to it would be named by no node " +
	"of the flow and the platform's build would halt on it"

// inlineMCPToolsNode is an authored Langflow MCP Tools node whose template keeps
// its server in fields of its own rather than in an mcp_server field.
func inlineMCPToolsNode(id string) map[string]any {
	return map[string]any{"id": id, "type": "genericNode", "data": map[string]any{"id": id, "type": "MCPTools",
		"node": map[string]any{"template": map[string]any{
			"_type": "Component",
			"mode":  map[string]any{"_input_type": "TabInput", "name": "mode", "type": "tab", "value": "SSE"},
			"tool":  map[string]any{"_input_type": "DropdownInput", "name": "tool", "type": "str", "value": "search"},
		}}}}
}

// TestALangflowNodeThatIsNoMCPToolsNodeIsNeverWiredToTheToolsSocket imports the
// committed Langflow export Basic Prompting with one authored MCP Tools node
// added whose template carries no mcp_server field, over a catalog holding
// core/ai-tool/mcp, which that node's type maps to, and a tool that is a vector
// store as well, with a mapping override mapping the export's language model to
// a tool:
//
//   - mapped to a tool reached over HTTP, which the tools socket alone takes,
//     the language model stays in the flow, chooses nothing, and is reported
//     with why Langflow cannot reach a tools connection through it;
//   - mapped to the tool that is a vector store too, it passes the tools socket
//     over and is wired to the vector_store socket, its decision saying so;
//   - in both, the MCP Tools node carrying no mcp_server field stays in the flow
//     and is reported the same way, and the tools socket draws no connection.
//
// 🔴 PLANTED DEFECT: in platform (plugins/langflow-importer/format.go), delete
// the line setting the Refuse field to refuseOf, rebuilt into the importer
// fixture: both rows fail, both nodes wired to the tools socket.
func TestALangflowNodeThatIsNoMCPToolsNodeIsNeverWiredToTheToolsSocket(t *testing.T) {
	t.Parallel()
	vectorTool := partComponent("acme/ai-tool/vector-search", "    component.aggrega.ai/type: function\n"+
		"    component.aggrega.ai/category: ai-tool\n    tool.aggrega.ai/transport: http\n"+
		"    tool.aggrega.ai/impact: read-only\n    datastore.aggrega.ai/engine: search\n", "index")
	flow := decoded(t, readRealExport(t, langflowPromptingProject))
	data := asObject(flow["data"])
	data["nodes"] = append(data["nodes"].([]any), inlineMCPToolsNode("MCPTools-inline"))
	source := encoded(t, flow)

	refused := func(ref, nodeType, target string) string {
		return "node " + ref + " (" + nodeType + ") is wired to no socket of core/ai-agent/langflow: it maps to " + target +
			", which satisfies the selector of the tools socket of core/ai-agent/langflow, but " + langflowToolsRefusal +
			"; no other socket of core/ai-agent/langflow takes it; it stays in the flow"
	}
	inline := refused("MCPTools-inline", "MCPTools", "core/ai-tool/mcp")
	for _, tc := range []struct {
		name, tool string
		reported   []string
		vector     map[string]string
	}{
		{name: "a tool the tools socket alone takes", tool: "acme/ai-tool/web-fetch",
			reported: []string{refused("LanguageModelComponent-FLeYF", "LanguageModelComponent", "acme/ai-tool/web-fetch"), inline}},
		{name: "a tool a later socket takes", tool: "acme/ai-tool/vector-search",
			reported: []string{inline}, vector: map[string]string{"languagemodelcomponent-fleyf": "languagemodelcomponent-fleyf"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw, folder := catalogFolderImportWorkspaceAttaching(t, map[string]any{"mapping_overrides": map[string]any{
				"langflow": map[string]any{"LanguageModelComponent": map[string]any{"component": tc.tool, "category": "ai-tool"}}}})
			writeCatalogComponents(t, folder.Dir, append(append([]catalogComponent{}, platformComponents...), mcpTool, vectorTool)...)
			res, outputDir := writtenImport(t, rw, "inline-mcp-flow.json", source)

			var reported []string
			for _, d := range res.Diagnostics {
				if d.Code == "part-not-placed" {
					reported = append(reported, d.Message)
				}
			}
			if !slices.Equal(reported, tc.reported) {
				t.Errorf("the parts reported as wired to no socket are\n%q\nwant\n%q", reported, tc.reported)
			}

			_, text := carriedFlow(t, outputDir, "langflow")
			agg := readAsTheCompilerDoes(t, text)
			inst := agg.Spec.Components["langflow"]
			if tools := inst.Uses["tools"]; len(tools) != 0 {
				t.Errorf("the platform's tools socket draws %v, want nothing", tools)
			}
			vector := map[string]string{}
			for alias, c := range inst.Uses["vector_store"] {
				vector[alias] = c.Target
			}
			if !maps.Equal(vector, tc.vector) {
				t.Errorf("the vector_store socket draws %v, want %v", vector, tc.vector)
			}

			for _, d := range res.Decisions {
				switch d.SourceNode.ID {
				case "MCPTools-inline":
					if d.Chosen != nil {
						t.Errorf("the MCP Tools node carrying no mcp_server field chose %+v", d.Chosen)
					}
				case "LanguageModelComponent-FLeYF":
					if tc.vector == nil {
						if d.Chosen != nil {
							t.Errorf("the language model mapped to a tool chose %+v", d.Chosen)
						}
						continue
					}
					want := "; wired to the vector_store socket of core/ai-agent/langflow as languagemodelcomponent-fleyf; " +
						"not to the tools socket, whose selector its Component satisfies too, since " + langflowToolsRefusal
					if d.Chosen == nil || !strings.Contains(d.Rationale, want) {
						t.Errorf("the language model was decided %+v: %q, want a choice and a rationale holding %q", d.Chosen,
							d.Rationale, want)
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

// referencingLangflowModels is referencingLanguageModel with a second mapping
// override mapping Langflow's Embedding Model component to
// core/ai-model/openai-embedding, which no row of the importer's own mapping arm
// does either.
func referencingLangflowModels() map[string]any {
	settings := referencingLanguageModel()
	overrides := settings["mapping_overrides"].(map[string]any)["langflow"].(map[string]any)
	overrides["EmbeddingModel"] = map[string]any{
		"component": "core/ai-model/openai-embedding",
		"category":  "ai-model",
		"traits":    map[string]any{"model.aggrega.ai/capability": "embedding", "component.aggrega.ai/hosting": "managed"},
		"vendor":    "openai",
	}
	return settings
}

// selectingNodeID is the id of the Language Model node of Langflow 1.6.0's Basic
// Prompting, a committed real export, and remixEmbeddingNodeID that of the
// Embedding Model node of the committed real export Nvidia Remix.
const (
	selectingNodeID      = "LanguageModelComponent-kBOja"
	remixEmbeddingNodeID = "EmbeddingModel-X6URU"
)

// The reports a wired Langflow model node of a type running on several
// providers draws: reached through its connection, or other than through it,
// and the clauses each carries.
const (
	keyNamed        = "its field api_key names the variable OPENAI_API_KEY, which the platform delivers for this connection and Langflow reads as a global variable"
	fromEnvironment = "no field of it gives its API's address, so the OpenAI client it runs takes OPENAI_BASE_URL, the address the platform delivers for %s, from the environment"
	offOpenAI       = "the node %s, while the platform delivers its model connection (OPENAI_BASE_URL, OPENAI_API_KEY) to a node running on the OpenAI provider, so none of them is written into it, and the node reaches the part at the address its own fields or its component's defaults give"
)

// TestALangflowModelOfSeveralProvidersReadsItsConnectionWhereItRunsOnOpenAI
// imports, under the default layout and over a catalog whose generative and
// embedding model Components satisfy the platform's model socket, flows whose one
// model part is a node of a Langflow type running on one of several providers —
// the Language Model and Embedding Model components — mapped by an override to a
// model Component, so the model socket draws one connection to it:
//
//   - Langflow 1.6.0's Basic Prompting, whose Language Model node runs on OpenAI
//     and names the key in its api_key field as the global variable
//     OPENAI_API_KEY: that field is written with the variable the platform
//     delivers, so the flow is carried as the export, byte for byte, the key not
//     reported as emptied, and the node, holding no field for its API's address,
//     is reported as reaching the address the platform delivers from the
//     environment;
//   - Nvidia Remix, whose Embedding Model node runs on OpenAI with an empty
//     api_base and api_key: both are written with the variables the platform
//     delivers;
//   - a Language Model node running on another provider, its key a literal: no
//     variable is written into it, the key is written empty and reaches nothing,
//     and the node is reported as reached other than through its connection —
//     the connection's key never reaches a provider it is not delivered for;
//   - the committed Basic Prompting, whose Language Model node selects no model:
//     no variable is written into it, and it is reported the same way.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source:
//
//   - in connectionFields (plugins/langflow-importer/wire.go), replace
//     `"LanguageModelComponent": {"model": {` with
//     `"LanguageModelComponentX": {"model": {`: every Language Model row fails,
//     the node reported as of a type whose fields the importer does not know —
//     the Basic Prompting 1.6.0 row with its api_key written empty and reported
//     as emptied too — and the Embedding Model row passes;
//   - in offProvider (plugins/langflow-importer/wire.go), replace
//     `case provider == node.provider:` with
//     `case provider == node.provider || provider != "":`: the other-provider row
//     fails, the node written with the variables and reported as reaching its
//     connection;
//   - in connectionFields (plugins/langflow-importer/wire.go), replace
//     `{field: "api_base", variable: named("OPENAI_BASE_URL"), address: true},`
//     with
//     `{field: "api_base_url", variable: named("OPENAI_BASE_URL"), address: true},`:
//     the Nvidia Remix row fails, its api_base not written with the variable.
func TestALangflowModelOfSeveralProvidersReadsItsConnectionWhereItRunsOnOpenAI(t *testing.T) {
	t.Parallel()
	const plantedKey = "sk-plantedANTHROPICkeyVALUE0123456789ab"
	named := func(template map[string]any, field, variable string) {
		f := asObject(template[field])
		f["value"], f["load_from_db"] = variable, true
	}
	for _, tc := range []struct {
		name string
		// source is a committed real export by its path beneath realExportsDir, or,
		// where it is empty, the authored flow build writes.
		source string
		build  func(t *testing.T) []byte
		node   string
		// reads is the report of a node reaching its connection; where it is
		// empty, literal is the report of one reaching the part other than
		// through it.
		reads, literal string
		// carried writes, into the decoded source, what the flow the draft carries
		// holds written anew; where it is nil, the flow is carried byte for byte.
		carried func(flow map[string]any)
		// emptied marks a node whose api_key the import reports written empty, and
		// planted the values that must reach nothing the import wrote.
		emptied bool
		planted []string
	}{
		{name: "Langflow 1.6.0's Basic Prompting, its Language Model on OpenAI naming its key's variable",
			source: langflowSelectingProject, node: selectingNodeID,
			reads: keyNamed + "; " + fmt.Sprintf(fromEnvironment, "this connection")},
		{name: "Nvidia Remix, its Embedding Model on OpenAI with an empty address and key",
			source: langflowRemixProject, node: remixEmbeddingNodeID,
			reads: "its field api_base names the variable OPENAI_BASE_URL, and its field api_key names the variable " +
				"OPENAI_API_KEY, which the platform delivers for this connection and Langflow reads as a global variable",
			carried: func(flow map[string]any) {
				template := langflowNode(t, flow, remixEmbeddingNodeID)
				named(template, "api_base", "OPENAI_BASE_URL")
				named(template, "api_key", "OPENAI_API_KEY")
			}},
		{name: "a Language Model on another provider, its key a literal (authored)",
			build: func(t *testing.T) []byte {
				flow := decoded(t, readRealExport(t, langflowSelectingProject))
				template := langflowNode(t, flow, selectingNodeID)
				asObject(template["provider"])["value"] = "Anthropic"
				key := asObject(template["api_key"])
				key["value"], key["load_from_db"] = plantedKey, false
				return encoded(t, flow)
			},
			node: selectingNodeID, literal: fmt.Sprintf(offOpenAI, "runs on a model provider other than OpenAI"),
			carried: func(flow map[string]any) {
				asObject(langflowNode(t, flow, selectingNodeID)["api_key"])["value"] = ""
			},
			emptied: true, planted: []string{plantedKey}},
		{name: "the committed Basic Prompting, its Language Model selecting no model",
			source: langflowPromptingProject, node: promptingNodeID,
			literal: fmt.Sprintf(offOpenAI, "selects no model provider")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := tc.build
			if tc.source != "" {
				source = func(t *testing.T) []byte { return readRealExport(t, tc.source) }
			}
			body := source(t)
			res, outputDir := writtenImport(t, wiringWorkspace(t, referencingLangflowModels()), "models-flow.json", body)
			if len(tc.planted) > 0 {
				assertNoneReaches(t, tc.planted, body, res, outputDir)
			}
			key := strings.ToLower(tc.node)

			flow, text := carriedFlow(t, outputDir, "langflow")
			agg := readAsTheCompilerDoes(t, text)
			if tc.carried == nil {
				if got, _ := agg.Spec.Components["langflow"].Values["flow_definition"].(string); got != string(body) {
					t.Errorf("the flow is not carried as the export, byte for byte (%d bytes carried, %d exported)",
						len(got), len(body))
				}
			} else {
				want := decoded(t, body)
				tc.carried(want)
				if !reflect.DeepEqual(flow, want) {
					got, _ := json.Marshal(langflowNode(t, flow, tc.node))
					t.Errorf("the carried flow is not the export with exactly the expected values written; its node "+
						"%s reads %s", tc.node, got)
				}
			}
			uses := map[string]string{}
			for alias, c := range agg.Spec.Components["langflow"].Uses["model"] {
				uses[alias] = c.Target
			}
			if want := map[string]string{key: key}; !reflect.DeepEqual(uses, want) {
				t.Errorf("the model socket draws %v, want %v", uses, want)
			}

			code, want, other := "part-reads-connection", "node "+tc.node+" reaches "+key+" through the model "+
				"connection "+key+" of "+langflowPlatform+": "+tc.reads, "part-address-in-flow"
			if tc.reads == "" {
				code, want, other = "part-address-in-flow", "node "+tc.node+" reaches "+key+", wired to the model "+
					"socket of "+langflowPlatform+" as "+key+", other than through that connection: "+tc.literal,
					"part-reads-connection"
			}
			var reported []string
			emptied := false
			for _, d := range res.Diagnostics {
				if d.SourceRef != tc.node {
					continue
				}
				switch d.Code {
				case code:
					reported = append(reported, d.Message)
				case other:
					t.Errorf("the node is reported %s: %q", other, d.Message)
				case "credential-emptied":
					emptied = emptied || strings.Contains(d.Message, "the value of api_key,")
				}
			}
			if !slices.Equal(reported, []string{want}) {
				t.Errorf("the node is reported %s as\n%q\nwant\n%q", code, reported, want)
			}
			if emptied != tc.emptied {
				t.Errorf("the node's api_key is reported as written empty: %v, want %v", emptied, tc.emptied)
			}
		})
	}
}

// nodeCopy returns a copy of the Langflow node of flow whose id is id, under the
// id as.
func nodeCopy(t *testing.T, flow map[string]any, id, as string) map[string]any {
	t.Helper()
	for _, n := range asObject(flow["data"])["nodes"].([]any) {
		if asObject(n)["id"] != id {
			continue
		}
		body, err := json.Marshal(n)
		if err != nil {
			t.Fatalf("encode the node %s: %v", id, err)
		}
		var c map[string]any
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatalf("decode the node %s: %v", id, err)
		}
		c["id"] = as
		asObject(c["data"])["id"] = as
		return c
	}
	t.Fatalf("the flow holds no node %s", id)
	return nil
}

// TestALangflowModelLeftInTheFlowIsReportedAsItsProviderAndFieldsReachIt
// imports Langflow 1.6.0's Basic Prompting with three authored model nodes added
// after its Language Model node — a copy of that node, one running on another
// provider, and a copy of Nvidia Remix's Embedding Model node, whose api_base is
// empty — mapped by overrides to model Components, so the platform's model
// socket draws its single connection to the export's own Language Model node,
// the first generative one, and each added node stays in the flow, reported with
// how the flow reaches its part as the node is written:
//
//   - the Language Model copy, holding no field for its API's address, and the
//     Embedding Model copy, its api_base empty, through the connection, by the
//     OPENAI_BASE_URL they take from the environment;
//   - the node on another provider other than through it.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixture from its source: in
// standingOf (plugins/langflow-importer/wire.go), replace
// `if node.environmentAddress != "" && blankAddress(raw) {` with `if false {`:
// the case fails, the Embedding Model copy reported as reaching the part other
// than through the connection.
func TestALangflowModelLeftInTheFlowIsReportedAsItsProviderAndFieldsReachIt(t *testing.T) {
	t.Parallel()
	flow := decoded(t, readRealExport(t, langflowSelectingProject))
	same := nodeCopy(t, flow, selectingNodeID, "LanguageModelComponent-lm2")
	other := nodeCopy(t, flow, selectingNodeID, "LanguageModelComponent-lm3")
	for _, n := range []map[string]any{same, other} {
		key := asObject(asObject(asObject(asObject(n["data"])["node"])["template"])["api_key"])
		key["value"], key["load_from_db"] = "", false
	}
	asObject(asObject(asObject(asObject(other["data"])["node"])["template"])["provider"])["value"] = "Anthropic"
	embedding := nodeCopy(t, decoded(t, readRealExport(t, langflowRemixProject)), remixEmbeddingNodeID,
		"EmbeddingModel-em1")
	data := asObject(flow["data"])
	data["nodes"] = append(data["nodes"].([]any), same, other, embedding)

	const holder = "languagemodelcomponent-kboja"
	held := func(ref, typ, target, how string) string {
		return "node " + ref + " (" + typ + ") is wired to no socket of " + langflowPlatform + ": it maps to " + target +
			", which satisfies the selector of the model socket of " + langflowPlatform + ", but that socket draws a " +
			"single connection, which reaches " + holder + ", wired from node " + selectingNodeID + " since its " +
			"Component carries model.aggrega.ai/capability: generative; as its node is written, the flow reaches it " +
			how + "; it stays in the flow"
	}
	through := "through the connection " + holder + ": " + fmt.Sprintf(fromEnvironment, "that connection")
	want := map[string]string{
		"LanguageModelComponent-lm2": held("LanguageModelComponent-lm2", "LanguageModelComponent", "core/ai-model/openai",
			through),
		"LanguageModelComponent-lm3": held("LanguageModelComponent-lm3", "LanguageModelComponent", "core/ai-model/openai",
			"other than through the connection "+holder+": "+fmt.Sprintf(offOpenAI, "runs on a model provider other than OpenAI")),
		"EmbeddingModel-em1": held("EmbeddingModel-em1", "EmbeddingModel", "core/ai-model/openai-embedding", through),
	}

	res, outputDir := writtenImport(t, wiringWorkspace(t, referencingLangflowModels()), "models-flow.json",
		encoded(t, flow))
	_, text := carriedFlow(t, outputDir, "langflow")
	uses := map[string]string{}
	for alias, c := range readAsTheCompilerDoes(t, text).Spec.Components["langflow"].Uses["model"] {
		uses[alias] = c.Target
	}
	if w := map[string]string{holder: holder}; !reflect.DeepEqual(uses, w) {
		t.Errorf("the model socket draws %v, want %v", uses, w)
	}
	reported := map[string]string{}
	for _, d := range res.Diagnostics {
		if _, ok := want[d.SourceRef]; ok && d.Code == "part-not-placed" {
			reported[d.SourceRef] = d.Message
		}
	}
	if !reflect.DeepEqual(reported, want) {
		t.Errorf("the models left in the flow are reported\n%v\nwant\n%v", reported, want)
	}
}

// TestAPlatformsModelSocketDrawsOneConnection imports a flow holding several
// models under the default layout, over a catalog whose generative and
// embedding model Components both satisfy the platforms' model socket, which
// each platform's Recipes deliver as one OPENAI_BASE_URL and one
// OPENAI_API_KEY:
//
//   - the committed Flowise export Multiple Documents QnA, two embedding models
//     ahead of its chat model: the model socket draws one connection, to the chat
//     model, and each embedding model stays in the flow, reported as reaching
//     that connection, its Base Path being empty;
//   - the committed Langflow export Basic Prompting with three authored models
//     added — an embedding model whose address field names the variable the
//     platform delivers, one holding an address of its own, and an OpenAI chat
//     model after them: the connection goes to the chat model, and each embedding
//     model is reported as its address field reads.
//
// Each held-out model chooses nothing, is no Component of the draft, and its
// decision carries the reason it is reported with.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source:
//
//   - in the Flowise importer's format (plugins/flowise-importer/format.go),
//     delete the line setting the Single field to singleSockets: the Flowise row
//     fails, the platform drawing three model connections;
//   - in standingOf (plugins/langflow-importer/wire.go), replace `value ==
//     variable {` with `value == "never" {`: the Langflow row fails, the embedding
//     model naming the variable reported as reaching an address of its own.
func TestAPlatformsModelSocketDrawsOneConnection(t *testing.T) {
	t.Parallel()
	const embeddingAddress = "https://planted-embeddings.example/v1"
	held := func(platform, ref, typ, holderRef, holderKey, how string) string {
		return "node " + ref + " (" + typ + ") is wired to no socket of " + platform + ": it maps to " +
			"core/ai-model/openai-embedding, which satisfies the selector of the model socket of " + platform + ", but " +
			"that socket draws a single connection, which reaches " + holderKey + ", wired from node " + holderRef +
			" since its Component carries model.aggrega.ai/capability: generative; as its node is written, the flow " +
			"reaches it " + how + "; it stays in the flow"
	}
	field := func(load bool, value string) map[string]any {
		return map[string]any{"_input_type": "MessageTextInput", "type": "str", "load_from_db": load, "value": value}
	}
	model := func(id, typ string, template map[string]any) map[string]any {
		template["_type"] = "Component"
		return map[string]any{"id": id, "type": "genericNode", "data": map[string]any{"id": id, "type": typ,
			"node": map[string]any{"template": template}}}
	}
	langflow := decoded(t, readRealExport(t, langflowPromptingProject))
	data := asObject(langflow["data"])
	data["nodes"] = append(data["nodes"].([]any),
		model("OpenAIEmbeddings-e1", "OpenAIEmbeddings", map[string]any{"openai_api_base": field(true, "OPENAI_BASE_URL")}),
		model("OpenAIEmbeddings-e2", "OpenAIEmbeddings", map[string]any{"openai_api_base": field(false, embeddingAddress)}),
		model("OpenAIModel-m1", "OpenAIModel", map[string]any{"openai_api_base": field(false, "")}))

	for _, tc := range []struct {
		name, source, key, holderRef, holderKey string
		reports                                 map[string]string
	}{
		{name: "a Flowise chatflow", source: string(readRealExport(t, flowiseChatflow)), key: "flowise",
			holderRef: "chatOpenAI_0", holderKey: "chatopenai-0", reports: map[string]string{
				"openAIEmbeddings_0": held(flowisePlatform, "openAIEmbeddings_0", "openAIEmbeddings", "chatOpenAI_0",
					"chatopenai-0", "through the connection chatopenai-0: its Base Path is empty, so the OpenAI client "+
						"Flowise runs it on reads OPENAI_BASE_URL, the address the platform delivers for that connection, "+
						"and, since it selects no credential, the key delivered as OPENAI_API_KEY"),
				"openAIEmbeddings_1": held(flowisePlatform, "openAIEmbeddings_1", "openAIEmbeddings", "chatOpenAI_0",
					"chatopenai-0", "through the connection chatopenai-0: its Base Path is empty, so the OpenAI client "+
						"Flowise runs it on reads OPENAI_BASE_URL, the address the platform delivers for that connection, "+
						"and, since it selects no credential, the key delivered as OPENAI_API_KEY"),
			}},
		{name: "a Langflow flow with authored models", source: string(encoded(t, langflow)), key: "langflow",
			holderRef: "OpenAIModel-m1", holderKey: "openaimodel-m1", reports: map[string]string{
				"OpenAIEmbeddings-e1": held(langflowPlatform, "OpenAIEmbeddings-e1", "OpenAIEmbeddings", "OpenAIModel-m1",
					"openaimodel-m1", "through the connection openaimodel-m1: its field openai_api_base names the "+
						"variable OPENAI_BASE_URL, which the platform delivers for that connection and Langflow reads as a "+
						"global variable"),
				"OpenAIEmbeddings-e2": held(langflowPlatform, "OpenAIEmbeddings-e2", "OpenAIEmbeddings", "OpenAIModel-m1",
					"openaimodel-m1", "other than through the connection openaimodel-m1: its field openai_api_base does "+
						"not name OPENAI_BASE_URL, the variable the platform delivers for that connection, so the node "+
						"reaches the part at the address that field or its component's defaults give"),
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, outputDir := writtenImport(t, wiringWorkspace(t, nil), "models.json", []byte(tc.source))
			_, text := carriedFlow(t, outputDir, tc.key)
			agg := readAsTheCompilerDoes(t, text)
			uses := map[string]string{}
			for alias, c := range agg.Spec.Components[tc.key].Uses["model"] {
				uses[alias] = c.Target
			}
			if want := map[string]string{tc.holderKey: tc.holderKey}; !reflect.DeepEqual(uses, want) {
				t.Errorf("the model socket draws %v, want %v", uses, want)
			}
			reported := map[string]string{}
			for _, d := range res.Diagnostics {
				if _, ok := tc.reports[d.SourceRef]; ok && d.Code == "part-not-placed" {
					reported[d.SourceRef] = d.Message
				}
			}
			if !reflect.DeepEqual(reported, tc.reports) {
				t.Errorf("the held-out models are reported\n%v\nwant\n%v", reported, tc.reports)
			}
			for _, d := range res.Decisions {
				msg, isHeld := tc.reports[d.SourceNode.ID]
				if !isHeld {
					continue
				}
				reason := strings.TrimSuffix(msg[strings.Index(msg, ": it maps to ")+2:], "; it stays in the flow")
				if d.Chosen != nil || d.Rationale != "kept in the flow "+agg.Spec.Components[tc.key].Type+" carries, with "+
					"no Component of its own: "+reason {
					t.Errorf("%s was decided %+v: %q", d.SourceNode.ID, d.Chosen, d.Rationale)
				}
			}
			var components []string
			for key := range agg.Spec.Components {
				components = append(components, key)
			}
			if slices.Sort(components); !slices.Equal(components, slices.Sorted(slices.Values([]string{tc.key, tc.holderKey}))) {
				t.Errorf("the draft's components are %v, want the platform and the one model it reaches", components)
			}
		})
	}
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
// request header, an environment value, a server address and arguments a server
// is started with in the server configuration of Flowise's Custom MCP tool —
// written as JSON text on a chatflow's node, its arguments a list or a single
// text, as text in the looser form Flowise also reads, and inside the
// configuration of a tool an agentflow's agent keeps beside a stored credential's
// id — and imports each under the default layout:
//
//   - none of the planted values is in any file the import wrote, nor in its
//     decisions or diagnostics, and the search finds each in its source;
//   - the JSON text is carried with exactly the header value, the environment
//     value, the address, the command and each argument written empty, every
//     other member kept — the command and args members too, their values empty;
//     the looser text, which does not read as JSON, is carried empty; the agent's
//     configuration is carried with the address and the credential's id empty;
//   - one warning names each field by its node and says what of it was emptied.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixture:
//
//   - in serverConfigCredentials (plugins/flowise-importer/walk.go), replace
//     `edits, what := serverConfigEdits(written)` with
//     `edits, what := []importer.Edit(nil), ""`: the chatflow row fails, the
//     header, the environment value, the address and the arguments in the
//     drafted aggregate.yaml;
//   - in serverConfigEdits (the same file), replace
//     `if (m.Kind == mcpconfig.Command || m.Kind == mcpconfig.Argument) && mcpconfig.Blank(m.Value) {`
//     with `if m.Kind == mcpconfig.Argument || (m.Kind == mcpconfig.Command && mcpconfig.Blank(m.Value)) {`:
//     the chatflow row fails, the planted arguments in the drafted aggregate.yaml.
func TestTheServerConfigurationOfAFlowiseCustomMCPToolIsWrittenEmpty(t *testing.T) {
	t.Parallel()
	const (
		address = "https://flowise-mcp.internal.example/mcp?token=sk-plantedFWurl0123456789"
		header  = "Bearer sk-plantedFWheader0123456789abc"
		env     = "sk-plantedFWenv0123456789abcdef"
		loose   = "sk-plantedFWloose0123456789abcd"
		agent   = "https://agent-mcp.internal.example/mcp"
		credID  = "planted-credential-id-0123456789abcdef"
		argList = "--api-key=sk-plantedFWargLIST0123456789"
		argText = "--token=sk-plantedFWargTEXT0123456789"
	)
	customMCP := func(id string, config string) map[string]any {
		return map[string]any{"id": id, "type": "customNode", "position": map[string]any{"x": 0, "y": 0},
			"data": map[string]any{"id": id, "name": "customMCP", "label": "Custom MCP", "type": "Custom MCP Tool",
				"inputParams":  []any{map[string]any{"label": "MCP Server Config", "name": "mcpServerConfig", "type": "code"}},
				"inputAnchors": []any{}, "inputs": map[string]any{"mcpServerConfig": config, "mcpActions": `["search"]`}}}
	}
	config, _ := json.MarshalIndent(map[string]any{"url": address, "headers": map[string]any{"Authorization": header},
		"env": map[string]any{"API_KEY": env}, "transport": "http"}, "", "    ")
	started, _ := json.MarshalIndent(map[string]any{"command": "npx", "args": []any{"-y", "@acme/mcp-server", argList},
		"env": map[string]any{"API_KEY": env}}, "", "    ")
	startedText, _ := json.Marshal(map[string]any{"command": "npx", "args": argText})

	t.Run("a chatflow", func(t *testing.T) {
		t.Parallel()
		flow := decoded(t, readRealExport(t, flowiseChatflow))
		flow["nodes"] = append(flow["nodes"].([]any), customMCP("customMCP_0", string(config)),
			customMCP("customMCP_1", "{\n  url: 'https://loose.example/mcp',\n  headers: {Authorization: '"+loose+"'},\n}"),
			customMCP("customMCP_2", string(started)), customMCP("customMCP_3", string(startedText)))
		source := encoded(t, flow)
		res, outputDir := writtenImport(t, wiringWorkspace(t, nil), "custom-mcp.json", source)
		assertNoneReaches(t, []string{address, "flowise-mcp.internal.example", header, env, loose, argList, argText},
			source, res, outputDir)

		carried, _ := carriedFlow(t, outputDir, "flowise")
		for id, want := range map[string]map[string]any{
			"customMCP_0": {"url": "", "headers": map[string]any{"Authorization": ""}, "env": map[string]any{"API_KEY": ""},
				"transport": "http"},
			"customMCP_2": {"command": "", "args": []any{"", "", ""}, "env": map[string]any{"API_KEY": ""}},
			"customMCP_3": {"command": "", "args": ""},
		} {
			var got map[string]any
			text, _ := asObject(flowiseNode(t, carried, id)["inputs"])["mcpServerConfig"].(string)
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatalf("the carried configuration of %s does not read as JSON: %v\n%s", id, err, text)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the carried configuration of %s reads %v, want %v", id, got, want)
			}
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
			"mcpServerConfig of node customMCP_2 is written with its environment values, command and arguments empty" + tail,
			"mcpServerConfig of node customMCP_3 is written with its command and arguments empty" + tail,
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
