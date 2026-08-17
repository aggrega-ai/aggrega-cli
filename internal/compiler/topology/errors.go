// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrSubAggregateLoad fires from flattenAggregate when a sub-aggregate
// import either fails to load (the SubAggregateLoader callback returns
// an error) or is detected as a circular reference (the Aggregate's
// `aggregates` block names a sub-aggregate type already on the visited
// stack). The wrapped error carries the failing type URI and the
// accumulated dotted prefix of the offending chain so callers can point
// the author at the specific import that caused the failure.
func ErrSubAggregateLoad() error { return errSubAggregateLoad }

var errSubAggregateLoad = cerrors.Register("compiler-topology/sub-aggregate-load", cerrors.Meta{
	Message: "sub-aggregate load failed",
})

// ErrMaxDepth fires from flattenAggregate when sub-aggregate inclusion
// recursion exceeds maxSubAggregateDepth (currently 10). The bound
// caps both pathological-depth authoring mistakes and any sub-aggregate
// cycle that would otherwise recurse to stack-overflow. The wrapped
// error carries the depth reached and the prefix path of the offending
// chain.
func ErrMaxDepth() error { return errMaxDepth }

var errMaxDepth = cerrors.Register("compiler-topology/max-depth", cerrors.Meta{
	Message: "max sub-aggregate nesting depth exceeded",
})

// ErrReleaseEdgeRetarget fires from the Release edge pass when a Release
// per-edge `uses` entry whose (consumer, socket, alias) matches an existing
// developer-declared edge supplies a different target. A Release may
// configure an edge (add per-edge infra) or wire an edge into a
// declared-but-unbound socket, but it may not redirect an edge the developer
// already declared — the developer owns the architecture they authored. A
// Release entry that names the same target, or omits the target, is a
// configure and is permitted. The wrapped error names the edge and both the
// declared and the Release-supplied target.
func ErrReleaseEdgeRetarget() error { return errReleaseEdgeRetarget }

var errReleaseEdgeRetarget = cerrors.Register("compiler-topology/release-edge-retarget", cerrors.Meta{
	Message: "release edge retargets a developer-declared edge",
})

// ErrUnboundReleaseEdgeConfig fires when a Release per-edge `uses` entry
// carries per-edge `infra` but no edge exists at its (consumer, socket, alias)
// once every edge has been drawn. An entry that names a target creates the edge
// it configures; an entry that omits one configures an edge that must already
// have been declared, so an entry with configuration and no edge to put it on
// applies to nothing. Accepting it would let an operator author infrastructure
// that never reaches the output and read the successful build as confirmation,
// which is the inverse of a Loud Guardrail (Invariant 4).
//
// The condition is exact rather than heuristic: after the Release edge pass the
// edge set is final, so the entry either landed on an edge or it did not. Note
// which declaration is at fault — the socket IS declared by the consuming
// component's usesSchema, or the socket-allowlist guard would have refused it
// first; what is missing is a bound edge at that socket and alias. The wrapped
// error names the consuming node, the socket and the alias, and says whether
// the node exists in the topology at all, because a Release naming a node the
// Aggregate does not declare reaches this same condition.
func ErrUnboundReleaseEdgeConfig() error { return errUnboundReleaseEdgeConfig }

var errUnboundReleaseEdgeConfig = cerrors.Register("compiler-topology/unbound-release-edge-config", cerrors.Meta{
	Message: "release per-edge configuration has no edge to apply to",
})

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
func ErrDanglingUsesTarget() error { return errDanglingUsesTarget }

var errDanglingUsesTarget = cerrors.Register("compiler-topology/dangling-uses-target", cerrors.Meta{
	Message: "uses edge target does not exist",
})

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
func ErrSubAggregateNameCollision() error { return errSubAggregateNameCollision }

var errSubAggregateNameCollision = cerrors.Register("compiler-topology/sub-aggregate-name-collision", cerrors.Meta{
	Message: "flattened node ID collision: rename one of the colliding instances so their flattened IDs differ",
})

