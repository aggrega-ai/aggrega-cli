// Package types defines the typed in-memory boundaries between compiler stages.
// Every struct maps 1:1 to a canonical JSON schema in the aggrega-schemas repo.
//
// The 8-entity model: Aggregate, Component, Feature, Recipe, FeatureRecipe,
// Provider, Manifest, Release. Plus the internal IR graph and TemplateContext.
//
// All structs carry both JSON and YAML struct tags. Field names match schema
// property names exactly. Opaque developer payloads use map[string]interface{}.
package types
