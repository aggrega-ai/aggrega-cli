// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package modelcred names the variable the credential an agent presents to its
// model is read from, for the importers drafting an agent wired to a model: the
// variable the source states, where it states one, and otherwise the one the
// model provider's own client libraries read the provider's API key from, which
// the draft says the source does not state.
package modelcred

import "github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"

// provider is a model provider whose client libraries read its API key from one
// environment variable by convention: its name, as a sentence names it, and that
// variable.
type provider struct {
	name, variable string
}

// conventional are the catalog's model Components whose provider's client
// libraries read the API key from one variable by convention, by Component name.
// A provider whose libraries read no one variable — a cloud provider's own
// credential chain, a local runtime taking no key — is not listed.
var conventional = map[string]provider{
	"core/ai-model/openai":       {"OpenAI", "OPENAI_API_KEY"},
	"core/ai-model/azure-openai": {"Azure OpenAI", "AZURE_OPENAI_API_KEY"},
	"core/ai-model/anthropic":    {"Anthropic", "ANTHROPIC_API_KEY"},
	"core/ai-model/gemini":       {"Google's Gemini API", "GOOGLE_API_KEY"},
	"core/ai-model/mistral":      {"Mistral", "MISTRAL_API_KEY"},
	"core/ai-model/cohere":       {"Cohere", "COHERE_API_KEY"},
}

// Credential is the secret an agent keyed key attaches on its socket connection
// under alias to the model instance keyed target, an instance of component, as
// importer.ConnectionCredential writes one: read from stated, the variable the
// source names for it, where it names one; otherwise from the variable
// component's provider's client libraries read the API key from by convention,
// the secret's text saying the source states none; and, for a provider listed
// with none, from the variable ConnectionCredential names after target.
func Credential(key, socket, alias, target, component, stated string) importer.Secret {
	if stated != "" {
		return importer.ConnectionCredential(key, socket, alias, target, stated)
	}
	p, ok := conventional[component]
	if !ok {
		return importer.ConnectionCredential(key, socket, alias, target, "")
	}
	secret := importer.ConnectionCredential(key, socket, alias, target, p.variable)
	secret.Text += " (the source names no variable for it, so it is read from the one " + p.name +
		"'s client libraries read the API key from)"
	return secret
}
