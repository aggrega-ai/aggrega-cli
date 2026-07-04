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
// OrderedMap[SecretSlot], yaml.Node, *Interoperability); on an `items` node it
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
	"feature_gen.go":        {"Feature", schemas.FeatureV1alpha1},
	"manifest_gen.go":       {"Manifest", schemas.ManifestV1alpha1},
	"recipe_gen.go":         {"Recipe", schemas.RecipeV1alpha1},
	"featurerecipe_gen.go":  {"FeatureRecipe", schemas.FeatureRecipeV1alpha1},
	"schemalibrary_gen.go":  {"SchemaLibrary", schemas.SchemaLibraryV1alpha1},
	"mappinglibrary_gen.go": {"MappingLibrary", schemas.MappingLibraryV1alpha1},
	"aggregate_gen.go":      {"Aggregate", schemas.AggregateV1alpha1},
	"release_gen.go":        {"Release", schemas.ReleaseV1alpha1},
	"component_gen.go":      {"Component", schemas.ComponentV1alpha1},
	"plugin_gen.go":         {"Plugin", schemas.PluginV1alpha1},
}

// enumSource binds an output filename to a string-enum-const emission: the
// embedded schema, the destination package, the schema file basename for the
// generated header, and the explicit set of enum-bearing field names to emit.
//
// The emitter is a general string-enum → Go bare-const capability: for each
// named field it locates the field's string `enum` anywhere in the schema
// (recursing through `properties`, `items`, and `x-aggrega-go-type`-annotated
// nodes alike) and emits one bare untyped string const per value, named
// Pascal(field)+Pascal(value). `fields` scopes the emission to the enums this
// file owns rather than every enum in the schema — the schema also carries
// enums (pluginType, abiVersion) whose Go identity lives in hand-written
// homes; listing only the owned fields keeps a single home per enum.
type enumSource struct {
	schema     string
	schemaFile string
	pkg        string
	fields     []string
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
		doc: "These consts are the single source for the Plugin entity's per-export\n" +
			"operates and scope vocabularies. The describe() host descriptor re-exports\n" +
			"them so the load-time shape gate and the runtime read one set of values.\n" +
			"\n" +
			"The feature-recipe-pipeline wire scope vocabulary (the runtime document-set\n" +
			"scoping and patch-op scope) is a separate surface — a different package and a\n" +
			"different schema — and is not generated here. Its own/host values must agree\n" +
			"with these by hand for now; unifying the two is a tracked follow-up.",
	},
}

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
	"renderctx.Diagnostic":    "json.RawMessage",
	"OrderedMap[Unit]":        "map[string]Unit",
	"OrderedMap[any]":         "map[string]json.RawMessage",
	"DocumentScope":           "string",
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
	"Policies": "json.RawMessage",
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

// providerGuestVocabulary maps the provider render schema's host type spellings
// to the neutral, cycle-free guest vocabulary: an opaque cross-package host type
// (the render-time context, the per-render boundary context, the shared
// diagnostic) becomes json.RawMessage (verbatim passthrough preserves its
// bytes), and every order-preserving host container becomes a plain map (the
// guest reads a map; key order on the wire is the emitting host's concern). The
// ordered structured content of an output file is the same order-preserving-any
// container as a node's values, so both surface to the guest as
// map[string]json.RawMessage — the guest reads and echoes the content by key,
// and the host runs the single serializer that turns it into bytes.
var providerGuestVocabulary = map[string]string{
	"renderctx.RenderContext":             "json.RawMessage",
	"renderctx.BoundaryContext":           "json.RawMessage",
	"renderctx.Diagnostic":                "json.RawMessage",
	"OrderedMap[any]":                     "map[string]json.RawMessage",
	"OrderedMap[[]string]":                "map[string][]string",
	"types.OrderedMap[string]":            "map[string]string",
	"OrderedMap[types.IRFeatureEntry]":    "map[string]json.RawMessage",
	"OrderedMap[types.Component]":         "map[string]json.RawMessage",
	"OrderedMap[types.Feature]":           "map[string]json.RawMessage",
	"OrderedMap[types.Recipe]":            "map[string]json.RawMessage",
	"OrderedMap[types.FeatureRecipe]":     "map[string]json.RawMessage",
	"OrderedMap[types.MappingLibrary]":    "map[string]json.RawMessage",
	"types.Workload":                      "json.RawMessage",
	"map[string]types.OrderedMap[string]": "map[string]map[string]string",
}

