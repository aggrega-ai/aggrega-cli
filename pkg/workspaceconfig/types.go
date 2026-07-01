// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package workspaceconfig parses and exposes the per-workspace
// configuration file (`.aggrega-config.yaml`) that the aggrega CLI
// consults at every build invocation. The `workspace` block is the
// root marker — its presence marks the workspace root and it holds
// only the root-defining catalog directories. Pure settings live at
// the top level: default_registry, offline, auto_pull, and
// artifact_cache_dir, plus the build, plugins, lint, and parse_cache
// blocks — covering registry routing, build-time strictness dials,
// workspace-level Wasm plugin attachments, per-rule lint gates, and
// the daemon's parse-cache eviction caps.
//
// The workspace file is flat top-level (no apiVersion/kind/metadata
// envelope), distinguishing it from catalog content which carries
// the envelope. The Config type and the workspace file's wire
// format are pinned by the aggrega-config-v1alpha1 JSON Schema in
// aggrega-schemas/json/.
//
// Several documented defaults are NOT the Go zero-value:
// default_registry = "hub.aggrega.ai", artifact_cache_dir =
// ".aggrega/cache", halt_on_deprecated_severity = "error",
// enforce_monotonic_narrowing = true, enforce_immutability = true,
// parse_cache.max_entries = 1000, and parse_cache.max_bytes =
// 100_000_000. Callers obtain a Config seeded with those defaults via
// Default(); the loader's overlay pattern (Load reads file → schema
// validates → yaml.Unmarshal into a Default()-initialised value)
// preserves the documented defaults when the file omits a key.
//
// List-append escape hatches: six list-valued fields (catalog_dirs,
// narrowing_exemptions, and the four plugin sockets mutator/policy/
// linter/importer) carry a sibling `<field>_append` companion. At
// each precedence layer, any entries in the `_append` companion are
// concatenated onto the resolved base list — letting workspace and
// global configs compose entries rather than shadow them. Same-layer
// interaction: when both the base and its `_append` companion are
// specified at one layer, the resolved value at that layer is the
// base list followed by the append list. The `_append` fields are
// merge-time-only: LoadWithGlobal zeroes them on the returned Config
// so downstream consumers cannot accidentally read them.
package workspaceconfig

// Config carries the workspace's full configuration. Fields map to
// the JSON Schema property names via yaml tags (snake_case wire
// format to PascalCase Go identifiers).
//
// YAML 1.2 boolean parsing note: go.yaml.in/yaml/v3 treats `yes`/`no`/
// `on`/`off` as STRINGS, not booleans (unlike YAML 1.1). A workspace
// file with `workspace.offline: yes` fails JSON Schema validation
// with "expected boolean, got string" rather than silently being
// parsed as true. Authors must use `true` / `false` for boolean
// flags.
type Config struct {
	// Workspace is the root marker: its presence marks the workspace
	// root, and it carries only the root-defining catalog directories.
	Workspace WorkspaceConfig `yaml:"workspace"`
	// DefaultRegistry, Offline, AutoPull, and ArtifactCacheDir are
	// top-level (not root-defining) settings — global-capable, so they
	// cascade-merge independently of the root marker.
	DefaultRegistry  string `yaml:"default_registry"`
	Offline          bool   `yaml:"offline"`
	AutoPull         bool   `yaml:"auto_pull"`
	ArtifactCacheDir string `yaml:"artifact_cache_dir"`

	// PluginSidecarDir is an on-disk directory the plugin resolver checks
	// ahead of the local / cache / registry tiers (and behind the embedded
	// built-ins): the operator drops a .wasm there to override an embedded
	// core plugin or to supply a third-party local plugin without publishing
	// it. A relative path resolves to absolute at load against the declaring
	// config file's directory, like ArtifactCacheDir. Empty disables the
	// sidecar tier. The resolved bytes are still verified against the lockfile
	// digest pin, so a sidecar override is determinism-safe when it matches the
	// pin and halts when it does not.
	PluginSidecarDir string `yaml:"plugin_sidecar_dir"`

	Build       BuildConfig       `yaml:"build"`
	Plugins     PluginsConfig     `yaml:"plugins"`
	Lint        LintConfig        `yaml:"lint"`
	ParseCache  ParseCacheConfig  `yaml:"parse_cache"`
	Concurrency ConcurrencyConfig `yaml:"concurrency"`

	// Signing carries the global signing defaults: the keyless service
	// URLs and an optional keyed-key path. A per-registry signing block
	// overrides these for that host; both are overridden by a CLI flag.
	Signing SigningConfig `yaml:"signing,omitempty"`

	// Registries carries per-registry settings keyed by host. A registry
	// absent from this list uses the defaults: a present signature is
	// verified, and an absent signature is tolerated (except for the
	// mandatory hub baseline).
	Registries []RegistryConfig `yaml:"registries"`

	// SkipSignatureVerification is the global, loud opt-out: when true,
	// signature verification is disabled across every registry, including
	// the mandatory hub baseline. It exists for break-glass and
	// development scenarios and is never silent — every pull logs a
	// prominent warning naming that verification was globally disabled.
	// Defaults to false.
	SkipSignatureVerification bool `yaml:"skip_signature_verification"`
}

