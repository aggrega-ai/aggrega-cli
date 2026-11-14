// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/treememo"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
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
//     environment variable of that name where none is stored. A model node no
//     field of which gives its API's address — Langflow's Language Model
//     component holds no such field for OpenAI — reads the model connection's
//     address from the environment all the same, as OPENAI_BASE_URL.

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
// halts on such a connection. A node of a type running on one of several model
// providers that names a provider other than the one the platform delivers its
// model connection for is refused the model socket (otherProvider). Every other
// socket it may take.
func refuseOf(tree json.RawMessage, node importer.Node, socket string) string {
	switch socket {
	case "model":
		return otherProvider(tree, node)
	case "tools":
	default:
		return ""
	}
	if _, template, ok := nodeTemplate(tree, node.ID); ok && template.member("mcp_server") != nil {
		return ""
	}
	return "Langflow reaches a tools connection only through an MCP Tools node naming it in its mcp_server field, " +
		"which this node does not carry, so a tools connection wired to it would be named by no node of the flow and " +
		"the platform's build would halt on it"
}

// otherProvider says why a node of a type running on one of several model
// providers — the Language Model and Embedding Model components — cannot take
// the platform's model connection where it names a provider other than the one
// the platform delivers that connection for, as providerStanding reads it: the
// model it names is not the model a connection of that socket reaches, and the
// connection's key would reach a provider it is not delivered for. A node naming
// that provider, or none the import can read — it selects none, names it through
// a global variable, or takes its model from another node — is not refused: the
// wiring (wireFields) writes the connection's variables into a node running on
// that provider, and none into a node whose provider it cannot read.
func otherProvider(tree json.RawMessage, node importer.Node) string {
	n, known := connectionFields[node.Type]["model"]
	if !known {
		return ""
	}
	_, template, ok := nodeTemplate(tree, node.ID)
	if !ok {
		return ""
	}
	why, readable := providerStanding(template, n)
	if !readable || why == "" {
		return ""
	}
	return fmt.Sprintf("the node %s, while the platform delivers its model connection (%s) to a node running on the "+
		"%s provider, so the model the node names is not one that connection reaches", why,
		strings.Join(socketVariables("model", ""), ", "), n.provider)
}

// wireOf says how the flow reaches a part wired to a socket of the platform. An
// MCP Tools node wired to the tools socket reaches its server through the
// connection: the address its server configuration holds is written empty, since
// the platform writes the connection's own there, and where the connection's
// alias differs from the name the node gives its server, the node is given the
// alias. Any other part reaches its connection through the fields of its node
// that name the variables the platform delivers for it, each written with that
// variable's name — a name only, never a value — where the node is of a type
// whose fields the importer knows, runs on the model provider those variables
// are delivered for, and the field can name a variable in this export, and
// through the environment where no field of it gives the part's address and its
// type reads that address from there; otherwise the part is reported as reached
// other than through its connection. On every wired node, what the node would
// send over what its connection supplies is written empty — an MCP Tools node's
// own headers, an OpenAI Embeddings node's default headers and default query —
// as part of how it reaches the connection, whatever the empty_credentials
// setting says.
func wireOf(tree json.RawMessage, part importer.Part) importer.Wiring {
	index, template, ok := nodeTemplate(tree, part.Node.ID)
	if !ok {
		return importer.Wiring{}
	}
	at := templateAt(index)
	var w importer.Wiring
	if server, ok := serverOf(template); ok && part.Name != "" {
		w = wireServer(server, part, at)
	} else {
		w = wireFields(template, part, at)
	}
	edits, said := ownHeaderEdits(template, part.Node.Type, at)
	if len(edits) == 0 {
		return w
	}
	w.Edits = append(w.Edits, edits...)
	if w.Reads != "" {
		w.Reads += "; " + said
	} else {
		w.Literal = strings.Join(nonEmpty(w.Literal, said), "; ")
	}
	return w
}

// templateAt returns how a value beneath the template of the node at position
// index of the flow's node list is addressed from the root of the export.
func templateAt(index int) func(tokens ...string) string {
	return func(tokens ...string) string {
		return importer.Pointer(append([]string{"data", "nodes", strconv.Itoa(index), "data", "node", "template"},
			tokens...)...)
	}
}

