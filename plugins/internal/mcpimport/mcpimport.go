// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package mcpimport is the MCP importer: it reads the MCP servers an MCP
// client's configuration lists, or the one server an MCP registry server
// description describes, and drafts an Aggregate holding one instance of the
// standard library's MCP tool, core/ai-tool/mcp, per server, and a Release
// beside it carrying what each server runs and the environment it is started
// with.
//
// The importer guest (plugins/mcp-importer) holds the descriptor, the signatures
// the files are recognised by and the export wiring; everything it does with a
// source is here, so it compiles and is tested on the host toolchain as well.
//
// # What a source holds
//
// A client's configuration lists its servers by name, under mcpServers — as
// Claude Code's .mcp.json, Claude Desktop's claude_desktop_config.json and
// Cursor's .cursor/mcp.json write them — or under servers, as Visual Studio
// Code's .vscode/mcp.json writes them. A server is started from a command line,
// its command and its args, with its env as its environment, or reached at its
// url, sent its headers. A registry server description, the server.json the MCP
// registry publishes, describes one server: the packages it is published as and
// the remotes it is served at.
//
// The importer reads every one of these shapes alike, so it never needs to know
// which file a source is, and reads the order the source writes its servers in:
// the host's parse decides whether a source parses, and the members are read in
// the order the text writes them through the plugin kit's order-preserving
// reader, since the parser's tree writes them in sorted order.
//
// # What it drafts
//
// One Aggregate, aggregate.yaml, composing every server every source lists, each
// as an instance of core/ai-tool/mcp keyed by its name, and one Release beside it,
// release.yaml, naming that Aggregate and carrying for each instance the
// operational inputs the source states: a server started from a command line as
// infra.stdio_command, and the launcher it runs from as infra.stdio_runtime where
// the command names one a bridge image carries; a server reached at an address as
// infra.endpoint, the address without any part that carries a key; a package
// published as an image as infra.image; and a server's environment as the
// server_env attachment, core/referenced-secret-set, one entry per variable. What
// the source cannot state is left unset and named in a comment, so the build
// halts naming it. The Release's environment and its Manifest are left unset
// too, since no source names either and the import drafts no Manifest.
//
// # Credential values
//
// A value of a server's configuration can carry a key: an environment value, the
// address, the command and each argument. Each one the draft writes that is not
// a single variable reference is located in the drafted values and handed to the
// plugin kit's credential decision, importer.CarryCredentials, which reads the
// empty_credentials setting: by default each is carried as written and named in
// a credential-carried warning by its server and its place, never by its value;
// where the setting asks for an MCP server's members, each is written empty and
// reported under credential-emptied — an argument as an empty item of the command
// line, an address or an environment value left unset. A variable reference —
// ${NAME}, ${env:NAME}, ${input:id}, ${workspaceFolder} — holds no value, so it is
// written as the text it is whatever the setting says, and a variable whose value
// is one is left for the Release, its reference named. A header's value is
// written nowhere: a remote server's credential is held by each consumer on its
// connection to the tool, and the draft drafts no consumer.
package mcpimport

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/jsonc"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// Format is the one format the importer serves: every MCP client configuration
// and every registry server description.
const Format = "mcp"

// Syntax is the syntactic format a source is parsed as.
const Syntax = "json"

// Component is the catalog Component each server is drafted as an instance of.
const Component = "core/ai-tool/mcp"

// SchemaPrefix is what the $schema of a registry server description opens with:
// the address the registry publishes each version of its schema under.
const SchemaPrefix = "https://static.modelcontextprotocol.io/schemas/"

// secretSet is the Feature a server's environment is attached as, on the
// server_env slot of core/ai-tool/mcp.
const (
	secretSet = "core/referenced-secret-set"
	envSlot   = "server_env"
)

// where completes each credential report, naming where the value is written.
const where = "in the Release the draft writes"

// Import turns one import request into the drafts: each source — the request's
// named sources, or its primary bytes — has its comments and trailing commas
// written as spaces (jsonc.Strip, the one reading of JSON with comments the
// compiler's guests share), is parsed as JSON through parse, the host's
// parser, and has its servers read. A source the parser refuses is reported
// under parse-failed, which halts the import, and one listing no server under
// no-nodes; the import carries on with the other sources. A request naming a
// format other than Format reads no server.
//
// Where any source lists a server, it returns the drafted Aggregate and Release
// as files and entities, with a referenced Component entity per server; where the
// empty_credentials setting holds a value it does not take, it returns that halt
// and drafts nothing. Given one request and one parser, it returns byte-identical
// output.
func Import(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) aggrega.ImportOutput {
	var out aggrega.ImportOutput
	sources := importer.Sources(in)
	used := map[string]bool{}
	var servers []server
	for _, src := range sources {
		text := jsonc.Strip(src.Bytes)
		if _, err := parse(Syntax, text); err != nil {
			out.Diagnostics = append(out.Diagnostics, importer.ParseFailed(src, in.Format, err))
			continue
		}
		var found []server
		if in.Format == Format {
			found = serversOf(text)
		}
		if len(found) == 0 {
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(src, in.Format))
			continue
		}
		for _, s := range found {
			s.ref = s.name
			if len(sources) > 1 {
				s.ref = src.Name + "#" + s.name
			}
			s.key = importer.UniqueKey(used, s.name)
			servers = append(servers, s)
		}
	}
	if len(servers) == 0 {
		return out
	}

	written, diags, err := importer.CarryCredentials(in.Settings, draftedValues(servers), where, located(servers))
	if err != nil {
		out.Error = "mcp importer: write the drafted values: " + err.Error()
		return out
	}
	out.Diagnostics = append(out.Diagnostics, diags...)
	if written == nil {
		return out
	}
	if err := readBack(servers, written); err != nil {
		out.Error = "mcp importer: read the drafted values back: " + err.Error()
		return out
	}

	name := importer.TargetNamespace(in.Settings) + "/flow"
	aggregate, release := renderAggregate(name, servers), renderRelease(name, servers)
	for _, s := range servers {
		out.Entities = append(out.Entities, aggrega.EmittedEntity{Kind: "Component", Name: Component, Mode: "reference",
			SourceRef: s.ref})
	}
	out.Entities = append(out.Entities,
		aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft", Body: aggregate},
		aggrega.EmittedEntity{Kind: "Release", Name: name, Mode: "draft", Body: release})
	out.Files = []aggrega.ScaffoldFile{
		{Path: "aggregate.yaml", Content: aggregate},
		{Path: "release.yaml", Content: release},
	}
	return out
}

