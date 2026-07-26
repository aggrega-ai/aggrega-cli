// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"sort"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// jsonSchemaFalseSentinel is the JSON Schema 2020-12 equivalent of a
// closed object — a schema that rejects every value. The library's
// *Schema lets us express "additionalProperties: false" as a Not-of-
// empty sub-schema (Not: &Schema{} rejects everything).
var jsonSchemaFalseSentinel = &jsonschema.Schema{Not: &jsonschema.Schema{}}

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
			AdditionalProperties: jsonSchemaFalseSentinel,
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
				AdditionalProperties: jsonSchemaFalseSentinel,
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
		AdditionalProperties: jsonSchemaFalseSentinel,
	}
}

// GenerateBaseAttachmentsJSONSchema builds a JSON Schema 2020-12
// sub-schema from a component/feature featuresSchema map. Each slot
// becomes a property in the attachment object; required slots land in
// the parent's Required: []string array (alphabetically sorted for
// determinism); optional slots stay outside.
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
// Extension keyword: `x-aggrega-inject` (when the slot supplies an
// Inject allowlist) carries the list of permitted mechanism strings
// on the slot's sub-schema. The inject sub-schema mirrors the
// declared allowlist as a closed-properties object — undeclared
// mechanisms get rejected at the schema level.
//
// Author-order from the OrderedMap input drives PropertyOrder.
func GenerateBaseAttachmentsJSONSchema(featuresSchema types.OrderedMap[types.AttachmentSlot]) *jsonschema.Schema {
	if featuresSchema.Len() == 0 {
		return nil
	}

	propertyOrder := make([]string, 0, featuresSchema.Len())
	properties := map[string]*jsonschema.Schema{}
	var required []string

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
				"type":   {Type: "string"},
				"values": {Type: "object"},
				"uses":   {Type: "object"},
			},
			AdditionalProperties: jsonSchemaFalseSentinel,
		}
		slotSchema.PropertyOrder = []string{"type", "values", "uses"}

		// slotSchema.Required carries the slot-internal requirement
		// (the slot's value-map must declare a `type` field) when the
		// slot is required at the parent level. The parent-level
		// slot-requirement is handled separately by appending to the
		// outer Required slice below. Same Required field name, two
		// distinct meanings at different schema depths.
		if slot.Required {
			slotSchema.Required = []string{"type"}
		}

		if len(slot.Inject) > 0 {
			injectProps := map[string]*jsonschema.Schema{}
			for _, mech := range slot.Inject {
				injectProps[mech] = &jsonschema.Schema{
					Type:                 "object",
					AdditionalProperties: &jsonschema.Schema{Type: "string"},
				}
			}
			slotSchema.Properties["inject"] = &jsonschema.Schema{
				Type:                 "object",
				Properties:           injectProps,
				AdditionalProperties: jsonSchemaFalseSentinel,
			}
			slotSchema.PropertyOrder = append(slotSchema.PropertyOrder, "inject")
			slotSchema.Extra = map[string]any{
				"x-aggrega-inject": append([]string(nil), slot.Inject...),
			}
		}

		properties[slotName] = slotSchema
		propertyOrder = append(propertyOrder, slotName)
		if slot.Required {
			required = append(required, slotName)
		}
	}

	if len(required) > 0 {
		sort.Strings(required)
	}

	// No AdditionalProperties constraint: the outer attachment object is
	// open by design (see the godoc). The `uses` generator above closes
	// its parent because an undeclared socket name is a topology error;
	// an undeclared attachment name is a legal free attachment.
	return &jsonschema.Schema{
		Type:          "object",
		Properties:    properties,
		PropertyOrder: propertyOrder,
		Required:      required,
	}
}
