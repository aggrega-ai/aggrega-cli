// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ----- JSON Schema bridge synthesis -----

func TestGenerateBaseUsesJSONSchema_Simple(t *testing.T) {
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("databases", types.UsesSchemaEntry{
		Selector: `component.aggrega.ai/type in (resource, stateful-server)`,
		Inject:   []string{"env", "file"},
	})

	out := GenerateBaseUsesJSONSchema(schema)
	if out == nil {
		t.Fatal("expected non-nil schema; got nil")
	}
	if out.Type != "object" {
		t.Errorf("expected root type=object; got %q", out.Type)
	}
	socket, ok := out.Properties["databases"]
	if !ok {
		t.Fatalf("expected databases socket; got %v", out.Properties)
	}
	sel, ok := socket.Extra["x-aggrega-selector"].(string)
	if !ok || sel != `component.aggrega.ai/type in (resource, stateful-server)` {
		t.Errorf("expected x-aggrega-selector extension; got %v", socket.Extra["x-aggrega-selector"])
	}
	inj, ok := socket.Extra["x-aggrega-inject"].([]string)
	if !ok || len(inj) != 2 || inj[0] != "env" || inj[1] != "file" {
		t.Errorf("expected x-aggrega-inject=[env file]; got %v", socket.Extra["x-aggrega-inject"])
	}
	// Each socket's value is a map keyed by instance name; the
	// AdditionalProperties slot carries the (target, inject?) shape.
	if socket.AdditionalProperties == nil {
		t.Fatalf("expected socket.AdditionalProperties to constrain instance values; got nil")
	}
	if socket.AdditionalProperties.Type != "object" {
		t.Errorf("expected instance value type=object; got %q", socket.AdditionalProperties.Type)
	}
	target, ok := socket.AdditionalProperties.Properties["target"]
	if !ok || target.Type != "string" {
		t.Errorf("expected target: string on instance value; got %v", target)
	}
	if len(socket.AdditionalProperties.Required) != 1 || socket.AdditionalProperties.Required[0] != "target" {
		t.Errorf("expected target in Required; got %v", socket.AdditionalProperties.Required)
	}
	if _, ok := socket.AdditionalProperties.Properties["inject"]; !ok {
		t.Errorf("expected inject block on instance value when allowlist supplied")
	}
}

func TestGenerateBaseUsesJSONSchema_NoInject(t *testing.T) {
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("apis", types.UsesSchemaEntry{Selector: `component.aggrega.ai/type in (server)`})

	out := GenerateBaseUsesJSONSchema(schema)
	if out == nil {
		t.Fatal("expected non-nil schema; got nil")
	}
	socket := out.Properties["apis"]
	if _, ok := socket.Extra["x-aggrega-inject"]; ok {
		t.Errorf("expected no x-aggrega-inject for Black-Box pattern (no allowlist); got %v", socket.Extra["x-aggrega-inject"])
	}
	if _, ok := socket.AdditionalProperties.Properties["inject"]; ok {
		t.Errorf("expected no inject sub-schema on instance value when allowlist absent")
	}
}

func TestGenerateBaseUsesJSONSchema_Empty(t *testing.T) {
	out := GenerateBaseUsesJSONSchema(types.OrderedMap[types.UsesSchemaEntry]{})
	if out != nil {
		t.Errorf("expected nil for empty usesSchema; got %v", out)
	}
}

func TestGenerateBaseUsesJSONSchema_AuthorOrder(t *testing.T) {
	// OrderedMap iteration is author-order-deterministic by contract.
	// The emitted PropertyOrder must reflect the declared sequence
	// (z, a, m) rather than alphabetic (a, m, z).
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("z_socket", types.UsesSchemaEntry{Selector: "s1"})
	schema.Set("a_socket", types.UsesSchemaEntry{Selector: "s2"})
	schema.Set("m_socket", types.UsesSchemaEntry{Selector: "s3"})

	out := GenerateBaseUsesJSONSchema(schema)
	expected := []string{"z_socket", "a_socket", "m_socket"}
	if len(out.PropertyOrder) != 3 {
		t.Fatalf("expected PropertyOrder len=3; got %v", out.PropertyOrder)
	}
	for i, want := range expected {
		if out.PropertyOrder[i] != want {
			t.Errorf("expected PropertyOrder[%d]=%q; got %q", i, want, out.PropertyOrder[i])
		}
	}
}

