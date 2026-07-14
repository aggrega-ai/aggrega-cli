// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

// PluginSpec is the input shape the Loader accepts. It is deliberately
// minimal — no URI resolution, no version handling, no catalog lookup
// — those concerns belong to upstream layers (the workspace-config
// parser and the catalog source-resolver) that populate a PluginSpec
// and hand it to Load.
//
// Field shape mirrors the runtime block of the Plugin catalog
// entity (whose canonical authoring surface is documented at the
// language-reference's Wasm-plugin contract page) so that a future
// translation from a catalog-loaded Plugin entity to PluginSpec is
// a field-for-field map, no field reinvention.
type PluginSpec struct {
	// Name is the plugin's identifier for diagnostics. The Loader
	// surfaces this in error messages from the AOT-purity firewall and
	// from Plugin.Call's panic-recovery wrap. Typically the plugin's
	// catalog URI leaf (for example "publishing-linter").
	Name string

	// PluginType is the plugin's kind — provider, linter, frp_step, mutator,
	// or policy-validator. The Loader uses it only to compose a human-readable
	// program-name label for the guest's WASI argv (see programName); it is a
	// diagnostic label, not a dispatch key.
	PluginType PluginType

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
	// (64 KiB per page). Zero or absent applies the platform default cap
	// (DefaultMaxMemoryPages), so a load is never unbounded.
	MaxMemoryPages uint32

	// TimeoutMs caps per-Call execution wall time in milliseconds. Zero
	// or absent applies the platform default cap (DefaultTimeoutMs), so a
	// Call is never uncancellable.
	TimeoutMs uint64

	// Contract is the optional authored capability claim the load gate
	// cross-checks against the plugin's self-reported describe() descriptor (see
	// CrossCheckContract). nil disables the cross-check — the path every
	// non-entity caller and the publish path take. The entity-fronted load path
	// populates it from the catalog Plugin entity's exports.
	Contract *EntityContractClaim

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

// programName is the human-readable program-name label the Loader supplies as
// the guest's WASI argv[0]. It exists to satisfy transitive dependencies (such
// as klog) whose package init reads os.Args[0] and panics on an empty argv; it
// is NOT a machine-parseable identity, so callers must not split or parse it.
// It composes "<type>-<name>" when both are set, falls back to the name alone,
// and never returns an empty string.
func (s PluginSpec) programName() string {
	switch {
	case s.PluginType != "" && s.Name != "":
		return string(s.PluginType) + "-" + s.Name
	case s.Name != "":
		return s.Name
	default:
		return "aggrega-plugin"
	}
}
