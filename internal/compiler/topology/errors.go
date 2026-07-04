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

	// ErrInvalidUsesFormat fires from normalizeUses and parseVerboseEntry
	// when a `uses` block carries a value that is neither a string array
	// nor a verbose map, or a string-array element that is not a string,
	// or a verbose-entry value that is neither a map nor a simple-string
	// shorthand, or an inject block that is not a map. The wrapped error
	// names the socket and (when applicable) the alias plus the offending
	// Go type. Socket-name validity is enforced upstream by the resolver
	// against each entity's usesSchema (the socket-allowlist and
	// required-socket checks happen at Step 1); the formats this sentinel
	// catches are structural shape errors the YAML schema accepts but the
	// builder cannot interpret.
	ErrInvalidUsesFormat = errors.New("invalid uses format")

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
	ErrSubAggregateNameCollision = errors.New("flattened node ID collision")

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
