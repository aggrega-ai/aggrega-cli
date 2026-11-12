// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"reflect"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

//go:generate go run ../../internal/tools/typegen plugin_gen.go
//go:generate go run ../../internal/tools/typegen plugin_enums_gen.go

// Plugin's top-level struct declarations (Plugin,
// PluginMetadata, PluginSpec) are generated into
// plugin_gen.go from the published plugin-v1beta1 schema, as are the provider
// block and everything under it (the block itself, its render-behavior
// contract, and that contract's input-kind claim); their
// godoc is the schema's descriptions. This hand-written companion holds
// the optional nested blocks the generator names but does not
// descend into (PluginSignature, PluginRuntime, the three per-plugin-type
// blocks PluginPipelineStep, PluginParser and PluginImporter, the importer
// block's PluginDetectSignature and PluginPathMatch, and the contract's nested
// PluginArtifactDelimiters, PluginAssemblyNameAnnotation and
// PluginSourceIgnore),
// plus the Plugin methods and the aliases below. The shared Maintainer and
// WellKnownAnnotation types the generated fields reference live in common.go.
//
// The generator derives a nested type's name from its path through the schema,
// so the provider chain is declared as PluginSpecProvider,
// PluginSpecProviderContract and PluginSpecProviderContractInputKinds. The
// three names this package published before those declarations were generated —
// PluginProvider, ProviderContract and PluginInputKinds — survive as aliases,
// which preserve both the name and the type identity, so an external consumer
// naming one keeps compiling and a reflective comparison against one still sees
// the same type.
//
// The runtime block stays field-aligned with the loader's plugin input
// (max-memory pages, per-call timeout, the machine-derived host-function
// record, the config key/value map) so a catalog-loaded entity maps to the
// loader's runtime shape field-for-field, no field reinvention. The loader's
// network-firewall input has no authoring counterpart here: a plugin
// never reaches the network at build time, so there is no host-allowlist
// field to author. The loader identity inputs (the plugin's diagnostic
// name and resolved artifact path) are resolution internals supplied by
// upstream layers, not entity authoring fields.
//
// PluginSignature and PluginRuntime are referenced by pointer (named via
// x-aggrega-go-type on the schema's object nodes) so an entity that omits them
// round-trips without emitting an empty block. The provider block needs no
// pointer for that: it is held by value and omitted by its omitzero tag. The
// two encoders differ here and only one ever needed the help — the YAML encoder
// treats an all-zero struct as empty under plain omitempty, so it never emitted
// the block either way, while encoding/json's omitempty is a no-op on a struct
// value and omitzero is what suppresses it there.

// PluginCoordinate is the single rule deriving a plugin's identity COORDINATE
// from its pluginType and bare name: `<pluginType>/<name>`. It preserves case —
// it is the cased identity the catalog indexes under, the lockfile pins, and a
// consuming URI addresses. A plugin's bare name is unique only within its socket,
// so the pluginType segment is what distinguishes two same-named plugins in
// different sockets; the coordinate is always derived from the (pluginType, name)
// pair, never re-parsed from a pre-joined string. The physical OCI location a
// fetch derives from this coordinate lowercases every segment; the coordinate
// itself is the identity and stays cased.
//
// It lives here (not a higher package) so every layer — the catalog index, the
// closure descent, the artifact-reference derivation — shares the one rule
// without an import cycle.
func PluginCoordinate(pluginType, name string) string {
	return pluginType + "/" + name
}

// Coordinate returns the Plugin entity's identity coordinate, `<pluginType>/<name>`,
// derived through the single PluginCoordinate rule.
func (p Plugin) Coordinate() string {
	return PluginCoordinate(p.Metadata.PluginType, p.Metadata.Name)
}

// ArtifactReference returns the .wasm artifact reference a Plugin entity resolves
// to: its identity coordinate, the derived `<pluginType>/<name>` path (via
// Coordinate). The .wasm rides inline in the entity's single OCI manifest, so the
// artifact and the entity share one identity; the entity carries no separate
// artifact field. A physical fetch location is derived from this identity at the
// fetch boundary — the embed tier keys on the coordinate directly (offline,
// registry-independent), and the network tier binds it to <registry>/<location>
// through the configured registry — never stored in the entity.
//
// It is the single home of the entity to artifact-reference derivation: every
// read-site that resolves a Plugin entity to its .wasm funnels through here, so
// the rule lives in exactly one place and cannot drift across the load, lock,
// verify, and display surfaces.
func (p Plugin) ArtifactReference() string {
	return p.Coordinate()
}

