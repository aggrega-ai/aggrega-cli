// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpconfig"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/treememo"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
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
// Path is empty and it selects no credential, so a Base Path the export holds
// and the credential it selects are written empty, and so are its default
// headers, which its client would send over the header it builds from the key
// the platform delivers — whatever the empty_credentials setting says, since
// this is how the node reaches its connection. Any other part is reported as
// reached at the address the chatflow holds, with what makes the chatflow read
// its connection instead.
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
		n, ok := nodesOf(tree)[part.Node.ID]
		if !ok {
			return w
		}
		at := func(tokens ...string) []string {
			return append([]string{"nodes", strconv.Itoa(n.position), "data"}, tokens...)
		}
		base := "its Base Path is empty"
		if basepath := memberOf(n.inputs, "basepath"); basepath != nil && !mcpconfig.Blank(basepath) {
			w.Edits = append(w.Edits, importer.Edit{Pointer: importer.Pointer(at("inputs", "basepath")...),
				Value: json.RawMessage(`""`)})
			base = "the Base Path the exported chatflow held is written empty"
		}
		credential := "it selects no credential"
		if n.credential != nil && !mcpconfig.Blank(n.credential) {
			w.Edits = append(w.Edits, importer.Edit{Pointer: importer.Pointer(at("credential")...),
				Value: json.RawMessage(`""`)})
			credential = "the credential it selected is written empty"
		}
		w.Reads = base + ", so the OpenAI client Flowise runs it on reads the address the platform delivers for this " +
			"connection, OPENAI_BASE_URL, and, since " + credential + ", the key delivered as OPENAI_API_KEY"
		if e, ok := wholeKeptEdit(memberOf(n.inputs, baseOptionsInput), at("inputs", baseOptionsInput)); ok {
			w.Edits = append(w.Edits, e)
			w.Reads += "; " + defaultHeadersWritten
		}
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

// defaultHeadersWritten says, in a report of how a node reaches the model
// connection, that its default headers are written empty and why.
const defaultHeadersWritten = "its default headers (baseOptions) are written empty, since its client sends them " +
	"over the header it builds from the key the platform delivers"

// standingOf says how the chatflow reaches the part of a node left in the flow
// because another part holds the model socket's single connection, as the node
// is written: an OpenAI node whose Base Path is empty and which selects no
// credential reads the variables the platform delivers for that connection,
// through its client's environment fallback; any other reaches the address its
// Base Path, or its own defaults, give, or presents the credential it selects.
func standingOf(tree json.RawMessage, part importer.Part) (bool, string) {
	if part.Socket != "model" {
		return false, ""
	}
	if !openAINodes[part.Node.Type] {
		return false, fmt.Sprintf("a %s node does not read OPENAI_BASE_URL, so it reaches a model at the address the "+
			"chatflow or its own defaults give", part.Node.Type)
	}
	n, ok := nodesOf(tree)[part.Node.ID]
	if !ok {
		return false, ""
	}
	if basepath := memberOf(n.inputs, "basepath"); basepath != nil && !mcpconfig.Blank(basepath) {
		return false, "its Base Path holds an address of its own, which the chatflow carries as the export wrote it, " +
			"so the OpenAI client Flowise runs it on reaches that address"
	}
	if n.credential != nil && !mcpconfig.Blank(n.credential) {
		return false, "it selects a credential, so the OpenAI client Flowise runs it on presents that credential's key " +
			"rather than the one the platform delivers for that connection"
	}
	return true, "its Base Path is empty, so the OpenAI client Flowise runs it on reads OPENAI_BASE_URL, the address " +
		"the platform delivers for that connection, and, since it selects no credential, the key delivered as " +
		"OPENAI_API_KEY"
}

// reachedOf says how the model connection the draft wires reaches a node it
// wires to no socket, as the node is written: an OpenAI node whose Base Path is
// empty and which selects no credential, and an OpenAI model's configuration an
// agentflow node keeps among its inputs that gives no Base Path and selects no
// credential, run on an OpenAI client reading the address and the key the
// platform delivers from the environment, so their default headers, which that
// client would send over the header it builds from the delivered key, are
// written empty, as on a wired node. A node selecting a credential, giving a
// Base Path of its own, or keeping no default headers to write, is reached by
// none, and nothing is written for it.
func reachedOf(tree json.RawMessage, node importer.Node, wired []importer.Part) importer.Wiring {
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
	n, ok := nodesOf(tree)[node.ID]
	if !ok || (openAINodes[node.Type] && n.credential != nil && !mcpconfig.Blank(n.credential)) {
		return importer.Wiring{}
	}
	edits, holders := configHeaderEdits(n.inputs, node.Type, nil,
		[]string{"nodes", strconv.Itoa(n.position), "data", "inputs"})
	if len(edits) == 0 {
		return importer.Wiring{}
	}
	var subjects []string
	for _, h := range holders {
		if h == "" {
			subjects = append(subjects, "the node")
		} else {
			subjects = append(subjects, "its model configuration "+h)
		}
	}
	subject := strings.Join(subjects, " and ")
	return importer.Wiring{Edits: edits, Reads: fmt.Sprintf("the model connection %s reaches it, since %s gives no Base "+
		"Path and selects no credential, so the OpenAI client Flowise runs it on reads OPENAI_BASE_URL and "+
		"OPENAI_API_KEY, which the platform delivers for that connection; the default headers (baseOptions) of %s "+
		"are written empty, since that client sends them over the header it builds from the delivered key",
		model.Alias, subject, subject)}
}

