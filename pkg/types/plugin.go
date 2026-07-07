// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

//go:generate go run ../../internal/tools/typegen plugin_gen.go
//go:generate go run ../../internal/tools/typegen plugin_enums_gen.go

// Plugin's top-level struct declarations (Plugin,
// PluginMetadata, PluginSpec) are generated into
// plugin_gen.go from the published plugin-v1alpha1 schema; their
// godoc is the schema's descriptions. This hand-written companion holds
// the optional nested blocks the generator names but does not
// descend into (PluginSignature, PluginRuntime, PluginProvider,
// PluginCapabilities and its nested PluginInputKinds and
// PluginArtifactDelimiters), plus any future Plugin methods or
// marshalers. The shared Maintainer and WellKnownAnnotation types the
// generated fields reference live in common.go.
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
// PluginSignature, PluginRuntime, and PluginProvider are referenced by
// pointer (named via x-aggrega-go-type on the schema's object nodes) so an
// entity that omits them round-trips without emitting an empty block — a
// value-typed nested struct would re-marshal as `{}`.

// PluginProvider is the optional provider-socket metadata block on a
// pluginType: provider Plugin. It documents how the plugin's rendering
// engine translates the Workload kind into provider-native manifests and
// which platform-native annotations the engine reads. It carries no
// meaning on a linter or frp_step plugin. The block is hand-written
// because the generator names it via x-aggrega-go-type on the schema's
// object node and does not descend into it.
type PluginProvider struct {
	// WorkloadSchema is JSON Schema 2020-12 documenting how this provider
	// translates the aggrega.ai/v1alpha1 Workload kind into provider-native
	// manifests. A non-empty body indicates the engine supports Workload
	// synthesis; an empty or absent field carries no synthesis claim.
	// Whether a workload: Recipe is supported is determined by the bound
	// engine's synthesis capability, not by this field's presence. The body
	// is compiled to JSON Schema 2020-12 at load as a structural guardrail;
	// it becomes executable input to Wasm translators in a future iteration.
	WorkloadSchema string `json:"workloadSchema,omitempty" yaml:"workloadSchema,omitempty"`
	// WellKnownAnnotations documents the platform-native annotation keys
	// (the aggrega.ai/ namespace) this provider's engine reads, each with
	// the behavior applied when the annotation is absent. It is descriptive
	// contract metadata for catalog readers and the inspect surface; the
	// compiler does not consume it to gate or branch a build. Entries
	// preserve author order.
	WellKnownAnnotations []WellKnownAnnotation `json:"wellKnownAnnotations,omitempty" yaml:"wellKnownAnnotations,omitempty"`
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

// PluginCapabilities is a plugin's self-declared compile-time render
// behaviors, authored on the entity as the claim the host cross-checks against
// the plugin's own describe() self-report at publish and at load: the input
// kinds it renders, how it assembles a dispatch group's per-node output, the
// Go-template delimiter pair it applies to imported artifact files, and any
// restriction on which source files it consumes. It describes the plugin as a
// whole, not any one export. The block is hand-written because the generator
// names it via x-aggrega-go-type on the schema's object node and does not
// descend into it.
type PluginCapabilities struct {
	// InputKinds is which of the three Recipe input kinds this plugin renders:
	// a sourced file set, a synthesized workload, and a standalone
	// artifacts-only node.
	InputKinds PluginInputKinds `json:"inputKinds,omitempty,omitzero" yaml:"inputKinds,omitempty"`
	// BoundaryAssembly is how the plugin combines a dispatch group's per-node
	// render output: "none" writes each node's output directly at the dispatch
	// path; "merge" folds the per-node fragments into one document; "nest"
	// places each node's output under its own position in an umbrella
	// structure. Empty carries no assembly claim.
	BoundaryAssembly string `json:"boundaryAssembly,omitempty" yaml:"boundaryAssembly,omitempty"`
	// ArtifactDelimiters is the Go-template delimiter pair the plugin applies
	// to imported artifact files. Absent means the default "{{"/"}}" pair; a
	// plugin whose target syntax collides with that pair declares an alternate
	// pair here.
	ArtifactDelimiters *PluginArtifactDelimiters `json:"artifactDelimiters,omitempty" yaml:"artifactDelimiters,omitempty"`
	// SourceFilter is the source-file paths the plugin consumes when a node
	// supplies a file set. Absent means the plugin consumes every file in the
	// set. Entries preserve author order.
	SourceFilter []string `json:"sourceFilter,omitempty" yaml:"sourceFilter,omitempty"`
	// ReusableInstance is whether one loaded plugin instance may serve every
	// render, assemble, and validate crossing of a build rather than a fresh
	// instance per crossing. Authored true only when the plugin holds no state
	// across crossings — a pure function of its per-crossing input — so a single
	// instance produces byte-identical output whether reused or freshly loaded.
	// Absent (false) loads a fresh instance per crossing.
	ReusableInstance bool `json:"reusableInstance,omitempty" yaml:"reusableInstance,omitempty"`
}

// PluginInputKinds is a plugin's authored input-kind claim: which of the three
// Recipe input kinds it renders. Each field is an independent claim.
type PluginInputKinds struct {
	// Sourced declares that the plugin renders a sourced file set (a chart, a
	// module, a compose file).
	Sourced bool `json:"sourced,omitempty" yaml:"sourced,omitempty"`
	// Synthesized declares that the plugin renders a synthesized workload into
	// provider-native artifacts.
	Synthesized bool `json:"synthesized,omitempty" yaml:"synthesized,omitempty"`
	// ArtifactsStandalone declares that the plugin renders a standalone
	// artifacts-only node — one carrying imported artifact documents and no
	// source or workload.
	ArtifactsStandalone bool `json:"artifactsStandalone,omitempty" yaml:"artifactsStandalone,omitempty"`
}

// PluginArtifactDelimiters is a plugin's authored Go-template delimiter pair
// for imported artifact files, declared when it differs from the default
// "{{"/"}}" pair.
type PluginArtifactDelimiters struct {
	// Left is the opening delimiter.
	Left string `json:"left" yaml:"left"`
	// Right is the closing delimiter.
	Right string `json:"right" yaml:"right"`
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
	// TimeoutMs caps per-call execution wall time in milliseconds. Zero or
	// absent applies the platform default cap, so a call is never
	// uncancellable; an attachment that runs longer raises this value.
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
