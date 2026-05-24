// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package workspaceconfig parses and exposes the per-workspace
// configuration file (`.aggrega-config.yaml`) that the aggrega CLI
// consults at every build invocation. The file carries four
// top-level blocks — workspace, build, plugins, lint — covering
// registry routing, build-time strictness dials, workspace-level
// Wasm plugin attachments, and per-rule lint gates.
//
// The workspace file is flat top-level (no apiVersion/kind/metadata
// envelope), distinguishing it from catalog content which carries
// the envelope. The Config type and the workspace file's wire
// format are pinned by the aggrega-config-v1alpha1 JSON Schema in
// aggrega-schemas/json-schemas/.
//
// Several documented defaults are NOT the Go zero-value:
// default_registry = "hub.aggrega.ai", cache_dir = ".aggrega/cache",
// halt_on_deprecated_severity = "error", enforce_monotonic_narrowing
// = true, and enforce_immutability = true. Callers obtain a Config
// seeded with those defaults via Default(); the loader's overlay
// pattern (Load reads file → schema validates → yaml.Unmarshal into
// a Default()-initialised value) preserves the documented defaults
// when the file omits a key.
package workspaceconfig

// Config carries the workspace's full configuration. Fields map to
// the JSON Schema property names via yaml tags (snake_case wire
// format to PascalCase Go identifiers).
//
// YAML 1.2 boolean parsing note: gopkg.in/yaml.v3 treats `yes`/`no`/
// `on`/`off` as STRINGS, not booleans (unlike YAML 1.1). A workspace
// file with `workspace.offline: yes` fails JSON Schema validation
// with "expected boolean, got string" rather than silently being
// parsed as true. Authors must use `true` / `false` for boolean
// flags.
type Config struct {
	Workspace WorkspaceConfig `yaml:"workspace"`
	Build     BuildConfig     `yaml:"build"`
	Plugins   PluginsConfig   `yaml:"plugins"`
	Lint      LintConfig      `yaml:"lint"`
}

// WorkspaceConfig groups workspace-level orientation: which registry
// the build pushes to and pulls from, where the local artifact cache
// lives, whether the build operates offline, and how CUE-module
// imports resolve to registries.
type WorkspaceConfig struct {
	DefaultRegistry string            `yaml:"default_registry"`
	CatalogDirs     []string          `yaml:"catalog_dirs"`
	CacheDir        string            `yaml:"cache_dir"`
	Offline         bool              `yaml:"offline"`
	AutoPull        bool              `yaml:"auto_pull"`
	CueRegistry     CueRegistryConfig `yaml:"cue_registry"`
}

// CueRegistryConfig routes CUE-module imports to registries.
// Default is the registry host imports resolve through when no
// override matches; Overrides applies per-prefix routing.
type CueRegistryConfig struct {
	Default   string                `yaml:"default"`
	Overrides []CueRegistryOverride `yaml:"overrides"`
}

// CueRegistryOverride routes module imports under Prefix to the
// named Registry host.
type CueRegistryOverride struct {
	Prefix   string `yaml:"prefix"`
	Registry string `yaml:"registry"`
}

// BuildConfig groups build-time strictness dials and authoring
// hints. AuthoringRelease names a Release YAML the LSP treats as
// the "current Release" for inline diagnostics; HaltOnDeprecated
// and HaltOnDeprecatedSeverity (reserved for the upcoming
// deprecation-policy rule) control how the build reacts to
// deprecated-entity consumption.
type BuildConfig struct {
	AuthoringRelease         string `yaml:"authoring_release"`
	HaltOnDeprecated         bool   `yaml:"halt_on_deprecated"`
	HaltOnDeprecatedSeverity string `yaml:"halt_on_deprecated_severity"`
}

// PluginsConfig groups workspace-level Wasm plugin attachments by
// socket. Each socket invokes its plugins' documented entry function
// per the socket contract; PluginRef.Function overrides the socket's
// default when set.
type PluginsConfig struct {
	Mutator  []PluginRef `yaml:"mutator"`
	Policy   []PluginRef `yaml:"policy"`
	Linter   []PluginRef `yaml:"linter"`
	Importer []PluginRef `yaml:"importer"`
}

