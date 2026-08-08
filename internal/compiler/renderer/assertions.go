// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
)

// AssertionContext is the template data bound to one assertion-evaluation
// invocation. The shape matches chapter 08 §The template context.
//
// Field availability by phase:
//
//   - Step 4b (Component / Feature assertions): Values, Infra, Uses,
//     Features, Metadata are populated; Provides, Locals, Recipe are nil
//     (a missingkey=error halt fires on accidental references).
//   - Step 5b (Recipe / FeatureRecipe assertions): all fields populated.
//
// Iteration order across nested maps is alphabetical-key-ordered (matches
// the renderer's TemplateAlphabeticalMapDeep discipline). Author order is
// preserved at the OrderedMap layer but flattened to alphabetical at the
// template-render boundary; assertion predicates don't depend on key
// iteration order.
// The json tags name the keys an assertion body reads, and each one spells the
// field name exactly. They are declared rather than left to a fallback because
// the delivered key set is a published authoring contract: catalog assertions are
// written against these spellings, so the surface states them instead of
// inheriting whatever a tag-less field would be named.
type AssertionContext struct {
	Values   map[string]any          `json:"values"`
	Infra    map[string]any          `json:"infra"`
	Uses     map[string]any          `json:"uses"`
	Features map[string]any          `json:"features"`
	Metadata map[string]any          `json:"metadata"`
	Provides map[string]any          `json:"provides"`
	Locals   map[string]any          `json:"locals"`
	Recipe   *AssertionRecipeContext `json:"recipe"`
}

// AssertionRecipeContext exposes Recipe-specific surface to Step 5b
// assertions: metadata for identity (name, version, etc.) plus source
// for the wrapped chart/module pointer. Nil at Step 4b.
//
// The json tags carry the same reason as AssertionContext's: the delivered key
// set is what assertion bodies are authored against, so it is declared here.
type AssertionRecipeContext struct {
	Metadata map[string]any `json:"metadata"`
	Source   any            `json:"source"`
}

// EvaluateAssertion runs the named template body against the context. The
// evaluator uses missingkey=error: any reference to a missing key halts
// with a no-such-key diagnostic. Authors needing optional-field idioms
// use hasKey to gate the reference, index + default to provide a
// fallback, or the optional helper, which takes the map, the key and the
// fallback and performs the presence check directly:
// `optional .values "optional" <fallback>`. (Plain Sprig default does NOT
// work under missingkey=error: the bare reference `.values.optional`
// halts before default can fire; index returns nil for missing keys and
// default can then take effect:
// `(index .values "optional") | default <fallback>`.)
//
// Returns nil on pass (template renders to empty/whitespace and no fail
// call). Returns a non-nil error wrapping ErrAssertionFailed on
// {{ fail "..." }} invocation. Returns a non-nil error wrapping the
// template-engine error on parse/execute failures (e.g., missingkey=error
// halts surface here).
func EvaluateAssertion(name, body string, ctx AssertionContext) error {
	// The assertion surface declares no narrowing: every field the context type
	// defines is readable from an assertion. What it contributes is fail — the one
	// function that exists only here, because an assertion is the only surface
	// whose purpose is to halt the build rather than to produce text.
	_, err := templateEval{
		name:       name,
		text:       body,
		scope:      templateScope{data: ctx},
		missingKey: missingKeyError,
		extraFuncs: map[string]any{
			"fail": func(msg string) (string, error) {
				return "", fmt.Errorf("%w: %s", ErrAssertionFailed(), msg)
			},
		},
		failed: func(stage templateEvalStage, err error) error {
			if stage == stageParse {
				return fmt.Errorf("assertion %q parse: %w", name, err)
			}
			return fmt.Errorf("assertion %q: %w", name, err)
		},
	}.run()
	return err
}
