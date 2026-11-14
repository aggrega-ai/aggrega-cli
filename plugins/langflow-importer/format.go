// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/selectormatch"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// langflow is the one format this importer serves: Langflow flow JSON, parsed by
// the host as JSON and walked by walkTree, its draft named after the flow's name,
// and run by the Langflow platform.
var langflow = importer.Format{
	Name:     "langflow",
	Syntax:   "json",
	Mappings: mappings,
	Walk:     walkTree,
	Title:    flowName,
	Platform: &platform,
}

// platform is the Langflow platform as the standard library's
// core/ai-agent/langflow Component runs it: the flow is carried as the
// Component's flow_definition and named by its flow_id, an MCP Tools node
// reaches the MCP server it names through the tools connection of that name,
// and no other node reaches a tools connection (wire.go), the model socket
// draws a single connection, which no model node naming a provider other than
// OpenAI takes (wire.go), a referenced model is given the model its node
// selects (model.go), the image is drafted at the version below, with the
// version the flow was last tested with named where it differs, the
// administrator's password and the model connection's credential are attached
// by reference, read from LANGFLOW_ADMIN_PASSWORD and OPENAI_API_KEY — the
// variable the Language Model node's own key field names — and, for the compose
// target, the administrator's password is declared with the key the compose
// Recipe reads it under where an Aggregate names one, LANGFLOW_SUPERUSER_PASSWORD.
var platform = importer.Platform{
	Component:       "core/ai-agent/langflow",
	DefinitionValue: "flow_definition",
	IdentifierValue: "flow_id",
	Identifier:      flowID,
	Credentials:     credentialsOf,
	Match:           selectormatch.Match,
	Reach:           reachOf,
	Refuse:          refuseOf,
	Wire:            wireOf,
	Reached:         reachedOf,
	Single:          singleSockets,
	Standing:        standingOf,
	Model:           modelOf,
	Image:           image,
	SourceVersion:   lastTestedVersion,
	Secrets: []importer.Secret{{Path: "features.admin_password", Variable: "LANGFLOW_ADMIN_PASSWORD",
		Key:  "LANGFLOW_SUPERUSER_PASSWORD",
		Text: "the administrator's password, read while values.require_authentication stays true"}},
	CredentialVariables: map[string]string{"model": "OPENAI_API_KEY"},
}

// image is the Langflow image a draft runs the platform from: the version the
// standard library's Langflow Recipes are known to run, which loads a flow
// directory at startup as those Recipes deliver it; an image at Langflow 1.5.1
// or older does not.
const image = "langflowai/langflow:1.6.0"

// detect is how a Langflow flow is recognised when an import names no format,
// declared in describe() and recorded on the importer's Plugin entity. A
// Langflow export nests its graph under a top-level data object, so a source
// that parses as JSON and carries an array at data.nodes is one. The host
// evaluates the signature without knowing what Langflow is.
var detect = []aggrega.DetectSignature{{
	Syntax: langflow.Syntax,
	Match:  []aggrega.PathMatch{{Path: "data.nodes", Is: "array"}},
}}

// mappings is the Langflow mapping arm: the standard-library component each
// Langflow node type maps to. A row draws its signal only where the workspace's
// catalog holds the component it names. The whole-flow wrapper component
// core/ai-agent/langflow is not a row here: where the catalog holds it, the
// scorer weighs every node against it as it does every other catalog Component.
//
// MCPTools is Langflow's MCP Tools component, which calls the tools an MCP
// server publishes; the server it reaches is core/ai-tool/mcp, a server reached
// over HTTP that may change what it acts on.
//
// LanguageModelComponent is Langflow's Language Model component, the chat model
// its starter flows call. It runs on the model provider its provider field
// names, OpenAI by default, and maps to core/ai-model/openai, the model the
// platform's model connection serves: that connection is delivered as
// OPENAI_BASE_URL and OPENAI_API_KEY. A node naming another provider is refused
// the model socket (refuseOf, wire.go), so it stays in the flow, reported with
// the provider it names, and no model is drafted for it.
var mappings = map[string]importer.Mapping{
	"OpenAIModel":            {Component: "core/ai-model/openai", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}, Vendor: "openai"},
	"LanguageModelComponent": {Component: "core/ai-model/openai", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}, Vendor: "openai"},
	"AnthropicModel":         {Component: "core/ai-model/anthropic", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}, Vendor: "anthropic"},
	"OpenAIEmbeddings":       {Component: "core/ai-model/openai-embedding", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "embedding", "component.aggrega.ai/hosting": "managed"}, Vendor: "openai"},
	"Retrieval":              {Component: "core/ai-tool/retrieval", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "read-only"}, Vendor: "retrieval"},
	"APIRequest":             {Component: "core/ai-tool/http-request", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "mutating"}, Vendor: "http"},
	mcpToolsType:             {Component: "core/ai-tool/mcp", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/transport": "http", "tool.aggrega.ai/impact": "mutating"}, Vendor: "mcp"},
}
