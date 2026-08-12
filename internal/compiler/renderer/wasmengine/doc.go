// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package wasmengine is a host-only adapter that implements the host renderer's
// Provider and WorkloadSynthesizer interfaces by rendering one node through the
// helm provider Wasm guest across the provider render ABI. It projects a node's
// values, provenance, annotations, and either its chart source listing or its
// evaluated workload into a RenderNodeInput, invokes the guest's render_node
// export through the production describe-gated loader, and folds the returned
// files back into the interface's own output shape. It returns the guest's
// per-file output verbatim — each file's path, bytes, and declared format — so
// the emitted bytes are the guest's own and this adapter reinterprets none of
// them; a duplicate output path is a loud error rather than a silent overwrite.
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
// The adapter is host-only Go and carries no wasip1 build tag. It stays free of
// any provider package import: every piece of provider knowledge sits in the
// guest. For a sourced node the host walks the acquired source root into a
// name-only listing carrying no file content, and the guest resolves that
// listing itself and asks the host to pack the selection it chooses. It is the
// provider engine the hybrid dispatch selects for a provider that resolves to a
// Wasm rendering Plugin.
package wasmengine