// providerHostVocabulary maps the provider render schema's container annotations
// to the host realization: the order-preserving types.OrderedMap (qualified,
// since the host codec lands in package renderer) so the host wire is
// byte-deterministic across runs of identical input (the determinism contract).
// The ordered structured content of an output file is the recursively
// order-preserving types.OrderedMap[any], so the guest's key order is carried to
// the host serializer intact rather than collapsed by an unordered map. The
// cross-package opaque host types (the render context, the boundary context, the
// shared diagnostic) carry their real host spelling verbatim (no entry needed);
// the host marshals them through their own order-preserving codecs.
var providerHostVocabulary = map[string]string{
	"OrderedMap[any]":                  "types.OrderedMap[any]",
	"OrderedMap[[]string]":             "types.OrderedMap[[]string]",
	"OrderedMap[types.IRFeatureEntry]": "types.OrderedMap[types.IRFeatureEntry]",
	"OrderedMap[types.Component]":      "types.OrderedMap[types.Component]",
	"OrderedMap[types.Feature]":        "types.OrderedMap[types.Feature]",
	"OrderedMap[types.Recipe]":         "types.OrderedMap[types.Recipe]",
	"OrderedMap[types.FeatureRecipe]":  "types.OrderedMap[types.FeatureRecipe]",
	"OrderedMap[types.MappingLibrary]": "types.OrderedMap[types.MappingLibrary]",
}

