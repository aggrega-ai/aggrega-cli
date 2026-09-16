// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package topology is Step 2 of the compiler pipeline — the Topology
// Builder & Mutation Gate. It consumes a resolver.ResolveResult (the Step
// 1 output: a resolved Aggregate / Manifest / Release plus per-node
// boundary infra and a CatalogLookup) and produces a TopologyResult — the
// flattened node graph with directed edges, per-node attachments, and a
// compiler-internal map of exported-component aliases.
// The result is the upstream input to Step 3 (the IR builder); this
// package is the load-bearing seam between resolver-side validation and
// IR-side normalisation.
//
// Build is the orchestrator entry point. The body proceeds in five
// phases (numbered in the function): flatten the Aggregate tree (sub-
// aggregates → dot-notation IDs); populate node Labels from the catalog;
// populate BoundaryPath from the resolver; normalise polymorphic `uses`
// declarations into NormalizedEdge instances; and collect each
// attachment's `uses` edges, marking them with OriginFeature so the
// IR-side edge-atomicity contract carries downstream.
//
// Naming and authoring conventions the package relies on:
//
//   - Dotted node IDs. The dot is reserved as the compiler-internal
//     path separator; user-authored instance names cannot contain dots
//     (schema-banned), so flattening's dotted IDs are unambiguous.
//   - Cyclic edges. Build does not topologically sort or reject
//     back-edges; cycles flow through the IR intact for downstream
//     rendering. The runtime `uses` graph supports cycles (multi-agent
//     loops are a legitimate authoring shape); only the orthogonal
//     sub-aggregate inclusion graph rejects them.
//   - Per-edge origin marker. NormalizedEdge.OriginFeature keeps
//     Feature-authored edges separable from Component-authored edges.
//     The IR builder reads this field directly into IREdge.
//   - Sub-aggregate depth bound. flattenAggregate enforces a maximum
//     inclusion depth of 10 (maxSubAggregateDepth), halting with
//     ErrMaxDepth on overflow. The bound caps pathological depth and
//     converts cyclic includes into deterministic compile-time errors.
//   - Map-based root topology. Aggregate.Spec.Components, Subsystems,
//     Aggregates are all OrderedMap[V]; flattenAggregate iterates them
//     via .All() to preserve author order from the Aggregate's YAML
//     through to the flattened Nodes map.
//   - Determinism. Author-declared collections preserve Aggregate
//     author order through Build's pipeline. Nodes carry the
//     component / subsystem / sub-aggregate-import declaration sequence
//     via OrderedMap iteration; Edges carry the compositional cross-
//     product (component sequence × within-component `uses:` socket
//     order × within-socket alias order) augmented per node by
//     attachment order. The cross-package determinism contract
//     subsection below names the producer-and-consumer pair.
//   - Upstream guarantees. The resolver validates `uses` socket names
//     against usesSchema before this package runs; normalizeUses
//     therefore trusts that every socket name it parses is
//     catalog-recognised.
//
// Cross-package determinism contract. The IR builder (Step 3,
// internal/compiler/ir) consumes TopologyResult.Nodes and
// TopologyResult.Edges as inputs without re-validating their ordering. The
// contract has three commitments:
//
//   - TopologyResult.Nodes preserves Aggregate author order via
//     OrderedMap iteration over flattened components. Subsystem flattening,
//     sub-aggregate import merges, and the Build labels / boundary-path
//     phases all preserve author order; the OrderedMap shape is
//     load-bearing for IR JSON emission downstream.
//   - TopologyResult.Edges follows the compositional cross-product
//     (component sequence × within-component `uses:` socket order ×
//     within-socket alias order), augmented per node by attachment
//     order. Each layer is author-declared via an OrderedMap surface;
//     Build's step 4 walks flat.Nodes via OrderedMap.All() to drive the
//     outer cross-product, and per-node socket / alias / attachment
//     iteration preserves authoring sequence at every nested layer.
//   - The IR builder reads both as gospel. There is no in-package guard on
//     the consumer side that would catch a topology-side ordering
//     regression — a change here that flipped Edges back to alphabetical
//     order, or flipped Nodes to alphabetical, would silently break the
//     IR builder's edges-from-topology pass and the IRGraph node-emission
//     order. The corresponding consumer-side commitment lives in the IR
//     package's doc.go.
//
// Two flattened IDs meeting is two conditions, on the two paths that can
// produce it, and they are kept apart because one is an authoring mistake
// and the other is not.
//
// ErrSubAggregateNameCollision halts flattenAggregate if merging a loaded
// module's nodes would overwrite an entry already in the flat node map. That
// is impossible by current schema — every node a module contributes carries
// that module's name ahead of its own, instance names cannot contain the dot
// the two are joined with, and two modules imported under one name are one
// duplicate key — so the guard defends against schema loosening or validation
// bypass. The wrapped error carries the colliding ID, the dotted prefix path
// that produced it, and the existing entry's source aggregate.
//
// ErrSubsystemComponentIdentityCollision covers two subsystems of one
// Aggregate declaring a component under the same name, which is ordinary YAML
// the schema accepts: a subsystem contributes nothing to the identity a
// component carries, so both take one. That is not raised here. The walk
// records every declaration on flattenResult.DeclaringSubsystems, lets the
// node map keep the last of them, and ComponentIdentityIsDeclaredOnce — the
// rule declared over the flattened composition — reports the pair. Recording
// rather than halting is what makes the pair readable from the finished
// composition at all, since the node map holds one component per identity.
//
// Mutator-plugin contract. The MutatorInput / MutatorOutput /
// CompilationContext / MutatorGraph / MutatorNode types in
// wasm_types_gen.go are the ABI shape of the Step-2 mutator socket.
// AssembleMutatorInput (mutator.go) builds the labels-only payload the
// pipeline marshals to a configured mutator plugin, and ApplyMutatorPatches
// applies the plugin's returned JSON Patches to the node-label surface —
// the narrow auto-tagging exception — halting on any patch that reaches
// outside node labels.
//
// Downstream consumers. The IR builder (`internal/compiler/ir`) is the
// production reader of TopologyResult — see its package doc.go for the
// node / edge / attachment consumption pattern. Tests in this repo
// (`internal/compiler/fullstack_app_test.go`, `pipeline_integration_test.go`,
// `determinism_test.go`) construct topology results directly to exercise
// the pipeline end-to-end.
package topology
