// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// Source identifies which configuration layer contributed the
// effective Config returned by LoadWithGlobal. A merged Config
// reports the HIGHEST-precedence layer that supplied at least one
// field; precedence is workspace > global > defaults.
//
// The wire token returned by String() is the lowercase tag carried
// in the daemon's aggrega/initialize response under the
// "configSource" field.
type Source int

const (
	// SourceDefaults reports that neither a workspace config file
	// nor a global config file was loaded. Every Config field comes
	// from Default().
	SourceDefaults Source = iota

	// SourceGlobal reports that a global config file at
	// $AGGREGA_CONFIG or the XDG path was loaded and no workspace
	// config file was loaded.
	SourceGlobal

	// SourceWorkspace reports that a workspace config file was
	// loaded. The global file may also have been loaded; workspace
	// fields override global fields per-key, but the Source returns
	// "workspace" to indicate the operator-facing surface.
	SourceWorkspace
)

// String returns the lowercase wire token for s. The mapping is
// stable and is consumed by aggrega/initialize.
func (s Source) String() string {
	switch s {
	case SourceWorkspace:
		return "workspace"
	case SourceGlobal:
		return "global"
	default:
		return "defaults"
	}
}

// envVarConfigOverride names the environment variable an operator
// sets to point at the explicit global config path. It is the only
// global-config source; there is no XDG (`~/.config/aggrega`) path —
// the home-level `~/.aggrega-config.yaml` participates as the top
// cascade rung like any other tree file.
const envVarConfigOverride = "AGGREGA_CONFIG"

// appendSlices captures one layer's seven `_append` slices. Used by the
// resolution cascade to defer concatenation until after the base overlay
// completes, so the `_append` semantic composes correctly across
// layers (each layer's `_append` extends the final merged base in
// precedence order).
type appendSlices struct {
	CatalogDirs         []string
	NarrowingExemptions []NarrowingExemption
	Mutator             []PluginRef
	Policy              []PluginRef
	Linter              []PluginRef
	Importer            []PluginRef
	Providers           []ProviderRef
}

// captureAppends extracts the seven `_append` slices from a freshly-
// loaded layer Config. The captured slices share storage with the
// source Config; callers that subsequently mutate cfg should not also
// retain the captured value.
func captureAppends(cfg Config) appendSlices {
	return appendSlices{
		CatalogDirs:         cfg.Workspace.CatalogDirsAppend,
		NarrowingExemptions: cfg.Lint.PublishingDiscipline.NarrowingExemptionsAppend,
		Mutator:             cfg.Plugins.MutatorAppend,
		Policy:              cfg.Plugins.PolicyAppend,
		Linter:              cfg.Plugins.LinterAppend,
		Importer:            cfg.Plugins.ImporterAppend,
		Providers:           cfg.Plugins.ProvidersAppend,
	}
}

// stripAppends zeroes the seven `_append` fields on cfg. Called on
// every layer's Config before promoting it to merged (so the overlay
// pass does not carry `_append` slices through), and on the final
// merged Config (so downstream consumers see only the base fields
// with `_append` already concatenated by applyAppends).
func stripAppends(cfg *Config) {
	cfg.Workspace.CatalogDirsAppend = nil
	cfg.Lint.PublishingDiscipline.NarrowingExemptionsAppend = nil
	cfg.Plugins.MutatorAppend = nil
	cfg.Plugins.PolicyAppend = nil
	cfg.Plugins.LinterAppend = nil
	cfg.Plugins.ImporterAppend = nil
	cfg.Plugins.ProvidersAppend = nil
}

// applyAppends concatenates each layer's `_append` slices onto the
// corresponding base fields of merged. Precedence ordering is global
// then workspace; defaults never contribute (Default() leaves them
// nil). The empty-base case falls out naturally — append onto a nil
// slice produces the appended slice.
func applyAppends(merged *Config, layers ...appendSlices) {
	for _, layer := range layers {
		merged.Workspace.CatalogDirs = append(merged.Workspace.CatalogDirs, layer.CatalogDirs...)
		merged.Lint.PublishingDiscipline.NarrowingExemptions = append(merged.Lint.PublishingDiscipline.NarrowingExemptions, layer.NarrowingExemptions...)
		merged.Plugins.Mutator = append(merged.Plugins.Mutator, layer.Mutator...)
		merged.Plugins.Policy = append(merged.Plugins.Policy, layer.Policy...)
		merged.Plugins.Linter = append(merged.Plugins.Linter, layer.Linter...)
		merged.Plugins.Importer = append(merged.Plugins.Importer, layer.Importer...)
		merged.Plugins.Providers = append(merged.Plugins.Providers, layer.Providers...)
	}
}

// loadOverlayInto loads a YAML config file at path, overlaying its
// fields onto the seed target. Reads the file once, validates the
// bytes once via validateBytes, and unmarshals once into a copy of
// seed so the caller's value is unaffected on error.
//
// Distinct from Load() because Load() always seeds Default()
// internally — this helper supports the multi-layer merge where the
// seed is a globally-merged baseline.
func loadOverlayInto(path string, seed Config) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("workspaceconfig: read %s: %w", path, err)
	}
	if err := validateBytes(data, path); err != nil {
		return Config{}, err
	}
	target := seed
	if err := yaml.Unmarshal(data, &target); err != nil {
		return Config{}, fmt.Errorf("workspaceconfig: overlay %s: %w", path, err)
	}
	return target, nil
}