// serversOf reads the servers a source's text lists: the one server a registry
// server description describes, where its $schema opens with SchemaPrefix, and
// otherwise the servers a client's configuration lists.
func serversOf(text []byte) []server {
	top := members(text)
	for _, e := range top {
		var schema string
		if e.Key == "$schema" && json.Unmarshal(e.Value, &schema) == nil && strings.HasPrefix(schema, SchemaPrefix) {
			return []server{registryServer(top)}
		}
	}
	return clientServers(top)
}

// draftedValues writes, as one JSON document, the values of every server the
// credential decision judges: under each server's key, its command line, its
// address and its environment's values, in the order the draft writes them. A
// value the source leaves for its user is written empty; a variable whose value
// the draft does not write is written with none.
func draftedValues(servers []server) []byte {
	var b strings.Builder
	b.WriteString("{")
	for i, s := range servers {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(jsonString(s.key) + ":{")
		b.WriteString(`"stdio_command":[`)
		for j, c := range s.command {
			if j > 0 {
				b.WriteString(",")
			}
			b.WriteString(jsonString(c.text))
		}
		b.WriteString(`],"endpoint":`)
		if s.endpoint != nil {
			b.WriteString(jsonString(s.endpoint.text))
		} else {
			b.WriteString("null")
		}
		b.WriteString(`,"env":[`)
		for j, e := range s.env {
			if j > 0 {
				b.WriteString(",")
			}
			if e.value != nil && !e.value.reference {
				b.WriteString(jsonString(e.value.text))
			} else {
				b.WriteString("null")
			}
		}
		b.WriteString("]}")
	}
	b.WriteString("}")
	return []byte(b.String())
}

// located returns every value of draftedValues the credential decision judges,
// in the order the draft writes them: each item of a command line, the address
// and each environment value, where the value is not a single variable reference
// and is not left for the user — named by its server and by its place in the
// Release.
func located(servers []server) []importer.Credential {
	var out []importer.Credential
	for _, s := range servers {
		for j, c := range s.command {
			if c.reference || c.unset {
				continue
			}
			out = append(out, importer.Credential{Node: s.ref, Field: "infra.stdio_command." + strconv.Itoa(j),
				Pointer: importer.Pointer(s.key, "stdio_command", strconv.Itoa(j)), Class: c.class})
		}
		if s.endpoint != nil && !s.endpoint.reference {
			out = append(out, importer.Credential{Node: s.ref, Field: "infra.endpoint",
				Pointer: importer.Pointer(s.key, "endpoint"), Class: s.endpoint.class})
		}
		for j, e := range s.env {
			if e.value == nil || e.value.reference {
				continue
			}
			out = append(out, importer.Credential{Node: s.ref, Field: envSlot + "." + e.name,
				Pointer: importer.Pointer(s.key, "env", strconv.Itoa(j)), Class: e.value.class})
		}
	}
	return out
}

// readBack reads the values the credential decision wrote empty back onto the
// servers: a command-line item that held something and is written empty is
// written empty, and an address or an environment value that held something and
// is written empty is left for the Release. A value the decision carried is
// left exactly as the source writes it.
func readBack(servers []server, written []byte) error {
	var values map[string]struct {
		Command  []string  `json:"stdio_command"`
		Endpoint *string   `json:"endpoint"`
		Env      []*string `json:"env"`
	}
	if err := json.Unmarshal(written, &values); err != nil {
		return err
	}
	for i := range servers {
		s := &servers[i]
		v := values[s.key]
		for j := range s.command {
			if j < len(v.Command) && v.Command[j] == "" && s.command[j].text != "" {
				s.command[j].text = ""
				s.left = append(s.left, "infra.stdio_command."+strconv.Itoa(j)+", written empty")
			}
		}
		if s.endpoint != nil && v.Endpoint != nil && *v.Endpoint == "" && s.endpoint.text != "" {
			s.endpoint = nil
			s.left = append(s.left, "infra.endpoint")
		}
		for j := range s.env {
			e := &s.env[j]
			if e.value == nil || e.value.reference || j >= len(v.Env) || v.Env[j] == nil || *v.Env[j] != "" ||
				e.value.text == "" {
				continue
			}
			e.value = nil
		}
	}
	return nil
}

// launchers are the commands each bridge image carries a launcher for, by the
// runtime that selects the image: npm runs Node.js with npm and npx, and pypi
// adds Python, uv and uvx.
var launchers = map[string]string{
	"npx": "npm", "npm": "npm", "node": "npm",
	"uvx": "pypi", "uv": "pypi", "python": "pypi", "python3": "pypi",
}

// runtimeOf returns the runtime whose bridge image carries the launcher command
// names — read off its last path segment, less a .exe or .cmd extension — or ""
// where neither image carries it.
func runtimeOf(command string) string {
	base := command[strings.LastIndexAny(command, `/\`)+1:]
	base = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(base), ".exe"), ".cmd")
	return launchers[base]
}