// nonEmpty returns those of its arguments that are not empty, in order.
func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ownHeaderEdits returns the edits writing empty, whole and with its kind kept,
// what a node of type typ would send over what the connection reaching it
// supplies — an MCP Tools node's own headers, which Langflow sends its server
// over those of its server configuration, where the platform writes what a
// tools connection presents; an OpenAI Embeddings node's default headers and
// default query, which its client sends over what it builds from the key and the
// address it reads — each where it holds something, and says which it writes.
// A header or a parameter written with its name kept and its value emptied
// would still be sent; an empty object or list sends nothing.
func ownHeaderEdits(template flowTemplate, typ string, at func(tokens ...string) string) ([]importer.Edit, string) {
	var edits []importer.Edit
	var written []string
	for _, f := range ownHeaders[typ] {
		if e, ok := f.u.wholeKeptEdit(template, f.name, at); ok {
			edits = append(edits, e)
			written = append(written, f.what)
		}
	}
	if len(edits) == 0 {
		return nil, ""
	}
	why := "since Langflow sends them over the headers of its server configuration, where the platform writes what " +
		"the connection presents"
	if typ == embeddingsType {
		why = "since its client sends them over what it builds from the key and the address the connection supplies"
	}
	return edits, strings.Join(written, " and ") + " are written empty, " + why
}

// embeddingsType is the type Langflow gives its OpenAI Embeddings node.
const embeddingsType = "OpenAIEmbeddings"

// ownHeader is one field in which a node keeps what it sends over what its
// connection supplies: the field, how a report names it, and how it is written
// empty.
type ownHeader struct {
	name, what string
	u          unmarked
}

// ownHeaders are, by node type, the fields ownHeaderEdits writes empty.
var ownHeaders = map[string][]ownHeader{
	mcpToolsType: {{name: "headers", what: "its own headers", u: mcpToolsHeaders}},
	embeddingsType: {
		{name: "default_headers", what: "its default headers", u: embeddingHeaders},
		{name: "default_query", what: "its default query", u: embeddingQuery},
	},
}

// reachedOf says how a model connection the draft wires reaches a node it wires
// to no socket as the node is written, for an OpenAI Embeddings node, the one
// node type whose own default headers and query the client sends over what that
// connection supplies: where a part is wired to the model socket and the node
// reaches it through that connection as standingOf reads it — its field
// carrying the address naming the variable the platform delivers — its default
// headers and default query are written empty, as on a wired node. Every other
// node is reached by none: an MCP Tools node the draft does not wire names no
// tools connection the platform writes into it.
func reachedOf(tree json.RawMessage, node importer.Node, wired []importer.Part) importer.Wiring {
	if node.Type != embeddingsType {
		return importer.Wiring{}
	}
	var model *importer.Part
	for i := range wired {
		if wired[i].Socket == "model" {
			model = &wired[i]
			break
		}
	}
	if model == nil {
		return importer.Wiring{}
	}
	through, how := standingOf(tree, importer.Part{Node: node, Socket: "model", Key: model.Key, Alias: model.Alias})
	if !through {
		return importer.Wiring{}
	}
	index, template, ok := nodeTemplate(tree, node.ID)
	if !ok {
		return importer.Wiring{}
	}
	edits, said := ownHeaderEdits(template, node.Type, templateAt(index))
	if len(edits) == 0 {
		return importer.Wiring{}
	}
	return importer.Wiring{Edits: edits, Reads: fmt.Sprintf("the model connection %s reaches it, since %s; %s",
		model.Alias, how, said)}
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
		w.Edits = append(w.Edits, importer.Edit{Pointer: at(server.nameAt...), Value: importer.String(part.Alias)})
		reads = append(reads, "the node names its server "+part.Alias+", the connection's alias")
	}
	// The credential locator (walk.go) locates this address on every node, and
	// the empty_credentials setting decides whether it is written empty; on a
	// wired node this edit writes it empty whatever the setting says, since the
	// platform writes the connection's own address there, so the address is
	// reported here, as part of how the node reaches its connection, and not as a
	// credential value.
	if server.hasURL {
		w.Edits = append(w.Edits, importer.Edit{Pointer: at("mcp_server", "value", "config", "url"), Value: importer.String("")})
		reads = append(reads, "the address the exported flow held there is written empty")
	}
	w.Reads = strings.Join(reads, "; ")
	w.LeftForRelease = []importer.LeftItem{{Path: "infra.endpoint",
		Text: "the address of the MCP server the flow names " + part.Alias}}
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

