// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/selectormatch"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// n8n is the one format this importer serves: an exported n8n workflow, parsed
// by the host as JSON and walked by walkTree, its draft named after the
// workflow's name, and run by the n8n platform.
var n8n = importer.Format{
	Name:     "n8n",
	Syntax:   "json",
	Mappings: mappings,
	Walk:     walkTree,
	Title:    workflowName,
	Platform: &platform,
}

// platform is the n8n platform as the standard library's core/ai-agent/n8n
// Component runs it: the workflow is carried as the Component's flow_definition
// and named by its workflow_id, each part wired to it is reached as the workflow
// writes it (wire.go), the model socket draws a single connection, a referenced
// model is given the model its node selects (model.go), and the input the
// Component requires that a workflow cannot supply is left for the Release.
var platform = importer.Platform{
	Component:       "core/ai-agent/n8n",
	DefinitionValue: "flow_definition",
	IdentifierValue: "workflow_id",
	Identifier:      workflowID,
	Credentials:     credentialsOf,
	Match:           selectormatch.Match,
	Wire:            wireOf,
	Single:          singleSockets,
	Standing:        standingOf,
	Model:           modelOf,
	LeftForRelease: []importer.LeftItem{
		{Path: "infra.image", Text: "the container image running n8n"},
	},
}

// detect is how an n8n workflow is recognised when an import names no format,
// declared in describe() and recorded on the importer's Plugin entity. An
// exported workflow carries its nodes as an array and the connections between
// them as an object at its top level — the two members n8n's own import asks a
// workflow for. The last two predicates ask for nothing at edges and at
// data.nodes, where a flow that lists its connections as an edge array, or nests
// its graph, carries it, so a source carrying such a shape as well is not
// claimed by this signature too. The host evaluates the signature without
// knowing what n8n is.
var detect = []aggrega.DetectSignature{{
	Syntax: n8n.Syntax,
	Match: []aggrega.PathMatch{
		{Path: "nodes", Is: "array"},
		{Path: "connections", Is: "object"},
		{Path: "edges", Is: "absent"},
		{Path: "data.nodes", Is: "absent"},
	},
}}

// The two packages n8n's node types are named under: its base nodes and its AI
// nodes. A node's type is its package, a dot, and the node's own name.
const (
	basePackage = "n8n-nodes-base."
	aiPackage   = "@n8n/n8n-nodes-langchain."
)

// Labels the mapping rows below share.
var (
	generativeManaged = map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}
	embeddingManaged  = map[string]string{"model.aggrega.ai/capability": "embedding", "component.aggrega.ai/hosting": "managed"}
	vectorEngine      = map[string]string{"datastore.aggrega.ai/engine": "vector"}
)

// mappings is the n8n mapping arm: the standard-library component each n8n node
// type maps to, keyed by the node's whole type. A row draws its signal only where
// the workspace's catalog holds the component it names. The whole-workflow
// component core/ai-agent/n8n is not a row here: where the catalog holds it, the
// scorer weighs every node against it as it does every other catalog Component.
//
// The rows are the language models, the embeddings, the MCP client tool, the
// HTTP request tool, the search tool, the vector stores and the chat memories
// backed by a service whose component the standard library carries. A node run
// inside n8n itself — an agent, a chain, a trigger, a memory held in the
// workflow's own process, a code tool — is the workflow's own, and no row maps
// it.
var mappings = map[string]importer.Mapping{
	aiPackage + "lmChatOpenAi":          {Component: "core/ai-model/openai", Category: "ai-model", Traits: generativeManaged, Vendor: "openai"},
	aiPackage + "lmOpenAi":              {Component: "core/ai-model/openai", Category: "ai-model", Traits: generativeManaged, Vendor: "openai"},
	aiPackage + "lmChatAnthropic":       {Component: "core/ai-model/anthropic", Category: "ai-model", Traits: generativeManaged, Vendor: "anthropic"},
	aiPackage + "lmChatMistralCloud":    {Component: "core/ai-model/mistral", Category: "ai-model", Traits: generativeManaged, Vendor: "mistral"},
	aiPackage + "lmChatGoogleGemini":    {Component: "core/ai-model/gemini", Category: "ai-model", Traits: generativeManaged, Vendor: "gemini"},
	aiPackage + "lmChatCohere":          {Component: "core/ai-model/cohere", Category: "ai-model", Traits: generativeManaged, Vendor: "cohere"},
	aiPackage + "lmChatAzureOpenAi":     {Component: "core/ai-model/azure-openai", Category: "ai-model", Traits: generativeManaged, Vendor: "azure"},
	aiPackage + "lmChatAwsBedrock":      {Component: "core/ai-model/bedrock", Category: "ai-model", Traits: generativeManaged, Vendor: "bedrock"},
	aiPackage + "lmChatGoogleVertex":    {Component: "core/ai-model/vertex-ai", Category: "ai-model", Traits: generativeManaged, Vendor: "vertex"},
	aiPackage + "lmChatOllama":          {Component: "core/ai-model/ollama", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "self-hosted"}, Vendor: "ollama"},
	aiPackage + "embeddingsOpenAi":      {Component: "core/ai-model/openai-embedding", Category: "ai-model", Traits: embeddingManaged, Vendor: "openai"},
	aiPackage + "embeddingsAzureOpenAi": {Component: "core/ai-model/azure-openai-embedding", Category: "ai-model", Traits: embeddingManaged, Vendor: "azure"},
	aiPackage + "embeddingsAwsBedrock":  {Component: "core/ai-model/bedrock-embedding", Category: "ai-model", Traits: embeddingManaged, Vendor: "bedrock"},
	aiPackage + "embeddingsGoogleVertex": {Component: "core/ai-model/vertex-ai-embedding", Category: "ai-model", Traits: embeddingManaged,
		Vendor: "vertex"},
	aiPackage + mcpClientTool:         {Component: "core/ai-tool/mcp", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/transport": "http", "tool.aggrega.ai/impact": "mutating"}, Vendor: "mcp"},
	aiPackage + "toolHttpRequest":     {Component: "core/ai-tool/http-request", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "mutating"}, Vendor: "http"},
	basePackage + "httpRequestTool":   {Component: "core/ai-tool/http-request", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "mutating"}, Vendor: "http"},
	aiPackage + "toolSerpApi":         {Component: "core/ai-tool/search", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "read-only"}, Vendor: "search"},
	aiPackage + "vectorStoreQdrant":   {Component: "core/datastore/qdrant", Category: "datastore", Traits: vectorEngine, Vendor: "qdrant"},
	aiPackage + "vectorStorePGVector": {Component: "core/datastore/pgvector", Category: "datastore", Traits: vectorEngine, Vendor: "pgvector"},
	aiPackage + "vectorStoreChromaDB": {Component: "core/datastore/chroma", Category: "datastore", Traits: vectorEngine, Vendor: "chroma"},
	aiPackage + "vectorStoreMilvus":   {Component: "core/datastore/milvus", Category: "datastore", Traits: vectorEngine, Vendor: "milvus"},
	aiPackage + "vectorStoreWeaviate": {Component: "core/datastore/weaviate", Category: "datastore", Traits: vectorEngine, Vendor: "weaviate"},
	aiPackage + "memoryPostgresChat":  {Component: "core/datastore/postgres", Category: "datastore", Vendor: "postgres"},
	aiPackage + "memoryRedisChat":     {Component: "core/datastore/redis", Category: "datastore", Traits: map[string]string{"datastore.aggrega.ai/engine": "kv"}, Vendor: "redis"},
}
