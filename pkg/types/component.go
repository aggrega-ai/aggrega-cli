// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "go.yaml.in/yaml/v3"

//go:generate go run ../../internal/tools/typegen component_gen.go

// Component's struct declarations (Component, ComponentMetadata, ComponentSpec)
// are generated into component_gen.go from the published component-v1alpha1
// schema; their godoc is the schema's descriptions. This hand-written companion
// holds the value types the generated spec references — UsesSchemaEntry (its
// per-socket valueSchema/infraSchema content-shape yaml.Node fields) and
// AttachmentSlot — both shared with Feature's usesSchema / secretsSchema.

// UsesSchemaEntry defines one socket category in a Component or Feature usesSchema.
// Schema: component-v1alpha1.schema.json#/properties/spec/properties/usesSchema
type UsesSchemaEntry struct {
	Selector string   `json:"selector" yaml:"selector"`
	Required bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Inject   []string `json:"inject,omitempty" yaml:"inject,omitempty"`

	// ValueSchema is the per-edge developer-config contract for this socket:
	// the JSON Schema 2020-12 content an edge's `values` block (authored in
	// the Aggregate) is validated against. InfraSchema is the operator-config
	// counterpart for an edge's `infra` block (authored in the Release). Both
	// accept the same three authoring modes as the entity-level Schema field
	// (inline content / `$import` / `$ref`), dispatched by compactform.Compile.
	// Omission means the socket declares no per-edge contract for that pillar;
	// any edge config for that pillar is then unvalidated (opt-in conformance).
	// These contracts are per-edge and are not composed into the consuming
	// node's own values schema.
	ValueSchema yaml.Node `json:"valueSchema,omitempty" yaml:"valueSchema,omitempty"`
	InfraSchema yaml.Node `json:"infraSchema,omitempty" yaml:"infraSchema,omitempty"`
}

// AttachmentSlot declares one named attachment slot on a Component or Feature.
// A slot is a place an attachment is expected: the entity declares it, and a
// deployment fills it by attaching a Feature under the slot's name.
// Required=true means the slot must be filled. Inject is an allowlist of
// delivery mechanisms for the slot's material — empty means Black-Box (the
// material is consumed by a Recipe internally, not delivered to the workload).
// Schema: component-v1alpha1.schema.json#/properties/spec/properties/secretsSchema
type AttachmentSlot struct {
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Inject      []string `json:"inject,omitempty" yaml:"inject,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	// Selector is an optional Kubernetes-style label selector (same grammar and
	// pkg/selector engine as UsesSchemaEntry.Selector) constraining which
	// Feature may fill this slot. It is evaluated against the attached Feature's
	// own labels; a fill whose labels do not satisfy it halts. Empty declares no
	// constraint and accepts any fill.
	Selector string `json:"selector,omitempty" yaml:"selector,omitempty"`
	// Sensitive declares that the material this slot carries is sensitive. It is
	// a declaration only: no compiler behavior reads it, so setting it changes
	// nothing about validation, rendering, or emitted output.
	Sensitive bool `json:"sensitive,omitempty" yaml:"sensitive,omitempty"`
}
