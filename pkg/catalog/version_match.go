// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// ErrUnsatisfiableEntityVersion is a recoverable sentinel: a catalog reference
// names a base entity (Component, Feature, Aggregate, Plugin, Recipe or
// FeatureRecipe) that exists in the catalog, but no available version satisfies
// the reference's version constraint — an exact version that is absent, or a
// caret range that matches nothing. It is distinct from a plain miss (the base
// name itself is absent), which the lookups report as not-found. Recoverable so
// the serve daemon can surface it as a diagnostic without aborting; the CLI build
// joins it into a loud halt.
//
// Every kind reaches it through the same selection, so an unsatisfiable version
// pin behaves alike wherever it is written: a version typo on a Manifest recipe
// mapping halts exactly as the same typo on a component type does. The accessors
// whose shape carries no error channel report it as a miss, and which stage turns
// that miss into the loud halt depends on the surface: the resolution-completeness
// pass where the catalog was loaded eagerly, and the Release's dependency-closure
// resolution — naming the URI no catalog entity matches — on a surface that
// resolves that closure first, which stops before the pass runs.
func ErrUnsatisfiableEntityVersion() error { return errUnsatisfiableEntityVersion }

var errUnsatisfiableEntityVersion = cerrors.Register("unsatisfiable-entity-version", cerrors.Meta{
	ShortCode: "E0103",
	Message:   "no catalog version satisfies the referenced version constraint",
	RulePlace: cerrors.RuleNoStageReaches("Resolving a catalog entity's extends parent evaluates the rule, as it matches " +
		"that parent reference's version constraint against the versions the freshly loaded catalog holds " +
		"under the referenced name. The resolution runs over the whole catalog at the load, ahead of the " +
		"build that later resolves references against it, so no declared stage of a compilation holds the " +
		"version candidates the constraint is matched against. The version select hands the condition to " +
		"every caller that asks it to resolve a reference, and the extends resolution is the one that " +
		"reports it: the rest either drop it for a not-found or translate it into a condition of their own."),
})

// selectEntityVersion resolves a caller's catalog-entity reference (callerURI,
// optionally version-pinned) against an index of concrete-version entities,
// returning the index KEY of the entry that best satisfies it under the
// three-form rule the language documents:
//
//   - unversioned ("core/x")    -> the highest available non-prerelease version
//   - exact ("core/x:1.2.0")    -> that exact version
//   - caret ("core/x:^1.2.0")   -> the highest non-prerelease version in range
//
// It solves the INVERSE direction of implementsSatisfies. implementsSatisfies
// takes a concrete caller version and reports whether a caret-form
// `implements` declaration accepts it (the recipe `implements` direction —
// caret-form declaration, concrete caller). selectEntityVersion takes a caller
// CONSTRAINT and finds the highest concrete-version entry that satisfies it
// (concrete-version keys, constraint caller). The two solve opposite directions
// and deliberately do not share an implementation.
//
// Resolution scans the index VALUES via meta(name, version), so it is independent
// of the key format: it serves both the eager name:version-keyed catalog and any
// value-populated *Catalog shell whose keys are bare. A version-less entry (empty
// metadata.version) is the lone fallback when no parseable-semver candidate
// exists, so version-less catalogs keep resolving.
//
// Returns:
//   - (key, true, nil) on a satisfying match;
//   - ("", false, nil) when the base name is absent — a plain miss the lookups
//     report as not-found;
//   - ("", false, ErrUnsatisfiableEntityVersion) when the base exists but no
//     version satisfies an exact/caret constraint — a loud, recoverable halt.
//
// Determinism: the winner is the semver-highest satisfying version; ties (and the
// version-less fallback set) break on the lexically-smallest index key, never on
// map-iteration order.
func selectEntityVersion[V any](index map[string]V, callerURI string, meta func(V) (name, version string)) (string, bool, error) {
	base, verSeg, hasVer := resolver.SplitVersionedURI(callerURI)
	caret := hasVer && strings.HasPrefix(verSeg, "^")

	var constraint *semver.Constraints
	if caret {
		if c, err := semver.NewConstraint(verSeg); err == nil {
			constraint = c
		}
	}

	var cands []versionCandidate
	for k, v := range index {
		n, ver := meta(v)
		if n != base {
			continue
		}
		c := versionCandidate{key: k, raw: ver}
		if ver != "" {
			if pv, err := semver.NewVersion(ver); err == nil {
				c.parsed = pv
			}
		}
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		return "", false, nil // base absent — a plain miss
	}
	// Stable candidate order so tie-breaks and fallbacks are deterministic.
	sort.Slice(cands, func(i, j int) bool { return cands[i].key < cands[j].key })

	satisfies := func(c versionCandidate) bool {
		switch {
		case !hasVer: // unversioned -> any non-prerelease semver
			return c.parsed != nil && c.parsed.Prerelease() == ""
		case caret: // caret -> non-prerelease semver within range
			return c.parsed != nil && c.parsed.Prerelease() == "" && constraint != nil && constraint.Check(c.parsed)
		default: // exact -> literal version match
			return c.raw == verSeg
		}
	}

	best := -1
	for i, c := range cands {
		if !satisfies(c) {
			continue
		}
		if best == -1 || higherCandidate(cands[i], cands[best]) {
			best = i
		}
	}
	if best != -1 {
		return cands[best].key, true, nil
	}

	// No satisfying candidate. For an explicit exact/caret constraint this is a
	// loud, recoverable failure (the base exists but the requested version does
	// not). For an unversioned reference, fall back to the lone version-less /
	// best-effort entry so version-less catalogs still resolve.
	if hasVer {
		return "", false, ErrUnsatisfiableEntityVersion()
	}
	fallback := -1
	for i := range cands {
		if fallback == -1 || higherCandidate(cands[i], cands[fallback]) {
			fallback = i
		}
	}
	return cands[fallback].key, true, nil
}

