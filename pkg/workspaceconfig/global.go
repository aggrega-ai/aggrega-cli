// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

	// SourceGlobal reports that a global config file was loaded and no
	// workspace config file was loaded. The global file is the explicit
	// $AGGREGA_CONFIG path when that variable is set, otherwise the
	// auto-loaded default location of the platform the resolution was taken
	// for (machinedirs.GlobalConfigFile).
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

// EnvConfigOverride names the environment variable an operator sets to point at
// an explicit global config path. When set, it overrides the default location;
// when unset, the global layer is auto-loaded from the platform's default
// location if that file exists — `%APPDATA%\aggrega\config.yaml` on Windows,
// and elsewhere `$XDG_CONFIG_HOME/aggrega/config.yaml`, falling back to
// `~/.config/aggrega/config.yaml` (machinedirs.GlobalConfigFile). The `--config`
// flag is the flag form of this override; the flag wins over the variable when
// both are set. Either way the workspace-tree config files still layer on top.
const EnvConfigOverride = "AGGREGA_CONFIG"

// appendSlices captures the per-layer slices deferred to the post-overlay
// concatenation pass: the five `_append` companions, plus the CLI base list,
// plus the layer's Release catalog entries.
// The `_append` companions defer concatenation until after the base overlay
// completes, so the `_append` semantic composes correctly across layers (each
// layer's `_append` extends the final merged base in precedence order). The CLI
// base list rides the same pass — captured per layer and stripped from the
// overlaid base — because the CLI list composes across the cascade rather than
// whole-replacing, so global and workspace CLI plugins both register. The
// Release entries ride it for the same reason: they compose one Release at a
// time rather than the nearest layer's map replacing every other layer's, and
// they arrive already anchored to their declaring file by the single-file load.
type appendSlices struct {
	CatalogDirs []string
	Governance  []PluginRef
	Linter      []PluginRef
	Importer    []PluginRef
	Providers   []PluginRef
	CLI         []CLIPluginRef
	Releases    types.OrderedMap[ReleaseCatalogConfig]
}

// captureAppends extracts the deferred slices from a freshly-loaded layer
// Config: the five `_append` companions, the CLI base list (which composes
// rather than whole-replaces), and the Release catalog entries. The captured
// slices share storage with the source Config; callers that subsequently mutate
// cfg should not also retain the captured value.
func captureAppends(cfg Config) appendSlices {
	return appendSlices{
		CatalogDirs: cfg.Workspace.CatalogDirsAppend,
		Governance:  cfg.Plugins.GovernanceAppend,
		Linter:      cfg.Plugins.LinterAppend,
		Importer:    cfg.Plugins.ImporterAppend,
		Providers:   cfg.Plugins.ProvidersAppend,
		CLI:         cfg.Plugins.CLI,
		Releases:    cfg.Workspace.Releases,
	}
}

// stripAppends zeroes the deferred fields on cfg: the five `_append`
// companions, the CLI base list and the Release catalog entries. Called on every
// layer's Config before promoting it to merged (so the overlay pass does not
// carry these through) and on the final merged Config. The CLI base is stripped
// here because its entries are captured per layer and re-composed by
// applyAppends; leaving the overlaid base in place would let the nearest layer's
// `cli` whole-replace the rest. The Release entries are stripped for the same
// reason, and for a second one: the overlay decode reads them as the file wrote
// them, relative to that file, where the captured copy is already anchored.
func stripAppends(cfg *Config) {
	cfg.Workspace.CatalogDirsAppend = nil
	cfg.Plugins.GovernanceAppend = nil
	cfg.Plugins.LinterAppend = nil
	cfg.Plugins.ImporterAppend = nil
	cfg.Plugins.ProvidersAppend = nil
	cfg.Plugins.CLI = nil
	cfg.Workspace.Releases = types.OrderedMap[ReleaseCatalogConfig]{}
}

// applyAppends concatenates each layer's deferred slices onto the corresponding
// base fields of merged. Precedence ordering is global then workspace; defaults
// never contribute (Default() leaves them nil). The empty-base case falls out
// naturally — append onto a nil slice produces the appended slice. The CLI base
// list composes here in cascade order (global entries first, then each tree
// layer's), so a CLI plugin registers everywhere it is pinned. The Release
// entries compose here too, through foldReleaseCatalogs, in the same order.
func applyAppends(merged *Config, layers ...appendSlices) {
	for _, layer := range layers {
		merged.Workspace.CatalogDirs = append(merged.Workspace.CatalogDirs, layer.CatalogDirs...)
		merged.Plugins.Governance = append(merged.Plugins.Governance, layer.Governance...)
		merged.Plugins.Linter = append(merged.Plugins.Linter, layer.Linter...)
		merged.Plugins.Importer = append(merged.Plugins.Importer, layer.Importer...)
		merged.Plugins.Providers = append(merged.Plugins.Providers, layer.Providers...)
		merged.Plugins.CLI = append(merged.Plugins.CLI, layer.CLI...)
		foldReleaseCatalogs(&merged.Workspace.Releases, layer.Releases)
	}
}

// signingPolicyAccumulator composes the org-wide signing policy across the
// config cascade. It is the deliberate, scoped exception to the whole-replace-
// per-layer rule for the two org-policy fields: `required` OR-folds (any layer
// true wins, and no layer can turn it off) and `kinds` unions (a layer can
// widen the scope, never shrink it). It is intentionally separate from
// applyAppends, which owns the list-only contract of the five `_append`
// fields — the signing policy is not an `_append` field and does not share that
// path.
//
// Because OR and union are idempotent, folding a layer in more than once is
// harmless. Callers add every contributing layer (global, each tree file, each
// CI overlay) and finally the post-environment merged block, then read the
// composed result back onto the merged Config.
type signingPolicyAccumulator struct {
	required bool
	kinds    []string
	seen     map[string]struct{}
}

// add folds one layer's signing policy into the accumulator: OR for required,
// order-preserving deduplicated union for kinds. The kinds order is the order
// of first appearance across the layers added, so the composed list is
// deterministic for a given cascade.
func (a *signingPolicyAccumulator) add(s SigningConfig) {
	if s.Required {
		a.required = true
	}
	for _, k := range s.Kinds {
		if a.seen == nil {
			a.seen = make(map[string]struct{})
		}
		if _, dup := a.seen[k]; dup {
			continue
		}
		a.seen[k] = struct{}{}
		a.kinds = append(a.kinds, k)
	}
}

// composeInto folds cfg's current signing block into the accumulator — so an
// environment overlay applied on top of the cascade participates in the same
// monotonic compose — and then writes the composed policy back onto cfg,
// replacing only the two policy fields. The endpoint fields (FulcioURL and the
// rest) are left as the layer cascade resolved them.
func (a *signingPolicyAccumulator) composeInto(cfg *Config) {
	a.add(cfg.Signing)
	cfg.Signing.Required = a.required
	cfg.Signing.Kinds = a.kinds
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
