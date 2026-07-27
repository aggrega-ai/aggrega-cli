// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ProviderEngineKind returns the bare engine kind of a recipe.spec.capability
// binding, stripping any trailing version segment. A binding may carry a
// version pin — "helm:1.0.0" (exact), "helm:^1.0.0" (major-compatible range),
// or "helm" (unversioned) — where the pin selects a specific provider-rendering
// plugin version. Everything downstream of engine selection keys on the engine
// kind alone: the dispatch grouping, the {{ .capability }} path segment, the
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

// providerVersionGreater orders resolved provider versions for boundary-assembler
// selection within a dispatch group. A populated version outranks the empty
// version — a version-less binding (the in-tree fallback route, which carries no
// resolved plugin version) sorts below every versioned binding. Between two
// populated versions the higher semver wins; a version that does not parse as
// semver sorts below one that does, and two unparseable versions order lexically,
// so the ordering is total and deterministic for any input.
//
// It reports whether a ranks strictly above b. Equal versions return false, so a
// caller iterating candidates in processing order and replacing only on a strict
// win keeps the earliest candidate on a tie — the deterministic tie-break.
func providerVersionGreater(a, b string) bool {
	if a == b {
		return false
	}
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	av, aerr := semver.NewVersion(a)
	bv, berr := semver.NewVersion(b)
	switch {
	case aerr == nil && berr == nil:
		if av.Equal(bv) {
			return false
		}
		return av.GreaterThan(bv)
	case aerr == nil:
		return true
	case berr == nil:
		return false
	default:
		return a > b
	}
}
