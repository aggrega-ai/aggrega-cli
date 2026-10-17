// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// This file holds how the flow core/ai-agent/langflow carries reaches each part
// of it the draft wires to a socket of the platform, by the forms the platform's
// Recipes deliver a connection in:
//
//   - a tools connection is written into the flow: the Recipe writes the address
//     the tool publishes into the server configuration of each MCP Tools node
//     whose mcp_server field names the connection's alias, so an MCP Tools node
//     reaches its server by name, and the address the exported flow holds there
//     is written empty;
//   - a model or vector-store connection is delivered as environment variables,
//     which Langflow reads through a field naming a global variable: a field
//     whose object carries load_from_db true holds the variable's name as its
//     value, and Langflow reads the stored global variable of that name, or the
//     environment variable of that name where none is stored.

// reachOf reads how a Langflow node reaches the part it stands for. A node whose
// template carries the mcp_server field reaches the MCP server it names — a name
// alone, or an object holding a name and a server configuration — by that name,
// the way the platform's Recipes match it to a tools connection. One naming no
// server, and one whose configuration starts a server of its own, which Langflow
// would run in place of a connection's address, are wired to no socket. Every
// other node is a part of its own, if it is a part at all.
func reachOf(tree json.RawMessage, node importer.Node) importer.Reach {
	_, template, ok := nodeTemplate(tree, node.ID)
	if !ok {
		return importer.Reach{}
	}
	server, ok := serverOf(template)
	switch {
	case !ok:
		return importer.Reach{}
	case server.name == "":
		return importer.Reach{Refused: "it names no MCP server, so no tools connection can reach it"}
	case server.starts != "":
		return importer.Reach{Refused: fmt.Sprintf("its server configuration starts a server of its own (%s), which "+
			"Langflow would run in place of a connection's address, so it is wired to no tools connection; the "+
			"platform's build halts on it, since it names the MCP server %q, which no tools connection answers: remove "+
			"the command and the Stdio mode from its configuration to reach the server over HTTP through a connection, "+
			"or remove the node", server.starts, server.name)}
	}
	return importer.Reach{Name: server.name}
}

// refuseOf says why a Langflow node cannot reach a part through a connection of
// socket. The platform writes a tools connection's address only into an MCP
// Tools node whose mcp_server field names the connection, so a node whose
// template carries no mcp_server field — whatever its type, and whatever a
// mapping row or override maps it to — is refused the tools socket: a tools
// connection wired to it would be named by no node, and the platform's build
// halts on such a connection. Every other socket it may take.
func refuseOf(tree json.RawMessage, node importer.Node, socket string) string {
	if socket != "tools" {
		return ""
	}
	if _, template, ok := nodeTemplate(tree, node.ID); ok && memberOf(template, "mcp_server") != nil {
		return ""
	}
	return "Langflow reaches a tools connection only through an MCP Tools node naming it in its mcp_server field, " +
		"which this node does not carry, so a tools connection wired to it would be named by no node of the flow and " +
		"the platform's build would halt on it"
}

// wireOf says how the flow reaches a part wired to a socket of the platform. An
// MCP Tools node wired to the tools socket reaches its server through the
// connection: the address its server configuration holds is written empty, since
// the platform writes the connection's own there, and where the connection's
// alias differs from the name the node gives its server, the node is given the
// alias. Any other part reaches its connection through the fields of its node
// that name the variables the platform delivers for it, each written with that
// variable's name — a name only, never a value — where the node is of a type
// whose fields the importer knows and the field can name a variable in this
// export; otherwise the part is reported as reached other than through its
// connection.
func wireOf(tree json.RawMessage, part importer.Part) importer.Wiring {
	index, template, ok := nodeTemplate(tree, part.Node.ID)
	if !ok {
		return importer.Wiring{}
	}
	at := func(tokens ...string) string {
		return importer.Pointer(append([]string{"data", "nodes", strconv.Itoa(index), "data", "node", "template"},
			tokens...)...)
	}
	if server, ok := serverOf(template); ok && part.Name != "" {
		return wireServer(server, part, at)
	}
	return wireFields(template, part, at)
}

