// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// demoEchoBody is the body the demo importer echoes for the "demo" format — one
// drafted Component whose name is the requested format — and the content of the
// matching scaffold file. It mirrors the guest at aggrega-pdk/go/examples/import.
const demoEchoBody = "kind: Component\nmetadata:\n  name: demo\n"

// demoImporterIdentity is the identity and version an attachment names the demo
// importer entity below by.
const demoImporterIdentity = "importer/demo-importer:1.0.0"

// stubImporterResolver returns the demo importer entity for any requested
// format, standing in for the catalog's resolution so a run drives the real
// import socket and the real guest without a catalog on disk.
//
// attachedBy is the position of the attachment it answers as attaching the demo
// importer, or -1 for none. It is an answer the stand-in is given rather than one
// it derives: which attachment names an importer is the catalog's rule, proven
// against a real catalog beside this file, and the cases using this stand-in are
// about what an import does with the attachment it is told about.
type stubImporterResolver struct {
	attachedBy int
}

func (r stubImporterResolver) ImporterPluginForFormat(_ string, attached []string) (types.Plugin, int, error) {
	at := -1
	if r.attachedBy >= 0 && r.attachedBy < len(attached) {
		at = r.attachedBy
	}
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "demo-importer", Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Importer: &types.PluginImporter{Capabilities: []string{"import:demo"}}},
	}, at, nil
}

// pointSeamAtDemoGuest points BuildSeam at the built demo importer fixture for
// the duration of one test, restoring it afterwards, with no attachment
// attaching it.
//
// The demo guest echoes its source directly and never draws on the parser seam,
// so the parser pair is left nil; the parser side is exercised at the
// import-socket level rather than through this guest.
func pointSeamAtDemoGuest(t *testing.T) {
	t.Helper()
	pointSeamAtDemoGuestAttachedBy(t, -1)
}

// pointSeamAtDemoGuestAttachedBy is pointSeamAtDemoGuest with the demo importer
// attached by the workspace's attachment at position at.
func pointSeamAtDemoGuestAttachedBy(t *testing.T, at int) {
	t.Helper()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	prev := BuildSeam
	t.Cleanup(func() { BuildSeam = prev })
	BuildSeam = func(_ *workspaceconfig.ResolvedWorkspace, _ PluginBytesResolver) (Seam, error) {
		return Seam{
			ImporterRes: stubImporterResolver{attachedBy: at},
			ImporterLoad: func(p types.Plugin) (wasm.PluginSpec, error) {
				return wasm.PluginSpec{Name: p.Coordinate(), Path: fixture}, nil
			},
		}, nil
	}
}

// aWorkspace resolves a real workspace under a fresh scratch directory, with the
// global configuration layer redirected into that same scratch tree.
//
// It runs the single real resolution path rather than fabricating a value, which
// is what the sibling operations layers do. The redirect matters as much as the
// resolution: without it a run here would read whatever configuration this
// machine happens to carry, so the test's subject would depend on the machine it
// ran on.
func aWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	return resolveWorkspaceAt(t, wsDir)
}

// aWorkspaceAttaching resolves a real workspace, as aWorkspace does, whose
// configuration carries the plugins.importer attachments given, in order.
func aWorkspaceAttaching(t *testing.T, attachments ...importopstest.Attachment) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir, attachments...)
	return resolveWorkspaceAt(t, wsDir)
}

// aWorkspaceDir creates the directory a scratch workspace is rooted at, inside a
// fresh scratch tree that also holds its redirected global layer.
func aWorkspaceDir(t *testing.T) string {
	t.Helper()
	wsDir := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return wsDir
}

// resolveWorkspaceAt resolves the workspace whose configuration the caller wrote
// at wsDir, with the global configuration layer redirected into the scratch tree
// wsDir sits in.
func resolveWorkspaceAt(t *testing.T, wsDir string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	return resolveWorkspaceAtWith(t, wsDir, nil)
}

// resolveWorkspaceAtWith is resolveWorkspaceAt with the environment holding env
// beside the redirect, and nothing else.
func resolveWorkspaceAtWith(t *testing.T, wsDir string, env map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := filepath.Dir(wsDir)
	// The redirected global layer must EXIST, not merely be pointed at: the
	// resolution refuses a configured global path that is absent rather than
	// treating it as a layer that contributed nothing. Writing an empty document
	// is what makes the redirect a real empty layer instead of a failure.
	globalPath := filepath.Join(root, "global", workspaceconfig.ConfigFilename)
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("mkdir global layer: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	lookup := func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return env[k]
	}
	rw, err := workspaceconfig.Resolve(wsDir, "", lookup)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw
}

// writtenSource writes body to a fresh file and returns its path — the place an
// import of that source is handed.
func writtenSource(t *testing.T, body string) string {
	t.Helper()
	return importopstest.WriteSource(t, filepath.Join(t.TempDir(), "source"), body)
}

// fileSource is the source an import finds at a file named name holding content,
// read the way an import reads it.
func fileSource(t *testing.T, name string, content []byte) *Source {
	t.Helper()
	src, err := readSource(importopstest.WriteSource(t, filepath.Join(t.TempDir(), "source"), string(content)), name)
	if err != nil {
		t.Fatalf("read the source written for %q: %v", name, err)
	}
	return src
}

// noopResolver satisfies the plugin-bytes seam for a run whose loader is pointed
// past it.
func noopResolver(*workspaceconfig.ResolvedWorkspace, string, map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
	return pluginpath.ResolvedPlugin{}, nil
}

