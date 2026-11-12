// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package constraint

// Severity is what a raised objection does to the build: halt it, report it and
// let the build finish, or halt only a run that emits something a person will
// apply. A check declares its severity, so the consequence of a violation is
// part of the declaration rather than a property of which collection the
// check's output happens to be appended to.
//
// The three values are the three outcomes this compiler distinguishes, and the
// third is not a softening of the first. It separates a run that EMITS from one
// that only DESCRIBES a composition — a distinction the two values below cannot
// express, because each of them says the same thing to every reporting surface.
// A rule that recorded a finding without affecting any build's outcome would be
// a policy verdict at the plugin boundary rather than a check here, and such a
// verdict carries that boundary's own vocabulary.
//
// This is deliberately its own vocabulary rather than a reuse of one the
// compiler already carries. The others each belong to a boundary — an
// artifact's deprecation marker, a tolerant lockfile load, the editor protocol
// the language server publishes on, and the plugin diagnostic every Wasm socket
// shares, whose severity is a closed set the shared diagnostic contract declares
// and the socket boundary enforces — and they differ from one another because
// the boundaries do. A check sits at no boundary: it runs in-process over the
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

	// SeverityEmissionFatal refuses a run that emits something a person will
	// apply, and is carried — without refusing — by a run that only describes a
	// composition. It is the severity of a rule whose subject makes the emitted
	// output wrong while leaving the composition perfectly describable: an
	// instance that resolved to nothing to render through is the shape it exists
	// for, since a picture of that composition is exactly what the author needs
	// in order to fix it.
	//
	// # Which reporting surfaces refuse and which carry, and who decides
	//
	// The severity states the consequence; it does not name the surfaces. WHICH
	// runs emit is answered by the entry that reports, the same way the stream a
	// fatal and an advisory land on is chosen by the reporting path rather than
	// by the rule — a rule cannot know which surface asked it. So an entry that
	// produces a graph and nothing else carries the objection, an entry that
	// renders refuses on it, and an entry that serves both postures reads the
	// caller's own declaration of which one it is.
	//
	// # The coverage bound, stated because an unstated bound reads as full
	//
	// A surface that carries the objection reports it only if it has a channel to
	// report it ON. Two of this compiler's entries produce a graph for a caller
	// that renders nothing and expose no finding stream at all, so an objection
	// raised at this severity reaches them and is reported to NOBODY there. That
	// is not a hole this severity opened — it is what those surfaces already do
	// with the condition today — but a reader who is told only that the finding
	// is "carried" would take it to mean "shown", and it is not.
	//
	// And an entry serving both postures can only be as precise as the
	// declaration its caller makes. Where the only such declaration is one asked
	// for a particular condition, a second rule declared at this severity inherits
	// that caller's answer rather than getting one of its own; giving it one means
	// the caller declaring emission in its own right.
	SeverityEmissionFatal Severity = "emission-fatal"
)
