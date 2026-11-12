// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package analyzer holds the static contracts evaluated over the finalised IR
// graph, and derives what that graph makes available to the surfaces beyond it.
//
// # Role
//
// The contracts are these, each declared here as a value the framework invokes
// and none evaluated in this package:
//
//   - usesSchema selector rules on every edge — the target node's
//     labels must satisfy the source-side selector string declared on
//     the consuming Component's (or originating Feature's) usesSchema
//     entry for the socket being filled.
//   - Feature appliesTo constraints on every Feature attachment —
//     the target node's labels must satisfy the Feature catalog
//     entity's appliesTo / appliesToAnyOf constraints (AND across the
//     two surfaces; an omitted constraint is vacuously satisfied).
//   - featuresSchema selector rules on every attachment slot a component
//     type declares, on the component itself and on each of its dependency
//     sockets — the labels of the Feature bound to the slot must satisfy the
//     selector string the slot declares. A slot declaring no selector accepts
//     any fill. A socket's slot is filled by each connection on the socket, so
//     its rule is asked of each attachment a connection carries.
//   - featuresSchema required rules on the same slots — a slot the component
//     type declares required must be bound by some attachment on the node,
//     counting every scope the node inherits from, and a slot a socket
//     declares required must be filled on every connection wired through the
//     socket. Whether a slot may be left unbound is this rule's question and
//     not the one above it: the two are asked of the same slot and answer about
//     different things.
//   - the misnamed fill on a component's own slots — an attachment written on
//     the component instance under a name no slot declares must not fit, by the
//     selector a slot declares, a slot nothing binds on that node once every
//     scope has folded. An attachment inherited from a subsystem or a boundary
//     is not judged, and neither is a slot declaring no selector.
//   - presence mandates a component type declares — each
//     features.aggrega.ai/requiredType.<label> annotation must be satisfied by
//     some attachment of the type it names, under any name, counting every
//     scope the node inherits from.
//
// Every rule reading a label constraint delegates the matching to pkg/selector,
// all of them report on [AnalysisResult].Errors, and none is evaluated in this
// package. Each is declared here — [EdgeTargetSatisfiesSocketSelector],
// [AttachmentSatisfiesFeatureApplicability], [SlotFillSatisfiesSlotSelector] and
// [ConnectionAttachmentSatisfiesSocketSlotSelector], [RequiredAttachmentSlotBound]
// and [RequiredSocketSlotFilledOnEveryConnection],
// [AttachmentFittingAnEmptySlotIsNamedForIt], and
// [RequiredAttachmentTypeAttached] — and each
// is evaluated over a presentation of the finalised graph, which this package is
// not handed; whatever holds that presentation invokes them and appends what
// they raised to the same slice, through [ErrorsFromObjections]. So a reader
// looking for the edge walk, the attachment walk, either slot walk or the node
// walk that enforces them will not find one here, and that is the arrangement
// rather than a gap.
//
// A sixth rule is declared here and evaluated EARLIER than any of them, which is
// why it is separated rather than listed above.
// [AttachmentRequirementWellFormed] reports a presence mandate whose declaration
// cannot be evaluated at all — an annotation naming the mandate family with no
// label after it, or one whose value is not a Feature type reference. Its facts
// are complete once the composition is flattened, so it is invoked against that
// flattening's own presentation, and a document carrying such a declaration
// therefore halts before the graph exists. A broken declaration must never read
// as a satisfied requirement and must not read as an absent attachment either:
// the two have different remediations, and the earlier report is what puts the
// catalog fault in front of the author before anything reasons about what the
// declaration says.
//
// # Outputs
//
// Analyze returns one [AnalysisResult] carrying:
//
//   - Errors — one [AnalysisError] per violation, every one of them appended by
//     whatever invoked the rules declared here rather than by Analyze, which
//     evaluates none. The Errors slice is the gate signal: a non-empty slice
//     halts the pipeline before the render.
//   - Paths — the precalculated_paths array produced by cycle-safe
//     DFS through the IR graph (see [PrecalculatedPath]).
//   - PolicyInput — the [PolicyInput] payload destined for
//     the policy socket described in
//     aggrega-docs/architecture/14-extensibility.md.
//
// # Pipeline integration
//
// The build flow gates on len(AnalysisResult.Errors): a non-empty
// slice halts before the render, with one stderr line per violation.
// AnalysisResult.PolicyInput is computed on every build and, when a
// workspace configures a policy plugin, the pipeline marshals it to that
// plugin — with every leaf the graph's own sensitivity markings name
// withheld, on a copy — and appends each failed verdict to
// AnalysisResult.Errors, so a policy rejection halts the build through the
// same gate. AnalysisResult.Paths ride the PolicyInput to the plugin as
// precalculated reachability; they are otherwise not consumed by the render.
//
// # Determinism
//
// All output is byte-deterministic across runs of identical input:
//
//   - DFS adjacency lists sort alphabetically before traversal.
//   - DFS starts iterate node IDs in alphabetical order.
//   - PathIDs are assigned in deterministic-DFS-emission order
//     (path_01, path_02, …) — the index reflects traversal sequence
//     rather than a post-sort over the path set.
//   - AttachedFeatures on each path sort alphabetically.
//   - Socket-constraint violations emerge in the order the graph holds its
//     edges, which is the order the traversal invokes the rule in.
//   - Feature-applicability violations emerge in the order the graph holds
//     its nodes and then its merged attachments, which is again the order
//     the traversal invokes the rule in.
//   - Slot-fill violations emerge in the order the graph holds its nodes and
//     then in the order each node's component type declares its attachment
//     slots, which is once more the order the traversal invokes the rule in.
//   - Unbound-required-slot violations emerge in that same order, over that
//     same axis, and after every slot-fill violation: the two rules are
//     invoked as two walks rather than one, so all of the first rule's
//     findings precede all of the second's.
//   - Misnamed-fill violations emerge in the order the graph holds its nodes
//     and then its merged attachments, after every slot-fill violation and
//     before every presence-mandate one, for the same reason: one walk per
//     rule, in the order the invoking site runs them.
//   - Unsatisfied-presence-mandate violations emerge in the order the graph
//     holds its nodes and then in the order the component type's annotations
//     declare the mandates, and they precede the unbound-required-slot
//     violations, for the same reason: one walk per rule, in the order the
//     invoking site runs them.
//   - Enumeration reads the caller's cancellation signal once per start node
//     and returns the withdrawal with no partial result, so a cancelled
//     derivation yields nothing rather than a truncated payload.
package analyzer
