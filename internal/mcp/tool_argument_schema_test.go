// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The assertions in this file hold each tool's declared input schema to the
// argument rules the tool itself enforces, so the two cannot drift.
//
// A tool's input schema is the only thing a caller can read before making a
// call. Where it declares a rule the tool enforces, a caller reading it
// structurally never composes a call the server would refuse — which is the one
// failure that costs nothing, because it never happens. Where it declares less
// than the tool enforces, the caller learns the rule only by being refused; and
// where it declares more, a schema-constrained caller is denied a call the
// server would have accepted. Both are drift, and both are silent, so both are
// asserted here.
//
// The cross-check runs one way on purpose: a payload the schema declares invalid
// must also be refused by the tool. The converse is not asserted, because a
// payload with legal arguments still fails when the path it names is not there —
// an operation outcome, which is a different property with its own tests.

// compileToolSchema compiles a declared input schema into a validator, proving
// on the way that it is a valid JSON Schema rather than merely parseable JSON.
//
// The compile glue is local to this file rather than shared: it is a few lines
// of library setup, and the alternative — reaching for another package's
// unexported helper — would couple two unrelated surfaces to save them.
func compileToolSchema(t *testing.T, spec toolSpec) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(spec.inputSchema)))
	if err != nil {
		t.Fatalf("%s: input schema does not parse: %v", spec.name, err)
	}
	c := jsonschema.NewCompiler()
	id := "mem://" + spec.name + "-input-schema.json"
	if err := c.AddResource(id, doc); err != nil {
		t.Fatalf("%s: input schema is not a usable schema resource: %v", spec.name, err)
	}
	compiled, err := c.Compile(id)
	if err != nil {
		t.Fatalf("%s: input schema does not compile as a JSON Schema: %v", spec.name, err)
	}
	return compiled
}

// TestEveryToolInputSchemaCompiles asserts that every schema the server
// advertises is a valid JSON Schema. A schema that parses as JSON but does not
// compile is advertised to every client and usable by none of them, and nothing
// else here would notice.
func TestEveryToolInputSchemaCompiles(t *testing.T) {
	s := newGuidanceServer(t)
	specs := registeredToolSpecs(s)
	if len(specs) == 0 {
		t.Fatal("no tool specs enumerated; the assertion measured nothing")
	}
	for _, spec := range specs {
		compileToolSchema(t, spec)
	}
	t.Logf("compiled %d advertised input schema(s)", len(specs))
}

// argumentSchemaCase is one payload measured against a tool's declared schema.
type argumentSchemaCase struct {
	// tool names the tool whose schema and dispatch the payload is measured
	// against.
	tool string
	// scenario says what the payload represents, so a failure reads as a rule
	// rather than as a blob of JSON.
	scenario string
	// payload is the arguments object exactly as a caller would send it.
	payload string
	// valid is whether the declared schema admits the payload.
	valid bool
}

