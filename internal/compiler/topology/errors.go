// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import "errors"

var (
	// ErrSubAggregateLoad fires from flattenAggregate when a sub-aggregate
	// import either fails to load (the SubAggregateLoader callback returns
	// an error) or is detected as a circular reference (the Aggregate's
	// `aggregates` block names a sub-aggregate type already on the visited
	// stack). The wrapped error carries the failing type URI and the
	// accumulated dotted prefix of the offending chain so callers can point
	// the author at the specific import that caused the failure.
	ErrSubAggregateLoad = errors.New("sub-aggregate load failed")

	// ErrMaxDepth fires from flattenAggregate when sub-aggregate inclusion
	// recursion exceeds maxSubAggregateDepth (currently 10). The bound
	// caps both pathological-depth authoring mistakes and any sub-aggregate
	// cycle that would otherwise recurse to stack-overflow. The wrapped
	// error carries the depth reached and the prefix path of the offending
	// chain.
	ErrMaxDepth = errors.New("max sub-aggregate nesting depth exceeded")

	// ErrReleaseEdgeRetarget fires from the Release edge pass when a Release
	// per-edge `uses` entry whose (consumer, socket, alias) matches an existing
	// developer-declared edge supplies a different target. A Release may
	// configure an edge (add per-edge infra) or wire an edge into a
	// declared-but-unbound socket, but it may not redirect an edge the developer
	// already declared — the developer owns the architecture they authored. A
	// Release entry that names the same target, or omits the target, is a
	// configure and is permitted. The wrapped error names the edge and both the
	// declared and the Release-supplied target.
	ErrReleaseEdgeRetarget = errors.New("release edge retargets a developer-declared edge")

	// ErrDanglingUsesTarget fires when a `uses` edge names a target component
	// that is not present in the resolved topology — origin-agnostic: a
	// developer-authored Aggregate edge and a Release-wired edge are checked
	// alike. A dangling target is a Loud Guardrail, not a silent drop: an edge
	// to a non-existent target would otherwise vanish and leave the consumer's
	// inject silently empty. The wrapped error names the consuming node, the
	// socket, the alias, and the missing target so the author can correct the
	// reference. The check is the structural sibling of the uses-socket
	// allowlist (the socket must be declared and the target must resolve before
	// an edge is type-checked at Step 4).
	ErrDanglingUsesTarget = errors.New("uses edge target does not exist")

	// ErrSubAggregateNameCollision fires from flattenAggregate when a Set
	// operation on the flat node map would overwrite an existing entry —
	// a condition impossible by the current Aggregate schema (root-level
	// instance names cannot contain dots, so dotted sub-aggregate IDs
	// cannot collide with root-level names) but defended-against in case
	// a future schema loosening or a code path bypassing validation lets
	// two flattened IDs collide. The wrapped error carries the colliding
	// ID, the dotted prefix path that produced it, and the existing
	// entry's source aggregate so the diagnostic points the author at the
	// precise authoring position.
	ErrSubAggregateNameCollision = errors.New("flattened node ID collision: rename one of the colliding instances so their flattened IDs differ")

	// ErrUnnormalizedAggregate fires at the topology entry when an Aggregate
	// reaches flatten carrying BOTH a non-empty top-level components surface AND
	// a synthesized `default` subsystem — an impossible state for an Aggregate
	// born through the normalization core (Normalize folds the root into the
	// default and clears it). It signals a bypass: a hand-built `types.Aggregate`
	// literal or a load path that skipped the parse core. This is an
	// internal-invariant breach, not an authoring error — a Loud Guardrail
	// (Invariant 4) backstopping the NormalizedAggregate newtype rather than
	// silently flattening a half-normalized graph.
	ErrUnnormalizedAggregate = errors.New("aggregate reached topology un-normalized (non-empty root components alongside a synthesized default subsystem)")

	// ErrInvalidActive fires from flattenAggregate when a component's or a
	// sub-aggregate module's resolved `active` toggle is neither empty nor a
	// boolean literal. The toggle reaches flatten as an already-resolved string
	// (a boolean authored directly, or a parameter template the resolver
	// rendered to a literal); flatten interprets it strictly, so a value that is
	// not empty and does not parse as a boolean — including the "<no value>"
	// literal a template over a missing key produces — is a Loud Guardrail
	// (Invariant 4) rather than a silent default. The wrapped error names the
	// node whose toggle failed and the offending resolved value so the author
	// can correct the toggle or the parameter driving it.
	ErrInvalidActive = errors.New("active toggle did not resolve to a boolean")

	// ErrUsesPrunedComponent fires from the origin-agnostic dangling-target
	// sweep when a surviving component's `uses` edge targets a component that
	// was pruned from the topology because its `active` toggle resolved to
	// false. It is the pruned-target sibling of ErrDanglingUsesTarget: the
	// target is not merely absent, it was deliberately removed, so the
	// diagnostic distinguishes the two rather than reporting a generic dangling
	// reference. Pruning a component that a survivor still depends on would
	// otherwise leave the consumer's inject silently empty, so the build halts
	// (Invariant 4). The wrapped error names the surviving consumer, the pruned
	// target, and that the target was pruned because its `active` resolved to
	// false so the author can either keep the target present or drop the edge.
	ErrUsesPrunedComponent = errors.New("uses edge target was pruned because its active toggle resolved to false")

	// ErrMutatorPatchRejected fires when a mutator plugin returns a JSON Patch
	// the host refuses to apply to the labels-only graph projection: a patch
	// that fails to decode, a patch whose operation does not apply cleanly, or
	// a patch that reaches outside the node-label surface (adding or removing a
	// node, touching a node field other than its labels, or setting a non-string
	// label value). The mutator's sanctioned surface is node labels only — the
	// narrow auto-tagging exception — so any change beyond that is a Loud
	// Guardrail (Invariant 4): the build halts rather than silently absorbing a
	// mutation the plugin was not authorized to make. The wrapped error names
	// the offending patch and the specific confinement rule it broke.
	ErrMutatorPatchRejected = errors.New("mutator patch rejected")
)
