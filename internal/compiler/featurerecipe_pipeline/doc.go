// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package featurerecipe_pipeline is the home of the FeatureRecipe-Pipeline
// (FRP) Wasm-guest ABI: the JSON contract a pipeline-step plugin binds
// against when a Recipe or FeatureRecipe declares an inline `pipeline:` of
// Wasm-plugin steps.
//
// A FeatureRecipe pipeline is an ordered list of steps. Each step is a
// Wasm guest that receives the in-progress (cumulative) rendered AST plus a
// step-local input, and returns RFC 6902 JSON-Patch operations against that
// AST together with side effects and diagnostics. The orchestrator walks
// the steps in order, applies each step's patches to the cumulative AST,
// accumulates the side effects, and hands the next step the updated AST.
//
// The motivating shape is deployment-time credential management. A pipeline
// step cannot fetch a secret at compile time — the compiler performs no
// network access and produces byte-identical output for identical input.
// Instead, a fetcher step writes a CRD that drives a runtime controller to
// fetch a credential from a vault at runtime, and a later step patches the
// workload AST to consume the (runtime-resolved) credential. The build
// output stays reproducible; the runtime controller does the fetching at
// deploy time.
//
// **Status: contract definition only — not yet executed.** This package
// ships the FRP-step ABI types alone. The FRP runtime orchestrator that
// walks the pipeline steps, dispatches each Wasm guest, applies the JSON
// patches to the cumulative AST, inverts them on failure, and emits the
// accumulated side effects lands in later work; no production caller in
// this repository constructs or consumes these payloads today. The shapes
// are deliberately stable surface so that work can bind against them now.
// Until the orchestrator ships, the AST handoff these types describe is
// unvalidated by a working pipeline.
package featurerecipe_pipeline