// selectCapabilityScopedVersion resolves a capability-scoped catalog reference to
// the index KEY of the entity that best satisfies it. It is the resolution ladder
// for the kinds whose identity carries a capability segment
// ("capability/name:version"); every version decision on that ladder delegates to
// selectEntityVersion, so a capability-scoped reference and a plain by-name one
// apply one version rule rather than two.
//
// The reference may name the entity at three precision levels, tried in order:
//
//  1. Capability-qualified exact ("terraform/mariadb-rds:1.0.0") — a direct map
//     get on the identity key.
//  2. Capability-qualified ("terraform/mariadb-rds", "terraform/mariadb-rds:^1.0.0")
//     — the segment before the first "/" is matched against each entity's
//     capability, and the version is selected among that capability's entries.
//  3. Bare ("mariadb-rds", "mariadb-rds:1.0.0") — a scan across every capability,
//     with the version selected WITHIN each one.
//
// The two ambiguity axes are deliberately answered differently, and the
// distinction is the contract rather than an implementation detail. Choosing among
// the VERSIONS one capability offers is a version decision, so it goes to
// selectEntityVersion and yields the highest satisfying version. Choosing among
// the CAPABILITIES that offer one name is not a version decision at all — a bare
// reference two capabilities can both answer carries nothing that selects either,
// so it resolves to neither. Qualify the reference with its capability to select
// one.
//
// Returns the same three-way contract selectEntityVersion does:
//   - (key, true, nil) on a satisfying match;
//   - ("", false, nil) on a miss — the name is absent, or two capabilities offer it;
//   - ("", false, ErrUnsatisfiableEntityVersion) when the entity exists but no
//     available version satisfies an exact or caret pin.
//
// Resolution scans the index VALUES via meta, so like selectEntityVersion it is
// independent of the key format and serves both a fully-keyed catalog and a
// value-populated shell whose keys are bare.
//
// Determinism: capabilities are visited in sorted order and selectEntityVersion
// sorts its own candidates by key, so no outcome depends on map-iteration order.
func selectCapabilityScopedVersion[V any](index map[string]V, uri string, meta func(V) (capability, name, version string)) (string, bool, error) {
	if _, ok := index[uri]; ok {
		return uri, true, nil
	}

	byName := func(v V) (string, string) {
		_, name, version := meta(v)
		return name, version
	}

	if capability, rest, qualified := strings.Cut(uri, "/"); qualified {
		scoped := map[string]V{}
		for k, v := range index {
			if c, _, _ := meta(v); c == capability {
				scoped[k] = v
			}
		}
		if key, ok, err := selectEntityVersion(scoped, rest, byName); ok {
			return key, true, nil
		} else if err != nil {
			// A capability the reference named explicitly, holding the entity but
			// not the pinned version, halts here rather than falling through. The
			// bare reading below could not resolve it anyway: an entity name carries
			// no "/", so the bare candidate would still hold the capability segment
			// and match nothing.
			return "", false, err
		}
	}

	byCapability := map[string]map[string]V{}
	for k, v := range index {
		c, _, _ := meta(v)
		if byCapability[c] == nil {
			byCapability[c] = map[string]V{}
		}
		byCapability[c][k] = v
	}
	capabilities := make([]string, 0, len(byCapability))
	for c := range byCapability {
		capabilities = append(capabilities, c)
	}
	sort.Strings(capabilities)

	var hit string
	var unsatisfiable error
	found := 0
	for _, c := range capabilities {
		key, ok, err := selectEntityVersion(byCapability[c], uri, byName)
		switch {
		case ok:
			hit = key
			found++
		case err != nil && unsatisfiable == nil:
			unsatisfiable = err
		}
	}
	switch {
	case found == 1:
		return hit, true, nil
	case found > 1:
		return "", false, nil
	default:
		return "", false, unsatisfiable
	}
}

// versionCandidate is one catalog entry sharing the looked-up base name, paired
// with its index key and its parsed metadata.version (nil when version-less or
// unparseable). Used by selectEntityVersion to choose the highest-satisfying entry.
type versionCandidate struct {
	key    string
	raw    string
	parsed *semver.Version
}

// higherCandidate reports whether a should outrank b as the selected version: a
// parseable semver outranks a version-less / unparseable one; between two
// parseable versions the greater semver wins; otherwise the lexically-smaller key
// wins so the choice is deterministic.
func higherCandidate(a, b versionCandidate) bool {
	switch {
	case a.parsed != nil && b.parsed == nil:
		return true
	case a.parsed == nil && b.parsed != nil:
		return false
	case a.parsed != nil && b.parsed != nil && !a.parsed.Equal(b.parsed):
		return a.parsed.GreaterThan(b.parsed)
	default:
		return a.key < b.key
	}
}
