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

var errSubAggregateLoad = cerrors.Register("sub-aggregate-not-loadable", cerrors.Meta{
	ShortCode: "E0072",
	Message:   "sub-aggregate load failed",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrMaxDepth fires from flattenAggregate when sub-aggregate inclusion
// recursion exceeds maxSubAggregateDepth (currently 10). The bound
// caps both pathological-depth authoring mistakes and any sub-aggregate
// cycle that would otherwise recurse to stack-overflow. The wrapped
// error carries the depth reached and the prefix path of the offending
// chain.
func ErrMaxDepth() error { return errMaxDepth }

var errMaxDepth = cerrors.Register("sub-aggregate-nesting-depth-exceeded", cerrors.Meta{
	ShortCode: "E0068",
	Message:   "max sub-aggregate nesting depth exceeded",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errReleaseEdgeRetarget = cerrors.Register("release-edge-retarget", cerrors.Meta{
	ShortCode: "E0071",
	Message:   "release edge retargets a developer-declared edge",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errUnboundReleaseEdgeConfig = cerrors.Register("unbound-release-edge-config", cerrors.Meta{
	ShortCode: "E0074",
	Message:   "release per-edge configuration has no edge to apply to",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrDanglingUsesTarget fires when a `uses` edge names a target component
// that is not present in the assembled graph — origin-agnostic: a
// developer-authored Aggregate edge, a Feature-projected edge and a
// Release-wired edge are checked alike. A dangling target is a Loud Guardrail,
// not a silent drop: an edge to a non-existent target would otherwise vanish
// and leave the consumer's inject silently empty.
//
// It is raised by [ConnectionTargetIsAComponent], the rule declared over the
// finalised graph, and by nothing else. The diagnostic carries the missing
// target and the socket it was wired into, positioned by the framework at the
// authored entry in the document that drew the connection — so an edge an
// attachment or a deployment drew names that document rather than a dependency
// block the consuming component does not have.
//
// A target the composition removed by a false `active` toggle is reported by
// [ErrUsesPrunedComponent] instead, so an author is told which of the two it is.
func ErrDanglingUsesTarget() error { return errDanglingUsesTarget }

var errDanglingUsesTarget = cerrors.Register("dangling-uses-target", cerrors.Meta{
	ShortCode: "E0066",
	Message:   "uses edge target does not exist",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSubAggregateNameCollision fires from flattenAggregate when merging a
// loaded module's nodes into the accumulating set would overwrite an entry
// already there. It has exactly one raise site, the merge itself, and the
// identity two subsystems of one Aggregate would share is a separate condition
// — see [ErrSubsystemComponentIdentityCollision].
//
// The merge arm is not reachable through ordinary authoring. A module's nodes
// all carry that module's dotted prefix, an instance name is written from
// letters, digits, `-` and `_` so it cannot carry the dot the prefix is joined
// with, and two modules imported under one name are one duplicate key the
// document is refused for. So this backstops a composition assembled by
// something other than the ordinary authoring path. The wrapped error carries
// the colliding ID, the dotted prefix path that produced it, and the existing
// entry's source aggregate.
func ErrSubAggregateNameCollision() error { return errSubAggregateNameCollision }

var errSubAggregateNameCollision = cerrors.Register("sub-aggregate-name-collision", cerrors.Meta{
	ShortCode: "E0073",
	Message:   "flattened node ID collision: rename one of the colliding instances so their flattened IDs differ",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSubsystemComponentIdentityCollision fires when two component instances an
// Aggregate declares in different subsystems would take one identity in the
// flattened composition.
//
// A subsystem contributes nothing to the identity a component carries: the
// composition keys a component on the name its author wrote, with the prefix of
// the module contributing it ahead of it and nothing from the subsystem tree in
// between. So two subsystems of one Aggregate may each declare a component under
// the same name, which the schema accepts because they are distinct authored
// keys, and the two then answer to one identity. The second would replace the
// first and one of the components would be absent from the output with nothing
// said about it, so the build halts instead (Invariant 4).
//
// The same name twice inside ONE subsystem is a different fault: those are one
// duplicate key and the document is refused as it loads.
//
// It is raised by [ComponentIdentityIsDeclaredOnce], the rule declared over the
// flattened composition, and by nothing else. The diagnostic carries the shared
// identity and the subsystems that declared it, positioned by the framework at
// the authored component key.
func ErrSubsystemComponentIdentityCollision() error { return errSubsystemComponentIdentityCollision }

var errSubsystemComponentIdentityCollision = cerrors.Register("subsystem-component-identity-collision", cerrors.Meta{
	ShortCode: "E0294",
	Message: "components in different subsystems take the same identity: rename one of them, " +
		"since a subsystem contributes nothing to a component's identity",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errUnnormalizedAggregate = cerrors.Register("aggregate-not-in-canonical-form", cerrors.Meta{
	ShortCode: "E0075",
	Message:   "aggregate reached topology un-normalized (non-empty root components alongside a synthesized default subsystem)",
	RulePlace: cerrors.NotARule("An Aggregate reached the topology walk in a state the normalization core cannot produce, so " +
		"a caller built one by hand or took a load path that skipped that core. It reports an " +
		"invariant of this program's own construction; the authored shape it would correspond to is " +
		"refused before it gets here."),
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

var errInvalidActive = cerrors.Register("active-toggle-not-a-boolean", cerrors.Meta{
	ShortCode: "E0067",
	Message:   "active toggle did not resolve to a boolean",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUsesPrunedComponent fires when a surviving component's `uses` edge targets
// a component that was pruned from the composition because its `active` toggle
// resolved to false. It is the pruned-target sibling of ErrDanglingUsesTarget:
// the target is not merely absent, it was deliberately removed, so the
// diagnostic distinguishes the two rather than reporting a generic dangling
// reference. Pruning a component that a survivor still depends on would
// otherwise leave the consumer's inject silently empty, so the build halts
// (Invariant 4).
//
// It is raised by [ConnectionTargetIsActive], the rule declared over the
// finalised graph, and by nothing else — origin-agnostic in the same way its
// sibling is. The diagnostic carries the pruned target and the socket it was
// wired into, positioned by the framework at the authored entry, so the author
// can either keep the target present or drop the edge.
func ErrUsesPrunedComponent() error { return errUsesPrunedComponent }

var errUsesPrunedComponent = cerrors.Register("uses-target-not-active", cerrors.Meta{
	ShortCode: "E0077",
	Message:   "uses edge target was pruned because its active toggle resolved to false",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrReexportCycle fires from resolveExportTarget when the alias chain it walks
// revisits an alias it has already visited, so following the chain further
// would never land on a node. An Aggregate may re-publish a module's exported
// alias under a name of its own, and resolving such an alias follows the chain
// of re-publications to the node it ultimately names.
//
// Nothing an author writes assembles a chain that loops. The inclusion tree is
// acyclic and every export value is qualified more deeply than the frame it is
// resolved in, so a well-formed chain strictly descends and terminates; the
// visited set backstops an alias table assembled by something other than the
// ordinary authoring path, and the walk halts rather than following it forever
// (Invariant 4). The wrapped error names the chain it walked, which is a note
// for whoever is debugging the build rather than an authoring instruction.
func ErrReexportCycle() error { return errReexportCycle }

var errReexportCycle = cerrors.Register("reexport-cycle", cerrors.Meta{
	ShortCode: "E0070",
	Message:   "re-exported component alias forms a cycle",
	RulePlace: cerrors.NotARule("A re-published component alias table walked back to an alias it had " +
		"already visited, so following it further would never land on a node. It reports an invariant " +
		"of this program's own construction: the inclusion tree is acyclic and every export value is " +
		"qualified more deeply than the frame it is resolved in, so no authored Aggregate assembles a " +
		"chain that loops."),
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

var errMutatorPatchRejected = cerrors.Register("mutator-patch-rejected", cerrors.Meta{
	ShortCode: "E0069",
	Message:   "mutator patch rejected",
	RulePlace: cerrors.NotARule("A mutator plugin returned a patch the host refused to apply to the labels-only graph " +
		"projection — one that does not decode, does not apply cleanly, or reaches outside the " +
		"node-label surface. It reports a guest whose returned output does not conform to the surface " +
		"its role admits."),
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

var errUnplacedSubModule = cerrors.Register("unplaced-sub-module", cerrors.Meta{
	ShortCode: "E0076",
	Message:   "sub-module placed in no boundary: route it into a boundary under `aggregates`, or name its components there under `components`",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnplacedComponent fires when a component the composition holds is placed in
// no boundary at all. A Release places a component two ways and both count:
// naming it under a boundary's `components`, under the id it carries once the
// composition is flattened, or routing the sub-module that contributes it in
// under `aggregates`, which places every component that module contains. A
// component reaching neither is rendered outside every boundary the deployment
// declared, so its output location, sync wave, annotations and per-component
// infrastructure all come from nowhere.
//
// It halts rather than warning for the reason its sub-module counterpart does:
// the alternative is a silent one, where the build succeeds, the artifacts are
// emitted, and nothing says the operator's boundary configuration was never
// applied to them (Invariant 4). Two components split across that line render
// into separate outputs and lose the ordering they carry when placed together.
//
// A Release that declares top-level `components` rather than named boundaries
// does not fire it. Normalizing such a Release gives it a boundary of its own
// holding exactly those components, so every component it names is placed.
func ErrUnplacedComponent() error { return errUnplacedComponent }

var errUnplacedComponent = cerrors.Register("unplaced-component", cerrors.Meta{
	ShortCode: "E0260",
	Message:   "component placed in no boundary: name it under a boundary's `components`, or place the sub-module that contributes it under `aggregates`",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnresolvedBoundaryComponent fires when a boundary names a component the
// composition does not hold. The boundary's `directory`, `subDirectory`,
// `syncWave`, annotations and per-component `infra` are then written for a
// component that is not there: nothing renders under them, and the entry places
// nothing.
//
// A boundary names a component two ways and both fire it. Naming one under
// `components` addresses it by the id it carries once the composition is
// flattened, so a component a sub-module contributes is written with that
// module's dotted prefix ahead of its own name. Naming one inside an `aggregates`
// routing entry addresses it by the local name it carries inside that module,
// which the compiler resolves to the same qualified id. The diagnostic names the
// id the entry addressed, which is the value to compare against the composition
// — for a routed entry that is not the name in the file, because the file spells
// the local half and the module supplies the rest.
//
// It halts rather than warning for the reason its placement counterparts do: the
// alternative is silent. The build succeeds, the artifacts are emitted, and
// nothing says that an entry the operator wrote configured nothing at all
// (Invariant 4). It is the over-naming counterpart of the under-placement rules
// beside it — those ask of a component whether anything placed it, this asks of
// an entry whether anything answers to it — and either fault can occur without
// the other.
//
// A subsystem contributes nothing to the id a component carries, so a component
// the application declares inside a subsystem is addressed by its bare name. An
// entry naming it under the subsystem instead addresses nothing and fires this.
//
// A component switched off by a false `active` toggle does not fire it. Such a
// component is declared and deliberately removed, so an entry naming it is not a
// mistake, and refusing it would make the toggle unusable while any Release
// configured the component.
func ErrUnresolvedBoundaryComponent() error { return errUnresolvedBoundaryComponent }

var errUnresolvedBoundaryComponent = cerrors.Register("unresolved-boundary-component", cerrors.Meta{
	ShortCode: "E0261",
	Message:   "boundary names a component the composition does not hold: correct the name, or remove the entry",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
