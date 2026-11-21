// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ErrUnsatisfiableEntityVersion is a recoverable sentinel: a catalog reference
// names a base entity (Component, Feature, Aggregate, Plugin, Recipe or
// FeatureRecipe) or a library (SchemaLibrary, MappingLibrary, WorkloadLibrary or
// ArtifactLibrary) that exists in the catalog, but no available version satisfies
// the reference's version constraint — an exact version that is absent, a caret
// range that matches nothing, or a library's version floor that no held version
// of the floor's major meets. It is distinct from a plain miss (the base name
// itself is absent), which the lookups report as not-found. Recoverable so the
// serve daemon can surface it as a diagnostic without aborting; the CLI build
// joins it into a loud halt.
//
// Every kind reaches it through the same selection, so an unsatisfiable version
// pin behaves alike wherever it is written: a version typo on a Manifest recipe
// mapping halts exactly as the same typo on a component type does. Where a
// Release's dependency closure is resolved, the walk reports it for an entity
// reference every tier missed whose name the local catalog holds at other
// versions only; for a library reference whose name a catalog directory holds at
// other versions only, asking no other tier, or that every tier missed while the
// pinned content cache holds its name at other versions only, naming the versions
// held; and an extends link whose parent the catalog holds at other versions only
// reports it too, both in the extends resolution over the closure and in the
// ancestry reading the mapping-consistency gate performs. The accessors whose
// shape carries no error channel report it as a miss, and where the catalog was
// loaded eagerly the resolution-completeness pass turns that miss into the loud
// halt.
func ErrUnsatisfiableEntityVersion() error { return errUnsatisfiableEntityVersion }

var errUnsatisfiableEntityVersion = cerrors.Register("unsatisfiable-entity-version", cerrors.Meta{
	ShortCode: "E0103",
	Message:   "no catalog version satisfies the referenced version constraint",
	RulePlace: cerrors.RuleNoStageReaches("The version select evaluates the rule, as it matches a reference's version " +
		"constraint against the versions a loaded catalog holds under the referenced name. It is reported " +
		"where references are resolved against that catalog ahead of any compilation: the extends resolution " +
		"over the whole catalog at the load, and, for a Release, the walk over its dependency closure, the " +
		"extends resolution over that closure and the extends ancestry the mapping-consistency gate reads. " +
		"None of those moments is a declared stage of a compilation, so no declared stage holds the version " +
		"candidates the constraint is matched against. Every other caller of the select either drops the " +
		"condition for a not-found or translates it into a condition of its own."),
})

// ErrConflictingVersionConstraints is a recoverable sentinel: the versions a
// catalog holds under a component or feature type's name include some the
// reference alone admits, but none that also satisfies every other constraint
// declared on the instance. It is distinct from E0103, where the reference
// itself matches no held version, and from a plain miss, where the name is
// absent. The version select raises it when asked with an admission predicate
// that carries those constraints.
func ErrConflictingVersionConstraints() error { return errConflictingVersionConstraints }

var errConflictingVersionConstraints = cerrors.Register("conflicting-version-constraints", cerrors.Meta{
	ShortCode: "E0382",
	Message:   "no version of a component or feature type satisfies every constraint declared on the instance",
	RulePlace: cerrors.RuleNoStageReaches("The version select evaluates the rule, asked with the constraints a Release " +
		"declares on an instance: its Aggregate's type, the Manifest key that maps it and the mapped Recipe's " +
		"`implements`. It is reported where a Release's dependency closure is resolved for the lockfile and where " +
		"the build settles each instance's version before compiling. Resolving the closure is no declared stage " +
		"of a compilation, so no declared stage holds the version candidates the constraints are matched against."),
})

