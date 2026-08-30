// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import "fmt"

// catalogEntryAbsentCaption is the words this condition reports with, ahead of
// the type they are about. It is the ONE home for that wording: the rule asked
// once per component raises it as the caption of its detail value, and the
// sub-aggregate conformance guard renders it into the message it composes, so
// the two surfaces cannot come to say different things about one condition.
//
// A second copy would agree on the day it was written, and the agreement is what
// would hide the divergence the day either was edited. The failure mode is not
// hypothetical for this condition in particular: one of its two surfaces
// composes a message and the other raises an objection, so a wording held in
// both places has nothing comparing them and no gate that could fail.
//
// It is spelled as the words that PRECEDE the type, because that is the form a
// captioned detail value renders in, and rendering is what makes the two
// surfaces byte-identical rather than merely similar.
//
// It says nothing a reader cannot observe or act on. The condition is not a
// fault in an authored document — a reference nobody can resolve halts earlier,
// at resolution — so the one thing the wording owes its reader is that their own
// files are not where the correction goes.
const catalogEntryAbsentCaption = "the catalog this build reads holds no entry for a component type it had " +
	"already resolved, which nothing you authored can cause — the type is"

// catalogEntryAbsentMessage renders the one wording above for a surface that
// composes its own message text rather than raising an objection.
//
// The quoting matches what a captioned detail value renders to, so the message
// this returns and the message the rule's objection becomes are the same bytes
// for the same type. That equality is asserted rather than assumed; a rendering
// that drifted would otherwise leave one condition reading two ways depending on
// which surface met it.
func catalogEntryAbsentMessage(typeRef string) string {
	return fmt.Sprintf("%s %q", catalogEntryAbsentCaption, typeRef)
}

// guardEntityViewInconsistency classifies a guard's catalog-entity miss
// (`found == false` for a component type) and returns the loud halt for the
// inconsistent case, or nil for the legitimate skip.
//
// At a guard, a missing catalog entity is never a dangling user reference — a
// genuinely unresolvable reference halts at resolution (NotFound) before the
// guards run. So a miss is one of two things, told apart by resolution's own
// Tier-1 discriminator [isRelativePathRef] (the three-tier entity-resolution
// rule: `./…`/`../…` is a local-path file, a bare token is a catalog identity),
// NOT by an ad-hoc string check:
//
//   - a Tier-1 relative-path type (`./…` / `../…`): a genuinely non-catalog
//     local file with no catalog entity — the caller's existing skip is correct,
//     so this returns nil;
//   - a bare-token catalog reference resolution resolved but that is absent from
//     the guard's view: an internal inconsistency between the build's resolved
//     set and the guard's catalog view — this returns an
//     [ErrGuardEntityViewInconsistency] ResolverError for the caller to append.
//
// Every guard that would otherwise skip silently on a missing catalog entity
// routes its miss through this one helper, so the classification and the
// diagnostic are identical across them, and stay identical when the lookup
// backing changes underneath. The callers are the statement of how many there
// are; a number written here beside them would be a second copy that reads as
// current while lagging the set it counts.
//
// The rule asked once per component — componentTypeResolvesToACatalogEntry —
// reaches the same condition without reaching this function, because it is
// expressed as a declared check and raises an objection rather than composing a
// message. It shares what matters: the relative-path discriminator through the
// fact the view publishes for it, and the wording through
// [catalogEntryAbsentCaption]. A parity test holds the two renderings byte-equal,
// which is what keeps a shared wording from becoming two wordings that merely
// agree today.
func guardEntityViewInconsistency(entity, field, typeRef string) *ResolverError {
	if isRelativePathRef(typeRef) {
		return nil
	}
	return &ResolverError{
		Entity:  entity,
		Field:   field,
		Message: catalogEntryAbsentMessage(typeRef),
		Err:     ErrGuardEntityViewInconsistency(),
	}
}

// GuardEntityViewInconsistency is the exported entry point to the catalog-entity
// view-inconsistency classifier, for callers outside this package — the topology
// sub-aggregate conformance guard, which validates flattened sub-aggregate nodes
// that the resolution guards do not reach. Same classification as the in-package
// guards: a Tier-1 relative-path type is a legitimate skip (nil); a bare catalog
// reference absent from the guard's view is the loud
// [ErrGuardEntityViewInconsistency] halt.
func GuardEntityViewInconsistency(entity, field, typeRef string) *ResolverError {
	return guardEntityViewInconsistency(entity, field, typeRef)
}
