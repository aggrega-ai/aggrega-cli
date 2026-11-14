// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/selectormatch"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// flowise is the one format this importer serves: Flowise flow JSON, parsed by
// the host as JSON and walked by walkTree, and run by the Flowise platform.
var flowise = importer.Format{
	Name:     "flowise",
	Syntax:   "json",
	Mappings: mappings,
	Walk:     walkTree,
	Platform: &platform,
}

// platform is the Flowise platform as the standard library's
// core/ai-agent/flowise Component runs it: the chatflow is carried as the
// Component's flow_definition and named by its chatflow_id, each part wired to
// it is reached as the platform delivers its connection (wire.go), the model
// socket draws a single connection, a referenced model is given the model its
// node selects (model.go), the image is drafted at the version below, the
// owner's email, which no chatflow holds, is left for the Release, the owner's
// password and the model connection's credential are attached by reference,
// read from FLOWISE_OWNER_PASSWORD and OPENAI_API_KEY, the variable the platform
// delivers the key as, and, for the compose target, the owner's password is
// declared with the key the compose Recipe reads it under where an Aggregate
// names one, OWNER_PASSWORD.
var platform = importer.Platform{
	Component:       "core/ai-agent/flowise",
	DefinitionValue: "flow_definition",
	IdentifierValue: "chatflow_id",
	Identifier:      chatflowID,
	Credentials:     credentialsOf,
	Match:           selectormatch.Match,
	Wire:            wireOf,
	Reached:         reachedOf,
	Single:          singleSockets,
	Standing:        standingOf,
	Model:           modelOf,
	Image:           image,
	LeftForRelease: []importer.LeftItem{
		{Path: "infra.owner_email", Text: "the email address of the platform's owner account, required while " +
			"values.flow_definition is set"},
	},
	Secrets: []importer.Secret{{Path: "features.owner_password", Variable: "FLOWISE_OWNER_PASSWORD",
		Key: "OWNER_PASSWORD", Text: "the owner account's password, required while values.flow_definition is set"}},
	CredentialVariables: map[string]string{"model": "OPENAI_API_KEY"},
}

// image is the Flowise image a draft runs the platform from: the version the
// standard library's Flowise Recipes are known to run. A chatflow states no
// version of Flowise, so no other is named.
const image = "flowiseai/flowise:3.1.3"

// detect is how a Flowise flow is recognised when an import names no format,
// declared in describe() and recorded on the importer's Plugin entity. A Flowise
// export — a chatflow or an agentflow — keeps its graph flat at the top level,
// so a source that parses as JSON with arrays at nodes and edges is one. The
// third predicate asks for nothing at data.nodes, where a flow that nests its
// graph carries it, so a source carrying both shapes is not claimed by this
// signature as well. The host evaluates the signature without knowing what
// Flowise is.
var detect = []aggrega.DetectSignature{{
	Syntax: flowise.Syntax,
	Match: []aggrega.PathMatch{
		{Path: "nodes", Is: "array"},
		{Path: "edges", Is: "array"},
		{Path: "data.nodes", Is: "absent"},
	},
}}

// mappings is the Flowise mapping arm: the standard-library component each
// Flowise node type maps to. A row draws its signal only where the workspace's
// catalog holds the component it names. The whole-flow wrapper component
// core/ai-agent/flowise is not a row here: where the catalog holds it, the
// scorer weighs every node against it as it does every other catalog Component.
var mappings = map[string]importer.Mapping{
	"chatOpenAI":       {Component: "core/ai-model/openai", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}, Vendor: "openai"},
	"chatAnthropic":    {Component: "core/ai-model/anthropic", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"}, Vendor: "anthropic"},
	"openAIEmbeddings": {Component: "core/ai-model/openai-embedding", Category: "ai-model", Traits: map[string]string{"model.aggrega.ai/capability": "embedding", "component.aggrega.ai/hosting": "managed"}, Vendor: "openai"},
	"retrieverTool":    {Component: "core/ai-tool/retrieval", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "read-only"}, Vendor: "retrieval"},
	"httpRequest":      {Component: "core/ai-tool/http-request", Category: "ai-tool", Traits: map[string]string{"tool.aggrega.ai/impact": "mutating"}, Vendor: "http"},
}