// connectionNode is how a Langflow node of one type reaches the connection of
// one socket it is wired through.
type connectionNode struct {
	// fields are the fields of the node naming the variables the platform
	// delivers for the connection.
	fields []connectionField
	// provider, where set, is the model provider the node must run on for its
	// fields to be written with those variables. A node of a type running on
	// several providers hands its key to the provider it runs on, and the
	// platform delivers its model connection for an OpenAI-compatible client, so
	// a node naming another provider is refused the socket (otherProvider), and a
	// node whose provider the import cannot read is written with none of them
	// (offProvider): the connection's key would otherwise reach another provider.
	provider string
	// environmentAddress, where set, is the variable the OpenAI client the node
	// runs reads the part's address from in the environment where no field of
	// the node gives one: none of its fields carries the address, or the one that
	// does is empty. It is set only beside the OpenAI provider, whose client that
	// is.
	environmentAddress string
}

// connectionFields are, by node type and then by socket, how a Langflow node
// reaches the connection it is wired through, read off Langflow's own
// components: its OpenAI chat model and OpenAI embeddings components take their
// API's address in openai_api_base — the chat model falls back to OpenAI's
// public address, not to an environment variable, where it is empty — and their
// key in api_key and openai_api_key; its Language Model and Embedding Model
// components, which run on one of several providers, take their key in api_key,
// and reach OPENAI_BASE_URL from the environment where no field gives their
// API's address — the Language Model component has no such field for OpenAI, the
// Embedding Model component's api_base is one, read where it is set; its
// PGVector component takes the whole connection string in pg_server_url.
var connectionFields = map[string]map[string]connectionNode{
	"OpenAIModel": {"model": {fields: []connectionField{
		{field: "openai_api_base", variable: named("OPENAI_BASE_URL"), address: true},
		{field: "api_key", variable: named("OPENAI_API_KEY")},
	}}},
	"OpenAIEmbeddings": {"model": {fields: []connectionField{
		{field: "openai_api_base", variable: named("OPENAI_BASE_URL"), address: true},
		{field: "openai_api_key", variable: named("OPENAI_API_KEY")},
	}}},
	"LanguageModelComponent": {"model": {
		fields: []connectionField{
			{field: "api_key", variable: named("OPENAI_API_KEY")},
		},
		provider:           openAIProvider,
		environmentAddress: "OPENAI_BASE_URL",
	}},
	"EmbeddingModel": {"model": {
		fields: []connectionField{
			{field: "api_base", variable: named("OPENAI_BASE_URL"), address: true},
			{field: "api_key", variable: named("OPENAI_API_KEY")},
		},
		provider:           openAIProvider,
		environmentAddress: "OPENAI_BASE_URL",
	}},
	"pgvector": {"vector_store": {fields: []connectionField{
		{field: "pg_server_url", variable: storeVariable("CONNECTION_STRING"), address: true},
	}}},
}

// openAIProvider is the name Langflow gives the OpenAI model provider.
const openAIProvider = "OpenAI"

