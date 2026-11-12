// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package errors

import stderrors "errors"

// occurrence is an occurrence of a registered condition whose whole text its raise
// site composed. It is reached only through [Occurrence].
type occurrence struct {
	condition *sealed
	text      string
}

// Error returns the text the raise site composed, and nothing beside it.
func (o *occurrence) Error() string { return o.text }

// Is reports whether target is the condition this is an occurrence of.
func (o *occurrence) Is(target error) bool { return stderrors.Is(o.condition, target) }

// As reads the condition this is an occurrence of into target, so a read of the
// condition's identifiers off a chain — [CodeOf], [ShortCodeOf], [MessageOf] —
// reaches it.
func (o *occurrence) As(target any) bool { return stderrors.As(o.condition, target) }

// Occurrence returns an error naming condition whose text is text, exactly as the
// raise site composed it: the condition is matchable with errors.Is, its codes are
// read off a chain carrying it, and its registered sentence appears nowhere in
// what the error renders.
//
// It is for a raise site that composes a finding's whole text itself and whose
// finding travels inside a larger report — a frame that wraps it supplies the
// category and the scope, and a surface that prints it puts the condition's short
// code in the head. [Raise] builds a different thing: an envelope carrying its own
// category and source pointer, which the terminal renders as a diagnostic of its
// own.
//
// # Why the condition is reached through Is and As, and not through Unwrap
//
// A walk that renders a chain on one line — [CauseChain], and [HeadedCauseChain]
// beneath it — follows Unwrap, and appends every frame whose text the line does not
// already carry. A finding unwrapping to its condition would therefore end, on
// every such surface, with the condition's registered sentence: text the raise
// site never wrote, added after the text it did. errors.Is and errors.As consult a
// value's own Is and As methods as well as Unwrap, and they are how a condition is
// matched and how its identifiers are read, so reaching the condition through those
// two keeps it matchable and its codes readable while the rendered text stays the
// raise site's own. Do not add an Unwrap to it: that is the change that puts the
// sentence back on the line.
//
// condition is the value a [Register] call returned. A nil condition, or one no
// Register call produced, panics — the refusal [Raise] makes, for the same reasons.
func Occurrence(condition Identity, text string) error {
	return &occurrence{condition: registeredCondition("Occurrence", condition), text: text}
}
