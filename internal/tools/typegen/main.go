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
// cross-package opaque type, a host-qualified container) maps them to a neutral
// spelling (e.g. an opaque host type to json.RawMessage, the host's qualified
// order-preserving container to the destination package's own). Absence of an
// entry passes the annotation value through verbatim. An entry may rewrite what
// an order-preserving container holds but never the container itself: an entry
// that would turn one into a plain map is refused at generation, because the
// container's order is part of the wire contract the schema marks.
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

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// kindSource binds an output filename to the Go type name and the embedded
// schema bytes the generator reads. Adding a kind is a one-line entry.
type kindSource struct {
	typeName string
	schema   string
}

var kindSources = map[string]kindSource{
	"feature_gen.go":         {"Feature", schemas.FeatureV1alpha1},
	"manifest_gen.go":        {"Manifest", schemas.ManifestV1alpha1},
	"recipe_gen.go":          {"Recipe", schemas.RecipeV1alpha1},
	"featurerecipe_gen.go":   {"FeatureRecipe", schemas.FeatureRecipeV1alpha1},
	"schemalibrary_gen.go":   {"SchemaLibrary", schemas.SchemaLibraryV1alpha1},
	"mappinglibrary_gen.go":  {"MappingLibrary", schemas.MappingLibraryV1alpha1},
	"workloadlibrary_gen.go": {"WorkloadLibrary", schemas.WorkloadLibraryV1alpha1},
	"artifactlibrary_gen.go": {"ArtifactLibrary", schemas.ArtifactLibraryV1alpha1},
	"aggregate_gen.go":       {"Aggregate", schemas.AggregateV1alpha1},
	"release_gen.go":         {"Release", schemas.ReleaseV1alpha1},
	"component_gen.go":       {"Component", schemas.ComponentV1alpha1},
	"plugin_gen.go":          {"Plugin", schemas.PluginV1alpha1},
	"workload_gen.go":        {"Workload", schemas.WorkloadV1alpha1},
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
	// code as hostfunc_error_code on a host-function reply envelope when a
	// callback fails; a guest branches on it without parsing the message. The key
	// names this catalog rather than failure in general, because a socket output
	// travelling the other way carries a halting condition's identifiers under
	// condition_code and condition_short_code, which are a different vocabulary.
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
	"one as hostfunc_error_code beside the free-form error message when a callback\n" +
	"fails; a guest branches on the code without parsing the message, tolerating an\n" +
	"absent code (older host) or an unrecognized one (newer host) by falling back\n" +
	"to the message. Generated into Go, Rust, and TypeScript from one schema so\n" +
	"the three codecs share one closed vocabulary.\n" +
	"\n" +
	"This closed catalog is a different vocabulary from the halting conditions the\n" +
	"compiler registers. A guest reporting a condition it raised puts that\n" +
	"condition's own identifiers on its socket output, under condition_code and\n" +
	"condition_short_code, and those values are not members of this set. Each key\n" +
	"names the vocabulary it draws from, so neither can be read as the general case."

// codecTarget binds an output filename to a multi-type codec emission: the
// embedded `$defs`-shaped schema, the schema file basename for the generated
// header, the destination package name, the per-target type vocabulary that
// rewrites x-aggrega-go-type values, and the target language. Each `$defs`
// entry becomes one typed value plus a streaming JSON codec.
//
// lang selects the backend: "" or "go" emits the reflect-free Go codec
// (Marshal/UnmarshalJSON); "rust" emits serde-derive structs (BTreeMap for
// sorted map keys, the crate's OrderedMap for an order-preserving one, RawValue
// for verbatim opaque carry); "ts" emits TypeScript interfaces plus thin
// per-type encoders and decoders. The Go, Rust, and TS codecs produce
// byte-identical wire output from one schema: a plain map emits in sorted key
// order, a map the schema marks order-preserving emits in the order it holds,
// and opaque values carry verbatim. A target that reads no schema — the shared
// CBOR helpers, the order-keeping container of each binding language, the
// plugin-wire registry — carries a lang naming what it emits instead.
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
	// skipCBOR withholds the canonical-CBOR codec from this target, leaving it the
	// JSON codec alone. It defaults false, so a target that says nothing carries
	// both.
	//
	// The default is what emitsCBOR's criterion asks for: a socket payload is
	// encoded by one side and decoded by the other, so withholding the codec would
	// leave one end of such a wire with no way to speak it. Setting this records
	// that the criterion does not hold for the target — that its wire has no
	// second side reading CBOR at all — which is a different claim from "nothing
	// has asked for it yet", and is why the exclusion is stated at the
	// registration, with its reason, rather than inferred from the schema or the
	// language.
	skipCBOR bool
	// codecOnly emits the canonical-CBOR codec ALONE: no struct declarations, and
	// no JSON codec either. It is the mode for a destination package that already
	// declares these types by hand — the compiler's own core types, whose
	// declarations are hand-written so their field order is the author's and not
	// the schema's. A codec target ordinarily emits a type's declaration and its
	// codec as one artifact, which would make such a package a declaration target
	// and take that order away from it.
	//
	// The two exclusions are one decision, not two, and they share one reason: the
	// destination package owns both the declarations AND the way those types are
	// already marshalled. Emitting MarshalJSON/UnmarshalJSON onto them would
	// silently reroute every existing encoding/json call on those types through a
	// different encoder — the generated one emits fields in schema order, the
	// standard library in Go struct order — which moves bytes on paths that reach
	// committed output. A CBOR codec adds methods nothing was calling, so it moves
	// none.
	//
	// The codec still needs the shared CBOR helpers, so a codecOnly target sets
	// helpers as usual; the JSON helpers are skipped along with the JSON codec they
	// serve. (Ignored by the Rust/TS backends, which emit no separate host codec.)
	codecOnly bool
	// opaqueHostTypes names, per "<DefType>.<property>", the Go type the
	// destination package actually declares for a field whose x-aggrega-go-type
	// annotation spells the OPAQUE wire realization rather than that package's own
	// type.
	//
	// Why the annotation cannot simply be corrected: for an opaque field the
	// neutral spelling is the DELIBERATE one. Annotating such a field with its host
	// spelling was measured and refused — the host codec would then encode it
	// structurally, and the counterpart would have to declare a typed container to
	// decode that. The narrowed form ships: annotate the host spelling only outside
	// the opaque set. So for the opaque set the schema states the wire and this
	// table states the host, and the two are different facts rather than one of
	// them being wrong.
	//
	// It changes what the wire carries not at all. A listed field still rides as a
	// CBOR byte-string of its verbatim JSON bytes, which for an order-preserving
	// container is that container's own author-ordered encoding — so the order
	// survives inside the bytes.
	// The declared type is read for ONE purpose: the omission decision, which has
	// to be asked of the real type, since the emptiness of an order-preserving
	// container is not the emptiness of a byte slice.
	//
	// Only a codecOnly target needs it: a target that emits its own declarations
	// realizes the annotation itself, so annotation and declaration cannot disagree
	// there. A codecOnly target must list every bare-opaque field it carries —
	// generation refuses otherwise, rather than emitting a guard for the wrong type.
	//
	// An entry whose value is the spelling the annotation already resolved to means
	// "the declaration agrees", and selects no different arm — see
	// applyOpaqueHostTypes.
	opaqueHostTypes map[string]string
	// crossPackageDefTypes names generated types, PACKAGE-QUALIFIED, whose codec is
	// generated into a different package than this target's own — the graph a policy
	// payload carries, whose codec is emitted into the compiler's core types package
	// alongside the hand-written declarations it attaches to.
	//
	// It is a distinct surface from externalDefTypes, and the two are not
	// interchangeable. That one is same-package by its own contract, so the dispatch
	// it enables uses the package-local unexported worker; across a package boundary
	// that worker is unreachable, and the streaming reader it takes is a separate
	// type in every destination package besides. So a qualified reference needs its
	// own emission through the exported entry point, which is what naming a type here
	// selects. Widening externalDefTypes does not reach it either: the obstacle is the
	// emitted CALL, not the lookup.
	//
	// The wire is identical either way. A nested encoding is one complete CBOR item,
	// so appending the other package's encoding is byte-for-byte what encoding it in
	// place would have produced — which is what lets a payload carrying a shared shape
	// ride the wire as a structural map rather than as a byte-string of JSON bytes the
	// counterpart cannot decode.
	//
	// Two bounds, stated because neither is visible from a target that works. The
	// dispatch covers a field whose resolved type IS the qualified type, and a
	// composite over it (a slice, a map, an order-preserving container), since those
	// descend through the same value emitter. It does NOT cover a POINTER to one:
	// that arm has its own emission and keeps the in-package form, so such a field
	// fails to compile rather than emitting something wrong. And the decode side
	// reads the destination package's own streaming reader directly, so that package
	// must carry the shared CBOR helpers — any one file in it setting helpers is
	// enough, since they are package-scoped.
	//
	// (Ignored by the Rust/TS backends, whose runtime resolves a referenced type by
	// name within the crate/module the codecs share.)
	crossPackageDefTypes []string
	// skipDefs names $defs this target does not emit a codec for.
	//
	// It exists for the codecOnly mode, where the emitted codec attaches to
	// declarations the destination package carries by hand. A def that package does
	// not declare has nothing to attach to, so emitting its codec would not compile —
	// and declaring the type here to satisfy the generator would be the wrong repair
	// twice: it duplicates a type that already exists elsewhere, and where that
	// elsewhere is a published package the duplicate would carry a SECOND codec for
	// one wire shape, which is the divergence a single home exists to prevent. A
	// socket whose input types are the compiler's own and whose output type is the
	// published one is the ordinary shape of this, not an edge case.
	//
	// The exclusion is explicit rather than derived because the generator does not
	// read its destination package's declarations. So the failure direction is chosen:
	// a def added to a schema is emitted by default and breaks the build loudly if
	// nothing declares it, while a def skipped here is named at the target where a
	// reader of the target sees it. Two gates hold the table — generation refuses a
	// skipped def that an emitted def references (which would otherwise dispatch to a
	// codec that does not exist), and a registry gate reads it in the direction a
	// lookup cannot, catching an entry naming a def the schema no longer declares.
	skipDefs []string
	// wireExports binds a guest export to the $def carrying that export's INPUT
	// payload, for every export this target's types serve. It is one half of what
	// the plugin-wire registry is emitted from; binaryWire below is the other.
	//
	// The export NAME is deliberately absent: an entry names either the attachment
	// ROLE whose default the export is, or the plugin TYPE whose required export it
	// is, and the name is read from the plugin type vocabulary's canonical rule for
	// that key. Those rules are also what the host's own dispatch reads, so the
	// registry and the dispatch cannot disagree about what an export is called —
	// there is no second spelling of the name to drift.
	//
	// A target registered on the binary wire must declare every export its socket
	// carries here, and generation refuses one that declares none: a socket that
	// moved to the binary wire with no entry would leave a caller converting an
	// authored payload for its export handing the guest text it cannot read.
	//
	// An entry is admitted only where something can PIN it: the socket's output
	// decode must route through the host's two encoding-specific decode entry
	// points, because naming one of those two at a call site is the observable
	// choice the registry is checked against. A socket that decodes through its
	// generated codec directly offers nothing to check, so an entry for it would
	// be an unpinned hand-written fact — which is the failure this registry exists
	// to avoid rather than to relocate.
	wireExports []wireExport
	// binaryWire records whether the exports named in wireExports carry the
	// canonical-CBOR wire rather than the text one.
	//
	// It sits on the target rather than on each entry because the target is the
	// unit that moves: one target is one schema realized into one destination
	// package, there is no wire negotiation, and a socket's host side and its whole
	// guest set therefore change encoding together or not at all. Two exports
	// sharing a target share a wire by construction.
	binaryWire bool
	// pkgPath is the destination package's Go import path. Only a target carrying
	// wireExports needs it — the registry emitter imports the package to name the
	// payload type — and emission refuses a wireExports target that omits it.
	pkgPath string
}

