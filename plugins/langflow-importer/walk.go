// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpconfig"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// walkTree lifts the nodes and edges out of the canonical tree of a Langflow
// flow. It reads the already-parsed tree the host returned — it runs no grammar
// of its own. Langflow nests its graph under a top-level data object. An
// unrecognised shape yields no nodes, which the importer surfaces as a diagnostic
// rather than a silent empty import.
func walkTree(tree json.RawMessage) ([]importer.Node, []importer.Edge) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(tree, &top); err != nil {
		return nil, nil
	}

	var nodesRaw, edgesRaw json.RawMessage
	var data map[string]json.RawMessage
	if err := json.Unmarshal(top["data"], &data); err == nil {
		nodesRaw = data["nodes"]
		edgesRaw = data["edges"]
	}

	return importer.NodeList(nodesRaw, liftNode), importer.EdgeList(edgesRaw)
}

// liftNode reads one element of a Langflow node list: the node's type from its
// data, and its parameter fields from the template its data.node carries. A note
// pinned to the canvas is not lifted.
func liftNode(id, outerType string, data map[string]json.RawMessage) (importer.Node, bool) {
	if isCanvasNote(outerType) {
		return importer.Node{}, false
	}
	var typ string
	_ = json.Unmarshal(data["type"], &typ)
	fields, secrets := templateFields(nestedTemplate(data["node"]))
	return importer.Node{ID: id, Type: typ, Fields: fields, Secrets: secrets}, true
}

// isCanvasNote reports whether a node's outer type marks it as a note an author
// pinned to the flow's canvas — Langflow's noteNode. A note documents the flow and
// runs nothing, so it is not lifted as a node: lifting one would draft a component
// for every note. An edge naming a note is then dropped, as is any edge naming a
// node that was not lifted.
func isCanvasNote(outerType string) bool {
	return outerType == "noteNode"
}

// nestedTemplate returns the template object of a langflow node's data.node,
// where the node's parameter fields live.
func nestedTemplate(nodeRaw json.RawMessage) json.RawMessage {
	var node map[string]json.RawMessage
	if err := json.Unmarshal(nodeRaw, &node); err != nil {
		return nil
	}
	return node["template"]
}

// templateFields returns the names of a langflow template's parameter fields, in
// the order the tree presents them, split into value fields and credential
// fields. A template also carries entries that are not parameters — a string
// naming the template's own kind, a boolean refresh flag, an object holding only
// a value the editor keeps for itself — and only an entry whose value is an
// object carrying a type is a parameter field.
//
// A parameter field is a credential when langflow marks it as one: its object
// carries password set to true, which is how langflow declares a field whose
// value is secret. The name plays no part, so a field named like a credential
// that langflow does not mark is a value field, and a marked field is a
// credential whatever it is called. Only the type and password members are read;
// the field's value is never decoded.
func templateFields(raw json.RawMessage) (fields, secrets []string) {
	for _, e := range importer.ObjectEntries(raw) {
		var field struct {
			Type     json.RawMessage `json:"type"`
			Password json.RawMessage `json:"password"`
		}
		if err := json.Unmarshal(e.Value, &field); err != nil || field.Type == nil {
			continue
		}
		if string(field.Password) == "true" {
			secrets = append(secrets, e.Key)
		} else {
			fields = append(fields, e.Key)
		}
	}
	return fields, secrets
}

// flowID returns the identifier a Langflow export gives its flow: the string at
// its top-level id, empty where it carries none. The id is read with the flow's
// node list (wire.go), in the one decoding of the tree they share.
func flowID(tree json.RawMessage) string {
	var id string
	if err := json.Unmarshal(nodesOf(tree).id, &id); err != nil {
		return ""
	}
	return id
}

// flowName returns the name a Langflow export gives its flow, the one Langflow
// lists the flow under: the string at its top-level name, empty where it carries
// none. The name is read with the flow's node list (wire.go), in the one
// decoding of the tree they share.
func flowName(tree json.RawMessage) string {
	var name string
	if err := json.Unmarshal(nodesOf(tree).name, &name); err != nil {
		return ""
	}
	return name
}

