// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strconv"

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
// its top-level id, empty where it carries none.
func flowID(tree json.RawMessage) string {
	var top struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return ""
	}
	var id string
	if err := json.Unmarshal(top.ID, &id); err != nil {
		return ""
	}
	return id
}

// credentialsOf locates every credential value a Langflow flow holds, by the
// marks Langflow itself writes: the value of each template field whose object
// carries password set to true, and each header and environment value of the
// server configuration an MCP Tools node carries in its mcp_server field, where
// a request's credentials travel. Each is addressed from the root of the export,
// and named by its node's id and the field it sits in. A field holding no value
// member is not located, since there is nothing in it to write empty.
func credentialsOf(tree json.RawMessage) []importer.Credential {
	var top struct {
		Data struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return nil
	}
	var out []importer.Credential
	for i, raw := range top.Data.Nodes {
		var node struct {
			ID   string `json:"id"`
			Data struct {
				Node struct {
					Template json.RawMessage `json:"template"`
				} `json:"node"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			continue
		}
		at := func(tokens ...string) string {
			return importer.Pointer(append([]string{"data", "nodes", strconv.Itoa(i), "data", "node", "template"}, tokens...)...)
		}
		for _, e := range importer.ObjectEntries(node.Data.Node.Template) {
			var field struct {
				Password json.RawMessage `json:"password"`
				Value    json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal(e.Value, &field); err != nil || field.Value == nil {
				continue
			}
			if string(field.Password) == "true" {
				out = append(out, importer.Credential{Node: node.ID, Field: e.Key, Pointer: at(e.Key, "value")})
			}
			if e.Key == "mcp_server" {
				out = append(out, serverCredentials(node.ID, field.Value, at)...)
			}
		}
	}
	return out
}

// serverCredentials locates the header and environment values of the server
// configuration an MCP Tools node's mcp_server value carries: an object with a
// name and a config, whose headers are an object of names to values or a list
// of key and value items, and whose env is an object of names to values. A value
// naming a server alone carries no configuration and holds none.
func serverCredentials(nodeID string, value json.RawMessage, at func(tokens ...string) string) []importer.Credential {
	var server struct {
		Config struct {
			Headers json.RawMessage `json:"headers"`
			Env     json.RawMessage `json:"env"`
		} `json:"config"`
	}
	if err := json.Unmarshal(value, &server); err != nil {
		return nil
	}
	var out []importer.Credential
	for _, e := range importer.ObjectEntries(server.Config.Headers) {
		out = append(out, importer.Credential{Node: nodeID, Field: "mcp_server.config.headers." + e.Key,
			Pointer: at("mcp_server", "value", "config", "headers", e.Key)})
	}
	var items []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(server.Config.Headers, &items); err == nil {
		for j, item := range items {
			if item.Value == nil {
				continue
			}
			out = append(out, importer.Credential{Node: nodeID, Field: "mcp_server.config.headers." + item.Key,
				Pointer: at("mcp_server", "value", "config", "headers", strconv.Itoa(j), "value")})
		}
	}
	for _, e := range importer.ObjectEntries(server.Config.Env) {
		out = append(out, importer.Credential{Node: nodeID, Field: "mcp_server.config.env." + e.Key,
			Pointer: at("mcp_server", "value", "config", "env", e.Key)})
	}
	return out
}
