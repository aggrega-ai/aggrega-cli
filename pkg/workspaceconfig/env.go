// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"strings"
)

// envBindingKind classifies how an environment value is parsed before it
// overlays its Config field: a verbatim string, a boolean (true/false/1/0),
// or an OS-path-list that splits on the platform separator and replaces the
// list field.
type envBindingKind int

const (
	envKindString envBindingKind = iota
	envKindBool
	envKindPathList
)

// envBinding is one row of the environment-overlay table: the AGGREGA_-prefixed
// variable name, the config path it mirrors (dot-separated, mapped 1:1 to the
// variable name upper-snake-cased), its parse kind, and the apply function that
// writes a SET value onto the Config. A variable participates in the overlay if
// and only if it has a row here — this table is the single enumeration of every
// config-backed environment override the product honors, so there is no
// scattered environment read across the resolution path.
type envBinding struct {
	envVar     string
	configPath string
	kind       envBindingKind
	apply      func(cfg *Config, raw string) error
	// shortName marks a deliberate exception to the AGGREGA_ + full-config-path
	// naming convention: the variable uses a shortened, more ergonomic name.
	// The naming guard skips the path-equality check for such a row (the
	// AGGREGA_ prefix and the character class still hold).
	shortName bool
}

// envBindings is the ordered single-source table of config-backed environment
// overrides. Order is fixed and deterministic: the overlay walks it in this
// order, and the "loaded from environment" breadcrumb reports contributing
// variables in this order, so identical inputs always produce identical output
// (no map iteration anywhere on this path).
//
// Each row's envVar equals AGGREGA_ followed by its configPath upper-snake-cased
// (dots become underscores); the naming convention is mechanically pinned by a
// package guard test. Per-registry settings are deliberately absent — an array
// of hosts does not express as a single variable — so they remain config-only.
var envBindings = []envBinding{
	{
		// Taken as written here and judged where the default registry is resolved,
		// after this overlay: the value may name a registries entry, which any
		// layer may declare, so it is read the way a configuration file's value is
		// read — resolved to the entry's host, or held to the reading of a
		// registry location — and a value that is neither fails loudly, naming the
		// variable, rather than reaching the location binding and the trust
		// lookups.
		envVar:     "AGGREGA_DEFAULT_REGISTRY",
		configPath: "default_registry",
		kind:       envKindString,
		apply:      func(cfg *Config, raw string) error { cfg.DefaultRegistry = raw; return nil },
	},
	{
		envVar:     "AGGREGA_OFFLINE",
		configPath: OfflineSettingKey,
		kind:       envKindBool,
		apply:      func(cfg *Config, raw string) error { return applyBool(raw, &cfg.Offline) },
	},
	{
		envVar:     "AGGREGA_AUTO_PULL",
		configPath: "auto_pull",
		kind:       envKindBool,
		apply:      func(cfg *Config, raw string) error { return applyBool(raw, &cfg.AutoPull) },
	},
	{
		// Validated here so a value the setting cannot hold fails loudly, naming
		// the variable, exactly as a config file does — the setting is a
		// workspace-relative subdirectory on every layer that can carry it.
		envVar:     "AGGREGA_ARTIFACT_CACHE_DIR",
		configPath: "artifact_cache_dir",
		kind:       envKindString,
		apply: func(cfg *Config, raw string) error {
			if err := validateArtifactCacheDir(raw, EnvironmentVariableWords("AGGREGA_ARTIFACT_CACHE_DIR")); err != nil {
				return err
			}
			cfg.ArtifactCacheDir = raw
			return nil
		},
	},
	{
		// The workspace.catalog_dirs path drops its workspace. prefix here: the
		// catalog directories are the only workspace-block setting, and the
		// PATH-paralleling AGGREGA_CATALOG_DIRS is the ergonomic name. A
		// deliberate exception to the full-path naming convention.
		envVar:     "AGGREGA_CATALOG_DIRS",
		configPath: "workspace.catalog_dirs",
		kind:       envKindPathList,
		shortName:  true,
		apply: func(cfg *Config, raw string) error {
			cfg.Workspace.CatalogDirs = splitPathList(raw)
			return nil
		},
	},
	{
		envVar:     "AGGREGA_SIGNING_FULCIO_URL",
		configPath: "signing.fulcio_url",
		kind:       envKindString,
		apply:      func(cfg *Config, raw string) error { cfg.Signing.FulcioURL = raw; return nil },
	},
	{
		envVar:     "AGGREGA_SIGNING_REKOR_URL",
		configPath: "signing.rekor_url",
		kind:       envKindString,
		apply:      func(cfg *Config, raw string) error { cfg.Signing.RekorURL = raw; return nil },
	},
	{
		envVar:     "AGGREGA_SIGNING_TIMESTAMP_URL",
		configPath: "signing.timestamp_url",
		kind:       envKindString,
		apply:      func(cfg *Config, raw string) error { cfg.Signing.TimestampURL = raw; return nil },
	},
	{
		envVar:     "AGGREGA_SKIP_SIGNATURE_VERIFICATION",
		configPath: "skip_signature_verification",
		kind:       envKindBool,
		apply:      func(cfg *Config, raw string) error { return applyBool(raw, &cfg.SkipSignatureVerification) },
	},
	{
		envVar:     "AGGREGA_SIGNING_KEY_PATH",
		configPath: "signing.key_path",
		kind:       envKindString,
		apply:      func(cfg *Config, raw string) error { cfg.Signing.KeyPath = raw; return nil },
	},
	{
		// The org signing policy is monotonic across layers, so the environment
		// value is folded (not whole-replaced) into the composed policy: it can
		// turn the requirement on but cannot turn a config-set requirement off.
		envVar:     "AGGREGA_SIGNING_REQUIRED",
		configPath: "signing.required",
		kind:       envKindBool,
		apply:      func(cfg *Config, raw string) error { return applyBool(raw, &cfg.Signing.Required) },
	},
	{
		// Folded into the composed kinds union; the environment can widen the
		// scope but never shrink it. Validated here so an unknown kind fails
		// loudly, naming the variable, exactly as a config file does.
		envVar:     "AGGREGA_SIGNING_KINDS",
		configPath: "signing.kinds",
		kind:       envKindPathList,
		apply: func(cfg *Config, raw string) error {
			kinds := splitPathList(raw)
			if err := validateSigningKinds(kinds, EnvironmentVariableWords("AGGREGA_SIGNING_KINDS")); err != nil {
				return err
			}
			cfg.Signing.Kinds = kinds
			return nil
		},
	},
}