// wireExport binds one guest export to the $def carrying its input payload.
//
// The export is named by exactly one of role and pluginType, and generation
// refuses an entry naming both or neither. Naming the key rather than the export
// is what keeps the export name single-sourced: it is read at emission from the
// rule that key is governed by.
type wireExport struct {
	// role is the governance attachment role whose default entry function is the
	// export this payload shape serves, read from the canonical role-to-export
	// rule.
	role plugintype.Role
	// pluginType is the plugin type whose required export this payload shape
	// serves, for a socket whose export is fixed by the type rather than chosen by
	// an attachment. The export is that type's minimum-export rule, which must name
	// one exact export rather than a prefix.
	pluginType plugintype.Type
	// inputDef is the $def name of the type carrying that export's input payload.
	inputDef string
}

// exportName resolves the export this entry names through the rule its key is
// governed by, and refuses an entry the rules cannot resolve to exactly one
// export name.
func (w wireExport) exportName() (string, error) {
	switch {
	case w.role != "" && w.pluginType != "":
		return "", fmt.Errorf("names both role %q and plugin type %q, so the export it serves is "+
			"named twice", string(w.role), string(w.pluginType))
	case w.role != "":
		export, ok := plugintype.GovernanceRoleExport(w.role)
		if !ok {
			return "", fmt.Errorf("names role %q, which the canonical role-to-export rule does not know",
				string(w.role))
		}
		return export, nil
	case w.pluginType != "":
		entry, ok := plugintype.Lookup(w.pluginType)
		if !ok {
			return "", fmt.Errorf("names plugin type %q, which the plugin-type registry does not know",
				string(w.pluginType))
		}
		if entry.MinExport.Name == "" || entry.MinExport.IsPrefix {
			return "", fmt.Errorf("names plugin type %q, whose minimum-export rule names no single export",
				string(w.pluginType))
		}
		return entry.MinExport.Name, nil
	default:
		return "", fmt.Errorf("names neither a role nor a plugin type, so nothing resolves the export it serves")
	}
}

// emitsDef reports whether this target emits a codec for the named $def.
func (t codecTarget) emitsDef(name string) bool {
	for _, skipped := range t.skipDefs {
		if skipped == name {
			return false
		}
	}
	return true
}

// emitsCBOR reports whether a codec target carries the canonical-CBOR codec
// alongside JSON — EncodeCBOR/DecodeCBOR/DecodeCBORStrict plus a
// `cbor:"<key>[,omitempty]"` struct tag in Go, and the hand-written CBOR
// encoders in Rust and TS.
//
// A target carries it unless it says otherwise: a socket payload is encoded by
// one side and decoded by the other, so a target that lacked the codec would
// leave one end of that wire with no way to speak it. A target whose wire has no
// such second side states that in skipCBOR, where the reason sits beside the
// registration it applies to.
func (t codecTarget) emitsCBOR() bool {
	return !t.skipCBOR
}

