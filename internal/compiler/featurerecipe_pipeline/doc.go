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
// A pipeline step is a distinct guest contract from the mutator socket, and the
// two are easy to conflate because both hand a plugin a graph-shaped payload and
// take patches back. The mutator runs once over the whole topology graph and
// returns opaque label patches; a pipeline step runs per inline `pipeline:` entry
// declared in a Recipe or FeatureRecipe, receives the keyed document set plus its
// step input, and returns key-addressed RFC 6902 patches alongside side effects
// and diagnostics. Different attachment point, different payload.
//
// Their patch surfaces are deliberately not unified either. A mutator's patches
// are an opaque slice the host pipes through; a pipeline step's are structured
// operations naming a target document by scope and key, because the apply engine
// applies them and restores the document set by snapshot when a step fails — so
// the operations have to be legible to the host that applies them, where a
// mutator's do not. Two guest contracts, two shapes, on purpose.
//
// This package also owns the runtime that speaks the ABI. [Walk] builds the
// keyed document set, evaluates each step's `with` (and its optional `when`
// gate), dispatches each Wasm guest, applies the returned patches to the
// named documents, restores the set by snapshot on a failed step, and
// accumulates the side effects. The renderer drives [Walk] for every Recipe
// and FeatureRecipe `pipeline:` at render time. The reference plugins
// exercise this contract; few third-party guests target it yet.
package featurerecipe_pipeline
