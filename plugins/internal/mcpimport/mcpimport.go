// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// which file a source is. Visual Studio Code reads its configuration as JSON
// with comments, and the other clients and the registry read theirs as JSON, so
// a source is read as the shape it holds asks: one listing its servers under
// servers is read through the host's parser for JSON with comments, and its
// members in the order that parser's tree writes them, which is name order; every
// other source is parsed through the host's JSON parser, which refuses a
// comment, and its members are read in the order its text writes them, through
// the plugin kit's order-preserving reader.
//
// # What it drafts
//
// One Aggregate, aggregate.yaml, composing every server every source lists, each
// as an instance of core/ai-tool/mcp keyed by its name, and one Release beside it,
// release.yaml, naming that Aggregate and carrying for each instance the
// operational inputs the source states: a server started from a command line as
// infra.stdio_command, and the launcher it runs from as infra.stdio_runtime where
// the command names one a bridge image carries; a server reached at an address as
// infra.endpoint, the address without any part that carries a key — its user
// part, its query and its fragment, each written in the comment above its entry
// beside the address; a package
// published as an image as infra.image; and a server's environment as the
// server_env attachment, core/referenced-secret-set, one entry per variable. What
// the source cannot state is left unset and named in a comment, so the build
// halts naming it. The Release's environment and its Manifest are left unset
// too, since no source names either and the import drafts no Manifest.
//
// The Aggregate and the Release are named as importer.DraftName names a draft: a
// registry server description gives the server it describes a name, and a draft
// made from descriptions alone, all naming one server, is named after it; a
// client's configuration gives itself none, so a draft holding a server one
// lists is named after the directory or the file the import was handed.
//
// # Credential values
//
// A value of a server's configuration can carry a key: a header's value, an
// environment value, the address, each part of the address infra.endpoint
// leaves out, the command and each argument. Each one the draft writes that is
// not a single variable reference is located in the drafted values and handed
// to the plugin kit's credential decision, importer.CarryCredentials, which
// reads the empty_credentials setting: by default each is carried as written
// and named in a credential-carried warning by its server and its place, never
// by its value; where the setting asks for an MCP server's members, each is
// written empty and reported under credential-emptied — an argument as an empty
// item of the command line, an address or an environment value left unset, a
// header's value or a part of the address left out of the comment, which names
// the header or the part alone. A variable reference — the bare name ${NAME},
// ${workspaceFolder} among them, ${env:NAME} or ${input:id}, as
// importer.IsVariableReference reads one — holds no value, so it is written as
// the text it is whatever the setting says, and a variable, a header or a part
// of the address whose value is one is written with the reference it reads
// from; a reference carrying text after an operator, such as ${NAME-value} or
// ${NAME:-value}, holds that text and is located like any other value. No
// key of the Release takes a header's value — a remote server's credential is
// held by each consumer on its connection to the tool, and the draft drafts no
// consumer — so the headers a server is sent are written in the comment above
// its entry, each beside its value; and infra.endpoint takes the address
// without any part that carries a key, so the parts it leaves out are written
// in that comment too, each beside its name.
package mcpimport

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Format is the one format the importer serves: every MCP client configuration
// and every registry server description.
const Format = "mcp"

// Syntax is the syntactic format a source is parsed as, and SyntaxJSONC the one
// a source listing its servers under servers, as Visual Studio Code writes them,
// is parsed as.
const (
	Syntax      = "json"
	SyntaxJSONC = "jsonc"
)

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

// addressField is the field a report names the parts of a server's address
// infra.endpoint leaves out by: the url a client's server and a registry
// description's remote write it under.
const addressField = "url"

