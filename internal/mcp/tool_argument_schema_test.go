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
		{"validate", "a response format outside the published set", `{"release":"r.yaml","response_format":"verbose"}`, false},

		// list-entities and list-releases: no subject at all, and a window over the
		// records they return. The window is the only rule either of them carries,
		// and it is a range rather than a combination — both bounds are declared, so
		// a caller reading the schema never composes a page this surface refuses.
		{"list-entities", "no arguments at all, which is the summary", `{}`, true},
		{"list-entities", "the records, bounded by default", `{"response_format":"detailed"}`, true},
		{"list-entities", "a page named outright", `{"response_format":"detailed","limit":10,"offset":20}`, true},
		{"list-entities", "the smallest page", `{"response_format":"detailed","limit":1}`, true},
		{"list-entities", "the largest page", `{"response_format":"detailed","limit":500}`, true},
		{"list-entities", "an offset at the start", `{"offset":0}`, true},
		{"list-entities", "every optional argument at once", `{"workspace_root":"/w","catalog_dirs":["c"],"release":"r.yaml","response_format":"concise","limit":25,"offset":0}`, true},
		{"list-entities", "the listing in one Release's terms", `{"release":"r.yaml"}`, true},
		{"list-entities", "a blank Release, which names none", `{"release":""}`, false},
		{"list-entities", "a Release of the wrong type", `{"release":5}`, false},
		{"list-entities", "a page of no records", `{"response_format":"detailed","limit":0}`, false},
		{"list-entities", "a negative page", `{"response_format":"detailed","limit":-5}`, false},
		{"list-entities", "a page past the bound", `{"response_format":"detailed","limit":501}`, false},
		{"list-entities", "an offset before the start", `{"offset":-1}`, false},
		{"list-entities", "a page size that is not a whole number", `{"limit":"10"}`, false},
		// A published enumeration is an exact-match constraint, so a declared value
		// in another case is a value the schema rejects exactly as an invented one
		// is — which is why both shapes are measured rather than only the first.
		{"list-entities", "a response format outside the published set", `{"response_format":"verbose"}`, false},
		{"list-entities", "a published response format in the wrong case", `{"response_format":"Detailed"}`, false},
		{"list-entities", "a published response format with whitespace around it", `{"response_format":" detailed "}`, false},
		{"list-entities", "a blank response format", `{"response_format":""}`, false},

		{"list-releases", "no arguments at all, which is the summary", `{}`, true},
		{"list-releases", "the paths, bounded by default", `{"response_format":"detailed"}`, true},
		{"list-releases", "a page named outright", `{"response_format":"detailed","limit":10,"offset":20}`, true},
		{"list-releases", "every optional argument at once", `{"workspace_root":"/w","response_format":"detailed","limit":500,"offset":0}`, true},
		{"list-releases", "a page of no paths", `{"response_format":"detailed","limit":0}`, false},
		{"list-releases", "a page past the bound", `{"response_format":"detailed","limit":501}`, false},
		{"list-releases", "an offset before the start", `{"offset":-1}`, false},
		{"list-releases", "a response format outside the published set", `{"response_format":"verbose"}`, false},

		// describe: a name, a Release, or a name in a Release's terms — and a
		// registry reference, which is read from its registry, never with a Release.
		{"describe", "catalog-entity mode", `{"name":"core/compute/api-server"}`, true},
		{"describe", "Release-closure mode", `{"release":"r.yaml"}`, true},
		{"describe", "no subject", `{}`, false},
		{"describe", "a name in a Release's terms", `{"name":"n","release":"r.yaml"}`, true},
		{"describe", "a registry reference alone", `{"name":"oci://hub.aggrega.ai/component/core/x:1.0.0"}`, true},
		{"describe", "a registry reference with a Release", `{"name":"oci://hub.aggrega.ai/component/core/x:1.0.0","release":"r.yaml"}`, false},
		{"describe", "a blank subject", `{"name":""}`, false},
		{"describe", "a response format outside the published set", `{"name":"n","response_format":"verbose"}`, false},

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
		{"build", "a response format outside the published set", `{"release":"r.yaml","response_format":"verbose"}`, false},
		{"explain-error", "a diagnostic", `{"message":"parameter contract violated"}`, true},
		{"explain-error", "no diagnostic", `{}`, false},
		{"explain-error", "a blank diagnostic", `{"message":""}`, false},

		// pull: a Release, optionally compared rather than written, or narrowed to
		// named upgrade targets — and never both, because one leaves the lockfile as
		// it is and the other rewrites it. A comparison takes no answer to whether
		// the pin step runs either, whichever value it carries, because a
		// comparison writes no entity.lock for the step to act on. There is
		// deliberately no argument that re-resolves every pin at once, so the
		// wholesale form cannot be composed here at all rather than being accepted
		// and then refused.
		//
		// 🔴 The comparison's two exclusions are the one rule here the declared
		// schema deliberately does NOT state, so the three rows combining them are
		// admitted by the schema below. Stated as a combinator, the exclusion is a
		// branch requiring nothing but a constant on an optional argument, which a
		// client renders as the tool requiring that argument — and a caller that
		// believed it would turn every resolution into a comparison that writes
		// nothing. The tool refuses each combination itself, in a sentence naming
		// both arguments, before anything is resolved; that refusal is asserted
		// where the pull tool's own conflicts are.
		{"pull", "a Release", `{"release":"r.yaml"}`, true},
		{"pull", "compare only", `{"release":"r.yaml","verify":true}`, true},
		{"pull", "named upgrade targets", `{"release":"r.yaml","upgrade_targets":["core/database"]}`, true},
		{"pull", "comparison explicitly declined alongside targets", `{"release":"r.yaml","verify":false,"upgrade_targets":["core/database"]}`, true},
		{"pull", "a Release with every optional argument", `{"release":"r.yaml","workspace_root":"/w","catalog_dirs":["c"],"verify":true}`, true},
		{"pull", "the pin step asked for", `{"release":"r.yaml","pin_sources":true}`, true},
		{"pull", "the pin step declined", `{"release":"r.yaml","pin_sources":false}`, true},
		{"pull", "the pin step asked for with comparison explicitly declined", `{"release":"r.yaml","verify":false,"pin_sources":true}`, true},
		{"pull", "the pin step asked for alongside targets", `{"release":"r.yaml","pin_sources":true,"upgrade_targets":["core/database"]}`, true},
		{"pull", "no Release", `{}`, false},
		{"pull", "a blank Release", `{"release":""}`, false},
		{"pull", "a Release of the wrong type", `{"release":5}`, false},
		{"pull", "comparing and upgrading together, refused by the tool itself", `{"release":"r.yaml","verify":true,"upgrade_targets":["core/database"]}`, true},
		{"pull", "comparing and asking for the pin step, refused by the tool itself", `{"release":"r.yaml","verify":true,"pin_sources":true}`, true},
		{"pull", "comparing and declining the pin step, refused by the tool itself", `{"release":"r.yaml","verify":true,"pin_sources":false}`, true},
		{"pull", "an upgrade naming no target", `{"release":"r.yaml","upgrade_targets":[]}`, false},
		{"pull", "a blank upgrade target", `{"release":"r.yaml","upgrade_targets":[""]}`, false},
		{"pull", "upgrade targets sent as a bare string", `{"release":"r.yaml","upgrade_targets":"core/database"}`, false},

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

		// attachment-container: both coordinates required together. A node id is
		// a key of one Release's graph, so it is meaningless without the Release
		// whose graph it keys — which makes this a pair rather than a
		// combination rule with alternatives.
		{"attachment-container", "a graph node with its Release", `{"graph_node":"ledger","release":"r.yaml"}`, true},
		{"attachment-container", "a pair with every optional argument", `{"graph_node":"g","release":"r.yaml","workspace_root":"/w","catalog_dirs":["c"]}`, true},
		{"attachment-container", "neither coordinate", `{}`, false},
		{"attachment-container", "a graph node with no Release", `{"graph_node":"g"}`, false},
		{"attachment-container", "a Release with no graph node", `{"release":"r.yaml"}`, false},
		{"attachment-container", "a blank graph node", `{"graph_node":"","release":"r.yaml"}`, false},
		{"attachment-container", "a graph node of the wrong type", `{"graph_node":5,"release":"r.yaml"}`, false},

		// edit-document: four required arguments, plus two rules that hold only
		// for some of the edits — the socket a wiring needs, and the non-empty
		// reference the three entity-carrying edits need. Both are declared as
		// conditionals rather than as alternatives, because the condition is the
		// value of one argument rather than a choice between shapes, and the
		// tool refuses each of them through the shared layer's own check.
		{editDocumentToolName, "adding a component", `{"edit":"add-component","file":"a.yaml","node":["spec","components"],"name":"web","value":"core/api-server:1.0.0"}`, true},
		{editDocumentToolName, "wiring an edge, which names its socket", `{"edit":"wire-edge","file":"a.yaml","node":["spec","components","web"],"name":"db","value":"spec.components.store","socket":"database"}`, true},
		{editDocumentToolName, "setting a number, which stays a number", `{"edit":"set-value","file":"a.yaml","node":["spec","components","web"],"name":"instances","value":3}`, true},
		{editDocumentToolName, "setting nothing at all, which only set-value admits", `{"edit":"set-value","file":"a.yaml","node":["spec","components","web"],"name":"note","value":null}`, true},
		{editDocumentToolName, "an edit with every optional argument", `{"edit":"set-value","file":"a.yaml","node":["spec"],"name":"k","value":"v","dry_run":true,"workspace_root":"/w"}`, true},
		{editDocumentToolName, "no arguments at all", `{}`, false},
		{editDocumentToolName, "an edit outside the closed set", `{"edit":"delete-component","file":"a.yaml","node":["spec"],"name":"web","value":"x"}`, false},
		{editDocumentToolName, "a blank document", `{"edit":"add-component","file":"","node":["spec"],"name":"web","value":"x"}`, false},
		{editDocumentToolName, "a node path with no segments", `{"edit":"add-component","file":"a.yaml","node":[],"name":"web","value":"x"}`, false},
		{editDocumentToolName, "a blank node segment", `{"edit":"add-component","file":"a.yaml","node":[""],"name":"web","value":"x"}`, false},
		{editDocumentToolName, "a blank entry name", `{"edit":"set-value","file":"a.yaml","node":["spec"],"name":"","value":"v"}`, false},
		{editDocumentToolName, "wiring an edge with no socket", `{"edit":"wire-edge","file":"a.yaml","node":["spec","components","web"],"name":"db","value":"spec.components.store"}`, false},
		{editDocumentToolName, "wiring an edge with a blank socket", `{"edit":"wire-edge","file":"a.yaml","node":["spec","components","web"],"name":"db","value":"t","socket":""}`, false},
		{editDocumentToolName, "adding a component with no reference", `{"edit":"add-component","file":"a.yaml","node":["spec"],"name":"web"}`, false},
		{editDocumentToolName, "adding a component whose reference is a number", `{"edit":"add-component","file":"a.yaml","node":["spec"],"name":"web","value":5}`, false},
		{editDocumentToolName, "a mapping where a scalar belongs", `{"edit":"set-value","file":"a.yaml","node":["spec"],"name":"k","value":{"cpu":2}}`, false},
		{editDocumentToolName, "a delivery that is not a boolean", `{"edit":"set-value","file":"a.yaml","node":["spec"],"name":"k","value":"v","dry_run":"yes"}`, false},

		// import: one required subject and three optional arguments, with no
		// combination rule at all — the format is either named or recognised from
		// the source's own shape, and the delivery is a property of the caller. A
		// blank format is a row rather than an omission: a caller that wrote the
		// key and left it empty said something, and detecting instead would be an
		// argument quietly substituted, which reads to its sender exactly like one
		// that was honoured.
		{importToolName, "a source, with the format recognised from its shape", `{"source":"flow.json"}`, true},
		{importToolName, "a source whose format is named outright", `{"source":"flow.json","format":"langflow"}`, true},
		{importToolName, "a source reported rather than written", `{"source":"flow.json","dry_run":true}`, true},
		{importToolName, "every argument at once", `{"source":"flow.json","format":"flowise","dry_run":false,"workspace_root":"/w"}`, true},
		{importToolName, "no source", `{}`, false},
		{importToolName, "a blank source", `{"source":""}`, false},
		{importToolName, "a source of the wrong type", `{"source":5}`, false},
		{importToolName, "a blank format, which is neither naming one nor leaving it out", `{"source":"flow.json","format":""}`, false},
		{importToolName, "a delivery that is not a boolean", `{"source":"flow.json","dry_run":"yes"}`, false},

		// connection-candidates: one required subject, with the optional single
		// socket alongside. It carries no combination rule, and no coordinate-free
		// form: there is deliberately no label set here.
		{"connection-candidates", "every socket an entity declares", `{"node":"example/compute/web"}`, true},
		{"connection-candidates", "one socket by name", `{"node":"example/compute/web","socket":"database"}`, true},
		{"connection-candidates", "no subject", `{}`, false},
		{"connection-candidates", "a blank subject", `{"node":""}`, false},
		{"connection-candidates", "a blank socket", `{"node":"n","socket":""}`, false},

		// describe-capabilities: the whole announced surface, or one entity kind.
		{describeCapabilitiesName, "the whole announced surface", `{}`, true},
		{describeCapabilitiesName, "narrowed to one kind", `{"kind":"component"}`, true},
		{describeCapabilitiesName, "a kind that is not an entity kind at all", `{"kind":"nonesuch"}`, false},

		// The four publication tools. Every invalid row here is refused before the
		// operation is reached, which is what makes them drivable at all: a row that
		// got past argument checking would try to publish.
		{publishEntityToolName, "an entity to the workspace's own destination", `{"entity":"1.0.0/entity.yaml"}`, true},
		{publishEntityToolName, "a Plugin entity with its executable", `{"entity":"p.yaml","plugin":"g.wasm"}`, true},
		{publishEntityToolName, "a dry run to a named destination", `{"entity":"e.yaml","registry":"r.example","dry_run":true}`, true},
		{publishEntityToolName, "signing declined explicitly", `{"entity":"e.yaml","sign":false}`, true},
		{publishEntityToolName, "keyed signing by held key", `{"entity":"e.yaml","key_path":"/k.pem"}`, true},
		{publishEntityToolName, "keyless signing with a supplied credential", `{"entity":"e.yaml","identity_token":"t","fulcio_url":"https://f"}`, true},
		{publishEntityToolName, "no entity", `{}`, false},
		{publishEntityToolName, "a blank entity", `{"entity":""}`, false},
		{publishEntityToolName, "an entity of the wrong type", `{"entity":5}`, false},
		{publishEntityToolName, "a blank executable", `{"entity":"e.yaml","plugin":""}`, false},

		{publishArtifactToolName, "a file with its name and version", `{"file":"b.txt","name":"handbook","tag":"2.0.0"}`, true},
		{publishArtifactToolName, "a dry run with an explicit media type", `{"file":"b.txt","name":"h","tag":"2.0.0","media_type":"application/x-tar","dry_run":true}`, true},
		{publishArtifactToolName, "no file", `{"name":"h","tag":"2.0.0"}`, false},
		{publishArtifactToolName, "no name", `{"file":"b.txt","tag":"2.0.0"}`, false},
		{publishArtifactToolName, "no tag", `{"file":"b.txt","name":"h"}`, false},
		{publishArtifactToolName, "a blank tag", `{"file":"b.txt","name":"h","tag":""}`, false},

		{signArtifactsToolName, "one reference", `{"references":["r.example/component/core/api:1.0.0"]}`, true},
		{signArtifactsToolName, "a batch with a supplied credential", `{"references":["a:1","b:1"],"identity_token":"t"}`, true},
		{signArtifactsToolName, "no references", `{}`, false},
		{signArtifactsToolName, "an empty reference list", `{"references":[]}`, false},
		{signArtifactsToolName, "a blank entry in the list", `{"references":[""]}`, false},
		{signArtifactsToolName, "references sent as a bare string", `{"references":"a:1"}`, false},

		// deprecate-artifact: a single reference with its content, or a batch file
		// — and the batch file carries its own content, so content sent alongside it
		// would be read by nothing.
		{deprecateArtifactToolName, "one reference with its content", `{"reference":"a:1","severity":"warning","reason":"superseded"}`, true},
		{deprecateArtifactToolName, "one reference with every optional field", `{"reference":"a:1","severity":"warning","reason":"superseded","replacement":"a:2","message":"move on"}`, true},
		{deprecateArtifactToolName, "a batch file", `{"from":"batch.yaml"}`, true},
		{deprecateArtifactToolName, "a batch file with a signing key", `{"from":"batch.yaml","key_path":"/k.pem"}`, true},
		{deprecateArtifactToolName, "neither a reference nor a batch file", `{}`, false},
		{deprecateArtifactToolName, "both at once", `{"reference":"a:1","severity":"warning","reason":"x","from":"batch.yaml"}`, false},
		{deprecateArtifactToolName, "a reference with no content", `{"reference":"a:1"}`, false},
		{deprecateArtifactToolName, "a reference with only a severity", `{"reference":"a:1","severity":"warning"}`, false},
		{deprecateArtifactToolName, "content alongside a batch file", `{"from":"batch.yaml","severity":"warning"}`, false},
		{deprecateArtifactToolName, "a replacement alongside a batch file", `{"from":"batch.yaml","replacement":"a:2"}`, false},
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
//
// The pull tool is absent from the list, deliberately: its one combination rule
// is the exclusion its schema does not state, for the reason given beside its
// rows above, and the tool refuses each combination itself.
func TestDeclaredSchemasCoverEveryToolWithACombinationRule(t *testing.T) {
	withCombinationRules := []string{
		"validate", "describe", "lint", "get-schema", "gen-catalog", "attach-candidates",
		deprecateArtifactToolName,
	}
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
