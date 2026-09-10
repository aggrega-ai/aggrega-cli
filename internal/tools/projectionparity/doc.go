// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package projectionparity reports a structured fact a shared computation
// produced that one caller-facing surface carries and another silently drops.
//
// A capability reachable from more than one surface computes its answer once, in
// a shared core, and each surface then projects that answer into its own output.
// The membership question — does this surface carry this capability at all — is
// already answered by a declaration each surface makes and a check that walks it.
// This reads the second question, which that declaration was never going to
// reach: given that two surfaces both call one computation, does each one carry
// what the computation handed it.
//
// The rule it enforces is that a structured fact the shared computation produces
// belongs on every surface that can carry structure, and only human-formatted
// presentation may differ. A count, a verdict, a classification or a hint is owed
// everywhere. A terminal's indentation, its spine and its phase labels are not,
// and neither is the field spelling a wire format prefers, because those are
// forms of the same fact rather than the fact itself.
//
// # The corpus is derived, never listed
//
// A shared computation is not named here. It is read: an exported function of a
// package outside every surface, called from two or more surfaces, whose result
// includes a struct type declared in that same package. That is the parity
// subject exactly — one producer, two surfaces, one result — so a computation
// becomes subject to this read by being called twice, and no convention about
// package naming is relied on.
//
// A listed corpus was refused for the reason a listed corpus is always refused
// here: it answers "nothing is wrong" about code it never opened, and it goes
// stale in the direction of silence. What the caller does supply is the surfaces,
// because nothing in the tree links a surface to the packages that project its
// results — the availability declaration and the projection live in different
// packages for the command line — so that mapping is the caller's to state and
// to guard.
//
// # An exemption is stated at the site and read back against it
//
// A surface that legitimately cannot carry a fact says so where it drops it, in
// a comment naming the result under the package that declares it, then the field
// and the reason:
//
//	// projection-omits internal/catalogops.CatalogResult.Notices: <the reason this surface cannot carry it>
//
// Naming the result alone answers the other finding this read emits, a result a
// surface discards whole:
//
//	// projection-omits internal/catalogops.CatalogResult: <the reason this surface consumes no part of it>
//
// The two forms claim different things, so each is read back by its own checks.
// The live case for the broader one is a caller that consumes a result's
// PRESENCE rather than its content — a lookup used to validate an identifier,
// where the answer is whether the identifier resolves and there is no reader for
// the resolved value to be reported to.
//
// The result may also be named bare, and the bare form is accepted wherever it
// identifies one result. It is not what a finding prescribes, and the reason is
// that whether a bare name identifies one result is a property of the whole
// module rather than of the site: a module may declare that name in more than one
// package, in which case the bare marker names all of them and identifies none.
// So an author cannot see from where they write the marker whether the bare form
// holds, and a bare marker accepted today stops being accepted the day a second
// package declares a result of that name — with nothing at the site having
// changed. Every finding therefore prescribes the qualified form, which is the
// one spelling that cannot come to mean something else.
//
// The declaration is verified rather than believed. One that names a field the
// site does read is reported, and so is one that names a field no result type at
// the site carries, and so is one stating no reason. The broader form is reported
// where the surface never discarded that result, and where it does project fields
// from it — either would have it clear more than the narrow form could. A name
// that reaches no collected result, one that reaches more than one, and a marker
// whose shape names neither form are all reported too, rather than passed over:
// a marker nothing recognises is a reason its author believes they gave. A
// declaration nothing checks is one a later author applies by habit, and at that
// point a surface that drops a fact for a reason no longer looks different from
// one that dropped it by accident.
//
// 🔴 What the qualified form does NOT do is widen what an exemption can excuse.
// Every read-back above holds unchanged: it decides which result the marker
// names and nothing about whether the claim is borne out. A qualified marker on a
// field the surface reads is still stale, and a qualified whole-result marker on
// a surface that projects any field of that result is still refused.
//
// A central list of exemptions was refused deliberately. It would be a second
// copy of a fact whose only honest home is the code that drops the field: the
// list agrees with the code the day it is written, nothing compares them
// afterwards, and the disagreement is invisible from the list's own output.
//
// # What is legitimately presentation, so it is not re-found as a violation
//
// These differ by design between surfaces and are not this read's subject,
// because none of them is a fact the shared computation produced:
//
//   - A terminal's spine and its phase labels, which exist to place output in a
//     stream a person is watching.
//   - The stripping of a terminal's nesting indentation, which both non-terminal
//     surfaces do identically.
//   - A registry search's per-record screening prose, which both structured
//     surfaces discard in favour of the structured verdict they carry instead.
//   - camelCase on a wire against snake_case in a tool payload, each consistent
//     within itself: one fact, two spellings.
//   - An echo of the request a session-anchored surface does not need, because
//     the session already holds what a stateless call has to be told.
//   - A field withheld with an explicit stated reason, which is the good pattern
//     rather than a defect — and is the shape the marker above makes readable.
//
// # What it refuses to conclude
//
// The reading is syntactic and local. It follows the value a producer returns
// within the function that received it, and one hop into a same-package helper
// the value is handed to whole. Any other escape — assignment to a second
// variable, capture by a closure, a return, a hand-off this reading cannot
// follow — is reported as an unreadable site, and **no parity verdict is drawn
// for that surface and that result type at all**. A verdict over a value only
// partly followed would be the one failure this package must not have: a clean
// result certifying nothing.
//
// Two blind spots are structural, and stating them is the point of this section
// rather than a caveat on it.
//
// A surface that does not call the shared computation at all is invisible here.
// Where a surface answers the same request with a second implementation of its
// own — taking different input, computing its own answer, calling nothing shared
// — there is no shared result to project and so nothing for this read to
// compare. That divergence is real and this instrument cannot see it: only the
// membership declaration, or a reader, can. The condition-explanation capability
// is the live instance, and it is not modelled here.
//
// A field no surface reads is not reported. The shared computation may be
// producing something nobody projects, which is a different defect with a
// different repair, and reporting it here would put an unused-output finding
// inside a parity verdict.
//
// 🔴 And one class reads as a divergence and is not: a result carrying the same
// content twice, in two shapes for two kinds of caller. Where one field holds an
// ordered report meant to be printed and a second repeats part of it as bare
// entries for a caller that summarises, a surface reading only the first has
// carried the fact and dropped nothing — and this read compares field NAMES, so
// it cannot see that one field's content is inside another's. It reports the
// second field as dropped.
//
// That is not a bug to be fixed by weakening the comparison. Inferring which
// fields restate which would mean reading prose or guessing from shapes, and a
// guess here would clear real divergences. The stated exemption is the answer:
// the surface says, where it drops the repeat, that it carries the content
// through the other field. The exemption then sits beside the code a reader
// would have to check anyway, which is more than the field set alone ever says.
//
// The reason it is called out this loudly is that it was met while writing this
// package: a claim that a surface dropped an advisory was written, and the
// result's own documentation said the advisory lines were already inside the
// report field the surface did read. A finding of this class must be checked
// against the result's documentation before it is treated as a defect.
//
// The soundness runs in one direction, as with any syntactic reading. A local
// variable shadowing the name the result was bound to can contribute field names
// the result never carried, which can only make this read quieter about a
// dropped field or push it into the unreadable class — never make it accuse a
// surface that carries everything.
package projectionparity