// providerOf reads the model provider a Langflow node runs on, as Langflow
// reads it: none where its model field takes the model from another node
// connected to it; otherwise the value of its provider field — the provider an
// earlier Language Model or Embedding Model component selects there, or the
// override a later one replaces its selection's provider with — and, where that
// is empty, the provider of the model its model field selects. Where the node
// runs on none the import can read, it says why. A provider's name is no
// credential, and the value is read as the flow writes it.
func providerOf(template flowTemplate) (string, string) {
	selection := memberOf(template.member("model"), "value")
	var s string
	if json.Unmarshal(selection, &s) == nil && s == connectedModel {
		return "", "takes its model from another node connected to it"
	}
	if raw := template.member("provider"); raw != nil && json.Unmarshal(memberOf(raw, "value"), &s) == nil &&
		strings.TrimSpace(s) != "" {
		if string(memberOf(raw, "load_from_db")) == "true" {
			return "", "names its model provider through a global variable, which Langflow reads only when the flow runs"
		}
		return strings.TrimSpace(s), ""
	}
	var list []json.RawMessage
	if json.Unmarshal(selection, &list) == nil && len(list) > 0 {
		selection = list[0]
	}
	if raw := memberOf(selection, "provider"); raw != nil && json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s), ""
	}
	return "", "selects no model provider"
}

// providerStanding reads a node of a type whose connection is delivered for one
// model provider (node.provider) against that provider, through providerOf, the
// one comparison of the two that refusing a socket and writing the variables
// share. why is empty where the node runs on that provider, or its type on one
// alone; otherwise it completes "the node …": the provider the node names, where
// readable is true, or why the import can read none.
func providerStanding(template flowTemplate, node connectionNode) (why string, readable bool) {
	if node.provider == "" {
		return "", false
	}
	provider, unread := providerOf(template)
	switch {
	case provider == node.provider:
		return "", true
	case provider != "":
		return "runs on the " + provider + " model provider", true
	}
	return unread, false
}

// offProvider says why a part is written with none of the variables the
// platform delivers for its connection, where its type runs on several model
// providers and the import cannot read the one it runs on — it selects none,
// names it through a global variable, or takes its model from another node; empty
// where it runs on the one those variables are for, or its type runs on one. A
// node naming another provider is refused the socket (otherProvider) and is never
// wired or held out of it; were one asked about, it is off the provider all the
// same, so it is never written with the variables.
func offProvider(template flowTemplate, node connectionNode) string {
	why, _ := providerStanding(template, node)
	return why
}

// offProviderReach says how a node of a part's type off the provider its
// connection's variables are delivered for reaches the part: why it is off,
// and that it is written with none of them.
func offProviderReach(why string, part importer.Part, node connectionNode) string {
	return fmt.Sprintf("the node %s, while the platform delivers its %s connection (%s) to a node running on the %s "+
		"provider, so none of them is written into it, and the node reaches the part at the address its own fields or "+
		"its component's defaults give", why, part.Socket, strings.Join(socketVariables(part.Socket, part.Alias), ", "),
		node.provider)
}

// environmentReach says how a node no field of which gives its API's address
// reaches the address its connection delivers as variable: connection names the
// connection, "this connection" or "that connection".
func environmentReach(variable, connection string) string {
	return fmt.Sprintf("no field of it gives its API's address, so the OpenAI client it runs takes %s, the address "+
		"the platform delivers for %s, from the environment", variable, connection)
}

