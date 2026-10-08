// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// AssertionContext is the template data bound to one assertion-evaluation
// invocation. The shape matches §The template context in
// language-reference/templates/assertions-block.md.
//
// Field availability by the stage each assertion kind is evaluated at:
//
//   - Component / Feature assertions, at the graph stage (the finalised
//     graph): Values, Infra, Uses, Features, Metadata are populated;
//     Provides, Locals, Recipe are nil (a missingkey=error halt fires on
//     accidental references).
//   - Recipe / FeatureRecipe assertions, at the hydrated stage (each node's
//     locals and provides resolved): all fields populated.
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

// AssertionRecipeContext exposes Recipe-specific surface to Recipe /
// FeatureRecipe assertions: metadata for identity (name, version, etc.) plus
// source for the chart or module the Recipe sources. Nil for Component /
// Feature assertions.
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
// use hasKey to gate the reference, or the optional helper, which takes
// the map, the key and the fallback and performs the presence check
// directly: `optional .values "optional" <fallback>`. (Plain Sprig default
// does NOT work under missingkey=error: the bare reference
// `.values.optional` halts before default can fire. Reading the key
// through index does not help either: index of a map key the map does not
// hold halts naming the key, on this surface as on every other.)
//
// Returns nil on pass (template renders to empty/whitespace and no fail
// call). Returns a non-nil error wrapping ErrAssertionFailed on
// {{ fail "..." }} invocation. Returns a non-nil error wrapping the
// template-engine error on parse/execute failures (e.g., missingkey=error
// halts surface here).
//
// withheld names what of ctx an author declared sensitive. An assertion whose
// body reads such a value and fails reports its own sentence with the
// placeholder where the value stood, or, where what it did depended on what the
// value holds, the failing position and a sentence saying the text is withheld —
// never the value. The condition it reports is unchanged either way.
func EvaluateAssertion(name, body string, ctx AssertionContext, withheld redact.Keys) error {
	// The assertion surface declares no narrowing: every field the context type
	// defines is readable from an assertion. What it contributes is fail — the one
	// function that exists only here, because an assertion is the only surface
	// whose purpose is to halt the build rather than to produce text.
	_, err := templateEval{
		name:       name,
		text:       body,
		scope:      templateScope{}.with(ctx, withheld),
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