// PluginRef references a Wasm plugin attached at a workspace-level
// socket. URI identifies the plugin (OCI reference, local path, or
// registry alias). Function overrides the socket's default entry
// function (mutator → mutate, policy → validate, linter → lint,
// importer → import); when omitted, the socket's documented default
// applies. The empty-string value of Function is the "use default"
// sentinel; consumers apply the per-socket default when Function == "".
type PluginRef struct {
	URI      string `yaml:"uri"`
	Function string `yaml:"function,omitempty"`
}

// LintConfig groups per-rule strictness dials. Future lint
// sub-categories (build-time discouragement, FinOps lint, etc.)
// land as peer fields alongside PublishingDiscipline.
type LintConfig struct {
	PublishingDiscipline PublishingDisciplineConfig `yaml:"publishing_discipline"`
}

// PublishingDisciplineConfig carries workspace-wide gates for the
// publishing-linter's rule set. EnforceMonotonicNarrowing and
// EnforceImmutability are workspace-wide rule gates (both default
// true). NarrowingExemptions carries per-entity escape hatches for
// legitimate narrowing cases. The remaining flags relax individual
// discouragement checks.
//
// JSON marshalling matches the schema's Policies $def in
// aggrega-schemas/json-schemas/wasm-linter-v1alpha1.schema.json.
// The publishing-linter Wasm plugin consumes a JSON object with the
// same field set; the symmetry is pinned at unit-test time by
// pkg/validator's TestPoliciesRoundTripThroughSchema. Adding or
// renaming a field here requires a coordinated update to the schema
// and to the plugin's policies.rs mirror.
type PublishingDisciplineConfig struct {
	EnforceMonotonicNarrowing     bool                 `yaml:"enforce_monotonic_narrowing"`
	EnforceImmutability           bool                 `yaml:"enforce_immutability"`
	NarrowingExemptions           []NarrowingExemption `yaml:"narrowing_exemptions"`
	AllowDynamicRefs              bool                 `yaml:"allow_dynamic_refs"`
	AllowIDOverrides              bool                 `yaml:"allow_id_overrides"`
	AllowDirectXAggregaExtensions bool                 `yaml:"allow_direct_x_aggrega_extensions"`
	AllowAggregaAIPrefix          bool                 `yaml:"allow_aggrega_ai_prefix"`
	AllowNonstandardNames         bool                 `yaml:"allow_nonstandard_names"`
	AllowCategoryMismatches       bool                 `yaml:"allow_category_mismatches"`
}

// NarrowingExemption documents a per-entity exemption from the
// narrowing-violation rule. Entity names the catalog entity; Paths
// enumerate the specific schema paths exempted (e.g.
// "spec.schema.values.primary.replicaCount"); Reason is the
// human-readable justification surfaced to reviewers.
type NarrowingExemption struct {
	Entity string   `yaml:"entity"`
	Paths  []string `yaml:"paths"`
	Reason string   `yaml:"reason"`
}

// Default returns a Config populated with the documented defaults.
// Six documented defaults are NOT the Go zero-value and are seeded
// explicitly: Workspace.DefaultRegistry = "hub.aggrega.ai",
// Workspace.CacheDir = ".aggrega/cache", Workspace.AutoPull = true,
// Build.HaltOnDeprecatedSeverity = "error",
// Lint.PublishingDiscipline.EnforceMonotonicNarrowing = true, and
// Lint.PublishingDiscipline.EnforceImmutability = true.
// Remaining fields stay at Go's zero-value (every Allow* flag false,
// every list nil, every other string empty).
//
// Callers that want a true zero-value Config use Config{} directly;
// callers that want the documented baseline use Default(). The
// loader's overlay pattern (`p := Default(); yaml.Unmarshal(data, &p)`)
// preserves these defaults when the workspace file omits a key.
func Default() Config {
	return Config{
		Workspace: WorkspaceConfig{
			DefaultRegistry: "hub.aggrega.ai",
			CacheDir:        ".aggrega/cache",
			AutoPull:        true,
		},
		Build: BuildConfig{
			HaltOnDeprecatedSeverity: "error",
		},
		Lint: LintConfig{
			PublishingDiscipline: PublishingDisciplineConfig{
				EnforceMonotonicNarrowing: true,
				EnforceImmutability:       true,
			},
		},
	}
}