// RegistryConfig carries settings scoped to one OCI registry host,
// including its signature trust policy.
type RegistryConfig struct {
	// Host is the registry these settings apply to (e.g.
	// "hub.aggrega.ai"), matched against the host of a push/pull target.
	Host string `yaml:"host"`
	// RequiresSignatures, when true, refuses an unsigned push to this
	// registry outright and signs a push to it by default. There is no
	// override: an unsigned push to a signature-requiring registry always
	// halts. Defaults to false.
	RequiresSignatures bool `yaml:"requires_signatures,omitempty"`
	// TrustedIdentities is the OR-set of keyless signing identities
	// accepted for artifacts pulled from this registry. A signature
	// verifies when its certificate matches ANY one entry. Used by the
	// keyless verification path.
	TrustedIdentities []TrustedIdentity `yaml:"trusted_identities,omitempty"`
	// TrustedDeprecationIdentities is the OR-set of keyless identities
	// additionally accepted to sign a retroactive deprecation marker for an
	// artifact pulled from this registry. A deprecation marker is honored when
	// its signature matches the ORIGINAL artifact's signer (same-signer) OR any
	// entry here — so a registry owner or a security team can retract an
	// artifact the original publisher can no longer reach, without being
	// trusted to sign the artifacts themselves. Used only by the deprecation
	// discovery path; it never widens the trust set for artifact signatures.
	TrustedDeprecationIdentities []TrustedIdentity `yaml:"trusted_deprecation_identities,omitempty"`
	// PublicKey is a pinned cosign-compatible public-key PEM (inline) for
	// keyed-mode registries. When set, signatures from this registry are
	// verified offline against this key rather than against keyless
	// identities.
	PublicKey string `yaml:"public_key,omitempty"`
	// TrustedRoot is an optional path to a pinned Sigstore trust-root
	// bundle for air-gapped keyless verification. When set, the keyless
	// anchor is loaded from this file with no network; when empty, the
	// real public-good trust root is fetched and cached.
	TrustedRoot string `yaml:"trusted_root,omitempty"`
	// Advisory, when true, downgrades a hard verification failure for
	// this registry to a warning rather than halting the pull. The
	// verification still runs and the failure is reported; the pull
	// proceeds. Defaults to false (failures halt).
	Advisory bool `yaml:"advisory,omitempty"`
	// SkipVerification, when true, skips signature verification for this
	// registry entirely. Unlike the global opt-out, this is scoped to one
	// host. Defaults to false.
	SkipVerification bool `yaml:"skip_verification,omitempty"`
	// Signing carries the signing defaults scoped to this registry: the
	// keyless service URLs and an optional keyed-key path. It overrides the
	// global signing block for pushes to this host; a CLI flag overrides
	// both.
	Signing SigningConfig `yaml:"signing,omitempty"`
}

