// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package validator implements JSON Schema 2020-12 validation for Aggrega entities.
//
// Three engines:
//   - Schema Engine: evaluates JSON Schema against developer values
//   - Inheritance Engine: Two-Tiered Merge (YAML metadata deep-merge + JSON Schema
//     composition via extends)
//   - Split Schema Engine: synthesises a base JSON Schema from usesSchema and
//     composes it with the entity's authored Schema content via the merger phase
//
// All validation errors pass through an Aggrega error translation layer; raw
// validator library error strings never reach the user.
package validator
