// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package resolver implements Step 1 of the compile pipeline: turn the
// authored YAML triple (Aggregate / Release / Manifest) into a resolved
// in-memory artifact that downstream phases (topology, IR, renderer, analyzer)
// consume as their input.
//
// The package exposes two coupled but distinct entry points:
//
//   - Resolve produces the mechanical artifacts: parsed Aggregate / Release /
//     Manifest plus per-component boundary infra context. It performs YAML
//     loading, parametersFrom merging, Go-template interpolation against the
//     merged parameter map, instance-name validation, boundary infra wiring,
//     and active parameter-map purge.
//
//   - RunGuards executes the semantic checks that need a catalog lookup
//     (uses-socket existence, self-inject allowlist, inject key collisions,
//     credential label partition, requiredFeatures completeness, Release
//     feature merge, cueSchema pillar validation, required uses-socket
//     population). Callers run RunGuards after Resolve and before topology.
//
// CUE schema parsing and validation live in pkg/validator. The resolver
// invokes pkg/validator only from within RunGuards' cueSchema arm; it does
// not parse CUE itself.
//
// The Resolve pipeline ordering is:
//
//  1. Load Release.
//  2. Load Manifest.
//  3. Load parametersFrom external files (deep-merge, later overrides earlier).
//  4. Merge Release.parameters over external parameters (Release wins).
//  5. Read raw Aggregate YAML.
//  6. Interpolate parameters into the raw bytes via Go template + Sprig.
//  7. Unmarshal the interpolated bytes into a typed Aggregate.
//  8. Validate instance names (returns the first violation only).
//  9. Wire per-component boundary infra from Release boundaries.
//  10. Actively zero-and-nil the parameter map before returning.
//
// Step 10 removes parameter values from memory before the result is returned
// so secret-bearing or environment-specific values do not linger past the
// interpolation step.
package resolver
