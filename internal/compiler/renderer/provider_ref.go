// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "strings"

// ProviderEngineKind returns the bare engine kind of a recipe.spec.provider
// binding, stripping any trailing version segment. A binding may carry a
// version pin — "helm:1.0.0" (exact), "helm:^1.0.0" (major-compatible range),
// or "helm" (unversioned) — where the pin selects a specific provider-rendering
// plugin version. Everything downstream of engine selection keys on the engine
// kind alone: the dispatch grouping, the {{ .provider }} path segment, the
// pipeline compatibility check, and codec selection all operate on "helm", not
// on the pinned version. Only engine resolution reads the version.
//
// The kind is the segment before the last colon; a binding with no colon is
// already bare and returns unchanged. The version-carrying resolver
// (selectEntityVersion in the catalog package) owns the inverse direction —
// the catalog package cannot be imported here without an import cycle, so the
// split is kept minimal and local to this dispatch-owning package.
func ProviderEngineKind(providerRef string) string {
	if i := strings.LastIndex(providerRef, ":"); i >= 0 {
		return providerRef[:i]
	}
	return providerRef
}
