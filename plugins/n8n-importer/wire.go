// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// This file holds how the workflow core/ai-agent/n8n carries reaches each part of
// it the draft wires to a socket of the platform. The platform's Recipes deliver
// every connection as environment variables — a model connection as
// OPENAI_BASE_URL and OPENAI_API_KEY, a tools or vector-store connection as
// variables named for its alias — while an n8n node reaches a model, a store or
// a service through the stored credential it selects, or the address its own
// parameters hold, and a workflow reads a variable of the platform's environment
// only through an expression reading $env,
// which the platform refuses unless the Aggregate sets
// values.allow_env_access_in_nodes to true. The draft leaves that value as the
// Component's default, false, so the importer writes nothing into the workflow
// for a connection, and reports each wired part as reached other than through it.

// envAccess says, in each report, why the workflow does not read what the
// platform delivers for a connection.
const envAccess = "an n8n workflow reads a variable of the platform's environment only through an expression " +
	"reading $env, which the platform refuses while values.allow_env_access_in_nodes is false, the value the draft " +
	"leaves it at; setting it true lets every workflow, and every user who can edit one, read every variable of the " +
	"platform's environment, the delivered credentials included"

// wireOf says how the workflow reaches a part wired to a socket of the platform:
// at the address the node's stored credential or its own parameters give, never
// through the connection, whose variables the node does not read. An MCP client
// node reaches its server at the address its parameters hold in the workflow the
// draft carries — the address the export held, or none where the
// empty_credentials setting writes it empty with the other members of an MCP
// server's configuration — and the Release supplies the server's own.
func wireOf(_ json.RawMessage, part importer.Part) importer.Wiring {
	variables := strings.Join(socketVariables(part.Socket, part.Alias), " and ")
	if mcpClients[part.Node.Type] {
		return importer.Wiring{
			Literal: fmt.Sprintf("the node reaches its server at the address its parameters hold in the workflow the "+
				"draft carries — the one the export held, or none where the empty_credentials setting writes it empty, "+
				"until an address is set again; the platform delivers this connection's address as %s, but %s",
				variables, envAccess),
			LeftForRelease: []string{fmt.Sprintf("infra.endpoint of %s, the address of the MCP server the node calls",
				part.Key)},
		}
	}
	if variables == "" {
		return importer.Wiring{Literal: fmt.Sprintf("the platform delivers nothing the workflow reads for its %s "+
			"connection, so the node reaches the part at the address its stored credential or its own parameters give",
			part.Socket)}
	}
	return importer.Wiring{Literal: fmt.Sprintf("an n8n %s node reaches the part at the address the stored credential "+
		"it selects, or its own parameters, give; the platform delivers this connection as %s, but %s",
		part.Node.Type, variables, envAccess)}
}

// socketVariables names the variables the platform delivers for a connection of
// socket written under alias, and none for a socket it delivers nothing for.
func socketVariables(socket, alias string) []string {
	switch socket {
	case "model":
		return []string{"OPENAI_BASE_URL", "OPENAI_API_KEY"}
	case "tools":
		return []string{"AGGREGA_TOOL_" + importer.VariableName(alias) + "_URL"}
	case "vector_store":
		return []string{"AGGREGA_VECTOR_STORE_" + importer.VariableName(alias) + "_URL"}
	}
	return nil
}

// singleSockets are the sockets of the n8n platform that draw a single
// connection: the model socket, whose connection both Recipes deliver as one
// OPENAI_BASE_URL and one OPENAI_API_KEY. Where several model parts are placed
// in it, the connection goes to the first whose Component is a generative
// model, the model a workflow's agent calls.
var singleSockets = []importer.SingleSocket{
	{Name: "model", Prefer: map[string]string{"model.aggrega.ai/capability": "generative"}},
}

// standingOf says how the workflow reaches the part of a node left in it because
// another part holds the model socket's single connection: as every n8n node
// does, at the address its stored credential or its own parameters give, never
// through the connection.
func standingOf(_ json.RawMessage, part importer.Part) (bool, string) {
	if part.Socket != "model" {
		return false, ""
	}
	return false, fmt.Sprintf("an n8n %s node reaches a model at the address the stored credential it selects, or its "+
		"own parameters, give, and %s", part.Node.Type, envAccess)
}
