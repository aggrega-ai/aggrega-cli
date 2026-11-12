// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package adkimport

import (
	"regexp"
	"slices"
	"strings"
)

// modelClass is one class of model ADK serves a model string through: its name,
// as ADK names the class; whether it reaches the Gemini API alone, never Vertex
// AI; and the patterns of the model strings ADK serves through it.
type modelClass struct {
	name           string
	geminiAPIAlone bool
	patterns       []modelPattern
}

// modelPattern is one pattern of a model class: the expression a model string
// ADK serves through the class fully matches; the family a draft names such a
// model by; and whether ADK sends that model's requests to the Gemini API, as
// the catalog's Gemini Component does.
type modelPattern struct {
	re        *regexp.Regexp
	family    string
	geminiAPI bool
}

// modelClasses mirrors the model classes Google ADK 2.13.0 registers and the
// patterns each serves, in the order it registers them — its google/adk/models
// package, the _LAZY_PROVIDERS table of __init__.py, whose Gemini patterns are
// also those google_llm.py's Gemini class names — since a model string fully
// matching patterns of two classes is served by the one registered first. It is
// ADK's own list, and changes only as ADK's does.
//
// geminiAPI is set for the patterns whose models ADK sends to the Gemini API:
// the Gemini class's own model names, which reach the Gemini API or Vertex AI as
// the application's environment chooses, and the Gemma class's (gemma_llm.py), a
// subclass of Gemini for Gemma 3 models, which supports the Gemini API only and
// not Vertex AI (gemma_llm.py:190-191) and so is marked geminiAPIAlone. The
// Gemini class's two projects/ patterns are Vertex AI resource names, and are not
// set. ApigeeLlm is a subclass of Gemini too, but sends its requests to an Apigee
// proxy at an address of its own, so it is not set there.
//
// One pattern is written as an equivalent: ADK's LiteLLM class serves
// ollama/(?!gemma3).*, a form the regular expressions here do not take; its
// Gemma-through-Ollama class, registered before it, serves ollama/gemma3.*, so
// ollama/.* in the LiteLLM class's place serves the same strings.
var modelClasses = []modelClass{
	{name: "Gemini", patterns: slices.Concat(
		patternsOf("Gemini", true, `gemini-.*`, `gemma-4.*`, `model-optimizer-.*`),
		// ADK builds the client of a model whose name opens with projects/ for
		// Vertex AI, whatever the environment chooses (google_llm.py:463-464).
		patternsOf("Vertex AI", false, `projects\/.+\/locations\/.+\/endpoints\/.+`,
			`projects\/.+\/locations\/.+\/publishers\/google\/models\/gemini.+`))},
	{name: "Gemma", geminiAPIAlone: true, patterns: patternsOf("Gemma", true, `gemma-.*`)},
	{name: "ApigeeLlm", patterns: patternsOf("Apigee", false, `apigee\/.*`)},
	{name: "Claude", patterns: patternsOf("Anthropic", false, `claude-.*`)},
	{name: "Gemma3Ollama", patterns: patternsOf("Gemma, through Ollama", false, `ollama/gemma3.*`)},
	{name: "OpenAILlm", patterns: patternsOf("OpenAI", false, `gpt-.*`, `o\d+-.*`)},
	{name: "LiteLlm", patterns: patternsOf("LiteLLM", false, `openai/.*`, `azure/.*`, `azure_ai/.*`, `groq/.*`,
		`anthropic/.*`, `bedrock/.*`, `ollama/.*`, `ollama_chat/.*`, `together_ai/.*`, `vertex_ai/.*`, `mistral/.*`,
		`deepseek/.*`, `fireworks_ai/.*`, `cohere/.*`, `databricks/.*`, `ai21/.*`)},
	{name: "OCIGenAILlm", patterns: patternsOf("OCI Generative AI", false, `meta\.llama-.*`, `google\.gemini-.*`,
		`google\.gemma-.*`, `xai\.grok-.*`, `mistralai\.mistral-.*`, `mistralai\.mixtral-.*`, `nvidia\..*`)},
}

// patternsOf compiles each pattern to match a whole model string, as ADK matches
// one, naming its models by family and marking whether ADK sends their requests
// to the Gemini API.
func patternsOf(family string, geminiAPI bool, patterns ...string) []modelPattern {
	out := make([]modelPattern, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, modelPattern{re: regexp.MustCompile(`^(?:` + p + `)$`), family: family, geminiAPI: geminiAPI})
	}
	return out
}

// liteLLMIfKnown is the family a model string written as provider/model is
// named by where no class's pattern matches it: ADK hands it to LiteLLM only
// where LiteLLM's own list of providers holds the provider it names, and
// otherwise serves it through none of its classes.
const liteLLMIfKnown = "LiteLLM, if LiteLLM knows the provider it names"

// served is how ADK 2.13.0 serves one model string: the family a draft names it
// by, empty where ADK serves it through none of its classes; whether ADK sends
// its requests to the Gemini API, and whether to the Gemini API alone, never
// Vertex AI; and the model name the class serving it is handed.
type served struct {
	family                    string
	geminiAPI, geminiAPIAlone bool
	name                      string
}

// servedBy returns how ADK 2.13.0 serves a model string, as its models registry
// resolves one — google/adk/models/registry.py, LLMRegistry.resolve and
// new_llm. A string written as class:model whose class names one of ADK's
// classes, as ADK compares the two, is served by that class, which is handed
// the model after the colon, named as the first of the class's patterns that
// model fully matches names one, or as its first pattern does where it matches
// none; any other string by the first class, in the order ADK registers them,
// one of whose patterns it fully matches, handed the whole string and named as
// that pattern names one. A string matching none but written as provider/model
// is named by liteLLMIfKnown, and reaches no class here: whether LiteLLM serves
// it rests on LiteLLM's own list of providers, which the import does not hold.
// Any other string is served by no class.
func servedBy(model string) served {
	if prefix, rest, ok := strings.Cut(model, ":"); ok {
		for _, c := range modelClasses {
			if classPrefix(prefix, c.name) {
				return c.serve(c.patternOf(rest), rest)
			}
		}
	}
	for _, c := range modelClasses {
		for _, p := range c.patterns {
			if p.re.MatchString(model) {
				return c.serve(p, model)
			}
		}
	}
	if strings.Contains(model, "/") {
		return served{family: liteLLMIfKnown, name: model}
	}
	return served{name: model}
}

// patternOf returns the first of the class's patterns a model name fully
// matches, or its first pattern where the name matches none.
func (c modelClass) patternOf(name string) modelPattern {
	for _, p := range c.patterns {
		if p.re.MatchString(name) {
			return p
		}
	}
	return c.patterns[0]
}

// serve is how the class serves a model name one of its patterns names.
func (c modelClass) serve(p modelPattern, name string) served {
	return served{family: p.family, geminiAPI: p.geminiAPI, geminiAPIAlone: p.geminiAPI && c.geminiAPIAlone,
		name: name}
}

// classPrefix reports whether prefix names the class name as ADK compares the
// two: in any letter case, the class's whole name or the name without a closing
// Llm.
func classPrefix(prefix, name string) bool {
	p, n := strings.ToLower(prefix), strings.ToLower(name)
	return p == n || p == strings.TrimSuffix(n, "llm")
}

// familyOf names the family ADK serves a model string through, for a
// sentence naming the model: its family, or that ADK serves it through none of
// its classes.
func familyOf(model string) string {
	if f := servedBy(model).family; f != "" {
		return f
	}
	return "none of the model classes ADK registers"
}
