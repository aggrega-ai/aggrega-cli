// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// This file holds how the chatflow core/ai-agent/flowise carries reaches each
// part of it the draft wires to a socket of the platform, by the forms the
// platform's Recipes deliver a connection in:
//
//   - a model connection is delivered as OPENAI_BASE_URL and OPENAI_API_KEY,
//     which the OpenAI client Flowise's OpenAI nodes run on reads from the
//     environment where the node's Base Path is empty and it selects no
//     credential;
//   - a tools or vector-store connection is delivered as variables named for
//     its alias, which a chatflow reads only through a Runtime variable of the
//     same name created on Flowise's Variables page, as $vars.<name> — and
//     nothing creates one at startup — so the importer writes nothing for it and
//     reports the address the chatflow holds.

// openAINodes are the Flowise node types whose OpenAI client reads the address
// and the key from the environment where the node's Base Path is empty and it
// selects no credential: the chat model and the embeddings.
var openAINodes = map[string]bool{"chatOpenAI": true, "openAIEmbeddings": true}

// wireOf says how the chatflow reaches a part wired to a socket of the platform.
// An OpenAI node wired to the model socket reaches its connection once its Base
// Path is empty, so a Base Path the export holds is written empty; its selected
// credential is written empty with every other credential. Any other part is
// reported as reached at the address the chatflow holds, with what makes the
// chatflow read its connection instead.
func wireOf(tree json.RawMessage, part importer.Part) importer.Wiring {
	switch part.Socket {
	case "model":
		if !openAINodes[part.Node.Type] {
			return importer.Wiring{Literal: fmt.Sprintf("the platform delivers its model connection as OPENAI_BASE_URL "+
				"and OPENAI_API_KEY, which Flowise's OpenAI client reads for an OpenAI node whose Base Path is empty and "+
				"which selects no credential; a %s node is not one, so it reaches the part at the address the chatflow "+
				"or its own defaults give", part.Node.Type)}
		}
		var w importer.Wiring
		reads := "its Base Path is empty, so the OpenAI client Flowise runs it on reads the address the platform " +
			"delivers for this connection, OPENAI_BASE_URL, and, since it selects no credential, the key delivered as " +
			"OPENAI_API_KEY"
		if index, basepath, ok := basePathOf(tree, part.Node.ID); ok && string(basepath) != `""` && string(basepath) != "null" {
			w.Edits = append(w.Edits, importer.Edit{
				Pointer: importer.Pointer("nodes", strconv.Itoa(index), "data", "inputs", "basepath"),
				Value:   json.RawMessage(`""`),
			})
			reads = "the Base Path the exported chatflow held is written empty, so the OpenAI client Flowise runs it on " +
				"reads the address the platform delivers for this connection, OPENAI_BASE_URL, and, since it selects no " +
				"credential, the key delivered as OPENAI_API_KEY"
		}
		w.Reads = reads
		return w
	case "tools", "vector_store":
		prefix := "AGGREGA_TOOL_"
		if part.Socket == "vector_store" {
			prefix = "AGGREGA_VECTOR_STORE_"
		}
		variable := prefix + importer.VariableName(part.Alias) + "_URL"
		return importer.Wiring{Literal: fmt.Sprintf("Flowise reads %s, the address the platform delivers for this "+
			"connection, only through a Runtime variable of that name, which nothing creates at startup: create the "+
			"Runtime variable %s on Flowise's Variables page and read it in the node as $vars.%s; until then the node "+
			"reaches the part at the address the chatflow holds", variable, variable, variable)}
	}
	return importer.Wiring{Literal: fmt.Sprintf("the platform delivers nothing the chatflow reads for its %s "+
		"connection, so the node reaches the part at the address the chatflow holds", part.Socket)}
}

// singleSockets are the sockets of the Flowise platform that draw a single
// connection: the model socket, whose connection both Recipes deliver as one
// OPENAI_BASE_URL and one OPENAI_API_KEY. Where several model parts are placed
// in it, the connection goes to the first whose Component is a generative
// model, the model a chatflow's chat node calls.
var singleSockets = []importer.SingleSocket{
	{Name: "model", Prefer: map[string]string{"model.aggrega.ai/capability": "generative"}},
}

// standingOf says how the chatflow reaches the part of a node left in the flow
// because another part holds the model socket's single connection, as the node
// is written: an OpenAI node whose Base Path is empty reads the variables the
// platform delivers for that connection, through its client's environment
// fallback; any other reaches the address its Base Path, or its own defaults,
// give.
func standingOf(tree json.RawMessage, part importer.Part) (bool, string) {
	if part.Socket != "model" {
		return false, ""
	}
	if !openAINodes[part.Node.Type] {
		return false, fmt.Sprintf("a %s node does not read OPENAI_BASE_URL, so it reaches a model at the address the "+
			"chatflow or its own defaults give", part.Node.Type)
	}
	if _, basepath, ok := basePathOf(tree, part.Node.ID); ok && string(basepath) != `""` && string(basepath) != "null" {
		return false, "its Base Path holds an address of its own, which the chatflow carries as the export wrote it, " +
			"so the OpenAI client Flowise runs it on reaches that address"
	}
	return true, "its Base Path is empty, so the OpenAI client Flowise runs it on reads OPENAI_BASE_URL, the address " +
		"the platform delivers for that connection, and, since it selects no credential, the key delivered as " +
		"OPENAI_API_KEY"
}

// basePathOf returns the position, in the chatflow's node list, of the node
// whose id is id, and the value of its basepath input; false where no node has
// that id or it holds no basepath input.
func basePathOf(tree json.RawMessage, id string) (int, json.RawMessage, bool) {
	var top struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return 0, nil, false
	}
	for i, raw := range top.Nodes {
		var node struct {
			ID   string `json:"id"`
			Data struct {
				Inputs json.RawMessage `json:"inputs"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &node) != nil || node.ID != id {
			continue
		}
		for _, e := range importer.ObjectEntries(node.Data.Inputs) {
			if e.Key == "basepath" {
				return i, e.Value, true
			}
		}
		return 0, nil, false
	}
	return 0, nil, false
}
