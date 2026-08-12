// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compose holds the compose provider's translation core: the
// filesystem-free functions that turn a node's render input into Docker Compose
// content, and that fold a dispatch group's per-node contributions into one
// document.
//
// The core carries no build tag, so it compiles for the host and for the wasip1
// guest alike, and it therefore imports no host-only capability — no filesystem,
// no network. Every entry point takes exactly the inputs its translation
// consumes: AssembleBoundaryCore takes the per-node fragments, the per-node
// annotations, the uses edges, the boundary-path fallback and the YAML indent;
// RenderSourcedCore takes the base compose bytes rather than a path. That is
// what lets the compose provider plugin call these translations directly
// instead of carrying its own copy of them.
//
// # The two input kinds converge on one fragment
//
// A node reaches the merge through one of two paths, and both produce the same
// intermediate shape, so the merge has one input format rather than two:
//
//   - Synthesised. A node whose Recipe declares workload: is translated into a
//     partial compose document holding just that node's contribution — one
//     service for the main container plus one per sidecar, one top-level volume
//     per persistent claim, and the per-container environment, probes and volume
//     mounts.
//   - Sourced. A node whose Recipe declares source: has that file parsed and the
//     Recipe's evaluated values deep-merged on top, per compose's own overlay
//     semantics (see deepMergeOverlay).
//
// Either way the result is marshalled to YAML and handed back as one
// intermediate fragment named `<nodeID>.compose-fragment.yaml`. The fragment is
// self-contained: it round-trips through YAML unmarshalling cleanly, which is
// what lets the merge parse it back.
//
// # Fragments are intermediates, never output
//
// The fragments exist only to be folded together. They accumulate in memory,
// keyed by node ID, and nothing writes them to the dispatch path — the compose
// provider declares that it merges its dispatch group's per-node output, and
// merged output means the per-node halves are inputs to the merge rather than
// artifacts in their own right. Only the assembled docker-compose.yaml is
// emitted, and it lands at the dispatch path — one file for the whole dispatch
// group, however many nodes contributed to it.
//
// # Determinism
//
// Given identical input the assembled document is byte-identical, and every
// ordering decision on the way there is deliberate rather than incidental:
// contributing nodes are sorted before the merge walks them, so merge order is a
// property of the node IDs and not of map iteration; inferred dependencies are
// sorted by target name before they are recorded; and a collision diagnostic
// sorts both its contributor descriptors and its services, so the same conflict
// produces the same message text every time. Where a collection carries a
// meaningful author order, that order is preserved instead — an init-container
// chain is ordered because the ordering is the meaning.
//
// # Translation discipline
//
// Aggrega's Workload model carries Kubernetes-shaped semantics (pod
// specifications, probes, volume types, service types). Compose is
// shape-poorer, so some translations are degraded and some authored fields
// reach no part of the output. Where a field cannot be expressed, the
// translation either reports it out-of-band — UnsupportedFieldDiagnostic for a
// field nothing in the output carries, PartialSupportDiagnostic for one carried
// in reduced form — or halts with a translation error naming what could not be
// expressed. An out-of-band diagnostic never alters the rendered bytes: the
// emitted fragment is byte-for-byte what it would be without it.
//
// Not every divergence is reported. A probe degraded to a shell-command wrapper
// is translated silently, so the reported set is the fields the translation
// cannot carry rather than every point where compose and Kubernetes semantics
// differ.
package compose
