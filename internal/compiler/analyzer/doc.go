// Package analyzer implements Step 4 of the Aggrega compiler pipeline:
// the Static Analyzer & Validation Gate.
//
// It consumes a types.IRGraph and catalog lookup to produce selector validation
// verdicts, precalculated_paths (cycle-safe DFS), and the policy-validator.wasm
// input payload. All output arrays are deterministic (sorted adjacency lists,
// sorted errors, sorted features).
package analyzer
