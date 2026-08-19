// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command typegen is the schema→Go field-declaration generator. It reads a
// published JSON schema (from the embedded aggrega-schemas module) and emits
// Go type declarations — names, Go types, json/yaml tags, and godoc drawn from
// the schema descriptions — into a generated `*_gen.go` file. Hand-written
// content (methods, custom marshalers, shared types like Maintainer,
// polymorphic types like SelectorTerm) lives in adjacent hand-written files;
// the generator never touches them.
//
// It serves two emission shapes from one shared machinery:
//
//   - The single-type shape (the entity kinds in pkg/types): one top-level
//     entity struct whose nested objects become named structs breadth-first.
//   - The multi-type shape (a schema whose types live under `$defs`): one Go
//     type per `$defs` entry, in declared order, in a single file. The
//     top-level `oneOf` wire-discriminator is not a Go type and is ignored
//     (absence of handling — the defs walk reads `$defs` and never the root's
//     `oneOf`/`type`).
//
// The annotation vocabulary is uniform: `x-aggrega-go-type` names the Go type
// of the node it annotates. On an object node it is that type (e.g.
// OrderedMap[AttachmentSlot], yaml.Node, *Interoperability); on an `items` node it
// is the element type and the mechanical array recursion lifts it to []T (e.g.
// Maintainer → []Maintainer, SelectorTerm → []SelectorTerm). There is no
// context-dependent meaning and no per-form special case: the generator emits
// the named type verbatim and never descends into an annotated node. A
// `$ref` of the form `#/$defs/<Name>` resolves to the Go type `<Name>`
// (the def names are already PascalCase), and an `x-aggrega-go-type`
// annotation on the same node still wins over a `$ref`. Otherwise the
// mechanical scalar/bool/array mapping applies and a nested object becomes a
// named struct.
//
// A target may carry a type vocabulary — a map that rewrites an
// `x-aggrega-go-type` value before emission. A schema records host type
// spellings; a target whose package cannot reference those spellings (a
// cross-package opaque type, a host-only container) maps them to a neutral
// spelling (e.g. an opaque host type to json.RawMessage, an order-preserving
// host container to a plain map). Absence of an entry passes the annotation
// value through verbatim.
//
// Import resolution is mechanical and orthogonal to type emission: an emitted
// type carrying a package selector (e.g. yaml.Node) is matched against the
// knownImports table and the corresponding import is emitted. Extending that
// table for a new package is a one-line mechanical change, not a vocabulary
// decision.
//
// Determinism: fields emit in the schema's property declaration order, and
// multi-type files emit their types in `$defs` declaration order
// (encoding/json preserves object key order via json.RawMessage + manual
// decode here), so regeneration is byte-stable.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// kindSource binds an output filename to the Go type name and the embedded
// schema bytes the generator reads. Adding a kind is a one-line entry.
type kindSource struct {
	typeName string
	schema   string
}

var kindSources = map[string]kindSource{
	"feature_gen.go":          {"Feature", schemas.FeatureV1alpha1},
	"manifest_gen.go":         {"Manifest", schemas.ManifestV1alpha1},
	"recipe_gen.go":           {"Recipe", schemas.RecipeV1alpha1},
	"featurerecipe_gen.go":    {"FeatureRecipe", schemas.FeatureRecipeV1alpha1},
	"schemalibrary_gen.go":    {"SchemaLibrary", schemas.SchemaLibraryV1alpha1},
	"mappinglibrary_gen.go":   {"MappingLibrary", schemas.MappingLibraryV1alpha1},
	"workloadlibrary_gen.go":  {"WorkloadLibrary", schemas.WorkloadLibraryV1alpha1},
	"artifactslibrary_gen.go": {"ArtifactsLibrary", schemas.ArtifactsLibraryV1alpha1},
	"aggregate_gen.go":        {"Aggregate", schemas.AggregateV1alpha1},
	"release_gen.go":          {"Release", schemas.ReleaseV1alpha1},
	"component_gen.go":        {"Component", schemas.ComponentV1alpha1},
	"plugin_gen.go":           {"Plugin", schemas.PluginV1alpha1},
	"workload_gen.go":         {"Workload", schemas.WorkloadV1alpha1},
}

// enumSource binds an output filename to a string-enum-const emission: the
// embedded schema, the destination package, the schema file basename for the
// generated header, and the explicit set of enum-bearing field names to emit.
//
// The emitter is a general closed-string-vocabulary → Go bare-const capability:
// for each named field it locates the field's string `enum`, or the single value
// its string `const` declares, anywhere in the schema
// (recursing through `properties`, `items`, and `x-aggrega-go-type`-annotated
// nodes alike) and emits one bare untyped string const per value, named
// Pascal(field)+Pascal(value) for an `enum` and Pascal(field) alone for a
// `const`, whose one value is the field itself. `fields` scopes the emission to the vocabularies
// this file owns rather than every one in the schema — the schema also carries
// vocabularies (pluginType) whose Go identity lives in hand-written
// homes; listing only the owned fields keeps a single home per vocabulary.
type enumSource struct {
	schema     string
	schemaFile string
	pkg        string
	fields     []string
	// lang selects the emission backend: an empty value (or "go") emits Go bare
	// consts; "rust" emits Rust &str consts; "ts" emits TypeScript string
	// consts. One schema declaration feeds every language backend, so the code
	// vocabulary is single-sourced across all three the way the wire structs are.
	lang string
	// regen is the "Regenerate with ..." header line(s) for this target, so a
	// target committed under pkg/types and one committed across the aggrega-abi
	// submodule boundary each name their own regeneration command.
	regen string
	// doc is a file-level documentation comment emitted after the header,
	// explaining the file's single-source role and any deliberately-separate
	// sibling vocabularies. Each line is emitted as a `//` comment.
	doc string
}

var enumSources = map[string]enumSource{
	"plugin_enums_gen.go": {
		schema:     schemas.PluginV1alpha1,
		schemaFile: "plugin-v1alpha1.schema.json",
		pkg:        "types",
		fields:     []string{"operates", "scope"},
		regen:      "// Regenerate with `go generate ./pkg/types/...`.\n",
		doc: "These consts are the single source for the Plugin entity's per-export\n" +
			"operates and scope vocabularies. The describe() host descriptor re-exports\n" +
			"them so the load-time shape gate and the runtime read one set of values.\n" +
			"\n" +
			"The feature-recipe-pipeline package consumes this scope vocabulary by\n" +
			"aliasing these consts, so the Go values are single-sourced. A separate\n" +
			"schema (the feature-recipe pipeline-step schema) declares the same scope\n" +
			"enum independently of the Plugin schema; unifying the two schemas is a\n" +
			"tracked follow-up.",
	},

	// Host-function error codes — the single source of the machine-matchable
	// wire-code vocabulary, generated into the aggrega-abi Go/Rust/TS codec from
	// one schema so the three languages share one closed set. The host sets a
	// code as error_code on a host-function reply envelope when a callback fails;
	// a guest branches on it without parsing the message.
	"hostfunc_error_enums_gen.go": {
		schema:     schemas.WasmHostfuncErrorV1alpha1,
		schemaFile: "wasm-hostfunc-error-v1alpha1.schema.json",
		pkg:        "abi",
		fields:     []string{"code"},
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        hostfuncErrorEnumDoc,
	},
	"hostfunc_error_enums_gen.rs": {
		schema:     schemas.WasmHostfuncErrorV1alpha1,
		schemaFile: "wasm-hostfunc-error-v1alpha1.schema.json",
		fields:     []string{"code"},
		lang:       "rust",
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        hostfuncErrorEnumDoc,
	},
	"hostfunc_error_enums_gen.ts": {
		schema:     schemas.WasmHostfuncErrorV1alpha1,
		schemaFile: "wasm-hostfunc-error-v1alpha1.schema.json",
		fields:     []string{"code"},
		lang:       "ts",
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        hostfuncErrorEnumDoc,
	},

	// Plugin ABI version — the descriptor's abi_version is declared as a
	// single-valued const in the describe schema, so the schema is the one place
	// the version is written and the Go/Rust/TS consts below are generated from
	// it. The three plugin kits re-export the generated const instead of
	// declaring their own copy, so bumping the ABI is a one-line schema edit.
	"abi_version_enums_gen.go": {
		schema:     schemas.WasmDescribeV1alpha1,
		schemaFile: "wasm-describe-v1alpha1.schema.json",
		pkg:        "abi",
		fields:     []string{"abi_version"},
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        abiVersionEnumDoc,
	},
	"abi_version_enums_gen.rs": {
		schema:     schemas.WasmDescribeV1alpha1,
		schemaFile: "wasm-describe-v1alpha1.schema.json",
		fields:     []string{"abi_version"},
		lang:       "rust",
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        abiVersionEnumDoc,
	},
	"abi_version_enums_gen.ts": {
		schema:     schemas.WasmDescribeV1alpha1,
		schemaFile: "wasm-describe-v1alpha1.schema.json",
		fields:     []string{"abi_version"},
		lang:       "ts",
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        abiVersionEnumDoc,
	},

	// Diagnostic severity — the shared diagnostic's severity is declared as a
	// closed three-member enum on the diagnostic schema, so the schema is the one
	// place the vocabulary is written and the Go/Rust/TS consts below are
	// generated from it. Every socket that carries a diagnostic reports from this
	// one set, and the compiler re-exports these consts rather than declaring its
	// own copies.
	"diagnostic_enums_gen.go": {
		schema:     schemas.WasmDiagnosticV1alpha1,
		schemaFile: "wasm-diagnostic-v1alpha1.schema.json",
		pkg:        "abi",
		fields:     []string{"severity"},
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        diagnosticSeverityEnumDoc,
	},
	"diagnostic_enums_gen.rs": {
		schema:     schemas.WasmDiagnosticV1alpha1,
		schemaFile: "wasm-diagnostic-v1alpha1.schema.json",
		fields:     []string{"severity"},
		lang:       "rust",
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        diagnosticSeverityEnumDoc,
	},
	"diagnostic_enums_gen.ts": {
		schema:     schemas.WasmDiagnosticV1alpha1,
		schemaFile: "wasm-diagnostic-v1alpha1.schema.json",
		fields:     []string{"severity"},
		lang:       "ts",
		regen:      "// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		doc:        diagnosticSeverityEnumDoc,
	},
}

// abiVersionEnumDoc is the shared file-level doc for the three plugin-ABI-version
// targets, so the Go/Rust/TS files carry one explanation.
const abiVersionEnumDoc = "This const is the plugin ABI version, generated from the descriptor schema's\n" +
	"declaration of abi_version. The schema pins that field to a single value, and\n" +
	"this const, its Rust and TypeScript siblings, and the three plugin kits'\n" +
	"re-exports are all generated or aliased from that one declaration.\n" +
	"\n" +
	"A guest stamps the version into the descriptor it returns from describe(); the\n" +
	"host accepts exactly the version it speaks and refuses a guest built against\n" +
	"any other at load rather than mis-invoking it at dispatch.\n" +
	"\n" +
	"The version the host speaks has its own home, and it is NOT generated from\n" +
	"this schema: the compiler declares it by hand as ABIVersionV1Alpha1 in\n" +
	"aggrega-cli/internal/wasm/describe.go, and the descriptor check in that file\n" +
	"is what rejects a guest at load. That home is named here because it is the\n" +
	"enforcing one, and it is held to this schema by a cross-check in the\n" +
	"compiler's own test suite. So bumping the ABI is a schema edit plus a\n" +
	"regeneration AND a hand edit to the host constant, and a bump that stops at\n" +
	"the regeneration fails that cross-check rather than silently leaving the\n" +
	"compiler refusing every guest built against the new version.\n" +
	"\n" +
	"It is unrelated to the IR payload's schema version, which travels in an IR\n" +
	"graph's provenance and versions the graph shape rather than the plugin ABI.\n" +
	"The two spell the same string today and are separate vocabularies."

// diagnosticSeverityEnumDoc is the shared file-level doc for the three
// diagnostic-severity enum targets, so the Go/Rust/TS files carry one
// explanation.
const diagnosticSeverityEnumDoc = "These consts are the single source of the shared diagnostic's severity wire\n" +
	"vocabulary, closed to the three values every Wasm socket reports from: halt\n" +
	"stops the build and names what to fix, warn surfaces alongside a build that\n" +
	"still completes, and info records the observation and leaves the build outcome\n" +
	"unchanged. Generated into Go, Rust, and TypeScript from one schema so a guest\n" +
	"and the host name the same three values.\n" +
	"\n" +
	"The compiler re-exports these consts under its own analyzer vocabulary rather\n" +
	"than declaring copies of the literals, so the value a guest writes and the\n" +
	"value the host routes on have one home.\n" +
	"\n" +
	"A diagnostic's severity is distinct from the editor-protocol severity the\n" +
	"language server publishes and from the deprecation-marker severity a registry\n" +
	"marker carries: those are separate vocabularies on separate boundaries, and a\n" +
	"value crossing between them is translated rather than copied."

// hostfuncErrorEnumDoc is the shared file-level doc for the three host-function
// error-code enum targets, so the Go/Rust/TS files carry one explanation.
const hostfuncErrorEnumDoc = "These consts are the single source of the host-function error-code wire\n" +
	"vocabulary, shared across every host-function reply envelope. The host sets\n" +
	"one as error_code beside the free-form error message when a callback fails;\n" +
	"a guest branches on the code without parsing the message, tolerating an\n" +
	"absent code (older host) or an unrecognized one (newer host) by falling back\n" +
	"to the message. Generated into Go, Rust, and TypeScript from one schema so\n" +
	"the three codecs share one closed vocabulary."

