// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// Every host-function reply envelope that carries error_code references the
// shared error-code vocabulary rather than declaring a bare string, so the
// closed set the field's description promises is a constraint the wire contract
// states rather than prose beside an unconstrained value. These tests hold that
// reference by driving values through a JSON Schema implementation and reading
// the outcome: a declared code is accepted and an invented one is refused, on
// every envelope that carries the field.
//
// What this does and does not establish. The reply envelopes are produced by
// the host and consumed by a guest through the generated codecs, which shuffle
// bytes and check no membership, and no step of a build validates an envelope
// against this schema. So the constraint proved here is a property of the
// published wire contract, which a guest author's own tooling can enforce, and
// these tests are where it is enforced inside this repository. The narrower
// claim is the true one: the schema declares the closed set, and this is the
// gate that holds it.

// envelopeSchemaID is the identity of the schema declaring an envelope, and
// envelopeDef the definition inside it. Together they address one reply envelope
// for compilation.
type envelopeRef struct {
	name      string
	schemaID  string
	defName   string
	rawSchema string
}

const (
	providerSchemaID = "https://aggrega.ai/schemas/wasm-provider-v1alpha1.schema.json"
	parseSchemaID    = "https://aggrega.ai/schemas/wasm-parse-v1alpha1.schema.json"
	vocabSchemaID    = "https://aggrega.ai/schemas/wasm-hostfunc-error-v1alpha1.schema.json"
)

// errorCodeEnvelopes are the reply envelopes carrying error_code. Every envelope
// declaring the field appears here; TestHostfuncErrorCodeEnvelopes_CoverEveryDeclaringEnvelope
// holds this list to the schemas so an envelope added later cannot go unchecked.
func errorCodeEnvelopes() []envelopeRef {
	return []envelopeRef{
		{"SourcePackResponse", providerSchemaID, "SourcePackResponse", schemas.WasmProviderV1alpha1},
		{"SourceFileResponse", providerSchemaID, "SourceFileResponse", schemas.WasmProviderV1alpha1},
		{"FetchExternalInputResponse", providerSchemaID, "FetchExternalInputResponse", schemas.WasmProviderV1alpha1},
		{"ParseSourceResponse", parseSchemaID, "ParseSourceResponse", schemas.WasmParseV1alpha1},
	}
}

// referencedSchemas are the sibling schemas a socket schema addresses by a
// relative cross-document reference, keyed by the identity that reference
// resolves to. A socket schema resolves nothing over the network, so every
// document it reaches is supplied here.
func referencedSchemas() map[string]string {
	return map[string]string{
		vocabSchemaID: schemas.WasmHostfuncErrorV1alpha1,
		"https://aggrega.ai/schemas/wasm-diagnostic-v1alpha1.schema.json": schemas.WasmDiagnosticV1alpha1,
		"https://aggrega.ai/schemas/ir-v1alpha1.schema.json":              schemas.IRSchemaV1alpha1,
		"https://aggrega.ai/schemas/workload-v1alpha1.schema.json":        schemas.WorkloadV1alpha1,
	}
}

// compileEnvelope compiles one envelope definition with the shared vocabulary
// available for reference resolution. socketSource overrides the socket schema's
// bytes, so a test can compile a deliberately weakened variant of it.
func compileEnvelope(t *testing.T, env envelopeRef, socketSource string) *jsonschema.Schema {
	t.Helper()

	sources := referencedSchemas()
	sources[env.schemaID] = socketSource

	c := jsonschema.NewCompiler()
	for id, source := range sources {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(source)))
		if err != nil {
			t.Fatalf("unmarshal %s: %v", id, err)
		}
		if err := c.AddResource(id, doc); err != nil {
			t.Fatalf("add resource %s: %v", id, err)
		}
	}

	schema, err := c.Compile(env.schemaID + "#/$defs/" + env.defName)
	if err != nil {
		t.Fatalf("compile %s: %v", env.name, err)
	}
	return schema
}