// ErrUnnormalizedAggregate fires at the topology entry when an Aggregate
// reaches flatten carrying BOTH a non-empty top-level components surface AND
// a synthesized `default` subsystem — an impossible state for an Aggregate
// born through the normalization core (Normalize folds the root into the
// default and clears it). It signals a bypass: a hand-built `types.Aggregate`
// literal or a load path that skipped the parse core. This is an
// internal-invariant breach, not an authoring error — a Loud Guardrail
// (Invariant 4) backstopping the NormalizedAggregate type rather than
// silently flattening a half-normalized graph.
func ErrUnnormalizedAggregate() error { return errUnnormalizedAggregate }

var errUnnormalizedAggregate = cerrors.Register("compiler-topology/unnormalized-aggregate", cerrors.Meta{
	Message: "aggregate reached topology un-normalized (non-empty root components alongside a synthesized default subsystem)",
})

// ErrInvalidActive fires from flattenAggregate when a component's or a
// sub-aggregate module's authored `active` toggle does not resolve to a boolean
// literal. The toggle reaches flatten as an already-resolved string (a boolean
// authored directly, or a parameter template the resolver rendered to a
// literal); flatten interprets it strictly, so a literal that does not parse as
// a boolean is a Loud Guardrail (Invariant 4) rather than a silent default.
//
// An EMPTY literal is one such value, and it is the case worth naming: a toggle
// reading a parameter resolves to nothing while that parameter is unfulfilled,
// and defaulting that to "present" would deploy the entity the author gated
// behind the parameter. Only a toggle the author never wrote defaults to
// present, which flatten reads from the toggle's absence rather than from its
// value. The wrapped error names the node whose toggle failed and the offending
// resolved value so the author can correct the toggle or fulfill the parameter
// it reads.
func ErrInvalidActive() error { return errInvalidActive }

var errInvalidActive = cerrors.Register("compiler-topology/invalid-active", cerrors.Meta{
	Message: "active toggle did not resolve to a boolean",
})

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
func ErrUsesPrunedComponent() error { return errUsesPrunedComponent }

var errUsesPrunedComponent = cerrors.Register("compiler-topology/uses-pruned-component", cerrors.Meta{
	Message: "uses edge target was pruned because its active toggle resolved to false",
})

// ErrReexportCycle fires from flattenAggregate when resolving a re-exported
// component alias walks a chain of exported aliases that loops back on itself
// before reaching a concrete node. A parent aggregate may re-publish a child
// module's exported alias under a new name; resolving that re-export follows
// the child's alias table to the underlying node. An acyclic inclusion tree
// makes such a loop unconstructible through normal authoring, so this is a
// backstop: rather than following the chain forever, the walk halts (Invariant
// 4). The wrapped error names the alias chain it walked so the author can see
// where the references close the loop.
func ErrReexportCycle() error { return errReexportCycle }

var errReexportCycle = cerrors.Register("compiler-topology/reexport-cycle", cerrors.Meta{
	Message: "re-exported component alias forms a cycle",
})

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
func ErrMutatorPatchRejected() error { return errMutatorPatchRejected }

var errMutatorPatchRejected = cerrors.Register("compiler-topology/mutator-patch-rejected", cerrors.Meta{
	Message: "mutator patch rejected",
})

// ErrUnplacedSubModule fires when a Release places none of a sub-module's
// components in any boundary. A boundary holds components two ways and both
// count: naming a component under `components`, under the dotted id it carries
// once the composition is flattened, and routing the whole sub-module in under
// `aggregates`, which places every component that module contains. A sub-module
// reaching neither has its components rendered outside every boundary the
// deployment declared, so their output location, sync wave, annotations and
// per-component infrastructure all come from nowhere.
//
// It halts rather than warning because the alternative is a silent one: the
// build succeeds, the artifacts are emitted, and nothing says the operator's
// boundary configuration was never applied to them (Invariant 4). It is the
// under-placement counterpart of the over-placement rejection a Release load
// already makes, and the two together are what make the placement rule complete.
//
// A sub-module contributing no components does not fire it: there is nothing to
// place. Neither does one a false `active` toggle removed, which was never
// composed.
func ErrUnplacedSubModule() error { return errUnplacedSubModule }

var errUnplacedSubModule = cerrors.Register("compiler-topology/unplaced-sub-module", cerrors.Meta{
	Message: "sub-module placed in no boundary: route it into a boundary under `aggregates`, or name its components there under `components`",
})