var codecTargets = map[string]codecTarget{
	"frp_gen.go": {
		schema:     schemas.WasmFrpStepV1alpha1,
		schemaFile: "wasm-frp-step-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: frpGuestNeutralVocabulary,
		helpers:    true, // FRP owns the shared streaming-decode helpers in package abi
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

	// Linter socket — guest codecs into aggrega-abi (Go/Rust/TS) and the host
	// codec into aggrega-cli's validator package (the shipped `aggrega lint`
	// publishing-linter dispatch). The host realizes policies as an opaque
	// json.RawMessage it assembles dynamically; the guest realizes the typed
	// Policies struct.
	"linter_gen.go": {
		schema:     schemas.WasmLinterV1alpha1,
		schemaFile: "wasm-linter-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: linterGuestVocabulary,
	},
	"linter_gen.rs": {
		schema:     schemas.WasmLinterV1alpha1,
		schemaFile: "wasm-linter-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: linterGuestVocabulary,
		lang:       "rust",
	},
	"linter_gen.ts": {
		schema:     schemas.WasmLinterV1alpha1,
		schemaFile: "wasm-linter-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: linterGuestVocabulary,
		lang:       "ts",
	},
	"linter_host_gen.go": {
		schema:     schemas.WasmLinterV1alpha1,
		schemaFile: "wasm-linter-v1alpha1.schema.json",
		pkg:        "validator",
		vocabulary: linterHostVocabulary,
		helpers:    true, // the host linter codec is alone in package validator
	},

	// IR graph — guest codec into aggrega-abi (Go/Rust/TS). The full
	// target-agnostic IR graph the Topology Builder emits; the policy socket's
	// graph references it. inject/values/infra carry opaque (the schema annotates
	// them json.RawMessage); the recursive provenance pointer and the secrets
	// self-map break their cycles by pointer/map.
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

	// Policy-validator socket — guest codec into aggrega-abi (Go/Rust/TS). The
	// graph field references the IRGraph emitted by the IR codec into the same abi
	// package; externalDefTypes makes the Go codec dispatch directly to IRGraph's
	// generated codec rather than through a reflective json.Marshal.
	"policy_gen.go": {
		schema:           schemas.WasmPolicyValidatorV1alpha1,
		schemaFile:       "wasm-policy-validator-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       policyGuestVocabulary,
		externalDefTypes: []string{"IRGraph"},
	},
	"policy_gen.rs": {
		schema:           schemas.WasmPolicyValidatorV1alpha1,
		schemaFile:       "wasm-policy-validator-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       policyGuestVocabulary,
		lang:             "rust",
		externalDefTypes: []string{"IRGraph"},
	},
	"policy_gen.ts": {
		schema:           schemas.WasmPolicyValidatorV1alpha1,
		schemaFile:       "wasm-policy-validator-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       policyGuestVocabulary,
		lang:             "ts",
		externalDefTypes: []string{"IRGraph"},
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
	// The output basenames carry the `providerrender_` stem rather than
	// `provider_`: the `provider_gen.go` basename is already the Provider entity's
	// single-type target in pkg/types, and the generator dispatches by basename, so
	// a shared basename would resolve to the entity kind, not this codec target.
	"providerrender_gen.go": {
		schema:     schemas.WasmProviderV1alpha1,
		schemaFile: "wasm-provider-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: providerGuestVocabulary,
	},
	"providerrender_gen.rs": {
		schema:     schemas.WasmProviderV1alpha1,
		schemaFile: "wasm-provider-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: providerGuestVocabulary,
		lang:       "rust",
	},
	"providerrender_gen.ts": {
		schema:     schemas.WasmProviderV1alpha1,
		schemaFile: "wasm-provider-v1alpha1.schema.json",
		pkg:        "abi",
		vocabulary: providerGuestVocabulary,
		lang:       "ts",
	},
	"providerrender_host_gen.go": {
		schema:     schemas.WasmProviderV1alpha1,
		schemaFile: "wasm-provider-v1alpha1.schema.json",
		pkg:        "renderer",
		vocabulary: providerHostVocabulary,
		helpers:    true, // the host provider render codec is alone in package renderer
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
}

// schemaNode is a decoded JSON Schema object node preserving the declared
// property order.
type schemaNode struct {
	Type        string
	Description string
	GoType      string // x-aggrega-go-type override (names a hand-written or generic type)
	Ref         string // $ref target (e.g. #/$defs/Unit); resolves to a Go type name
	Required    []string
	Enum        []string    // enum values, in declared order (string enums only)
	props       []namedNode // in declared order
	Items       *schemaNode
	defs        []namedNode // $defs entries, in declared order (multi-type schemas only)
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
		Items       json.RawMessage `json:"items"`
		Properties  json.RawMessage `json:"properties"`
		Defs        json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return n, err
	}
	n.Type, n.Description, n.GoType, n.Ref, n.Required, n.Enum = flat.Type, flat.Description, flat.GoType, flat.Ref, flat.Required, flat.Enum
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

var initialisms = map[string]string{"api": "API", "url": "URL", "id": "ID", "ir": "IR", "uri": "URI", "json": "JSON"}

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
func emitStruct(b *strings.Builder, typeName, doc string, n schemaNode, used map[string]bool, vocab map[string]string) []pending {
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
		fmt.Fprintf(b, "\t%s %s `json:%q yaml:%q`\n", field, gt, jsonTag, yamlTag)
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
			field:    field,
			key:      p.name,
			goType:   goType(p.node, field, vocab),
			required: required[p.name],
			desc:     p.node.Description,
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
		guard := omitGuard("t."+f.field, f.goType)
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
// encoding/json's omitempty semantics for that type. An empty string means the
// type carries no omitempty notion (the field is always emitted).
func omitGuard(expr, goType string) string {
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
		// capture the raw value and unmarshal it. This is the only residual
		// stdlib decode; it is reached for an opaque `any` value the guest
		// authors on the output side, never for a typed field on the decode
		// hot path.
		emitDecodeAnyFallback(b, dst, goType, indent)
	}
}

// emitDecodeAnyFallback decodes an opaque value (an `any` field, or a pointer
// to a non-def type) by capturing its raw bytes and unmarshaling them. It is
// the one place a stdlib reflective decode remains; it is not on the typed
// decode path of the input payload.
func emitDecodeAnyFallback(b *strings.Builder, dst, goType, indent string) {
	fmt.Fprintf(b, "%sif raw, err := readRaw(dec, data); err != nil {\n%s\treturn err\n%s} else if err := json.Unmarshal(raw, &%s); err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent, dst, indent, indent)
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

	var body strings.Builder
	used := map[string]bool{}
	// Top-level entity struct, then its nested named structs breadth-first in
	// declared order.
	queue := []pending{{name: typeName, doc: root.Description, node: root}}
	emitQueue(&body, queue, used, nil)

	head := genHeader("package types",
		fmt.Sprintf("// aggrega-schemas/json/%s-v1alpha1.schema.json. DO NOT EDIT.\n", strings.ToLower(typeName)),
		fmt.Sprintf("// Hand-written content (shared types, methods, marshalers) lives in\n// %s.go and common.go; regenerate with `go generate ./pkg/types/...`.\n", strings.ToLower(typeName)),
		used)

	return formatSource(head, body.String())
}

// namedEnum is one field's resolved string enum: the schema field name and its
// declared-order enum values.
type namedEnum struct {
	field  string
	values []string
}

// collectEnums walks a schema node depth-first, in declared order, collecting
// every string `enum` whose owning field name is in want. It recurses through
// `properties` and `items` and does NOT stop at an x-aggrega-go-type annotation
// — the enum-bearing fields (e.g. exports.items.operates/scope) live under
// items nodes the struct path skips, so the enum walk must descend through them.
// A field name appears at most once: the first match in declared order wins, so
// a duplicated name across branches is collected once deterministically.
func collectEnums(n schemaNode, want map[string]bool, seen map[string]bool, out *[]namedEnum) {
	for _, p := range n.props {
		if len(p.node.Enum) > 0 && want[p.name] && !seen[p.name] {
			seen[p.name] = true
			*out = append(*out, namedEnum{field: p.name, values: p.node.Enum})
		}
		collectEnums(p.node, want, seen, out)
	}
	if n.Items != nil {
		collectEnums(*n.Items, want, seen, out)
	}
}

// generateEnums produces the formatted Go source for a string-enum-const file:
// one bare untyped string const per enum value of each requested field, named
// Pascal(field)+Pascal(value), in (field-declaration, then enum-array) order so
// regeneration is byte-stable. It is the single-type enum-emit path. Bare
// untyped consts (not a typed enum) keep the consts drop-in for a string-typed
// consumer (a string switch scrutinee, a string struct field) without forcing a
// type conversion at the comparison site.
func generateEnums(e enumSource) ([]byte, error) {
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

	// Every requested field must resolve to an enum; a missing one means the
	// schema changed (the field was renamed or its enum dropped) and the
	// generated consts would silently vanish. Fail loudly instead.
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

	var body strings.Builder
	for _, en := range enums {
		fieldPascal := pascal(en.field)
		fmt.Fprintf(&body, "const (\n")
		for _, v := range en.values {
			fmt.Fprintf(&body, "\t%s%s = %q\n", fieldPascal, pascal(v), v)
		}
		body.WriteString(")\n\n")
	}

	var docLines string
	if e.doc != "" {
		for _, line := range strings.Split(e.doc, "\n") {
			if line == "" {
				docLines += "//\n"
			} else {
				docLines += "// " + line + "\n"
			}
		}
		docLines += "\n"
	}

	head := genHeader("package "+e.pkg,
		fmt.Sprintf("// aggrega-schemas/json/%s. DO NOT EDIT.\n", e.schemaFile),
		"// Regenerate with `go generate ./pkg/types/...`.\n",
		map[string]bool{})

	return formatSource(head, docLines+body.String())
}

// generateDefs dispatches a multi-type codec target to its language backend.
// The front-end (schema decode, `$defs` walk, vocabulary rewrite, field
// resolution) is shared; only the emission half forks per language. An empty
// lang defaults to the Go backend so existing Go targets need no lang field.
func generateDefs(t codecTarget) ([]byte, error) {
	switch t.lang {
	case "", "go":
		return generateDefsGo(t)
	case "rust":
		return generateDefsRust(t)
	case "ts":
		return generateDefsTS(t)
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
	emitQueue(&body, queue, used, t.vocabulary)

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
	if t.helpers {
		emitCodecHelpers(&body)
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
func emitQueue(body *strings.Builder, queue []pending, used map[string]bool, vocab map[string]string) {
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nested := emitStruct(body, cur.name, cur.doc, cur.node, used, vocab)
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
		fmt.Fprintf(&head, "import %q\n\n", imports[0])
	case len(imports) > 1:
		head.WriteString("import (\n")
		for _, imp := range imports {
			fmt.Fprintf(&head, "\t%q\n", imp)
		}
		head.WriteString(")\n\n")
	}
	return head.String()
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
		return generateEnums(e)
	}
	if t, ok := codecTargets[base]; ok {
		return generateDefs(t)
	}
	return nil, fmt.Errorf("typegen: no kind, enum, or codec target registered for %q", base)
}
