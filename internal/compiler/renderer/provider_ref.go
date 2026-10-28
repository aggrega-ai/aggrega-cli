// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/Masterminds/semver/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
// It forwards to types.BareCapability, which is the one projection every
// reader of a capability binding takes — this dispatch, and the load-time
// rules that carve one capability out of a shape requirement. Those readers sit
// in packages that cannot import one another, so a local split here would be a
// second copy of a rule they all have to agree on, and the disagreement would
// surface as a legitimate document refused rather than as anything failing.
// The version-carrying resolver (selectEntityVersion in the catalog package)
// owns the inverse direction.
func ProviderEngineKind(providerRef string) string {
	return types.BareCapability(providerRef)
}

// providerIdentity resolves the provider a capability binding renders through:
// the bare engine kind and the version of the provider-rendering plugin the
// binding resolves to, the pair a render stamps on what it emits as the
// aggrega.ai/provider and aggrega.ai/provider-version labels. versionOf is the
// render's resolver; with none, or where it resolves no version, the version is
// empty.
//
// Every render reads its pair through this one function, from the binding of the
// entity that render belongs to — a component's Recipe, or a FeatureRecipe, which
// names its own capability whatever the component it is attached to, or the
// boundary it is attached at, renders through. A FeatureRecipe's render context
// starts as a copy of a component's, and the pair it carried there named that
// component's provider; resolving it again from the FeatureRecipe's own binding
// is what keeps an attachment rendered through another provider from labelling
// its output with the provider that did not render it.
func providerIdentity(capability string, versionOf func(ref string) string) (name, version string) {
	name = ProviderEngineKind(capability)
	if versionOf != nil {
		version = versionOf(capability)
	}
	return name, version
}

// providerVersionGreater orders resolved provider versions for boundary-assembler
// selection within a dispatch group. A populated version outranks the empty
// version — a version-less binding, one for which no provider-rendering plugin
// version was resolved, sorts below every versioned binding. Between two
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
