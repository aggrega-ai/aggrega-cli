// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/selectormatch"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// langflow is the one format this importer serves: Langflow flow JSON, parsed by
// the host as JSON and walked by walkTree, and run by the Langflow platform.
var langflow = importer.Format{
	Name:     "langflow",
	Syntax:   "json",
	Mappings: mappings,
	Walk:     walkTree,
	Platform: &platform,
}

// platform is the Langflow platform as the standard library's
// core/ai-agent/langflow Component runs it: the flow is carried as the
// Component's flow_definition and named by its flow_id, an MCP Tools node
// reaches the MCP server it names through the tools connection of that name,
// and no other node reaches a tools connection (wire.go), the model socket
// draws a single connection, a referenced model is given the model its node
// selects (model.go), the inputs the Component requires that a flow cannot
// supply are left for the Release, and the lines the compose Recipe needs for
// the administrator's password are shown for an author building for compose.
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
	LeftForRelease: []string{
		"infra.image, the container image running Langflow",
		"the admin_password slot, the administrator's password, while values.require_authentication stays true",
	},
	TargetAttachments: []importer.TargetAttachment{composeAdminPassword},
}

// composeAdminPassword declares the administrator's password with the key the
// compose Recipe reads it under where an Aggregate names one. On compose the
// Recipe reads every secret the Release alone attaches under the key value, so
// the administrator's password and a model connection's credential, both
// attached by the Release, would arrive under one key and halt the build; the
// Kubernetes Recipe references each secret where the Release keeps it and needs
// no such line.
var composeAdminPassword = importer.TargetAttachment{
	Target: "compose",
	Slot:   "admin_password",
	Type:   "core/secret-value",
	Key:    "LANGFLOW_SUPERUSER_PASSWORD",
	Why: "the compose Recipe reads every secret the Release alone attaches under the key value, so the " +
		"administrator's password and a connection's credential would both arrive under it and halt the build; the " +
		"Release still attaches admin_password, naming this type or none",
}

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
var mappings = map[string]importer.Mapping{
	"OpenAIModel":      {Component: "core/ai-model/openai", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}, Vendor: "openai"},
	"AnthropicModel":   {Component: "core/ai-model/anthropic", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}, Vendor: "anthropic"},
	"OpenAIEmbeddings": {Component: "core/ai-model/openai-embedding", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "embedding", "component.aggrega.ai/hosting": "managed"}, Vendor: "openai"},
	"Retrieval":        {Component: "core/ai-tool/retrieval", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "read-only"}, Vendor: "retrieval"},
	"APIRequest":       {Component: "core/ai-tool/http-request", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "mutating"}, Vendor: "http"},
	mcpToolsType:       {Component: "core/ai-tool/mcp", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/transport": "http", "tool.aggrega.ai/impact": "mutating"}, Vendor: "mcp"},
}
