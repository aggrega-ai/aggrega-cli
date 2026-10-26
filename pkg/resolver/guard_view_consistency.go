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
//
// Because no author can produce the condition, the wording names no author and
// asks for a report instead: a reader who cannot correct it can still carry it
// to someone who can, and the type the two views disagree on is the one fact
// that makes the divergence reproducible. It follows the wording every other
// compiler-fault halt uses — "internal error", then the fault named as the
// compiler's rather than the document's, then what there is nothing to correct
// in — so a reader meeting two of them reads one form twice rather than two
// forms once.
const catalogEntryAbsentCaption = "internal error: this build resolved a component type that the catalog it " +
	"reads then held no entry for. This is a fault in the compiler rather than in an authored document, so " +
	"there is nothing to correct in the Aggregate, the Release or the catalog. Please report it, naming the " +
	"command that produced this build and the type the views disagree on:"

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

// guardEntityViewInconsistency builds the loud halt a guard reports when the
// catalog entity behind a component's type is absent from its view
// (`found == false` for a component type).
//
// At a guard, a missing catalog entity is never a dangling user reference: every
// surface that compiles a Release resolves the references it reaches first — the
// ones the Release's own Aggregate makes and the ones every module makes — and
// refuses one no catalog holds there, as the author's reference to nothing. That
// holds however the type is written. A type written with a `./` or `../` prefix
// names no catalog entity, because a type is never read as a file, and that
// resolution refuses it like any other reference nothing holds. So a miss here is
// an internal inconsistency between the build's resolved set and the guard's
// catalog view, whatever the type's spelling, and this returns an
// [ErrGuardEntityViewInconsistency] ResolverError for the caller to append.
//
// Every guard that would otherwise skip silently on a missing catalog entity
// routes its miss through this one helper, so the diagnostic is identical across
// them, and stays identical when the lookup backing changes underneath. The
// callers are the statement of how many there are; a number written here beside
// them would be a second copy that reads as current while lagging the set it
// counts.
//
// The rule asked once per component — componentTypeResolvesToACatalogEntry —
// reaches the same condition without reaching this function, because it is
// expressed as a declared check and raises an objection rather than composing a
// message. It shares what matters, the wording, through
// [catalogEntryAbsentCaption]. A parity test holds the two renderings byte-equal,
// which is what keeps a shared wording from becoming two wordings that merely
// agree today.
//
// # Why the finding names no document and no field
//
// It takes the type and nothing else, and the two subject fields it leaves empty
// are the point rather than an omission. Every other finding carries the document
// its reader must edit and the key inside it; this condition has no such document,
// so a subject here would be a coordinate pointing into a file nobody has to open.
// A reader sent to an authoring key for a fault their file cannot cause reads the
// key as the accusation and the wording as boilerplate around it.
//
// Leaving both empty is also what keeps the condition out of an authoring
// domain it does not belong to. A finding that names no subject is filed under
// no category, so the printed head names the condition and says nothing about
// which surface to fix — which is the only true answer for a fault that is
// nobody's. The wording carries the rest: it says plainly that there is nothing
// to correct in the Aggregate, the Release or the catalog, and asks for a
// report instead.
func guardEntityViewInconsistency(typeRef string) *ResolverError {
	return &ResolverError{
		Message: catalogEntryAbsentMessage(typeRef),
		Err:     ErrGuardEntityViewInconsistency(),
	}
}

// GuardEntityViewInconsistency is the exported entry point to the catalog-entity
// view-inconsistency halt, for callers outside this package — the topology
// sub-aggregate conformance guard, which validates flattened sub-aggregate nodes
// that the resolution guards do not reach. It builds the loud
// [ErrGuardEntityViewInconsistency] halt the in-package guards report for a
// component type absent from the guard's view, whatever the type's spelling.
//
// It takes the type alone. A caller holding the document it was walking and the
// node it was at has nothing to pass them to, which is deliberate: the finding
// names no authoring subject, and a parameter for one would invite a caller to
// supply the coordinate this condition exists not to name.
func GuardEntityViewInconsistency(typeRef string) *ResolverError {
	return guardEntityViewInconsistency(typeRef)
}
