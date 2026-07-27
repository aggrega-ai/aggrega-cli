// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
)

// aggregaFuncMap returns the template-function set used by Aggrega's
// renderer surfaces: Sprig v3's FuncMap augmented with Aggrega-specific
// helpers. A fresh map is returned on each call so callers can add
// per-site functions (e.g., the assertion evaluator's `fail` override)
// without mutating other callers' maps.
//
// Currently the only Aggrega-specific helper is [optional]; the helper
// surface grows additively as catalog-authoring needs surface.
func aggregaFuncMap() template.FuncMap {
	funcs := sprig.FuncMap()
	funcs["optional"] = optional
	return funcs
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
