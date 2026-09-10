// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package viewnarrowing reports a constraint check that declares a wider view
// than the facts its predicate reads, and names the narrower view that would
// suffice.
//
// A check declares the facts it needs as a view type, and where it runs follows
// from that declaration: it is the point in the compilation at which the
// innermost view in the declared type's embedding chain is satisfied. Declaring
// a wider view than the predicate reads therefore places the check later than
// it needs to be — and the type system has no objection, because a wider view
// satisfies a narrower need. Over-declaration is invisible to the compiler,
// which is the whole reason it needs reading for.
//
// # The chain is read, never tabulated
//
// [ReadChain] derives which facts are complete at which point by reading the
// view interfaces and the embedding between them. Nothing here maps an accessor
// to a point in the compilation by hand: the view chain is generated from the
// fact registry, so a hand-written mapping would go stale the moment a fact
// moved stages, and would then report a confident wrong answer rather than
// failing. The one identifier this package anchors on is the check type's own
// name, and even the base of the chain is read from that type's first
// type-parameter constraint rather than assumed.
//
// # What this cannot catch, and what closes that instead
//
// This reads one direction only: a check declaring a view wider than the facts
// its body reads. The opposite defect — a check declaring a narrow view and
// then reaching behind an accessor to re-derive a fact the view does not name —
// is invisible to it, and no amount of additional reading here would help.
// Nothing in a predicate's body distinguishes a fact obtained from an accessor
// from a fact reconstructed out of something the check should never have been
// handed. This package sees the accessors a body calls; it cannot see the ones
// a body bypassed.
//
// What closes that gap is a property of the invocation site rather than an
// analysis: an invocation site that accepts check values and supplies the view
// itself leaves the authoring documents and the graph unreachable from a check,
// so a check has nothing raw to re-derive a fact from.
//
// That property has two halves, and exactly one of them holds — so the gap is
// bounded rather than closed. The framework's invocation sites do accept check
// values rather than bare predicate functions. The pull sites own the traversal
// as well: each enumerates a kind's coordinates, assembles every instance's
// provenance and invokes the check once per instance, so a check names its
// subject and never constructs one. Real rules run through them from
// pkg/resolver, internal/compiler/topology, internal/compiler/pipeline and
// internal/compiler/renderer, and the sites that push one presented document
// over as it arrives are several rather than one.
//
// What stays open is the half the gap actually turns on: every one of those
// sites takes the view as an argument its caller constructs, and the view
// contract declares no method, so it admits any type at all — including one a
// check declares for itself, which this package reports as a view it cannot
// place. A site supplying the view itself would leave the authoring documents
// and the graph unreachable from a check, and none does. Stating all of that
// plainly is the point of this section: an analyzer whose documented reach
// exceeds its actual reach is how a blind spot comes to be treated as covered.
//
// # The one shape that reads no fact and is still placed
//
// A predicate can have no fact to read. Where its whole input is a property of
// the subject it is asked about — an identity, a locator, a name — the subject
// carries that input and no view supplies it, so the check reads nothing from
// its view and has nothing to narrow to. It still names a view, because that is
// what fixes the point in the compilation it runs at.
//
// Such a check declares itself so, on the check's own subject-only field,
// stating what it reads from its subject and why that is not a view fact. The
// declaration is read back against the body rather than taken on trust: a
// declaration on a predicate that does read a view fact is reported, and so is
// one whose stated reason names nothing the predicate reads from its subject. So
// the field admits the shape it names and cannot be spent on the defect this
// package exists to find — a check reading facts while declaring a view later
// than they require is reported exactly as it was before, declaration or not.
//
// It is a field rather than a flag for the same reason it is verified: a marking
// applied by writing nothing is one a later author applies without deciding
// anything, and at that point a check placed at nothing no longer looks
// different from a check that meant it.
//
// # What it refuses to conclude
//
// A body this package cannot fully read is reported, never silently cleared.
// Where the view escapes to somewhere the reading cannot follow — handed to a
// helper, assigned to another variable, or a predicate supplied as a named
// function rather than written in place — the check is reported as unreadable
// and receives no narrowing verdict at all. A verdict over a body only
// partially read would be the one failure this package must not have: a clean
// result that certifies nothing.
//
// The reading is deliberately syntactic, and its soundness runs in one
// direction. A local variable sharing the view parameter's name would
// contribute accessor names the view never supplied, and additional names can
// only push the narrowest sufficient view later, or into the unknown-fact
// report — never earlier. So a pathological body can make this package quieter
// about over-declaration; it cannot make it accuse a check that declares
// correctly.
package viewnarrowing
