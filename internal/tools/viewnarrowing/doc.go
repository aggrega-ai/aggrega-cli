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
// No such invocation site exists yet, so that gap is open today and this
// package does not narrow it. What exists is a push-model entry, which takes
// a check's predicate function together with a view value the caller
// constructs, so it bounds nothing about what a check can be handed — and the
// view type parameter admits any type at all, including one a check declares
// for itself, which this package reports as a view it cannot place. Stating all
// of that plainly is the point of this section: an analyzer whose documented
// reach exceeds its actual reach is how a blind spot comes to be treated as
// covered.
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