// TestDeclaredSchemasStateTheArgumentRulesTheToolsEnforce measures representative
// payloads against each tool's declared schema, and cross-checks every payload
// the schema rejects against the tool's own refusal.
//
// The valid rows are as load-bearing as the invalid ones: an over-tight
// combination rule would deny a caller a call the server accepts, and only a
// legal payload can catch that.
func TestDeclaredSchemasStateTheArgumentRulesTheToolsEnforce(t *testing.T) {
	cases := []argumentSchemaCase{
		// validate: one scope per call, with plugin optionally cross-checked
		// against a Plugin entity, and type meaningful only alongside plugin.
		{"validate", "release scope", `{"release":"r.yaml"}`, true},
		{"validate", "single-entity scope", `{"entity":"e.yaml"}`, true},
		{"validate", "whole-catalog scope", `{"catalog":true}`, true},
		{"validate", "plugin scope, type taken from the entity later", `{"plugin":"p.wasm"}`, true},
		{"validate", "plugin scope, binary-only", `{"plugin":"p.wasm","type":"renderer"}`, true},
		{"validate", "plugin scope cross-checked against a Plugin entity", `{"plugin":"p.wasm","entity":"pe.yaml"}`, true},
		{"validate", "plugin scope with both a type and a cross-check entity", `{"plugin":"p.wasm","type":"renderer","entity":"pe.yaml"}`, true},
		{"validate", "a scope with every optional argument", `{"release":"r.yaml","workspace_root":"/w","catalog_dirs":["c"],"response_format":"concise"}`, true},
		{"validate", "catalog explicitly false selects no scope", `{"entity":"e.yaml","catalog":false}`, true},
		{"validate", "no scope at all", `{}`, false},
		{"validate", "release and entity together", `{"release":"r.yaml","entity":"e.yaml"}`, false},
		{"validate", "release and catalog together", `{"release":"r.yaml","catalog":true}`, false},
		{"validate", "plugin and release together", `{"plugin":"p.wasm","release":"r.yaml"}`, false},
		{"validate", "plugin and catalog together", `{"plugin":"p.wasm","catalog":true}`, false},
		{"validate", "catalog and entity together", `{"catalog":true,"entity":"e.yaml"}`, false},
		{"validate", "type without plugin", `{"type":"renderer"}`, false},
		{"validate", "a blank scope selector", `{"release":""}`, false},
		{"validate", "a scope selector of the wrong type", `{"release":5}`, false},

		// describe: exactly one subject.
		{"describe", "catalog-entity mode", `{"name":"core/compute/api-server"}`, true},
		{"describe", "Release-closure mode", `{"release":"r.yaml"}`, true},
		{"describe", "no subject", `{}`, false},
		{"describe", "both subjects", `{"name":"n","release":"r.yaml"}`, false},
		{"describe", "a blank subject", `{"name":""}`, false},

		// lint: exactly one subject, with the mode flags alongside.
		{"lint", "entity mode", `{"entity":"e.yaml"}`, true},
		{"lint", "Release mode", `{"release":"r.yaml"}`, true},
		{"lint", "entity mode with its own flags", `{"entity":"e.yaml","pre_publish":true,"plugin":"p.wasm"}`, true},
		{"lint", "no subject", `{}`, false},
		{"lint", "both subjects", `{"entity":"e.yaml","release":"r.yaml"}`, false},

		// get-schema: one kind, or the enumeration. Asking to enumerate while
		// also naming a kind is legal — the enumeration simply wins — so the
		// two shapes overlap rather than exclude.
		{"get-schema", "one kind", `{"kind":"component"}`, true},
		{"get-schema", "enumerate every kind", `{"list":true}`, true},
		{"get-schema", "enumerate, with a kind that is then ignored", `{"list":true,"kind":"component"}`, true},
		{"get-schema", "neither a kind nor the enumeration", `{}`, false},
		{"get-schema", "list explicitly false, and no kind", `{"list":false}`, false},
		{"get-schema", "a blank kind", `{"kind":""}`, false},

		// scaffold, build, explain-error: one required argument each, which a
		// blank value does not supply.
		{"scaffold", "a kind", `{"kind":"component"}`, true},
		{"scaffold", "a kind and a seed name", `{"kind":"component","name":"api"}`, true},
		{"scaffold", "no kind", `{}`, false},
		{"scaffold", "a blank kind", `{"kind":""}`, false},
		{"build", "a Release", `{"release":"r.yaml"}`, true},
		{"build", "no Release", `{}`, false},
		{"build", "a blank Release", `{"release":""}`, false},
		{"explain-error", "a diagnostic", `{"message":"parameter contract violated"}`, true},
		{"explain-error", "no diagnostic", `{}`, false},
		{"explain-error", "a blank diagnostic", `{"message":""}`, false},

		// gen-catalog: one source of guests, plus the two outputs it always
		// needs.
		{"gen-catalog", "every embedded guest", `{"from_embedded":true,"output_dir":"o","overlay":"v"}`, true},
		{"gen-catalog", "one named .wasm", `{"plugin":"p.wasm","output_dir":"o","overlay":"v"}`, true},
		{"gen-catalog", "one named .wasm, embedded explicitly declined", `{"from_embedded":false,"plugin":"p.wasm","output_dir":"o","overlay":"v"}`, true},
		{"gen-catalog", "no source of guests", `{"output_dir":"o","overlay":"v"}`, false},
		{"gen-catalog", "both sources of guests", `{"from_embedded":true,"plugin":"p.wasm","output_dir":"o","overlay":"v"}`, false},
		{"gen-catalog", "no output directory", `{"from_embedded":true,"overlay":"v"}`, false},
		{"gen-catalog", "no overlay", `{"from_embedded":true,"output_dir":"o"}`, false},
		{"gen-catalog", "a blank output directory", `{"from_embedded":true,"output_dir":"","overlay":"v"}`, false},

		// attach-candidates: exactly one of three coordinates, with the Release
		// meaningful only alongside the graph node — and required by it, since a
		// graph node names a node on a Release's graph and there is no graph to
		// look in without one.
		{"attach-candidates", "a workspace entity by name", `{"node":"example/compute/web"}`, true},
		{"attach-candidates", "a graph node with its Release", `{"graph_node":"data.main-db","release":"r.yaml"}`, true},
		{"attach-candidates", "a label set outright", `{"labels":{"component.aggrega.ai/type":"server"}}`, true},
		{"attach-candidates", "an empty label set, which is a subject", `{"labels":{}}`, true},
		{"attach-candidates", "a coordinate with every optional argument", `{"node":"n","workspace_root":"/w","catalog_dirs":["c"]}`, true},
		{"attach-candidates", "no coordinate at all", `{}`, false},
		{"attach-candidates", "a name and a label set together", `{"node":"n","labels":{}}`, false},
		{"attach-candidates", "a name and a graph node together", `{"node":"n","graph_node":"g","release":"r.yaml"}`, false},
		{"attach-candidates", "a graph node with no Release", `{"graph_node":"g"}`, false},
		{"attach-candidates", "a Release without the graph node it qualifies", `{"node":"n","release":"r.yaml"}`, false},
		{"attach-candidates", "a blank coordinate", `{"node":""}`, false},
		{"attach-candidates", "a coordinate of the wrong type", `{"node":5}`, false},
		{"attach-candidates", "a label value that is not a string", `{"labels":{"k":5}}`, false},

		// connection-candidates: one required subject, with the optional single
		// socket alongside. It carries no combination rule, and no coordinate-free
		// form: there is deliberately no label set here.
		{"connection-candidates", "every socket an entity declares", `{"node":"example/compute/web"}`, true},
		{"connection-candidates", "one socket by name", `{"node":"example/compute/web","socket":"database"}`, true},
		{"connection-candidates", "no subject", `{}`, false},
		{"connection-candidates", "a blank subject", `{"node":""}`, false},
		{"connection-candidates", "a blank socket", `{"node":"n","socket":""}`, false},

		// describe-capabilities: the whole long tail, or one entity kind.
		{describeCapabilitiesName, "the whole long tail", `{}`, true},
		{describeCapabilitiesName, "narrowed to one kind", `{"kind":"component"}`, true},
		{describeCapabilitiesName, "a kind that is not an entity kind at all", `{"kind":"nonesuch"}`, false},
	}

	s := newGuidanceServer(t)
	compiled := map[string]*jsonschema.Schema{}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.scenario, func(t *testing.T) {
			schema, ok := compiled[tc.tool]
			if !ok {
				spec, found := s.lookupTool(tc.tool)
				if !found {
					t.Fatalf("no tool named %q is registered; the case measures nothing", tc.tool)
				}
				schema = compileToolSchema(t, *spec)
				compiled[tc.tool] = schema
			}

			var doc any
			if err := json.Unmarshal([]byte(tc.payload), &doc); err != nil {
				t.Fatalf("the case's own payload is not JSON: %v", err)
			}
			err := schema.Validate(doc)
			switch {
			case tc.valid && err != nil:
				t.Fatalf("the declared schema rejects a payload the tool accepts, so a caller reading the schema is denied a legal call: %v", err)
			case !tc.valid && err == nil:
				t.Fatalf("the declared schema admits %s, which the tool refuses, so the schema teaches a caller a rule the server does not follow", tc.payload)
			}
			if tc.valid {
				return
			}

			// The schema and the tool must refuse the same payloads. Driving the
			// real dispatch is what makes this a cross-check rather than two
			// independent readings of one rule.
			f := callToolRaw(t, s, tc.tool, tc.payload)
			if !f.failed {
				t.Errorf("the declared schema rejects this payload and the tool accepted it: %q", f.text)
			}
			assertActionableGuidance(t, tc.tool, tc.scenario, f)
		})
	}
	t.Logf("measured %d payload(s) against the declared schemas", len(cases))
}