// codecTarget binds an output filename to a multi-type codec emission: the
// embedded `$defs`-shaped schema, the schema file basename for the generated
// header, the destination package name, the per-target type vocabulary that
// rewrites x-aggrega-go-type values, and the target language. Each `$defs`
// entry becomes one typed value plus a streaming JSON codec.
//
// lang selects the backend: "" or "go" emits the reflect-free Go codec
// (Marshal/UnmarshalJSON); "rust" emits serde-derive structs (BTreeMap for
// sorted map keys, RawValue for verbatim opaque carry); "ts" emits TypeScript
// interfaces plus thin per-type sorting encoders. The Go, Rust, and TS codecs
// produce byte-identical wire output from one schema: maps emit in sorted key
// order and opaque values carry verbatim.
type codecTarget struct {
	schema     string
	schemaFile string
	pkg        string
	vocabulary map[string]string
	lang       string
	// helpers controls whether the Go backend emits the shared streaming-decode
	// helpers (expectDelim, readKey, writeJSONString, …) into this file. Exactly
	// one Go codec file per package emits them; the others in the same package
	// reference them. (Ignored by the Rust/TS backends, whose wire codec is the
	// language's own serde/JSON runtime.)
	helpers bool
	// externalDefTypes names generated types that live in the same destination
	// package but are emitted by a different codec target (a cross-codec, in-
	// package reference — e.g. the policy codec's graph field whose type is the
	// IRGraph emitted by the IR codec target into the same abi package). Listing
	// them here lets the Go backend treat such a field as a generated def type and
	// dispatch encode/decode directly to its own generated Marshal/UnmarshalJSON,
	// keeping the codec dispatch-free across the cross-codec boundary rather than
	// falling back to a reflective json.Marshal. (Ignored by the Rust/TS backends,
	// whose runtime resolves the in-package type by name.)
	externalDefTypes []string
}

// emitsCBOR reports whether a codec target carries the canonical-CBOR codec
// alongside JSON — EncodeCBOR/DecodeCBOR plus a `cbor:"<key>[,omitempty]"`
// struct tag in Go, and the hand-written CBOR encoders in Rust and TS. Every
// target carries it: a socket payload is encoded by one side and decoded by the
// other, so a target that lacked the codec would leave one end of that wire with
// no way to speak it.
func (t codecTarget) emitsCBOR() bool {
	return true
}

// frpGuestNeutralVocabulary maps the FRP schema's host type spellings to a
// neutral, cycle-free guest vocabulary: an opaque cross-package host type
// becomes json.RawMessage (verbatim passthrough preserves its bytes), and the
// order-preserving host container becomes a plain map (key order is the
// emitting host's concern; a guest that selects by content reads a map). With
// this vocabulary the generated codec references only builtins, encoding/json,
// and its own def-local types — no cross-package import, so the codec compiles
// in a standalone, dependency-free module a Wasm guest can import directly.
var frpGuestNeutralVocabulary = map[string]string{
	"renderctx.RenderContext": "json.RawMessage",
	"OrderedMap[Unit]":        "map[string]Unit",
	"OrderedMap[any]":         "map[string]json.RawMessage",
	"DocumentScope":           "string",
}

// frpHostVocabulary maps the FRP schema's container and diagnostic annotations to
// the host realization: the order-preserving types.OrderedMap (qualified, since
// the host codec lands in the feature-recipe-pipeline package) so the host wire is
// byte-deterministic across runs of identical input (the determinism contract),
// and the shared diagnostic at renderctx.Diagnostic (the host realization of the
// single generated diagnostic shape). The render context carries its real host
// spelling verbatim, so it needs no entry.
//
// The document-root scope resolves to a plain string on both sides of this wire,
// and deliberately so. The codec classifies a value by the spelling of its Go
// type, so a distinct named type over a string is a spelling it cannot place: it
// falls back to carrying the value opaquely, which puts a quoted JSON fragment on
// the binary wire where the other side expects a plain text string. Sharing one
// spelling with the guest is what keeps the two ends of this wire able to read
// each other. The closed vocabulary itself is not lost — the two values are named
// constants in the pipeline package, single-sourced from the Plugin entity's scope
// vocabulary.
var frpHostVocabulary = map[string]string{
	"OrderedMap[Unit]": "types.OrderedMap[Unit]",
	"OrderedMap[any]":  "types.OrderedMap[any]",
	"Diagnostic":       "renderctx.Diagnostic",
	"DocumentScope":    "string",
}

// mutatorGuestVocabulary maps the mutator schema's host container spellings to
// the neutral guest vocabulary: the order-preserving host containers become
// plain maps (the guest reads a map; key order on the wire is the host's
// concern). The mutator carries no cross-package opaque host type.
var mutatorGuestVocabulary = map[string]string{
	"OrderedMap[MutatorNode]": "map[string]MutatorNode",
	"OrderedMap[string]":      "map[string]string",
}

// mutatorHostVocabulary maps the mutator schema's container annotations to the
// host realization: the order-preserving types.OrderedMap (qualified, since the
// codec lands in package topology) so the host wire is byte-deterministic across
// runs of identical input (the determinism contract). The guest reads a plain
// map (its vocabulary rewrites OrderedMap -> map): key order on the wire is the
// emitting host's concern.
var mutatorHostVocabulary = map[string]string{
	"OrderedMap[MutatorNode]": "types.OrderedMap[MutatorNode]",
	"OrderedMap[string]":      "types.OrderedMap[string]",
}

// annotationTransformGuestVocabulary maps the annotation-transform schema's host
// container spelling to the neutral guest vocabulary: the order-preserving host
// container becomes a plain map (the guest reads a map; key order on the wire is
// the host's concern). The annotation-transform carries no cross-package opaque
// host type.
var annotationTransformGuestVocabulary = map[string]string{
	"OrderedMap[string]": "map[string]string",
}

// annotationTransformHostVocabulary maps the annotation-transform schema's
// container annotation to the host realization: the order-preserving
// types.OrderedMap (qualified, since the codec lands in package pipeline) so the
// host wire is byte-deterministic across runs of identical input (the determinism
// contract). The guest reads a plain map (its vocabulary rewrites OrderedMap ->
// map): key order on the wire is the emitting host's concern.
var annotationTransformHostVocabulary = map[string]string{
	"OrderedMap[string]": "types.OrderedMap[string]",
}

// linterGuestVocabulary is empty: the linter schema's opaque fields already
// carry the neutral json.RawMessage spelling, and it declares no host container
// type. Absence of an entry passes each annotation through verbatim — including
// the policies field, whose x-aggrega-go-type: Policies annotation resolves to
// the typed Policies struct the guest realizes.
var linterGuestVocabulary = map[string]string{}

// linterHostVocabulary maps the linter schema's policies annotation to the host
// realization. The host assembles the publishing-discipline configuration
// dynamically from the workspace config plus CLI tri-state overrides and pipes
// it through as opaque bytes (the proven cross-boundary json.RawMessage carry),
// so the host LinterInput.Policies is a json.RawMessage rather than the typed
// Policies struct the guest realizes. The proposed/previous/parent fields
// already carry json.RawMessage from the schema annotations.
var linterHostVocabulary = map[string]string{
	"Policies":   "json.RawMessage",
	"Diagnostic": "abi.Diagnostic",
}

// irGuestVocabulary maps the IR schema's host container spellings to the neutral
// guest vocabulary: every order-preserving OrderedMap becomes a plain map (the
// guest reads a map; key order on the wire is the emitting host's concern). The
// inject/values/infra fields already carry the opaque json.RawMessage spelling
// from the schema annotations, and the recursive/pointer fields carry their
// pointer spelling, so neither needs a vocabulary entry.
var irGuestVocabulary = map[string]string{
	"OrderedMap[IRNode]":                        "map[string]IRNode",
	"OrderedMap[IREdge]":                        "map[string]IREdge",
	"OrderedMap[string]":                        "map[string]string",
	"OrderedMap[any]":                           "map[string]json.RawMessage",
	"OrderedMap[FieldProvenance]":               "map[string]FieldProvenance",
	"OrderedMap[IRFeatureEntry]":                "map[string]IRFeatureEntry",
	"OrderedMap[OrderedMap[IRFeatureEntry]]":    "map[string]map[string]IRFeatureEntry",
	"OrderedMap[OrderedMap[IRBoundaryFeature]]": "map[string]map[string]IRBoundaryFeature",
}

// policyGuestVocabulary is empty: the policy schema's graph field is annotated
// with the IRGraph type (emitted by the IR codec target into the same abi
// package and named in the policy target's externalDefTypes), and the rest are
// plain scalars and string slices. Absence of an entry passes annotations
// through verbatim.
var policyGuestVocabulary = map[string]string{}

// describeGuestVocabulary is empty: the descriptor's only non-scalar field is
// settings_schema, whose x-aggrega-go-type: json.RawMessage annotation already
// carries the neutral opaque spelling (verbatim passthrough preserves the
// declared JSON Schema document's bytes). Every other field is a scalar, a
// string slice, a boolean, or a def-local nested object referenced by $ref, so
// each annotation passes through verbatim with no vocabulary entry.
var describeGuestVocabulary = map[string]string{}

// describeHostVocabulary maps the descriptor schema's opaque field spelling to
// the host realization. The only non-scalar carry is settings_schema, whose
// x-aggrega-go-type: json.RawMessage annotation already spells the neutral
// opaque type the host reads (verbatim passthrough preserves the declared JSON
// Schema document's bytes), so the map is empty and every annotation passes
// through verbatim — mirroring the guest describe vocabulary.
var describeHostVocabulary = map[string]string{}

// diagnosticGuestVocabulary is empty: the shared diagnostic's non-scalar field is
// the optional location (a def-local Location, itself carrying an optional
// def-local Span), whose x-aggrega-go-type annotations already carry the def-local
// pointer spellings; every other field is a scalar. Absence of an entry passes
// each annotation through verbatim.
var diagnosticGuestVocabulary = map[string]string{}

// providerGuestVocabulary maps the provider render schema's host type spellings
// to the neutral, cycle-free guest vocabulary: an opaque cross-package host type
// (the render-time context, the per-render boundary context, the enriched uses
// edge the boundary-env-overrides input carries) becomes json.RawMessage
// (verbatim passthrough preserves its bytes), and every order-preserving host
// container becomes a plain map (the guest reads a map; key order on the wire is
// the emitting host's concern). The shared diagnostic is not opaque: it is the
// generated abi.Diagnostic (named in the target's externalDefTypes), so the guest
// codec dispatches straight to its own generated codec. The
// ordered structured content of an output file is the same order-preserving-any
// container as a node's values, so both surface to the guest as
// map[string]json.RawMessage — the guest reads and echoes the content by key,
// and the host runs the single serializer that turns it into bytes. The
// pipeline-codec unit and patched-unit shapes surface to the guest opaquely
// (json.RawMessage): an external PDK provider guest that implements the optional
// carve/reassemble exports carries them verbatim, while the host realizes them
// as the typed frp shapes its pipeline drives.
var providerGuestVocabulary = map[string]string{
	"renderctx.RenderContext":   "json.RawMessage",
	"renderctx.BoundaryContext": "json.RawMessage",
	"Unit":                      "json.RawMessage",
	"PatchedUnit":               "json.RawMessage",
	"JSONPatchOp":               "json.RawMessage",
	"types.EnrichedEdge":        "json.RawMessage",
	"OrderedMap[any]":           "map[string]json.RawMessage",
	"OrderedMap[[]string]":      "map[string][]string",
	"types.OrderedMap[string]":  "map[string]string",
	"types.OrderedMap[types.OrderedMap[string]]": "map[string]map[string]string",
	"OrderedMap[types.IRFeatureEntry]":           "map[string]json.RawMessage",
	"OrderedMap[types.Component]":                "map[string]json.RawMessage",
	"OrderedMap[types.Feature]":                  "map[string]json.RawMessage",
	"OrderedMap[types.Recipe]":                   "map[string]json.RawMessage",
	"OrderedMap[types.FeatureRecipe]":            "map[string]json.RawMessage",
	"OrderedMap[types.MappingLibrary]":           "map[string]json.RawMessage",
	"types.Workload":                             "json.RawMessage",
	"map[string]types.OrderedMap[string]":        "map[string]map[string]string",
}

// providerHostVocabulary maps the provider render schema's container annotations
// to the host realization: the order-preserving types.OrderedMap (qualified,
// since the host codec lands in package renderer) so the host wire is
// byte-deterministic across runs of identical input (the determinism contract).
// The ordered structured content of an output file is the recursively
// order-preserving types.OrderedMap[any], so the guest's key order is carried to
// the host serializer intact rather than collapsed by an unordered map. The
// render context and boundary context carry their real host spelling verbatim (no
// entry needed); the shared diagnostic maps to renderctx.Diagnostic (the host
// realization of the single generated diagnostic shape). The host marshals these
// through their own codecs. The
// pipeline-codec unit and patched-unit shapes resolve to the typed frp shapes
// the host pipeline carves and reassembles with, so the codec seam reuses the
// same types end-to-end rather than a parallel host copy.
var providerHostVocabulary = map[string]string{
	"Unit":                             "frp.Unit",
	"PatchedUnit":                      "frp.PatchedUnit",
	"JSONPatchOp":                      "frp.JSONPatchOp",
	"OrderedMap[any]":                  "types.OrderedMap[any]",
	"OrderedMap[[]string]":             "types.OrderedMap[[]string]",
	"OrderedMap[types.IRFeatureEntry]": "types.OrderedMap[types.IRFeatureEntry]",
	"OrderedMap[types.Component]":      "types.OrderedMap[types.Component]",
	"OrderedMap[types.Feature]":        "types.OrderedMap[types.Feature]",
	"OrderedMap[types.Recipe]":         "types.OrderedMap[types.Recipe]",
	"OrderedMap[types.FeatureRecipe]":  "types.OrderedMap[types.FeatureRecipe]",
	"OrderedMap[types.MappingLibrary]": "types.OrderedMap[types.MappingLibrary]",
	"Diagnostic":                       "renderctx.Diagnostic",
}

