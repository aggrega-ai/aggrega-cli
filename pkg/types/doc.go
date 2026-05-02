// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package types defines the typed in-memory boundaries between compiler
// stages and is the leaf package consumed by every other package in the
// compilation pipeline. Every struct maps 1:1 to a canonical JSON schema
// in the aggrega-schemas repo; field names match schema property names
// exactly and every struct carries paired JSON and YAML tags. Opaque
// developer payloads land in map[string]interface{}.
//
// The 8-Entity Taxonomy authored against this package: Aggregate,
// Component, Feature, Recipe, FeatureRecipe, Provider, Manifest, Release.
// The IR graph (IRGraph plus per-graph types in ir.go) and the
// TemplateContext consumed by template rendering are also declared here.
//
// OrderedMap[V] is the foundational generic for any author-declared
// collection — every YAML-driven map field uses it so that author order
// survives parse and emission round-trip, a load-bearing property for
// the deterministic-output invariant.
package types