// selectEntityVersionWhere resolves a caller's catalog-entity reference (callerURI,
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
// The split and the caret range come from types.ParseVersionPin, the one home of
// the pin grammar, so this select parses no pin of its own.
//
// Returns:
//   - (key, true, nil) on a satisfying match;
//   - ("", false, nil) when the base name is absent — a plain miss the lookups
//     report as not-found;
//   - ("", false, ErrUnsatisfiableEntityVersion) when the base exists but no
//     version satisfies a well-formed exact/caret constraint — a loud,
//     recoverable halt;
//   - ("", false, the malformed-pin fault) when the base exists, nothing
//     satisfies, and the pin is none of the forms a pin takes. The fault is
//     reported in place of the unsatisfiable condition because a malformed pin
//     names no version, and it is judged only here, after the scan found no
//     match, so a reference that resolves today still resolves.
//
// Determinism: the winner is the semver-highest satisfying version; ties (and the
// version-less fallback set) break on the lexically-smallest index key, never on
// map-iteration order.
//
// The admission predicate narrows the selection: a candidate is selected only when the
// reference's form is satisfied and admits(version) is true, and the winner is
// the highest such version. The predicate never widens what the reference
// admits: a pre-release is selected only by an exact reference, whatever the
// predicate says. A nil predicate admits every version. Where some held version
// satisfies the reference alone and the predicate admits none of them, the result is
// ("", false, ErrConflictingVersionConstraints); an absent base, an unsatisfiable
// reference and a malformed pin report as above, whatever the predicate. The
// version-less fallback of an unversioned reference applies to the versions the
// predicate admits.
func selectEntityVersionWhere[V any](index map[string]V, callerURI string, meta func(V) (name, version string), admits func(version string) bool) (string, bool, error) {
	pin := types.ParseVersionPin(callerURI)
	base := pin.Base()
	_, hasVer := pin.Pin()
	caret := pin.IsCaret()
	constraint := pin.Range()

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
			return pinNamesExactly(pin, c.raw)
		}
	}

	admitted := func(c versionCandidate) bool { return admits == nil || admits(c.raw) }

	best := -1
	satisfiedAlone := false
	for i, c := range cands {
		if !satisfies(c) {
			continue
		}
		satisfiedAlone = true
		if !admitted(c) {
			continue
		}
		if best == -1 || higherCandidate(cands[i], cands[best]) {
			best = i
		}
	}
	if best != -1 {
		return cands[best].key, true, nil
	}
	if satisfiedAlone {
		return "", false, ErrConflictingVersionConstraints()
	}

	// No satisfying candidate. For an explicit exact/caret constraint this is a
	// loud, recoverable failure (the base exists but the requested version does
	// not). For an unversioned reference, fall back to the lone version-less /
	// best-effort entry so version-less catalogs still resolve.
	if hasVer {
		if fault := pin.Fault(); fault != nil {
			return "", false, fault
		}
		return "", false, ErrUnsatisfiableEntityVersion()
	}
	fallback := -1
	for i := range cands {
		if !admitted(cands[i]) {
			continue
		}
		if fallback == -1 || higherCandidate(cands[i], cands[fallback]) {
			fallback = i
		}
	}
	if fallback == -1 {
		return "", false, ErrConflictingVersionConstraints()
	}
	return cands[fallback].key, true, nil
}

