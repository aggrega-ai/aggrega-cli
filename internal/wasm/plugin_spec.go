// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

// PluginSpec is the input shape the Loader accepts. It is deliberately
// minimal — no URI resolution, no version handling, no catalog lookup
// — those concerns belong to upstream layers (the workspace-config
// parser and the catalog source-resolver) that populate a PluginSpec
// and hand it to Load.
//
// Field shape mirrors the runtime block of the WasmPlugin catalog
// entity (whose canonical authoring surface is documented at the
// language-reference's Wasm-plugin contract page) so that a future
// translation from a catalog-loaded WasmPlugin entity to PluginSpec is
// a field-for-field map, no field reinvention.
type PluginSpec struct {
	// Name is the plugin's identifier for diagnostics. The Loader
	// surfaces this in error messages from the AOT-purity firewall and
	// from Plugin.Call's panic-recovery wrap. Typically the plugin's
	// catalog URI leaf (for example "publishing-linter").
	Name string

	// Path is the filesystem path to the .wasm artifact bytes. Upstream
	// resolution (catalog URI to local path) happens elsewhere; the
	// Loader treats Path as an opaque resolved location. Ignored when
	// Bytes is non-nil.
	Path string

	// Bytes is the .wasm artifact content supplied directly, bypassing a
	// filesystem read. A non-nil Bytes wins over Path: the Loader compiles
	// these bytes and never reads from disk. This is the path for an artifact
	// with no stable filesystem location — bytes embedded in the binary, or
	// content resolved from a verified cache slot — where reading "the file"
	// is not meaningful. When Bytes is nil the Loader reads Path as before.
	Bytes []byte

	// MaxMemoryPages caps the plugin's linear memory in Wasm pages
	// (64 KiB per page). Zero leaves the Extism runtime's default
	// memory policy in effect.
	MaxMemoryPages uint32

	// TimeoutMs caps per-Call execution wall time in milliseconds.
	// Zero leaves the call unbounded (suitable for tests; production
	// callers SHOULD set a non-zero ceiling).
	TimeoutMs uint64

	// DeclaredHostFunctions lists the host function names the plugin
	// imports. The Loader validates that every entry appears in the
	// curated allowlist (see hostfuncs.go); a declared function outside
	// the allowlist triggers ErrWasmManifestRejected. Only declared
	// functions are registered on the constructed plugin; undeclared
	// allowlist members are not exposed, giving each plugin the
	// least-privilege host surface it actually needs.
	DeclaredHostFunctions []string

	// ConfigKV carries plugin-specific configuration that the Extism
	// runtime hands to the plugin at start. Nil leaves the runtime's
	// default empty configuration in effect.
	ConfigKV map[string]string

	// AllowedHosts is accepted only to give callers an explicit,
	// caller-visible firewall-rejection path. Any non-empty AllowedHosts
	// triggers ErrWasmManifestRejected — outbound network access from a
	// plugin would break AOT purity (no runtime network access at build
	// time) and is rejected categorically.
	AllowedHosts []string
}
