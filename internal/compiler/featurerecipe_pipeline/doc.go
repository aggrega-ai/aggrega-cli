// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package featurerecipe_pipeline is the home of the FeatureRecipe-Pipeline
// (FRP) Wasm-guest ABI: the JSON contract a pipeline-step plugin binds
// against when a Recipe or FeatureRecipe declares an inline `pipeline:` of
// Wasm-plugin steps.
//
// A pipeline is an ordered list of steps. Each step is a Wasm guest that
// receives a keyed set of the node's rendered documents — under named
// roots, `own` always and `host` when the step is a component-scope
// FeatureRecipe attached to a host component — plus a step-local input. A
// step does one or both of two operations: it PATCHes an existing document
// (an RFC 6902 JSON-Patch rooted at the document's scope and key) and it
// EMITs a new document (a side effect). The guest returns its patches,
// side effects, and diagnostics; the apply engine applies the patches to
// the named documents by key and accumulates the side effects.
//
// Selection is by content, never by filename. The document keys are
// render-time products of the provider engine, so a plugin author cannot
// know them ahead of time. A guest iterates the handed set, inspects each
// document's content, selects the entries its domain logic targets, learns
// those keys at runtime, and roots its patches at the selected key. The
// host hands the whole set and applies returned patches by key; it never
// reads a document's content or infers the guest's intent.
//
// The motivating shape is deployment-time credential management. A pipeline
// step cannot fetch a secret at compile time — the compiler performs no
// network access and produces byte-identical output for identical input.
// Instead, a fetcher step EMITs a CRD that drives a runtime controller to
// fetch a credential from a vault at runtime, and a later step PATCHes the
// workload document to consume the (runtime-resolved) credential. The build
// output stays reproducible; the runtime controller does the fetching at
// deploy time.
//
// **Status: contract definition only — not yet executed.** This package
// ships the FRP-step ABI types alone. The runtime that builds the keyed
// document set, dispatches each Wasm guest, applies the patches to the
// named documents, restores the set by snapshot on failure, and emits the
// accumulated side effects lands in later work; no production caller in
// this repository constructs or consumes these payloads today. The shapes
// are deliberately stable surface so that work can bind against them now.
// Until the runtime ships, the document handoff these types describe is
// unvalidated by a working pipeline.
package featurerecipe_pipeline