// blankAddress reports whether a field carrying a part's address gives none: it
// is not in the exported flow, or names no global variable and holds an empty
// value or none.
func blankAddress(raw json.RawMessage) bool {
	if raw == nil {
		return true
	}
	if string(memberOf(raw, "load_from_db")) == "true" {
		return false
	}
	value := memberOf(raw, "value")
	return value == nil || string(value) == "null" || string(value) == `""`
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

// unknownFields says that the importer knows no field of a node of a part's
// type naming a variable the platform delivers for its socket's connection.
func unknownFields(part importer.Part) string {
	return fmt.Sprintf("the importer knows no field of a Langflow %s node that names a variable the platform delivers "+
		"for its %s connection (%s), so the node reaches the part at the address its own fields or its component's "+
		"defaults give", part.Node.Type, part.Socket, strings.Join(socketVariables(part.Socket, part.Alias), ", "))
}

// wireFields is wireOf for a part reached through the fields of its node.
func wireFields(template flowTemplate, part importer.Part, at func(tokens ...string) string) importer.Wiring {
	node, known := connectionFields[part.Node.Type][part.Socket]
	if !known {
		return importer.Wiring{Literal: unknownFields(part)}
	}
	if why := offProvider(template, node); why != "" {
		return importer.Wiring{Literal: offProviderReach(why, part, node)}
	}
	var w importer.Wiring
	var reads, literal []string
	// addressed records that a field carrying the part's address is written with
	// the variable, or holds an address the node reaches as the flow writes it;
	// where neither, a type reading its address from the environment reads it
	// there.
	addressed := false
	for _, f := range node.fields {
		variable := f.variable(part.Alias)
		why := ""
		raw := template.member(f.field)
		switch {
		case raw == nil:
			why = "is not in the exported flow"
		case memberOf(raw, "load_from_db") == nil:
			why = "carries no load_from_db member in this export"
		case memberOf(raw, "value") == nil:
			why = "carries no value member in this export"
		}
		if why != "" {
			if f.address && (node.environmentAddress == "" || !blankAddress(raw)) {
				addressed = true
				literal = append(literal, fmt.Sprintf("its field %s %s, so it cannot name %s, the variable the platform "+
					"delivers for this connection, and the node reaches the part at the address the flow or its "+
					"component's defaults give", f.field, why, variable))
			}
			continue
		}
		w.Edits = append(w.Edits,
			importer.Edit{Pointer: at(f.field, "value"), Value: importer.String(variable)},
			importer.Edit{Pointer: at(f.field, "load_from_db"), Value: json.RawMessage("true")})
		reads = append(reads, fmt.Sprintf("its field %s names the variable %s", f.field, variable))
		addressed = addressed || f.address
	}
	var clauses []string
	if len(reads) > 0 {
		clauses = append(clauses, strings.Join(reads, ", and ")+", which the platform delivers for this connection and "+
			"Langflow reads as a global variable")
	}
	if node.environmentAddress != "" && !addressed {
		clauses = append(clauses, environmentReach(node.environmentAddress, "this connection"))
	}
	w.Reads = strings.Join(clauses, "; ")
	w.Literal = strings.Join(literal, "; ")
	return w
}

// singleSockets are the sockets of the Langflow platform that draw a single
// connection: the model socket, whose connection both Recipes deliver as one
// OPENAI_BASE_URL and one OPENAI_API_KEY. Where several model parts are placed
// in it, the connection goes to the first whose Component is a generative
// model, the model a flow's chat node calls.
var singleSockets = []importer.SingleSocket{
	{Name: "model", Prefer: map[string]string{"model.aggrega.ai/capability": "generative"}},
}

// standingOf says how the flow reaches the part of a node left in the flow
// because another part holds its socket's single connection, as the node is
// written: through that connection where the field of the node carrying the
// part's address names the variable the platform delivers for it, read as a
// global variable, or where no field gives the address and the node's type reads
// it from the environment; otherwise at the address that field or the node's
// component gives — as a node whose model provider the import cannot read does.
// A node naming a provider other than the one the connection's variables are
// delivered for is refused the socket (otherProvider), so it is never held out of
// it and never asked about here.
func standingOf(tree json.RawMessage, part importer.Part) (bool, string) {
	_, template, ok := nodeTemplate(tree, part.Node.ID)
	if !ok {
		return false, ""
	}
	node, known := connectionFields[part.Node.Type][part.Socket]
	if !known {
		return false, unknownFields(part)
	}
	if why := offProvider(template, node); why != "" {
		return false, offProviderReach(why, part, node)
	}
	for _, f := range node.fields {
		if !f.address {
			continue
		}
		variable := f.variable(part.Alias)
		raw := template.member(f.field)
		var value string
		if string(memberOf(raw, "load_from_db")) == "true" && json.Unmarshal(memberOf(raw, "value"), &value) == nil &&
			value == variable {
			return true, fmt.Sprintf("its field %s names the variable %s, which the platform delivers for that "+
				"connection and Langflow reads as a global variable", f.field, variable)
		}
		if node.environmentAddress != "" && blankAddress(raw) {
			break
		}
		return false, fmt.Sprintf("its field %s does not name %s, the variable the platform delivers for that "+
			"connection, so the node reaches the part at the address that field or its component's defaults give",
			f.field, variable)
	}
	if node.environmentAddress != "" {
		return true, environmentReach(node.environmentAddress, "that connection")
	}
	return false, ""
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
func serverOf(template flowTemplate) (mcpServer, bool) {
	field := template.member("mcp_server")
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
// id is id, and the template its data.node carries — of the first such node,
// where several carry that id; false where no node has that id.
func nodeTemplate(tree json.RawMessage, id string) (int, flowTemplate, bool) {
	nodes := nodesOf(tree)
	at, ok := nodes.first[id]
	if !ok {
		return 0, flowTemplate{}, false
	}
	n := nodes.list[at]
	return n.position, n.template, true
}

// flowNode is one node of a Langflow flow's node list: its position in the
// list, its id, its type — the string at its data.type, empty where that holds
// none — and the template its data.node carries.
type flowNode struct {
	position int
	id       string
	typ      string
	template flowTemplate
}

// flowTemplate is the template a Langflow node's data.node carries: the
// template as the flow writes it, its members in the order the template writes
// them, as importer.ObjectEntries reads them, and, by name, the value of the
// first member of each name.
type flowTemplate struct {
	raw     json.RawMessage
	entries []importer.ObjectEntry
	first   map[string]json.RawMessage
}

// readTemplate reads a node's template once, for every question asked of it.
func readTemplate(raw json.RawMessage) flowTemplate {
	t := flowTemplate{raw: raw, entries: importer.ObjectEntries(raw), first: map[string]json.RawMessage{}}
	for _, e := range t.entries {
		if _, seen := t.first[e.Key]; !seen {
			t.first[e.Key] = e.Value
		}
	}
	return t
}

// member returns the value of the template's first member named key, nil where
// it holds none, as memberOf reads an object.
func (t flowTemplate) member(key string) json.RawMessage {
	return t.first[key]
}

// flowNodes are the elements of a Langflow flow's node list that read as nodes,
// in the order the list holds them, and, by id, the first of them carrying each
// id; and the flow's own top-level id, name and last_tested_version, undecoded.
type flowNodes struct {
	list    []flowNode
	first   map[string]int
	id      json.RawMessage
	name    json.RawMessage
	version json.RawMessage
}

// nodesMemo holds the nodes last read off a flow's tree.
var nodesMemo treememo.Memo[flowNodes]

// nodesOf returns the nodes of the flow whose tree is tree, reading its node
// list once per tree however many questions are asked about its nodes.
func nodesOf(tree json.RawMessage) flowNodes {
	return nodesMemo.Of(tree, readNodes)
}

// readNodes reads the node list of a Langflow flow's tree, at data.nodes: each
// element that decodes as a node, with its type and the template its data.node
// carries, its members read once. A type that is not a string reads as none
// and leaves the node read. A tree whose node list does not decode holds none.
// It reads the flow's top-level id, name and last_tested_version in the same
// decoding, as decoding each alone would: a member whose value does not fit the
// node list leaves them read.
func readNodes(tree json.RawMessage) flowNodes {
	nodes := flowNodes{first: map[string]int{}}
	var top struct {
		ID      json.RawMessage `json:"id"`
		Name    json.RawMessage `json:"name"`
		Version json.RawMessage `json:"last_tested_version"`
		Data    struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"data"`
	}
	err := json.Unmarshal(tree, &top)
	var mismatch *json.UnmarshalTypeError
	if err == nil || errors.As(err, &mismatch) {
		nodes.id, nodes.name, nodes.version = top.ID, top.Name, top.Version
	}
	if err != nil {
		return nodes
	}
	for i, raw := range top.Data.Nodes {
		var node struct {
			ID   string `json:"id"`
			Data struct {
				Type json.RawMessage `json:"type"`
				Node struct {
					Template json.RawMessage `json:"template"`
				} `json:"node"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &node) != nil {
			continue
		}
		if _, seen := nodes.first[node.ID]; !seen {
			nodes.first[node.ID] = len(nodes.list)
		}
		var typ string
		_ = json.Unmarshal(node.Data.Type, &typ)
		nodes.list = append(nodes.list,
			flowNode{position: i, id: node.ID, typ: typ, template: readTemplate(node.Data.Node.Template)})
	}
	return nodes
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
