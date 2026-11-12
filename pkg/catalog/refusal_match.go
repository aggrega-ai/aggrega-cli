// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
// A kind hint and a record's kind are two vocabularies, and the difference is
// deliberate rather than an inconsistency to reconcile. A hint is the lowercase
// note a closure walk keeps for itself about where a reference was collected; a
// record's kind is the capitalised kind the refused document declares at its own
// head, which is what its author typed. EntityKindForHint is the single home
// that translates the first into the second, so a caller passes the hint it
// holds and lets the translation happen here. Passing a declared kind instead
// leaves the reference's kind unmapped, and the answer is then a no-match on
// every input rather than an error.
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
	ref := parseReference(uri, kind)
	// A reference with no name left names no document, so nothing can match it.
	// Requiring a name here is also what keeps a refusal whose header yielded no
	// identity out of the candidate set, since its empty name can then never
	// equal the reference's.
	if ref.name == "" {
		return SkippedEntity{}, false
	}
	var match SkippedEntity
	candidates := 0
	for _, s := range skipped {
		// A document of a kind no catalog holds declares no identity, whatever
		// kind it names, so it is never the refusal a report leads with.
		if s.KindNotHeld() || s.Kind != kind || !ref.namedBy(s) {
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

// UnheldKindDocumentsFor returns the documents in skipped that declare a kind a
// catalog does not hold under the name the reference uri asks for, in the
// record's order, or nil when none does.
//
// It answers the question SoleRefusalFor declines for such a document. The kind
// such a document declares is not one a reference can name, so it is matched by
// what it declares beside the kind — its name, its version where the reference
// states one, and for a recipe reference the provider that scopes it — whatever
// kind the reference was collected as. kindHint is read only to split the
// reference the way its kind composes it.
//
// Every match is returned rather than a sole one, because a caller lists these
// beneath the failure it explains and never leads with one: a reader weighs a
// list, and a second file of the same name is a second thing to open rather than
// a reason to name neither.
func UnheldKindDocumentsFor(skipped []SkippedEntity, uri, kindHint string) []SkippedEntity {
	if len(skipped) == 0 || uri == "" {
		return nil
	}
	ref := parseReference(uri, EntityKindForHint(kindHint))
	if ref.name == "" {
		return nil
	}
	var out []SkippedEntity
	for _, s := range skipped {
		if s.KindNotHeld() && ref.namedBy(s) {
			out = append(out, s)
		}
	}
	return out
}

// referenceParts is a reference split into the parts of an identity it states.
// A part it leaves open — a version, a provider — is empty and unconstrained.
type referenceParts struct {
	provider   string
	name       string
	version    string
	hasVersion bool
}

// parseReference splits uri into the identity parts it states for a reference
// of the given kind.
func parseReference(uri, kind string) referenceParts {
	base, version, hasVersion := resolver.SplitVersionedURI(uri)
	ref := referenceParts{name: base, version: version, hasVersion: hasVersion}
	if kind == "Recipe" || kind == "FeatureRecipe" {
		// A recipe reference may be provider-qualified. The provider is the
		// leading segment, split the way the identity key composes it, and the
		// naming convention keeps it out of metadata.name.
		if i := strings.IndexByte(base, '/'); i > 0 {
			ref.provider, ref.name = base[:i], base[i+1:]
		}
	}
	return ref
}

// namedBy reports whether every part of the identity the reference states agrees
// with what s declares, leaving the kind to the caller.
func (r referenceParts) namedBy(s SkippedEntity) bool {
	if s.Name != r.name {
		return false
	}
	if r.hasVersion && s.Version != r.version {
		return false
	}
	// The capability a recipe's identity carries is its binding with any
	// version pin removed, which is how the catalog keys it.
	if r.provider != "" && types.BareCapability(s.Capability) != r.provider {
		return false
	}
	return true
}