// referenceDetectionSeam is a seam standing in for a catalog that holds the
// reference importer of each agent-flow format and the binary's own parsers: the
// importers are the entities generated from their built modules, so they carry
// the recognition signatures those modules declare, and the parsers are read
// straight from the binary. It resolves no importer, so it serves recognition
// and nothing after it.
func referenceDetectionSeam(t *testing.T) Seam {
	t.Helper()
	parserRes, parserLoad := importopstest.EmbeddedParsers(t)
	return Seam{
		ParserRes:  parserRes,
		ParserLoad: parserLoad,
		Importers:  importopstest.ReferenceImporterEntities(t),
	}
}

// TestSelectFormat exercises the format-selection contract over the reference
// importers' declared signatures: a source whose graph is nested under
// data.nodes is recognised as langflow, one whose nodes and edges are flat arrays
// as flowise, and one carrying a node array and a connections object as n8n —
// shapes read off the importers, not spelled by the host. A source carrying the
// n8n shape beside another format's is recognised as that other format alone,
// never as both. A JSON shape matching none, an n8n-like source whose connections
// are a list, content that is not JSON, a top-level array and a null data.nodes
// are refused rather than guessed at, and an explicit format wins over whatever
// the content would be recognised as.
//
// An MCP client's configuration is recognised as mcp by its file's name and its
// top-level object — .mcp.json, claude_desktop_config.json and .cursor/mcp.json
// by an object at mcpServers, read as JSON, .vscode/mcp.json by one at servers,
// read as JSON with comments, so a commented one among them — and a registry
// server description by its $schema alone, whatever the file is called; and since
// exactly one format is recognised in each case, no other importer's signature
// matches any of them. A client file whose name names no client, one in the other
// client's shape, and a .mcp.json holding a comment, which does not parse as
// JSON, are refused, the last reaching the importer by naming its format.
//
// A Compose file is recognised as compose by its name — any of the names Compose
// reads a Compose file or its override file under by default — and an object at
// services, read as YAML; the same file under another name, and a file of a
// Compose name holding no services object, are refused.
//
// A Dify DSL is recognised as dify under any name by app at its kind and the
// presence of app.mode and version, read as YAML — under a Compose name too,
// since it holds no services object; one of another kind, and one holding no
// version, are refused.
//
// A CrewAI project's declarations are recognised as crewai by their names: a
// classic project's config/agents.yaml and config/tasks.yaml read as YAML, a
// crew.jsonc holding a list at agents and an agent's agents/<name>.jsonc holding a
// role, both read as JSON with comments — a commented file among them, which the
// JSON signatures refuse — and their strict-JSON spellings crew.json and
// agents/<name>.json alike. An agents.yaml outside config/, a crew.jsonc listing
// no agents, an agent's file outside agents/ and a .json file under agents/
// stating no role are refused.
//
// A LangGraph application's configuration is recognised as langgraph, read as
// JSON with an object at graphs, by its name, langgraph.json, or under any name by
// its $schema naming the configuration's schema; a langgraph.json holding no
// graphs object, a configuration under another name naming no schema, and an
// environment file, which no signature names, are refused.
//
// A Google ADK application's root configuration is recognised as google-adk,
// read as YAML, by its name, root_agent.yaml, and an agent's name with its
// instruction or its agent_class; the same configuration under another name — as
// a sub-agent's configuration is called, which no signature names — and a
// root_agent.yaml stating no agent are refused.
func TestSelectFormat(t *testing.T) {
	seam := referenceDetectionSeam(t)

	langflow := []byte(`{"data":{"nodes":[],"edges":[]}}`)
	flowise := []byte(`{"nodes":[],"edges":[]}`)
	n8n := []byte(`{"id":"a1","nodes":[],"connections":{}}`)
	// An MCP client's configuration in each of its two shapes, each with a
	// comment, and a registry server description.
	mcpServers := []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}}}`)
	commentedMCPServers := []byte("{\n  // the project's servers\n  \"mcpServers\": {\"github\": {\"command\": \"npx\"}}\n}\n")
	vscodeServers := []byte(`{"servers":{"docs":{"type":"http","url":"https://docs.example.com/mcp"}}}`)
	commentedServers := []byte("{\n  // the workspace's servers\n  \"servers\": {\"docs\": {\"type\": \"http\", \"url\": \"https://docs.example.com/mcp\"},},\n}\n")
	serverDescription := []byte(`{"$schema":"https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",` +
		`"name":"io.github.example/weather","description":"Weather","version":"1.2.0"}`)
	// A Compose file, and a YAML file of a Compose name that holds no services.
	composeFile := []byte("services:\n  web:\n    image: nginx:1.27\n")
	noServices := []byte("volumes:\n  data: {}\n")
	// A Dify DSL in its smallest shape, the same with another kind, and the same
	// with no version.
	difyApp := []byte("app:\n  mode: workflow\n  name: Probe\nkind: app\nversion: 0.7.0\n")
	otherKind := []byte("app:\n  mode: workflow\nkind: rag_pipeline\nversion: 0.7.0\n")
	noVersion := []byte("app:\n  mode: workflow\nkind: app\n")
	// A classic CrewAI project's agents and tasks, a commented crew.jsonc, one
	// listing no agents, and a commented agent's file.
	crewAgents := []byte("researcher:\n  role: Researcher\n  goal: Find facts\n  backstory: Careful\n")
	crewTasks := []byte("research_task:\n  description: Research\n  expected_output: Notes\n  agent: researcher\n")
	crewFile := []byte("// the crew\n{\n  \"agents\": [\"researcher\",],\n  \"tasks\": [],\n}\n")
	crewNoAgents := []byte("{\"tasks\": []}\n")
	agentFile := []byte("{\n  /* the researcher */\n  \"role\": \"Researcher\",\n  \"goal\": \"Find facts\",\n}\n")
	// A LangGraph configuration, the same naming its schema, one whose graphs is
	// not an object, and an environment file.
	langgraphConfig := []byte(`{"dependencies":["."],"graphs":{"agent":"./agent.py:graph"}}`)
	langgraphSchema := []byte(`{"$schema":"https://langgra.ph/schema.json","dependencies":["."],` +
		`"graphs":{"agent":"./agent.py:graph"}}`)
	langgraphNoGraphs := []byte(`{"dependencies":["."],"graphs":["./agent.py:graph"]}`)
	environmentFile := []byte("OPENAI_API_KEY=sk-not-a-key\nGRAPHS=agent\n")
	// An ADK root LlmAgent, a root SequentialAgent, and a configuration stating
	// no agent.
	adkRoot := []byte("name: assistant\nmodel: gemini-flash-latest\ninstruction: Answer the user's questions.\n")
	adkSequence := []byte("agent_class: SequentialAgent\nname: pipeline\nsub_agents:\n  - config_path: step.yaml\n")
	adkNoAgent := []byte("description: no agent here\n")

	cases := []struct {
		name     string
		explicit string
		source   string
		content  []byte
		want     string
		wantErr  bool
	}{
		{name: "langflow nested data.nodes", source: "flow.json", content: langflow, want: "langflow"},
		{name: "flowise flat nodes+edges", source: "chatflow.json", content: flowise, want: "flowise"},
		{name: "n8n nodes and connections", source: "workflow.json", content: n8n, want: "n8n"},
		{name: "n8n's shape beside flowise's edges is flowise alone", source: "x.json",
			content: []byte(`{"nodes":[],"connections":{},"edges":[]}`), want: "flowise"},
		{name: "n8n's shape beside langflow's data.nodes is langflow alone", source: "x.json",
			content: []byte(`{"nodes":[],"connections":{},"data":{"nodes":[]}}`), want: "langflow"},
		{name: "connections that are a list are no n8n workflow", source: "x.json",
			content: []byte(`{"nodes":[],"connections":[]}`), wantErr: true},
		{name: "explicit format wins over n8n content", explicit: "flowise", source: "workflow.json", content: n8n, want: "flowise"},
		{name: "a json shape matching neither is refused", source: "x.json", content: []byte(`{"foo":"bar"}`), wantErr: true},
		{name: "content that is not json is refused", source: "x.json", content: []byte("not json at all"), wantErr: true},
		{name: "a top-level array is refused", source: "x.json", content: []byte(`[1,2,3]`), wantErr: true},
		{name: "a null data.nodes is not an array", source: "x.json", content: []byte(`{"data":{"nodes":null}}`), wantErr: true},
		{name: "explicit format wins over langflow content", explicit: "flowise", source: "flow.json", content: langflow, want: "flowise"},
		{name: "explicit format wins over garbage content", explicit: "langflow", source: "x.json", content: []byte("not json"), want: "langflow"},
		{name: "Claude Code's .mcp.json", source: ".mcp.json", content: mcpServers, want: "mcp"},
		{name: "a .mcp.json named with its directory", source: "project/.mcp.json", content: mcpServers, want: "mcp"},
		{name: "Claude Desktop's configuration", source: "claude_desktop_config.json", content: mcpServers, want: "mcp"},
		{name: "Cursor's .cursor/mcp.json", source: ".cursor/mcp.json", content: mcpServers, want: "mcp"},
		{name: "Visual Studio Code's .vscode/mcp.json", source: ".vscode/mcp.json", content: vscodeServers, want: "mcp"},
		{name: "a registry server.json", source: "server.json", content: serverDescription, want: "mcp"},
		{name: "a registry server description named otherwise, told by its content", source: "weather.json",
			content: serverDescription, want: "mcp"},
		{name: "a client file named only mcp.json names no client, so is refused", source: "mcp.json",
			content: mcpServers, wantErr: true},
		{name: "a client file renamed is refused", source: "servers.json", content: mcpServers, wantErr: true},
		{name: "a .mcp.json in Visual Studio Code's shape is refused", source: ".mcp.json", content: vscodeServers,
			wantErr: true},
		{name: "a commented .vscode/mcp.json is read as JSON with comments", source: ".vscode/mcp.json",
			content: commentedServers, want: "mcp"},
		{name: "a commented .mcp.json does not parse as JSON, so is refused", source: ".mcp.json",
			content: commentedMCPServers, wantErr: true},
		{name: "explicit mcp wins over the commented .mcp.json", explicit: "mcp", source: ".mcp.json",
			content: commentedMCPServers, want: "mcp"},
		{name: "a compose.yaml", source: "compose.yaml", content: composeFile, want: "compose"},
		{name: "a docker-compose.yml named with its directory", source: "app/docker-compose.yml", content: composeFile,
			want: "compose"},
		{name: "an override file", source: "compose.override.yaml", content: composeFile, want: "compose"},
		{name: "a Compose file under another name is refused", source: "stack.yaml", content: composeFile, wantErr: true},
		{name: "a compose.yaml holding no services is refused", source: "compose.yaml", content: noServices, wantErr: true},
		{name: "explicit compose wins over the name", explicit: "compose", source: "stack.yaml", content: composeFile,
			want: "compose"},
		{name: "a Dify DSL under a name of its author's choosing", source: "support-answers.yml", content: difyApp,
			want: "dify"},
		{name: "a Dify DSL named as a Compose file is told by its content", source: "compose.yaml", content: difyApp,
			want: "dify"},
		{name: "a DSL of another kind is refused", source: "pipeline.yml", content: otherKind, wantErr: true},
		{name: "a DSL holding no version is refused", source: "app.yml", content: noVersion, wantErr: true},
		{name: "explicit dify wins over a Compose file", explicit: "dify", source: "compose.yaml", content: composeFile,
			want: "dify"},
		{name: "a classic CrewAI project's agents", source: "src/crew/config/agents.yaml", content: crewAgents,
			want: "crewai"},
		{name: "a classic CrewAI project's tasks", source: "config/tasks.yaml", content: crewTasks, want: "crewai"},
		{name: "a commented crew.jsonc", source: "crew.jsonc", content: crewFile, want: "crewai"},
		{name: "a commented agent's file", source: "project/agents/researcher.jsonc", content: agentFile,
			want: "crewai"},
		{name: "an agents.yaml outside config/ is refused", source: "agents.yaml", content: crewAgents, wantErr: true},
		{name: "a crew.jsonc listing no agents is refused", source: "crew.jsonc", content: crewNoAgents, wantErr: true},
		{name: "an agent's file outside agents/ is refused", source: "researcher.jsonc", content: agentFile,
			wantErr: true},
		{name: "a crew.json in strict JSON", source: "project/crew.json", content: []byte(`{"agents":["researcher"],"tasks":[]}`),
			want: "crewai"},
		{name: "an agent's file in strict JSON", source: "agents/researcher.json",
			content: []byte(`{"role":"Researcher","goal":"Find facts"}`), want: "crewai"},
		{name: "a .json file under agents/ stating no role is refused", source: "agents/notes.json",
			content: []byte(`{"notes":["one"]}`), wantErr: true},
		{name: "explicit crewai wins over a Compose file", explicit: "crewai", source: "compose.yaml",
			content: composeFile, want: "crewai"},
		{name: "a langgraph.json", source: "langgraph.json", content: langgraphConfig, want: "langgraph"},
		{name: "a langgraph.json named with its directory", source: "apps/support/langgraph.json",
			content: langgraphConfig, want: "langgraph"},
		{name: "a LangGraph configuration under another name, told by its $schema", source: "deploy.json",
			content: langgraphSchema, want: "langgraph"},
		{name: "a langgraph.json whose graphs is no object is refused", source: "langgraph.json",
			content: langgraphNoGraphs, wantErr: true},
		{name: "a LangGraph configuration under another name naming no schema is refused", source: "deploy.json",
			content: langgraphConfig, wantErr: true},
		{name: "an environment file is recognised as nothing", source: "local.env", content: environmentFile,
			wantErr: true},
		{name: "an environment file in JSON is recognised as nothing", source: "app/local.env",
			content: langgraphConfig, wantErr: true},
		{name: "explicit langgraph wins over a Compose file", explicit: "langgraph", source: "compose.yaml",
			content: composeFile, want: "langgraph"},
		{name: "an ADK root_agent.yaml", source: "root_agent.yaml", content: adkRoot, want: "google-adk"},
		{name: "an ADK root_agent.yaml named with its directory", source: "my_agent/root_agent.yaml",
			content: adkRoot, want: "google-adk"},
		{name: "an ADK root_agent.yaml naming its agent_class", source: "root_agent.yaml", content: adkSequence,
			want: "google-adk"},
		{name: "an ADK configuration under another name is refused", source: "assistant.yaml", content: adkRoot,
			wantErr: true},
		{name: "a root_agent.yaml stating no agent is refused", source: "root_agent.yaml", content: adkNoAgent,
			wantErr: true},
		{name: "explicit google-adk wins over a Compose file", explicit: "google-adk", source: "compose.yaml",
			content: composeFile, want: "google-adk"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectFormat(context.Background(), tc.explicit, fileSource(t, tc.source, tc.content), seam)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SelectFormat(%q, %q, ...) = %q, nil; want error", tc.explicit, tc.source, got)
				}
				var undetected *UndetectedFormatError
				if !errors.As(err, &undetected) {
					t.Fatalf("SelectFormat refusal must be an *UndetectedFormatError so a surface can re-word it; got %T: %v", err, err)
				}
				if undetected.Path() != tc.source {
					t.Fatalf("UndetectedFormatError.Path() = %q; want %q", undetected.Path(), tc.source)
				}
				if want := []string{"langflow", "flowise", "n8n", "mcp", "compose", "dify", "crewai", "langgraph", "google-adk"}; !slices.Equal(undetected.Formats(), want) {
					t.Fatalf("the refusal offers %v as the formats to name; the catalog's importers advertise %v",
						undetected.Formats(), want)
				}
				return
			}
			if err != nil {
				t.Fatalf("SelectFormat(%q, %q, ...): unexpected error: %v", tc.explicit, tc.source, err)
			}
			if got != tc.want {
				t.Fatalf("SelectFormat(%q, %q, ...) = %q; want %q", tc.explicit, tc.source, got, tc.want)
			}
		})
	}
}

// TestUndetectedFormatMessageNamesTheTerminalRemedy pins the sentence the
// command line prints: the file, the flag, and the formats the catalog's
// importers advertise as the examples — or, where they advertise none, that
// there is nothing to name. A protocol surface re-words it through the type.
func TestUndetectedFormatMessageNamesTheTerminalRemedy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		formats []string
		want    string
	}{
		{[]string{"langflow", "flowise"}, `cannot detect the source format of "flow.json"; pass --format to name it ` +
			`explicitly (for example --format langflow or --format flowise)`},
		{[]string{"langflow"}, `cannot detect the source format of "flow.json"; pass --format to name it ` +
			`explicitly (for example --format langflow)`},
		{[]string{"a", "b", "c"}, `cannot detect the source format of "flow.json"; pass --format to name it ` +
			`explicitly (for example --format a, --format b or --format c)`},
		{nil, `cannot detect the source format of "flow.json"; pass --format to name it explicitly ` +
			`(no importer plugin in the workspace's catalog advertises a format to name)`},
	} {
		err := &UndetectedFormatError{Source: "flow.json", Advertised: tc.formats}
		if err.Error() != tc.want {
			t.Errorf("Error() with %v advertised = %q; want %q", tc.formats, err.Error(), tc.want)
		}
	}
}

