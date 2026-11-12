// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// modelNodes are the Flowise node types whose modelName input names the model
// they call, read off Flowise's own nodes: its OpenAI and Anthropic chat models
// and its OpenAI embeddings.
var modelNodes = map[string]bool{"chatOpenAI": true, "chatAnthropic": true, "openAIEmbeddings": true}

// modelOf reads, off a Flowise node's inputs, the name of the model the node
// selects: its modelName input. A model's name is no credential: the input is
// none Flowise marks as one, and its value is read as the chatflow writes it.
// Where the node names none a draft can write, it says why: it selects none, or
// it names its model through an expression Flowise reads only when the chatflow
// runs. A type modelNodes does not list gets neither.
func modelOf(tree json.RawMessage, node importer.Node) (string, string) {
	if !modelNodes[node.Type] {
		return "", ""
	}
	_, inputs, ok := nodeInputs(tree, node.ID)
	if !ok {
		return "", ""
	}
	var name string
	for _, e := range importer.ObjectEntries(inputs) {
		if e.Key == "modelName" {
			_ = json.Unmarshal(e.Value, &name)
		}
	}
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", "selects no model"
	case strings.Contains(name, "{{"):
		return "", "names its model through an expression, which Flowise reads only when the chatflow runs"
	}
	return name, ""
}