func TestGenerateBaseUsesJSONSchema_ClosedAdditionalProperties(t *testing.T) {
	// Closed-struct discipline: the parent's AdditionalProperties
	// rejects every value (Not of empty schema). Marshaling produces
	// the schema-form additionalProperties: {"not": {}} which is the
	// JSON Schema 2020-12 equivalent of closed objects.
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("databases", types.UsesSchemaEntry{Selector: "s1"})

	out := GenerateBaseUsesJSONSchema(schema)
	if out.AdditionalProperties == nil || out.AdditionalProperties.Not == nil {
		t.Errorf("expected closed parent (AdditionalProperties.Not non-nil); got %+v", out.AdditionalProperties)
	}
}

func TestGenerateBaseAttachmentsJSONSchema_RequiredVsOptional(t *testing.T) {
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("db_password", types.AttachmentSlot{Required: true, Inject: []string{"env"}})
	schema.Set("api_token", types.AttachmentSlot{Required: false, Inject: []string{"file"}})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	if out == nil {
		t.Fatal("expected non-nil schema; got nil")
	}
	// Required slots land in Required: []string (sorted alphabetically).
	if len(out.Required) != 1 || out.Required[0] != "db_password" {
		t.Errorf("expected Required=[db_password]; got %v", out.Required)
	}
	dbSlot := out.Properties["db_password"]
	if dbSlot == nil {
		t.Fatal("expected db_password slot; got nil")
	}
	if len(dbSlot.Required) != 1 || dbSlot.Required[0] != "type" {
		t.Errorf("expected required slot's type field marked required; got %v", dbSlot.Required)
	}
	apiSlot := out.Properties["api_token"]
	if apiSlot == nil {
		t.Fatal("expected api_token slot; got nil")
	}
	if len(apiSlot.Required) != 0 {
		t.Errorf("expected optional slot to have no Required; got %v", apiSlot.Required)
	}
	// Both slots get x-aggrega-inject extension.
	if _, ok := dbSlot.Extra["x-aggrega-inject"]; !ok {
		t.Errorf("expected db_password.x-aggrega-inject; got %v", dbSlot.Extra)
	}
	if _, ok := apiSlot.Extra["x-aggrega-inject"]; !ok {
		t.Errorf("expected api_token.x-aggrega-inject; got %v", apiSlot.Extra)
	}
}

func TestGenerateBaseAttachmentsJSONSchema_RequiredSortedAlphabetically(t *testing.T) {
	// Author order is preserved for PropertyOrder; Required is sorted
	// alphabetically post-construction for cross-build byte-identity.
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("zulu", types.AttachmentSlot{Required: true})
	schema.Set("alpha", types.AttachmentSlot{Required: true})
	schema.Set("mike", types.AttachmentSlot{Required: true})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	expectedRequired := []string{"alpha", "mike", "zulu"}
	if len(out.Required) != 3 {
		t.Fatalf("expected 3 required slots; got %v", out.Required)
	}
	for i, want := range expectedRequired {
		if out.Required[i] != want {
			t.Errorf("expected Required[%d]=%q (sorted); got %q", i, want, out.Required[i])
		}
	}
	// PropertyOrder preserves author sequence.
	expectedOrder := []string{"zulu", "alpha", "mike"}
	for i, want := range expectedOrder {
		if out.PropertyOrder[i] != want {
			t.Errorf("expected PropertyOrder[%d]=%q (author order); got %q", i, want, out.PropertyOrder[i])
		}
	}
}