// cliGuestVocabulary is empty: the CLI run input carries only an argument
// vector (a plain string slice) and declares no host container or opaque
// cross-package type, so every annotation passes through verbatim. It exists as
// the codec target's vocabulary slot for symmetry with the other socket targets
// and as the seam a future granted-input field would hook into.
var cliGuestVocabulary = map[string]string{}

// parseGuestVocabulary is empty: the parse schema's non-scalar carries — the
// source bytes ([]byte, base64 on the wire), the opaque options and tree
// (json.RawMessage passthrough), and the def-local span/diagnostic types
// referenced by $ref — already spell the neutral guest types the generator
// emits. The parser attaches no meaning to the tree, so it carries it verbatim;
// no host container or cross-package opaque type needs a rewrite, and every
// annotation passes through unchanged.
var parseGuestVocabulary = map[string]string{}

// importGuestVocabulary is empty: the import schema's non-scalar carries — the
// source bytes ([]byte, base64 on the wire), the opaque settings document
// (json.RawMessage passthrough), the string-to-string label and annotation maps
// (map[string]string, sorted-key marshal), and the def-local nested types
// referenced by $ref — already spell the neutral guest types the generator
// emits. No host container or cross-package opaque type needs a rewrite, and
// every annotation passes through unchanged.
var importGuestVocabulary = map[string]string{}

var codecTargets = map[string]codecTarget{
	"frp_gen.go": {
		schema:           schemas.WasmFrpStepV1alpha1,
		schemaFile:       "wasm-frp-step-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       frpGuestNeutralVocabulary,
		helpers:          true, // FRP owns the shared streaming-decode helpers in package abi
		externalDefTypes: []string{"Diagnostic"},
	},
	"frp_gen.rs": {
		schema:           schemas.WasmFrpStepV1alpha1,
		schemaFile:       "wasm-frp-step-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       frpGuestNeutralVocabulary,
		lang:             "rust",
		externalDefTypes: []string{"Diagnostic"},
	},
	"frp_gen.ts": {
		schema:           schemas.WasmFrpStepV1alpha1,
		schemaFile:       "wasm-frp-step-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       frpGuestNeutralVocabulary,
		lang:             "ts",
		externalDefTypes: []string{"Diagnostic"},
	},
	"frp_host_gen.go": {
		schema:     schemas.WasmFrpStepV1alpha1,
		schemaFile: "wasm-frp-step-v1alpha1.schema.json",
		pkg:        "featurerecipe_pipeline",
		vocabulary: frpHostVocabulary,
		helpers:    true, // the host feature-recipe-pipeline codec is alone in its package
	},

	// Shared diagnostic — guest codecs into aggrega-abi (Go/Rust/TS). The single
	// diagnostic shape every socket shares; a socket that carries diagnostics
	// references it (a $ref + x-aggrega-go-type: Diagnostic annotation) and lists
	// it in externalDefTypes, so the socket's codec dispatches directly to this
	// type's own generated codec rather than through a reflective json.Marshal —
	// the same single-sourced cross-codec pattern the policy graph's IRGraph uses.
	// Emitted once into package abi; the abi Go codec references the helpers FRP
	// owns in package abi (helpers: false, the default).
	"diagnostic_gen.go": {
		schema:     schemas.WasmDiagnosticV1alpha1,
		schemaFile: "wasm-diagnostic-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: diagnosticGuestVocabulary,
	},
	"diagnostic_gen.rs": {
		schema:     schemas.WasmDiagnosticV1alpha1,
		schemaFile: "wasm-diagnostic-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: diagnosticGuestVocabulary,
		lang:       "rust",
	},
	"diagnostic_gen.ts": {
		schema:     schemas.WasmDiagnosticV1alpha1,
		schemaFile: "wasm-diagnostic-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: diagnosticGuestVocabulary,
		lang:       "ts",
	},

	// CLI socket — guest codecs into aggrega-abi (Go/Rust/TS). A CLI plugin
	// receives the invocation's argument vector; the exit code is the guest
	// export's integer return and stdout/stderr flow through host functions, so
	// the socket carries an input shape only. The abi Go codec references the
	// helpers FRP owns in package abi (helpers: false, the default).
	"cli_gen.go": {
		schema:     schemas.WasmCliV1alpha1,
		schemaFile: "wasm-cli-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: cliGuestVocabulary,
	},
	"cli_gen.rs": {
		schema:     schemas.WasmCliV1alpha1,
		schemaFile: "wasm-cli-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: cliGuestVocabulary,
		lang:       "rust",
	},
	"cli_gen.ts": {
		schema:     schemas.WasmCliV1alpha1,
		schemaFile: "wasm-cli-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: cliGuestVocabulary,
		lang:       "ts",
	},

	// Describe socket — guest codecs into aggrega-abi (Go/Rust/TS). Every plugin
	// returns the PluginDescriptor shape from its describe() export; the codec
	// realizes that self-declaration (the exports, the optional provider render
	// contract, the optional CLI commands and flags, the settings schema, the
	// authorship metadata). The abi Go codec references the helpers FRP owns in
	// package abi (helpers: false, the default). The only non-scalar carry is the
	// opaque settings_schema (json.RawMessage passthrough).
	"describe_gen.go": {
		schema:     schemas.WasmDescribeV1alpha1,
		schemaFile: "wasm-describe-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: describeGuestVocabulary,
	},
	"describe_gen.rs": {
		schema:     schemas.WasmDescribeV1alpha1,
		schemaFile: "wasm-describe-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: describeGuestVocabulary,
		lang:       "rust",
	},
	"describe_gen.ts": {
		schema:     schemas.WasmDescribeV1alpha1,
		schemaFile: "wasm-describe-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: describeGuestVocabulary,
		lang:       "ts",
	},
	"describe_host_gen.go": {
		schema:     schemas.WasmDescribeV1alpha1,
		schemaFile: "wasm-describe-v1alpha1.schema.json",
		pkg:        "wasm",
		vocabulary: describeHostVocabulary,
		helpers:    true, // the host describe codec is alone in package wasm
	},

	// Mutator socket — guest codecs into aggrega-abi (Go/Rust/TS) and the host
	// codec into aggrega-cli's topology package. The abi Go codec references the
	// helpers FRP owns in package abi (helpers: false); the topology host codec
	// is alone in its package and owns its helpers (helpers: true).
	"mutator_gen.go": {
		schema:     schemas.WasmMutatorV1alpha1,
		schemaFile: "wasm-mutator-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: mutatorGuestVocabulary,
	},
	"mutator_gen.rs": {
		schema:     schemas.WasmMutatorV1alpha1,
		schemaFile: "wasm-mutator-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: mutatorGuestVocabulary,
		lang:       "rust",
	},
	"mutator_gen.ts": {
		schema:     schemas.WasmMutatorV1alpha1,
		schemaFile: "wasm-mutator-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: mutatorGuestVocabulary,
		lang:       "ts",
	},
	"wasm_types_gen.go": {
		schema:     schemas.WasmMutatorV1alpha1,
		schemaFile: "wasm-mutator-v1alpha1.schema.json",
		pkg:        "topology",
		vocabulary: mutatorHostVocabulary,
		helpers:    true, // the host mutator codec is alone in package topology
	},

	// Annotation-transform socket — guest codecs into aggrega-abi (Go/Rust/TS) and
	// the host codec into aggrega-cli's pipeline package. The abi Go codec
	// references the helpers FRP owns in package abi (helpers: false); the pipeline
	// host codec is alone in its package for this schema and owns its helpers
	// (helpers: true).
	"annotationtransform_gen.go": {
		schema:     schemas.WasmAnnotationTransformV1alpha1,
		schemaFile: "wasm-annotation-transform-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: annotationTransformGuestVocabulary,
	},
	"annotationtransform_gen.rs": {
		schema:     schemas.WasmAnnotationTransformV1alpha1,
		schemaFile: "wasm-annotation-transform-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: annotationTransformGuestVocabulary,
		lang:       "rust",
	},
	"annotationtransform_gen.ts": {
		schema:     schemas.WasmAnnotationTransformV1alpha1,
		schemaFile: "wasm-annotation-transform-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: annotationTransformGuestVocabulary,
		lang:       "ts",
	},
	"annotationtransform_host_gen.go": {
		schema:     schemas.WasmAnnotationTransformV1alpha1,
		schemaFile: "wasm-annotation-transform-v1alpha1.schema.json",
		pkg:        "pipeline",
		vocabulary: annotationTransformHostVocabulary,
		helpers:    true, // the host annotation-transform codec is alone in package pipeline
	},

	// Linter socket — guest codecs into aggrega-abi (Go/Rust/TS) and the host
	// codec into aggrega-cli's validator package (the shipped `aggrega lint`
	// publishing-linter dispatch). The host realizes policies as an opaque
	// json.RawMessage it assembles dynamically; the guest realizes the typed
	// Policies struct.
	"linter_gen.go": {
		schema:           schemas.WasmLinterV1alpha1,
		schemaFile:       "wasm-linter-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       linterGuestVocabulary,
		externalDefTypes: []string{"Diagnostic"},
	},
	"linter_gen.rs": {
		schema:           schemas.WasmLinterV1alpha1,
		schemaFile:       "wasm-linter-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       linterGuestVocabulary,
		lang:             "rust",
		externalDefTypes: []string{"Diagnostic"},
	},
	"linter_gen.ts": {
		schema:           schemas.WasmLinterV1alpha1,
		schemaFile:       "wasm-linter-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       linterGuestVocabulary,
		lang:             "ts",
		externalDefTypes: []string{"Diagnostic"},
	},
	"linter_host_gen.go": {
		schema:     schemas.WasmLinterV1alpha1,
		schemaFile: "wasm-linter-v1alpha1.schema.json",
		pkg:        "validator",
		vocabulary: linterHostVocabulary,
		helpers:    true, // the host linter codec is alone in package validator
	},

	// IR graph — guest codec into aggrega-abi (Go/Rust/TS). The full
	// provider-agnostic IR graph the Topology Builder emits; the policy socket's
	// graph references it. inject/values/infra carry opaque (the schema annotates
	// them json.RawMessage); the one reference cycle — field provenance pointing at
	// the annotation contribution it suppressed — is broken by a pointer.
	"ir_gen.go": {
		schema:     schemas.IRSchemaV1alpha1,
		schemaFile: "ir-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: irGuestVocabulary,
	},
	"ir_gen.rs": {
		schema:     schemas.IRSchemaV1alpha1,
		schemaFile: "ir-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: irGuestVocabulary,
		lang:       "rust",
	},
	"ir_gen.ts": {
		schema:     schemas.IRSchemaV1alpha1,
		schemaFile: "ir-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: irGuestVocabulary,
		lang:       "ts",
	},

	// Policy socket — guest codec into aggrega-abi (Go/Rust/TS). The
	// graph field references the IRGraph emitted by the IR codec into the same abi
	// package; externalDefTypes makes the Go codec dispatch directly to IRGraph's
	// generated codec rather than through a reflective json.Marshal.
	"policy_gen.go": {
		schema:           schemas.WasmPolicyV1alpha1,
		schemaFile:       "wasm-policy-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       policyGuestVocabulary,
		externalDefTypes: []string{"IRGraph", "Diagnostic"},
	},
	"policy_gen.rs": {
		schema:           schemas.WasmPolicyV1alpha1,
		schemaFile:       "wasm-policy-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       policyGuestVocabulary,
		lang:             "rust",
		externalDefTypes: []string{"IRGraph", "Diagnostic"},
	},
	"policy_gen.ts": {
		schema:           schemas.WasmPolicyV1alpha1,
		schemaFile:       "wasm-policy-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       policyGuestVocabulary,
		lang:             "ts",
		externalDefTypes: []string{"IRGraph", "Diagnostic"},
	},

	// Provider render socket — guest codecs into aggrega-abi (Go/Rust/TS) and the
	// host codec into aggrega-cli's renderer package. The guest realizes the
	// order-preserving containers as plain maps and the opaque cross-package host
	// types (render context, boundary context, diagnostic) as json.RawMessage; the
	// host realizes the containers as types.OrderedMap and the opaque host types at
	// their real spelling. The abi Go codec references the helpers FRP owns in
	// package abi (helpers: false); the renderer host codec is alone in its package
	// and owns its helpers (helpers: true).
	//
	// The output basenames carry the `providerrender_` stem to name this render
	// socket's codec target distinctly; the generator dispatches by basename, so a
	// descriptive stem keeps this codec target unambiguous against other basenames.
	"providerrender_gen.go": {
		schema:           schemas.WasmProviderV1alpha1,
		schemaFile:       "wasm-provider-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       providerGuestVocabulary,
		externalDefTypes: []string{"Diagnostic"},
	},
	"providerrender_gen.rs": {
		schema:           schemas.WasmProviderV1alpha1,
		schemaFile:       "wasm-provider-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       providerGuestVocabulary,
		lang:             "rust",
		externalDefTypes: []string{"Diagnostic"},
	},
	"providerrender_gen.ts": {
		schema:           schemas.WasmProviderV1alpha1,
		schemaFile:       "wasm-provider-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       providerGuestVocabulary,
		lang:             "ts",
		externalDefTypes: []string{"Diagnostic"},
	},
	"providerrender_host_gen.go": {
		schema:     schemas.WasmProviderV1alpha1,
		schemaFile: "wasm-provider-v1alpha1.schema.json",
		pkg:        "rendercore",
		vocabulary: providerHostVocabulary,
		helpers:    true, // the host provider render codec is alone in package rendercore
	},

	// Parser socket — guest codecs into aggrega-abi (Go/Rust/TS). A parser turns
	// foreign source bytes into a canonical structured tree: it receives a
	// ParseInput (format, base64 bytes, opaque options) and returns a ParseOutput
	// (the opaque tree plus optional diagnostics and source spans). Every carry is
	// a scalar, base64 bytes, an opaque json.RawMessage, or a def-local span/
	// diagnostic type, so the guest vocabulary is empty. The abi Go codec
	// references the helpers FRP owns in package abi (helpers: false, the default).
	"parse_gen.go": {
		schema:           schemas.WasmParseV1alpha1,
		schemaFile:       "wasm-parse-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       parseGuestVocabulary,
		externalDefTypes: []string{"Diagnostic"},
	},
	"parse_gen.rs": {
		schema:           schemas.WasmParseV1alpha1,
		schemaFile:       "wasm-parse-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       parseGuestVocabulary,
		lang:             "rust",
		externalDefTypes: []string{"Diagnostic"},
	},
	"parse_gen.ts": {
		schema:           schemas.WasmParseV1alpha1,
		schemaFile:       "wasm-parse-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       parseGuestVocabulary,
		lang:             "ts",
		externalDefTypes: []string{"Diagnostic"},
	},

	// Importer socket — guest codecs into aggrega-abi (Go/Rust/TS). An importer
	// turns a foreign infrastructure description into Aggrega entities: it
	// receives an ImportInput (format, base64 primary bytes, optional named
	// sources, opaque settings) and returns an ImportOutput (the emitted
	// entities, the mapping decisions behind them, optional diagnostics, and
	// optional scaffold files). Every carry is a scalar, base64 bytes, an opaque
	// json.RawMessage, a string-to-string map, or a def-local nested type
	// referenced by $ref, so the guest vocabulary is empty. The abi Go codec
	// references the helpers FRP owns in package abi (helpers: false, the default).
	"import_gen.go": {
		schema:           schemas.WasmImportV1alpha1,
		schemaFile:       "wasm-import-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       importGuestVocabulary,
		externalDefTypes: []string{"Diagnostic"},
	},
	"import_gen.rs": {
		schema:           schemas.WasmImportV1alpha1,
		schemaFile:       "wasm-import-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       importGuestVocabulary,
		lang:             "rust",
		externalDefTypes: []string{"Diagnostic"},
	},
	"import_gen.ts": {
		schema:           schemas.WasmImportV1alpha1,
		schemaFile:       "wasm-import-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       importGuestVocabulary,
		lang:             "ts",
		externalDefTypes: []string{"Diagnostic"},
	},
	// The shared CBOR machinery emitted once per guest crate/package. These
	// targets are schema-free: they carry the read/write helpers, the runtime key
	// sort, the float16 conversions, and a dependency-free base64 codec that the
	// per-type CBOR codecs reference. The Go guest emits its helpers into the
	// helpers-bearing codec file (FRP); Rust and TS emit a dedicated module.
	"cbor_gen.rs": {
		pkg:  "abi",
		lang: "rust-cbor-helpers",
	},
	"cbor_gen.ts": {
		pkg:  "abi",
		lang: "ts-cbor-helpers",
	},
}

