// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// matchImplements resolves a caller's component / feature type URI against a
// catalog index keyed by the entity's spec.implements declaration. The lookup
// honours the three-form precedence the language documents:
//
//  1. Exact: literal-string match against the index key.
//  2. Caret-range: the caller's URI carries a parseable version; the helper
//     iterates the index for keys of the form "<base>:^<version>" sharing
//     the same base, parses each as a SemVer constraint, and returns the
//     entry whose constraint is satisfied by the caller's version. When
//     multiple caret-form entries match, the one with the highest baseline
//     version wins.
//  3. Unversioned fallback: strip the caller's version segment and try
//     "<base>" against the index.
//
// The function is generic over the index value type so RecipeLookup and
// FeatureRecipeLookup share one implementation. Each map iteration
// snapshots the keys via Go's range; the helper allocates per call but
// the catalog map is small (the standard library publishes <100 Recipes
// today), so the cost is negligible.
//
// On miss the zero value of V is returned alongside false.
func matchImplements[V any](index map[string]V, callerURI string) (V, bool) {
	if v, ok := index[callerURI]; ok {
		return v, true
	}

	base, version, hasVersion := splitVersionedURI(callerURI)
	if !hasVersion {
		var zero V
		return zero, false
	}

	callerVer, err := semver.NewVersion(version)
	if err == nil {
		var bestKey string
		var bestBaseline *semver.Version
		for key := range index {
			kBase, kVersion, kHasVersion := splitVersionedURI(key)
			if !kHasVersion || kBase != base || !strings.HasPrefix(kVersion, "^") {
				continue
			}
			constraint, err := semver.NewConstraint(kVersion)
			if err != nil {
				continue
			}
			if !constraint.Check(callerVer) {
				continue
			}
			baselineStr := strings.TrimPrefix(kVersion, "^")
			baselineVer, err := semver.NewVersion(baselineStr)
			if err != nil {
				continue
			}
			if bestBaseline == nil || baselineVer.GreaterThan(bestBaseline) {
				bestBaseline = baselineVer
				bestKey = key
			}
		}
		if bestKey != "" {
			return index[bestKey], true
		}
	}

	if v, ok := index[base]; ok {
		return v, true
	}

	var zero V
	return zero, false
}

// splitVersionedURI splits a URI like "core/api-server:1.0.0" into its base
// (before the last colon) and version (after the last colon) segments. The
// last colon is the separator because version segments do not contain colons
// while base segments may carry registry-style separators ("hub.aggrega.ai/component/core/api-server:1.0.0").
//
// Returns the bare URI as base, "" as version, and false as hasVersion when
// the URI carries no colon.
func splitVersionedURI(uri string) (base, version string, hasVersion bool) {
	idx := strings.LastIndex(uri, ":")
	if idx < 0 {
		return uri, "", false
	}
	return uri[:idx], uri[idx+1:], true
}