// validateEnvelope validates a document carrying errorCode against the envelope,
// returning the validation error or nil.
func validateEnvelope(t *testing.T, schema *jsonschema.Schema, errorCode string) error {
	t.Helper()

	raw, err := json.Marshal(map[string]any{"error": "the callback failed", "error_code": errorCode})
	if err != nil {
		t.Fatalf("marshal instance: %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("unmarshal instance: %v", err)
	}
	return schema.Validate(inst)
}

// TestHostfuncErrorCodeEnvelopes_RefuseAnInventedCode drives an invented code
// through validation on every envelope carrying the field and reads the refusal.
// Each envelope also carries a declared code as a positive control, so a row that
// refuses for some reason other than the code — a mis-addressed definition, a
// property the envelope does not permit — fails visibly instead of passing as a
// refusal the test never earned.
func TestHostfuncErrorCodeEnvelopes_RefuseAnInventedCode(t *testing.T) {
	t.Parallel()

	const invented = "no_such_error_code"
	declared := hostfuncErrorCodes(t)[0]

	for _, env := range errorCodeEnvelopes() {
		env := env
		t.Run(env.name, func(t *testing.T) {
			t.Parallel()
			schema := compileEnvelope(t, env, env.rawSchema)

			if err := validateEnvelope(t, schema, declared); err != nil {
				t.Fatalf("the declared code %q was refused by %s, so the refusal below would not be attributable to the code: %v",
					declared, env.name, err)
			}

			err := validateEnvelope(t, schema, invented)
			if err == nil {
				t.Fatalf("%s accepted the invented code %q; the envelope does not constrain error_code to the shared vocabulary",
					env.name, invented)
			}
			if !strings.Contains(err.Error(), "error_code") {
				t.Errorf("%s refused the invented code but the refusal does not name error_code, so it is not attributable to this field: %v",
					env.name, err)
			}
			t.Logf("%s refusal: %v", env.name, err)
		})
	}
}

// TestHostfuncErrorCodeEnvelopes_RefusalIsNotVacuous proves the refusal above is
// caused by the reference to the shared vocabulary and by nothing else. It
// compiles each envelope from a variant of its schema with the reference stripped
// from that envelope's error_code — the exact defect the constraint repairs — and
// asserts the invented code is then accepted. Without this, the refusal assertion
// would keep passing if the reference were removed but some unrelated keyword
// rejected the document.
func TestHostfuncErrorCodeEnvelopes_RefusalIsNotVacuous(t *testing.T) {
	t.Parallel()

	const invented = "no_such_error_code"

	for _, env := range errorCodeEnvelopes() {
		env := env
		t.Run(env.name, func(t *testing.T) {
			t.Parallel()

			weakened := stripErrorCodeRef(t, env)
			schema := compileEnvelope(t, env, weakened)

			if err := validateEnvelope(t, schema, invented); err != nil {
				t.Fatalf("%s still refused the invented code with the vocabulary reference stripped, so the refusal asserted elsewhere is not attributable to the reference: %v",
					env.name, err)
			}
		})
	}
}

// stripErrorCodeRef returns the envelope's schema with the vocabulary reference
// removed from that one envelope's error_code, restoring the bare-string
// declaration the constraint replaced. Every other envelope keeps its reference,
// so the weakening is scoped to the envelope under test.
func stripErrorCodeRef(t *testing.T, env envelopeRef) string {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal([]byte(env.rawSchema), &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", env.schemaID, err)
	}

	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("%s declares no $defs", env.schemaID)
	}
	def, ok := defs[env.defName].(map[string]any)
	if !ok {
		t.Fatalf("%s declares no $defs.%s", env.schemaID, env.defName)
	}
	props, ok := def["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s.%s declares no properties", env.schemaID, env.defName)
	}
	field, ok := props["error_code"].(map[string]any)
	if !ok {
		t.Fatalf("%s.%s declares no error_code property", env.schemaID, env.defName)
	}
	if _, ok := field["$ref"]; !ok {
		t.Fatalf("%s.%s error_code carries no $ref to strip; the constraint this test measures is absent",
			env.schemaID, env.defName)
	}
	delete(field, "$ref")
	field["type"] = "string"

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal weakened %s: %v", env.schemaID, err)
	}
	return string(out)
}

// TestHostfuncErrorCodeEnvelopes_CoverEveryDeclaringEnvelope holds the table
// above to the schemas: every definition in either socket schema that declares an
// error_code property is covered by the tests here. An envelope added later with
// the field left unconstrained fails here rather than shipping unchecked, which a
// fixed list of envelopes alone cannot catch.
func TestHostfuncErrorCodeEnvelopes_CoverEveryDeclaringEnvelope(t *testing.T) {
	t.Parallel()

	covered := map[string]bool{}
	for _, env := range errorCodeEnvelopes() {
		covered[env.schemaID+"#"+env.defName] = true
	}

	for schemaID, source := range map[string]string{
		providerSchemaID: schemas.WasmProviderV1alpha1,
		parseSchemaID:    schemas.WasmParseV1alpha1,
	} {
		var doc struct {
			Defs map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal([]byte(source), &doc); err != nil {
			t.Fatalf("unmarshal %s: %v", schemaID, err)
		}
		if len(doc.Defs) == 0 {
			t.Fatalf("%s declares no $defs, so this scan would pass over an empty set", schemaID)
		}
		for defName, def := range doc.Defs {
			if _, declares := def.Properties["error_code"]; !declares {
				continue
			}
			if !covered[schemaID+"#"+defName] {
				t.Errorf("%s declares error_code on %s, which no test here covers", schemaID, defName)
			}
			delete(covered, schemaID+"#"+defName)
		}
	}

	for ref := range covered {
		t.Errorf("the tests here cover %s, which declares no error_code property", ref)
	}
}
