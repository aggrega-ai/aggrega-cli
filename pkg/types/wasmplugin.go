// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

//go:generate go run ../../internal/tools/typegen wasmplugin_gen.go
//go:generate go run ../../internal/tools/typegen wasmplugin_enums_gen.go

// WasmPlugin's top-level struct declarations (WasmPlugin,
// WasmPluginMetadata, WasmPluginSpec) are generated into
// wasmplugin_gen.go from the published wasmplugin-v1alpha1 schema; their
// godoc is the schema's descriptions. This hand-written companion holds
// the two optional nested blocks the generator names but does not
// descend into (WasmPluginSignature, WasmPluginRuntime), plus any future
// WasmPlugin methods or marshalers. The shared Maintainer type the
// generated metadata references lives in common.go.
//
// The runtime block stays field-aligned with the loader's plugin input
// (max-memory pages, per-call timeout, declared host functions, the
// config key/value map) so a catalog-loaded entity maps to the loader's
// runtime shape field-for-field, no field reinvention. The loader's
// network-firewall input has no authoring counterpart here: a plugin
// never reaches the network at build time, so there is no host-allowlist
// field to author. The loader identity inputs (the plugin's diagnostic
// name and resolved artifact path) are resolution internals supplied by
// upstream layers, not entity authoring fields.
//
// WasmPluginSignature and WasmPluginRuntime are referenced by pointer
// (named via x-aggrega-go-type on the schema's object nodes) so an entity
// that omits them round-trips without emitting an empty block — a
// value-typed nested struct would re-marshal as `{}`.

// WasmPluginExport is one export the plugin offers, with its declared
// capability. An export is a named entry point the host dispatches to (for
// example "render_node", "lint_naming", "step"); the capability fields are the
// authored claim of what the export does, which the host cross-checks against
// the plugin's self-report at publish and at load. Entries preserve author
// order. The element type is hand-written because the generator names it via
// x-aggrega-go-type on the schema's items node and does not descend into it.
type WasmPluginExport struct {
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

// WasmPluginSignature is the optional, documentation-only pointer to a
// plugin's attached signature. It records where the signature lives and
// how it was produced for catalog readers; it does not itself gate or
// admit the plugin. The enforcing check is the per-registry trust policy
// applied at pull, which verifies the signature referrer and records a
// verified fact in the lockfile.
type WasmPluginSignature struct {
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

// WasmPluginRuntime is the optional execution-constraints block. Its
// fields are field-aligned with the loader's plugin input so a
// catalog-loaded entity maps to the loader's runtime shape
// field-for-field. Outbound network access has no field here: a plugin
// never reaches the network at build time, so there is nothing to allow.
type WasmPluginRuntime struct {
	// MaxMemoryPages caps the plugin's linear memory in Wasm pages (64 KiB
	// per page). Zero or absent applies the platform default cap, so a load
	// is never unbounded; an attachment that needs more raises this value.
	MaxMemoryPages uint32 `json:"maxMemoryPages,omitempty" yaml:"maxMemoryPages,omitempty"`
	// TimeoutMs caps per-call execution wall time in milliseconds. Zero or
	// absent applies the platform default cap, so a call is never
	// uncancellable; an attachment that runs longer raises this value.
	TimeoutMs uint64 `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	// DeclaredHostFunctions lists the host function names the plugin imports.
	// The host validates every entry against the curated allowlist and
	// registers only the declared functions, giving each plugin the
	// least-privilege host surface it actually needs.
	DeclaredHostFunctions []string `json:"declaredHostFunctions,omitempty" yaml:"declaredHostFunctions,omitempty"`
	// ConfigKV carries plugin-specific configuration the runtime hands to the
	// plugin at start, as string-to-string pairs in author order so the
	// emitted record is byte-identical across runs.
	ConfigKV OrderedMap[string] `json:"configKV,omitempty,omitzero" yaml:"configKV,omitempty"`
}
