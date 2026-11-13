// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// modelParams are, by node type, the parameter an n8n model node names the
// model it calls in, read off n8n's own nodes: its chat models, its completion
// models and its embeddings.
var modelParams = map[string]string{
	aiPackage + "lmChatOpenAi":           "model",
	aiPackage + "lmOpenAi":               "model",
	aiPackage + "lmChatAnthropic":        "model",
	aiPackage + "lmChatMistralCloud":     "model",
	aiPackage + "lmChatGoogleGemini":     "modelName",
	aiPackage + "lmChatCohere":           "model",
	aiPackage + "lmChatAzureOpenAi":      "model",
	aiPackage + "lmChatAwsBedrock":       "model",
	aiPackage + "lmChatGoogleVertex":     "modelName",
	aiPackage + "lmChatOllama":           "model",
	aiPackage + "embeddingsOpenAi":       "model",
	aiPackage + "embeddingsAzureOpenAi":  "model",
	aiPackage + "embeddingsAwsBedrock":   "model",
	aiPackage + "embeddingsGoogleVertex": "modelName",
}

// modelOf reads, off an n8n node's parameters, the name of the model the node
// selects: the parameter modelParams names for its type, written as a text or as
// the value of a resource locator — an object holding the selected value in its
// value member, as n8n's newer model nodes keep it. A model's name is no
// credential: n8n marks the parameter as none, and its value is read as the
// workflow writes it. Where the node names none a draft can write, it says why:
// the export carries none, since n8n leaves out of an export a parameter set to
// its default; or it names its model through an expression n8n reads only when
// the workflow runs. A type modelParams does not list gets neither.
func modelOf(tree json.RawMessage, node importer.Node) (string, string) {
	param, ok := modelParams[node.Type]
	if !ok {
		return "", ""
	}
	n, ok := workflowOf(tree).node(node.ID)
	if !ok {
		return "", ""
	}
	raw := memberAt(n.parameters, []string{param})
	if raw == nil {
		return "", "names no model in the export, which leaves out a parameter set to n8n's default for the node's type"
	}
	if value := memberAt(raw, []string{"value"}); value != nil {
		raw = value
	}
	var name string
	_ = json.Unmarshal(raw, &name)
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", "selects no model"
	case strings.HasPrefix(name, "="):
		return "", "names its model through an expression, which n8n reads only when the workflow runs"
	}
	return name, ""
}
