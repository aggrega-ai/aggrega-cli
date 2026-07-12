// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"sort"

	"go.yaml.in/yaml/v3"
)

// loadCIOverlay reads a CI-owned additive overlay file, validates it against
// the same aggrega-config-v1alpha1 schema as the base config, and returns the
// typed Config together with the wire paths of any non-additive keys it
// declared. Those keys carry no additive meaning, so the caller ignores them
// and surfaces them as resolution warnings rather than applying them — the
// overlay can only add to the six list-valued fields, never scalar-override
// the base.
func loadCIOverlay(path string) (cfg Config, ignored []string, err error) {
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		return Config{}, nil, fmt.Errorf("workspaceconfig: read %s: %w", path, rerr)
	}
	if verr := validateBytes(data, path); verr != nil {
		return Config{}, nil, verr
	}
	cfg = Default()
	if uerr := yaml.Unmarshal(data, &cfg); uerr != nil {
		return Config{}, nil, fmt.Errorf("workspaceconfig: typed unmarshal %s: %w", path, uerr)
	}
	return cfg, ciNonAdditiveKeys(data), nil
}

// foldCIAdditive collapses a CI overlay's six list-valued fields into one
// append layer, folding BOTH each base list and its `_append` companion, so a
// CI author writing either form adds to the resolved list. Scalar and map
// fields on cfg are not read here — the overlay is additive-only and those
// fields are reported by ciNonAdditiveKeys instead.
func foldCIAdditive(cfg Config) appendSlices {
	pd := cfg.Lint.PublishingDiscipline
	return appendSlices{
		CatalogDirs:         concat(cfg.Workspace.CatalogDirs, cfg.Workspace.CatalogDirsAppend),
		NarrowingExemptions: concat(pd.NarrowingExemptions, pd.NarrowingExemptionsAppend),
		Mutator:             concat(cfg.Plugins.Mutator, cfg.Plugins.MutatorAppend),
		Policy:              concat(cfg.Plugins.Policy, cfg.Plugins.PolicyAppend),
		Linter:              concat(cfg.Plugins.Linter, cfg.Plugins.LinterAppend),
		Importer:            concat(cfg.Plugins.Importer, cfg.Plugins.ImporterAppend),
	}
}

// concat returns a fresh slice holding a followed by b, in that fixed order.
// A fresh allocation avoids aliasing either input's backing array, so the
// folded append layer is independent of the loaded overlay Config.
func concat[T any](a, b []T) []T {
	out := make([]T, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// ciNonAdditiveKeys reports the wire paths of every key in a CI overlay that
// carries no additive semantic, in a stable (sorted) order for deterministic
// diagnostics. Only the six list-valued fields are additive: the whole
// `plugins` block (the four sockets and their `_append` companions),
// `workspace.catalog_dirs` (+ `_append`), and
// `lint.publishing_discipline.narrowing_exemptions` (+ `_append`). Every other
// key — top-level scalars and maps, the workspace catalog-unrelated keys, the
// lint enforcement gates — is reported so the caller can warn the author that
// the overlay ignored it.
func ciNonAdditiveKeys(data []byte) []string {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil || root == nil {
		return nil
	}
	var ignored []string
	for _, k := range sortedMapKeys(root) {
		switch k {
		case "plugins":
			// Fully additive: every plugins sub-key is one of the four
			// sockets or an `_append` companion.
		case "workspace":
			ignored = append(ignored, ignoredSubKeys("workspace", root[k], "catalog_dirs", "catalog_dirs_append")...)
		case "lint":
			ignored = append(ignored, ignoredLintKeys(root[k])...)
		default:
			ignored = append(ignored, k)
		}
	}
	return ignored
}

// ignoredSubKeys reports "<parent>.<key>" for each key of node (a mapping) not
// in allowed. A non-mapping node reports nothing — schema validation already
// rejects a mistyped block, so no additive/non-additive question arises.
func ignoredSubKeys(parent string, node any, allowed ...string) []string {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	allow := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allow[a] = true
	}
	var out []string
	for _, k := range sortedMapKeys(m) {
		if !allow[k] {
			out = append(out, parent+"."+k)
		}
	}
	return out
}

// ignoredLintKeys reports the non-additive keys under a CI overlay's `lint`
// block. Only lint.publishing_discipline.narrowing_exemptions and its
// `_append` companion are additive; every other lint key (the enforcement
// gates and discouragement toggles) is reported.
func ignoredLintKeys(node any) []string {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for _, k := range sortedMapKeys(m) {
		if k != "publishing_discipline" {
			out = append(out, "lint."+k)
			continue
		}
		out = append(out, ignoredSubKeys("lint.publishing_discipline", m[k], "narrowing_exemptions", "narrowing_exemptions_append")...)
	}
	return out
}

// sortedMapKeys returns m's keys in ascending order. The sort exists only to
// make the ignored-key diagnostics deterministic; this path produces advisory
// warnings, not rendered artifacts, and there is no author-order to preserve
// (the ignored keys are an unordered set).
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
