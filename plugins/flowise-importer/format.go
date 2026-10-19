// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/selectormatch"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
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
// node selects (model.go), the inputs the Component requires that a chatflow
// cannot supply are left for the Release, and the lines the compose Recipe
// needs for the owner's password are shown for an author building for compose.
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
	LeftForRelease: []string{
		"infra.image, the container image running Flowise",
		"infra.owner_email, the email address of the platform's owner account, required while values.flow_definition is set",
		"the owner_password slot, that account's password, required while values.flow_definition is set",
	},
	TargetAttachments: []importer.TargetAttachment{composeOwnerPassword},
}

// composeOwnerPassword declares the owner's password with the key the compose
// Recipe reads it under where an Aggregate names one. On compose the Recipe
// reads every secret the Release alone attaches under the key value, so the
// owner's password and a model connection's credential, both attached by the
// Release, would arrive under one key and halt the build; the Kubernetes Recipe
// references each secret where the Release keeps it and needs no such line.
var composeOwnerPassword = importer.TargetAttachment{
	Target: "compose",
	Slot:   "owner_password",
	Type:   "core/secret-value",
	Key:    "OWNER_PASSWORD",
	Why: "the compose Recipe reads every secret the Release alone attaches under the key value, so the owner's " +
		"password and a connection's credential would both arrive under it and halt the build; the Release still " +
		"attaches owner_password, naming this type or none",
}

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