// SigningConfig carries the signing-side endpoint defaults: the keyless
// service URLs and an optional pointer to a keyed-key file. It is the sign
// counterpart of the verify-side trust policy and lives at both the global
// (top-level) and per-registry levels.
//
// SECURITY: this struct holds NO secret material. The OIDC identity token
// is a short-lived secret and is never a config field — it is supplied at
// runtime via a flag, the ambient OIDC environment, or an interactive flow.
// KeyPath is a PATH to a key file, never the key bytes; inline private-key
// material never appears in config. This mirrors the verify side, which
// pins an inline public_key but only ever a key PATH on the sign side.
type SigningConfig struct {
	// FulcioURL overrides the public-good Fulcio certificate-authority URL
	// for keyless signing. Empty means use the public-good default.
	FulcioURL string `yaml:"fulcio_url,omitempty"`
	// RekorURL overrides the public-good Rekor transparency-log URL for
	// keyless signing. Empty means use the public-good default.
	RekorURL string `yaml:"rekor_url,omitempty"`
	// TimestampURL overrides the public-good RFC3161 timestamp-authority URL
	// for keyless signing. Empty means use the public-good default.
	TimestampURL string `yaml:"timestamp_url,omitempty"`
	// KeyPath is a path to a held ECDSA P-256 private-key PEM file for
	// offline keyed signing. It is a POINTER to a file, never inline key
	// material. Empty selects the keyless path.
	KeyPath string `yaml:"key_path,omitempty"`
}

// ResolveSigning resolves the signing config for host by overlaying the
// per-registry signing block onto the global one, field by field: a
// non-empty per-registry value wins over the global value, and an empty
// per-registry value falls through to the global value. Public-good URL
// defaults are NOT applied here — the caller overlays CLI flags first and
// applies endpoint defaults last, so this method returns only the
// config-derived layer (per-registry > global) with empty meaning "unset".
// Host matching is exact; callers normalise the host before the lookup.
func (c Config) ResolveSigning(host string) SigningConfig {
	resolved := c.Signing
	for _, r := range c.Registries {
		if r.Host == host {
			resolved = overlaySigning(resolved, r.Signing)
			break
		}
	}
	return resolved
}

// overlaySigning returns base with each non-empty field of over taking
// precedence. An empty field in over leaves base's value in place.
func overlaySigning(base, over SigningConfig) SigningConfig {
	if over.FulcioURL != "" {
		base.FulcioURL = over.FulcioURL
	}
	if over.RekorURL != "" {
		base.RekorURL = over.RekorURL
	}
	if over.TimestampURL != "" {
		base.TimestampURL = over.TimestampURL
	}
	if over.KeyPath != "" {
		base.KeyPath = over.KeyPath
	}
	return base
}

// TrustedIdentity is one acceptable keyless signing identity: a
// certificate subject (the OIDC identity) paired with the OIDC issuer that
// vouched for it. A registry's TrustedIdentities is the OR-set a keyless
// signature is matched against.
type TrustedIdentity struct {
	// Identity is the certificate subject alternative name — the OIDC
	// identity bound into the short-lived signing certificate (e.g. a
	// maintainer email or a CI workflow URI).
	Identity string `yaml:"identity,omitempty"`
	// IdentityRegexp, when set, matches the certificate subject as a
	// regular expression instead of an exact string.
	IdentityRegexp string `yaml:"identity_regexp,omitempty"`
	// Issuer is the OIDC issuer URL that issued the identity token (e.g.
	// "https://accounts.google.com").
	Issuer string `yaml:"issuer"`
}

// RegistryRequiresSignatures reports whether the registry at host is
// configured to require signatures. An unconfigured registry (absent
// from Registries) does not require signatures. Host matching is exact;
// callers normalise the host (strip any port or path) before the lookup
// if needed.
func (c Config) RegistryRequiresSignatures(host string) bool {
	for _, r := range c.Registries {
		if r.Host == host {
			return r.RequiresSignatures
		}
	}
	return false
}

// RegistryTrustPolicy returns the resolved trust policy for host and
// whether a trust policy applies (an explicit per-registry entry, or the
// embedded hub baseline). An unconfigured non-hub host returns the zero
// RegistryConfig (Host set) and found=false: callers apply the
// default-for-unknown policy (verify-if-a-signature-is-present) to such a
// host. The hub baseline host always returns found=true with the embedded
// baseline (overridden by an operator entry when present), so its
// mandatory signature requirement holds even with no operator config. Host
// matching is exact; callers normalise the host before the lookup.
func (c Config) RegistryTrustPolicy(host string) (RegistryConfig, bool) {
	for _, r := range c.Registries {
		if r.Host == host {
			return r, true
		}
	}
	if host == HubBaselineHost {
		return DefaultBaseline(), true
	}
	return RegistryConfig{Host: host}, false
}

