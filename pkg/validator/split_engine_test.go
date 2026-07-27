// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
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
	// A required slot does NOT land in the outer required list: presence is a
	// question about the post-cascade attachment set, not about the block this
	// schema describes.
	if len(out.Required) != 0 {
		t.Errorf("expected no outer required list; got %v", out.Required)
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

func TestGenerateBaseAttachmentsJSONSchema_NeverEmitsAPresenceRequirement(t *testing.T) {
	// The single-source rung for "a slot's declared requirement": presence is
	// enforced in exactly one place, over the node's post-cascade attachment
	// set. This projection must be structurally incapable of making the same
	// claim over an authored block, because the two would disagree — a slot
	// bound by an attachment cascading in from a broader scope is satisfied,
	// and an outer required list here would reject it. Every slot declared
	// required, and still no outer required list.
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("zulu", types.AttachmentSlot{Required: true})
	schema.Set("alpha", types.AttachmentSlot{Required: true})
	schema.Set("mike", types.AttachmentSlot{Required: true})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	if len(out.Required) != 0 {
		t.Errorf("projection emitted a presence requirement %v; presence belongs to the post-cascade check alone", out.Required)
	}
	// PropertyOrder preserves author sequence.
	expectedOrder := []string{"zulu", "alpha", "mike"}
	for i, want := range expectedOrder {
		if out.PropertyOrder[i] != want {
			t.Errorf("expected PropertyOrder[%d]=%q (author order); got %q", i, want, out.PropertyOrder[i])
		}
	}
}

func TestGenerateBaseAttachmentsJSONSchema_InjectModelsTheAuthoredShape(t *testing.T) {
	// The slot's contract is the set of permitted mechanism NAMES. Each
	// permitted mechanism's value is an array, because that is what an author
	// writes (`env: [{in: …, from: …}]`); modelling it as anything else would
	// reject every legal fill that authors delivery. The entry shape itself is
	// the entity schema's contract and is deliberately not restated here.
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("password", types.AttachmentSlot{Inject: []string{"env", "file"}})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	inject := out.Properties["password"].Properties["inject"]
	if inject == nil {
		t.Fatal("expected an inject sub-schema for a slot declaring an allowlist")
	}
	for _, mech := range []string{"env", "file"} {
		m := inject.Properties[mech]
		if m == nil {
			t.Fatalf("expected allowlisted mechanism %q to be permitted", mech)
		}
		if m.Type != "array" {
			t.Errorf("mechanism %q modelled as %q; an authored delivery block is an array", mech, m.Type)
		}
	}
	if _, ok := inject.Properties["arg"]; ok {
		t.Error("a mechanism outside the allowlist must not be permitted")
	}
	if inject.AdditionalProperties == nil || inject.AdditionalProperties.Not == nil {
		t.Errorf("expected the inject object closed so an un-allowlisted mechanism is rejected; got %+v", inject.AdditionalProperties)
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

func TestGenerateBaseAttachmentsJSONSchema_RequiredSlotFillMustNameAType(t *testing.T) {
	// A required slot's fill must name the Feature it attaches — the one
	// slot-internal meaning `required` keeps. Asserted at schema level because
	// that is the only level at which it is reachable: the entity envelope
	// schema requires `type` on every attachment at every name and halts first,
	// and the Go attachment struct always marshals the key. The constraint
	// stays in the projection because a description of a fill's shape that
	// omitted its one mandatory key would be an incomplete description.
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("root_password", types.AttachmentSlot{Required: true})
	v, err := jsonvalidator.New(GenerateBaseAttachmentsJSONSchema(slots))
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	bad := map[string]any{"root_password": map[string]any{"values": map[string]any{"origin": "x"}}}
	rpt, err := v.Validate(bad, &jsonvalidator.Context{Surround: bad, SkipPredicates: true})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if rpt == nil || len(rpt.Findings) == 0 {
		t.Error("a fill naming no type does not describe an attachment; the projection must reject it")
	}

	good := map[string]any{"root_password": map[string]any{"type": "core/secret-value"}}
	rpt2, err := v.Validate(good, &jsonvalidator.Context{Surround: good, SkipPredicates: true})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if rpt2 != nil && len(rpt2.Findings) != 0 {
		t.Errorf("a fill naming its type must be accepted; got %+v", rpt2.Findings)
	}
}

func TestGenerateBaseJSONSchemas_CompileAsValidators(t *testing.T) {
	// Both projections must be usable, not merely well-formed on paper. The
	// library resolves a schema document as a tree and refuses one where the
	// same *Schema node sits at two positions, so reusing a single closed-object
	// node across depths yields a projection that fails at validator
	// construction — and a caller that treats a construction failure as
	// "nothing to check" would then validate nothing at all, silently. Both
	// generators place a closed object at more than one depth, so both need this
	// guard.
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("root_password", types.AttachmentSlot{Required: true, Inject: []string{}})
	slots.Set("user_password", types.AttachmentSlot{Inject: []string{"env", "file"}})
	if _, err := jsonvalidator.New(GenerateBaseAttachmentsJSONSchema(slots)); err != nil {
		t.Errorf("attachment projection does not compile into a validator: %v", err)
	}

	sockets := types.OrderedMap[types.UsesSchemaEntry]{}
	sockets.Set("database", types.UsesSchemaEntry{Selector: "a=b", Inject: []string{"env"}})
	sockets.Set("cache", types.UsesSchemaEntry{Selector: "c=d"})
	if _, err := jsonvalidator.New(GenerateBaseUsesJSONSchema(sockets)); err != nil {
		t.Errorf("uses projection does not compile into a validator: %v", err)
	}
}

func TestGenerateBaseAttachmentsJSONSchema_OptionalSlotStillConstrainsShape(t *testing.T) {
	// An optional slot still shapes a fill it DOES receive: its sub-schema
	// stays closed and permits exactly the keys an attachment may carry, so
	// declaring `required: false` relaxes presence and nothing else.
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("password", types.AttachmentSlot{Required: false})

	out := GenerateBaseAttachmentsJSONSchema(schema)
	slot := out.Properties["password"]
	if slot.AdditionalProperties == nil || slot.AdditionalProperties.Not == nil {
		t.Errorf("expected an optional slot's sub-schema to stay closed; got %+v", slot.AdditionalProperties)
	}
	// The permitted key set mirrors the attachment shape the entity schemas
	// admit: a key legal on a free attachment stays legal on a slot fill, or
	// wiring the projection would reject authoring the envelope allows.
	for _, want := range []string{"type", "description", "values", "uses"} {
		if _, ok := slot.Properties[want]; !ok {
			t.Errorf("expected optional slot to permit %q", want)
		}
	}
}

func TestGenerateBaseAttachmentsJSONSchema_SensitiveSlotCarriesTheMarker(t *testing.T) {
	// A slot declared sensitive carries the marker onto its sub-schema; a slot
	// that declares nothing carries none. The negative half is what keeps the
	// marker meaningful — an unconditional one would say every slot in every
	// catalog handles secret material.
	schema := types.OrderedMap[types.AttachmentSlot]{}
	schema.Set("db_password", types.AttachmentSlot{Sensitive: true, Inject: []string{"env"}})
	schema.Set("blackbox_key", types.AttachmentSlot{Sensitive: true})
	schema.Set("tuning", types.AttachmentSlot{Inject: []string{"env"}})

	out := GenerateBaseAttachmentsJSONSchema(schema)

	for _, name := range []string{"db_password", "blackbox_key"} {
		slot := out.Properties[name]
		if got := slot.Extra["x-aggrega-sensitive"]; got != true {
			t.Errorf("%s: expected x-aggrega-sensitive=true; got %v (%T) in %v", name, got, got, slot.Extra)
		}
	}
	// A sensitive Black-Box slot still gets the marker and still gets no
	// inject property — the two declarations are independent.
	if _, ok := out.Properties["blackbox_key"].Extra["x-aggrega-inject"]; ok {
		t.Errorf("Black-Box slot must carry no inject allowlist; got %v", out.Properties["blackbox_key"].Extra)
	}
	if _, ok := out.Properties["tuning"].Extra["x-aggrega-sensitive"]; ok {
		t.Errorf("an undeclared slot must carry no marker; got %v", out.Properties["tuning"].Extra)
	}
	// A slot declaring neither marker carries no Extra map at all, so the
	// sub-schema marshals without an empty trailing object.
	plain := types.OrderedMap[types.AttachmentSlot]{}
	plain.Set("plain", types.AttachmentSlot{})
	if extra := GenerateBaseAttachmentsJSONSchema(plain).Properties["plain"].Extra; extra != nil {
		t.Errorf("a slot declaring no extension keyword should leave Extra nil; got %v", extra)
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"x-aggrega-sensitive":true`) {
		t.Errorf("expected the marker in the marshaled projection; got %s", b)
	}
	t.Logf("projected slot schema: %s", b)

	// The projection must still compile into a working validator: an
	// unrecognised extension keyword that broke construction would silently
	// disable the slot-shape check rather than announce itself.
	if _, err := jsonvalidator.New(out); err != nil {
		t.Errorf("projection carrying the marker does not compile into a validator: %v", err)
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