// credentialsOf locates every value a Langflow flow holds that can carry a
// credential, each of its class, every class every time:
//
//   - marked: the value of each template field whose object carries password set
//     to true, the mark Langflow itself writes;
//   - an MCP server's members: each header and environment value of the server
//     configuration an MCP Tools node carries in its mcp_server field, the
//     server's address, the command starting a server and each argument it is
//     started with; an MCP Tools node carrying no mcp_server field writes its
//     server in fields of its own instead, whose values are located as
//     fieldServerCredentials says; and the fields of the inputs unmarkedFields
//     names as a server's, an MCP Tools node's own headers among them;
//   - unmarked: every other input Langflow marks as no credential and that can
//     carry one, named in unmarkedFields, by node type and field, or in
//     unmarkedAnyType, by field, with how its value is located.
//
// Each is addressed from the root of the export, and named by its node's id and
// the field it sits in. A field holding no value member is not located, since
// there is nothing in it; nor is a field marked password as anything else.
// Which of them is written empty is the empty_credentials setting's, which the
// plugin kit reads; a value the wiring writes (wire.go) is the wiring's.
func credentialsOf(tree json.RawMessage) []importer.Credential {
	var out []importer.Credential
	for _, node := range nodesOf(tree).list {
		at := func(tokens ...string) string {
			return importer.Pointer(append([]string{"data", "nodes", strconv.Itoa(node.position), "data", "node", "template"},
				tokens...)...)
		}
		for _, e := range node.template.entries {
			var field struct {
				Password json.RawMessage `json:"password"`
				Value    json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal(e.Value, &field); err != nil || field.Value == nil {
				continue
			}
			marked := string(field.Password) == "true"
			if marked {
				out = append(out, importer.Credential{Node: node.id, Field: e.Key, Pointer: at(e.Key, "value"),
					Class: importer.CredentialMarked})
			}
			if e.Key == "mcp_server" {
				out = append(out, serverCredentials(node.id, field.Value, at)...)
			}
			if marked {
				continue
			}
			if u, ok := unmarkedOf(node.typ, e.Key); ok {
				out = append(out, u.locate(node.id, e.Key, e.Value, at)...)
			}
		}
		if node.typ == mcpToolsType && node.template.member("mcp_server") == nil {
			out = append(out, fieldServerCredentials(node.id, node.template.raw, at)...)
		}
	}
	return out
}

// mcpToolsType is the type Langflow gives its MCP Tools node.
const mcpToolsType = "MCPTools"

// serverCredentials locates, in the server configuration an MCP Tools node's
// mcp_server value carries — an object with a name and a config — each header
// value, each environment value, the server's address, the command and each
// argument, as mcpconfig reads such a configuration, each a member of an MCP
// server's configuration. The address is located whether or not the node is
// wired, since it can carry a key in its user part, its path or its query.
// Where the node is wired to a tools connection, the edit wireServer (wire.go)
// makes at that address writes it empty, the platform writing the connection's
// own address there, and an edit wins over a located value at the same place,
// so a wired node's address is reported once, with how the node reaches its
// connection, and never as a credential value. Written empty, a command or an
// argument keeps its member, so a configuration that starts a server of its own
// still reads as one. A value naming a server alone carries no configuration and
// holds none.
func serverCredentials(nodeID string, value json.RawMessage, at func(tokens ...string) string) []importer.Credential {
	var out []importer.Credential
	for _, m := range mcpconfig.Members(memberOf(value, "config")) {
		c := importer.Credential{Node: nodeID, Class: importer.CredentialServerMember,
			Pointer: at(append([]string{"mcp_server", "value", "config"}, m.Tokens...)...)}
		switch m.Kind {
		case mcpconfig.Header:
			c.Field = "mcp_server.config.headers." + m.Name
		case mcpconfig.Env:
			c.Field = "mcp_server.config.env." + m.Name
		case mcpconfig.Address:
			c.Field, c.Detail = "mcp_server.config.url", addressDetail
		case mcpconfig.Command:
			c.Field, c.Detail = "mcp_server.config.command", commandDetail
		case mcpconfig.Argument:
			c.Field, c.Detail = "mcp_server.config.args", argumentDetail
			if m.Name != "" {
				c.Field += "." + m.Name
			}
		default:
			continue
		}
		out = append(out, c)
	}
	return out
}

// fieldServerCredentials locates the server an MCP Tools node carrying no
// mcp_server field writes in fields of its own template, as mcpconfig reads
// that layout: each header value of headers_input, each entry of env, the
// address in sse_url, and the whole command line in command, the arguments it
// starts the server with included. Langflow marks none of them as a credential,
// and each can carry one; each is a member of an MCP server's configuration,
// and written empty keeps its member. Each is named by the template field it
// sits in: a header by its name, and an entry of env, written empty whole since
// the variable's name and its value share one text, by its position where env
// is a list, so a report names no part of a value. Such a node names no MCP
// server, so no tools connection reaches it and nothing else is written into
// its server.
func fieldServerCredentials(nodeID string, template json.RawMessage,
	at func(tokens ...string) string) []importer.Credential {
	var out []importer.Credential
	for _, m := range mcpconfig.FieldMembers(template) {
		detail, ok := fieldDetails[m.Kind]
		if !ok {
			continue
		}
		c := importer.Credential{Node: nodeID, Field: m.Tokens[0], Pointer: at(m.Tokens...), Detail: detail,
			Class: importer.CredentialServerMember}
		if m.Name != "" {
			c.Field += "." + m.Name
		}
		out = append(out, c)
	}
	return out
}

// fieldDetails says, by kind, why a value an MCP Tools node writes in fields of
// its own is written empty.
var fieldDetails = map[mcpconfig.Kind]string{
	mcpconfig.Header:  headerDetail,
	mcpconfig.Env:     envDetail,
	mcpconfig.Address: addressDetail,
	mcpconfig.Command: commandLineDetail,
}

// scrub is how the value of an input Langflow marks as no credential is written
// empty.
type scrub int

const (
	// headerValues writes each value the headers hold empty and keeps each
	// header's name, as mcpconfig.HeaderValues reads headers — and so for query
	// parameters, which a flow writes in the same shapes; each value is reported
	// by its header's or its parameter's name. A value that is neither an object
	// nor a list is written empty whole.
	headerValues scrub = iota
	// wholeKept writes the whole value empty with its kind kept — an object as
	// {}, a list as [] — reported once; a value of any other kind is written as
	// an empty text.
	wholeKept
	// wholeText writes the whole value as an empty text, reported once.
	wholeText
)

// unmarked is one input Langflow marks as no credential that can carry one: how
// its value is written empty, why, and, for wholeKept, what of an object or a
// list is; and whether it is a member of an MCP server's configuration rather
// than an input of any other part of the flow, which decides its class.
type unmarked struct {
	scrub  scrub
	why    string
	parts  string
	server bool
}

// class is the class of a value the input holds: a member of an MCP server's
// configuration, or an input its format marks as no credential.
func (u unmarked) class() importer.CredentialClass {
	if u.server {
		return importer.CredentialServerMember
	}
	return importer.CredentialUnmarked
}

// unmarkedFields are, by the type Langflow gives a node and then by field, the
// inputs Langflow marks as no credential that can carry one, each located on
// every node of its type, each of its class:
//
//   - the request headers of an API Request, a URL, a Docling Remote, a Cloudflare
//     Workers AI Embeddings and a Confluent Kafka Producer node: each value, its
//     header's name kept, since a name carries no secret;
//   - the query parameters an API Request node appends to its address and those
//     a Google Serper, a SerpAPI, a Serp and a SearchApi node send their search
//     API: each value, its parameter's name kept, as a header's;
//   - an OpenAI Embeddings node's default headers and default query parameters:
//     whole, its kind kept, since a header or a parameter whose name is kept and
//     whose value is emptied would still be sent — the client sends its default
//     headers over those it builds from its key, and merges its default query
//     over the query of the address it calls — while an empty object or list
//     sends nothing. On a node the platform's model connection reaches, the
//     wiring writes them empty instead (wire.go), since kept they would be sent
//     in place of what that connection supplies;
//   - a whole request written as a command line: an API Request node's cURL text,
//     which only fills the node's fields in the editor, and a Webhook node's,
//     which only shows an example;
//   - an Olivya node's API key and a Mem0 node's configuration, the second
//     whole with its kind kept;
//   - as members of an MCP server's configuration: an MCP Tools node's own
//     headers, whole with its kind kept for the reason an OpenAI Embeddings
//     node's are, which the wiring writes empty instead on a node wired to a
//     tools connection, into whose server configuration the platform writes the
//     connection (wire.go); and the one field each of Langflow's deprecated MCP
//     components keeps its server in, an MCPSse node's address in url and an
//     MCPStdio node's command line, its arguments included, in command. Neither
//     deprecated component names an MCP server, so no tools connection reaches
//     it.
var unmarkedFields = map[string]map[string]unmarked{
	mcpToolsType:                    {"headers": mcpToolsHeaders},
	embeddingsType:                  {"default_headers": embeddingHeaders, "default_query": embeddingQuery},
	"APIRequest":                    {"headers": {scrub: headerValues, why: headerValueDetail}, "curl_input": {scrub: wholeText, why: requestTextDetail}, "query_params": queryParameters},
	"GoogleSerperAPI":               {"query_params": queryParameters},
	"SerpAPI":                       {"search_params": queryParameters},
	"Serp":                          {"search_params": queryParameters},
	"SearchAPI":                     {"search_params": queryParameters},
	"SearchComponent":               {"search_params": queryParameters},
	"URLComponent":                  {"headers": {scrub: headerValues, why: headerValueDetail}},
	"DoclingRemote":                 {"api_headers": {scrub: headerValues, why: headerValueDetail}},
	"CloudflareWorkersAIEmbeddings": {"headers": {scrub: headerValues, why: headerValueDetail}},
	"ConfluentKafkaProducer":        {"headers": {scrub: headerValues, why: headerValueDetail}},
	"Webhook":                       {"curl": {scrub: wholeText, why: requestTextDetail}},
	"OlivyaComponent":               {"api_key": {scrub: wholeText, why: apiKeyDetail}},
	"mem0_chat_memory":              {"mem0_config": {scrub: wholeKept, why: memoryConfigDetail, parts: "configuration members"}},
	"MCPSse":                        {"url": {scrub: wholeText, why: addressDetail, server: true}},
	"MCPStdio":                      {"command": {scrub: wholeText, why: commandLineDetail, server: true}},
}

// queryParameters is how the query parameters a request is sent with are
// written empty.
var queryParameters = unmarked{scrub: headerValues, why: queryParameterDetail}

// mcpToolsHeaders, embeddingHeaders and embeddingQuery are an MCP Tools node's
// own headers and an OpenAI Embeddings node's default headers and default
// query, each written empty whole with its kind kept, as the locator and as
// the wiring (wire.go) write them.
var (
	mcpToolsHeaders  = unmarked{scrub: wholeKept, why: mcpHeadersDetail, parts: headerNamesAndValues, server: true}
	embeddingHeaders = unmarked{scrub: wholeKept, why: embeddingHeadersDetail, parts: headerNamesAndValues}
	embeddingQuery   = unmarked{scrub: wholeKept, why: embeddingQueryDetail, parts: queryNamesAndValues}
)

// unmarkedAnyType are, by field, the inputs Langflow marks as no credential that
// can carry one on whichever node declares them: the verification token every
// Composio component inherits from the one base that declares it.
var unmarkedAnyType = map[string]unmarked{
	"verification_token": {scrub: wholeText, why: verificationTokenDetail},
}

// unmarkedOf returns how the value of the field named field of a node of type
// typ is written empty where Langflow marks it as no credential, and false
// where it is no such input.
func unmarkedOf(typ, field string) (unmarked, bool) {
	if u, ok := unmarkedFields[typ][field]; ok {
		return u, true
	}
	u, ok := unmarkedAnyType[field]
	return u, ok
}

// locate locates, in the template field named name — field is its object —
// the values u names, of its class, reading each value member the field writes,
// so a field writing one twice is located where any of them holds something. A
// header or a parameter holding nothing, an empty text or null, is not located.
func (u unmarked) locate(nodeID, name string, field json.RawMessage, at func(tokens ...string) string) []importer.Credential {
	var out []importer.Credential
	seen := map[string]bool{}
	for _, v := range importer.ObjectEntries(field) {
		if v.Key != "value" {
			continue
		}
		switch u.scrub {
		case headerValues:
			for _, c := range headerValueCredentials(nodeID, name, v.Value, u.why, at) {
				if !seen[c.Pointer] {
					seen[c.Pointer] = true
					c.Class = u.class()
					out = append(out, c)
				}
			}
		case wholeKept:
			if c, ok := wholeKeptCredential(nodeID, name, v.Value, u, at); ok {
				c.Class = u.class()
				return []importer.Credential{c}
			}
		case wholeText:
			return []importer.Credential{{Node: nodeID, Field: name, Pointer: at(name, "value"), Detail: u.why,
				Class: u.class()}}
		}
	}
	return out
}

// wholeKeptEdit returns the edit writing the value of the template field named
// name empty whole with its kind kept, as u locates it — the wiring's way of
// writing what a node would send in place of what its connection supplies — and
// false where the field holds nothing to write empty. Of a field writing its
// value twice, the first holding something decides what is written at both.
func (u unmarked) wholeKeptEdit(template flowTemplate, name string, at func(tokens ...string) string) (importer.Edit, bool) {
	for _, v := range importer.ObjectEntries(template.member(name)) {
		if v.Key != "value" {
			continue
		}
		if c, ok := wholeKeptCredential("", name, v.Value, u, at); ok {
			return importer.Edit{Pointer: c.Pointer, Value: firstJSON(c.Value, json.RawMessage(`""`))}, true
		}
	}
	return importer.Edit{}, false
}

// firstJSON returns the first of its arguments that is set.
func firstJSON(values ...json.RawMessage) json.RawMessage {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// headerValueCredentials locates each value the headers, or the query
// parameters, of the field named name hold, as mcpconfig.HeaderValues reads
// them, named by the field and the header's or the parameter's name, and the
// whole value where it is neither an object nor a list and holds something.
func headerValueCredentials(nodeID, name string, value json.RawMessage, why string,
	at func(tokens ...string) string) []importer.Credential {
	if written, ok := mcpconfig.EmptyKept(value); ok && written == nil {
		return []importer.Credential{{Node: nodeID, Field: name, Pointer: at(name, "value"), Detail: why}}
	}
	var out []importer.Credential
	for _, m := range mcpconfig.HeaderValues(value, name, "value") {
		out = append(out, importer.Credential{Node: nodeID, Field: name + "." + m.Name, Pointer: at(m.Tokens...),
			Detail: why})
	}
	return out
}

// wholeKeptCredential locates the whole value of the field named name, written
// empty with its kind kept as mcpconfig.EmptyKept writes it: an object or a
// list reported with what of it is written empty, a value of any other kind
// with why.
func wholeKeptCredential(nodeID, name string, value json.RawMessage, u unmarked,
	at func(tokens ...string) string) (importer.Credential, bool) {
	written, ok := mcpconfig.EmptyKept(value)
	c := importer.Credential{Node: nodeID, Field: name, Pointer: at(name, "value"), Detail: u.why}
	if written != nil {
		c.Value, c.Detail = written, u.parts
	}
	return c, ok
}

// The details saying why an input Langflow marks as no credential is written
// empty, and headerNamesAndValues and queryNamesAndValues, what of headers and of
// query parameters written whole is.
const (
	mcpHeadersDetail = "the headers an MCP Tools node sends its server can carry a credential, though Langflow marks " +
		"them as none"
	embeddingHeadersDetail = "the headers an OpenAI Embeddings node sends its API can carry a credential, though " +
		"Langflow marks them as none"
	embeddingQueryDetail = "the query parameters an OpenAI Embeddings node sends its API can carry a credential, " +
		"though Langflow marks them as none"
	headerNamesAndValues = "header names and values"
	queryNamesAndValues  = "query parameter names and values"
	headerValueDetail    = "a header can carry a credential, though Langflow marks it as none"
	queryParameterDetail = "a query parameter can carry a credential, though Langflow marks it as none"
	requestTextDetail    = "a request written as a command line can carry a credential in a header or a user part, " +
		"though Langflow marks it as none"
	apiKeyDetail            = "the field holds an API key, though Langflow marks it as no credential"
	memoryConfigDetail      = "a memory store's configuration can carry a credential, though Langflow marks it as none"
	verificationTokenDetail = "the field holds a verification token, though Langflow marks it as no credential"
)

// addressDetail, commandDetail and argumentDetail say why the address an MCP
// server is reached at, the command it is started with, and each of its
// arguments, is written empty though Langflow marks none of them as a
// credential; headerDetail, envDetail and commandLineDetail say why a header
// value, an environment entry and a whole command line are, where an MCP Tools
// node writes its server in fields of its own.
const (
	addressDetail = "the address an MCP server is reached at can carry a credential in its user part, its path or " +
		"its query, though Langflow marks it as none"
	commandDetail  = "the command an MCP server is started with can carry a credential, though Langflow marks it as none"
	argumentDetail = "an argument an MCP server is started with can carry a credential, though Langflow marks it as none"
	headerDetail   = "a header sent to an MCP server can carry a credential, though Langflow marks it as none"
	envDetail      = "an environment entry an MCP server is started with can carry a credential, though Langflow " +
		"marks it as none"
	commandLineDetail = "the command line an MCP server is started with, its arguments included, can carry a credential, " +
		"though Langflow marks it as none"
)