func TestGenerateBaseAttachmentsJSONSchema_BlackboxNoInject(t *testing.T) {
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("internal_only", types.AttachmentSlot{Required: true})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	slot := out.Properties["internal_only"]
	if _, ok := slot.Extra["x-aggrega-inject"]; ok {
		t.Errorf("expected no x-aggrega-inject for Black-Box (Inject empty); got %v", slot.Extra)
	}
	if _, ok := slot.Properties["inject"]; ok {
		t.Errorf("expected no inject sub-schema on Black-Box slot")
	}
}

func TestGenerateBaseAttachmentsJSONSchema_Empty(t *testing.T) {
	out := GenerateBaseAttachmentsJSONSchema(types.OrderedMap[types.AttachmentSlot]{})
	if out != nil {
		t.Errorf("expected nil for empty featuresSchema; got %v", out)
	}
}

func TestGenerateBaseAttachmentsJSONSchema_OpenOuterClosedSlots(t *testing.T) {
	// The two depths are independent decisions and both matter:
	//
	//   outer object OPEN  — an attachment name no slot declares is a legal
	//                        free attachment, so declaring one slot must not
	//                        forbid the attachments the entity did not
	//                        enumerate.
	//   slot sub-schema CLOSED — a fill that DOES bind a declared slot is
	//                        still constrained to that slot's shape.
	//
	// Asserting only one of the pair would let the other regress silently:
	// closing the outer object breaks free attachments, and opening a slot
	// sub-schema drops the shape constraint on every fill.
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("password", types.AttachmentSlot{Inject: []string{"env"}})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	if out == nil {
		t.Fatal("expected non-nil schema; got nil")
	}
	if out.AdditionalProperties != nil {
		t.Errorf("expected OPEN outer object (no additionalProperties constraint); got %+v", out.AdditionalProperties)
	}
	slot := out.Properties["password"]
	if slot == nil {
		t.Fatal("expected password slot; got nil")
	}
	if slot.AdditionalProperties == nil || slot.AdditionalProperties.Not == nil {
		t.Errorf("expected CLOSED slot sub-schema (AdditionalProperties.Not non-nil); got %+v", slot.AdditionalProperties)
	}
	// A slot constrains which fill may bind it, not what the fill
	// configures; infra stays absent so the rule has one home on the
	// attached Feature's own schema.
	if _, ok := slot.Properties["infra"]; ok {
		t.Errorf("expected no infra property on a slot sub-schema; the attached Feature's schema owns that contract")
	}
}

func TestGenerateBaseAttachmentsJSONSchema_OptionalSlotStillConstrainsShape(t *testing.T) {
	// slot.Required drives two things at two schema depths: membership of
	// the outer required list, AND the slot-internal "a fill must declare a
	// type" requirement. An optional slot drops the former and keeps the
	// latter's parent closed-ness, so a fill it DOES receive is still
	// shape-constrained rather than free-form.
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("password", types.AttachmentSlot{Required: false})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	if len(out.Required) != 0 {
		t.Errorf("expected optional slot absent from the outer required list; got %v", out.Required)
	}
	slot := out.Properties["password"]
	if slot.AdditionalProperties == nil || slot.AdditionalProperties.Not == nil {
		t.Errorf("expected an optional slot's sub-schema to stay closed; got %+v", slot.AdditionalProperties)
	}
	for _, want := range []string{"type", "values", "uses"} {
		if _, ok := slot.Properties[want]; !ok {
			t.Errorf("expected optional slot to permit %q", want)
		}
	}
}

func TestGenerateBaseUsesJSONSchema_MarshalsCleanly(t *testing.T) {
	// The emitted *jsonschema.Schema must marshal to valid JSON for
	// downstream consumers (the validator adapter, golden tests).
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("databases", types.UsesSchemaEntry{Selector: "sel", Inject: []string{"env"}})

	out := GenerateBaseUsesJSONSchema(schema)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, frag := range []string{"databases", "x-aggrega-selector", "x-aggrega-inject", "target"} {
		if !strings.Contains(string(b), frag) {
			t.Errorf("expected %q in marshaled output; got %s", frag, b)
		}
	}
}
