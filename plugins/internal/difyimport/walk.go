// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package difyimport

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// This file reads a Dify DSL's tree: the nodes the draft decides — each node of
// a workflow's graph, or the model, knowledge bases and tools of a chat
// application's configuration, and each plugin the application depends on — and
// what of each Dify configures for itself.

// The identifiers the nodes read off a chat application's configuration, an
// agent application and the dependencies are given: the path of the member each
// is read from, an element written with its position.
const (
	chatModelID      = "model_config.model"
	chatDatasetIDPre = "model_config.dataset_configs.datasets."
	chatToolIDPre    = "model_config.agent_mode.tools."
	agentID          = "agent"
	dependencyIDPre  = "dependencies."
)

// The types those nodes are given.
const (
	chatModelType   = "model"
	chatDatasetType = "dataset"
	chatToolType    = "tool"
	agentType       = "agent"
)

// dsl is the part of a Dify DSL's tree the importer reads.
type dsl struct {
	Workflow *struct {
		Graph struct {
			Nodes json.RawMessage `json:"nodes"`
			Edges json.RawMessage `json:"edges"`
		} `json:"graph"`
		EnvironmentVariables []json.RawMessage `json:"environment_variables"`
	} `json:"workflow"`
	ModelConfig *struct {
		Model          json.RawMessage `json:"model"`
		DatasetConfigs struct {
			Datasets struct {
				Datasets []json.RawMessage `json:"datasets"`
			} `json:"datasets"`
		} `json:"dataset_configs"`
		AgentMode struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"agent_mode"`
	} `json:"model_config"`
	Agent        json.RawMessage   `json:"agent"`
	Dependencies []json.RawMessage `json:"dependencies"`
	// App is the application's own block, undecoded, so an app that is not an
	// object leaves the rest of the DSL read; appName reads its name off it.
	App json.RawMessage `json:"app"`
}

// readDSL reads tree, false where it is not an object.
func readDSL(tree json.RawMessage) (dsl, bool) {
	var d dsl
	return d, json.Unmarshal(tree, &d) == nil
}

// walkTree lifts the nodes of a Dify DSL, in the order the DSL writes them: each
// node of a workflow's graph that names its type, with the graph's edges; or a
// chat application's model, each knowledge base and each tool of its
// configuration; or an agent application; and after them each plugin the
// application depends on.
func walkTree(tree json.RawMessage) ([]importer.Node, []importer.Edge) {
	d, ok := readDSL(tree)
	if !ok {
		return nil, nil
	}
	var nodes []importer.Node
	var edges []importer.Edge
	switch {
	case d.Workflow != nil:
		var graph []struct {
			ID   json.RawMessage `json:"id"`
			Data struct {
				Type json.RawMessage `json:"type"`
			} `json:"data"`
		}
		_ = json.Unmarshal(d.Workflow.Graph.Nodes, &graph)
		for _, n := range graph {
			// An identifier written as a number reads as the text it is written as.
			id, _ := draftyaml.ScalarText(n.ID)
			typ, _ := draftyaml.ScalarText(n.Data.Type)
			if id != "" && typ != "" {
				nodes = append(nodes, importer.Node{ID: id, Type: typ})
			}
		}
		edges = importer.EdgeList(d.Workflow.Graph.Edges)
	case d.ModelConfig != nil:
		if holdsObject(d.ModelConfig.Model) {
			nodes = append(nodes, importer.Node{ID: chatModelID, Type: chatModelType})
		}
		for i := range d.ModelConfig.DatasetConfigs.Datasets.Datasets {
			nodes = append(nodes, importer.Node{ID: chatDatasetIDPre + strconv.Itoa(i), Type: chatDatasetType})
		}
		for i := range d.ModelConfig.AgentMode.Tools {
			nodes = append(nodes, importer.Node{ID: chatToolIDPre + strconv.Itoa(i), Type: chatToolType})
		}
	case holdsObject(d.Agent):
		nodes = append(nodes, importer.Node{ID: agentID, Type: agentType})
	}
	for i, dep := range d.Dependencies {
		var entry struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(dep, &entry)
		nodes = append(nodes, importer.Node{ID: dependencyIDPre + strconv.Itoa(i), Type: firstOf(entry.Type, "dependency")})
	}
	return nodes, edges
}

// holdsObject reports whether raw is a JSON object.
func holdsObject(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m != nil
}

// configuredOf says what a node names that Dify configures for itself: the model
// a node calls and its provider, the tool a node calls and its provider, the
// knowledge bases a node retrieves from, the agent strategy an agent node runs,
// or the plugin a dependency names. A node naming none of these says nothing.
func configuredOf(tree json.RawMessage, node importer.Node) string {
	d, ok := readDSL(tree)
	if !ok {
		return ""
	}
	switch {
	case node.ID == chatModelID && d.ModelConfig != nil:
		return modelPhrase(d.ModelConfig.Model, "the model")
	case strings.HasPrefix(node.ID, chatDatasetIDPre) && d.ModelConfig != nil:
		if _, ok := elementOf(d.ModelConfig.DatasetConfigs.Datasets.Datasets, node.ID, chatDatasetIDPre); ok {
			return knowledgeBases(1)
		}
	case strings.HasPrefix(node.ID, chatToolIDPre) && d.ModelConfig != nil:
		if raw, ok := elementOf(d.ModelConfig.AgentMode.Tools, node.ID, chatToolIDPre); ok {
			return toolPhrase(raw)
		}
	case strings.HasPrefix(node.ID, dependencyIDPre):
		if raw, ok := elementOf(d.Dependencies, node.ID, dependencyIDPre); ok {
			return dependencyPhrase(raw)
		}
	case d.Workflow != nil:
		return graphNodePhrase(d.Workflow.Graph.Nodes, node)
	}
	return ""
}

// elementOf returns the element of list a node's identifier — prefix followed by
// its position — names.
func elementOf(list []json.RawMessage, id, prefix string) (json.RawMessage, bool) {
	i, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil || i < 0 || i >= len(list) {
		return nil, false
	}
	return list[i], true
}

// graphNodePhrase says what a node of a workflow's graph names that Dify
// configures for itself, read off its data by its type.
func graphNodePhrase(rawNodes json.RawMessage, node importer.Node) string {
	var nodes []struct {
		ID   json.RawMessage            `json:"id"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(rawNodes, &nodes) != nil {
		return ""
	}
	for _, n := range nodes {
		id, _ := draftyaml.ScalarText(n.ID)
		if id != node.ID {
			continue
		}
		switch node.Type {
		case "llm", "question-classifier", "parameter-extractor":
			return modelPhrase(n.Data["model"], "the model")
		case "tool":
			return toolPhrase(dataObject(n.Data))
		case "agent":
			strategy, _ := draftyaml.ScalarText(n.Data["agent_strategy_name"])
			provider, _ := draftyaml.ScalarText(n.Data["agent_strategy_provider_name"])
			if strategy == "" && provider == "" {
				return ""
			}
			return fmt.Sprintf("the agent strategy %q of the plugin %q, which Dify installs and configures", strategy,
				provider)
		case "knowledge-retrieval":
			var ids []json.RawMessage
			_ = json.Unmarshal(n.Data["dataset_ids"], &ids)
			phrase := knowledgeBases(len(ids))
			var single struct {
				Model json.RawMessage `json:"model"`
			}
			_ = json.Unmarshal(n.Data["single_retrieval_config"], &single)
			if m := modelPhrase(single.Model, "the model"); m != "" {
				phrase += ", and " + m + " it routes a query with"
			}
			var multiple struct {
				Reranking json.RawMessage `json:"reranking_model"`
			}
			_ = json.Unmarshal(n.Data["multiple_retrieval_config"], &multiple)
			if m := modelPhrase(multiple.Reranking, "the reranking model"); m != "" {
				phrase += ", and " + m
			}
			return phrase
		}
		return ""
	}
	return ""
}

// dataObject writes a node's data back as one JSON object.
func dataObject(data map[string]json.RawMessage) json.RawMessage {
	raw, _ := json.Marshal(data)
	return raw
}

// knowledgeBases names the knowledge bases a node retrieves from: count of them,
// each named by an identifier of the installation the application was exported
// from.
func knowledgeBases(count int) string {
	if count == 1 {
		return "a knowledge base, named by an identifier of the Dify installation the application was exported from " +
			"and exported without its documents: it is created inside Dify, in the vector store " + Module +
			" wires to the API server"
	}
	return fmt.Sprintf("%d knowledge bases, each named by an identifier of the Dify installation the application was "+
		"exported from and exported without its documents: they are created inside Dify, in the vector store %s "+
		"wires to the API server", count, Module)
}

// modelPhrase names a model a member of the DSL selects — an object holding its
// provider and its name, under name or model — after what, empty where it names
// neither.
func modelPhrase(raw json.RawMessage, what string) string {
	var m struct {
		Provider json.RawMessage `json:"provider"`
		Name     json.RawMessage `json:"name"`
		Model    json.RawMessage `json:"model"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	provider, _ := draftyaml.ScalarText(m.Provider)
	name, _ := draftyaml.ScalarText(m.Name)
	if name == "" {
		name, _ = draftyaml.ScalarText(m.Model)
	}
	switch {
	case name == "" && provider == "":
		return ""
	case name == "":
		return fmt.Sprintf("a model of the model provider %q, whose credentials are set inside Dify", provider)
	case provider == "":
		return fmt.Sprintf("%s %q, whose provider and credentials are set inside Dify", what, name)
	}
	return fmt.Sprintf("%s %q of the model provider %q, whose credentials are set inside Dify", what, name, provider)
}

// toolPhrase names the tool an object of the DSL calls — a tool node's data, or
// a chat application's tool — and its provider, empty where it names neither.
func toolPhrase(raw json.RawMessage) string {
	var t struct {
		ToolName     json.RawMessage `json:"tool_name"`
		ProviderID   json.RawMessage `json:"provider_id"`
		ProviderName json.RawMessage `json:"provider_name"`
		ProviderType json.RawMessage `json:"provider_type"`
	}
	if json.Unmarshal(raw, &t) != nil {
		return ""
	}
	tool, _ := draftyaml.ScalarText(t.ToolName)
	provider, _ := draftyaml.ScalarText(t.ProviderID)
	if provider == "" {
		provider, _ = draftyaml.ScalarText(t.ProviderName)
	}
	kind, _ := draftyaml.ScalarText(t.ProviderType)
	if tool == "" && provider == "" {
		return ""
	}
	of := ""
	if kind != "" {
		of = " " + strconv.Quote(kind)
	}
	return fmt.Sprintf("the tool %q of the%s tool provider %q, whose credentials are set inside Dify", tool, of,
		provider)
}

// dependencyPhrase names the plugin a dependency of the DSL names, by the
// identifier Dify installs it under, empty where it names none.
func dependencyPhrase(raw json.RawMessage) string {
	var dep struct {
		Type  json.RawMessage            `json:"type"`
		Value map[string]json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &dep) != nil {
		return ""
	}
	kind, _ := draftyaml.ScalarText(dep.Type)
	var id string
	for _, key := range []string{"marketplace_plugin_unique_identifier", "plugin_unique_identifier",
		"github_plugin_unique_identifier", "repo"} {
		if id, _ = draftyaml.ScalarText(dep.Value[key]); id != "" {
			break
		}
	}
	if id == "" {
		return ""
	}
	from := "which Dify installs"
	switch kind {
	case "marketplace":
		from = "which Dify installs from its marketplace"
	case "github":
		from = "which Dify installs from GitHub"
	case "package":
		from = "which Dify installs from a package"
	}
	return fmt.Sprintf("the plugin %q, %s", id, from)
}

// firstOf returns the first of its arguments that is not empty.
func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
