// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpconfig"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
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

// credentialsOf locates every credential value a Langflow flow holds, by the
// marks Langflow itself writes: the value of each template field whose object
// carries password set to true, and each header and environment value of the
// server configuration an MCP Tools node carries in its mcp_server field, where
// a request's credentials travel — and, in that configuration, the server's
// address, the command starting a server and each argument it is started with,
// which Langflow marks as no credential and which can carry one all the same.
// An MCP Tools node carrying no mcp_server field writes its server in fields of
// its own instead, whose values are located as fieldServerCredentials says.
// Each is addressed from the root of the export, and named by its node's id and
// the field it sits in. A field holding no value member is not located, since
// there is nothing in it to write empty.
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
			if string(field.Password) == "true" {
				out = append(out, importer.Credential{Node: node.id, Field: e.Key, Pointer: at(e.Key, "value")})
			}
			if e.Key == "mcp_server" {
				out = append(out, serverCredentials(node.id, field.Value, at)...)
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
// argument, as mcpconfig reads such a configuration. The address is located
// whether or not the node is wired, since it can carry a key in its user part,
// its path or its query. Where the node is wired to a tools connection, the
// edit wireServer (wire.go) makes at that address writes it empty, the platform
// writing the connection's own address there, and an edit wins over a
// credential at the same value, so a wired node's address is reported once,
// with how the node reaches its connection, and never as an emptied credential.
// A command or an argument is written empty and its member kept, so a
// configuration that starts a server of its own still reads as one; one already
// empty is left as written. A value naming a server alone carries no
// configuration and holds none.
func serverCredentials(nodeID string, value json.RawMessage, at func(tokens ...string) string) []importer.Credential {
	var out []importer.Credential
	for _, m := range mcpconfig.Members(memberOf(value, "config")) {
		c := importer.Credential{Node: nodeID,
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
// and each can carry one, so each is written empty, its member kept. Each is
// named by the template field it sits in: a header by its name, and an entry of
// env, which is written empty whole since the variable's name and its value
// share one text, by its position where env is a list, so a report names no
// part of a value. Such a node names no MCP server, so no tools connection
// reaches it and nothing else is written into its server.
func fieldServerCredentials(nodeID string, template json.RawMessage,
	at func(tokens ...string) string) []importer.Credential {
	var out []importer.Credential
	for _, m := range mcpconfig.FieldMembers(template) {
		detail, ok := fieldDetails[m.Kind]
		if !ok {
			continue
		}
		c := importer.Credential{Node: nodeID, Field: m.Tokens[0], Pointer: at(m.Tokens...), Detail: detail}
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
