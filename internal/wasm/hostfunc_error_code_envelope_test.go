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

// Every host-function REPLY envelope carries its machine-matchable failure
// identity under hostfunc_error_code, and that key references the shared
// error-code vocabulary rather than declaring a bare string — so the closed set
// the field's description promises is a constraint the wire contract states
// rather than prose beside an unconstrained value.
//
// A guest→host socket OUTPUT reports a halting condition the guest raised, under
// condition_code beside condition_short_code, and is deliberately NOT held
// against the catalogue: those identifiers come from the compiler's own open
// register, which the catalogue does not contain, so referencing it there would
// declare a condition to be a member of a set it is not in. The census below
// reads both keys and holds each to its own register.
//
// These tests hold the reply envelopes' reference by driving values through a
// JSON Schema implementation and reading the outcome: a declared code is accepted
// and an invented one is refused, on every reply envelope that carries the field.
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

// hostfuncErrorCodeKey is the reply envelopes' failure-identity key, and
// conditionCodeKey with conditionShortCodeKey are the guest-reported condition's
// pair. They are named here rather than spelled at each site because the census
// below reads all three as data, and a key whose spelling drifted between the
// scan and the assertion would read as an absent field rather than as a defect.
const (
	hostfuncErrorCodeKey = "hostfunc_error_code"
	conditionCodeKey     = "condition_code"
	conditionShortKey    = "condition_short_code"
)

