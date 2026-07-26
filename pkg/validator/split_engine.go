// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// jsonSchemaFalse returns the JSON Schema 2020-12 equivalent of a closed
// object — a schema that rejects every value. The library's *Schema lets us
// express "additionalProperties: false" as a Not-of-empty sub-schema
// (Not: &Schema{} rejects everything).
//
// A FUNCTION rather than a shared value, deliberately. The library resolves a
// schema document as a tree and rejects one where the same *Schema node appears
// at two positions, so a single shared sentinel placed on both a parent and a
// nested object makes the whole projection fail to compile — a validator built
// from it errors at construction instead of validating anything. Each closed
// position therefore gets its own node.
func jsonSchemaFalse() *jsonschema.Schema {
	return &jsonschema.Schema{Not: &jsonschema.Schema{}}
}

// GenerateBaseUsesJSONSchema builds a JSON Schema 2020-12 sub-schema
// from a component/feature usesSchema map. The returned schema
// represents the `uses` pillar's structural shape: each socket
// becomes a property; each socket's value is an object map keyed by
// instance name whose values carry the (target, inject?) shape.
//
// Extension keywords attach Aggrega-specific metadata for downstream
// tooling: `x-aggrega-selector` carries the original selector
// expression on each socket; `x-aggrega-inject` (when the entry
// supplies an Inject allowlist) carries the list of permitted
// mechanism strings. The selector itself is not enforced at the
// schema level — the analyzer (Step 4 selector matcher) handles that;
// the extension keyword is metadata-only for inspection + policy
// tooling.
//
// Closed-struct discipline: the parent `uses` schema rejects
// socket names not declared in usesSchema via
// additionalProperties: false (Schema-form: Not of empty). Author-
// order from the OrderedMap input drives PropertyOrder on the
// emitted schema, preserving the author's declared sequence.
func GenerateBaseUsesJSONSchema(usesSchema types.OrderedMap[types.UsesSchemaEntry]) *jsonschema.Schema {
	if usesSchema.Len() == 0 {
		return nil
	}

	propertyOrder := make([]string, 0, usesSchema.Len())
	properties := map[string]*jsonschema.Schema{}

	for socketName, entry := range usesSchema.All() {
		// Per-instance-name value: { target: string, inject?: { ... } }.
		instanceValue := &jsonschema.Schema{
			Type:                 "object",
			Required:             []string{"target"},
			Properties:           map[string]*jsonschema.Schema{"target": {Type: "string"}},
			AdditionalProperties: jsonSchemaFalse(),
		}
		instanceValue.PropertyOrder = []string{"target"}
		if len(entry.Inject) > 0 {
			injectProps := map[string]*jsonschema.Schema{}
			for _, mech := range entry.Inject {
				injectProps[mech] = &jsonschema.Schema{
					Type:                 "object",
					AdditionalProperties: &jsonschema.Schema{Type: "string"},
				}
			}
			instanceValue.Properties["inject"] = &jsonschema.Schema{
				Type:                 "object",
				Properties:           injectProps,
				AdditionalProperties: jsonSchemaFalse(),
			}
			instanceValue.PropertyOrder = append(instanceValue.PropertyOrder, "inject")
		}

		// Socket schema: object map keyed by instance name. Each
		// instance-name's value matches instanceValue (above) via
		// AdditionalProperties as a constraining sub-schema.
		socketSchema := &jsonschema.Schema{
			Type:                 "object",
			AdditionalProperties: instanceValue,
			Extra: map[string]any{
				"x-aggrega-selector": entry.Selector,
			},
		}
		if len(entry.Inject) > 0 {
			socketSchema.Extra["x-aggrega-inject"] = append([]string(nil), entry.Inject...)
		}

		properties[socketName] = socketSchema
		propertyOrder = append(propertyOrder, socketName)
	}

	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		PropertyOrder:        propertyOrder,
		AdditionalProperties: jsonSchemaFalse(),
	}
}