// knownImports maps a package selector that may appear in an x-aggrega-go-type
// annotation to its import path. Mechanical and extensible — a new qualified
// type (e.g. a future time.Duration inside a generic) is a one-line add here,
// not a vocabulary-improvisation halt.
var knownImports = map[string]string{
	"yaml":      "go.yaml.in/yaml/v3",
	"types":     "github.com/aggrega-ai/aggrega-cli/pkg/types",
	"renderctx": "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx",
	"frp":       "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline",
	"abi":       "github.com/aggrega-ai/aggrega-abi-go/pkg/abi",
}

// schemaNode is a decoded JSON Schema object node preserving the declared
// property order.
type schemaNode struct {
	Type        string
	Description string
	GoType      string // x-aggrega-go-type override (names a hand-written or generic type)
	Ref         string // $ref target (e.g. #/$defs/Unit); resolves to a Go type name
	Required    []string
	Enum        []string // enum values, in declared order (string enums only)
	// Const is a string `const` value, which is a closed vocabulary of exactly
	// one member: a schema declaring `const` fixes the field to that single
	// value the same way an `enum` fixes it to a set. A non-string const
	// (a number, a boolean) leaves this empty — see decodeNode.
	Const string
	props []namedNode // in declared order
	Items *schemaNode
	defs  []namedNode // $defs entries, in declared order (multi-type schemas only)
}

type namedNode struct {
	name string
	node schemaNode
}

func decodeNode(raw json.RawMessage) (schemaNode, error) {
	var n schemaNode
	// Decode the flat fields first.
	var flat struct {
		Type        string          `json:"type"`
		Description string          `json:"description"`
		GoType      string          `json:"x-aggrega-go-type"`
		Ref         string          `json:"$ref"`
		Required    []string        `json:"required"`
		Enum        []string        `json:"enum"`
		Const       json.RawMessage `json:"const"`
		Items       json.RawMessage `json:"items"`
		Properties  json.RawMessage `json:"properties"`
		Defs        json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return n, err
	}
	n.Type, n.Description, n.GoType, n.Ref, n.Required, n.Enum = flat.Type, flat.Description, flat.GoType, flat.Ref, flat.Required, flat.Enum
	// `const` is decoded as raw JSON rather than a string field because the
	// keyword admits any JSON type and the schema corpus carries a numeric one
	// (a lockfile version). A typed string field would fail the whole decode on
	// that schema; reading it raw and keeping only a string keeps every schema
	// decodable while a non-string const is simply not a string vocabulary.
	if len(flat.Const) > 0 {
		var s string
		if err := json.Unmarshal(flat.Const, &s); err == nil {
			n.Const = s
		}
	}
	if len(flat.Items) > 0 {
		it, err := decodeNode(flat.Items)
		if err != nil {
			return n, err
		}
		n.Items = &it
	}
	if len(flat.Properties) > 0 {
		ordered, err := decodeOrderedProps(flat.Properties)
		if err != nil {
			return n, err
		}
		n.props = ordered
	}
	if len(flat.Defs) > 0 {
		defs, err := decodeOrderedProps(flat.Defs)
		if err != nil {
			return n, err
		}
		n.defs = defs
	}
	return n, nil
}

// decodeOrderedProps walks the properties object's tokens in order,
// preserving declaration order (the determinism contract).
func decodeOrderedProps(raw json.RawMessage) ([]namedNode, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // opening '{'
		return nil, err
	}
	var out []namedNode
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := keyTok.(string)
		var sub json.RawMessage
		if err := dec.Decode(&sub); err != nil {
			return nil, err
		}
		node, err := decodeNode(sub)
		if err != nil {
			return nil, err
		}
		out = append(out, namedNode{name: key, node: node})
	}
	return out, nil
}

var initialisms = map[string]string{"api": "API", "url": "URL", "id": "ID", "ir": "IR", "uri": "URI", "json": "JSON", "dns": "DNS"}

