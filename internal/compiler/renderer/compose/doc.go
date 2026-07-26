// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compose implements the compose provider engine: per-node Workload
// synthesis into Docker Compose service fragments, plus (in a follow-up track)
// a Release-level merger that composes fragments into a single
// docker-compose.yaml at the Release output root.
//
// Per-node contract (this file). SynthesizeWorkload returns one EmittedFile
// per node — `<nodeID>.compose-fragment.yaml` — containing a partial
// ComposeFile populated with the node's services, volumes, and networks. The
// fragment is self-contained: it round-trips through yaml.Unmarshal cleanly.
//
// Release-level merge. A BoundaryComposer/Release-merger
// reads the per-node fragments, deep-merges them, and emits a single
// docker-compose.yaml at the Release output root. Until that lands, fragments
// remain visible in the output tree.
//
// Translation discipline. Aggrega's Workload model carries Kubernetes-shaped
// semantics (PodSpec, probes, volume types, service types). Compose is
// shape-poorer; some translations are degraded with explicit warning comments
// in the rendered YAML. The engine never silently drops information — fields
// it cannot express either translate to a sensible compose primitive (with a
// comment explaining the degradation) or fail with TRANSLATION_ERROR.
package compose
