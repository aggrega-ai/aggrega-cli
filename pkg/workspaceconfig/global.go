// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
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
// sets to point at a non-XDG global config path.
const envVarConfigOverride = "AGGREGA_CONFIG"

// envVarXDGConfigHome names the XDG Base Directory env var that
// relocates the default XDG config root.
const envVarXDGConfigHome = "XDG_CONFIG_HOME"

// xdgConfigSubpath is the path under the resolved XDG_CONFIG_HOME
// (or ~/.config when XDG_CONFIG_HOME is unset) where the global
// config file lives.
const xdgConfigSubpath = "aggrega/config.yaml"

// GlobalConfigPath returns the path the loader consults for the
// global configuration file:
//
//  1. $AGGREGA_CONFIG if set to a non-empty value.
//  2. $XDG_CONFIG_HOME/aggrega/config.yaml if XDG_CONFIG_HOME is set.
//  3. ~/.config/aggrega/config.yaml otherwise.
//
// Returns the path and a flag indicating whether the path was
// supplied by an explicit env override (true) or the XDG default
// (false). The flag controls the missing-file disposition in
// LoadGlobal: explicit overrides treat missing files as errors;
// XDG defaults treat them as "no global config, use defaults."
//
// When no home directory can be resolved (rare; in containerized
// environments without HOME or os.UserHomeDir support), returns the
// empty string and false.
func GlobalConfigPath() (path string, explicit bool) {
	if v := os.Getenv(envVarConfigOverride); v != "" {
		return v, true
	}
	if v := os.Getenv(envVarXDGConfigHome); v != "" {
		return filepath.Join(v, xdgConfigSubpath), false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".config", xdgConfigSubpath), false
}

// LoadGlobal reads the global config file at the path returned by
// GlobalConfigPath. The bool result reports whether a file was
// actually loaded (true means a Config came from disk; false means
// the returned Config is Default()).
//
// Missing-file disposition:
//   - Explicit override via $AGGREGA_CONFIG: missing file is an
//     error (operator opted in to a path; missing is misconfiguration).
//   - XDG default path: missing file is silently ignored; Default()
//     applies, loaded=false.
//
// Malformed file (yaml parse failure or schema validation failure)
// is always an error regardless of source.
//
// A pathOverride non-empty string replaces the GlobalConfigPath
// resolution entirely — used by tests to point at fixture files
// without depending on the environment.
func LoadGlobal(pathOverride string) (cfg Config, loaded bool, err error) {
	var path string
	var explicit bool
	if pathOverride != "" {
		path = pathOverride
		explicit = true
	} else {
		path, explicit = GlobalConfigPath()
	}
	if path == "" {
		return Default(), false, nil
	}
	// Distinguish missing-file from malformed-file BEFORE calling
	// Load() — Load() swallows os.ErrNotExist (returns Default with
	// no error), which would collapse the two cases for our caller.
	if _, statErr := os.Stat(path); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			if explicit {
				return Config{}, false, fmt.Errorf("workspaceconfig: global config not found at %s: %w", path, statErr)
			}
			return Default(), false, nil
		}
		return Config{}, false, fmt.Errorf("workspaceconfig: stat global %s: %w", path, statErr)
	}
	cfg, err = Load(path)
	if err != nil {
		return Config{}, false, fmt.Errorf("workspaceconfig: load global %s: %w", path, err)
	}
	return cfg, true, nil
}

// LoadWithGlobal performs the three-level merge: built-in Default()
// → global config (if present at the resolved path) → workspace
// config (if workspacePath is non-empty AND the file exists).
//
// The merge runs by repeated yaml-overlay-into-seeded-target: each
// successive load reads the file's YAML into a target already
// carrying the prior layer's values, so absent keys preserve the
// prior layer. yaml.v3's semantics make this naturally per-field
// for scalar and map fields. List-valued fields whole-replace at
// the highest layer that supplies them.
//
// Returns the merged Config and the Source enum naming the highest
// layer that contributed.
//
// Errors:
//   - Malformed workspace or global file (schema failure, parse
//     failure) is returned as-is.
//   - Explicit $AGGREGA_CONFIG pointing at a missing file is an
//     error per LoadGlobal.
//
// globalPathOverride bypasses GlobalConfigPath resolution (test seam).
func LoadWithGlobal(workspacePath, globalPathOverride string) (Config, Source, error) {
	// Layer 1: defaults.
	merged := Default()
	source := SourceDefaults

	// Layer 2: global, if present.
	globalCfg, globalLoaded, err := LoadGlobal(globalPathOverride)
	if err != nil {
		return Config{}, SourceDefaults, err
	}
	if globalLoaded {
		merged = globalCfg
		source = SourceGlobal
	}

	// Layer 3: workspace, if path provided AND the file exists.
	if workspacePath != "" {
		if _, statErr := os.Stat(workspacePath); statErr == nil {
			// Re-load with the merged Config as the overlay target.
			// Load() seeds Default() internally; we want the global-
			// merged baseline instead. Reuse the lower-level loader
			// via a dedicated helper that takes a seeded target.
			wsCfg, err := loadOverlayInto(workspacePath, merged)
			if err != nil {
				return Config{}, SourceDefaults, err
			}
			merged = wsCfg
			source = SourceWorkspace
		}
	}

	return merged, source, nil
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