// pascal converts a schema property key (camelCase or snake_case) to a Go
// exported field name, applying the Go initialism convention. Segment
// boundaries are an interior uppercase letter (camelCase) or an underscore
// (snake_case); the underscore is consumed, not emitted.
func pascal(s string) string {
	var segs []string
	cur := strings.Builder{}
	for i, r := range s {
		if r == '_' {
			segs = append(segs, cur.String())
			cur.Reset()
			continue
		}
		if i > 0 && r >= 'A' && r <= 'Z' {
			segs = append(segs, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	segs = append(segs, cur.String())
	out := strings.Builder{}
	for _, seg := range segs {
		if seg == "" {
			continue
		}
		low := strings.ToLower(seg)
		if init, ok := initialisms[low]; ok {
			out.WriteString(init)
		} else {
			out.WriteString(strings.ToUpper(seg[:1]) + seg[1:])
		}
	}
	return out.String()
}

// goType maps a schema node to its Go type. Resolution order:
//
//  1. x-aggrega-go-type names the Go type of the node it annotates, uniformly
//     — on an object node it is that type; on an `items` node the mechanical
//     array recursion lifts it to []T. A non-nil vocabulary rewrites the
//     annotation value (host spelling → target spelling); absence passes it
//     through verbatim.
//  2. A `$ref` of the form #/$defs/<Name> resolves to the Go type <Name>
//     (the def names are already PascalCase; the resolution is a verbatim
//     suffix strip). An x-aggrega-go-type annotation on the same node wins
//     over a $ref.
//  3. Otherwise the mechanical scalar/array mapping applies, and a nested
//     object becomes the named struct typeName.
//
// A nil vocabulary (the single-type entity path) leaves every annotation
// value untouched.
func goType(n schemaNode, typeName string, vocab map[string]string) string {
	if n.GoType != "" {
		return rewriteType(n.GoType, vocab)
	}
	if n.Ref != "" {
		return refTypeName(n.Ref)
	}
	switch n.Type {
	case "string":
		return "string"
	case "boolean":
		return "bool"
	case "integer":
		return "int"
	case "array":
		if n.Items != nil {
			return "[]" + goType(*n.Items, "", vocab)
		}
		return "[]any"
	case "object":
		return typeName // a named nested struct
	default:
		return "any"
	}
}

// rewriteType applies a target's type vocabulary to an x-aggrega-go-type
// value. A nil or entry-less vocabulary returns the value verbatim.
func rewriteType(goType string, vocab map[string]string) string {
	if vocab != nil {
		if mapped, ok := vocab[goType]; ok {
			return mapped
		}
	}
	return goType
}

// refTypeName resolves a #/$defs/<Name> $ref to the Go type name <Name>. The
// def names are authored in PascalCase, so the resolution is a verbatim
// suffix strip with no name munging. A $ref the generator does not recognize
// (not a #/$defs/ form) passes through verbatim so the resulting source fails
// to compile loudly rather than silently mistyping a field.
func refTypeName(ref string) string {
	const prefix = "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ref
}

// checkStructNode refuses, before anything is emitted, the two schema shapes the
// generator has no Go type name for. An object carrying neither an
// `x-aggrega-go-type` annotation nor `properties` resolves to a struct name that
// nothing then declares, because a struct is only emitted for an object that has
// properties to build one from. An object sitting on an `items` node resolves to
// nothing at all, because the mechanical array recursion has no name to give the
// element. Both otherwise emit a file — the first referencing an undeclared type,
// the second an array with an empty element type — and each surfaces downstream
// as a formatter error naming a line in generated code, which does not say which
// schema node caused it. So the refusal names the node's path in the schema and
// the annotation it needs.
//
// The walk mirrors emission, and mirroring where it STOPS is what keeps the
// refusal from rejecting schemas that generate correctly: the generator emits an
// annotated type verbatim and never descends into an annotated node, and it
// resolves a `$ref` to the def's own type without reading the node either — so a
// node under an annotation or a `$ref` is never read by the generator, and is
// never judged here.
//
// It takes a node the generator emits as a struct: the entity root on the
// single-type path, or one `$defs` entry on the multi-type path. path is that
// node's location in the schema, which each refusal extends to point at the
// offending node rather than at the type it was reached through.
func checkStructNode(schemaFile, path string, n schemaNode) error {
	for _, p := range n.props {
		if err := checkFieldNode(schemaFile, path+"/properties/"+p.name, p.node); err != nil {
			return err
		}
	}
	return nil
}

// checkFieldNode takes a node in a property position, where the generator has a
// name to give a nested struct — so an object here is emittable exactly when it
// declares the properties that struct is built from.
func checkFieldNode(schemaFile, path string, n schemaNode) error {
	if n.GoType != "" || n.Ref != "" {
		return nil
	}
	switch n.Type {
	case "array":
		if n.Items == nil {
			return nil
		}
		return checkElementNode(schemaFile, path+"/items", *n.Items)
	case "object":
		if len(n.props) == 0 {
			return fmt.Errorf("typegen: %s: the object at %s declares no properties and carries no "+
				"x-aggrega-go-type annotation, so there is no struct to build from it and the generated "+
				"file would reference a type that is never declared. Give the node an "+
				"x-aggrega-go-type annotation naming the Go type of the node it annotates, or declare "+
				"the properties the struct is built from", schemaFile, path)
		}
		return checkStructNode(schemaFile, path, n)
	}
	return nil
}

// checkElementNode takes a node in an `items` position, where the generator has
// no name to give the element — so an object here is emittable only when it is
// annotated or resolved by a `$ref`. Its own properties do not help: the array
// recursion never names a struct for them.
func checkElementNode(schemaFile, path string, n schemaNode) error {
	if n.GoType != "" || n.Ref != "" {
		return nil
	}
	switch n.Type {
	case "array":
		if n.Items == nil {
			return nil
		}
		return checkElementNode(schemaFile, path+"/items", *n.Items)
	case "object":
		return fmt.Errorf("typegen: %s: the items node at %s is an object carrying no "+
			"x-aggrega-go-type annotation, so there is no element type to emit and the generated file "+
			"would carry an array with an empty element type. Give the items node an "+
			"x-aggrega-go-type annotation naming the element type; the mechanical array recursion "+
			"lifts it to []T", schemaFile, path)
	}
	return nil
}

// collectSelectors records the package selector of any qualified type
// (<lower>.<Upper>, e.g. yaml.Node) appearing in a Go type string, so the
// import block can be emitted. Same-package types (OrderedMap[...], []T, *T)
// carry no selector and are ignored.
func collectSelectors(goType string, used map[string]bool) {
	for i := 1; i < len(goType)-1; i++ {
		if goType[i] != '.' {
			continue
		}
		j := i - 1
		for j >= 0 && (isIdentByte(goType[j])) {
			j--
		}
		sel := goType[j+1 : i]
		next := goType[i+1]
		if sel != "" && sel[0] >= 'a' && sel[0] <= 'z' && next >= 'A' && next <= 'Z' {
			used[sel] = true
		}
	}
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// emitStruct writes one struct declaration. typeName seeds nested named types
// (<parent><Field>). Returns the nested object structs it discovered, to emit
// after; records package selectors used by field types into `used`. A non-nil
// vocab rewrites x-aggrega-go-type field values per the target's vocabulary.
func emitStruct(b *strings.Builder, typeName, doc string, n schemaNode, used map[string]bool, vocab map[string]string, cbor bool) []pending {
	required := map[string]bool{}
	for _, r := range n.Required {
		required[r] = true
	}
	if doc != "" {
		b.WriteString(wrapDoc(typeName, doc))
	}
	fmt.Fprintf(b, "type %s struct {\n", typeName)
	var nested []pending
	for _, p := range n.props {
		field := pascal(p.name)
		nestedName := typeName + field
		gt := goType(p.node, nestedName, vocab)
		collectSelectors(gt, used)
		if p.node.Type == "object" && p.node.GoType == "" && len(p.node.props) > 0 {
			nested = append(nested, pending{name: nestedName, doc: p.node.Description, node: p.node})
		}
		jsonTag, yamlTag := p.name, p.name
		if !required[p.name] {
			jsonTag += ",omitempty"
			yamlTag += ",omitempty"
			// OrderedMap is a struct value: encoding/json's omitempty is a
			// no-op on it, so omitzero (Go 1.24+, via IsZero) is the
			// load-bearing option that omits an empty map rather than emitting
			// `{}`. yaml.v3 honours its IsZeroer through plain omitempty.
			if strings.HasPrefix(gt, "OrderedMap[") {
				jsonTag += ",omitzero"
			}
		}
		if p.node.Description != "" {
			fmt.Fprintf(b, "\t// %s %s\n", field, oneLine(p.node.Description))
		}
		if cbor {
			// The cbor tag mirrors the JSON wire key (with omitempty for
			// non-required fields) so a reference CBOR encoder used as a test
			// oracle keys the map by the wire key and omits the same fields the
			// generated codec omits. (omitzero has no cbor analog; guest codec
			// types carry no OrderedMap, so the omitzero case never applies here.)
			cborTag := p.name
			if !required[p.name] {
				cborTag += ",omitempty"
			}
			fmt.Fprintf(b, "\t%s %s `json:%q yaml:%q cbor:%q`\n", field, gt, jsonTag, yamlTag, cborTag)
		} else {
			fmt.Fprintf(b, "\t%s %s `json:%q yaml:%q`\n", field, gt, jsonTag, yamlTag)
		}
	}
	b.WriteString("}\n\n")
	return nested
}

type pending struct {
	name string
	doc  string
	node schemaNode
}

func wrapDoc(name, doc string) string {
	return "// " + name + " " + oneLine(doc) + "\n"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// codecField is one struct field resolved for codec emission: the Go field
// name, the JSON wire key, the resolved Go type, whether it is required
// (required fields are never omitted on marshal), and the property description
// (carried for the Rust/TS backends, which render it as a doc comment).
type codecField struct {
	field    string
	key      string
	goType   string
	required bool
	desc     string
	// schemaType is the node's declared JSON Schema `type`, kept beside the
	// resolved Go type because the two answer different questions. The Go type is
	// a name, and a name alone does not reveal the kind underneath it: a field
	// annotated with a named scalar type resolves to a spelling the omit logic
	// cannot classify, while the schema still says plainly that the value is a
	// string. Carrying the declared type lets the omit decision fall back on the
	// kind when the name is unclassifiable, instead of emitting the field
	// unconditionally and contradicting its own omitempty tag.
	schemaType string
}

// resolveCodecFields resolves a def node's properties into codec field
// descriptors, in declared order, applying the target vocabulary to
// x-aggrega-go-type values and resolving $refs to def type names. It mirrors
// emitStruct's field-type resolution so the codec types match the emitted
// struct fields exactly.
func resolveCodecFields(n schemaNode, vocab map[string]string) []codecField {
	required := map[string]bool{}
	for _, r := range n.Required {
		required[r] = true
	}
	var fields []codecField
	for _, p := range n.props {
		field := pascal(p.name)
		fields = append(fields, codecField{
			field:      field,
			key:        p.name,
			goType:     goType(p.node, field, vocab),
			required:   required[p.name],
			desc:       p.node.Description,
			schemaType: p.node.Type,
		})
	}
	return fields
}

// emitCodec writes a dispatch-free streaming JSON codec — MarshalJSON and
// UnmarshalJSON — for one def struct type. Composite fields (named structs,
// slices of named structs, maps) are walked by generated code, and each
// nested def value is decoded by a direct call to that type's own generated
// UnmarshalJSON rather than a json.Decoder.Decode dispatch — so no stdlib
// interface-dispatch through reflect remains on the decode path. Scalar leaf
// fields are decoded by hand from a single json.Token. The generated code
// references no `reflect` symbol — the dispatch-free guarantee is by
// construction.
//
// defTypes names the generated def struct types, so the emitter can tell a
// nested generated type (direct UnmarshalJSON call) from a scalar or an
// opaque passthrough.
func emitCodec(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string, defTypes map[string]bool) {
	fields := resolveCodecFields(n, vocab)
	emitMarshal(b, typeName, fields, defTypes)
	emitUnmarshal(b, typeName, fields, defTypes)
}

// emitMarshal writes T.MarshalJSON: it builds the JSON object manually in
// field-declaration order (the determinism contract), honouring omitempty for
// non-required fields by Go type so the output matches encoding/json on the
// equivalent struct tags byte-for-byte. Each nested def value is encoded by a
// direct call to that type's own MarshalJSON, and leaves are written by hand —
// no json.Marshal dispatch through reflect remains on the encode path (except
// the residual opaque-`any` fallback).
func emitMarshal(b *strings.Builder, typeName string, fields []codecField, defTypes map[string]bool) {
	fmt.Fprintf(b, "// MarshalJSON encodes %s in field-declaration order, omitting\n", typeName)
	fmt.Fprintf(b, "// empty non-required fields, dispatch-free.\n")
	fmt.Fprintf(b, "func (t %s) MarshalJSON() ([]byte, error) {\n", typeName)
	b.WriteString("\tvar buf bytes.Buffer\n")
	b.WriteString("\tbuf.WriteByte('{')\n")
	b.WriteString("\tfirst := true\n")
	for _, f := range fields {
		guard := omitGuard("t."+f.field, f.goType, f.schemaType)
		if !f.required && guard != "" {
			fmt.Fprintf(b, "\tif %s {\n", guard)
			emitMarshalField(b, f, "\t\t", defTypes)
			b.WriteString("\t}\n")
		} else {
			emitMarshalField(b, f, "\t", defTypes)
		}
	}
	b.WriteString("\tbuf.WriteByte('}')\n")
	b.WriteString("\treturn buf.Bytes(), nil\n")
	b.WriteString("}\n\n")
}

// emitMarshalField writes the separator + key + encoded value of one field
// into buf, dispatching the encode by Go type via emitEncodeValue.
func emitMarshalField(b *strings.Builder, f codecField, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%sif !first {\n%s\tbuf.WriteByte(',')\n%s}\n", indent, indent, indent)
	b.WriteString(indent + "first = false\n")
	fmt.Fprintf(b, "%sbuf.WriteString(%q)\n", indent, `"`+f.key+`":`)
	emitEncodeValue(b, "t."+f.field, f.goType, indent, defTypes)
}

// emitEncodeValue writes the dispatch-free encode of the lvalue `src` of Go
// type `goType` into buf. A nested generated def type (or a pointer to one) is
// encoded by a direct call to its own MarshalJSON — never json.Marshal, whose
// reflective buffer allocation the TinyGo bare collector corrupts. Scalars are
// written by hand; json.RawMessage is written verbatim; an opaque `any` value
// falls back to json.Marshal (the one residual, reached only for a value the
// guest authors, never for a typed field on the decode path).
func emitEncodeValue(b *strings.Builder, src, goType, indent string, defTypes map[string]bool) {
	switch {
	case goType == "string":
		fmt.Fprintf(b, "%swriteJSONString(&buf, %s)\n", indent, src)
	case goType == "int" || goType == "int64":
		fmt.Fprintf(b, "%sbuf.WriteString(strconv.FormatInt(int64(%s), 10))\n", indent, src)
	case goType == "float64":
		fmt.Fprintf(b, "%sif err := writeJSONFloat(&buf, %s); err != nil {\n%s\treturn nil, err\n%s}\n", indent, src, indent, indent)
	case goType == "bool":
		fmt.Fprintf(b, "%sif %s {\n%s\tbuf.WriteString(\"true\")\n%s} else {\n%s\tbuf.WriteString(\"false\")\n%s}\n", indent, src, indent, indent, indent, indent)
	case goType == "[]byte":
		fmt.Fprintf(b, "%swriteJSONBytes(&buf, %s)\n", indent, src)
	case goType == "json.RawMessage":
		fmt.Fprintf(b, "%sbuf.Write(%s)\n", indent, src)
	case strings.HasPrefix(goType, "*"):
		elem := goType[1:]
		if defTypes[elem] {
			fmt.Fprintf(b, "%sif enc, err := %s.MarshalJSON(); err != nil {\n%s\treturn nil, err\n%s} else {\n%s\tbuf.Write(enc)\n%s}\n", indent, src, indent, indent, indent, indent)
		} else {
			emitEncodeAnyFallback(b, src, indent)
		}
	case defTypes[goType]:
		fmt.Fprintf(b, "%sif enc, err := %s.MarshalJSON(); err != nil {\n%s\treturn nil, err\n%s} else {\n%s\tbuf.Write(enc)\n%s}\n", indent, src, indent, indent, indent, indent)
	case strings.HasPrefix(goType, "map["):
		valType := goType[strings.IndexByte(goType, ']')+1:]
		emitEncodeMap(b, src, valType, indent, defTypes)
	case strings.HasPrefix(goType, "[]"):
		elemType := goType[2:]
		emitEncodeSlice(b, src, elemType, indent, defTypes)
	default:
		emitEncodeAnyFallback(b, src, indent)
	}
}

// emitEncodeMap writes a map's JSON object encode. Map key order is sorted so
// the output matches encoding/json's sorted map-key marshal byte-for-byte (the
// determinism contract). Each value encodes through emitEncodeValue.
func emitEncodeMap(b *strings.Builder, src, valType, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%s{\n", indent)
	fmt.Fprintf(b, "%s\tkeys := make([]string, 0, len(%s))\n", indent, src)
	fmt.Fprintf(b, "%s\tfor k := range %s {\n%s\t\tkeys = append(keys, k)\n%s\t}\n", indent, src, indent, indent)
	fmt.Fprintf(b, "%s\tsort.Strings(keys)\n", indent)
	fmt.Fprintf(b, "%s\tbuf.WriteByte('{')\n", indent)
	fmt.Fprintf(b, "%s\tfor i, k := range keys {\n", indent)
	fmt.Fprintf(b, "%s\t\tif i > 0 {\n%s\t\t\tbuf.WriteByte(',')\n%s\t\t}\n", indent, indent, indent)
	fmt.Fprintf(b, "%s\t\twriteJSONString(&buf, k)\n", indent)
	fmt.Fprintf(b, "%s\t\tbuf.WriteByte(':')\n", indent)
	fmt.Fprintf(b, "%s\t\tmv := %s[k]\n", indent, src)
	emitEncodeValue(b, "mv", valType, indent+"\t\t", defTypes)
	fmt.Fprintf(b, "%s\t}\n", indent)
	fmt.Fprintf(b, "%s\tbuf.WriteByte('}')\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// emitEncodeSlice writes a slice's JSON array encode. Each element encodes
// through emitEncodeValue in order.
func emitEncodeSlice(b *strings.Builder, src, elemType, indent string, defTypes map[string]bool) {
	fmt.Fprintf(b, "%sbuf.WriteByte('[')\n", indent)
	fmt.Fprintf(b, "%sfor i := range %s {\n", indent, src)
	fmt.Fprintf(b, "%s\tif i > 0 {\n%s\t\tbuf.WriteByte(',')\n%s\t}\n", indent, indent, indent)
	fmt.Fprintf(b, "%s\tev := %s[i]\n", indent, src)
	emitEncodeValue(b, "ev", elemType, indent+"\t", defTypes)
	fmt.Fprintf(b, "%s}\n", indent)
	fmt.Fprintf(b, "%sbuf.WriteByte(']')\n", indent)
}

// emitEncodeAnyFallback encodes an opaque value (an `any` field, or a pointer
// to a non-def type) via json.Marshal. It is the one place a stdlib reflective
// encode remains; it is reached for a value the guest authors on the output
// side, not for a typed field on the decode path.
func emitEncodeAnyFallback(b *strings.Builder, src, indent string) {
	fmt.Fprintf(b, "%sif enc, err := json.Marshal(%s); err != nil {\n%s\treturn nil, err\n%s} else {\n%s\tbuf.Write(enc)\n%s}\n", indent, src, indent, indent, indent, indent)
}

// omitGuard returns a boolean expression that is true when a non-required
// field of the given Go type must be emitted (i.e. it is non-empty), matching
// encoding/json's omitempty semantics for that type. For an order-preserving
// container, whose emptiness omitempty cannot see, it matches omitzero
// semantics instead, so the field is omitted when empty rather than emitted as
// an empty map. An empty string means the type carries no omitempty notion (the
// field is always emitted).
//
// schemaType is the field's declared JSON Schema type, consulted only when the
// Go type's spelling classifies as nothing. That case is reached by a field
// annotated with a named type — a scope vocabulary declared as its own string
// type, say — whose kind is invisible in its name while the schema states it
// outright. Falling back on the declared kind is what keeps such a field's
// emission agreeing with the omitempty tag emitted beside it; without the
// fallback the field is emitted unconditionally, which silently contradicts its
// own tag and puts an empty value on the wire the reference encoder omits. The
// fallback deliberately stops at the kinds the schema names unambiguously: a
// declared object may be realized as either a map, whose emptiness omitempty
// sees, or a struct, whose emptiness it does not, so an object keeps the
// unconditional emission that is correct for the struct case, and a genuine map
// is already classified by its own spelling above.
func omitGuard(expr, goType, schemaType string) string {
	// An order-preserving container is a struct value, so encoding/json's
	// omitempty is a no-op on it: emptiness has to be asked of the type. IsZero
	// is the method omitzero dispatches to, so guarding on it omits exactly the
	// values a reader of the field's omitempty tag expects omitted. The spelling
	// is recognised through orderedMapValueType so this and the value encoder
	// agree on what an order-preserving container is. Checked before the switch
	// below, which no order-preserving container's spelling reaches; a composite
	// that merely contains one — a plain map of them, a slice of them — still
	// falls to its own case there.
	if _, ok := orderedMapValueType(goType); ok {
		return "!" + expr + ".IsZero()"
	}
	switch {
	case goType == "string":
		return expr + ` != ""`
	case goType == "int" || goType == "int64" || goType == "float64":
		return expr + " != 0"
	case goType == "bool":
		return expr + " != false"
	case goType == "[]byte" || goType == "json.RawMessage":
		return "len(" + expr + ") != 0"
	case goType == "any" || goType == "interface{}":
		// encoding/json's omitempty on an interface field omits when the
		// interface is nil (the underlying scalar emptiness does not matter).
		return expr + " != nil"
	case strings.HasPrefix(goType, "[]"):
		return "len(" + expr + ") != 0"
	case strings.HasPrefix(goType, "map["):
		return "len(" + expr + ") != 0"
	case strings.HasPrefix(goType, "*"):
		return expr + " != nil"
	}
	switch schemaType {
	case "string":
		return expr + ` != ""`
	case "integer", "number":
		return expr + " != 0"
	case "boolean":
		return expr + " != false"
	case "array":
		return "len(" + expr + ") != 0"
	default:
		return ""
	}
}

// emitUnmarshal writes T.UnmarshalJSON: a streaming json.Decoder object walk
// that dispatches each known key to a per-field decode and descends fully into
// composite fields. Each nested def value is decoded by a direct call to that
// type's own generated UnmarshalJSON — never a json.Decoder.Decode dispatch —
// so no stdlib interface-dispatch through reflect remains on the decode path.
// Unknown keys are skipped by reading and discarding their raw value. No
// reflect symbol appears.
func emitUnmarshal(b *strings.Builder, typeName string, fields []codecField, defTypes map[string]bool) {
	fmt.Fprintf(b, "// UnmarshalJSON decodes %s with a streaming token walk, descending\n", typeName)
	fmt.Fprintf(b, "// into composite fields by direct call to each nested type's generated\n")
	fmt.Fprintf(b, "// UnmarshalJSON, dispatch-free. Unknown keys are skipped.\n")
	fmt.Fprintf(b, "func (t *%s) UnmarshalJSON(data []byte) error {\n", typeName)
	b.WriteString("\tdec := json.NewDecoder(bytes.NewReader(data))\n")
	b.WriteString("\tif err := expectDelim(dec, '{'); err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\tfor dec.More() {\n")
	b.WriteString("\t\tkey, err := readKey(dec)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	b.WriteString("\t\tswitch key {\n")
	for _, f := range fields {
		fmt.Fprintf(b, "\t\tcase %q:\n", f.key)
		emitUnmarshalField(b, f, "\t\t\t", defTypes)
	}
	b.WriteString("\t\tdefault:\n")
	b.WriteString("\t\t\tif _, err := readRaw(dec, data); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif err := expectDelim(dec, '}'); err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
}

// emitUnmarshalField writes the per-field decode for one key. Maps and slices
// are token-walked element-wise; each element decodes through emitDecodeValue,
// which calls a nested def type's own generated UnmarshalJSON directly rather
// than dispatching through json.Decoder.Decode.
func emitUnmarshalField(b *strings.Builder, f codecField, indent string, defTypes map[string]bool) {
	switch {
	case strings.HasPrefix(f.goType, "map["):
		valType := f.goType[strings.IndexByte(f.goType, ']')+1:]
		fmt.Fprintf(b, "%st.%s = make(%s)\n", indent, f.field, f.goType)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, '{'); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(b, "%sfor dec.More() {\n", indent)
		fmt.Fprintf(b, "%s\tmk, err := readKey(dec)\n%s\tif err != nil {\n%s\t\treturn err\n%s\t}\n", indent, indent, indent, indent)
		fmt.Fprintf(b, "%s\tvar mv %s\n", indent, valType)
		emitDecodeValue(b, "mv", valType, indent+"\t", defTypes)
		fmt.Fprintf(b, "%s\tt.%s[mk] = mv\n", indent, f.field)
		fmt.Fprintf(b, "%s}\n", indent)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, '}'); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
	case strings.HasPrefix(f.goType, "[]") && f.goType != "[]byte":
		elemType := f.goType[2:]
		fmt.Fprintf(b, "%st.%s = nil\n", indent, f.field)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, '['); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(b, "%sfor dec.More() {\n", indent)
		fmt.Fprintf(b, "%s\tvar elem %s\n", indent, elemType)
		emitDecodeValue(b, "elem", elemType, indent+"\t", defTypes)
		fmt.Fprintf(b, "%s\tt.%s = append(t.%s, elem)\n", indent, f.field, f.field)
		fmt.Fprintf(b, "%s}\n", indent)
		fmt.Fprintf(b, "%sif err := expectDelim(dec, ']'); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
	default:
		emitDecodeValue(b, "t."+f.field, f.goType, indent, defTypes)
	}
}

// emitDecodeValue writes the dispatch-free decode of the next JSON value into
// the lvalue `dst` of Go type `goType`. A nested generated def type (or a
// pointer to one) is decoded by capturing the next value's raw bytes and
// calling that type's own UnmarshalJSON directly — never json.Decoder.Decode,
// whose interface-dispatch routes through stdlib reflection that the TinyGo
// bare collector breaks. Scalars are decoded by hand from a single token;
// json.RawMessage and `any` carry the raw bytes through.
func emitDecodeValue(b *strings.Builder, dst, goType, indent string, defTypes map[string]bool) {
	switch {
	case goType == "string":
		fmt.Fprintf(b, "%sif s, err := readString(dec); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = s\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "int" || goType == "int64":
		fmt.Fprintf(b, "%sif n, err := readInt(dec); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = %s(n)\n%s}\n", indent, indent, indent, indent, dst, goType, indent)
	case goType == "float64":
		fmt.Fprintf(b, "%sif n, err := readFloat(dec); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = n\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "bool":
		fmt.Fprintf(b, "%sif v, err := readBool(dec); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = v\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "[]byte":
		fmt.Fprintf(b, "%sif v, err := readBytes(dec); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = v\n%s}\n", indent, indent, indent, indent, dst, indent)
	case goType == "json.RawMessage":
		fmt.Fprintf(b, "%sif raw, err := readRaw(dec, data); err != nil {\n%s\treturn err\n%s} else {\n%s\t%s = raw\n%s}\n", indent, indent, indent, indent, dst, indent)
	case strings.HasPrefix(goType, "*"):
		elem := goType[1:]
		if defTypes[elem] {
			fmt.Fprintf(b, "%sif raw, err := readRaw(dec, data); err != nil {\n%s\treturn err\n%s} else {\n", indent, indent, indent)
			fmt.Fprintf(b, "%s\tv := new(%s)\n", indent, elem)
			fmt.Fprintf(b, "%s\tif err := v.UnmarshalJSON(raw); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, indent, indent)
			fmt.Fprintf(b, "%s\t%s = v\n%s}\n", indent, dst, indent)
		} else {
			emitDecodeAnyFallback(b, dst, goType, indent)
		}
	case defTypes[goType]:
		fmt.Fprintf(b, "%sif raw, err := readRaw(dec, data); err != nil {\n%s\treturn err\n%s} else {\n", indent, indent, indent)
		fmt.Fprintf(b, "%s\tif err := %s.UnmarshalJSON(raw); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, dst, indent, indent)
		fmt.Fprintf(b, "%s}\n", indent)
	default:
		// any / interface{} and any other shape with no generated codec:
		// capture the raw value and decode it with a number-preserving decoder.
		// This is the only residual stdlib decode. It is reached both for an
		// opaque `any` value the guest authors on the output side and for a
		// non-def typed field decoded on the guest input hot path — notably
		// WorkloadProjection.Workload, whose opaque `any` sub-fields must keep
		// integers as integers rather than widening them to float64.
		emitDecodeAnyFallback(b, dst, goType, indent)
	}
}

// emitDecodeAnyFallback decodes an opaque value (an `any` field, or a pointer
// to a non-def type) by capturing its raw bytes and decoding them through a
// json.Decoder with UseNumber set. UseNumber preserves nested opaque numbers as
// json.Number so an integer is not silently widened to float64 (which would
// re-serialize a large integer in exponential form and drop precision beyond
// 2^53). This path is reached on the guest input hot path for non-def typed
// fields such as WorkloadProjection.Workload, whose opaque `any` sub-fields
// (scheduling affinity, tolerations, lifecycle httpGet, volume items) must keep
// integers as integers. It is the one place a stdlib reflective decode remains.
func emitDecodeAnyFallback(b *strings.Builder, dst, goType, indent string) {
	fmt.Fprintf(b, "%sif raw, err := readRaw(dec, data); err != nil {\n%s\treturn err\n%s} else {\n", indent, indent, indent)
	fmt.Fprintf(b, "%s\tdec2 := json.NewDecoder(bytes.NewReader(raw))\n", indent)
	fmt.Fprintf(b, "%s\tdec2.UseNumber()\n", indent)
	fmt.Fprintf(b, "%s\tif err := dec2.Decode(&%s); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, dst, indent, indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// emitCodecHelpers writes the shared streaming-decode helpers the generated
// UnmarshalJSON methods call. expectDelim and readKey read a single token;
// readRaw captures the next complete JSON value's exact source bytes via a
// depth-tracking token scan (so an opaque passthrough re-marshals
// byte-identically); the readScalar helpers decode one leaf token by hand.
// None dispatches through json.Decoder.Decode, so none routes through the
// stdlib reflective decode the TinyGo bare collector breaks.
func emitCodecHelpers(b *strings.Builder) {
	b.WriteString("// expectDelim reads the next token and asserts it is the given delimiter.\n")
	b.WriteString("func expectDelim(dec *json.Decoder, want json.Delim) error {\n")
	b.WriteString("\ttok, err := dec.Token()\n")
	b.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\td, ok := tok.(json.Delim)\n")
	b.WriteString("\tif !ok || d != want {\n")
	b.WriteString("\t\treturn fmt.Errorf(\"abi: expected %q, got %v\", want, tok)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readKey reads the next token and asserts it is a string object key.\n")
	b.WriteString("func readKey(dec *json.Decoder) (string, error) {\n")
	b.WriteString("\ttok, err := dec.Token()\n")
	b.WriteString("\tif err != nil {\n\t\treturn \"\", err\n\t}\n")
	b.WriteString("\tkey, ok := tok.(string)\n")
	b.WriteString("\tif !ok {\n")
	b.WriteString("\t\treturn \"\", fmt.Errorf(\"abi: expected string key, got %v\", tok)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn key, nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readRaw captures the next complete JSON value's exact source bytes by\n")
	b.WriteString("// slicing the original input between the decoder's offsets before and after\n")
	b.WriteString("// a depth-tracking token scan of the value. The returned bytes are verbatim\n")
	b.WriteString("// (no re-serialization), so an opaque passthrough field re-marshals\n")
	b.WriteString("// byte-identically and a nested def value can be handed to its own\n")
	b.WriteString("// UnmarshalJSON. It uses only Decoder.More, Decoder.Token, and\n")
	b.WriteString("// Decoder.InputOffset — no Decoder.Decode dispatch.\n")
	b.WriteString("func readRaw(dec *json.Decoder, data []byte) (json.RawMessage, error) {\n")
	b.WriteString("\t// InputOffset before the first value token points just past the\n")
	b.WriteString("\t// preceding token (an object key, a preceding array element, or\n")
	b.WriteString("\t// nothing), so the slice may carry a leading key/value separator (:),\n")
	b.WriteString("\t// an array-element separator (,), and whitespace; the value's own bytes\n")
	b.WriteString("\t// are recovered by trimming those leading non-value bytes. The trailing\n")
	b.WriteString("\t// offset is exact (just past the value's last token).\n")
	b.WriteString("\tstart := dec.InputOffset()\n")
	b.WriteString("\tdepth := 0\n")
	b.WriteString("\tfor {\n")
	b.WriteString("\t\ttok, err := dec.Token()\n")
	b.WriteString("\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
	b.WriteString("\t\tif d, ok := tok.(json.Delim); ok {\n")
	b.WriteString("\t\t\tif d == '{' || d == '[' {\n\t\t\t\tdepth++\n\t\t\t} else {\n\t\t\t\tdepth--\n\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif depth == 0 {\n\t\t\tbreak\n\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\tend := dec.InputOffset()\n")
	b.WriteString("\tvalue := bytes.TrimLeft(data[start:end], \" \\t\\n\\r:,\")\n")
	b.WriteString("\tout := make(json.RawMessage, len(value))\n")
	b.WriteString("\tcopy(out, value)\n")
	b.WriteString("\treturn out, nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readString decodes the next token as a JSON string.\n")
	b.WriteString("func readString(dec *json.Decoder) (string, error) {\n")
	b.WriteString("\ttok, err := dec.Token()\n")
	b.WriteString("\tif err != nil {\n\t\treturn \"\", err\n\t}\n")
	b.WriteString("\ts, ok := tok.(string)\n")
	b.WriteString("\tif !ok {\n\t\treturn \"\", fmt.Errorf(\"abi: expected string, got %v\", tok)\n\t}\n")
	b.WriteString("\treturn s, nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readFloat decodes the next token as a JSON number.\n")
	b.WriteString("func readFloat(dec *json.Decoder) (float64, error) {\n")
	b.WriteString("\ttok, err := dec.Token()\n")
	b.WriteString("\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	b.WriteString("\tf, ok := tok.(float64)\n")
	b.WriteString("\tif !ok {\n\t\treturn 0, fmt.Errorf(\"abi: expected number, got %v\", tok)\n\t}\n")
	b.WriteString("\treturn f, nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readInt decodes the next token as a JSON number and truncates to int.\n")
	b.WriteString("func readInt(dec *json.Decoder) (int, error) {\n")
	b.WriteString("\tf, err := readFloat(dec)\n")
	b.WriteString("\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	b.WriteString("\treturn int(f), nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readBool decodes the next token as a JSON bool.\n")
	b.WriteString("func readBool(dec *json.Decoder) (bool, error) {\n")
	b.WriteString("\ttok, err := dec.Token()\n")
	b.WriteString("\tif err != nil {\n\t\treturn false, err\n\t}\n")
	b.WriteString("\tv, ok := tok.(bool)\n")
	b.WriteString("\tif !ok {\n\t\treturn false, fmt.Errorf(\"abi: expected bool, got %v\", tok)\n\t}\n")
	b.WriteString("\treturn v, nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// readBytes decodes the next token as a base64 JSON string into bytes,\n")
	b.WriteString("// matching encoding/json's []byte wire form.\n")
	b.WriteString("func readBytes(dec *json.Decoder) ([]byte, error) {\n")
	b.WriteString("\ts, err := readString(dec)\n")
	b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	b.WriteString("\treturn base64.StdEncoding.DecodeString(s)\n")
	b.WriteString("}\n\n")

	b.WriteString("// hexDigits is the lowercase hex alphabet for \\u escapes.\n")
	b.WriteString("const hexDigits = \"0123456789abcdef\"\n\n")

	b.WriteString("// writeJSONString writes s as a JSON string literal, matching\n")
	b.WriteString("// encoding/json's default (HTML-safe) string escaping byte-for-byte:\n")
	b.WriteString("// it escapes the quote, backslash, control characters, the HTML-sensitive\n")
	b.WriteString("// bytes <, >, and &, and the line/paragraph separators U+2028/U+2029.\n")
	b.WriteString("func writeJSONString(buf *bytes.Buffer, s string) {\n")
	b.WriteString("\tbuf.WriteByte('\"')\n")
	b.WriteString("\tstart := 0\n")
	b.WriteString("\tfor i := 0; i < len(s); {\n")
	b.WriteString("\t\tif c := s[i]; c < utf8.RuneSelf {\n")
	b.WriteString("\t\t\tif c >= ' ' && c != '\"' && c != '\\\\' && c != '<' && c != '>' && c != '&' {\n")
	b.WriteString("\t\t\t\ti++\n\t\t\t\tcontinue\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tif start < i {\n\t\t\t\tbuf.WriteString(s[start:i])\n\t\t\t}\n")
	b.WriteString("\t\t\tswitch c {\n")
	b.WriteString("\t\t\tcase '\"':\n\t\t\t\tbuf.WriteString(`\\\"`)\n")
	b.WriteString("\t\t\tcase '\\\\':\n\t\t\t\tbuf.WriteString(`\\\\`)\n")
	b.WriteString("\t\t\tcase '\\n':\n\t\t\t\tbuf.WriteString(`\\n`)\n")
	b.WriteString("\t\t\tcase '\\r':\n\t\t\t\tbuf.WriteString(`\\r`)\n")
	b.WriteString("\t\t\tcase '\\t':\n\t\t\t\tbuf.WriteString(`\\t`)\n")
	b.WriteString("\t\t\tdefault:\n")
	b.WriteString("\t\t\t\tbuf.WriteString(`\\u00`)\n")
	b.WriteString("\t\t\t\tbuf.WriteByte(hexDigits[c>>4])\n")
	b.WriteString("\t\t\t\tbuf.WriteByte(hexDigits[c&0xF])\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\ti++\n\t\t\tstart = i\n\t\t\tcontinue\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tr, size := utf8.DecodeRuneInString(s[i:])\n")
	b.WriteString("\t\tif r == '\\u2028' || r == '\\u2029' {\n")
	b.WriteString("\t\t\tif start < i {\n\t\t\t\tbuf.WriteString(s[start:i])\n\t\t\t}\n")
	b.WriteString("\t\t\tbuf.WriteString(`\\u202`)\n")
	b.WriteString("\t\t\tbuf.WriteByte(hexDigits[r&0xF])\n")
	b.WriteString("\t\t\ti += size\n\t\t\tstart = i\n\t\t\tcontinue\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\ti += size\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif start < len(s) {\n\t\tbuf.WriteString(s[start:])\n\t}\n")
	b.WriteString("\tbuf.WriteByte('\"')\n")
	b.WriteString("}\n\n")

	b.WriteString("// writeJSONFloat writes f as a JSON number, matching encoding/json's\n")
	b.WriteString("// float formatting (the shortest round-trippable form, with the small\n")
	b.WriteString("// exponent-notation cutover encoding/json uses).\n")
	b.WriteString("func writeJSONFloat(buf *bytes.Buffer, f float64) error {\n")
	b.WriteString("\tif f != f || f-f != 0 {\n")
	b.WriteString("\t\treturn fmt.Errorf(\"abi: unsupported float value %v\", f)\n")
	b.WriteString("\t}\n")
	b.WriteString("\tabs := f\n\tif abs < 0 {\n\t\tabs = -abs\n\t}\n")
	b.WriteString("\tfmt := byte('f')\n")
	b.WriteString("\tif abs != 0 && (abs < 1e-6 || abs >= 1e21) {\n\t\tfmt = 'e'\n\t}\n")
	b.WriteString("\tout := strconv.AppendFloat(nil, f, fmt, -1, 64)\n")
	b.WriteString("\tif fmt == 'e' {\n")
	b.WriteString("\t\tn := len(out)\n")
	b.WriteString("\t\tif n >= 4 && out[n-4] == 'e' && out[n-3] == '-' && out[n-2] == '0' {\n")
	b.WriteString("\t\t\tout[n-2] = out[n-1]\n\t\t\tout = out[:n-1]\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\tbuf.Write(out)\n")
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")

	b.WriteString("// writeJSONBytes writes b as a base64 JSON string, matching\n")
	b.WriteString("// encoding/json's []byte wire form.\n")
	b.WriteString("func writeJSONBytes(buf *bytes.Buffer, b []byte) {\n")
	b.WriteString("\tbuf.WriteByte('\"')\n")
	b.WriteString("\tbuf.WriteString(base64.StdEncoding.EncodeToString(b))\n")
	b.WriteString("\tbuf.WriteByte('\"')\n")
	b.WriteString("}\n\n")
}

// entitySchemaFile names the published schema an entity kind is generated from.
// The generated header line and the refusals below both name it, so it is
// derived in one place rather than spelled out at each.
func entitySchemaFile(typeName string) string {
	return strings.ToLower(typeName) + "-v1alpha1.schema.json"
}

// generate produces the formatted Go source for one entity kind: its struct
// field declarations plus the import block its field types require. It is the
// single-type emission path (one top-level struct + nested named structs),
// shared by main and the regen-drift test. The single-type path uses no
// vocabulary — entity x-aggrega-go-type values emit verbatim.
func generate(typeName, schemaJSON string) ([]byte, error) {
	root, err := decodeNode(json.RawMessage(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	if err := checkStructNode(entitySchemaFile(typeName), "#", root); err != nil {
		return nil, err
	}

	var body strings.Builder
	used := map[string]bool{}
	// Top-level entity struct, then its nested named structs breadth-first in
	// declared order.
	queue := []pending{{name: typeName, doc: root.Description, node: root}}
	emitQueue(&body, queue, used, nil, false)

	head := genHeader("package types",
		fmt.Sprintf("// aggrega-schemas/json/%s. DO NOT EDIT.\n", entitySchemaFile(typeName)),
		fmt.Sprintf("// Hand-written content (shared types, methods, marshalers) lives in\n// %s.go and common.go; regenerate with `go generate ./pkg/types/...`.\n", strings.ToLower(typeName)),
		used)

	return formatSource(head, body.String())
}

// namedEnum is one field's resolved string enum: the schema field name and its
// declared-order enum values.
type namedEnum struct {
	field  string
	values []string
	// fromConst records that the schema pinned this field with `const` rather
	// than listing an `enum`, which is what decides how the identifier is named.
	fromConst bool
}

// closedValues returns a node's closed string vocabulary in declared order — its
// `enum` members, or the single member a string `const` declares — and whether
// the declaration was a `const`. A `const` is a
// one-member vocabulary — the schema fixes the field to exactly that value — so
// both keywords resolve to the same shape and every enum backend emits from a
// values slice without caring which keyword declared it. `enum` wins if a node
// somehow carries both, keeping the richer declaration.
func closedValues(n schemaNode) ([]string, bool) {
	if len(n.Enum) > 0 {
		return n.Enum, false
	}
	if n.Const != "" {
		return []string{n.Const}, true
	}
	return nil, false
}

// constIdent names the emitted identifier for one member of a closed vocabulary,
// joining the field and value parts with sep (empty for the Pascal-cased Go and
// TypeScript idioms, "_" for Rust's SCREAMING_SNAKE_CASE).
//
// An `enum` member is named for the field and the value together, because the
// field alone cannot tell the members apart. A `const` member is named for the
// field alone: the schema pins that field to exactly one value, so folding the
// value into the identifier would rename the symbol every time the value is
// revised, and each consumer would have to be re-pointed at a new name for the
// same concept. Naming it for the field keeps a revision a one-line schema edit
// that every generated binding and every consumer follows unchanged.
func constIdent(field, value, sep string, fromConst bool) string {
	if fromConst {
		return field
	}
	return field + sep + value
}

// collectEnums walks a schema node depth-first, in declared order, collecting
// every closed string vocabulary — an `enum` or a single-valued `const` — whose
// owning field name is in want. It recurses through
// `properties`, `items` and `$defs`, and does NOT stop at an x-aggrega-go-type
// annotation
// — the enum-bearing fields (e.g. exports.items.operates/scope) live under
// items nodes the struct path skips, so the enum walk must descend through them.
// A field name appears at most once: the first match in declared order wins, so
// a duplicated name across branches is collected once deterministically.
func collectEnums(n schemaNode, want map[string]bool, seen map[string]bool, out *[]namedEnum) {
	for _, p := range n.props {
		if vals, fromConst := closedValues(p.node); len(vals) > 0 && want[p.name] && !seen[p.name] {
			seen[p.name] = true
			*out = append(*out, namedEnum{field: p.name, values: vals, fromConst: fromConst})
		}
		collectEnums(p.node, want, seen, out)
	}
	if n.Items != nil {
		collectEnums(*n.Items, want, seen, out)
	}
	// A schema that declares its types under $defs and carries no root
	// properties (the describe socket is one) keeps every vocabulary out of reach
	// of the walk above, so the walk descends into $defs as well. It runs last so
	// a field reachable through properties resolves to the node it already
	// resolved to — the first match in declared order still wins.
	for _, d := range n.defs {
		collectEnums(d.node, want, seen, out)
	}
}

// generateEnums produces the formatted Go source for a string-enum-const file:
// one bare untyped string const per value of each requested field, named
// Pascal(field)+Pascal(value) for an `enum` and Pascal(field) alone for a
// `const`, in (field-declaration, then enum-array) order so
// regeneration is byte-stable. It is the single-type enum-emit path. Bare
// untyped consts (not a typed enum) keep the consts drop-in for a string-typed
// consumer (a string switch scrutinee, a string struct field) without forcing a
// type conversion at the comparison site.
func generateEnums(e enumSource) ([]byte, error) {
	enums, err := resolveEnums(e)
	if err != nil {
		return nil, err
	}

	var body strings.Builder
	for _, en := range enums {
		fieldPascal := pascal(en.field)
		fmt.Fprintf(&body, "const (\n")
		for _, v := range en.values {
			fmt.Fprintf(&body, "\t%s = %q\n", constIdent(fieldPascal, pascal(v), "", en.fromConst), v)
		}
		body.WriteString(")\n\n")
	}

	head := genHeader("package "+e.pkg,
		fmt.Sprintf("// aggrega-schemas/json/%s. DO NOT EDIT.\n", e.schemaFile),
		e.regen,
		map[string]bool{})

	return formatSource(head, enumDocComment(e.doc)+body.String())
}

// resolveEnums decodes an enum source's schema and resolves every requested
// field to its closed string vocabulary — an `enum` or a single-valued `const` —
// failing loudly if a requested field is missing (a
// schema rename or a dropped enum would otherwise silently vanish the consts).
// It is the shared front-end for the Go, Rust, and TS enum backends, so the
// three languages emit from one resolved vocabulary.
func resolveEnums(e enumSource) ([]namedEnum, error) {
	root, err := decodeNode(json.RawMessage(e.schema))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	want := map[string]bool{}
	for _, f := range e.fields {
		want[f] = true
	}
	var enums []namedEnum
	collectEnums(root, want, map[string]bool{}, &enums)

	for _, f := range e.fields {
		found := false
		for _, en := range enums {
			if en.field == f {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("enum field %q not found in schema %s", f, e.schemaFile)
		}
	}
	return enums, nil
}

// enumDocComment renders a file-level doc block as `//` comment lines, blank
// source lines becoming bare `//`, with a trailing blank line. An empty doc
// yields no output. Shared by the Go/Rust/TS enum backends so the file-level
// explanation is identical across languages.
func enumDocComment(doc string) string {
	if doc == "" {
		return ""
	}
	var out string
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			out += "//\n"
		} else {
			out += "// " + line + "\n"
		}
	}
	return out + "\n"
}

// screamingSnake upper-cases a lowercase-snake token (the wire values and the
// field name are already snake) for the Rust SCREAMING_SNAKE_CASE const idiom.
func screamingSnake(s string) string {
	return strings.ToUpper(s)
}

// generateEnumsRust emits the Rust &str consts for an enum source: one
// `pub const <FIELD>_<VALUE>: &str = "<value>";` per value — `<FIELD>` alone for
// a `const` — in schema
// declaration order so regeneration is byte-stable. The consts are bare &str so
// they drop into a string comparison without a type conversion, matching the Go
// bare-const contract.
func generateEnumsRust(e enumSource) ([]byte, error) {
	enums, err := resolveEnums(e)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio S.r.l.\n\n")
	fmt.Fprintf(&b, "// Code generated by internal/tools/typegen from\n// aggrega-schemas/json/%s. DO NOT EDIT.\n", e.schemaFile)
	b.WriteString(e.regen)
	b.WriteString("\n")
	b.WriteString(enumDocComment(e.doc))
	for _, en := range enums {
		fieldScream := screamingSnake(en.field)
		for _, v := range en.values {
			fmt.Fprintf(&b, "pub const %s: &str = %q;\n",
				constIdent(fieldScream, screamingSnake(v), "_", en.fromConst), v)
		}
	}
	return []byte(b.String()), nil
}

// generateEnumsTS emits the TypeScript string consts for an enum source: one
// `export const <Field><Value> = "<value>";` per value — `<Field>` alone for a
// `const` — in schema declaration
// order so regeneration is byte-stable. The const identifiers mirror the Go
// PascalCase names so the PDK re-export is symmetric across languages.
func generateEnumsTS(e enumSource) ([]byte, error) {
	enums, err := resolveEnums(e)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio S.r.l.\n\n")
	fmt.Fprintf(&b, "// Code generated by internal/tools/typegen from\n// aggrega-schemas/json/%s. DO NOT EDIT.\n", e.schemaFile)
	b.WriteString(e.regen)
	b.WriteString("\n")
	b.WriteString(enumDocComment(e.doc))
	for _, en := range enums {
		fieldPascal := pascal(en.field)
		for _, v := range en.values {
			fmt.Fprintf(&b, "export const %s = %q;\n",
				constIdent(fieldPascal, pascal(v), "", en.fromConst), v)
		}
	}
	return []byte(b.String()), nil
}

// generateDefs dispatches a multi-type codec target to its language backend.
// The front-end (schema decode, `$defs` walk, vocabulary rewrite, field
// resolution) is shared; only the emission half forks per language. An empty
// lang defaults to the Go backend so existing Go targets need no lang field.
func generateDefs(t codecTarget) ([]byte, error) {
	// Every backend below emits from the same decoded schema, so a shape the
	// generator cannot name a type for is refused once here rather than in each:
	// the Go, Rust and TypeScript files a schema owes are refused together, so no
	// language is left holding a file the others refused to emit. The CBOR-helper
	// targets are generated from no schema and carry nothing to check.
	if t.schema != "" {
		root, err := decodeNode(json.RawMessage(t.schema))
		if err != nil {
			return nil, fmt.Errorf("decode schema: %w", err)
		}
		// The multi-type walk reads `$defs` and never the root's oneOf/type, so
		// the refusal starts at each def for the same reason emission does.
		for _, d := range root.defs {
			if err := checkStructNode(t.schemaFile, "#/$defs/"+d.name, d.node); err != nil {
				return nil, err
			}
		}
	}
	switch t.lang {
	case "", "go":
		return generateDefsGo(t)
	case "rust":
		return generateDefsRust(t)
	case "ts":
		return generateDefsTS(t)
	case "rust-cbor-helpers":
		return generateRustCBORHelpers(t)
	case "ts-cbor-helpers":
		return generateTSCBORHelpers(t)
	default:
		return nil, fmt.Errorf("typegen: unknown codec target language %q", t.lang)
	}
}

// generateDefsGo produces the formatted Go source for a multi-type codec
// package: one Go type per `$defs` entry (in declared order) plus, per type, a
// reflect-free streaming JSON codec. The top-level `oneOf` wire-discriminator
// is not a Go type and is ignored — the walk reads `$defs` only and never the
// root's oneOf/type. The target's vocabulary rewrites x-aggrega-go-type values.
func generateDefsGo(t codecTarget) ([]byte, error) {
	root, err := decodeNode(json.RawMessage(t.schema))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	if len(root.defs) == 0 {
		return nil, fmt.Errorf("schema %s has no $defs to emit", t.schemaFile)
	}

	var body strings.Builder
	used := map[string]bool{}
	// One named type per $def, in schema declaration order. Each def is an
	// object; emitStruct queues any anonymous nested objects breadth-first,
	// though the $defs-shaped schema names every composite via a $ref so the
	// nested-object queue stays empty in practice.
	var queue []pending
	for _, d := range root.defs {
		queue = append(queue, pending{name: d.name, doc: d.node.Description, node: d.node})
	}
	emitQueue(&body, queue, used, t.vocabulary, t.emitsCBOR())

	// defTypes names the generated def struct types, so the codec emitter can
	// tell a nested generated type — decoded by a direct call to its own
	// UnmarshalJSON — from a scalar or an opaque passthrough. It also includes
	// externalDefTypes: types emitted by another codec target into the same
	// package, so a cross-codec in-package field (the policy graph's IRGraph)
	// dispatches directly to its generated codec rather than through json.Marshal.
	defTypes := map[string]bool{}
	for _, d := range root.defs {
		defTypes[d.name] = true
	}
	for _, ext := range t.externalDefTypes {
		defTypes[ext] = true
	}

	// The dispatch-free streaming codec for each emitted def type, in the same
	// declared order. Codec emission descends into each field's composite shape
	// (map/struct/slice) and decodes each nested def value by a direct call to
	// its own UnmarshalJSON, so no stdlib reflective struct-walk or
	// interface-dispatch remains on the decode path.
	for _, d := range root.defs {
		emitCodec(&body, d.name, d.node, t.vocabulary, defTypes)
	}
	// The canonical-CBOR codec, parallel to the JSON codec above, for a guest
	// target. Emitted per def type in the same declared order; the shared CBOR
	// helpers land in the same helpers-bearing file the JSON helpers do.
	if t.emitsCBOR() {
		for _, d := range root.defs {
			emitCBORCodec(&body, d.name, d.node, t.vocabulary, defTypes)
		}
	}
	if t.helpers {
		emitCodecHelpers(&body)
	}
	if t.helpers && t.emitsCBOR() {
		emitCBORHelpers(&body)
	}

	// The codec spine's imports are derived from the emitted body so a file that
	// omits the shared helpers (one per-package file owns them; the rest
	// reference them) does not carry an unused import. A helpers-bearing file
	// references all of bytes, encoding/base64, encoding/json, fmt, sort,
	// strconv, and unicode/utf8; a helpers-free codec file references the subset
	// its own Marshal/Unmarshal touch.
	head := genHeaderImports("package "+t.pkg,
		fmt.Sprintf("// aggrega-schemas/json/%s. DO NOT EDIT.\n", t.schemaFile),
		"// Regenerate with `go generate ./internal/tools/typegen` from the\n// aggrega-cli checkout.\n",
		codecSpineImports(body.String()))

	return formatSource(head, body.String())
}

// codecSpineImports returns the standard-library import paths the generated
// codec body references, derived by scanning for each package's selector token.
// Deriving from the body (rather than a fixed list) keeps a helpers-free codec
// file free of unused imports while leaving a helpers-bearing file's import set
// unchanged. The returned slice is in a stable order; genHeaderImports sorts it.
func codecSpineImports(body string) []string {
	candidates := []struct {
		token string
		path  string
	}{
		{"bytes.", "bytes"},
		{"base64.", "encoding/base64"},
		{"json.", "encoding/json"},
		{"fmt.", "fmt"},
		{"math.", "math"},
		{"sort.", "sort"},
		{"strconv.", "strconv"},
		{"utf8.", "unicode/utf8"},
	}
	var imports []string
	for _, c := range candidates {
		if strings.Contains(body, c.token) {
			imports = append(imports, c.path)
		}
	}
	// A codec field typed as a cross-package type (a host-vocabulary spelling like
	// types.OrderedMap[...]) carries a package selector; emit its import via the
	// knownImports table, the same mechanism the single-type entity path uses.
	for sel, path := range knownImports {
		if strings.Contains(body, sel+".") {
			imports = append(imports, path)
		}
	}
	return imports
}

// emitQueue drains a breadth-first struct-emission queue into body, emitting
// each queued struct and the nested anonymous structs it discovers, in order.
func emitQueue(body *strings.Builder, queue []pending, used map[string]bool, vocab map[string]string, cbor bool) {
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nested := emitStruct(body, cur.name, cur.doc, cur.node, used, vocab, cbor)
		queue = append(queue, nested...)
	}
}

// genHeader builds the SPDX + code-generated header, the package clause, and
// the import block the used selectors require (mapped via knownImports).
func genHeader(packageClause, schemaLine, regenLines string, used map[string]bool) string {
	var imports []string
	for sel := range used {
		if path, ok := knownImports[sel]; ok {
			imports = append(imports, path)
		}
	}
	return genHeaderImports(packageClause, schemaLine, regenLines, imports)
}

// genHeaderImports builds the SPDX + code-generated header, the package
// clause, and an import block from an explicit import-path list (sorted,
// deduplicated by sort). Used by the codec path, whose imports are the fixed
// codec-spine packages rather than selector-derived.
func genHeaderImports(packageClause, schemaLine, regenLines string, imports []string) string {
	var head strings.Builder
	head.WriteString("// SPDX-License-Identifier: Apache-2.0\n// Copyright 2026 Cognitio S.r.l.\n\n")
	head.WriteString("// Code generated by internal/tools/typegen from\n")
	head.WriteString(schemaLine)
	head.WriteString(regenLines)
	head.WriteString("\n")
	head.WriteString(packageClause + "\n\n")

	sort.Strings(imports)
	switch {
	case len(imports) == 1:
		fmt.Fprintf(&head, "import %s\n\n", importSpec(imports[0]))
	case len(imports) > 1:
		head.WriteString("import (\n")
		for _, imp := range imports {
			fmt.Fprintf(&head, "\t%s\n", importSpec(imp))
		}
		head.WriteString(")\n\n")
	}
	return head.String()
}

// importAliases maps an import path to the explicit alias the emitted body
// references it by, for the rare package whose Go package name is not the
// selector the vocabulary spells it with. The featurerecipe_pipeline package is
// referenced as frp in the body (matching the alias the renderer package uses
// throughout), so its import needs an explicit alias. A path absent here is
// emitted bare — its package name matches the selector already (types, renderctx,
// and yaml, whose package name is yaml despite the /v3 path suffix).
var importAliases = map[string]string{
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline": "frp",
}

// importSpec renders one import line, adding an explicit alias for a path in
// importAliases so the qualified type spelling in the body resolves; every other
// path is emitted bare.
func importSpec(path string) string {
	if alias, ok := importAliases[path]; ok {
		return fmt.Sprintf("%s %q", alias, path)
	}
	return fmt.Sprintf("%q", path)
}

// formatSource gofmts the concatenated header + body. On a gofmt failure it
// returns the unformatted source for debugging alongside the error.
func formatSource(head, body string) ([]byte, error) {
	src, ferr := format.Source([]byte(head + body))
	if ferr != nil {
		return []byte(head + body), fmt.Errorf("gofmt: %w", ferr)
	}
	return src, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: typegen <output-file>")
		os.Exit(2)
	}
	outPath := os.Args[1]
	base := filepath.Base(outPath)

	out, err := generateFor(base)
	if err != nil {
		// On a gofmt failure `out` holds the unformatted source for debugging.
		if len(out) > 0 {
			_ = os.WriteFile(outPath, out, 0o644)
		}
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
}

// generateFor dispatches an output basename to its emission path: a registered
// entity kind (single-type) or a registered codec target (multi-type). It is
// the shared dispatch main and the regen-drift test both use.
func generateFor(base string) ([]byte, error) {
	if src, ok := kindSources[base]; ok {
		return generate(src.typeName, src.schema)
	}
	if e, ok := enumSources[base]; ok {
		switch e.lang {
		case "", "go":
			return generateEnums(e)
		case "rust":
			return generateEnumsRust(e)
		case "ts":
			return generateEnumsTS(e)
		default:
			return nil, fmt.Errorf("typegen: unknown enum lang %q for %q", e.lang, base)
		}
	}
	if t, ok := codecTargets[base]; ok {
		return generateDefs(t)
	}
	return nil, fmt.Errorf("typegen: no kind, enum, or codec target registered for %q", base)
}
