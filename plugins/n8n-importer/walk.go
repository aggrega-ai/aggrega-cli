// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/treememo"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// walkTree lifts the nodes, and the connections between them, out of the
// canonical tree of an exported n8n workflow. It reads the already-parsed tree
// the host returned — it runs no grammar of its own. An n8n workflow keeps its
// nodes as an array at the top level and its connections as an object keyed by
// the name of the node each connection leaves. An unrecognised shape yields no
// nodes, which the importer surfaces as a diagnostic rather than a silent empty
// import.
//
// A node is identified by its name, the identifier n8n's connections use and one
// it keeps unique within a workflow. Each connection the workflow lists is one
// edge, from the node it leaves to the node it enters, whatever its type: the
// main connections carry data from one node to the next, and the AI connections
// hand the node they enter the model, tool, store or memory the node they leave
// stands for, so in either case the node a connection enters consumes the node
// it leaves.
func walkTree(tree json.RawMessage) ([]importer.Node, []importer.Edge) {
	w := workflowOf(tree)
	nodes := make([]importer.Node, 0, len(w.nodes))
	for _, n := range w.nodes {
		if n.typ == stickyNote {
			continue
		}
		nodes = append(nodes, liftNode(n))
	}
	return nodes, w.edges
}

// stickyNote is the type of a note an author pins to a workflow's canvas. A note
// documents the workflow and runs nothing, so it is not lifted as a node: lifting
// one would draft a component for every note. A connection naming a note is then
// dropped, as is any connection naming a node that was not lifted.
const stickyNote = basePackage + "stickyNote"

// liftNode lifts one node of a workflow: its name as its identifier, its type,
// the credentials it selects and the parameters n8n marks as passwords as the
// fields it marks as credentials, and its other top-level parameters as its value
// fields.
func liftNode(n workflowNode) importer.Node {
	secrets := n.credentialTypes()
	password := passwordParams(n.typ)
	var fields []string
	for _, e := range importer.ObjectEntries(n.parameters) {
		if password[e.Key] {
			secrets = append(secrets, e.Key)
			continue
		}
		fields = append(fields, e.Key)
	}
	return importer.Node{ID: n.name, Type: n.typ, Fields: fields, Secrets: secrets}
}

// workflowID returns the identifier an n8n export gives its workflow: the string
// at its top-level id, empty where it gives none. The kit derives one from the
// carried workflow where this is empty or holds a character a draft does not
// write as it is.
func workflowID(tree json.RawMessage) string {
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

// workflowNode is one element of an n8n workflow's node list that reads as a
// node: its position in the list, its name, its type, its parameters and the
// credentials it selects, undecoded.
type workflowNode struct {
	position    int
	name        string
	typ         string
	parameters  json.RawMessage
	credentials json.RawMessage
}

// credentialTypes returns the credential types the node selects, in the order
// its credentials object lists them.
func (n workflowNode) credentialTypes() []string {
	var out []string
	for _, e := range importer.ObjectEntries(n.credentials) {
		out = append(out, e.Key)
	}
	return out
}

// workflow is what an n8n workflow's tree holds that this importer reads more
// than once: its nodes, in the order the list holds them, the first node of each
// name by name, and the connections between nodes as edges.
type workflow struct {
	nodes  []workflowNode
	byName map[string]int
	edges  []importer.Edge
}

// node returns the first node of the workflow named name, and false where none
// is.
func (w workflow) node(name string) (workflowNode, bool) {
	at, ok := w.byName[name]
	if !ok {
		return workflowNode{}, false
	}
	return w.nodes[at], true
}

// workflowMemo holds the workflow last read off a tree.
var workflowMemo treememo.Memo[workflow]

// workflowOf returns the workflow whose tree is tree, reading it once per tree
// however many questions are asked about its nodes.
func workflowOf(tree json.RawMessage) workflow {
	return workflowMemo.Of(tree, readWorkflow)
}

// readWorkflow reads an n8n workflow's tree: each element of its node list that
// decodes as a node, by name the first of each, and each connection its
// connections object lists, in the order the object writes them — by the node
// the connection leaves, then by its type, then by the output it leaves from —
// as an edge from the node it leaves to the node it enters. A node whose name is
// empty is identified by its id. A tree whose node list does not decode holds no
// node; an output n8n writes as null holds no connection.
func readWorkflow(tree json.RawMessage) workflow {
	w := workflow{byName: map[string]int{}}
	var top struct {
		Nodes       []json.RawMessage `json:"nodes"`
		Connections json.RawMessage   `json:"connections"`
	}
	if err := json.Unmarshal(tree, &top); err != nil {
		return w
	}
	for i, raw := range top.Nodes {
		var node struct {
			ID          string          `json:"id"`
			Name        string          `json:"name"`
			Type        string          `json:"type"`
			Parameters  json.RawMessage `json:"parameters"`
			Credentials json.RawMessage `json:"credentials"`
		}
		if json.Unmarshal(raw, &node) != nil {
			continue
		}
		name := node.Name
		if strings.TrimSpace(name) == "" {
			name = node.ID
		}
		if _, seen := w.byName[name]; !seen {
			w.byName[name] = len(w.nodes)
		}
		w.nodes = append(w.nodes, workflowNode{position: i, name: name, typ: node.Type, parameters: node.Parameters,
			credentials: node.Credentials})
	}
	for _, from := range importer.ObjectEntries(top.Connections) {
		for _, kind := range importer.ObjectEntries(from.Value) {
			var outputs []json.RawMessage
			if json.Unmarshal(kind.Value, &outputs) != nil {
				continue
			}
			for _, output := range outputs {
				var targets []struct {
					Node string `json:"node"`
				}
				if json.Unmarshal(output, &targets) != nil {
					continue
				}
				for _, to := range targets {
					if from.Key != "" && to.Node != "" {
						w.edges = append(w.edges, importer.Edge{Source: from.Key, Target: to.Node})
					}
				}
			}
		}
	}
	return w
}

// baseName returns the node's own name within its package — the part of its
// type after the package — with the suffix n8n gives a node used as an agent's
// tool removed, since such a node takes its base node's parameters; and the
// package. A type in neither package is returned whole with no package.
func baseName(typ string) (pkg, name string) {
	for _, p := range []string{basePackage, aiPackage} {
		if rest, ok := strings.CutPrefix(typ, p); ok {
			if p == basePackage {
				for _, suffix := range []string{"HitlTool", "Tool"} {
					if trimmed, cut := strings.CutSuffix(rest, suffix); cut && trimmed != "" {
						rest = trimmed
						break
					}
				}
			}
			return p, rest
		}
	}
	return "", typ
}