// PluginProvider is the provider-socket block on a pluginType: provider
// Plugin. It carries the render-engine capabilities the provider serves, how
// the plugin's rendering engine translates the Workload kind into
// provider-native manifests, which platform-native annotations the engine
// reads, and the engine's render-behavior contract. It carries no meaning on
// any other plugin type.
//
// It is an alias of the generated PluginSpecProvider rather than a declaration
// of its own: an alias preserves the name AND the type identity, so this name
// stays usable by an external consumer and by a reflective comparison while the
// shape itself is single-sourced at the schema. The generated block is a value
// rather than a pointer; a plugin type that carries no provider block leaves it
// zero, and the field's omitzero tag keeps it out of the serialized entity.
type PluginProvider = PluginSpecProvider

// PluginPipelineStep is the pipeline-step block on a pluginType: pipeline-step
// Plugin. It carries the provider capabilities the plugin's step is compatible
// with, and carries no meaning on any other plugin type. The block is
// hand-written because the generator names it via x-aggrega-go-type on the
// schema's object node and does not descend into it.
type PluginPipelineStep struct {
	// Capabilities is the provider capabilities this plugin's pipeline step is
	// compatible with — the provider engine names, such as "helm" or "compose",
	// whose rendered output the step knows how to transform. A step is written
	// against one provider's output shape at a time, so the list is an explicit,
	// tested-compatibility claim. Entries preserve author order. It is the
	// catalog-readable record of that claim; the compatibility check a pipeline
	// walk applies reads the same claim from the plugin's own describe()
	// self-report.
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// PluginParser is the parser block on a pluginType: parser Plugin. It carries
// the source formats the plugin turns into a canonical structured tree, and
// carries no meaning on any other plugin type. The block is hand-written
// because the generator names it via x-aggrega-go-type on the schema's object
// node and does not descend into it.
type PluginParser struct {
	// Capabilities is the source formats this parser turns into a canonical
	// structured tree, each written as "parse:<format>" — for example
	// "parse:yaml". The prefix is part of the entry, so a plugin handling
	// several formats carries one entry per format. Entries preserve author
	// order. Format resolution matches a requested format against these
	// entries.
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// PluginImporter is the importer block on a pluginType: importer Plugin. It
// carries the foreign source formats the plugin turns into draft authoring
// files and the signatures a source of those formats is recognised by, and
// carries no meaning on any other plugin type. The block is hand-written
// because the generator names it via x-aggrega-go-type on the schema's object
// node and does not descend into it.
type PluginImporter struct {
	// Capabilities is the foreign source formats this importer turns into draft
	// Aggrega authoring files, each written as "import:<format>" — for example
	// "import:terraform". The prefix is part of the entry, so a plugin handling
	// several formats carries one entry per format. Entries preserve author
	// order. Format resolution matches a requested format against these
	// entries.
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`

	// Detect is the machine-owned record of the signatures a source of this
	// importer's formats is recognised by when an import names no format, in the
	// order the plugin's describe() self-report declares them. It is derived from
	// that self-report when the entity is generated from the plugin, and the load
	// and publication compare it with the self-report where it is present. A nil
	// list records nothing and is compared with nothing.
	Detect []PluginDetectSignature `json:"detect,omitempty" yaml:"detect,omitempty"`
}

// PluginDetectSignature is one way a source of an importer's formats is
// recognised: the syntactic format the source is parsed as, the file-name
// patterns the signature applies to, and the predicates the parsed tree must
// satisfy. The element type is hand-written because the generator names it via
// x-aggrega-go-type on the schema's items node and does not descend into it.
type PluginDetectSignature struct {
	// Syntax is the syntactic format the source is parsed as — the format a
	// parser plugin advertises as "parse:<syntax>".
	Syntax string `json:"syntax" yaml:"syntax"`
	// Files is the file-name patterns the signature applies to, matched against
	// the end of the source's name. Empty applies it to a source that is one
	// file, whatever it is called, and to no file of a directory. The patterns of
	// an importer's signatures are also what decide which files of a directory an
	// import hands it.
	Files []string `json:"files,omitempty" yaml:"files,omitempty"`
	// Match is the predicates the parsed tree must satisfy, all of them, in
	// declared order.
	Match []PluginPathMatch `json:"match,omitempty" yaml:"match,omitempty"`
}

// PluginPathMatch is one predicate over a parsed tree: the value a dotted path
// reaches, the kind of value it must be — one of the Is* consts — and an
// optional string it must equal or begin with. The element type is hand-written
// because the generator names it via x-aggrega-go-type on the schema's items
// node and does not descend into it.
type PluginPathMatch struct {
	// Path is where in the tree the predicate looks, keys joined by dots from the
	// top of the tree; a whole-number segment selects an array position.
	Path string `json:"path" yaml:"path"`
	// Is is what the path must reach: IsPresent, IsAbsent, or one of the five
	// value kinds.
	Is string `json:"is" yaml:"is"`
	// Equals is a string the reached value must equal exactly. Empty asks for no
	// comparison.
	Equals string `json:"equals,omitempty" yaml:"equals,omitempty"`
	// Prefix is a string the reached value must begin with. Empty asks for no
	// comparison.
	Prefix string `json:"prefix,omitempty" yaml:"prefix,omitempty"`
}

// DeclaredCapabilities returns the capabilities this Plugin declares, read from
// the one per-plugin-type block its own metadata.pluginType gives meaning to. A
// plugin type that is not resolved by capability — a linter, a governance or a
// CLI plugin — declares none, and so does a plugin whose block is absent; both
// return an empty list.
//
// It exists because a capability entry means a different thing per plugin type:
// a provider's entry names a render engine it serves, a pipeline step's names a
// provider it is compatible with, and a parser's or importer's names a source
// format it handles. Containment is what carries that difference — the list is
// reachable only through the block that fixes its sense, so neither an author
// nor a reader has to know a convention to tell one sense from another. The
// consequence for a consumer is that it must not read a block directly: it asks
// here, and the plugin's declared type selects the block.
func (p Plugin) DeclaredCapabilities() []string {
	if slot := p.Spec.capabilitiesSlot(p.Metadata.PluginType, false); slot != nil {
		return *slot
	}
	return nil
}

// SetCapabilitiesFor records capabilities in the one per-plugin-type block
// pluginType gives meaning to, replacing whatever that block carried. A plugin
// type that is not resolved by capability accepts none, and the call is a no-op.
//
// An empty list against an absent block leaves that block absent from the
// serialized entity, so an entity never gains a block its author did not write
// and re-serializes byte-identically. An empty list against a block that is
// already present clears the list and leaves the block, so a machine-derived
// write always overrides an authored claim rather than letting a stale one
// survive. Both spellings of an empty list — a nil slice, and an allocated
// slice of length zero — mean the same thing here and produce the same bytes,
// so a caller never has to know which of the two it is holding.
//
// Absent means two different things underneath and the difference is not
// observable in the output: a block held by pointer is absent by being nil,
// while the provider block is held by value and is absent by being zero, which
// its omitzero tag keeps out of the serialized entity.
func (s *PluginSpec) SetCapabilitiesFor(pluginType string, capabilities []string) {
	slot := s.capabilitiesSlot(pluginType, len(capabilities) > 0)
	if slot == nil {
		return
	}
	if len(capabilities) == 0 {
		// Normalising to nil is what keeps the value-held provider block zero.
		// An allocated slice of length zero is a non-zero field, and a non-zero
		// block is precisely what omitzero cannot suppress — storing one would
		// put a block into the serialized entity that no author wrote.
		capabilities = nil
	}
	*slot = capabilities
}

// capabilitiesSlot is the single home of the plugin-type-to-block rule: it
// resolves the one block that holds a plugin type's capability list and returns
// the address of that list, or nil when the type holds no capabilities and none
// is to be created. Every read and every write funnels through it, so the rule
// cannot drift across the resolution, display, publish and synthesis surfaces —
// a second copy that agreed today is exactly what would hide a divergence when a
// new plugin type gains a block.
//
// allocate separates a write from a read on a block held by pointer. A write
// creates the block when the entity carries none; a read leaves the entity
// untouched and simply misses, because a missing block means the plugin declares
// nothing and materialising an empty one would make the entity re-serialize with
// a block nobody wrote.
//
// It carries no meaning for the provider block, which is held by value: there is
// no allocation to make and nothing a read could materialise, because a zero
// block is what absent looks like and its omitzero tag keeps it out of the
// serialized entity either way. So that arm returns the address unconditionally,
// and a read of a plugin carrying no provider block yields an empty list exactly
// as a miss did.
//
// Returning that address unconditionally is safe only because the write side
// normalises an empty list to nil before storing it. Storing an allocated slice
// of length zero through this address would leave the block non-zero, and a
// non-zero block is what omitzero stops suppressing — so the two sides are one
// mechanism, and removing the normalisation would put an empty block back into
// the serialized entity without changing a line here.
func (s *PluginSpec) capabilitiesSlot(pluginType string, allocate bool) *[]string {
	switch plugintype.Type(pluginType) {
	case plugintype.Provider:
		return &s.Provider.Capabilities
	case plugintype.PipelineStep:
		if s.PipelineStep == nil {
			if !allocate {
				return nil
			}
			s.PipelineStep = &PluginPipelineStep{}
		}
		return &s.PipelineStep.Capabilities
	case plugintype.Parser:
		if s.Parser == nil {
			if !allocate {
				return nil
			}
			s.Parser = &PluginParser{}
		}
		return &s.Parser.Capabilities
	case plugintype.Importer:
		if s.Importer == nil {
			if !allocate {
				return nil
			}
			s.Importer = &PluginImporter{}
		}
		return &s.Importer.Capabilities
	}
	return nil
}

// PluginExport is one export the plugin offers, with its declared
// capability. An export is a named entry point the host dispatches to (for
// example "render_node", "lint_naming", "step"); the capability fields are the
// authored claim of what the export does, which the host cross-checks against
// the plugin's self-report at publish and at load. Entries preserve author
// order. The element type is hand-written because the generator names it via
// x-aggrega-go-type on the schema's items node and does not descend into it.
type PluginExport struct {
	// Name is the export's ABI name — the name the host dispatches to (for
	// example "render_node", "lint_naming", "step").
	Name string `json:"name" yaml:"name"`
	// Operates declares the pipeline operation this export performs: "patch"
	// (modify an existing rendered document) or "emit" (add a new document).
	// Empty carries no operation claim.
	Operates string `json:"operates,omitempty" yaml:"operates,omitempty"`
	// Scope declares the document root this export operates on: "own" (the
	// node's own rendered documents) or "host" (the documents of the component a
	// component-scope FeatureRecipe is attached to). Empty carries no scope claim.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`
	// Selector is a free-form, human-readable description of what the export
	// acts on (for example "workloads carrying a pod template"). Documentation
	// for catalog readers; the compiler does not interpret it.
	Selector string `json:"selector,omitempty" yaml:"selector,omitempty"`
}

// PluginCommand is one top-level aggrega <command> verb a CLI plugin
// contributes, in the machine-owned command record. The record is derived from
// the plugin's describe() self-report and written by the stamp step; an author
// never writes it. The element type is hand-written because the generator names
// it via x-aggrega-go-type on the schema's items node and does not descend into
// it.
type PluginCommand struct {
	// Name is the command verb the host registers — the aggrega <name> token an
	// operator invokes, and the discriminator the host hands the plugin at
	// invocation so a single plugin backing several commands routes on which
	// command was called.
	Name string `json:"name" yaml:"name"`
	// Description is the command's one-line short-help text, shown in the command
	// listing and help output. Documentation only; the host does not act on it.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Flags is the command's flags, in declared order. Each entry contributes one
	// flag the host registers on the command so aggrega help <command> renders it
	// like a core command's flag. A command with no flags leaves it empty. It is
	// part of the same machine-owned record the stamp step derives from the
	// plugin's describe() self-report; an author never writes it.
	Flags []PluginCommandFlag `json:"flags,omitempty" yaml:"flags,omitempty"`
}

// PluginCommandFlag is one flag a CLI plugin declares on a command, in the
// machine-owned command record. Each flag is derived from the plugin's
// describe() self-report and written by the stamp step; an author never writes
// it. The type is hand-written because the generator names it via
// x-aggrega-go-type on the schema's items node and does not descend into it.
type PluginCommandFlag struct {
	// Name is the flag's long name — the --name token an operator passes on the
	// command line. Every declared flag names it.
	Name string `json:"name" yaml:"name"`
	// Shorthand is the flag's optional short alias — the -x form an operator may
	// pass instead of the long name — as a single ASCII letter or digit. Empty
	// means the flag has no short alias.
	Shorthand string `json:"shorthand,omitempty" yaml:"shorthand,omitempty"`
	// Description is the flag's one-line help text, shown in the command's help
	// output. Documentation only; the host does not act on it.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Type is the flag's value type — string, bool or int — and the kind of flag
	// help renders it as. Empty renders it as a string flag.
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	// Default is the flag's default value when the operator does not pass it,
	// expressed as a string; for a bool or int flag it is a value of that type.
	// Empty means the flag declares no default.
	Default string `json:"default,omitempty" yaml:"default,omitempty"`
}

// ProviderContract is a provider's self-declared compile-time render
// behaviors, authored on the entity's provider block as the contract the host
// cross-checks against the plugin's own describe() self-report at publish and at
// load: the input kinds it renders, how it assembles a dispatch group's per-node
// output, and the Go-template delimiter pair it applies to imported artifact
// files. It describes the provider as a whole, not any one export.
//
// It is an alias of the generated PluginSpecProviderContract, so the name and
// the type identity both survive while the shape is single-sourced at the
// schema. The contract is held by value on the provider block; a provider that
// declares none leaves it zero, and its omitzero tag keeps it out of the
// serialized entity.
type ProviderContract = PluginSpecProviderContract

// IsZero reports whether the contract declares nothing at all — which is what a
// provider that authors no render-behavior contract has said. It is the single
// home of that question: every read-site that used to ask whether the contract
// pointer was nil asks here instead, so "authors no contract" has one meaning
// across the resolution, advisory and engine-selection surfaces.
//
// It compares against the zero value rather than listing the fields, because the
// shape is generated from the schema: a hand-written field census here would keep
// compiling and silently stop covering a field the schema gains, which is the
// exact failure a generated shape is meant to remove.
//
// Declaring it also fixes what the field's omitzero tag omits, since that option
// dispatches to this method — so the serialized entity and this predicate cannot
// disagree about what an absent contract is.
func (c ProviderContract) IsZero() bool {
	return reflect.ValueOf(c).IsZero()
}

// PluginInputKinds is a plugin's authored input-kind claim: which of the three
// Recipe input kinds it renders. Each field is an independent claim.
//
// THIS IS THE ONE HOST-SIDE HOME for that claim. "Which input kinds does this
// provider render" is a single fact about a single provider, and every host-side
// consumer of it — the resolution claim a loaded entity carries, and the contract
// the central input-kind gate reads — is an ALIAS of this type rather than a
// struct of its own. So a kind added reaches all of them at once, and no field
// copy sits between them to drop one.
//
// The shape itself is no longer written here: it is an alias of the generated
// PluginSpecProviderContractInputKinds, so the declaration is derived from the
// entity schema and the name and type identity survive for every consumer and
// for a reflective comparison.
//
// Two representations remain genuinely distinct, and both are deliberate:
//
//   - The describe() wire structs (the host-side codec, and the plugin kit's
//     guest-side codec in the shared ABI module) are GENERATED from the
//     describe-socket schema. They are single-sourced at that schema, which is a
//     stronger guarantee than sharing a Go type would be, and they cannot share one
//     because a guest must not import the host.
//   - The published capability grid reads a guest's claim out of guest SOURCE TEXT
//     by parsing it, so it matches field names as strings and no Go type can bind it.
//
// To add an input kind: add the property to the entity schema's inputKinds node
// and regenerate, add it to the describe-socket schema so both generated codecs
// carry it, and extend the mappers that cross into the generated shapes. Do
// NOT declare a fourth struct with the same booleans, because nothing makes
// that fail to compile — not unexported fields, not aliases, not generation — so the
// field-set guard in internal/wasm is a floor that reports a new shape after the
// fact, never a bar that prevents one.
type PluginInputKinds = PluginSpecProviderContractInputKinds

// PluginArtifactDelimiters is a plugin's authored Go-template delimiter pair
// for imported artifact files, declared when it differs from the default
// "{{"/"}}" pair.
type PluginArtifactDelimiters struct {
	// Left is the opening delimiter.
	Left string `json:"left" yaml:"left"`
	// Right is the closing delimiter.
	Right string `json:"right" yaml:"right"`
}

// PluginAssemblyNameAnnotation is a plugin's authored assembly-name annotation:
// the boundary annotation whose resolved value names the artifact the plugin
// assembles from a dispatch group, declared when the plugin folds a group's
// contributing boundaries into one named assembly. The block is hand-written
// because the generator names it via x-aggrega-go-type on the schema's object
// node and does not descend into it.
type PluginAssemblyNameAnnotation struct {
	// Annotation is the boundary annotation key the plugin reads the assembled
	// artifact's name from.
	Annotation string `json:"annotation" yaml:"annotation"`
	// RequireUnique is whether every boundary contributing to one dispatch group
	// must agree on the annotation's resolved value. When true, a group whose
	// contributing boundaries resolve the annotation to different values halts the
	// build; when false, the plugin accepts the first resolved value.
	RequireUnique bool `json:"requireUnique,omitempty" yaml:"requireUnique,omitempty"`
}

// PluginSourceIgnore is a provider's declaration of the files its ecosystem never
// ships beside a source it renders, as ignore rules in gitignore syntax: its own
// patterns, scoped to the root of the source directory, and the names of the
// ignore files it honours inside a source, whose lines are further patterns scoped
// to the directory holding each. The block is hand-written because the generator
// names it via x-aggrega-go-type on the schema's object node and does not descend
// into it.
type PluginSourceIgnore struct {
	// Patterns are the ecosystem's own ignore patterns, in the order they apply.
	Patterns []string `json:"patterns,omitempty" yaml:"patterns,omitempty"`
	// Files are the names of the ignore files the ecosystem honours inside a
	// source.
	Files []string `json:"files,omitempty" yaml:"files,omitempty"`
}

// PluginSignature is the optional, documentation-only pointer to a
// plugin's attached signature. It records where the signature lives and
// how it was produced for catalog readers; it does not itself gate or
// admit the plugin. The enforcing check is the per-registry trust policy
// applied at pull, which verifies the signature referrer and records a
// verified fact in the lockfile.
type PluginSignature struct {
	// Type is the signature scheme. "sigstore-cosign" is the supported scheme.
	Type string `json:"type" yaml:"type"`
	// SignatureRef is the OCI referrer reference of the attached signature
	// bundle, for documentation and discovery. Empty when the signature is
	// discovered by referrer enumeration at pull rather than pinned here.
	SignatureRef string `json:"signatureRef,omitempty" yaml:"signatureRef,omitempty"`
	// PublicKeyRef points to the public key the signature was produced with,
	// for a keyed signature. Empty indicates a keyless (OIDC-identity)
	// signature, verified against the registry's trusted identities.
	PublicKeyRef string `json:"publicKeyRef,omitempty" yaml:"publicKeyRef,omitempty"`
}

// PluginRuntime is the optional execution-constraints block. Its
// fields are field-aligned with the loader's plugin input so a
// catalog-loaded entity maps to the loader's runtime shape
// field-for-field. Outbound network access has no field here: a plugin
// never reaches the network at build time, so there is nothing to allow.
type PluginRuntime struct {
	// MaxMemoryPages caps the plugin's linear memory in Wasm pages (64 KiB
	// per page). Zero or absent applies the platform default cap, so a load
	// is never unbounded; an attachment that needs more raises this value.
	MaxMemoryPages uint32 `json:"maxMemoryPages,omitempty" yaml:"maxMemoryPages,omitempty"`
	// TimeoutMs caps how long the plugin's own code may execute during one
	// call, in milliseconds, host-function time excluded. Zero or absent applies
	// the platform default cap; an attachment that runs longer raises this value.
	TimeoutMs uint64 `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	// HostFunctions is the machine-owned record of the host functions the
	// plugin imports, in canonical (sorted, de-duplicated) form. It is derived
	// from the published .wasm import section at publish, stamped into the
	// entity, and re-derived and verified at pull — an author never writes it.
	// The host registers exactly this derived set and validates every entry
	// against the curated allowlist, giving each plugin the least-privilege host
	// surface its binary actually needs.
	HostFunctions []string `json:"hostFunctions,omitempty" yaml:"hostFunctions,omitempty"`
	// ConfigKV carries plugin-specific configuration the runtime hands to the
	// plugin at start, as string-to-string pairs in author order so the
	// emitted record is byte-identical across runs.
	ConfigKV OrderedMap[string] `json:"configKV,omitempty,omitzero" yaml:"configKV,omitempty"`
}