// wireServer is wireOf for an MCP Tools node.
func wireServer(server mcpServer, part importer.Part, at func(tokens ...string) string) importer.Wiring {
	if part.Socket != "tools" {
		return importer.Wiring{Literal: fmt.Sprintf("Langflow reaches an MCP server through a tools connection alone, "+
			"whose address the platform writes into the node's server configuration; wired to the %s socket, the node "+
			"reaches its server as the flow writes it", part.Socket)}
	}
	var w importer.Wiring
	reads := []string{"the platform writes the address the tool publishes into the node's server configuration, and, " +
		"where the connection carries a credential, the name of the variable holding it under the header the tool " +
		"publishes"}
	if part.Alias != part.Name {
		w.Edits = append(w.Edits, importer.Edit{Pointer: at(server.nameAt...), Value: jsonString(part.Alias)})
		reads = append(reads, "the node names its server "+part.Alias+", the connection's alias")
	}
	if server.hasURL {
		w.Edits = append(w.Edits, importer.Edit{Pointer: at("mcp_server", "value", "config", "url"), Value: jsonString("")})
		reads = append(reads, "the address the exported flow held there is written empty")
	}
	w.Reads = strings.Join(reads, "; ")
	w.LeftForRelease = []string{fmt.Sprintf("infra.endpoint of %s, the address of the MCP server the flow names %s",
		part.Key, part.Alias)}
	return w
}

// connectionField is one field of a node that names a variable the platform
// delivers for the node's connection: the field, the variable's name for the
// connection's alias, and whether the variable carries the part's address — a
// field carrying only a credential leaves the address where it was.
type connectionField struct {
	field    string
	variable func(alias string) string
	address  bool
}

// named returns the name of a variable the platform delivers whatever the
// connection's alias.
func named(name string) func(string) string {
	return func(string) string { return name }
}

// storeVariable returns the name of the vector-store variable ending in suffix
// the platform delivers for a connection: AGGREGA_VECTOR_STORE_, the alias as
// the platform names it, an underscore and the suffix.
func storeVariable(suffix string) func(string) string {
	return func(alias string) string {
		return "AGGREGA_VECTOR_STORE_" + importer.VariableName(alias) + "_" + suffix
	}
}

// connectionFields are, by node type and then by socket, the fields of a Langflow
// node that name the variables the platform delivers for the connection it is
// wired through, read off Langflow's own components: its OpenAI chat model and
// OpenAI embeddings components take their API's address in openai_api_base —
// the chat model falls back to OpenAI's public address, not to an environment
// variable, where it is empty — and their key in api_key and openai_api_key; its
// PGVector component takes the whole connection string in pg_server_url.
var connectionFields = map[string]map[string][]connectionField{
	"OpenAIModel": {"model": {
		{field: "openai_api_base", variable: named("OPENAI_BASE_URL"), address: true},
		{field: "api_key", variable: named("OPENAI_API_KEY")},
	}},
	"OpenAIEmbeddings": {"model": {
		{field: "openai_api_base", variable: named("OPENAI_BASE_URL"), address: true},
		{field: "openai_api_key", variable: named("OPENAI_API_KEY")},
	}},
	"pgvector": {"vector_store": {
		{field: "pg_server_url", variable: storeVariable("CONNECTION_STRING"), address: true},
	}},
}

// socketVariables names the variables the platform delivers for a connection of
// socket written under alias.
func socketVariables(socket, alias string) []string {
	switch socket {
	case "model":
		return []string{"OPENAI_BASE_URL", "OPENAI_API_KEY"}
	case "vector_store":
		return []string{storeVariable("URL")(alias), storeVariable("CREDENTIAL")(alias),
			storeVariable("CONNECTION_STRING")(alias)}
	}
	return nil
}

