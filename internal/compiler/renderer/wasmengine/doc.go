// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package wasmengine is a host-only adapter that implements the host renderer's
// Provider interface, and the optional render interfaces beside it, by rendering
// one node through a provider's Wasm guest across the provider render ABI. It
// projects a node's values, provenance, annotations, and its source listing, its
// evaluated workload or its artifacts into a RenderNodeInput, invokes the guest's
// export through the production describe-gated loader, and folds the returned
// files back into the interface's own output shape. It returns the guest's
// per-file output verbatim — each file's path, bytes, and declared format — so
// the emitted bytes are the guest's own and this adapter reinterprets none of
// them; a duplicate output path is a loud error rather than a silent overwrite.
//
// The guest's describe() report, its validation and the load-gate cross-checks
// are a pure function of the guest's bytes, so they run once per provider and
// are kept. Each crossing then acquires an instance: a fresh one, closed before
// the call returns, unless the guest declares reusable_instance, in which case
// one instance serves every node and its source root is set again per crossing.
// A guest that makes no reuse claim therefore keeps nodes isolated. The loader is
// shared across the build and carries a compilation cache, so the guest's
// machine code compiles once for the whole build rather than once per node — a
// cost amortization only; it changes neither isolation nor output.
//
// The adapter is host-only Go and carries no wasip1 build tag. It stays free of
// any provider package import: every piece of provider knowledge sits in the
// guest. For a sourced node the host walks the acquired source root into a
// name-only listing carrying no file content, and the guest resolves that
// listing itself and asks the host to pack the selection it chooses. It is the
// provider engine the hybrid dispatch selects for a provider that resolves to a
// Wasm rendering Plugin.
package wasmengine
