// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// mappingEntryFinding is the frame a Manifest mapping finding carries beneath its
// envelope, naming the mapping entry the finding is about: the tier, the axis and
// the key the entry is written under.
//
// The entry travels as a frame of the chain rather than as part of the position
// the envelope states, because every field of that position is printed: a line or
// a column changes the file reference a terminal prints, and a locator is appended
// to it. The position a terminal prints for a mapping finding names the Manifest
// alone, and a reader there reads the entry off the message, which quotes it. An
// editor holding the Manifest's text needs the entry itself, to draw the finding on
// the entry's own line, and reads it through [ManifestMappingEntryOf].
//
// It adds no text and no identity of its own. Its Error is the text of the error
// it wraps, byte for byte, so a rendering that prints the text of a frame which is
// not an envelope prints the same bytes whether it meets this frame or the one
// beneath it; and errors.Is and errors.As reach through it to the condition or the
// cause beneath, so the codes a finding reports are unchanged.
//
// It sits beneath the envelope rather than above it so that the finding is still
// the envelope itself: a caller holding a mapping finding holds a
// *cerrors.CompilerError, as it did before the frame existed.
//
// It is a second statement of where a finding points — the key half of it, since
// the document half is the envelope's file, read like any other finding's — and
// it is carried rather than intended. The one home of that statement is the
// annotation [cerrors.Locate] makes, read back by [cerrors.LocatedAt] and, for the
// surfaces that place a finding, by the one reading of where a finding points
// (compilerops.FindingLocation). That annotation states a key as a dotted locator, and it cannot name a key that
// itself contains a dot today: a mapping key is a type reference whose version
// carries dots, so the locator assembled for such an entry names only the block
// enclosing it, and a dotted path spelled with the key is split at the version and
// stops at that block too. This frame therefore carries the entry as the three
// parts that name it, and it exists until that annotation gains a form stating a
// key as its path segments; at that point the mapping findings are annotated
// through it, this frame and [ManifestMappingEntryOf] are deleted, and the one
// reading places them like every other finding.
type mappingEntryFinding struct {
	entry constraint.ManifestRecipeMapping
	err   error
}

// Error returns the wrapped error's own text.
func (f *mappingEntryFinding) Error() string { return f.err.Error() }

// Unwrap returns the wrapped error, so the chain reads through this frame.
func (f *mappingEntryFinding) Unwrap() error { return f.err }

// atMappingEntry returns err carried as a finding about entry, for the envelope a
// mapping finding is built as to wrap.
func atMappingEntry(entry constraint.ManifestRecipeMapping, err error) error {
	return &mappingEntryFinding{entry: entry, err: err}
}

// ManifestMappingEntryOf returns the Manifest mapping entry a finding of the
// mapping-table consistency gate is about — the tier, the axis and the key it is
// written under, in the author's own spelling — and whether err's chain names one.
//
// A finding names its entry wherever the Manifest declares the entry it judged. One
// that names none — a type whose extends chain could not be walked, asked about by
// no entry the Manifest declares — is still a finding about the Manifest its
// position names; it is only not about a single entry of it.
//
// It exists for a surface that holds the Manifest's text and places a finding on
// the line its entry is written at. The terminal reads the entry off the finding's
// message instead, which quotes it, so nothing printed depends on this.
//
// It is a second reading of where a finding points, beside [cerrors.LocatedAt] and
// the one reading built on it (compilerops.FindingLocation), carried for the reason
// the frame it reads is: that annotation cannot state a key containing a dot today.
// It is deleted together with the frame once the annotation can.
func ManifestMappingEntryOf(err error) (constraint.ManifestRecipeMapping, bool) {
	var f *mappingEntryFinding
	if errors.As(err, &f) && f != nil {
		return f.entry, true
	}
	return constraint.ManifestRecipeMapping{}, false
}
