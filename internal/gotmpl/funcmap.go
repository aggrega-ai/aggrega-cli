// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"maps"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
)

// FuncMap returns the template-function set used by Aggrega's template
// surfaces: Sprig v3's FuncMap augmented with the Aggrega-specific
// helpers [Helpers] returns. A fresh map is returned on each call so
// callers can add per-site functions (e.g., the assertion evaluator's
// `fail` override) without mutating other callers' maps.
func FuncMap() template.FuncMap {
	funcs := sprig.FuncMap()
	// Iterating a map is safe here where it usually would not be: the loop only
	// assigns distinct keys into another map, so the merged set is the same
	// whatever order the keys arrive in and no iteration order can reach the
	// rendered bytes.
	maps.Copy(funcs, Helpers())
	return funcs
}

// Helpers returns the Aggrega-specific template helpers on their own,
// without the Sprig set [FuncMap] layers them over. It is the single
// binding of each helper's authored name to its implementation, so a
// surface that wants Aggrega's helpers without Sprig's — a surface
// evaluating an authored coordinate rather than rendering an artifact —
// gets the same behavior under the same name as the full set does.
// A fresh map is returned on each call.
//
// The helper surface grows additively as catalog-authoring needs
// surface. It binds [optional] and [capitalizedWords], and carries the
// replacements [BuiltinReplacements] returns, so every surface taking this
// set reads `index` the strict way that function describes.
//
// The helper functions themselves are unexported. Wiring one under a
// name of your own choosing is therefore not possible from outside this
// package, which is what keeps an authored name meaning one thing on
// every surface that registers it. A helper whose behavior is shared
// with a surface outside this package is bound through an unexported
// forwarder here for that reason, rather than by naming the shared
// implementation directly.
func Helpers() template.FuncMap {
	helpers := template.FuncMap{
		"optional":         optional,
		"capitalizedWords": capitalizedWords,
	}
	// Iterating a map is safe here where it usually would not be: the loop only
	// assigns distinct keys into another map, so the merged set is the same
	// whatever order the keys arrive in.
	maps.Copy(helpers, BuiltinReplacements())
	return helpers
}

// capitalizedWords reads a string as words separated by runs of anything
// outside ASCII letters and digits, capitalises each word's first letter,
// and joins them — so `payments-db` becomes `PaymentsDb`.
//
// Call shape from a template:
//
//	{{ capitalizedWords .context.instance_id }}
//
// It forwards to [alphanumeric.CapitalizedWords] and adds nothing. The
// derivation has one home because every template that folds a name has
// to agree with every other about what a given string folds to: a
// Recipe naming a resource from an instance id, and a FeatureRecipe
// naming its own resource from that same id, meet in one rendered
// artifact and would have to be compared by eye otherwise. A second
// implementation would agree on ordinary input and drift on an edge
// case, with nothing failing while the two still agreed.
//
// The fold is lossy, and a template author is the one who has to know
// it: two ids differing only in punctuation fold to one name, so a
// template deriving a name this way carries the collision the fold
// cannot see. Where a name has to tell two authored ids apart, the fold
// is the wrong tool for it.
//
// The forwarder exists so the sentence above about unexported helpers
// stays true: binding the shared function directly would make it
// bindable elsewhere under another name, which is the thing that keeps
// an authored name meaning one thing.
func capitalizedWords(s string) string {
	return alphanumeric.CapitalizedWords(s)
}

// optional returns the value at key in m if the key is present, otherwise
// returns fallback. Distinguishes "key absent" from "key present with
// zero/empty/false value" — the discriminator Sprig's `default` cannot
// make.
//
// Call shape from a template:
//
//	{{ optional .values "pool_size" 10 }}
//
// Returns `.values.pool_size` when the value is authored (including an
// authored zero), and `10` only when the key is absent from the map.
//
// Map-first argument order is essential: a piped value loses access to
// its parent map and cannot perform a presence check. The function takes
// the map and the key as separate arguments so the implementation can
// check key presence directly.
//
// Indexing a nil map returns the zero value with ok=false in Go, so a
// nil m argument is handled gracefully — the fallback is returned.
//
// One bound on the presence check, and it is a property of the template
// rather than of this function: a value template reads an absent key as
// nothing, which it does by supplying an empty value at each absent key
// the template names as a dotted path and reads as a value. So a template
// that reads one key BOTH through this helper and as such a path —
// `{{ optional .infra "port" 8080 }}` alongside `{{ .infra.port }}` —
// finds that key present and empty, and receives the empty value rather
// than the fallback. Reading a key one way per template keeps the two
// answers the same.
func optional(m map[string]any, key string, fallback any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return fallback
}
