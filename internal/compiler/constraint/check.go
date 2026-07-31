// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// Check is a constraint check as a value: the sentinel naming the rule, what a
// violation of it does to the build, and the predicate itself. Its three type
// parameters are the whole of its declaration.
//
// V names the facts the check reads — one of the view interfaces, each of which
// exposes exactly the facts complete at one point in the compilation. Because a
// view exposes named facts and never a raw document, the type parameter states
// what the check reads exhaustively rather than approximately.
//
// C names the host powers the check needs. Reading the filesystem, resolving a
// plugin, loading workspace configuration are declared here and never folded
// into V, so a view cannot be the route to one. See [NoHostPowers], which is
// what a check declares when it needs none — the ordinary case.
//
// S names what the check is invoked once per. A subject also carries the
// provenance of the thing it stands for, which is how an objection acquires a
// source file and a locator without the check assembling either.
//
// # It carries no stage, no caller list and no traversal
//
// That absence is the mechanism, not an omission. The point in the compilation
// a check runs at follows from V: it is the point at which the innermost view
// in V's embedding chain is satisfied, and views embed one another, so their
// order is a fact the compiler already knows. Which callers exercise the check
// follows from the same value intersected with how far each caller compiles.
// What the check iterates follows from S.
//
// A Stage field would let a declaration and a computation disagree about the
// same check, which is the arrangement this framework replaces: a placement
// asserted next to the facts it is supposed to follow from is a placement a
// reviewer has to audit by reconstructing the author's reasoning. A caller list
// would go stale the moment a caller was added. A traversal field would make a
// check able to choose what "once" means for it, and "once per instance of S"
// is exactly what the framework owns.
//
// # What constrains each parameter
//
// V is constrained by [View], the contract every view satisfies. View declares
// no method, so the constraint admits any type today; it is written as View
// rather than as the empty constraint because the name is what states the
// intent, and because a method added to the base contract tightens this
// constraint in the same edit rather than needing one here.
//
// S is constrained by [Subject]. Every subject kind embeds Subject and adds its
// own unexported marker, so a check declaring one kind cannot be handed
// another, and no type declared outside this package satisfies Subject at all.
//
// C is unconstrained, and the reason is worth stating rather than leaving as an
// omission. There is one capability and the axis is expected to stay empty. A
// marker interface closing the axis would add a seal that buys nothing, because
// the axis is already closed where it matters: invoking a check means supplying
// a value of C, and the framework is what invokes, so a check declaring a power
// the framework cannot construct cannot be invoked at all. The bound sits at
// the supply point rather than on the type.
type Check[V View, C any, S Subject] struct {
	// Sentinel names the rule the check enforces. An objection the check
	// raises carries it, and it is the identity a caller matches on.
	Sentinel error

	// Severity is what a raised objection does to the build.
	Severity Severity

	// Run evaluates the rule against the facts and one subject instance, and
	// returns one objection per violation it finds — nil when the input
	// satisfies the rule.
	//
	// It takes exactly the view and the subject. Everything else a check could
	// want is either a fact, and therefore on the view, or a host power, and
	// therefore declared in C.
	Run func(V, S) []Objection
}