// Import turns one import request into the drafts: each source — the request's
// named sources, or its primary bytes — is read through parse, the host's
// parser, and has its servers read. A source is parsed as SyntaxJSONC first; one
// holding an object at servers is read from the tree that parse returns, and
// every other is parsed again as Syntax and read from its own text, as
// sourceText says. A source a parser refuses is reported under parse-failed,
// which halts the import, and one listing no server under no-nodes; the import
// carries on with the other sources. A request naming a format other than Format
// reads no server.
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
		text, err := sourceText(src.Bytes, parse)
		if err != nil {
			out.Diagnostics = append(out.Diagnostics, importer.ParseFailed(importer.Named(in, src), in.Format, err))
			continue
		}
		var found []server
		if in.Format == Format {
			found = serversOf(text)
		}
		if len(found) == 0 {
			out.Diagnostics = append(out.Diagnostics, importer.NoNodes(importer.Named(in, src), in.Format))
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

	titles := make([]string, 0, len(servers))
	for _, s := range servers {
		titles = append(titles, s.title)
	}
	name := importer.DraftName(in, titles...)
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

// sourceText returns the JSON text a source's servers are read from, through
// parse: the source parsed as SyntaxJSONC, and, where its tree holds an object at
// servers — Visual Studio Code's configuration, which that client reads as JSON
// with comments — that tree, whose members are written in name order. Any other
// source is held to Syntax: parsed again as JSON, and returned as it is, so its
// members are read in the order it writes them. It returns the refusal of the
// parser the source is read through, and no text, where that parser refuses it.
//
// The order is not read from a commented source's own text: reading it would
// take a second reading of JSON with comments beside the parser's, and the
// parser's is the one the compiler keeps, so a configuration read with comments
// keeps the order its tree writes, whether or not it holds a comment.
func sourceText(source []byte, parse func(syntax string, source []byte) (json.RawMessage, error)) (json.RawMessage, error) {
	tree, err := parse(SyntaxJSONC, source)
	if err != nil {
		return nil, err
	}
	for _, e := range importer.ObjectEntries(tree) {
		if e.Key == "servers" && bytes.HasPrefix(bytes.TrimSpace(e.Value), []byte("{")) {
			return tree, nil
		}
	}
	if _, err := parse(Syntax, source); err != nil {
		return nil, err
	}
	return source, nil
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
// credential decision judges: under each server's key, its headers' values, its
// command line, its address, the text of each part of its address the address
// leaves out, and its environment's values, in the order the draft writes them.
// A value the source leaves for its user is written empty; a header, a variable
// or a part of the address whose value the draft does not write is written with
// none.
func draftedValues(servers []server) []byte {
	var b strings.Builder
	b.WriteString("{")
	for i, s := range servers {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(jsonString(s.key) + ":{")
		b.WriteString(`"headers":` + namedValues(s.headers) + ",")
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
		b.WriteString(`,"address":[`)
		for j, p := range s.addressParts {
			if j > 0 {
				b.WriteString(",")
			}
			if p.Reference {
				b.WriteString("null")
			} else {
				b.WriteString(jsonString(p.Text))
			}
		}
		b.WriteString(`],"env":` + namedValues(s.env) + "}")
	}
	b.WriteString("}")
	return []byte(b.String())
}

// namedValues writes the values of named as a JSON list, in their order: a value
// the draft writes as its text, and null for one it writes with none — a value
// the source does not write, or a single variable reference.
func namedValues(named []namedValue) string {
	var b strings.Builder
	b.WriteString("[")
	for j, e := range named {
		if j > 0 {
			b.WriteString(",")
		}
		if e.value != nil && !e.value.reference {
			b.WriteString(jsonString(e.value.text))
		} else {
			b.WriteString("null")
		}
	}
	b.WriteString("]")
	return b.String()
}

// located returns every value of draftedValues the credential decision judges,
// in the order the draft writes them: each header's value, each item of a
// command line, the address, each part of the source's address the address
// leaves out, and each environment value, where the value is not a single
// variable reference, as the plugin kit reads one for every format, and is not
// left for the user — named by its server and by its place: a header as
// headers.<name>, a part of the address by the source's url and the part's name,
// as AddressPartCredential names one, and every other value by its place in the
// Release.
func located(servers []server) []importer.Credential {
	var out []importer.Credential
	for _, s := range servers {
		for j, h := range s.headers {
			if h.value == nil || h.value.reference {
				continue
			}
			out = append(out, importer.Credential{Node: s.ref, Field: "headers." + h.name,
				Pointer: importer.Pointer(s.key, "headers", strconv.Itoa(j)), Class: h.value.class})
		}
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
		for j, p := range s.addressParts {
			if p.Reference || p.Text == "" {
				continue
			}
			out = append(out, AddressPartCredential(s.ref, addressField, importer.Pointer(s.key, "address",
				strconv.Itoa(j)), p))
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
// written empty, an address or an environment value that held something and is
// written empty is left for the Release, and a header's value or a part of the
// address that held something and is written empty is left out, the header or
// the part named alone. A value the decision carried is left exactly as the
// source writes it.
func readBack(servers []server, written []byte) error {
	var values map[string]struct {
		Headers  []*string `json:"headers"`
		Command  []string  `json:"stdio_command"`
		Endpoint *string   `json:"endpoint"`
		Address  []*string `json:"address"`
		Env      []*string `json:"env"`
	}
	if err := json.Unmarshal(written, &values); err != nil {
		return err
	}
	for i := range servers {
		s := &servers[i]
		v := values[s.key]
		for j := range s.headers {
			if emptiedAt(s.headers[j], v.Headers, j) {
				s.headers[j].value, s.headers[j].emptied = nil, true
			}
		}
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
		for j := range s.addressParts {
			p := &s.addressParts[j]
			if !p.Reference && p.Text != "" && j < len(v.Address) && v.Address[j] != nil && *v.Address[j] == "" {
				p.Emptied = true
			}
		}
		for j := range s.env {
			if emptiedAt(s.env[j], v.Env, j) {
				s.env[j].value = nil
			}
		}
	}
	return nil
}

// emptiedAt reports whether the credential decision wrote empty the value of e,
// the j-th of the values written: a value e holds something in, that is not a
// single variable reference, written as "".
func emptiedAt(e namedValue, written []*string, j int) bool {
	return e.value != nil && !e.value.reference && e.value.text != "" && j < len(written) && written[j] != nil &&
		*written[j] == ""
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
