// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "go.yaml.in/yaml/v3"

//go:generate go run ../../internal/tools/typegen component_gen.go

// Component's struct declarations (Component, ComponentMetadata, ComponentSpec)
// are generated into component_gen.go from the published component-v1beta1
// schema; their godoc is the schema's descriptions. This hand-written companion
// holds the value types the generated spec references — UsesSchemaEntry (its
// per-socket valueSchema/infraSchema content-shape yaml.Node fields) and
// AttachmentSlot — both shared with Feature's usesSchema / featuresSchema.

// UsesSchemaEntry defines one socket category in a Component or Feature usesSchema.
// Schema: component-v1beta1.schema.json#/properties/spec/properties/usesSchema
//
// It is one type for both kinds, and the Component's socket is the wider of the
// two: Required, ValueSchema, InfraSchema and FeaturesSchema are declarable only
// on a Component's socket. The Feature schema refuses each of them in a
// Feature's usesSchema when the document's envelope is validated, so a Feature
// decoded from a document never carries one.
type UsesSchemaEntry struct {
	Selector string   `json:"selector" yaml:"selector"`
	Required bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Inject   []string `json:"inject,omitempty" yaml:"inject,omitempty"`

	// FeaturesSchema declares the attachment slots each connection wired
	// through this socket may fill, keyed by slot name, in the same slot shape
	// a Component declares in its own featuresSchema. A connection carrying an
	// attachment under any other name is refused when the build loads, and a
	// socket declaring none admits no attachment on its connections.
	FeaturesSchema OrderedMap[AttachmentSlot] `json:"featuresSchema,omitempty,omitzero" yaml:"featuresSchema,omitempty"`

	// ValueSchema is the per-edge developer-config contract for this socket:
	// the JSON Schema 2020-12 content an edge's `values` block (authored in
	// the Aggregate) is validated against. InfraSchema is the operator-config
	// counterpart for an edge's `infra` block (authored in the Release). Both
	// accept the same three authoring modes as the entity-level Schema field:
	// inline content, `$import` of a file beside the entity, and `$ref` into a
	// SchemaLibrary entry.
	//
	// The two indirections resolve at different points and are not one
	// mechanism. An `$import` is substituted when the entity is read from the
	// catalog, so by the time these fields are read they already hold the
	// imported content and no consumer sees the directive. A `$ref` survives
	// into the content and is expanded by compactform.Compile, against the
	// SchemaLibrary map its caller supplies.
	//
	// Omission means the socket declares no per-edge contract for that pillar;
	// any edge config for that pillar is then unvalidated (opt-in conformance).
	// These contracts are per-edge and are not composed into the consuming
	// node's own values schema.
	ValueSchema yaml.Node `json:"valueSchema,omitempty" yaml:"valueSchema,omitempty"`
	InfraSchema yaml.Node `json:"infraSchema,omitempty" yaml:"infraSchema,omitempty"`
}

// PillarNode returns the authored per-edge contract this socket declares for one
// pillar, and nil for a pillar no socket can constrain.
//
// It is the home of the pillar-to-field mapping, so a reader asking which field
// carries a socket's contract for a pillar asks here rather than switching on
// the pillar name in place. Two readers that each spelled out the mapping could
// come to disagree about it, and the disagreement would be silent: a pillar
// mapped to no field in one reader is indistinguishable there from a socket
// declaring no contract, which is the one case that is correct to pass over.
//
// A socket constrains only the two per-edge pillars. `provides` is a producer's
// published surface rather than a consumer's authored block, so an edge has
// nothing to write there and a socket has no contract to declare over it — that
// absence is why the nil case exists rather than being an omission.
//
// The returned node is the entry's own, not a copy, so a caller reads it and
// does not retain it past the entry's lifetime.
func (e *UsesSchemaEntry) PillarNode(pillar string) *yaml.Node {
	switch pillar {
	case "values":
		return &e.ValueSchema
	case "infra":
		return &e.InfraSchema
	default:
		return nil
	}
}

// ContractNodes returns every per-edge contract field this socket carries, in
// field order, whether or not the socket declares a contract there — a field the
// socket leaves unwritten is a zero node. It serves a reader that needs each
// contract body regardless of which pillar it constrains, such as one collecting
// the schema libraries those bodies reference; a reader asking about one pillar
// asks PillarNode, and TestUsesSchemaEntry_ContractNodesAreThePillarNodes holds the
// two to the same fields.
//
// The returned nodes are the entry's own, as PillarNode's are.
func (e *UsesSchemaEntry) ContractNodes() []*yaml.Node {
	return []*yaml.Node{&e.ValueSchema, &e.InfraSchema}
}

// AttachmentSlot declares one named attachment slot on a Component or Feature.
// A slot is a place an attachment is expected: the entity declares it, and a
// Release fills it by attaching a Feature under the slot's name. Declaring a
// slot never closes the attachment surface — a Release may attach a Feature
// under a name no slot declares, so one entity serves both a slot-filled
// attachment and an attachment shared at a broader scope.
//
// Inject is an allowlist of delivery mechanisms for the slot's material — empty
// means Black-Box (the material is consumed by a Recipe internally, not
// delivered to the workload).
//
// Required declares that the slot must end up bound. On a Component's slot the
// build halts when no attachment binds the slot's name on a node of that type,
// tested against the attachment set the node holds after the whole attachment
// cascade — so an attachment contributed at a scope the component inherits from
// satisfies the slot exactly as a fill authored on the component itself does. On
// a Feature's slot the field is a declaration only: an attachment carries no
// nested attachment block, so a Feature's own slots have no surface to be bound
// from.
// Schema: component-v1beta1.schema.json#/properties/spec/properties/featuresSchema
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
	// Sensitive declares that whatever binds this slot handles secret material.
	// It is a marker, not a constraint: it is carried onto the slot's projected
	// validation sub-schema as the `x-aggrega-sensitive` extension keyword so
	// the declaration is readable by inspection and policy tooling, and it is
	// read when deriving whether the attachment binding this slot handles
	// sensitive material. No fill is accepted or rejected because of it and no
	// rendered output changes.
	Sensitive bool `json:"sensitive,omitempty" yaml:"sensitive,omitempty"`
}
