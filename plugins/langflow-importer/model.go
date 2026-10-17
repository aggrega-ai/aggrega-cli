// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// modelFields are, by node type, the fields of a Langflow node naming the model
// it calls, in the order Langflow reads them, read off Langflow's own
// components: its OpenAI and Anthropic chat models name theirs in model_name,
// its OpenAI embeddings in model, and its Language Model and Embedding Model
// components select one in model, which model_name, its override, replaces
// wherever it is set. An earlier export of the Language Model component names
// its model in model_name alone, which the same order reads.
var modelFields = map[string][]string{
	"OpenAIModel":            {"model_name"},
	"AnthropicModel":         {"model_name"},
	"OpenAIEmbeddings":       {"model"},
	"LanguageModelComponent": {"model_name", "model"},
	"EmbeddingModel":         {"model_name", "model"},
}

// connectedModel is the value a Language Model or Embedding Model component
// writes in its model field when it takes its model from another node connected
// to it rather than selecting one.
const connectedModel = "connect_other_models"

// modelOf reads, off a Langflow node's template, the name of the model the node
// selects. A model's name is no credential: no field read here is one the
// walker marks as a credential, and the value is read as the flow writes it.
// Where the node names none a draft can write, it says why: it selects none, it
// names its model through a global variable — a field whose object carries
// load_from_db true holds the variable's name, not the model's — or it takes its
// model from another node. A type modelFields does not list gets neither.
func modelOf(tree json.RawMessage, node importer.Node) (string, string) {
	fields, known := modelFields[node.Type]
	if !known {
		return "", ""
	}
	_, template, ok := nodeTemplate(tree, node.ID)
	if !ok {
		return "", ""
	}
	for _, f := range fields {
		raw := template.member(f)
		if raw == nil {
			continue
		}
		name, connected := selectedModel(memberOf(raw, "value"))
		switch {
		case connected:
			return "", "takes its model from another node connected to it"
		case name == "":
			continue
		case string(memberOf(raw, "load_from_db")) == "true":
			return "", "names its model through a global variable, which Langflow reads only when the flow runs"
		}
		return name, ""
	}
	return "", "selects no model"
}

// selectedModel reads the value of a field naming a model as Langflow reads it:
// a model's name; a list whose first entry is a name, or an object holding the
// name under name; or such an object alone. Surrounding spaces are not part of
// the name. connected reports the value naming a model taken from another node.
func selectedModel(value json.RawMessage) (name string, connected bool) {
	var s string
	if json.Unmarshal(value, &s) == nil {
		if s == connectedModel {
			return "", true
		}
		return strings.TrimSpace(s), false
	}
	var list []json.RawMessage
	if json.Unmarshal(value, &list) == nil {
		if len(list) == 0 {
			return "", false
		}
		if json.Unmarshal(list[0], &s) == nil {
			return strings.TrimSpace(s), false
		}
		value = list[0]
	}
	if raw := memberOf(value, "name"); raw != nil && json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s), false
	}
	return "", false
}