// hostfuncErrorCodeEnvelopes are the reply envelopes carrying
// hostfunc_error_code. Every envelope declaring the field appears here;
// TestHostfuncErrorCodeEnvelopes_CoverEveryDeclaringEnvelope holds this list to
// the schemas so an envelope added later cannot go unchecked.
func hostfuncErrorCodeEnvelopes() []envelopeRef {
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

// validateEnvelope validates a document carrying code under the reply
// envelope's failure-identity key, returning the validation error or nil.
func validateEnvelope(t *testing.T, schema *jsonschema.Schema, code string) error {
	t.Helper()

	raw, err := json.Marshal(map[string]any{"error": "the callback failed", hostfuncErrorCodeKey: code})
	if err != nil {
		t.Fatalf("marshal instance: %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("unmarshal instance: %v", err)
	}
	return schema.Validate(inst)
}

// inventedCode is a value the catalogue does not contain. It deliberately shares
// no substring with the key it is written under: the refusal assertion below
// reads the key's name out of the message to prove the refusal is attributable
// to that field, and a value echoing the key would satisfy that assertion on its
// own echo rather than on the field being named.
const inventedCode = "no_such_failure_identity"

// TestHostfuncErrorCodeEnvelopes_RefuseAnInventedCode drives an invented code
// through validation on every envelope carrying the field and reads the refusal.
// Each envelope also carries a declared code as a positive control, so a row that
// refuses for some reason other than the code — a mis-addressed definition, a
// property the envelope does not permit — fails visibly instead of passing as a
// refusal the test never earned.
func TestHostfuncErrorCodeEnvelopes_RefuseAnInventedCode(t *testing.T) {
	t.Parallel()

	declared := hostfuncErrorCodes(t)[0]

	for _, env := range hostfuncErrorCodeEnvelopes() {
		env := env
		t.Run(env.name, func(t *testing.T) {
			t.Parallel()
			schema := compileEnvelope(t, env, env.rawSchema)

			if err := validateEnvelope(t, schema, declared); err != nil {
				t.Fatalf("the declared code %q was refused by %s, so the refusal below would not be attributable to the code: %v",
					declared, env.name, err)
			}

			err := validateEnvelope(t, schema, inventedCode)
			if err == nil {
				t.Fatalf("%s accepted the invented code %q; the envelope does not constrain %s to the shared vocabulary",
					env.name, inventedCode, hostfuncErrorCodeKey)
			}
			if !strings.Contains(err.Error(), hostfuncErrorCodeKey) {
				t.Errorf("%s refused the invented code but the refusal does not name %s, so it is not attributable to this field: %v",
					env.name, hostfuncErrorCodeKey, err)
			}
			t.Logf("%s refusal: %v", env.name, err)
		})
	}
}

// TestHostfuncErrorCodeEnvelopes_RefusalIsNotVacuous proves the refusal above is
// caused by the reference to the shared vocabulary and by nothing else. It
// compiles each envelope from a variant of its schema with the reference stripped
// from that envelope's failure-identity key — the exact defect the constraint
// repairs — and asserts the invented code is then accepted. Without this, the
// refusal assertion would keep passing if the reference were removed but some
// unrelated keyword rejected the document.
func TestHostfuncErrorCodeEnvelopes_RefusalIsNotVacuous(t *testing.T) {
	t.Parallel()

	for _, env := range hostfuncErrorCodeEnvelopes() {
		env := env
		t.Run(env.name, func(t *testing.T) {
			t.Parallel()

			weakened := stripHostfuncErrorCodeRef(t, env)
			schema := compileEnvelope(t, env, weakened)

			if err := validateEnvelope(t, schema, inventedCode); err != nil {
				t.Fatalf("%s still refused the invented code with the vocabulary reference stripped, so the refusal asserted elsewhere is not attributable to the reference: %v",
					env.name, err)
			}
		})
	}
}

// stripHostfuncErrorCodeRef returns the envelope's schema with the vocabulary
// reference removed from that one envelope's failure-identity key, restoring the
// bare-string declaration the constraint replaced. Every other envelope keeps its
// reference, so the weakening is scoped to the envelope under test.
func stripHostfuncErrorCodeRef(t *testing.T, env envelopeRef) string {
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
	field, ok := props[hostfuncErrorCodeKey].(map[string]any)
	if !ok {
		t.Fatalf("%s.%s declares no %s property", env.schemaID, env.defName, hostfuncErrorCodeKey)
	}
	if _, ok := field["$ref"]; !ok {
		t.Fatalf("%s.%s %s carries no $ref to strip; the constraint this test measures is absent",
			env.schemaID, env.defName, hostfuncErrorCodeKey)
	}
	delete(field, "$ref")
	field["type"] = "string"

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal weakened %s: %v", env.schemaID, err)
	}
	return string(out)
}

// vocabRefMarker is the reference a closed-catalogue failure identity carries.
// The socket schemas address the catalogue by a relative document reference
// rather than by the identity it resolves to, so the marker is the document name.
const vocabRefMarker = "wasm-hostfunc-error-v1alpha1.schema.json"

// TestHostfuncErrorCodeEnvelopes_CoverEveryDeclaringEnvelope holds the table
// above to the schemas: every definition in either socket schema that declares a
// hostfunc_error_code is covered by the tests here. An envelope added later with
// the field left unconstrained fails here rather than shipping unchecked, which a
// fixed list of envelopes alone cannot catch.
//
// **Each key names the register its value is drawn from, and the census reads the
// key.** A host→guest reply envelope declares hostfunc_error_code, whose value is
// a member of the closed catalogue — which is what these tests hold. A guest→host
// socket OUTPUT declares condition_code beside condition_short_code, because a
// halting condition has two identifiers, and those come from the compiler's own
// open register rather than from the catalogue. So the two channels are told
// apart by the name each key carries, and neither name can be read as the general
// case for failure.
//
// The pairing is still asserted, because it remains a real property of the
// condition channel rather than the thing that identifies it: a guest setting one
// identifier and not the other has named a condition the host cannot resolve. So
// the census reads the key to decide which register a field belongs to, and then
// holds the condition channel to its pair.
//
// The guard keeps its teeth on both sides. A hostfunc_error_code the table does
// not cover fails, which is the unconstrained-field case the guard exists for.
// And a condition_code that DOES reference the catalogue fails too, so the
// failure channel cannot quietly acquire a closure that would refuse every code
// the compiler hands out.
func TestHostfuncErrorCodeEnvelopes_CoverEveryDeclaringEnvelope(t *testing.T) {
	t.Parallel()

	covered := map[string]bool{}
	for _, env := range hostfuncErrorCodeEnvelopes() {
		covered[env.schemaID+"#"+env.defName] = true
	}

	conditionChannels := 0
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
			if raw, declares := def.Properties[hostfuncErrorCodeKey]; declares {
				if covered[schemaID+"#"+defName] {
					delete(covered, schemaID+"#"+defName)
				} else {
					t.Errorf("%s declares %s on %s, which no test here covers, so its membership in the "+
						"closed host-function catalogue is asserted nowhere",
						schemaID, hostfuncErrorCodeKey, defName)
				}
				if !strings.Contains(string(raw), vocabRefMarker) {
					t.Errorf("%s declares %s on %s without referencing the closed host-function catalogue, "+
						"so the key names a catalogue its value is not held to",
						schemaID, hostfuncErrorCodeKey, defName)
				}
			}

			raw, declares := def.Properties[conditionCodeKey]
			if !declares {
				continue
			}
			conditionChannels++
			if _, hasShort := def.Properties[conditionShortKey]; !hasShort {
				t.Errorf("%s declares %s on %s with no %s beside it; a halting condition has two identifiers "+
					"and a guest reporting one without the other names a condition the host cannot resolve",
					schemaID, conditionCodeKey, defName, conditionShortKey)
			}
			if strings.Contains(string(raw), vocabRefMarker) {
				t.Errorf("%s declares the guest→host failure channel on %s and references the closed "+
					"host-function catalogue from its %s; the identifiers on that channel come "+
					"from the compiler's own register, which the catalogue does not contain",
					schemaID, defName, conditionCodeKey)
			}
		}
	}

	for ref := range covered {
		t.Errorf("the tests here cover %s, which declares no %s property", ref, hostfuncErrorCodeKey)
	}

	// Calibration: the condition arm is what makes this census cover the second
	// register at all, so a run finding no condition channel has stopped measuring
	// the distinction it was written for and would pass over a schema that had
	// renamed or dropped that channel entirely.
	if conditionChannels == 0 {
		t.Errorf("the scan found no %s anywhere, so the second register this census exists to separate "+
			"matched nothing; the failure channel's key moved, or the scan is reading the wrong schemas",
			conditionCodeKey)
	}
}