// wireFields is wireOf for a part reached through the fields of its node.
func wireFields(template json.RawMessage, part importer.Part, at func(tokens ...string) string) importer.Wiring {
	fields, known := connectionFields[part.Node.Type][part.Socket]
	if !known {
		return importer.Wiring{Literal: fmt.Sprintf("the importer knows no field of a Langflow %s node that names a "+
			"variable the platform delivers for its %s connection (%s), so the node reaches the part at the address its "+
			"own fields or its component's defaults give", part.Node.Type, part.Socket,
			strings.Join(socketVariables(part.Socket, part.Alias), ", "))}
	}
	var w importer.Wiring
	var reads, literal []string
	for _, f := range fields {
		variable := f.variable(part.Alias)
		why := ""
		raw := memberOf(template, f.field)
		switch {
		case raw == nil:
			why = "is not in the exported flow"
		case memberOf(raw, "load_from_db") == nil:
			why = "carries no load_from_db member in this export"
		case memberOf(raw, "value") == nil:
			why = "carries no value member in this export"
		}
		if why != "" {
			if f.address {
				literal = append(literal, fmt.Sprintf("its field %s %s, so it cannot name %s, the variable the platform "+
					"delivers for this connection, and the node reaches the part at the address the flow or its "+
					"component's defaults give", f.field, why, variable))
			}
			continue
		}
		w.Edits = append(w.Edits,
			importer.Edit{Pointer: at(f.field, "value"), Value: jsonString(variable)},
			importer.Edit{Pointer: at(f.field, "load_from_db"), Value: json.RawMessage("true")})
		reads = append(reads, fmt.Sprintf("its field %s names the variable %s", f.field, variable))
	}
	if len(reads) > 0 {
		w.Reads = strings.Join(reads, ", and ") + ", which the platform delivers for this connection and Langflow reads " +
			"as a global variable"
	}
	w.Literal = strings.Join(literal, "; ")
	return w
}

// mcpServer is the MCP server an MCP Tools node names in its mcp_server field:
// the name, the reference tokens beneath the node's template addressing it, and
// what the node's server configuration holds of an address or a server of its
// own.
type mcpServer struct {
	name   string
	nameAt []string
	hasURL bool
	// starts, where set, says what in the server configuration starts a server
	// of its own: a command, or the Stdio mode.
	starts string
}

// serverOf reads the MCP server a node's template names in its mcp_server field,
// as the platform's Recipes read it: the field's value is the server's name, or
// an object whose name is the server's name and whose config is its server
// configuration. It reports false for a template carrying no mcp_server field. A
// name that is not a string names no server.
func serverOf(template json.RawMessage) (mcpServer, bool) {
	field := memberOf(template, "mcp_server")
	if field == nil {
		return mcpServer{}, false
	}
	value := memberOf(field, "value")
	var name string
	if json.Unmarshal(value, &name) == nil {
		return mcpServer{name: name, nameAt: []string{"mcp_server", "value"}}, true
	}
	var s mcpServer
	if raw := memberOf(value, "name"); raw != nil && json.Unmarshal(raw, &name) == nil {
		s.name = name
		s.nameAt = []string{"mcp_server", "value", "name"}
	}
	for _, e := range importer.ObjectEntries(memberOf(value, "config")) {
		switch e.Key {
		case "url":
			s.hasURL = true
		case "command":
			s.starts = "it carries a command"
		case "mode":
			var mode string
			if json.Unmarshal(e.Value, &mode) == nil && mode == "Stdio" && s.starts == "" {
				s.starts = "it sets the Stdio mode"
			}
		}
	}
	return s, true
}

// nodeTemplate returns the position, in the flow's node list, of the node whose
// id is id, and the template its data.node carries; false where no node has that
// id.
func nodeTemplate(tree json.RawMessage, id string) (int, json.RawMessage, bool) {
	var top struct {
		Data struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return 0, nil, false
	}
	for i, raw := range top.Data.Nodes {
		var node struct {
			ID   string `json:"id"`
			Data struct {
				Node struct {
					Template json.RawMessage `json:"template"`
				} `json:"node"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &node) == nil && node.ID == id {
			return i, node.Data.Node.Template, true
		}
	}
	return 0, nil, false
}

// memberOf returns the value of the first member of a JSON object named key, nil
// where the object holds none or raw is not an object.
func memberOf(raw json.RawMessage, key string) json.RawMessage {
	for _, e := range importer.ObjectEntries(raw) {
		if e.Key == key {
			return e.Value
		}
	}
	return nil
}

// jsonString writes s as a JSON string.
func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
