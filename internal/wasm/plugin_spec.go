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
	// surfaces this in error messages from the AOT-purity load gate and
	// from the call core's panic-recovery wrap. Typically the plugin's
	// catalog URI leaf (for example "publishing-linter").
	Name string

	// PluginType is the expected plugin type at this load site — the kind of
	// plugin the socket declares it is loading; see plugintype.Type for the
	// canonical set. When set, the load gate enforces it: the loaded descriptor's
	// self-reported type must equal it or the load is rejected with an error
	// wrapping ErrWasmPluginTypeMismatch, so a guest of the wrong kind never
	// reaches its socket's dispatch. Empty declares no expectation and skips the
	// check — the introspection path that reads a guest's self-report to discover
	// its kind rather than to assert one. It also remains the source of the
	// guest's WASI argv program-name label (see programName): one concept — the
	// plugin's kind as expected at this socket — with two consumers.
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

	// TimeoutMs caps how long the GUEST may execute during one Call, in
	// milliseconds. Zero or absent applies the platform default cap
	// (DefaultTimeoutMs), so a Call is never uncancellable. On Linux execution
	// is measured as the processor time of the thread running the guest, so
	// time the guest spends waiting for a processor does not count; on other
	// platforms it is measured in wall time. On both, the time the instance
	// spends inside host functions is measured and excluded, so a guest is not
	// charged for work the host does on its behalf. The first Call to a freshly
	// loaded instance also counts the guest language runtime's one-time
	// initialisation, which runs inside that Call.
	TimeoutMs uint64

	// Contract is the optional authored capability claim the load gate
	// cross-checks against the plugin's self-reported describe() descriptor (see
	// CrossCheckContract). nil disables the cross-check — the path every
	// non-entity caller and the publish path take. The entity-fronted load path
	// populates it from the catalog Plugin entity's exports.
	Contract *EntityContractClaim

	// Commands is the entity's machine-owned CLI-command mirror, carried only on
	// the entity-fronted load path. When CommandsMirrored is true the load gate
	// verifies it equals the plugin's describe() commands exactly (see
	// DescribeAndValidate); a divergence halts with ErrWasmCommandsMirrorMismatch.
	// It is populated from the resolved entity's spec.commands.
	Commands []PluginCLICommand

	// CommandsMirrored reports whether the entity supplied its command mirror,
	// gating the load gate's full-equality command check. It is the explicit
	// entity-fronted signal — set only when a resolved entity carries a
	// spec.commands field — kept separate from Contract so the check runs on a CLI
	// plugin (whose Contract is nil) and is skipped on a bare .wasm load (which
	// supplies no mirror), independent of whether an authored contract claim is
	// present.
	CommandsMirrored bool

	// ConfigKV carries plugin-specific configuration that the Extism
	// runtime hands to the plugin at start. Nil leaves the runtime's
	// default empty configuration in effect.
	ConfigKV map[string]string

	// AllowedHosts is accepted only to give callers an explicit,
	// caller-visible load-gate-rejection path. Any non-empty AllowedHosts
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
