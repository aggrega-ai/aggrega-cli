// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// Severity is what a raised objection does to the build: halt it, or report it
// and let the build finish. A check declares its severity, so the consequence
// of a violation is part of the declaration rather than a property of which
// collection the check's output happens to be appended to.
//
// The two values are the two outcomes this compiler distinguishes. A rule that
// recorded a finding without affecting the build outcome would be a policy
// verdict at the plugin boundary rather than a check here, and such a verdict
// carries that boundary's own vocabulary.
//
// This is deliberately its own vocabulary rather than a reuse of one the
// compiler already carries. The others each belong to a boundary — an
// artifact's deprecation marker, a tolerant lockfile load, a policy guest's
// verdict on the wire, a plugin diagnostic whose severity is an open string so
// that each socket keeps its own — and they differ from one another because the
// boundaries do. A check sits at no boundary: it runs in-process over the
// compilation's own product and hands back an objection the framework converts
// for whichever surface reads it. Binding a check's declaration to a wire
// vocabulary would couple it to a surface it never reaches.
//
// The zero value names no severity. A check states one explicitly, so an
// omission reads as an omission rather than as the milder of the two.
type Severity string

const (
	// SeverityFatal halts the build. The objection is a violation the build
	// cannot proceed past, and the author has to change an input to get past
	// it.
	SeverityFatal Severity = "fatal"

	// SeverityWarning reports the objection and lets the build finish. It is
	// the severity of an advisory: a rule worth telling the author about that
	// does not make the emitted output wrong.
	SeverityWarning Severity = "warning"
)