// TestAnAmbiguousSourceIsRefusedUnderItsOwnCondition pins the refusal for a source
// the signatures of more than one format recognise: it is the registered
// ambiguous-import-format condition, reachable by errors.Is and carrying its short
// code through the chain, and its sentence names the source and every format it
// was recognised as before asking for --format.
func TestAnAmbiguousSourceIsRefusedUnderItsOwnCondition(t *testing.T) {
	t.Parallel()
	var err error = &AmbiguousFormatError{Source: "flow.json", Matched: []string{"langflow", "nested-flow"}}
	if !errors.Is(err, ErrAmbiguousImportFormat()) {
		t.Fatalf("the refusal is not the ambiguous-import-format condition: %v", err)
	}
	if short, ok := cerrors.ShortCodeOf(err); !ok || short != "E0339" {
		t.Errorf("the refusal carries short code %q (found %v); want E0339", short, ok)
	}
	want := `more than one importer recognises the source (source "flow.json", recognised as "langflow", ` +
		`"nested-flow"). Pass --format to name the one to read it as (for example --format langflow or ` +
		`--format nested-flow)`
	if err.Error() != want {
		t.Errorf("Error() = %q; want %q", err.Error(), want)
	}
}

// TestImportWritesTheDraftedFiles drives the real demo importer guest and
// asserts the echoed scaffold file lands on disk under the named output
// directory, with the written set naming it.
func TestImportWritesTheDraftedFiles(t *testing.T) {
	pointSeamAtDemoGuest(t)
	out := t.TempDir()

	res, err := Import(context.Background(), aWorkspace(t), Options{
		SourcePath:    writtenSource(t, "demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     out,
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, rerr := os.ReadFile(filepath.Join(out, "demo.yaml"))
	if rerr != nil {
		t.Fatalf("read written demo.yaml: %v", rerr)
	}
	if string(got) != demoEchoBody {
		t.Fatalf("written demo.yaml = %q; want %q", string(got), demoEchoBody)
	}
	if !slices.Equal(res.Written, []string{"demo.yaml", RecordFileName}) {
		t.Fatalf("Written = %v; want exactly [demo.yaml %s], the draft and then the record", res.Written, RecordFileName)
	}
	if len(res.Files) != 2 || res.Files[0].Content != demoEchoBody || res.Files[1].Path != RecordFileName {
		t.Fatalf("Files must carry the content as well as the path, the record last; got %+v", res.Files)
	}
	if record, rerr := os.ReadFile(filepath.Join(out, RecordFileName)); rerr != nil || string(record) != res.Files[1].Content {
		t.Fatalf("the record on disk is not the one the result carries (read error %v)", rerr)
	}
	if res.Format != "demo" {
		t.Fatalf("Format = %q; want demo", res.Format)
	}
}

// TestImportUnderAReportingDeliveryWritesNothing proves the reporting delivery
// returns the same file set and leaves the output directory untouched.
func TestImportUnderAReportingDeliveryWritesNothing(t *testing.T) {
	pointSeamAtDemoGuest(t)
	out := t.TempDir()

	res, err := Import(context.Background(), aWorkspace(t), Options{
		SourcePath:    writtenSource(t, "demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     out,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "demo.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("a reporting delivery must write nothing; stat err = %v", statErr)
	}
	if len(res.Written) != 0 {
		t.Fatalf("Written = %v; want empty under a reporting delivery", res.Written)
	}
	if len(res.Files) != 2 || res.Files[0].Path != "demo.yaml" || res.Files[0].Content != demoEchoBody ||
		res.Files[1].Path != RecordFileName {
		t.Fatalf("a reporting delivery must carry the same set a write puts on disk, the record among it; got %+v",
			res.Files)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 0 {
		t.Fatalf("output directory must be untouched; it holds %d entry/entries", len(entries))
	}
}

// TestAnImportNamingNoOutputDirectoryUsesTheWorkspaceRoot holds the one home of
// the default destination: an import handed no output directory writes beneath
// the root of the workspace it is handed, and reports that root as the directory
// its drafted paths are relative to, under both deliveries — never the directory
// the process happens to be sitting in.
//
// The process is moved to a folder outside the workspace first, so a default
// read from the process's folder and one read from the workspace are two
// different places and the assertions below can tell them apart.
//
// 🔴 PLANTED DEFECT: have outputDirFor (internal/importops/import.go) return "."
// rather than rw.Root() for an empty name, and both rows redden — the written row
// finds demo.yaml in the process's folder instead of at the root, and both name
// "." as the root their paths are relative to.
//
// Not parallel: it moves the process working directory and points the shared
// import seam.
func TestAnImportNamingNoOutputDirectoryUsesTheWorkspaceRoot(t *testing.T) {
	pointSeamAtDemoGuest(t)

	for _, row := range []struct {
		name   string
		dryRun bool
	}{
		{name: "writing the drafts"},
		{name: "reporting the drafts", dryRun: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			rw := aWorkspace(t)
			elsewhere := t.TempDir()
			t.Chdir(elsewhere)

			res, err := Import(context.Background(), rw, Options{
				SourcePath:    writtenSource(t, "demo source"),
				SourceName:    "demo-source.txt",
				Format:        "demo",
				Delivery:      authoredcontent.DeliveryFor(row.dryRun),
				ResolvePlugin: noopResolver,
			})
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if res.OutputDir != rw.Root() {
				t.Errorf("the drafted paths are reported relative to %q; want the workspace root %q",
					res.OutputDir, rw.Root())
			}
			if entries, rerr := os.ReadDir(elsewhere); rerr != nil || len(entries) != 0 {
				t.Errorf("the import touched the folder the process runs in (%v): %v", rerr, entries)
			}
			_, statErr := os.Stat(filepath.Join(rw.Root(), "demo.yaml"))
			if row.dryRun {
				if !os.IsNotExist(statErr) {
					t.Errorf("a reporting delivery wrote demo.yaml at the workspace root (stat: %v)", statErr)
				}
				return
			}
			body, rerr := os.ReadFile(filepath.Join(rw.Root(), "demo.yaml"))
			if rerr != nil {
				t.Fatalf("the draft is not at the workspace root: %v", rerr)
			}
			if string(body) != demoEchoBody {
				t.Errorf("the draft at the workspace root holds %q; want %q", body, demoEchoBody)
			}
		})
	}
}

// 🔴 TestImportRefusesAnImporterPathEscapingTheOutputRoot is the containment
// proof, and it is driven through the REAL guest rather than a fabricated file
// list: the demo importer names its scaffold file from a setting, so a setting
// carrying a climbing segment, handed by the attachment attaching it, makes the
// PLUGIN return the escaping path.
//
// It is a grid, and every cell is required: a path climbing through a slash and
// one climbing through a backslash; an output directory inside the workspace and
// one outside it; and a write and a dry run. A dry run is held to the write's
// refusal because a dry run answering with a draft the write then refuses tells
// its reader an import is fine that is not. The output directory inside the
// workspace is the cell no link judgement could refuse: the climb lands
// elsewhere inside the very root the drafts are held to, so only the judgement
// of the path as a name stands between it and an answer. Outside the workspace
// the climb leaves the root without passing a link, which the link judgement
// does not hold either.
//
// Each cell asserts more than the refusal, because refusing is only part of the
// contract: the refusal is the write's own error, naming the path the importer
// returned and the output directory it had to stay inside, and nothing was
// written anywhere — neither inside the output directory nor where the climb
// leads.
//
// 🔴 PLANTED DEFECT: have checkFiles (internal/importops/import.go) return nil at
// its head, leaving the judgement to the draftPlace call writeFiles makes for each
// file it writes, and every dry-run cell reddens — the import answers with the
// escaping draft instead of refusing it — while every write cell stays green.
func TestImportRefusesAnImporterPathEscapingTheOutputRoot(t *testing.T) {
	// Not parallel: it points the shared import seam.
	pointSeamAtDemoGuestAttachedBy(t, 0)

	climbs := []struct {
		name      string
		namespace string
		// drafted is the path the demo importer returns for namespace.
		drafted string
	}{
		{name: "climbing through a slash", namespace: "../escaped", drafted: "../escaped-demo.yaml"},
		{name: "climbing through a backslash", namespace: `..\escaped`, drafted: `..\escaped-demo.yaml`},
	}
	placements := []struct {
		name string
		// outputDir returns the output directory for the workspace rooted at root.
		outputDir func(t *testing.T, root string) string
	}{
		{name: "an output directory inside the workspace", outputDir: func(_ *testing.T, root string) string {
			return filepath.Join(root, "drafts")
		}},
		{name: "an output directory outside the workspace", outputDir: func(t *testing.T, _ string) string {
			return filepath.Join(t.TempDir(), "out")
		}},
	}
	deliveries := []struct {
		name   string
		dryRun bool
	}{
		{name: "a write"},
		{name: "a dry run", dryRun: true},
	}

	for _, climb := range climbs {
		for _, placement := range placements {
			for _, delivery := range deliveries {
				t.Run(climb.name+", "+placement.name+", "+delivery.name, func(t *testing.T) {
					rw := aWorkspaceAttaching(t, importopstest.Attachment{
						URI:      demoImporterIdentity,
						Settings: map[string]any{"target_namespace": climb.namespace},
					})
					out := placement.outputDir(t, rw.Root())
					if err := os.MkdirAll(out, 0o755); err != nil {
						t.Fatalf("mkdir output directory: %v", err)
					}

					res, err := Import(context.Background(), rw, Options{
						SourcePath:    writtenSource(t, "demo source"),
						SourceName:    "demo-source.txt",
						Format:        "demo",
						OutputDir:     out,
						Delivery:      authoredcontent.DeliveryFor(delivery.dryRun),
						ResolvePlugin: noopResolver,
					})
					if err == nil {
						t.Fatalf("an importer path climbing out of the output directory was answered rather than "+
							"refused; the answer carries %+v", res.Files)
					}
					var escaping *EscapingPathError
					if !errors.As(err, &escaping) {
						t.Fatalf("the refusal must be an *EscapingPathError so a surface can name the path; got %T: %v",
							err, err)
					}
					if escaping.Offending() != climb.drafted {
						t.Errorf("Offending() = %q; want the path the importer returned, %q",
							escaping.Offending(), climb.drafted)
					}
					if escaping.OutputDir != out {
						t.Errorf("the refusal names %q as the directory the path had to stay inside; want the output "+
							"directory %q", escaping.OutputDir, out)
					}
					if !strings.Contains(escaping.Error(), strconv.Quote(climb.drafted)) {
						t.Errorf("the message must name the offending path; got %q", escaping.Error())
					}

					// Nothing anywhere: not inside the output directory, and not where
					// the climb leads either.
					if inside, rerr := os.ReadDir(out); rerr != nil || len(inside) != 0 {
						t.Errorf("a refused import must write nothing at all; the output directory holds %v (read "+
							"error %v)", inside, rerr)
					}
					landing := filepath.Join(filepath.Dir(out), "escaped-demo.yaml")
					if _, statErr := os.Stat(landing); !os.IsNotExist(statErr) {
						t.Errorf("%s exists after the draft was refused (stat err = %v)", landing, statErr)
					}
				})
			}
		}
	}
}

// TestImportRefusesEveryEscapingShape drives the containment judgement over the
// shapes a plugin could return, so a later change that reached for a
// string-prefix comparison instead reddens here. It is the judgement both
// deliveries pass, so a shape refused here is refused by a dry run as by a write.
//
// The contained rows matter as much as the refused ones: a guard that refused an
// interior "a/../b" or a name merely beginning with two dots would reject
// legitimate importer output, and a bare-prefix comparison against the root
// would wrongly accept a sibling directory whose name extends it.
func TestImportRefusesEveryEscapingShape(t *testing.T) {
	pointSeamAtDemoGuest(t)

	cases := []struct {
		name    string
		path    string
		refused bool
	}{
		{name: "climbs one level", path: "../out.yaml", refused: true},
		{name: "climbs several levels", path: "../../../etc/out.yaml", refused: true},
		{name: "climbs after descending", path: "a/b/../../../out.yaml", refused: true},
		{name: "bare parent", path: "..", refused: true},
		{name: "absolute", path: "/etc/out.yaml", refused: true},
		{name: "empty", path: "", refused: true},
		{name: "plain name is contained", path: "out.yaml"},
		{name: "nested name is contained", path: "a/b/out.yaml"},
		{name: "interior climb resolving inside is contained", path: "a/../b/out.yaml"},
		{name: "a name beginning with two dots is an ordinary name", path: "..config.yaml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			err := checkFiles(out, out, []abi.ScaffoldFile{{Path: tc.path, Content: "x"}})
			var escaping *EscapingPathError
			refused := errors.As(err, &escaping)
			if refused != tc.refused {
				t.Fatalf("checkFiles(%q) refused = %v (err %v); want refused = %v", tc.path, refused, err, tc.refused)
			}
		})
	}
}

// 🔴 TestImportRefusesTheWholeSetWhenOnePathEscapes pins the ordering half of
// the guard: the set is judged as a whole before the first write, so the
// contained file in the same set must not reach disk, because a refused run
// leaving a partial prefix behind is a result nobody asked for. It runs the
// judgement and the write in the order Import runs them.
func TestImportRefusesTheWholeSetWhenOnePathEscapes(t *testing.T) {
	out := t.TempDir()
	files := []abi.ScaffoldFile{
		{Path: "contained.yaml", Content: "first"},
		{Path: "../escaped.yaml", Content: "second"},
	}
	err := checkFiles(out, out, files)
	if err == nil {
		_, err = writeFiles(out, files)
	}
	var escaping *EscapingPathError
	if !errors.As(err, &escaping) || escaping.Offending() != "../escaped.yaml" {
		t.Fatalf("a set carrying an escaping path must be refused naming it; got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "contained.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("the contained file was written before the set was refused; stat err = %v", statErr)
	}
}

// TestHaltedIsOneSpellingOfTheOutcome pins the predicate both surfaces read, so
// the command line's exit outcome and the wire answer's field cannot come to
// disagree about whether one import completed.
func TestHaltedIsOneSpellingOfTheOutcome(t *testing.T) {
	t.Parallel()
	res := Result{Diagnostics: []abi.Diagnostic{
		{Severity: "warn", Message: "one node was not mapped"},
		{Severity: abi.SeverityHalt, Message: "an edge referenced an unknown node"},
	}}
	if !res.Halted() {
		t.Fatal("a halt-severity diagnostic must make Halted report true")
	}
	warnOnly := Result{Diagnostics: []abi.Diagnostic{{Severity: "warn", Message: "one node was not mapped"}}}
	if warnOnly.Halted() {
		t.Fatal("a warn-only set must not report halted")
	}
	if (Result{}).Halted() {
		t.Fatal("an empty diagnostic set must not report halted")
	}
}

// TestImportRefusesWithoutAWorkspaceOrAResolver pins both wiring refusals, and
// pins the workspace one's sentence, which names the remedy a reader outside a
// workspace has to apply.
func TestImportRefusesWithoutAWorkspaceOrAResolver(t *testing.T) {
	t.Parallel()

	_, err := Import(context.Background(), nil, Options{ResolvePlugin: noopResolver})
	if err == nil {
		t.Fatal("a nil workspace must be refused")
	}
	want := "no workspace configuration resolved; run import from within a workspace, where an importer " +
		"advertising the source's format is selected from its catalog and the compiler's core importers"
	if err.Error() != want {
		t.Fatalf("nil-workspace refusal = %q; want %q", err.Error(), want)
	}

	_, err = Import(context.Background(), &workspaceconfig.ResolvedWorkspace{}, Options{})
	if err == nil {
		t.Fatal("a nil plugin resolver must be refused rather than defaulted")
	}
	if !strings.Contains(err.Error(), "plugin resolver") {
		t.Fatalf("the resolver refusal must name what is missing; got %q", err.Error())
	}
}

// TestAnImportReadsOnlyTheAttachmentAtThePositionItIsNamed pins what an import
// hands an importer, given the position of the attachment the resolution
// reported: that attachment's with-config and no other's, and nothing at all
// where no attachment is named — including where the workspace declares exactly
// one attachment, which names some other importer.
//
// A position past the end of the list names no attachment the workspace
// declares, so it is refused rather than read as none.
func TestAnImportReadsOnlyTheAttachmentAtThePositionItIsNamed(t *testing.T) {
	t.Parallel()
	settingsOf := func(ns string) *workspaceconfig.PluginRefWith {
		return &workspaceconfig.PluginRefWith{Settings: map[string]any{"target_namespace": ns}}
	}
	two := []workspaceconfig.PluginRef{
		{URI: "importer/first:1.0.0", With: settingsOf("first-ns")},
		{URI: "importer/second:1.0.0", With: settingsOf("second-ns")},
	}
	sole := []workspaceconfig.PluginRef{{URI: "importer/elsewhere:1.0.0", With: settingsOf("elsewhere-ns")}}

	for _, tc := range []struct {
		name    string
		refs    []workspaceconfig.PluginRef
		at      int
		want    string
		refused bool
	}{
		{name: "the second of two attachments", refs: two, at: 1, want: `{"target_namespace":"second-ns"}`},
		{name: "the first of two attachments", refs: two, at: 0, want: `{"target_namespace":"first-ns"}`},
		{name: "no attachment named, of two", refs: two, at: -1},
		{name: "no attachment named, of one", refs: sole, at: -1},
		{name: "a position past the end", refs: two, at: 2, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sources, settings, err := attachmentInputs(tc.refs, tc.at)
			if tc.refused {
				if err == nil {
					t.Fatalf("position %d of %d attachments was read as %q rather than refused", tc.at, len(tc.refs), settings)
				}
				return
			}
			if err != nil {
				t.Fatalf("attachmentInputs: %v", err)
			}
			if sources != nil {
				t.Errorf("sources = %+v; none of these attachments declares any", sources)
			}
			if string(settings) != tc.want {
				t.Errorf("settings = %q; want %q", settings, tc.want)
			}
		})
	}
}

// TestImportSourcesAreNameSortedAndLoudOnAMiss proves the with.sources path:
// glob-matched files are read into base-name-sorted named sources, and a pattern
// matching nothing is a loud miss rather than a silent empty import.
func TestImportSourcesAreNameSortedAndLoudOnAMiss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, body := range map[string]string{"beta.json": `{"b":2}`, "alpha.json": `{"a":1}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	sources, _, err := withInputs(&workspaceconfig.PluginRefWith{
		Sources: []string{filepath.Join(dir, "*.json")},
	})
	if err != nil {
		t.Fatalf("withInputs: %v", err)
	}
	if len(sources) != 2 || sources[0].source.Name != "alpha.json" || sources[1].source.Name != "beta.json" {
		t.Fatalf("sources must be base-name sorted; got %+v", sources)
	}
	if sources[0].path != filepath.Join(dir, "alpha.json") || sources[1].path != filepath.Join(dir, "beta.json") {
		t.Fatalf("each source must carry the path it was matched at beside its base name; got %+v", sources)
	}

	if _, _, missErr := withInputs(&workspaceconfig.PluginRefWith{
		Sources: []string{filepath.Join(dir, "*.nothing")},
	}); missErr == nil {
		t.Fatal("a pattern matching no files must be a loud miss")
	}
}

// TestSettingsReachTheGuest proves the with.settings path end-to-end: a setting
// on the attachment attaching the importer flows through Import into the guest,
// which prefixes the emitted entity name with it — so the drafted file lands
// under the prefixed name rather than the bare format.
func TestSettingsReachTheGuest(t *testing.T) {
	pointSeamAtDemoGuestAttachedBy(t, 0)
	out := t.TempDir()

	rw := aWorkspaceAttaching(t, importopstest.Attachment{
		URI:      demoImporterIdentity,
		Settings: map[string]any{"target_namespace": "prod"},
	})
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    writtenSource(t, "demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     out,
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	want := "kind: Component\nmetadata:\n  name: prod-demo\n"
	got, rerr := os.ReadFile(filepath.Join(out, "prod-demo.yaml"))
	if rerr != nil {
		t.Fatalf("read prod-demo.yaml (target_namespace must prefix the entity name): %v", rerr)
	}
	if string(got) != want {
		t.Fatalf("written prod-demo.yaml = %q; want %q", string(got), want)
	}
	if _, statErr := os.Stat(filepath.Join(out, "demo.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("demo.yaml must not exist when target_namespace prefixes the name; stat err = %v", statErr)
	}
	if !slices.Equal(res.Written, []string{"prod-demo.yaml", RecordFileName}) {
		t.Fatalf("Written = %v; want [prod-demo.yaml %s]", res.Written, RecordFileName)
	}
}

// TestSourcesReachTheGuest proves the with.sources path end-to-end: several
// glob-matched files named by the attachment attaching the importer are threaded
// into the guest, which reflects each source name back as a diagnostic,
// confirming the host delivered every source.
func TestSourcesReachTheGuest(t *testing.T) {
	pointSeamAtDemoGuestAttachedBy(t, 0)

	srcDir := t.TempDir()
	for name, body := range map[string]string{"beta.json": `{"b":2}`, "alpha.json": `{"a":1}`} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	rw := aWorkspaceAttaching(t, importopstest.Attachment{
		URI:     demoImporterIdentity,
		Sources: []string{filepath.Join(srcDir, "*.json")},
	})
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    writtenSource(t, "demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     t.TempDir(),
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	var joined strings.Builder
	for _, d := range res.Diagnostics {
		joined.WriteString(d.Message)
		joined.WriteString("\n")
	}
	got := joined.String()
	if !strings.Contains(got, "received source: alpha.json") || !strings.Contains(got, "received source: beta.json") {
		t.Fatalf("guest did not reflect both sources; diagnostics = %q", got)
	}
}

// TestTheSeamHasOneSpelling guards the seam against the alias that cannot be
// pointed: a package-level variable initialised from BuildSeam would copy the
// function value at initialisation, so pointing BuildSeam would not reach it.
// Driving a run through a pointed seam is what proves the read happens at call
// time.
func TestTheSeamHasOneSpelling(t *testing.T) {
	prev := BuildSeam
	t.Cleanup(func() { BuildSeam = prev })

	called := false
	BuildSeam = func(*workspaceconfig.ResolvedWorkspace, PluginBytesResolver) (Seam, error) {
		called = true
		return Seam{}, errSeamProbe
	}
	_, err := Import(context.Background(), aWorkspace(t), Options{
		SourcePath:    writtenSource(t, "x"),
		Format:        "demo",
		ResolvePlugin: noopResolver,
	})
	if !called {
		t.Fatal("Import must read BuildSeam at call time, not hold a value captured earlier")
	}
	if !strings.Contains(err.Error(), "seam probe") {
		t.Fatalf("the pointed seam's error must surface; got %v", err)
	}
}

// errSeamProbe is the sentinel the seam probe above returns.
var errSeamProbe = errors.New("seam probe")