// selectEntityVersion is selectEntityVersionWhere with no admission predicate;
// the three-form rule it states is that function's.
func selectEntityVersion[V any](index map[string]V, callerURI string, meta func(V) (name, version string)) (string, bool, error) {
	return selectEntityVersionWhere(index, callerURI, meta, nil)
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
// The capability an entity is scoped by is the one its identity carries: the
// capability meta reports with any version pin removed, read through
// [types.BareCapability] exactly as the identity key reads it. A pin selects the
// plugin that renders the entity and is no part of its name, so a reference
// naming the capability bare reaches a pinned entity, and a reference naming
// the capability with a pin reaches none.
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
	scopeOf := func(v V) string {
		c, _, _ := meta(v)
		return types.BareCapability(c)
	}

	if capability, rest, qualified := strings.Cut(uri, "/"); qualified {
		scoped := map[string]V{}
		for k, v := range index {
			if scopeOf(v) == capability {
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
		c := scopeOf(v)
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

// pinNamesExactly reports whether pin names exactly version: an exact pin — a
// version, not a range — whose version is version as written. It is the exact
// form of the version select's rule, and its one statement, so a caller asking
// whether a pin selects one particular entity asks it here.
func pinNamesExactly(pin types.VersionPin, version string) bool {
	pinned, hasVersion := pin.Pin()
	return hasVersion && !pin.IsCaret() && pinned == version
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

// selectLibraryVersion resolves a library reference (SchemaLibrary,
// MappingLibrary, WorkloadLibrary or ArtifactLibrary) against an index of the
// library versions a catalog holds, returning the index KEY of the pick. The
// reference's version is a floor, not an exact pin or a range:
//
//   - bare ("Lib")        -> the highest non-prerelease version, as
//     selectEntityVersion picks it;
//   - floor ("Lib:1.1.0") -> the highest held release at or above the floor that
//     shares the floor's major component, so a floor reads as `>=1.1.0` within
//     its major. A floor may omit trailing components ("Lib:1.1" reads as
//     1.1.0). Versions of major 0 share one major, so 0.5.1 is satisfied by
//     0.6.0. A prerelease is selected only when the floor names it literally
//     and no release of the major at or above the floor is held, as a component
//     reference selects a prerelease only by naming it exactly.
//
// A floor is selected through selectEntityVersionWhere, asked for the bare name
// with the floor's range, releases only, as its admission predicate, so a bare
// reference and a floor choose "highest" by the one rule.
//
// Returns the same three-way contract selectEntityVersion does: (key, true, nil)
// on a pick, ("", false, nil) when the library name is held at no version, and
// ("", false, ErrUnsatisfiableEntityVersion) when the name is held but no version
// satisfies the floor — reported as an *unsatisfiedLibraryFloor, which names the
// versions held. A floor that is no version at all reports the malformed-pin fault
// types.ParseVersionPin states.
//
// A caret reference names a range rather than a floor and is selected by
// selectEntityVersion.
//
// Determinism: candidates are ordered by semver and then by index key, never by
// map-iteration order. Versions equal in semver (differing in build metadata
// only) select the smaller index key, for a floor as for a bare reference, so
// "Lib" and "Lib:1.0.0" read the same copy of 1.0.0.
func selectLibraryVersion[V any](index map[string]V, ref string, meta func(V) (name, version string)) (string, bool, error) {
	pin := types.ParseVersionPin(ref)
	floorText, hasFloor := pin.Pin()
	if !hasFloor || pin.IsCaret() {
		return selectEntityVersion(index, ref, meta)
	}

	floor, floorErr := semver.NewVersion(floorText)
	base := pin.Base()
	var held []versionCandidate
	for k, v := range index {
		n, ver := meta(v)
		if n != base {
			continue
		}
		c := versionCandidate{key: k, raw: ver}
		if parsed, err := semver.NewVersion(ver); err == nil {
			c.parsed = parsed
		}
		held = append(held, c)
	}
	if len(held) == 0 {
		return "", false, nil
	}
	if floorErr != nil {
		if fault := pin.Fault(); fault != nil {
			return "", false, fault
		}
		return "", false, newUnsatisfiedLibraryFloor(base, held)
	}

	admitsFloor := func(version string) bool {
		v, err := semver.NewVersion(version)
		return err == nil && v.Prerelease() == "" && v.Major() == floor.Major() && !v.LessThan(floor)
	}
	key, ok, err := selectEntityVersionWhere(index, base, meta, admitsFloor)
	if ok {
		return key, true, nil
	}
	if err != nil && !errors.Is(err, ErrConflictingVersionConstraints()) {
		return "", false, err
	}
	// A floor that names a prerelease selects the copy that names it, when no
	// release of the major is held at or above the floor.
	if floor.Prerelease() != "" {
		if key, ok, err := selectEntityVersionWhere(index, ref, meta, nil); ok && err == nil {
			return key, true, nil
		}
	}
	return "", false, newUnsatisfiedLibraryFloor(base, held)
}

// unsatisfiedLibraryFloor is the library selection's account of a floor no held
// version meets. It is the unsatisfiable-version condition — it unwraps to
// ErrUnsatisfiableEntityVersion and reads as it — carrying the versions the index
// holds under the library's name, so a report of the condition can say what is
// held as well as what was asked for.
type unsatisfiedLibraryFloor struct {
	// held is each held version's identity, written `<Name>:<version>`, in semver
	// order.
	held []string
	// keys is the index key of each held version, in the same order.
	keys []string
}

// newUnsatisfiedLibraryFloor is the condition for a floor on name that none of
// the held candidates meets, the candidates put in semver order.
func newUnsatisfiedLibraryFloor(name string, held []versionCandidate) *unsatisfiedLibraryFloor {
	sort.Slice(held, func(i, j int) bool { return higherCandidate(held[j], held[i]) })
	e := &unsatisfiedLibraryFloor{}
	for _, c := range held {
		e.held = append(e.held, name+":"+c.raw)
		e.keys = append(e.keys, c.key)
	}
	return e
}

func (e *unsatisfiedLibraryFloor) Error() string { return ErrUnsatisfiableEntityVersion().Error() }

func (e *unsatisfiedLibraryFloor) Unwrap() error { return ErrUnsatisfiableEntityVersion() }

// HeldIdentities returns the identities held under the library's name, written
// `<Name>:<version>`, in semver order.
func (e *unsatisfiedLibraryFloor) HeldIdentities() []string { return e.held }
