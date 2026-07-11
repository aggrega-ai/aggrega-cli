// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"sort"

	extism "github.com/extism/go-sdk"
)

// translateToManifest builds an extism.Manifest from a PluginSpec and
// runs the AOT-purity firewall against the produced manifest. Returns
// the manifest plus nil error on success; returns the zero manifest
// plus an error wrapping ErrWasmManifestRejected on firewall failure.
//
// The firewall validates against the constructed manifest (not against
// the spec) because the manifest is what actually drives the runtime
// — a spec value that fails to round-trip would be caught here, and
// the firewall's rejection messages reference manifest fields the
// runtime would otherwise act on.
func translateToManifest(spec PluginSpec) (extism.Manifest, error) {
	// Apply the platform default cap when the spec leaves a limit zero, at this
	// single chokepoint so every load path — entity, linter, and test — gets a
	// consistent cap. A zero is "apply the default cap", never "unbounded": the
	// Memory block and Timeout are ALWAYS set, so the runtime always enforces a
	// memory ceiling and a per-call wall-time ceiling.
	manifest := extism.Manifest{
		Wasm: []extism.Wasm{
			extism.WasmFile{Path: spec.Path, Name: spec.Name},
		},
		Config:       spec.ConfigKV,
		AllowedHosts: spec.AllowedHosts,
		Timeout:      effectiveTimeoutMs(spec),
		Memory:       &extism.ManifestMemory{MaxPages: effectiveMaxPages(spec)},
	}

	if err := enforceAOTPurityFirewall(manifest, spec.Name); err != nil {
		return extism.Manifest{}, err
	}
	return manifest, nil
}

// enforceAOTPurityFirewall rejects any manifest declaring a capability
// the AOT-purity discipline forbids. Two surfaces today: non-empty
// AllowedHosts (outbound network access) and non-empty AllowedPaths
// (filesystem access). Both would let a plugin reach beyond its
// in-memory inputs at run time, which Aggrega does not permit.
//
// Two additional capability surfaces — WASI stdio passthrough and
// wall-time access — live on PluginConfig and ModuleConfig respectively,
// not on the manifest. The Loader never sets those fields, so they
// cannot be requested through the PluginSpec API; the firewall has
// nothing to check at the manifest level.
//
// Host-function-allowlist enforcement is not at the manifest level: the
// loader derives the host functions from the .wasm import section and runs
// the subset gate (validateHostFunctionsSubset) against the derived set, so
// a host function outside the curated set is rejected at derivation, not here.
func enforceAOTPurityFirewall(manifest extism.Manifest, pluginName string) error {
	if len(manifest.AllowedHosts) > 0 {
		return fmt.Errorf("plugin %q declares AllowedHosts %v: %w",
			pluginName, manifest.AllowedHosts, ErrWasmManifestRejected)
	}
	if len(manifest.AllowedPaths) > 0 {
		paths := make([]string, 0, len(manifest.AllowedPaths))
		for p := range manifest.AllowedPaths {
			paths = append(paths, p)
		}
		sort.Strings(paths) // deterministic error-message ordering across runs
		return fmt.Errorf("plugin %q declares AllowedPaths %v: %w",
			pluginName, paths, ErrWasmManifestRejected)
	}
	return nil
}

// validateHostFunctionsSubset ensures every name in names appears in the
// host-function allowlist. It is the single subset gate the loader applies to
// the set derived from a .wasm binary and the descriptor applies to a per-export
// host-function set. Returns nil if names is a subset of the allowlist;
// otherwise returns an error wrapping ErrWasmManifestRejected naming the
// violating function. name identifies the plugin (or plugin/export) in that
// message.
func validateHostFunctionsSubset(name string, names []string) error {
	for _, fn := range names {
		if _, ok := hostFunctionAllowlist[fn]; !ok {
			return fmt.Errorf("plugin %q declares host function %q outside the allowlist %v: %w",
				name, fn, allowlistedHostFunctionNames(), ErrWasmManifestRejected)
		}
	}
	return nil
}

// validateHostFunctionsSubsetCLI is the runtime CLI profile's subset gate: it
// admits every name the shared gate admits PLUS the CLI-only streaming
// emitters, so a CLI guest may import the streaming host functions the terminal
// profile grants. It is the gate Loader.LoadCLI applies to the set derived from
// a CLI guest's .wasm binary; Loader.Load keeps the shared gate above, which
// admits neither streaming name. A name in neither allowlist returns an error
// wrapping ErrWasmManifestRejected naming the violating function.
func validateHostFunctionsSubsetCLI(name string, names []string) error {
	for _, fn := range names {
		if hostFunctionAllowlist[fn] || cliHostFunctionAllowlist[fn] {
			continue
		}
		return fmt.Errorf("plugin %q declares host function %q outside the CLI allowlist %v: %w",
			name, fn, cliAllowlistedHostFunctionNames(), ErrWasmManifestRejected)
	}
	return nil
}