// configHeaderEdits returns the edits writing empty, whole with its kind kept,
// the default headers of every OpenAI model configuration in raw — an object
// whose type, its own selection or the one of an object holding it
// (selectedType), is an OpenAI node's, which holds default headers, gives no
// Base Path and selects no credential — at any depth of its objects and lists,
// and names the configuration holding each by the members on the way to it,
// empty for raw itself: field is the members on the way to raw, and tokens
// address raw from the root of the export.
func configHeaderEdits(raw json.RawMessage, typ string, field, tokens []string) ([]importer.Edit, []string) {
	var edits []importer.Edit
	var holders []string
	if entries := importer.ObjectEntries(raw); entries != nil {
		typ = selectedType(entries, typ)
		reached := openAINodes[typ]
		for _, e := range entries {
			switch e.Key {
			case "basepath", "credential", credentialIDInput:
				reached = reached && mcpconfig.Blank(e.Value)
			}
		}
		for _, e := range entries {
			f := append(append([]string{}, field...), e.Key)
			t := append(append([]string{}, tokens...), e.Key)
			if e.Key == baseOptionsInput && reached {
				if edit, ok := wholeKeptEdit(e.Value, t); ok {
					edits = append(edits, edit)
					holders = append(holders, strings.Join(field, "."))
				}
				continue
			}
			more, named := configHeaderEdits(e.Value, typ, f, t)
			edits, holders = append(edits, more...), append(holders, named...)
		}
		return edits, holders
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, nil
	}
	for j, item := range items {
		index := strconv.Itoa(j)
		more, named := configHeaderEdits(item, typ, append(append([]string{}, field...), index),
			append(append([]string{}, tokens...), index))
		edits, holders = append(edits, more...), append(holders, named...)
	}
	return edits, holders
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

// nodeInputs returns the position, in the chatflow's node list, of the node
// whose id is id, and the inputs its data holds — of the first such node, where
// several carry that id; false where no node has that id.
func nodeInputs(tree json.RawMessage, id string) (int, json.RawMessage, bool) {
	n, ok := nodesOf(tree)[id]
	if !ok {
		return 0, nil, false
	}
	return n.position, n.inputs, true
}

// chatflowNode is one node of a Flowise flow's node list: its position in the
// list, the inputs its data holds, and the credential it selects at
// data.credential, nil where it holds none.
type chatflowNode struct {
	position   int
	inputs     json.RawMessage
	credential json.RawMessage
}

// nodesMemo holds the nodes last read off a chatflow's tree.
var nodesMemo treememo.Memo[map[string]chatflowNode]

// nodesOf returns, by id, the first node carrying each id among the elements
// of the node list of the chatflow whose tree is tree that read as nodes,
// reading its node list once per tree however many questions are asked about
// its nodes.
func nodesOf(tree json.RawMessage) map[string]chatflowNode {
	return nodesMemo.Of(tree, readNodes)
}

// readNodes reads the node list of a Flowise flow's tree, at nodes: each
// element that decodes as a node, with the inputs its data holds, by id, the
// first of each id. A tree whose node list does not decode holds none.
func readNodes(tree json.RawMessage) map[string]chatflowNode {
	nodes := map[string]chatflowNode{}
	var top struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return nodes
	}
	for i, raw := range top.Nodes {
		var node struct {
			ID   string `json:"id"`
			Data struct {
				Inputs     json.RawMessage `json:"inputs"`
				Credential json.RawMessage `json:"credential"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &node) != nil {
			continue
		}
		if _, seen := nodes[node.ID]; !seen {
			nodes[node.ID] = chatflowNode{position: i, inputs: node.Data.Inputs, credential: node.Data.Credential}
		}
	}
	return nodes
}