// HubBaselineHost is the registry whose signature baseline is mandatory
// and default-on. Pulls from this host verify signatures against the
// embedded official baseline (overridable by operator config) and refuse
// an unsigned artifact, regardless of whether the host appears in
// Registries.
const HubBaselineHost = "hub.aggrega.ai"

// DefaultBaseline returns the embedded official trust baseline for the
// aggrega hub: the OR-set of the maintainer identity and the CI
// (GitHub-Actions-OIDC) identity, signatures required. It ships with
// aggrega-cli as a distributable policy fragment rather than a hardcoded
// rule — operator config layers over it via `aggrega registry add`, and
// enterprises distribute their own baseline. The returned value is a fresh
// copy the caller may mutate.
func DefaultBaseline() RegistryConfig {
	return RegistryConfig{
		Host:               HubBaselineHost,
		RequiresSignatures: true,
		TrustedIdentities: []TrustedIdentity{
			{Identity: "m.ferrero@cognitio.it", Issuer: "https://accounts.google.com"},
			{IdentityRegexp: `^https://github\.com/aggrega-ai/`, Issuer: "https://token.actions.githubusercontent.com"},
		},
	}
}

// WorkspaceConfig is the workspace root marker. Its PRESENCE marks the
// directory as the workspace root; it carries only the root-defining
// catalog directories. Settings that are not root-defining
// (default_registry, offline, auto_pull, artifact_cache_dir) live at the
// top level on Config.
//
// CatalogDirsAppend is the merge-time companion for CatalogDirs: at
// each precedence layer the entries here are concatenated onto the
// resolved CatalogDirs list. LoadWithGlobal zeroes this field on the
// returned Config.
type WorkspaceConfig struct {
	CatalogDirs       []string `yaml:"catalog_dirs"`
	CatalogDirsAppend []string `yaml:"catalog_dirs_append"`
}

// ResolvedArtifactCacheDir returns the configured on-disk artifact cache
// directory, applying the ".aggrega/cache" default when ArtifactCacheDir
// is unset. Centralizes the default so every catalog / closure load site
// resolves the cache path identically — if the default ever moves, it
// moves here.
func (c Config) ResolvedArtifactCacheDir() string {
	if c.ArtifactCacheDir == "" {
		return ".aggrega/cache"
	}
	return c.ArtifactCacheDir
}

// BuildConfig groups build-time strictness dials and authoring
// hints. AuthoringRelease names a Release YAML the LSP treats as
// the "current Release" for inline diagnostics. HaltOnDeprecated
// and HaltOnDeprecatedSeverity control how the build reacts to
// deprecated-entity consumption: HaltOnDeprecated halts on an
// entity marked deprecated by its author (metadata.deprecated:
// true); HaltOnDeprecatedSeverity is the threshold (in the ordered
// set warning < error < retracted, default "error") at or above
// which a retroactive deprecation marker recorded in the lockfile
// halts the build rather than emitting an advisory. YAMLIndent sets
// the indentation width of every rendered YAML artifact (default 2,
// range [2,9]).
type BuildConfig struct {
	AuthoringRelease         string `yaml:"authoring_release"`
	HaltOnDeprecated         bool   `yaml:"halt_on_deprecated"`
	HaltOnDeprecatedSeverity string `yaml:"halt_on_deprecated_severity"`
	YAMLIndent               int    `yaml:"yaml_indent"`
}