// applyEnvOverlay overlays the SET (non-empty) environment variables from the
// binding table onto cfg, in table order, and returns the names of the
// variables that contributed (also in table order) for the "loaded from
// environment" breadcrumb. A variable that is unset or empty is a no-op
// fall-through — it leaves the value resolved by the layers below untouched
// rather than overwriting it with a zero value. A SET-but-malformed value
// (a boolean that is not true/false/1/0) is a loud error, never a silent
// coercion. look reads the environment; pass os.Getenv in production.
func applyEnvOverlay(cfg *Config, look func(string) string) ([]string, error) {
	var contributing []string
	for _, b := range envBindings {
		raw := look(b.envVar)
		if raw == "" {
			continue
		}
		if err := b.apply(cfg, raw); err != nil {
			return nil, fmt.Errorf("%s: %w", EnvironmentVariableWords(b.envVar), err)
		}
		contributing = append(contributing, b.envVar)
	}
	return contributing, nil
}

// applyBool parses an environment boolean — true/false/1/0, case-insensitive —
// and writes it to dst. Any other non-empty value is rejected: yaml.v3 treats
// yes/no/on/off as strings rather than booleans, so the environment surface
// accepts only the unambiguous spellings and refuses the rest loudly rather
// than guessing.
func applyBool(raw string, dst *bool) error {
	switch strings.ToLower(raw) {
	case "true", "1":
		*dst = true
	case "false", "0":
		*dst = false
	default:
		return fmt.Errorf("invalid boolean %q (use true, false, 1, or 0)", raw)
	}
	return nil
}

// splitPathList splits an OS-path-list value (PATH-style, separated by the
// platform list separator) into its non-empty entries. Entries have no
// declaring config file to anchor a relative path against, so they are taken
// verbatim and should be supplied as absolute paths.
func splitPathList(raw string) []string {
	parts := strings.Split(raw, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// EnvWorkspaceRoot is the variable an operator sets to supply the workspace
// root without the --workspace-root flag. It pairs with that flag (the flag
// wins when set); the resolver consults it when no flag is given. It is not a
// Config field — it feeds root discovery — so it is applied at the command's
// root-flag fallback, not through the config overlay.
const EnvWorkspaceRoot = "AGGREGA_WORKSPACE_ROOT"

// ConfigBackedEnvVars returns the environment variables of the config-overlay
// table — the variables this package reads and applies onto the resolved
// Config. The command-layer secrets (identity token, registry password) and
// the workspace-root variable are resolved elsewhere and are not included; the
// combined enumeration for the naming and anti-drift guards lives at the
// command layer that knows all binding sites. Order is table order.
func ConfigBackedEnvVars() []string {
	out := make([]string, 0, len(envBindings))
	for _, b := range envBindings {
		out = append(out, b.envVar)
	}
	return out
}
