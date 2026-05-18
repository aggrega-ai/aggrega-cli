// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"fmt"
	"text/template"
)

// AssertionContext is the template data bound to one assertion-evaluation
// invocation. The shape matches chapter 08 §The template context.
//
// Field availability by phase:
//
//   - Step 4b (Component / Feature assertions): Values, Infra, Uses,
//     Secrets, Metadata are populated; Provides, Locals, Recipe are nil
//     (a missingkey=error halt fires on accidental references).
//   - Step 5b (Recipe / FeatureRecipe assertions): all fields populated.
//
// Iteration order across nested maps is alphabetical-key-ordered (matches
// the renderer's TemplateAlphabeticalMapDeep discipline). Author order is
// preserved at the OrderedMap layer but flattened to alphabetical at the
// template-render boundary; assertion predicates don't depend on key
// iteration order.
type AssertionContext struct {
	Values   map[string]any
	Infra    map[string]any
	Uses     map[string]any
	Secrets  map[string]any
	Metadata map[string]any
	Provides map[string]any
	Locals   map[string]any
	Recipe   *AssertionRecipeContext
}

// AssertionRecipeContext exposes Recipe-specific surface to Step 5b
// assertions: metadata for identity (name, version, etc.) plus source
// for the wrapped chart/module pointer. Nil at Step 4b.
type AssertionRecipeContext struct {
	Metadata map[string]any
	Source   any
}

// EvaluateAssertion runs the named template body against the context. The
// evaluator uses missingkey=error: any reference to a missing key halts
// with a no-such-key diagnostic. Authors needing optional-field idioms
// use hasKey to gate the reference, or index + default to provide a
// fallback. (Plain Sprig default does NOT work under missingkey=error:
// the bare reference `.Values.optional` halts before default can fire;
// index returns nil for missing keys and default can then take effect:
// `(index .Values "optional") | default <fallback>`.)
//
// Returns nil on pass (template renders to empty/whitespace and no fail
// call). Returns a non-nil error wrapping ErrAssertionFailed on
// {{ fail "..." }} invocation. Returns a non-nil error wrapping the
// template-engine error on parse/execute failures (e.g., missingkey=error
// halts surface here).
func EvaluateAssertion(name, body string, ctx AssertionContext) error {
	funcs := aggregaFuncMap()
	funcs["fail"] = func(msg string) (string, error) {
		return "", fmt.Errorf("%w: %s", ErrAssertionFailed, msg)
	}
	tmpl, err := template.New(name).
		Option("missingkey=error").
		Funcs(funcs).
		Parse(body)
	if err != nil {
		return fmt.Errorf("assertion %q parse: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return fmt.Errorf("assertion %q: %w", name, err)
	}
	return nil
}