// PluginsConfig groups workspace-level Wasm plugin attachments by
// socket. Each socket invokes its plugins' documented entry function
// per the socket contract; PluginRef.Function overrides the socket's
// default when set.
//
// MutatorAppend / PolicyAppend / LinterAppend / ImporterAppend are
// the merge-time companions for the four base sockets. At each
// precedence layer the entries here are concatenated onto the
// resolved base list for that socket. LoadWithGlobal zeroes these
// fields on the returned Config.
type PluginsConfig struct {
	Mutator        []PluginRef `yaml:"mutator"`
	MutatorAppend  []PluginRef `yaml:"mutator_append"`
	Policy         []PluginRef `yaml:"policy"`
	PolicyAppend   []PluginRef `yaml:"policy_append"`
	Linter         []PluginRef `yaml:"linter"`
	LinterAppend   []PluginRef `yaml:"linter_append"`
	Importer       []PluginRef `yaml:"importer"`
	ImporterAppend []PluginRef `yaml:"importer_append"`
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
// NarrowingExemptionsAppend is the merge-time companion for
// NarrowingExemptions: at each precedence layer the entries here are
// concatenated onto the resolved exemption list. LoadWithGlobal
// zeroes this field on the returned Config.
//
// JSON marshalling matches the schema's Policies $def in
// aggrega-schemas/json/wasm-linter-v1alpha1.schema.json.
// The publishing-linter Wasm plugin consumes a JSON object with the
// same field set; the symmetry is pinned at unit-test time by
// pkg/validator's TestPoliciesRoundTripThroughSchema. Adding or
// renaming a field here requires a coordinated update to the schema
// and to the plugin's policies.rs mirror.
type PublishingDisciplineConfig struct {
	EnforceMonotonicNarrowing     bool                 `yaml:"enforce_monotonic_narrowing"`
	EnforceImmutability           bool                 `yaml:"enforce_immutability"`
	NarrowingExemptions           []NarrowingExemption `yaml:"narrowing_exemptions"`
	NarrowingExemptionsAppend     []NarrowingExemption `yaml:"narrowing_exemptions_append"`
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

// ParseCacheConfig carries the daemon's parse-cache eviction caps. Both
// caps apply simultaneously — the LRU eviction loop runs until both
// the entry count and the byte total are within bounds. Either-zero
// means "unbounded on that axis", which is useful for CI scenarios
// where the cache lives only for one invocation. Distinct from
// Config.ArtifactCacheDir, the on-disk content cache.
type ParseCacheConfig struct {
	// MaxEntries caps the number of cached parses. Default 1000;
	// 0 = unbounded on the entry axis.
	MaxEntries int `yaml:"max_entries"`
	// MaxBytes caps the cumulative len(content) bytes the cache
	// holds. Default 100_000_000 (100 MB); 0 = unbounded on the
	// byte axis.
	MaxBytes int `yaml:"max_bytes"`
}

// ConcurrencyConfig carries the daemon's request-dispatch concurrency
// settings. The daemon reads requests off a single stdin stream and
// runs each through a worker pool; PoolSize sets how many worker
// goroutines drain the bounded request queue.
type ConcurrencyConfig struct {
	// PoolSize sets the number of worker goroutines servicing the
	// request queue. Default 4, which dispatches up to four requests
	// concurrently; set it to 1 to run requests serially in arrival
	// order. Values below 1 clamp to 1 at dispatch-loop startup.
	PoolSize int `yaml:"pool_size"`
}

// Default returns a Config populated with the documented defaults.
// Nine documented defaults are NOT the Go zero-value and are seeded
// explicitly: DefaultRegistry = "hub.aggrega.ai",
// ArtifactCacheDir = ".aggrega/cache", AutoPull = true,
// Build.HaltOnDeprecatedSeverity = "error",
// Lint.PublishingDiscipline.EnforceMonotonicNarrowing = true,
// Lint.PublishingDiscipline.EnforceImmutability = true,
// ParseCache.MaxEntries = 1000, ParseCache.MaxBytes = 100_000_000,
// and Concurrency.PoolSize = 4. Remaining fields stay at Go's
// zero-value (every Allow* flag false, every list nil, every other
// string empty).
//
// Callers that want a true zero-value Config use Config{} directly;
// callers that want the documented baseline use Default(). The
// loader's overlay pattern (`p := Default(); yaml.Unmarshal(data, &p)`)
// preserves these defaults when the workspace file omits a key.
func Default() Config {
	return Config{
		DefaultRegistry:  "hub.aggrega.ai",
		ArtifactCacheDir: ".aggrega/cache",
		AutoPull:         true,
		Build: BuildConfig{
			HaltOnDeprecatedSeverity: "error",
			YAMLIndent:               2,
		},
		Lint: LintConfig{
			PublishingDiscipline: PublishingDisciplineConfig{
				EnforceMonotonicNarrowing: true,
				EnforceImmutability:       true,
			},
		},
		ParseCache: ParseCacheConfig{
			MaxEntries: 1000,
			MaxBytes:   100_000_000,
		},
		Concurrency: ConcurrencyConfig{
			PoolSize: 4,
		},
	}
}