// frpGuestNeutralVocabulary maps the FRP schema's host type spellings to a
// neutral, cycle-free guest vocabulary: an opaque cross-package host type
// becomes json.RawMessage (verbatim passthrough preserves its bytes), including
// the values an order-preserving container holds, while the container itself
// stays the guest package's own order-keeping one, so a guest receives the order
// the host sent. With this vocabulary the generated codec references only
// builtins, encoding/json, and its own package's types — no cross-package import,
// so the codec compiles in a standalone, dependency-free module a Wasm guest can
// import directly.
var frpGuestNeutralVocabulary = map[string]string{
	"renderctx.RenderContext": "json.RawMessage",
	"OrderedMap[any]":         "OrderedMap[json.RawMessage]",
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

// mutatorGuestVocabulary is empty: the mutator schema's annotations already spell
// the guest realization. Its two order-preserving containers resolve to the
// guest package's own order-keeping container, so a guest receives the graph's
// nodes and each node's labels in the order the host holds them, and the
// mutator carries no cross-package opaque host type.
var mutatorGuestVocabulary = map[string]string{}

// mutatorHostVocabulary maps the mutator schema's container annotations to the
// host realization: the order-preserving types.OrderedMap (qualified, since the
// codec lands in package topology), so the host wire carries the order the host
// built each container in, which is the order identical input always builds
// (the determinism contract).
var mutatorHostVocabulary = map[string]string{
	"OrderedMap[MutatorNode]": "types.OrderedMap[MutatorNode]",
	"OrderedMap[string]":      "types.OrderedMap[string]",
}

// annotationTransformGuestVocabulary is empty: the annotation-transform schema's
// one container annotation already spells the guest package's own order-keeping
// container, so a guest receives a target's annotations in the order the host
// holds them, and the schema carries no cross-package opaque host type.
var annotationTransformGuestVocabulary = map[string]string{}

// annotationTransformHostVocabulary maps the annotation-transform schema's
// container annotation to the host realization: the order-preserving
// types.OrderedMap (qualified, since the codec lands in package pipeline), so the
// host wire carries the order the host built the container in, which is the order
// identical input always builds (the determinism contract).
var annotationTransformHostVocabulary = map[string]string{
	"OrderedMap[string]": "types.OrderedMap[string]",
}

// linterGuestVocabulary is empty: the container a Release's components ride in is
// annotated as the order-keeping container of the package the guest codec lands
// in, so the guest receives the components in the Aggregate's own instance order.
// The schema's other opaque fields already carry the neutral json.RawMessage
// spelling, and absence of an entry passes each annotation through verbatim —
// including the policies field, whose x-aggrega-go-type: Policies annotation
// resolves to the typed Policies struct the guest realizes.
var linterGuestVocabulary = map[string]string{}

// linterHostVocabulary maps the linter schema's policies annotation to the host
// realization. The host assembles the publishing-discipline configuration
// dynamically from the workspace config plus CLI tri-state overrides and pipes
// it through as opaque bytes (the proven cross-boundary json.RawMessage carry),
// so the host LinterInput.Policies is a json.RawMessage rather than the typed
// Policies struct the guest realizes. A Release's components ride in the
// order-preserving types.OrderedMap (qualified, since the codec lands in package
// validator), so the host writes them in the Aggregate's own instance order and
// the payload is byte-identical across runs of identical input. The entity
// fields already carry json.RawMessage from the schema annotations.
var linterHostVocabulary = map[string]string{
	"Policies":                    "json.RawMessage",
	"Diagnostic":                  "abi.Diagnostic",
	"OrderedMap[json.RawMessage]": "types.OrderedMap[json.RawMessage]",
}

// irGuestVocabulary maps the host's named attachment-provenance string type to a
// bare string, which is the whole of what a guest forwarding the value needs.
// Every order-preserving container annotation already spells the guest
// package's own order-keeping container, at every level it nests, so it needs
// no entry and a guest receives the graph in the order the host holds it. The
// inject/values/infra fields already carry the opaque json.RawMessage spelling
// from the schema annotations, and the recursive/pointer fields carry their
// pointer spelling, so neither needs a vocabulary entry.
var irGuestVocabulary = map[string]string{
	"AttachmentOrigin": "string",
}

// irHostVocabulary is empty, and empty is the correct content rather than a
// placeholder. The IR schema's x-aggrega-go-type annotations already spell the
// host types — the bare order-preserving container, the opaque json.RawMessage,
// the named attachment-provenance string type, the pointer-broken recursive
// field — because the compiler's core types package is where those types are
// declared. A codec emitted into that package sees its own types unqualified, so
// every annotation passes through verbatim and there is nothing to rewrite.
//
// The named attachment-provenance type needs no entry either. The guest maps it
// to a bare string because a guest forwarding the value needs nothing more; the
// host cannot, because the field's declaration is hand-written and rewriting the
// codec's view of its type would not change the field. The codec instead reads
// the value's kind from the schema's declared type and converts at the encode and
// decode boundary, so both ends of the wire carry a CBOR text string.
var irHostVocabulary = map[string]string{}

// irOpaqueHostTypes gives the host declaration's own Go type for each IR field
// the schema annotates with the opaque wire spelling. Ten fields, all of one
// shape: an order-preserving container, or the typed inject block, whose contents
// the wire never looks inside.
//
// This is where the deliberate asymmetry is written down. Every other annotation
// in the IR schema states the host spelling, which the guest realizes as its own
// package's types; these ten state the OPAQUE spelling, and that direction was
// ruled rather than overlooked — annotating them with their host spelling was
// measured to work and refused. So the schema states what the wire carries and
// this table states what the compiler holds, which are two different facts.
//
// The order-preserving containers are what make the entries worth having: each
// one's emptiness has to be asked of the container, not of a byte slice, or the
// field would contradict the omitempty its own declaration promises.
var irOpaqueHostTypes = map[string]string{
	"IRNode.self_inject":              "*InjectBlock",
	"IRNode.uses":                     "OrderedMap[UsesSocketValue]",
	"IREdge.values":                   "OrderedMap[any]",
	"IREdge.infra":                    "OrderedMap[any]",
	"IREdge.inject":                   "OrderedMap[OrderedMap[string]]",
	"IRFeatureEntry.values":           "OrderedMap[any]",
	"IRFeatureEntry.infra":            "OrderedMap[any]",
	"IRFeatureEntry.inject":           "OrderedMap[OrderedMap[string]]",
	"IRFeatureEntry.sensitive_inject": "OrderedMap[OrderedMap[string]]",
	"IRBoundaryFeature.infra":         "OrderedMap[any]",
}

// policyGuestVocabulary is empty: the policy schema's graph field is annotated
// with the IRGraph type (emitted by the IR codec target into the same abi
// package and named in the policy target's externalDefTypes), and the rest are
// plain scalars and string slices. Absence of an entry passes annotations
// through verbatim.
var policyGuestVocabulary = map[string]string{}

// policyHostVocabulary maps the policy schema's graph annotation to the host
// realization: the IR graph the Topology Builder emits, qualified, since the host
// policy codec lands in the analyzer package while the graph and its codec are
// declared in the core types package. Naming the qualified type here is only half
// of what makes the graph ride the wire structurally — the target also names it in
// crossPackageDefTypes, which is what selects a dispatch to the graph's own
// generated codec instead of the opaque arm.
//
// Nothing else in this schema needs an entry: the remaining carries are scalars,
// string slices, def-local shapes reached by $ref, and the opaque settings and
// artifact-tree documents, whose host declarations are stated in
// policyOpaqueHostTypes.
var policyHostVocabulary = map[string]string{
	"IRGraph": "types.IRGraph",
}

// policyOpaqueHostTypes gives the host declaration's own Go type for each policy
// field the schema annotates with the opaque wire spelling.
//
// The two settings documents are the entries that do work. The schema states the
// wire — a free-form document the host hands to the plugin without reading it —
// while the analyzer holds each as a decoded map, so the omission decision has to
// be asked of the map: a document's absence is an empty map here, not an empty byte
// slice, and guarding on the wrong one contradicts the omitempty the declaration
// promises.
//
// The artifact tree is the third shape a codec-only target has to be told about and
// the one that carries no divergence: the schema states the opaque spelling and the
// analyzer declares exactly that, so the entry records agreement. It selects no
// different encoding — the bytes ride verbatim, which is what the counterpart
// reads — and it is present because generation refuses an opaque field the target
// says nothing about, and saying "the declaration agrees" is how a target answers.
var policyOpaqueHostTypes = map[string]string{
	"PolicyInput.settings":       "map[string]any",
	"RenderPolicyInput.settings": "map[string]any",
	"RenderedArtifact.tree":      "json.RawMessage",
}

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
// container becomes the guest package's own order-keeping container, at every
// level it nests, with only what it holds rewritten where that is a host type —
// so a guest receives each container in the order the host sent it. The shared
// diagnostic is not opaque: it is the generated abi.Diagnostic (named in the
// target's externalDefTypes), so the guest codec dispatches straight to its own
// generated codec. The ordered structured content of an output file is the same
// order-preserving-any container as a node's values, so both surface to the guest
// as an order-keeping container of json.RawMessage — the guest reads and echoes
// the content by key, and the host runs the single serializer that turns it into
// bytes. The pipeline-codec unit and patched-unit shapes surface to the guest
// opaquely (json.RawMessage): an external PDK provider guest that implements the
// optional carve/reassemble exports carries them verbatim, while the host
// realizes them as the typed frp shapes its pipeline drives.
var providerGuestVocabulary = map[string]string{
	"renderctx.RenderContext":   "json.RawMessage",
	"renderctx.BoundaryContext": "json.RawMessage",
	"Unit":                      "json.RawMessage",
	"PatchedUnit":               "json.RawMessage",
	"JSONPatchOp":               "json.RawMessage",
	"types.EnrichedEdge":        "json.RawMessage",
	"OrderedMap[any]":           "OrderedMap[json.RawMessage]",
	"types.OrderedMap[string]":  "OrderedMap[string]",
	"types.OrderedMap[types.OrderedMap[string]]": "OrderedMap[OrderedMap[string]]",
	"OrderedMap[types.IRFeatureEntry]":           "OrderedMap[json.RawMessage]",
	"OrderedMap[types.Component]":                "OrderedMap[json.RawMessage]",
	"OrderedMap[types.Feature]":                  "OrderedMap[json.RawMessage]",
	"OrderedMap[types.Recipe]":                   "OrderedMap[json.RawMessage]",
	"OrderedMap[types.FeatureRecipe]":            "OrderedMap[json.RawMessage]",
	"OrderedMap[types.MappingLibrary]":           "OrderedMap[json.RawMessage]",
	"types.Workload":                             "json.RawMessage",
	"map[string]types.OrderedMap[string]":        "map[string]OrderedMap[string]",
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
// source bytes (a []byte field the codec converts to whatever form the wire
// carries bytes in), the opaque options and tree
// (json.RawMessage passthrough), and the def-local span/diagnostic types
// referenced by $ref — already spell the neutral guest types the generator
// emits. The parser attaches no meaning to the tree, so it carries it verbatim;
// no host container or cross-package opaque type needs a rewrite, and every
// annotation passes through unchanged.
var parseGuestVocabulary = map[string]string{}

// importGuestVocabulary is empty: the import schema's non-scalar carries — the
// source bytes (a []byte field the codec converts to whatever form the wire
// carries bytes in), the opaque settings document
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
	// package abi (helpers: false, the default). The annotated carries on this
	// schema are of two kinds: an opaque JSON Schema document held verbatim as
	// json.RawMessage, and a value held by reference so that omitting it stays
	// distinguishable from declaring it empty. The schema's own annotations are the
	// list of which fields those are; naming the kinds rather than the fields keeps
	// this from going stale as the descriptor grows.
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
		pkgPath:    "github.com/aggrega-ai/aggrega-cli/internal/compiler/topology",
		// binaryWire: this socket's wire is canonical CBOR. Its call site encodes
		// with the generated encoder and decodes through the binary encoding's
		// entry point, and every guest it speaks to reads and writes the same.
		binaryWire: true,
		wireExports: []wireExport{
			{role: plugintype.RoleLabelTransform, inputDef: "MutatorInput"},
		},
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
		pkgPath:    "github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline",
		// binaryWire is false: this socket still carries the text wire in both
		// directions. Its call site encodes with encoding/json and decodes through
		// the text encoding's entry point.
		wireExports: []wireExport{
			{role: plugintype.RoleAnnotationTransform, inputDef: "AnnotationTransformInput"},
		},
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
	// The host IR codec, committed into the compiler's own core types package.
	// codecOnly: that package declares all eleven IR types by hand, so the codec
	// attaches to those declarations instead of replacing them — which is what
	// keeps their field order the author's rather than the schema's. It is also
	// why this target emits no JSON codec: the package's types are already
	// marshalled by the standard library, and taking that over would move bytes
	// this codec has no business moving.
	//
	// helpers: it is the only CBOR codec in that package, so it owns the shared
	// CBOR helpers.
	//
	// This is the codec a host socket payload carrying the graph dispatches to,
	// which is the whole reason it exists: a qualified generated type reaches its
	// own generated codec, so the graph rides the wire as a structural CBOR map —
	// the shape the guest side already decodes — rather than as a byte-string of
	// JSON bytes.
	"ir_host_gen.go": {
		schema:          schemas.IRSchemaV1alpha1,
		schemaFile:      "ir-v1alpha1.schema.json",
		pkg:             "types",
		vocabulary:      irHostVocabulary,
		helpers:         true,
		codecOnly:       true,
		opaqueHostTypes: irOpaqueHostTypes,
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

	// The host policy codec, committed into the analyzer package that assembles
	// these payloads.
	//
	// codecOnly: that package declares the payload types by hand, so the codec
	// attaches to those declarations rather than replacing them — which is what keeps
	// their field order the author's and leaves the way they are already marshalled
	// alone.
	//
	// crossPackageDefTypes carries the whole point of the target. The graph field is
	// the IR graph, whose codec is generated into the core types package alongside
	// the hand-written declarations it attaches to; naming it qualified here is what
	// makes this codec dispatch to that one, so the graph rides the wire as a
	// structural CBOR map — the shape the plugin side already decodes — rather than
	// as a byte-string of JSON bytes it cannot read.
	//
	// skipDefs excludes the verdict envelope, which this package does not declare: the
	// host reads a plugin's verdicts through the published type, and that type already
	// carries a generated codec of its own. Emitting a second one here would put two
	// codecs on one wire shape, and declaring the type to satisfy the generator would
	// duplicate a published declaration. The exclusion also keeps the shared
	// diagnostic out of this target entirely, since the envelope is the only place
	// this schema mentions it.
	//
	// helpers: it is the only CBOR codec in that package, so it owns the shared CBOR
	// helpers — which the graph dispatch's decode side reads directly.
	//
	// wireExports carries both verdict roles, because this one schema serves two
	// pipeline stages with two different input shapes — the IR graph at static
	// analysis, the rendered artifacts after rendering. Each role has its own
	// default export name, so an export name names exactly one input shape and
	// the registry can encode for it without being told which stage it is
	// serving. The table lists a row per role because the shapes differ, not
	// because the names collide.
	"policy_host_gen.go": {
		schema:               schemas.WasmPolicyV1alpha1,
		schemaFile:           "wasm-policy-v1alpha1.schema.json",
		pkg:                  "analyzer",
		vocabulary:           policyHostVocabulary,
		helpers:              true,
		codecOnly:            true,
		opaqueHostTypes:      policyOpaqueHostTypes,
		crossPackageDefTypes: []string{"types.IRGraph"},
		skipDefs:             []string{"PolicyOutput"},
		pkgPath:              "github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer",
		// binaryWire is false: both verdict roles still carry the text wire.
		wireExports: []wireExport{
			{role: plugintype.RoleIRVerdict, inputDef: "PolicyInput"},
			{role: plugintype.RoleRenderVerdict, inputDef: "RenderPolicyInput"},
		},
	},

	// Provider render socket — guest codecs into aggrega-abi (Go/Rust/TS) and the
	// host codec into aggrega-cli's renderer package. The guest realizes the
	// order-preserving containers as its own order-keeping container and the
	// opaque cross-package host types (render context, boundary context) as
	// json.RawMessage; the host realizes the containers as types.OrderedMap and the
	// opaque host types at their real spelling. The abi Go codec references the helpers FRP owns in
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
	// ParseInput (format, source bytes, opaque options) and returns a ParseOutput
	// (the opaque tree plus optional diagnostics and source spans). Every carry is
	// a scalar, a byte field, an opaque json.RawMessage, or a def-local span/
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
	// receives an ImportInput (format, primary source bytes, optional named
	// sources, opaque settings) and returns an ImportOutput (the emitted
	// entities, the mapping decisions behind them, optional diagnostics, and
	// optional scaffold files). Every carry is a scalar, a byte field, an opaque
	// json.RawMessage, a string-to-string map, or a def-local nested type
	// referenced by $ref, so the guest vocabulary is empty. The abi Go codec
	// references the helpers FRP owns in package abi (helpers: false, the default).
	//
	// This socket has no host codec of its own: the host reads and writes this abi
	// Go codec directly, so the wire registration sits here. binaryWire: the
	// socket's wire is canonical CBOR in both directions — its call site encodes the
	// input with the generated encoder and decodes the output through the binary
	// encoding's entry point, and every importer guest reads and writes the same.
	// Its one export is the importer type's required export rather than a role's
	// default, so the entry names the plugin type.
	"import_gen.go": {
		schema:           schemas.WasmImportV1alpha1,
		schemaFile:       "wasm-import-v1alpha1.schema.json",
		pkg:              "abi",
		vocabulary:       importGuestVocabulary,
		externalDefTypes: []string{"Diagnostic"},
		pkgPath:          "github.com/aggrega-ai/aggrega-abi-go/pkg/abi",
		binaryWire:       true,
		wireExports: []wireExport{
			{pluginType: plugintype.Importer, inputDef: "ImportInput"},
		},
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
	// The session wire — a TypeScript binding only, into aggrega-abi. This schema
	// describes the request, response and notification shapes the long-running
	// session puts on its JSON-RPC wire; the host's own declarations are
	// hand-written and this target does not touch them, so nothing here emits a
	// host codec and the schema remains a description of those declarations rather
	// than their source.
	//
	// One language rather than three, and deliberately: the audience is a client
	// author driving the session over standard input and output, which is the
	// editor-integration case. A guest plugin never speaks this wire, so the
	// coverage assertion that obliges a socket payload to carry all three bindings
	// does not reach it — its criterion is a socket payload or the IR graph, and
	// this is neither.
	//
	// pkg is deliberately absent: it names the destination Go package, only the Go
	// backend reads it, and there is no Go target here to name one for.
	//
	// skipCBOR is set because the criterion for carrying a canonical-CBOR codec is
	// a payload one side encodes and the other decodes, and that criterion does
	// not hold here rather than merely going unexercised. This wire is JSON-RPC
	// text over standard input and output, and this binding is the only generated
	// side of it: no host codec, no codec in another language, and no guest, since
	// a guest plugin never speaks this wire. A CBOR codec emitted here would be a
	// second encoding of every message that nothing at either end reads.
	"servewire_gen.ts": {
		schema:     schemas.ServeWireV1alpha1,
		schemaFile: "serve-wire-v1alpha1.schema.json",
		lang:       "ts",
		skipCBOR:   true,
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
	// The order-keeping container of each guest binding language, for a map whose
	// order is part of what it carries. Schema-free like the helpers above, one
	// target per language: a Go and a Rust type, and the TypeScript reader and
	// writer that let the language's own Map keep the order of a JSON object's
	// members. The Go target references the streaming JSON helpers and the
	// repeated-key error the package's helpers-bearing file declares.
	"orderedmap_gen.go": {
		pkg:  "abi",
		lang: "go-ordered-map",
	},
	"orderedmap_gen.rs": {
		pkg:  "abi",
		lang: "rust-ordered-map",
	},
	"orderedmap_gen.ts": {
		pkg:  "abi",
		lang: "ts-ordered-map",
	},
	// The plugin-wire registry. Schema-free and derived: it reads no schema of its
	// own and instead walks the targets above, collecting each one's wireExports
	// and binaryWire, so the export-to-payload-to-encoding table is emitted from
	// the same registrations that drive the codecs rather than hand-kept beside
	// them — and generation fails on a target registered on the binary wire that
	// declares no export.
	//
	// What reads it: the direct-invocation command, which takes an authored text
	// payload and has to hand the guest whatever the named export's wire carries.
	// It cannot derive that from the export name — the socket fixes it — so the
	// fact has to be written down somewhere, and here it sits on the registrations
	// a test can pin against the call sites they describe.
	"pluginwire_gen.go": {
		pkg:  "pluginwire",
		lang: "go-wire-registry",
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
	// Types is the `type` keyword as the set of type names it declares. JSON
	// Schema admits two spellings, a single name and an array of names, and the
	// array spelling is a statement about the value rather than a shorthand for
	// one of its members: a field that is an array or null, an identifier that
	// is a string or a number. Both say something true that a narrower reading
	// would discard, so the set is kept whole and the single-name spelling is
	// simply a set of one. Ask declaredType for the one name, where one is what
	// the caller needs.
	Types       []string
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

// jsonNull is the `type` keyword's name for the absent value. It is a constant
// because the two accessors below read it for different halves of one fact, and
// a second spelling of it would let those halves disagree.
const jsonNull = "null"

// valueType reports the one type name describing the value's SHAPE, and the
// empty string where the schema names none. The mechanical mappings — Go type,
// structural refusal, nested struct discovery — each turn one shape into one
// answer, so routing them all through this accessor makes their behaviour on a
// set a property of the accessor rather than of each call site's discretion.
//
// A set of one names that one. A set of exactly two, one of them `null`, names
// the other: `null` alongside a single name says the value is sometimes sent as
// nothing, which is a fact about PRESENCE rather than a second shape, so the
// shape is the remaining name and every mapping has its answer. Ask nullable
// for the presence half — the two are read separately because a backend whose
// language distinguishes an empty value from an absent one needs both, while
// one whose zero value already carries absence needs only the shape.
//
// Any other set names no one shape. A union of genuinely different shapes — an
// identifier the transport defines as a string or a number — has no single name
// to give, and choosing a member would publish a contract the schema never
// stated, so every mapping reaches its fall-through arm on one.
func (n schemaNode) valueType() string {
	switch len(n.Types) {
	case 1:
		return n.Types[0]
	case 2:
		if n.Types[0] == jsonNull {
			return n.Types[1]
		}
		if n.Types[1] == jsonNull {
			return n.Types[0]
		}
	}
	return ""
}

// nullable reports whether the type set declares `null` alongside exactly one
// other name, which is how a schema states that a value is sent as nothing when
// it has nothing to report. It is the presence half of what valueType reads,
// and it is kept apart from the shape because the two halves land differently
// per language: a nil Go slice already marshals to null and back, so the Go
// mapping needs the shape alone, while a language whose array type admits no
// null needs the absence stated in the published type or its consumer meets the
// null at run time with nothing at compile time to have warned them.
func (n schemaNode) nullable() bool {
	if len(n.Types) != 2 {
		return false
	}
	return n.Types[0] == jsonNull || n.Types[1] == jsonNull
}

// decodeTypeKeyword reads the `type` keyword in either spelling JSON Schema
// admits — a single name, or an array of names — and returns what was declared,
// verbatim and in order. A typed string field cannot do this: it fails the
// whole node decode on the array spelling, and because `type` is decoded in the
// same pass as every other keyword, a node that fails there is never read at
// all. So the keyword is taken raw and interpreted here.
func decodeTypeKeyword(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("the type keyword is %s, which is neither a type name nor an array of type names", raw)
	}
	return many, nil
}

func decodeNode(raw json.RawMessage) (schemaNode, error) {
	var n schemaNode
	// Decode the flat fields first.
	var flat struct {
		Type        json.RawMessage `json:"type"`
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
	n.Description, n.GoType, n.Ref, n.Required, n.Enum = flat.Description, flat.GoType, flat.Ref, flat.Required, flat.Enum
	types, err := decodeTypeKeyword(flat.Type)
	if err != nil {
		return n, err
	}
	n.Types = types
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
//
// The mapping reads the node's SHAPE, so a value the schema declares as a
// collection-or-null maps to the collection: a nil Go slice marshals to null
// and back, which is the same statement the schema makes. The absence half is
// not lost — it rides on the node for a backend whose language cannot express
// it in the zero value, and goType is not that backend's only input.
func goType(n schemaNode, typeName string, vocab map[string]string) string {
	if n.GoType != "" {
		return rewriteType(n.GoType, vocab)
	}
	if n.Ref != "" {
		return refTypeName(n.Ref)
	}
	switch n.valueType() {
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

// nestedTypeName returns the type name given to an object a property declares
// inline. Such an object has no name of its own — the schema states its shape in
// place rather than through a `$defs` entry — so the name is derived from the
// path that reaches it: the declaring type's name, then the field's.
//
// It is one function rather than an expression at each site because the name is
// asked for from two directions that have to agree: the emission that DECLARES
// the type, and the field resolution that REFERENCES it. Two derivations of one
// name disagree in silence — each half stays self-consistent, so the generator
// emits a file that declares one type and names another, and nothing in it
// notices.
func nestedTypeName(declaringType, field string) string {
	return declaringType + field
}

// declaresInlineObject reports whether a property node is an object the schema
// states in place: it carries the properties a declaration is built from, and
// names no type of its own through a `$ref` or an x-aggrega-go-type annotation.
//
// Those two exclusions are what hold it to the field resolution, which reads the
// annotation first and the `$ref` second and reaches the shape only when neither
// is there. A node the resolution names through a `$ref` is declared by the def
// that `$ref` points at, so a second declaration under the derived name would be
// one nothing references.
func declaresInlineObject(n schemaNode) bool {
	return n.GoType == "" && n.Ref == "" && n.valueType() == "object" && len(n.props) > 0
}

// inlineObjectDefs returns the objects a declared type states inline, each under
// the name the field resolution gives it, breadth-first in declaration order.
//
// A backend owes a declaration for every one of them: a field typed as an inline
// object names a type, and a file naming a type it does not declare is refused
// by the destination language at a line in generated code that says nothing
// about which schema node produced it.
func inlineObjectDefs(typeName string, n schemaNode) []namedNode {
	var out []namedNode
	queue := []namedNode{{name: typeName, node: n}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, p := range cur.node.props {
			if !declaresInlineObject(p.node) {
				continue
			}
			nested := namedNode{name: nestedTypeName(cur.name, pascal(p.name)), node: p.node}
			out = append(out, nested)
			queue = append(queue, nested)
		}
	}
	return out
}

// expandInlineObjects returns the types a backend declares for a `$defs`-shaped
// schema: each entry, followed by the objects that entry states inline. A
// backend iterates this instead of the entries alone, so the set it declares is
// the set its own fields reference.
//
// The Go backend does not read it, and the asymmetry is structural rather than
// an omission: its declaration emission already queues an inline object as it
// walks a struct's properties, because that emission is shared with the
// single-type entity path, where a schema states most of its shape inline.
// Handing it an expanded list would declare each such type twice.
func expandInlineObjects(defs []namedNode) []namedNode {
	out := make([]namedNode, 0, len(defs))
	for _, d := range defs {
		out = append(out, d)
		out = append(out, inlineObjectDefs(d.name, d.node)...)
	}
	return out
}

// checkInlineObjectNames refuses a schema whose declarations would not all carry
// distinct names. An inline object's name is derived from the path that reaches
// it, so a `$defs` entry may already carry that name and two paths may derive
// one name — and either way the emitted file declares one type twice. The
// destination language refuses that at a line in generated code that does not
// say which schema nodes collided, so both are named here instead.
func checkInlineObjectNames(schemaFile string, defs []namedNode) error {
	declaredBy := map[string]string{}
	for _, d := range defs {
		declaredBy[d.name] = "the $defs entry " + d.name
	}
	for _, d := range defs {
		for _, nested := range inlineObjectDefs(d.name, d.node) {
			where := "the object " + d.name + " declares inline"
			if prior, ok := declaredBy[nested.name]; ok {
				return fmt.Errorf("%s: two declarations would both be named %s — %s, and %s. "+
					"Rename one of the properties, or give one of them an x-aggrega-go-type annotation "+
					"naming the type it carries", schemaFile, nested.name, prior, where)
			}
			declaredBy[nested.name] = where
		}
	}
	return nil
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
//
// It reads the node's shape, which is what keeps a nullable spelling inside the
// refusal's reach: an object-or-null with no properties is an object with no
// properties, and a check that stopped seeing it would be worse than one that
// fails, because the node would then be typed as opaque with nothing saying so.
func checkFieldNode(schemaFile, path string, n schemaNode) error {
	if n.GoType != "" || n.Ref != "" {
		return nil
	}
	switch n.valueType() {
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
	switch n.valueType() {
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

// structValued reports whether an emitted Go type is one the encoder holds as a
// struct value. It decides which non-required fields carry omitzero, and it is
// written as an exclusion of the non-struct spellings the generator can produce
// rather than as a list of struct spellings: the struct side is open (every
// nested object becomes a named type, and an x-aggrega-go-type annotation may
// name any type at all), while the non-struct side is closed and short.
//
// The excluded spellings are the mechanical scalar mappings (string, bool, int,
// any), the array recursion's slice, a pointer, a plain map, and the opaque
// byte-slice spelling an opaque host type maps to.
//
// An unrecognized spelling is treated as a struct, which is the safe direction:
// omitzero is never LESS omitting than omitempty for any kind. The two agree on
// scalars and pointers, and on slices and maps their union is what omitempty
// already does alone — so a wrong answer here changes emitted bytes only for a
// value that is genuinely a struct.
func structValued(gt string) bool {
	switch {
	case gt == "",
		strings.HasPrefix(gt, "*"),
		strings.HasPrefix(gt, "[]"),
		strings.HasPrefix(gt, "map["):
		return false
	}
	switch gt {
	case "string", "bool", "int", "any", "json.RawMessage":
		return false
	}
	return true
}

// emitStruct writes one struct declaration. typeName seeds nested named types
// (<parent><Field>). Returns the nested object structs it discovered, to emit
// after; records package selectors used by field types into `used`. A non-nil
// vocab rewrites x-aggrega-go-type field values per the target's vocabulary.
//
// codecBacked says the emitted type carries its own generated MarshalJSON, so
// its struct tag is documentation of what that codec does rather than the thing
// that decides it. It governs omitzero: see the tag construction below.
func emitStruct(b *strings.Builder, typeName, doc string, n schemaNode, used map[string]bool, vocab map[string]string, cbor, codecBacked bool) []pending {
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
		nestedName := nestedTypeName(typeName, field)
		gt := goType(p.node, nestedName, vocab)
		collectSelectors(gt, used)
		if declaresInlineObject(p.node) {
			nested = append(nested, pending{name: nestedName, doc: p.node.Description, node: p.node})
		}
		jsonTag, yamlTag := p.name, p.name
		if !required[p.name] {
			jsonTag += ",omitempty"
			yamlTag += ",omitempty"
			// encoding/json's omitempty is a no-op on a struct VALUE, so a
			// non-required struct-valued field emits `{}` for an author who
			// wrote nothing at all. omitzero (Go 1.24+, via IsZero or the zero
			// value) is the load-bearing option that omits it. The YAML tag
			// needs no counterpart: that encoder already treats an all-zero
			// struct as empty under plain omitempty, which is why only the JSON
			// tag carries it.
			//
			// It is withheld from a codec-backed type, and the reason is that
			// the tag means a different thing there. On a type the standard
			// library encodes reflectively the tag IS the encoder, so it must
			// state what should happen. On a type carrying its own generated
			// MarshalJSON the tag is inert documentation of what that codec
			// does — and the codec cannot omit a zero struct, because a struct
			// holding a slice is not comparable and the emission guard has no
			// zero test to fall back on. Claiming omitzero there would put an
			// omission in the tag that the code beside it never performs.
			//
			// An order-preserving container is the one struct value that guard
			// DOES omit — through the very IsZero method omitzero dispatches to
			// — so a codec-backed type keeps omitzero on exactly those fields.
			// The classification comes from orderedMapValueType rather than from
			// a prefix test written here, because that function is the single
			// home of the spelling and the emission guard already asks it: a
			// second copy here would recognise only the bare spelling and
			// silently under-claim on every codec emitted outside the package
			// that declares the container.
			omitzero := structValued(gt)
			if codecBacked {
				_, omitzero = orderedMapValueType(gt)
			}
			if omitzero {
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
			// generated codec omits. omitzero has no cbor analog, and an
			// order-keeping container is a struct whose entries a reflective
			// encoder cannot see, so a reference encoder reaches such a field only
			// through a view that presents the container as its list of entries —
			// where omitempty omits an empty list, as the generated codec omits
			// an empty container.
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
	//
	// It is empty wherever the node declares no single shape: a node carrying no
	// `type` keyword, and equally one declaring a union of genuinely different
	// shapes, since such a union names no one kind the fallback could decide on.
	schemaType string
	// nullable says the schema declares this value as sometimes sent as nothing
	// — the `null` spelled alongside a single type name. It is carried beside
	// the shape rather than folded into it because the two land differently per
	// language: Go's zero value already carries absence, so the Go and Rust
	// emissions read the shape alone, while a language whose array and object
	// types admit no null must state the absence in the published type. Left
	// unstated there, a consumer trusting the type meets the null at run time
	// with nothing at compile time to have warned them.
	nullable bool
	// hostOpaqueType is the destination package's own declared Go type for an
	// opaque field, when that differs from the opaque spelling the annotation
	// carries. Set only for a codecOnly target, from its opaqueHostTypes table.
	//
	// Its presence says two things at once, and both matter: encode and decode take
	// the opaque arm whatever the annotation resolved to, and the omission decision
	// is asked of THIS type rather than of the annotation's. Empty everywhere else,
	// so no target that realizes its own declarations is affected.
	hostOpaqueType string
}

// resolveCodecFields resolves a def node's properties into codec field
// descriptors, in declared order, applying the target vocabulary to
// x-aggrega-go-type values and resolving $refs to def type names. It mirrors
// emitStruct's field-type resolution so the codec types match the emitted
// struct fields exactly.
//
// typeName is the name of the type whose properties these are, and it is a
// parameter rather than a detail of the walk because an object a property
// declares inline is named after the type that declares it. Resolving such a
// field without that name yields a different name from the one the declaration
// carries, so the emitted file references a type nothing declares — which is why
// the name comes from nestedTypeName here and at the declaration alike.
func resolveCodecFields(typeName string, n schemaNode, vocab map[string]string) []codecField {
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
			goType:     goType(p.node, nestedTypeName(typeName, field), vocab),
			required:   required[p.name],
			desc:       p.node.Description,
			schemaType: p.node.valueType(),
			nullable:   p.node.nullable(),
		})
	}
	return fields
}

// emitCodec writes a dispatch-free streaming JSON codec — MarshalJSON,
// UnmarshalJSON and UnmarshalJSONStrict — for one def struct type. Composite
// fields (named structs, slices of named structs, maps) are walked by generated
// code, and each nested def value is decoded by a direct call to that type's own
// generated UnmarshalJSON rather than a json.Decoder.Decode dispatch — so no
// stdlib interface-dispatch through reflect remains on the decode path. Scalar
// leaf fields are decoded by hand from a single json.Token. The generated code
// references no `reflect` symbol — the dispatch-free guarantee is by
// construction.
//
// defTypes names the generated def struct types, so the emitter can tell a
// nested generated type (direct UnmarshalJSON call) from a scalar or an
// opaque passthrough.
func emitCodec(b *strings.Builder, typeName string, n schemaNode, vocab map[string]string, defTypes map[string]bool) {
	fields := resolveCodecFields(typeName, n, vocab)
	emitMarshal(b, typeName, fields, defTypes)
	emitUnmarshal(b, typeName, fields, defTypes)
	emitUnmarshalStrict(b, typeName, fields)
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
	case isOrderedMapSpelling(goType):
		// An order-preserving container writes its own JSON object, in the order
		// it holds, through a direct call — the container's codec is the one place
		// its order is written, and the call keeps the encode dispatch-free.
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

// emitUnmarshalStrict writes T.UnmarshalJSONStrict: the same decode as
// UnmarshalJSON, preceded by a pass that refuses a top-level key the type's
// shape does not declare instead of skipping it. The declared key set is written
// from the same resolved field slice that drives UnmarshalJSON's case arms, so
// the two cannot disagree about what the shape declares — adding a schema field
// moves both at once.
//
// Two entry points rather than one strict decoder: UnmarshalJSON is what every
// round-trip, golden vector and cross-language parity fixture reads, and a
// payload carrying a field the reader does not know is a normal forward-compatible
// read there. The strict entry point exists for the narrower case where the
// payload's identity is carried by its keys — a plugin's output read at a socket —
// and where skipping an unknown key produces an empty result that no consumer can
// tell from a real one.
func emitUnmarshalStrict(b *strings.Builder, typeName string, fields []codecField) {
	fmt.Fprintf(b, "// UnmarshalJSONStrict decodes %s exactly as UnmarshalJSON does, except\n", typeName)
	fmt.Fprintf(b, "// that a top-level key this shape does not declare is refused rather than\n")
	fmt.Fprintf(b, "// skipped, naming the offending key. It is the entry point for a payload\n")
	fmt.Fprintf(b, "// whose identity is carried by the keys it holds, where skipping an unknown\n")
	fmt.Fprintf(b, "// key would yield an empty value indistinguishable from a real one. The\n")
	fmt.Fprintf(b, "// check reads the object's own keys and does not descend.\n")
	fmt.Fprintf(b, "func (t *%s) UnmarshalJSONStrict(data []byte) error {\n", typeName)
	b.WriteString("\tif err := refuseUndeclaredJSONKeys(data")
	for _, f := range fields {
		fmt.Fprintf(b, ", %q", f.key)
	}
	b.WriteString("); err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\treturn t.UnmarshalJSON(data)\n")
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
	case defTypes[goType], isOrderedMapSpelling(goType):
		// A generated def type, or an order-preserving container, which reads its
		// own JSON object in member order and refuses a repeated key.
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

	b.WriteString("// refuseUndeclaredJSONKeys walks the top-level keys of a JSON object and\n")
	b.WriteString("// reports the first one the declared key set does not contain. It reads only\n")
	b.WriteString("// the object's own keys — it never descends into a value — so it is the shape\n")
	b.WriteString("// check for a payload whose identity is carried by which keys it holds.\n")
	b.WriteString("func refuseUndeclaredJSONKeys(data []byte, declared ...string) error {\n")
	b.WriteString("\tdec := json.NewDecoder(bytes.NewReader(data))\n")
	b.WriteString("\tif err := expectDelim(dec, '{'); err != nil {\n\t\treturn err\n\t}\n")
	b.WriteString("\tfor dec.More() {\n")
	b.WriteString("\t\tkey, err := readKey(dec)\n")
	b.WriteString("\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	b.WriteString("\t\tfound := false\n")
	b.WriteString("\t\tfor _, d := range declared {\n")
	b.WriteString("\t\t\tif key == d {\n\t\t\t\tfound = true\n\t\t\t\tbreak\n\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif !found {\n")
	b.WriteString("\t\t\treturn fmt.Errorf(\"abi: unexpected field %q\", key)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif _, err := readRaw(dec, data); err != nil {\n\t\t\treturn err\n\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn expectDelim(dec, '}')\n")
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
	emitQueue(&body, queue, used, nil, false, false)

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
		// A name a backend would declare twice is refused here for the same
		// reason and in the same place: every backend derives the name the same
		// way, so a collision is a property of the schema rather than of one
		// emission.
		if err := checkInlineObjectNames(t.schemaFile, root.defs); err != nil {
			return nil, err
		}
		// The order-preserving form is keyed on the schema's marking, so no
		// target's vocabulary may drop that marking from a field it rewrites.
		if err := checkVocabularyKeepsOrderedContainers(t, targetLabel(t)); err != nil {
			return nil, err
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
	case "go-ordered-map":
		return generateGoOrderedMap(t)
	case "rust-ordered-map":
		return generateRustOrderedMap(t)
	case "ts-ordered-map":
		return generateTSOrderedMap(t)
	case "go-wire-registry":
		return generateWireRegistry(t)
	default:
		return nil, fmt.Errorf("typegen: unknown codec target language %q", t.lang)
	}
}

// targetLabel names a schema-backed codec target in a refusal by what identifies
// it: the language it emits, the destination package where it has one, and the
// schema it is generated from. A target does not carry its own output name, and
// these three are what tell two targets of one schema apart.
func targetLabel(t codecTarget) string {
	lang := map[string]string{"": "Go", "go": "Go", "rust": "Rust", "ts": "TypeScript"}[t.lang]
	if lang == "" {
		lang = t.lang
	}
	if lang == "Go" && t.pkg != "" {
		return fmt.Sprintf("the Go codec target for package %s generated from %s", t.pkg, t.schemaFile)
	}
	return fmt.Sprintf("the %s codec target generated from %s", lang, t.schemaFile)
}

// checkOpaqueHostTypes refuses a codecOnly target that carries an opaque field
// the target names no host type for.
//
// It refuses rather than falling back because the fallback is silent and wrong:
// the omission decision would be asked of the opaque spelling, so an
// order-preserving container would be guarded as though it were a byte slice —
// which either fails to compile or, where it happens to compile, contradicts the
// omitempty the declaration promises. Listing the field is one line; discovering
// that omission is wrong on one end of a wire is not.
//
// A target that emits its own declarations is exempt by construction: it realizes
// the annotation itself, so the two cannot disagree.
func checkOpaqueHostTypes(t codecTarget, root schemaNode) error {
	for _, d := range root.defs {
		if !t.emitsDef(d.name) {
			continue
		}
		for _, p := range d.node.props {
			if goType(p.node, nestedTypeName(d.name, pascal(p.name)), t.vocabulary) != "json.RawMessage" {
				continue
			}
			if _, ok := t.opaqueHostTypes[d.name+"."+p.name]; !ok {
				return fmt.Errorf(
					"schema %s: %s.%s carries the opaque wire spelling and this codec emits no declaration for it, "+
						"so the type it is declared as is not derivable here: name it in the target's opaqueHostTypes",
					t.schemaFile, d.name, p.name)
			}
		}
	}
	return nil
}

// checkSkippedDefsUnreferenced refuses a target that skips a $def another emitted
// def references.
//
// The reference is the whole hazard. A skipped def is left out of the generated
// def-type set, so a field typed as one no longer dispatches to a generated codec —
// it falls to the opaque arm and rides as a byte-string of JSON bytes, while the
// counterpart, which emits every def, encodes the same field structurally. Neither
// side fails: each round-trips against itself, and the wire is undecodable in both
// directions. That is precisely the failure a generated codec exists to make
// impossible, so it is refused at generation rather than left to a wire test that
// may not exist yet.
//
// A stale entry — one naming a def the schema no longer declares — is looked up by
// nothing here and is caught by the registry gate, which reads the table in the
// direction a lookup cannot.
func checkSkippedDefsUnreferenced(t codecTarget, root schemaNode) error {
	if len(t.skipDefs) == 0 {
		return nil
	}
	skipped := map[string]bool{}
	for _, name := range t.skipDefs {
		skipped[name] = true
	}
	for _, d := range root.defs {
		if !t.emitsDef(d.name) {
			continue
		}
		for _, p := range d.node.props {
			resolved := goType(p.node, nestedTypeName(d.name, pascal(p.name)), t.vocabulary)
			for _, tok := range typeTokens(resolved) {
				if skipped[tok] {
					return fmt.Errorf(
						"schema %s: %s.%s resolves to %s, which names the skipped def %s: a field typed as a "+
							"skipped def falls to the opaque arm and rides as a byte-string of JSON bytes, while "+
							"the counterpart encodes it structurally — either emit %s here or carry the field "+
							"opaquely on both ends",
						t.schemaFile, d.name, p.name, resolved, tok, tok)
				}
			}
		}
	}
	return nil
}

// typeTokens splits a resolved Go type spelling into its identifier tokens, so a
// type name can be recognised wherever a composite buried it — a slice of it, a
// map value, an order-preserving container, a pointer. Splitting on the
// non-identifier characters is what keeps the match a whole name: a substring test
// would report PolicyOutput inside PolicyOutputEnvelope.
func typeTokens(spelling string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range spelling {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

// generateDefsGo produces the formatted Go source for a multi-type codec
// package: one Go type per `$defs` entry (in declared order) plus, per type, a
// reflect-free streaming JSON codec and its canonical-CBOR twin. The top-level
// `oneOf` wire-discriminator is not a Go type and is ignored — the walk reads
// `$defs` only and never the root's oneOf/type. The target's vocabulary rewrites
// x-aggrega-go-type values.
//
// A codecOnly target emits the CBOR codec alone, attaching it to declarations the
// destination package already carries by hand. What that leaves out — the
// declarations and the JSON codec — and why the two travel together is on the
// codecTarget field.
func generateDefsGo(t codecTarget) ([]byte, error) {
	root, err := decodeNode(json.RawMessage(t.schema))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	if len(root.defs) == 0 {
		return nil, fmt.Errorf("schema %s has no $defs to emit", t.schemaFile)
	}
	if t.codecOnly {
		if err := checkOpaqueHostTypes(t, root); err != nil {
			return nil, err
		}
	}
	if err := checkSkippedDefsUnreferenced(t, root); err != nil {
		return nil, err
	}

	var body strings.Builder
	used := map[string]bool{}
	// One named type per $def, in schema declaration order. Each def is an
	// object; emitStruct queues any anonymous nested objects breadth-first,
	// though the $defs-shaped schema names every composite via a $ref so the
	// nested-object queue stays empty in practice.
	//
	// A codecOnly target skips this entirely: its destination package declares
	// every type by hand, and the codec below attaches methods to those
	// declarations. Emitting a second declaration would not merely duplicate them,
	// it would not compile.
	if !t.codecOnly {
		var queue []pending
		for _, d := range root.defs {
			if !t.emitsDef(d.name) {
				continue
			}
			queue = append(queue, pending{name: d.name, doc: d.node.Description, node: d.node})
		}
		emitQueue(&body, queue, used, t.vocabulary, t.emitsCBOR(), true)
	}

	// defTypes names the generated def struct types, so the codec emitter can
	// tell a nested generated type — decoded by a direct call to its own
	// UnmarshalJSON — from a scalar or an opaque passthrough. It also includes
	// externalDefTypes: types emitted by another codec target into the same
	// package, so a cross-codec in-package field (the policy graph's IRGraph)
	// dispatches directly to its generated codec rather than through json.Marshal;
	// and crossPackageDefTypes, the same idea across a package boundary, which the
	// emitter tells apart by the selector in the spelling.
	//
	// A skipped def is left OUT: nothing this target emits may reference it, which
	// checkSkippedDefsUnreferenced has already established, so listing it would only
	// let a later change dispatch to a codec that is not there.
	defTypes := map[string]bool{}
	for _, d := range root.defs {
		if !t.emitsDef(d.name) {
			continue
		}
		defTypes[d.name] = true
	}
	for _, ext := range t.externalDefTypes {
		defTypes[ext] = true
	}
	for _, qualified := range t.crossPackageDefTypes {
		defTypes[qualified] = true
	}

	// The dispatch-free streaming codec for each emitted def type, in the same
	// declared order. Codec emission descends into each field's composite shape
	// (map/struct/slice) and decodes each nested def value by a direct call to
	// its own UnmarshalJSON, so no stdlib reflective struct-walk or
	// interface-dispatch remains on the decode path.
	//
	// Skipped for a codecOnly target: attaching MarshalJSON/UnmarshalJSON to
	// hand-written declarations replaces however that package is already
	// marshalled, which is a change to existing bytes rather than a new codec.
	if !t.codecOnly {
		for _, d := range root.defs {
			if !t.emitsDef(d.name) {
				continue
			}
			emitCodec(&body, d.name, d.node, t.vocabulary, defTypes)
		}
	}
	// The canonical-CBOR codec, parallel to the JSON codec above. Emitted per def
	// type in the same declared order; the shared CBOR helpers land in the
	// helpers-bearing file of the destination package. For a codecOnly target this
	// is the whole emission.
	if t.emitsCBOR() {
		for _, d := range root.defs {
			if !t.emitsDef(d.name) {
				continue
			}
			emitCBORCodec(&body, d.name, d.node, t.vocabulary, defTypes, t.opaqueHostTypes)
		}
	}
	// The JSON helpers serve the JSON codec, so they follow it out of a codecOnly
	// target rather than sitting there unreferenced.
	if t.helpers && !t.codecOnly {
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
func emitQueue(body *strings.Builder, queue []pending, used map[string]bool, vocab map[string]string, cbor, codecBacked bool) {
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nested := emitStruct(body, cur.name, cur.doc, cur.node, used, vocab, cbor, codecBacked)
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
