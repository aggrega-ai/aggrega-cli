// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// LocalsContextRoot is the context root a template reads a resolved local
// through, and the one spelling every surface compares against.
//
// It is here rather than beside either evaluator because both of them pass it to
// [ClassifyRootKeyReference] and a second spelling would leave one surface
// watching a root the other does not publish. A guard beside the render context
// types holds it to their json tags, so a renamed pillar cannot leave the
// classification watching a root no context defines.
const LocalsContextRoot = "locals"

// ErrLocalsForwardReference is the Loud Guardrail halt raised when a template
// references a locals key the same block declares but has not made readable yet.
// A locals block resolves in author order, and an entry reads only a sibling
// defined above it; the block is never sorted or reordered to make a later
// definition reachable, because emitted output would then follow an inference
// rather than what the author wrote. Without this halt the reference resolves
// through the missing-key policy to an empty string, so a derived hostname or a
// secret name silently loses the segment it was built from. The wrapping
// diagnostic names the referencing entry, the referenced key, the entries
// readable at that point, and the remedy — move the definition above its reader.
// It is deliberately distinct from ErrLocalsUnknownReference: this one is an
// ORDERING fault whose fix is to reorder the block, and reporting both through
// one condition would tell an author who mistyped a key to reorder their block.
// Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrLocalsForwardReference()).
func ErrLocalsForwardReference() error { return errLocalsForwardReference }

// The registered code names neither this package nor the surface that reports
// the condition, and that is deliberate rather than stale. A descriptive code is
// an identity a reader may already be holding off a printed report, so it is
// fixed from the moment it is handed out — the same identity-not-location
// discipline every other published identifier here follows. Re-spelling it to
// track a declaration's move would break a code a reader can still quote.
var errLocalsForwardReference = cerrors.Register("locals-forward-reference", cerrors.Meta{
	ShortCode: "E0042",
	Message:   "locals entry references a sibling defined later in the same block",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrLocalsUnknownReference is the Loud Guardrail halt raised when a template
// references a key the locals block does not declare at all — a mistyped key, or
// one that was renamed or removed. Every surface that reads the pillar raises it:
// a locals entry, a provides template, a Recipe source template, and the
// resolution of a structured source coordinate, which reads the whole block so
// the artifact the coordinate names can be pinned. The locals
// pillar is an open keyed map, so an unknown key under it passes the root check
// and resolves through the missing-key policy to an empty string; the halt is
// what stops that empty value from reaching an emitted artifact. The wrapping
// diagnostic names the referencing entry, the key that does not exist, and the
// keys the block does declare, so the correction is readable from the halt. It is
// deliberately distinct from ErrLocalsForwardReference: this one is a NAMING
// fault whose fix is to correct the key, and the two mistakes have different
// remedies. Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrLocalsUnknownReference()).
func ErrLocalsUnknownReference() error { return errLocalsUnknownReference }

// The code is fixed for the reason stated above the ordering condition.
var errLocalsUnknownReference = cerrors.Register("locals-unknown-reference", cerrors.Meta{
	ShortCode: "E0043",
	Message:   "template references a key the locals block does not declare",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// LocalsForwardRemedy is the ONE home of what an ordering fault tells the author
// to do, and it is a function of the occurrence rather than of the condition.
//
// A self-reference is the same condition — the key is declared and not yet
// readable — and no reordering fixes it, so the remedy that applies to every
// other ordering fault is wrong for exactly this one. That is why the remedy
// cannot live on the registered identity: a sentence registered with it in would
// be wrong at whichever of the two classes it did not describe.
//
// entry is the locals key being evaluated. It is empty on a surface that is not
// itself a locals entry — a provides or source template — where a self-reference
// is impossible, so the reordering remedy is the only one that can apply.
func LocalsForwardRemedy(referenced, entry string) string {
	if referenced == entry {
		return "a locals entry cannot read itself; compute the value from .values and .infra directly"
	}
	return fmt.Sprintf("define %q above %q in the locals block", referenced, entry)
}

// LocalsForwardPosition states WHERE the block declares the key a refused
// ordering reference names, and it is a function of the occurrence for the same
// reason the remedy above is.
//
// A self-reference is this same condition with the key declared AT the
// referencing entry rather than below it, so a sentence asserting "later" is
// false for exactly that class — and a diagnostic that states a position wrongly
// sends the author looking below an entry for a definition that is on it. The
// declared check over this fault reaches the same conclusion by omitting the
// position from its finding; a composed message has room to state it, so it
// states it truthfully instead.
//
// entry is the locals key being evaluated, empty on a surface that is not itself
// a locals entry — where a self-reference is impossible.
func LocalsForwardPosition(referenced, entry string) string {
	if referenced == entry {
		return fmt.Sprintf("the locals block defines %q at this entry", referenced)
	}
	return fmt.Sprintf("the locals block defines %q later", referenced)
}

// LocalsUnknownRemedy is what a naming fault tells the author to do. Unlike the
// ordering remedy it is the same at every occurrence, so it is a constant rather
// than a function — and it is a constant rather than part of the registered
// sentence because the reporting paths place a remedy in a slot of their own and
// a registered one would be stated twice by one of them.
const LocalsUnknownRemedy = "correct the key name"

// LocalsReadablePhrase names the locals readable at the point a refused reference
// was made, as a diagnostic quotes them back.
//
// It is one home because several reporting paths state it — each evaluator's
// composition, and the declared check over the same fault — and a second spelling
// of "nothing is readable yet" would leave them saying different things about one
// state.
func LocalsReadablePhrase(readable []string) string {
	if len(readable) == 0 {
		return "no locals are resolved yet"
	}
	return localsKeyList(readable)
}

// LocalsDeclaredList is the locals a block declares as a bare list, and "none"
// where it declares no locals at all.
//
// The empty case is reachable: a provides or source template may read the pillar
// on an entity that declares no locals block, and naming an empty list there
// would leave the reader with nothing where a set was promised.
func LocalsDeclaredList(declared []string) string {
	if len(declared) == 0 {
		return "none"
	}
	return localsKeyList(declared)
}

// LocalsDeclaredPhrase is that same set as a CLAUSE, for a diagnostic that reads
// as prose rather than as captioned values.
//
// The two spellings differ only in the empty case, and deliberately: a clause
// stating an absence reads as a sentence, while a value slot holding one reads as
// a word. Both derive the populated case from one list, so the set a reader is
// told about cannot differ between them.
func LocalsDeclaredPhrase(declared []string) string {
	if len(declared) == 0 {
		return "no locals are declared"
	}
	return "it declares " + LocalsDeclaredList(declared)
}

// localsKeyList spells a set of locals keys the way a reader meets them in a
// template — under the pillar, dotted — so a diagnostic quoting a set and a
// diagnostic quoting one reference read alike.
func localsKeyList(keys []string) string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, "."+LocalsContextRoot+"."+key)
	}
	return strings.Join(out, ", ")
}
