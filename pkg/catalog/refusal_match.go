// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "strings"

// SoleRefusalFor returns the refused catalog file whose own declared identity
// the reference names, and reports whether exactly one file declared it.
//
// It answers the question a resolution failure cannot answer for itself: a file
// the tolerant load refused never enters the catalog, so a reference to it fails
// as an unresolvable reference, and only an identity match can say which refusal
// explains that failure. skipped is the record the tolerant load returned; uri
// and kindHint are the reference the resolution failed on and the kind hint it
// was collected with.
//
// The match is an identity match, not a guess. A catalog indexes an entity under
// the identity the document declares for itself — its kind, its name, its
// version, and for a recipe the capability that scopes it — so a refused file
// declaring the identity a reference asks for IS the file that reference would
// have resolved to. Every part of the identity the reference states must agree; a
// part the reference leaves open (a bare name with no version, a recipe
// reference with no provider segment) is left unconstrained, which makes a
// second refused file of the same name a second candidate rather than a
// tie-break.
//
// EXACT OR ABSENT. The answer is a single file or nothing: no candidate and
// several candidates both report false, and a caller reports the failure exactly
// as it would with no refusal record at all. Reporting a wrong file as the cause
// would be worse than reporting no cause, because a reader acts on it — so where
// the record cannot single one out, it says so by declining.
//
// Two consequences follow from that, and both are the intended answer rather
// than a gap. A document too malformed for its header to be read declares no
// identity, so it can never be matched — it stays reported by path, which is
// still enough to open it. And a reference stating a version RANGE rather than a
// version matches nothing, because a range names no single document; the caller
// falls back to reporting every refusal.
func SoleRefusalFor(skipped []SkippedEntity, uri, kindHint string) (SkippedEntity, bool) {
	if len(skipped) == 0 || uri == "" {
		return SkippedEntity{}, false
	}
	// EntityKindForHint is the single home for the hint-to-kind normalization.
	// A hint it does not map — or none at all — leaves the reference's kind
	// unknown, and an unknown kind cannot make an exact match.
	kind := EntityKindForHint(kindHint)
	if kind == "" {
		return SkippedEntity{}, false
	}
	base, version, hasVersion := SplitVersionedURI(uri)
	provider, name := "", base
	if kind == "Recipe" || kind == "FeatureRecipe" {
		// A recipe reference may be provider-qualified. The provider is the
		// leading segment, split the way the identity key composes it, and the
		// naming convention keeps it out of metadata.name.
		if i := strings.IndexByte(base, '/'); i > 0 {
			provider, name = base[:i], base[i+1:]
		}
	}
	// A reference with no name left names no document, so nothing can match it.
	// Requiring a name here is also what keeps a refusal whose header yielded no
	// identity out of the candidate set, since its empty name can then never
	// equal the reference's.
	if name == "" {
		return SkippedEntity{}, false
	}
	var match SkippedEntity
	candidates := 0
	for _, s := range skipped {
		if s.Kind != kind || s.Name != name {
			continue
		}
		if hasVersion && s.Version != version {
			continue
		}
		if provider != "" && s.Capability != provider {
			continue
		}
		candidates++
		match = s
	}
	if candidates != 1 {
		return SkippedEntity{}, false
	}
	return match, true
}
