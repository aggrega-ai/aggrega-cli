// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package resolver implements resolution, the first stage of the compile
// pipeline: turn the
// authored YAML triple (Aggregate / Release / Manifest) into a resolved
// in-memory artifact that downstream phases (topology, IR, renderer, analyzer)
// consume as their input.
//
// The package exposes two coupled but distinct entry points:
//
//   - Resolve produces the mechanical artifacts: parsed Aggregate / Release /
//     Manifest plus per-component boundary infra context. It performs YAML
//     loading, parameters $import: resolution + inline merge, Go-template
//     interpolation against the merged parameter map, instance-name
//     validation, and boundary infra wiring.
//
//   - RunGuards executes the semantic checks that need a catalog lookup
//     (uses-socket existence, self-inject allowlist, inject key collisions,
//     Release feature merge, required uses-socket population, schema
//     conformance). Callers run RunGuards after Resolve and before topology.
//
// JSON Schema parsing and validation live in pkg/validator. The resolver
// surfaces catalog state to RunGuards; the schema-content validation flow
// runs in the validator pipeline against the entity's authored Schema
// content composed with usesSchema / featuresSchema bridge synthesis.
//
// The Resolve pipeline ordering is:
//
//  1. Load Release.
//  2. Load Manifest.
//  3. Load Release.spec.parameters.$import: target if populated (single
//     source file).
//  4. Merge the loaded values with Release.spec.parameters inline content
//     (single-source-only design — exactly one mode is populated).
//  5. Read raw Aggregate YAML.
//  6. Interpolate parameters into the raw bytes via Go template + Sprig.
//  7. Unmarshal the interpolated bytes into a typed Aggregate.
//  8. Validate instance names (returns the first violation only).
//  9. Wire per-component boundary infra from Release boundaries.
//
// The merged parameter map stays live for the whole resolve scope, because
// operator-authored path templates read `.parameters.<key>` while artifact
// paths are being emitted. Its reachability is bounded by that scope and the
// bound is structural: ResolveAndUse is the only exported producer of a
// resolved view, and it hands that view to a callback rather than returning
// it, so no caller can hold the map once ResolveAndUse returns.
package resolver
