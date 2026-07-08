// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ErrUnsatisfiableEntityVersion is a recoverable sentinel: a catalog reference
// names a base entity (Component / Feature / Aggregate) that exists in
// the catalog, but no available version satisfies the reference's version
// constraint — an exact version that is absent, or a caret range that matches
// nothing. It is distinct from a plain miss (the base name itself is absent),
// which the lookups report as not-found. Recoverable so the serve daemon can
// surface it as a diagnostic without aborting; the CLI build joins it into a loud
// halt.
var ErrUnsatisfiableEntityVersion = errors.New("no catalog version satisfies the referenced version constraint")

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

// splitPluginRefVersion splits a provider-plugin OCI identity like
// "oci://host/path/helm:1.0.0" into its digest-free ref and version-pin
// segments. Unlike splitVersionedURI it is safe for a scheme-bearing OCI ref:
// the version separator is a colon AFTER the last path slash, so neither the
// "oci://" scheme colon nor a host:port colon is mistaken for a version pin.
// Returns the whole ref as base, "" as version, and false as hasVersion when no
// post-slash colon is present.
func splitPluginRefVersion(ref string) (base, version string, hasVersion bool) {
	off := 0
	if slash := strings.LastIndex(ref, "/"); slash >= 0 {
		off = slash + 1
	}
	tail := ref[off:]
	idx := strings.LastIndex(tail, ":")
	if idx < 0 {
		return ref, "", false
	}
	return ref[:off+idx], ref[off+idx+1:], true
}

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
	base, verSeg, hasVer := splitVersionedURI(callerURI)
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
		return "", false, ErrUnsatisfiableEntityVersion
	}
	fallback := -1
	for i := range cands {
		if fallback == -1 || higherCandidate(cands[i], cands[fallback]) {
			fallback = i
		}
	}
	return cands[fallback].key, true, nil
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