// GenerateBaseAttachmentsJSONSchema builds a JSON Schema 2020-12
// sub-schema from a component/feature featuresSchema map, describing
// the SHAPE of an attachment block: each slot becomes a property whose
// sub-schema constrains the fill that binds it.
//
// It makes no presence claim, and cannot. Whether a slot declared
// `required: true` ends up bound is a question about the attachment set
// a node holds after the whole attachment cascade, and an attachment
// arriving from a broader scope binds the slot while leaving the
// authored block that scope cascades into empty. A schema over an
// authored block cannot see that, so the emitted object carries no
// outer `required` list at all: the shape schema is structurally
// incapable of rejecting an unfilled slot, and the requirement is
// enforced against the resolved attachment set instead.
//
// Open outer object, closed slot sub-schemas. The returned parent
// object sets no additionalProperties constraint, so an attachment
// whose name no slot declares is admitted: declaring one slot must
// never forbid the attachments an entity did not enumerate, which is
// what lets a single entity serve both a slot-filled attachment and an
// attachment shared at a broader scope. Each declared slot's own
// sub-schema stays closed, so a fill that DOES bind a declared slot is
// still constrained to that slot's shape. The two are independent
// decisions at two depths; opening the parent does not relax the slots.
//
// The slot sub-schema's permitted keys mirror the attachment shape the
// entity schemas admit — `type`, `description`, `values`, `uses`, plus
// `inject` when the slot declares an allowlist — so a key that is legal
// on a free attachment stays legal on a slot fill.
//
// Extension keyword: `x-aggrega-inject` (when the slot supplies an
// Inject allowlist) carries the list of permitted mechanism strings
// on the slot's sub-schema. The inject sub-schema constrains the
// mechanism NAMES to that allowlist and nothing more — each permitted
// mechanism's value is left as a bare array, because the delivery-entry
// shape is the entity schema's contract and restating it here would be
// a second home for one rule. A slot whose allowlist is empty
// (Black-Box) emits no `inject` property, so the closed sub-schema
// rejects a fill that dictates delivery for material the Recipe
// consumes internally.
//
// Author-order from the OrderedMap input drives PropertyOrder.
func GenerateBaseAttachmentsJSONSchema(featuresSchema types.OrderedMap[types.AttachmentSlot]) *jsonschema.Schema {
	if featuresSchema.Len() == 0 {
		return nil
	}

	propertyOrder := make([]string, 0, featuresSchema.Len())
	properties := map[string]*jsonschema.Schema{}

	for slotName, slot := range featuresSchema.All() {
		// A slot constrains WHICH fill may bind it, not what the fill
		// configures: `infra` is deliberately absent from the permitted
		// set, because the attached Feature's own schema owns its values
		// and infra contract. A slot-level infraSchema/valueSchema would
		// be a second validation home for one rule. Do not "complete the
		// parallel" with usesSchema by adding it here.
		slotSchema := &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"type":        {Type: "string"},
				"description": {Type: "string"},
				"values":      {Type: "object"},
				"uses":        {Type: "object"},
			},
			AdditionalProperties: jsonSchemaFalse(),
		}
		slotSchema.PropertyOrder = []string{"type", "description", "values", "uses"}

		// slotSchema.Required is the slot-INTERNAL requirement — the fill's
		// own map must name the Feature it attaches. It is the only meaning
		// Required carries at schema level; the "this slot must end up
		// bound" meaning is not expressible over an authored block and is
		// not emitted here.
		if slot.Required {
			slotSchema.Required = []string{"type"}
		}

		if len(slot.Inject) > 0 {
			injectProps := map[string]*jsonschema.Schema{}
			for _, mech := range slot.Inject {
				injectProps[mech] = &jsonschema.Schema{Type: "array"}
			}
			slotSchema.Properties["inject"] = &jsonschema.Schema{
				Type:                 "object",
				Properties:           injectProps,
				AdditionalProperties: jsonSchemaFalse(),
			}
			slotSchema.PropertyOrder = append(slotSchema.PropertyOrder, "inject")
			slotSchema.Extra = map[string]any{
				"x-aggrega-inject": append([]string(nil), slot.Inject...),
			}
		}

		properties[slotName] = slotSchema
		propertyOrder = append(propertyOrder, slotName)
	}

	// No AdditionalProperties constraint: the outer attachment object is
	// open by design (see the godoc). The `uses` generator above closes
	// its parent because an undeclared socket name is a topology error;
	// an undeclared attachment name is a legal free attachment. No
	// Required either — presence is not this schema's question.
	return &jsonschema.Schema{
		Type:          "object",
		Properties:    properties,
		PropertyOrder: propertyOrder,
	}
}
