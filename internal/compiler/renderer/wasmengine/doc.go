// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package wasmengine is a host-only adapter that implements the host renderer's
// Engine and WorkloadSynthesizer interfaces by rendering one node through the
// helm provider Wasm guest across the provider render ABI. It projects a node's
// values, provenance, annotations, and either its chart source or its evaluated
// workload into a RenderNodeInput, invokes the guest's render_node export through
// the production describe-gated loader, and folds the returned files back into
// the interface's own output shape. Given identical input it reproduces the
// in-tree helm engine byte-for-byte, because both surfaces drive the same
// filesystem-free render and synthesis core — one in-process, one across the ABI.
//
// The adapter loads and closes a fresh guest plugin per Render or
// SynthesizeWorkload call: it resolves the plugin's bytes through the injected
// lookup, hands them to the shared per-build loader, runs the load-and-describe
// gate (so the guest's self-reported ABI and exports are validated before
// dispatch), invokes render_node, and closes the plugin before returning. Each
// call is a self-contained load-describe-invoke-close cycle against its own
// plugin instance, so nodes stay isolated. The loader is shared across the
// build and carries a compilation cache, so the guest's machine code compiles
// once for the whole build rather than once per node — a cost amortization
// only; it changes neither isolation nor output.
//
// The adapter is host-only Go — it imports the host-only helm package to project
// a chart source and to reuse the host's per-slot secret projection — and carries
// no wasip1 build tag. It is the provider engine the hybrid dispatch selects for
// a provider whose catalog Provider resolves to a Wasm rendering plugin.
package wasmengine
