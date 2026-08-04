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
// Extension keywords, both metadata-only. `x-aggrega-inject` (when the
// slot supplies an Inject allowlist) carries the list of permitted
// mechanism strings on the slot's sub-schema. The inject sub-schema
// constrains the mechanism NAMES to that allowlist and nothing more —
// each permitted mechanism's value is left as a bare array, because the
// delivery-entry shape is the entity schema's contract and restating it
// here would be a second home for one rule. A slot whose allowlist is
// empty (Black-Box) emits no `inject` property, so the closed sub-schema
// rejects a fill that dictates delivery for material the Recipe
// consumes internally.
//
// `x-aggrega-sensitive` (when the slot declares Sensitive) carries the
// slot author's claim that whatever binds this slot handles secret
// material. It constrains nothing: no fill is accepted or rejected
// because of it, and it changes no rendered output. Carrying it here
// makes one declaration readable from the entity's projected schema by
// inspection and policy tooling, which is the same standing
// `x-aggrega-inject` has.
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

		extra := map[string]any{}
		if slot.Sensitive {
			extra["x-aggrega-sensitive"] = true
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
			extra["x-aggrega-inject"] = append([]string(nil), slot.Inject...)
		}

		// Left nil when the slot declares neither marker, so a slot that
		// carries no extension keyword marshals without an empty object.
		if len(extra) > 0 {
			slotSchema.Extra = extra
		}

		properties[slotName] = slotSchema
		propertyOrder = append(propertyOrder, slotName)
	}

	// No AdditionalProperties constraint: the outer attachment object is
	// open by design (see the godoc) — an attachment name no slot declares
	// is a legal free attachment rather than an error. No Required either —
	// presence is not this schema's question.
	return &jsonschema.Schema{
		Type:          "object",
		Properties:    properties,
		PropertyOrder: propertyOrder,
	}
}
