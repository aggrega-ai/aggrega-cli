// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package validator implements JSON Schema 2020-12 validation for Aggrega entities.
//
// Three engines:
//   - Schema Engine: evaluates JSON Schema against developer values
//   - Inheritance Engine: walks an extends chain, gating it against cycles, a
//     depth bound and unresolvable parents, and composes the chain's JSON
//     Schemas. Inherited labels and annotations merge on the typed entity in
//     ResolveComponentExtends / ResolveFeatureExtends, not here.
//   - Split Schema Engine: projects an entity's featuresSchema attachment
//     slots into a JSON Schema describing the shape of a fill that may bind
//     each slot. The projection is compiled and gated on its own, not
//     composed into the entity's authored Schema content; usesSchema is
//     projected into no schema at all.
//
// All validation errors pass through an Aggrega error translation layer; raw
// validator library error strings never reach the user.
package validator