// TestDeclaredSchemasCoverEveryToolWithACombinationRule names the tools whose
// arguments carry a combination rule and asserts each one declares it, so a tool
// gaining such a rule without declaring it is reported rather than overlooked.
//
// The list is the one thing here that is hand-written, and it has to be: whether
// a tool's arguments carry a combination rule is a fact about that tool's
// meaning, which nothing in the spec records. What the assertion buys is that
// the list cannot silently stop matching the schemas — a named tool whose schema
// declares no combination is a failure.
func TestDeclaredSchemasCoverEveryToolWithACombinationRule(t *testing.T) {
	withCombinationRules := []string{"validate", "describe", "lint", "get-schema", "gen-catalog", "attach-candidates"}
	s := newGuidanceServer(t)
	for _, name := range withCombinationRules {
		spec, found := s.lookupTool(name)
		if !found {
			t.Errorf("%s is named as carrying a combination rule but is not registered", name)
			continue
		}
		var declared struct {
			AnyOf []json.RawMessage `json:"anyOf"`
			OneOf []json.RawMessage `json:"oneOf"`
		}
		if err := json.Unmarshal([]byte(spec.inputSchema), &declared); err != nil {
			t.Errorf("%s: input schema does not parse: %v", name, err)
			continue
		}
		if len(declared.AnyOf)+len(declared.OneOf) < 2 {
			t.Errorf("%s enforces a combination rule its declared schema does not state, so a caller learns it only by being refused", name)
		}
	}
	if len(withCombinationRules) == 0 {
		t.Fatal("no tools named; the assertion measured nothing")
	}
}
