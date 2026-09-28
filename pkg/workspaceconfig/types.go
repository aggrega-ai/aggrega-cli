// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package workspaceconfig parses and exposes the per-workspace
// configuration file (`.aggrega-config.yaml`) that the aggrega CLI
// consults at every build invocation. The `workspace` block is the
// root marker — its presence marks the workspace root and it holds
// only the root-defining catalog directories. Pure settings live at
// the top level: default_registry, offline, auto_pull, and
// artifact_cache_dir, plus the build, plugins, lint, parse_cache, and
// compiled_module_cache blocks — covering registry routing, build-time
// strictness dials, workspace-level Wasm plugin attachments (including
// the per-target provider-rendering bindings), per-rule lint gates, the
// daemon's parse-cache eviction caps, and the size cap for the on-disk
// compiled-module store.
//
// The workspace file is flat top-level (no apiVersion/kind/metadata
// envelope), distinguishing it from catalog content which carries
// the envelope. The Config type and the workspace file's wire
// format are pinned by the aggrega-config-v1alpha1 JSON Schema in
// aggrega-schemas/json/.
//
// Several documented defaults are NOT the Go zero-value:
// default_registry = "hub.aggrega.ai", artifact_cache_dir =
// ".aggrega/cache", parse_cache.max_entries = 1000,
// parse_cache.max_bytes = 100_000_000, and
// compiled_module_cache.max_bytes =
// 53_687_091_200. Callers obtain a Config seeded with those defaults via
// Default(); the loader's overlay pattern (Load reads file → schema
// validates → yaml.Unmarshal into a Default()-initialised value)
// preserves the documented defaults when the file omits a key.
//
// List-append escape hatches: five list-valued fields (catalog_dirs,
// the three plugin sockets governance/linter/importer, and the provider
// bindings) carry a sibling `<field>_append`
// companion. At
// each precedence layer, any entries in the `_append` companion are
// concatenated onto the resolved base list — letting workspace and
// global configs compose entries rather than shadow them. Same-layer
// interaction: when both the base and its `_append` companion are
// specified at one layer, the resolved value at that layer is the
// base list followed by the append list. The `_append` fields are
// merge-time-only: LoadWithGlobal zeroes them on the returned Config
// so downstream consumers cannot accidentally read them.
//
// CI-owned additive overlay: beside the hand-authored
// `.aggrega-config.yaml` at any cascade directory, a separate
// `.aggrega-config-append-ci.yaml` gives an automated pipeline a file
// it owns and may overwrite wholesale without touching the developer's
// base config. It validates against the same schema and folds
// additively: for each of the six list-valued fields, both its base
// list and its `_append` companion concatenate onto the resolved list
// in cascade order. The overlay is additive-only — it cannot remove or
// scalar-override anything the base declares, so a scalar or map field
// it carries has no additive meaning and is ignored (and named in a
// resolution warning) rather than applied. It never anchors a workspace
// root: only `.aggrega-config.yaml` with a top-level `workspace:` block
// does that.
package workspaceconfig

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Config carries the workspace's full configuration. Fields map to
// the JSON Schema property names via yaml tags (snake_case wire
// format to PascalCase Go identifiers).
//
// YAML 1.2 boolean parsing note: go.yaml.in/yaml/v3 treats `yes`/`no`/
// `on`/`off` as STRINGS, not booleans (unlike YAML 1.1). A workspace
// file carrying the top-level `offline: yes` fails JSON Schema
// validation, reporting that the value has type "string" where a
// "boolean" was wanted, rather than silently being parsed as true.
// Authors must use `true` / `false` for boolean flags. The example is
// a top-level key deliberately: a key the schema does not admit at
// the position it is written is rejected for being in the wrong
// place, which would demonstrate a different refusal from the one
// this note is about.
type Config struct {
	// Workspace is the root marker: its presence marks the workspace
	// root, and it carries only the root-defining catalog directories.
	Workspace WorkspaceConfig `yaml:"workspace"`
	// DefaultRegistry, Offline, AutoPull, and ArtifactCacheDir are
	// top-level (not root-defining) settings — global-capable, so they
	// cascade-merge independently of the root marker.
	//
	// DefaultRegistry is the default registry. A layer writes it as a registry
	// host in the spelling its trust policy is looked up under —
	// registryhost.IsHost holds for it and registryhost.Normalize leaves it
	// unchanged — optionally followed by "/" and a repository path, or as the
	// Name of an entry under Registries, which then stands for that entry's
	// Host, the name winning where it is also a host spelling; empty names no
	// default. Resolution replaces a name with its entry's Host, once, over every
	// layer, and refuses any other value, a name two entries carry, and a name
	// followed by a path; so in a resolved configuration it is a registry
	// location — a host in that one spelling, optionally followed by a
	// repository path — or empty. It is read through DefaultRegistryLocation,
	// whose Host every trust decision is keyed on and whose Prefix every
	// location through the default is composed under.
	DefaultRegistry string `yaml:"default_registry"`
	// Offline is the posture that refuses every network reach. Its
	// authored key is OfflineSettingKey, and the yaml tag here is that
	// key's definition — a struct tag cannot be written from a
	// constant, which is why the constant names itself against this
	// line rather than the other way round.
	Offline          bool   `yaml:"offline"`
	AutoPull         bool   `yaml:"auto_pull"`
	ArtifactCacheDir string `yaml:"artifact_cache_dir"`

	// PinSourcesAtPull and PinSourcesAtPush are each operation's own default for
	// whether it runs the entity author's pin step rather than halting on an
	// entity whose lock does not record the pins: pull's resolution over the
	// entities a Release builds, and push's publication of the one entity it is
	// given. Both are false by default, and a command's own --pin-sources answer
	// overrides its key in both directions — see PullPinsSources and
	// PushPinsSources, which are where that precedence is decided.
	//
	// They are two keys rather than one because they are two operations, and a
	// key's name should say which operation it governs: one key governing both
	// would span an axis its name hides. Their authored keys are
	// PinSourcesAtPullKey and PinSourcesAtPushKey, and the yaml tags here are
	// those keys' definition, as for Offline.
	PinSourcesAtPull bool `yaml:"pin_sources_at_pull"`
	PinSourcesAtPush bool `yaml:"pin_sources_at_push"`

	Build   BuildConfig   `yaml:"build"`
	Plugins PluginsConfig `yaml:"plugins"`

	Lint        LintConfig        `yaml:"lint"`
	ParseCache  ParseCacheConfig  `yaml:"parse_cache"`
	Concurrency ConcurrencyConfig `yaml:"concurrency"`
	Serve       ServeConfig       `yaml:"serve"`

	// MCP carries the Model Context Protocol server's settings for the
	// HTTP transport. Its Listen field is what selects that transport;
	// with Listen empty the server speaks over standard input and output
	// and nothing else in the block is read.
	MCP MCPConfig `yaml:"mcp"`

	// CompiledModuleCache carries the size cap for the on-disk
	// compiled-module store. That store is machine-wide rather than
	// per-workspace, so the setting is most at home in the global
	// config file; it resolves through the same cascade as every
	// other key, so a workspace file can still override it.
	CompiledModuleCache CompiledModuleCacheConfig `yaml:"compiled_module_cache"`

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
	// "hub.aggrega.ai"), matched against the host of a push/pull target
	// through registryhost.Same. A configuration file is accepted only when
	// each Host is a registry host that registryhost.Normalize leaves
	// unchanged, so the entry's key is the spelling every lookup uses.
	Host string `yaml:"host"`
	// Name is an optional friendly alias for this registry. The host is the
	// identity; Name is a convenience selector that resolves to this entry's
	// Host wherever a registry is named (for example the push --registry flag,
	// and DefaultRegistry). Empty means the entry is addressed by host only. An
	// alias is unique across entries — two entries carrying the same alias make
	// it ambiguous.
	Name string `yaml:"name,omitempty"`
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
	// SkipSignatureVerification, when true, skips signature verification for
	// this registry entirely. It is the same break-glass switch as the global
	// skip_signature_verification opt-out, scoped by placement to this one host.
	// It does NOT defeat the top-level (all-registries) required policy: an
	// org-required signature is still verified on this host regardless of this
	// switch — only the global switch can disable that. Defaults to false.
	SkipSignatureVerification bool `yaml:"skip_signature_verification,omitempty"`
	// Signing carries this registry's signing block: the keyless service URLs,
	// an optional keyed-key path, and the per-host trust stance (required and
	// kinds). The endpoint fields override the global signing block for pushes to
	// this host; a CLI flag overrides both. Required and Kinds read from
	// placement — under a registry entry they scope the one trust stance to this
	// host (publishing an in-scope unsigned artifact is refused, and consuming an
	// unsigned one is refused), the same concept the top-level signing block
	// carries for all registries.
	Signing SigningConfig `yaml:"signing,omitempty"`
}

// SigningConfig carries the signing block — the keyless service URLs, an
// optional pointer to a keyed-key file, and the trust stance (Required and
// Kinds). It lives at both the top level and under each registry entry, and
// every field reads its scope from placement: the top-level block applies to
// all registries, a per-registry block applies to that one host. Required and
// Kinds are one concept, one name, at both scopes — no separate per-registry
// lever. The endpoint fields likewise apply at both levels (a per-registry
// value overrides the top-level default for pushes to that host).
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
	// TimestampURL names an RFC3161 timestamp authority for keyless signing. It
	// has no default: empty means none is contacted, and a keyless signature's
	// trusted time then comes from its transparency-log entry.
	TimestampURL string `yaml:"timestamp_url,omitempty"`
	// KeyPath is a path to a held ECDSA P-256 private-key PEM file for
	// offline keyed signing. It is a POINTER to a file, never inline key
	// material. Empty selects the keyless path.
	KeyPath string `yaml:"key_path,omitempty"`
	// Required turns on the one trust stance for this block's scope: publishing
	// an in-scope artifact kind requires a signature (the push/sign side refuses
	// an unsigned in-scope artifact) AND consuming one requires a signature (the
	// pull/verify side refuses an unsigned in-scope artifact). Scope is read from
	// placement — on the top-level block it covers every registry; under a
	// registry entry it covers that one host. The top-level policy composes
	// monotonically across the config cascade — any layer that sets it true makes
	// it effective true, and no layer can turn it back off — so it cannot be
	// removed by an ordinary lower-precedence config edit. Defaults to false.
	Required bool `yaml:"required,omitempty"`
	// Kinds scopes Required to specific artifact kinds. Empty, or a single "all"
	// entry, means every signable kind; otherwise the stance applies to exactly
	// the listed kinds, matching the entity kind value (e.g. "Component",
	// "Plugin"). On the top-level block it composes as a union across the cascade
	// — a layer can widen the scope but never shrink it.
	Kinds []string `yaml:"kinds,omitempty"`
}

// signingKindsAll is the wildcard entry in Signing.Kinds that selects every
// pushable artifact kind. An empty Kinds list means the same thing. It is the
// single spelling of the wildcard, shared by the scope accessor and the
// kinds-validation path.
const signingKindsAll = "all"

// signingRequiredForKind reports whether a signing block requires a signature
// for the given kind. It is the single home for the required-and-in-scope
// decision, shared by the top-level (all-registries) policy and the per-registry
// policy so the scope rule lives in exactly one place regardless of where the
// block is placed.
//
// The decision is s.Required AND kind-in-scope. Scope is every signable kind
// when s.Kinds is empty or contains the "all" wildcard; otherwise it is exact
// membership in s.Kinds. kind matches the entity kind value (e.g. "Component",
// "Plugin").
func signingRequiredForKind(s SigningConfig, kind string) bool {
	if !s.Required {
		return false
	}
	if len(s.Kinds) == 0 {
		return true
	}
	for _, k := range s.Kinds {
		if k == signingKindsAll || k == kind {
			return true
		}
	}
	return false
}

// SigningRequiredForKind reports whether the top-level (all-registries) signing
// policy requires a signature for an artifact of the given kind. The top-level
// block applies to every registry; a registry's own scope is read from its
// signing block via RegistryConfig.SigningRequiredForKind. Enforcement sites
// consult these accessors rather than re-deriving the scope.
func (c Config) SigningRequiredForKind(kind string) bool {
	return signingRequiredForKind(c.Signing, kind)
}

// SigningRequiredForKind reports whether this registry's own signing block
// requires a signature for the given kind — the per-host half of the one trust
// stance, scoped to this host by placement. It is the same required/kinds
// concept the top-level policy carries for all registries.
func (r RegistryConfig) SigningRequiredForKind(kind string) bool {
	return signingRequiredForKind(r.Signing, kind)
}

// ResolveSigning resolves the signing config for host by overlaying the
// per-registry signing block onto the global one, field by field: a
// non-empty per-registry value wins over the global value, and an empty
// per-registry value falls through to the global value. Public-good URL
// defaults are NOT applied here — the caller overlays CLI flags first and
// applies endpoint defaults last, so this method returns only the
// config-derived layer (per-registry > global) with empty meaning "unset".
// host may be a registry host or a reference naming one; both it and each
// configured host are read through registryhost.Normalize, so a spelling that
// differs only in case or in an explicit default port finds the same entry.
func (c Config) ResolveSigning(host string) SigningConfig {
	resolved := c.Signing
	for _, r := range c.Registries {
		if registryhost.Same(r.Host, host) {
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

// RegistrySigningRequiredForKind reports whether the registry at host demands a
// signature for the given kind through its own signing block — the per-host half
// of the one trust stance, the same required/kinds concept the top-level policy
// carries for all registries. An unconfigured registry (absent from Registries)
// demands none. host and each configured host are compared through
// registryhost.Same, the one reading every per-registry lookup shares.
func (c Config) RegistrySigningRequiredForKind(host, kind string) bool {
	for _, r := range c.Registries {
		if registryhost.Same(r.Host, host) {
			return r.SigningRequiredForKind(kind)
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
// mandatory signature requirement holds even with no operator config.
//
// host and each configured host are compared through registryhost.Same, and
// the hub baseline is recognised the same way, so "HUB.AGGREGA.AI" and
// "hub.aggrega.ai:443" reach the hub's baseline and an entry written for a host
// applies to every spelling of that host. A configuration the resolution
// accepted holds only hosts already in that spelling; the comparison is what
// keeps the lookup right for a host an operation derived from a reference.
func (c Config) RegistryTrustPolicy(host string) (RegistryConfig, bool) {
	for _, r := range c.Registries {
		if registryhost.Same(r.Host, host) {
			return r, true
		}
	}
	if registryhost.Same(host, HubBaselineHost) {
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

// baselineJSON is the embedded official trust baseline for the aggrega hub. It
// is the single source of the trusted identities: the same document is served
// byte-for-byte as the hub's advertised trust doc (the umbrella derivation
// script copies it into the served .well-known location), so the offline
// baseline and the online advertised policy can never drift.
//
//go:embed baseline.json
var baselineJSON []byte

// baselineDoc is the small private decode target for the embedded baseline. It
// deliberately mirrors the advertised-trust wire shape (requires_signatures, the
// trusted-identity OR-set, and the deprecation-identity OR-set) rather than
// importing the registrytrust wire type, keeping this package free of that
// dependency. Do not "unify" it away against the served-document type — the
// mirror is intentional: the JSON is the single identity source, and this struct
// is only its local reader.
type baselineDoc struct {
	RequiresSignatures           bool               `json:"requires_signatures"`
	TrustedIdentities            []baselineIdentity `json:"trusted_identities"`
	TrustedDeprecationIdentities []baselineIdentity `json:"trusted_deprecation_identities"`
}

// baselineIdentity mirrors one advertised-trust identity entry: an exact subject
// or a subject regexp, paired with the OIDC issuer that vouches for it.
type baselineIdentity struct {
	Identity       string `json:"identity,omitempty"`
	IdentityRegexp string `json:"identity_regexp,omitempty"`
	Issuer         string `json:"issuer"`
}

// parsedBaseline is the decoded embedded baseline. Parsing happens once at
// package initialization; malformed embedded data is a build-time-constant
// programming error, so it panics rather than being carried as a runtime error.
var parsedBaseline = mustParseBaseline()

func mustParseBaseline() baselineDoc {
	var d baselineDoc
	if err := json.Unmarshal(baselineJSON, &d); err != nil {
		panic(fmt.Sprintf("workspaceconfig: parse embedded baseline.json: %v", err))
	}
	return d
}

// DefaultBaseline returns the embedded official trust baseline for the aggrega
// hub: the OR-set of the maintainer identities and the CI (GitHub-Actions-OIDC)
// identity, with signatures required, plus the OR-set of identities accepted to
// sign a retroactive deprecation marker. It is parsed from the official baseline
// shipped alongside aggrega-cli — a distributable policy fragment rather than a
// hardcoded rule — so operator config layers over it via `aggrega registry add`
// and enterprises distribute their own baseline. The returned value is a fresh
// copy, with fresh identity slices, that the caller may mutate.
func DefaultBaseline() RegistryConfig {
	return RegistryConfig{
		Host:                         HubBaselineHost,
		Signing:                      SigningConfig{Required: parsedBaseline.RequiresSignatures},
		TrustedIdentities:            baselineIdentities(parsedBaseline.TrustedIdentities),
		TrustedDeprecationIdentities: baselineIdentities(parsedBaseline.TrustedDeprecationIdentities),
	}
}

// baselineIdentities copies the decoded baseline identities into the config
// package's identity type, allocating a fresh slice so each DefaultBaseline call
// hands back an independently mutable copy. It returns nil for an empty input so
// an absent OR-set stays nil rather than an empty slice.
func baselineIdentities(ids []baselineIdentity) []TrustedIdentity {
	if len(ids) == 0 {
		return nil
	}
	out := make([]TrustedIdentity, 0, len(ids))
	for _, id := range ids {
		out = append(out, TrustedIdentity{
			Identity:       id.Identity,
			IdentityRegexp: id.IdentityRegexp,
			Issuer:         id.Issuer,
		})
	}
	return out
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
//
// Releases gives a Release file its own catalog directories, keyed by that
// file. On a resolved Config each key is the Release file's absolute path, with
// symbolic links resolved, and each entry's directories are absolute; the
// cascade composes the entries one Release at a time, the way it composes
// CatalogDirs and CatalogDirsAppend for the whole workspace. It is read through
// ResolveCatalogDirs and nowhere else, which is what keeps every surface that
// resolves a Release reading the same directories for it.
type WorkspaceConfig struct {
	CatalogDirs       []string                               `yaml:"catalog_dirs"`
	CatalogDirsAppend []string                               `yaml:"catalog_dirs_append"`
	Releases          types.OrderedMap[ReleaseCatalogConfig] `yaml:"releases,omitempty"`
}

// ReleaseCatalogConfig is one Release's own catalog directories: the same two
// keys the workspace block carries, scoped to that Release.
//
// CatalogDirs, when the entry writes it, replaces the workspace's list for this
// Release; nil means the entry leaves the workspace's list in place, and a
// non-nil empty list is the entry's statement that this Release reads no local
// catalog. CatalogDirsAppend is added after whichever of the two applies.
type ReleaseCatalogConfig struct {
	CatalogDirs       []string `yaml:"catalog_dirs"`
	CatalogDirsAppend []string `yaml:"catalog_dirs_append"`
}

// MarshalYAML writes an entry the way a configuration file states it: a list the
// entry does not write is left out, and an empty list it does write is written
// empty. Without this an entry keeping the workspace's list would print exactly
// as one that reads no local catalog, which are the two statements the nil-ness
// of CatalogDirs exists to keep apart.
func (e ReleaseCatalogConfig) MarshalYAML() (any, error) {
	type written struct {
		CatalogDirs       *[]string `yaml:"catalog_dirs,omitempty"`
		CatalogDirsAppend []string  `yaml:"catalog_dirs_append,omitempty"`
	}
	w := written{CatalogDirsAppend: e.CatalogDirsAppend}
	if e.CatalogDirs != nil {
		dirs := e.CatalogDirs
		w.CatalogDirs = &dirs
	}
	return w, nil
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

// FillsLockfile reports whether a build resolves the Release's dependencies
// into the lockfile before it renders, given whether this one invocation asked
// for the fill.
//
// The two inputs are ADDITIVE and neither outranks the other. AutoPull turns
// the fill on for every build in the workspace; requested turns it on for a
// single invocation without editing workspace config. Either is enough, and
// there is no input that turns it off — a workspace configured to fill is
// asking every build to fill, so an invocation that did not ask still fills
// there. That is the whole rule, and it is stated here because this is the only
// place it is decided.
//
// It lives on Config, which owns one of the two inputs, because every surface
// carrying a build composes this same decision from these same two values: the
// command line's --auto-pull flag, the daemon's autoPull request field and the
// tool protocol's auto_pull argument are one knob at three invocation sites. A
// second copy of the expression would agree with this one until a fourth input
// appeared or the precedence changed, and the agreement is exactly what would
// hide the divergence.
//
// What it deliberately does NOT decide is whether the calling surface performs
// writes at all. A surface that promises to write nothing cannot honour a fill
// whatever these two inputs say, and it refuses or suppresses on its own terms
// before reaching this — the command line refuses --dry-run paired with
// --auto-pull, and suppresses the workspace setting for that one run. Folding
// that in here would make this answer a question about the caller rather than
// about the configuration.
func (c Config) FillsLockfile(requested bool) bool {
	return requested || c.AutoPull
}

// PullPinsSources reports whether a dependency resolution runs the pin step over
// the entities it builds, given this one invocation's answer: nil when the
// invocation said nothing, or the value it gave.
//
// The invocation's answer overrides the workspace's pin_sources_at_pull in BOTH
// directions, and that is the whole rule, stated here because this is the only
// place it is decided. A workspace configured to pin still has to be able to get
// the halt back for one invocation, and one configured to halt still has to be
// able to pin once, or the setting would be a trapdoor rather than a default.
// That is also why this differs from FillsLockfile, whose two inputs are
// additive: there, no input turns the fill off.
//
// Every surface that carries a pull composes this one decision — the command
// line's --pin-sources, the daemon's pinSources request field, the tool
// protocol's pin_sources argument, and a build's own dependency fill, which is a
// pull — so none of them decides it a second way.
func (c Config) PullPinsSources(requested *bool) bool {
	return pinsSources(c.PinSourcesAtPull, requested)
}

// PushPinsSources reports whether a publication runs the pin step over the
// entity it publishes before it bundles it, given this one invocation's answer.
// It is PullPinsSources' rule applied to pin_sources_at_push: the invocation's
// answer overrides the workspace's in both directions.
func (c Config) PushPinsSources(requested *bool) bool {
	return pinsSources(c.PinSourcesAtPush, requested)
}

// pinsSources is the precedence both operations share: an invocation's own
// answer where it gave one, the workspace's setting otherwise.
func pinsSources(setting bool, requested *bool) bool {
	if requested != nil {
		return *requested
	}
	return setting
}

// BuildConfig groups build-time strictness dials and authoring
// hints. AuthoringRelease names a Release YAML the LSP treats as
// the "current Release" for inline diagnostics.
// HaltOnDeprecatedSeverity governs only whether deprecated-entity
// consumption HALTS the build; it never governs whether it is
// reported, which is unconditional. It is the threshold (in the
// ordered set warning < error < retracted) at or above which a
// retroactive deprecation marker recorded in the lockfile halts the
// build, and it is UNSET by default: an unset threshold halts on
// nothing, because a recorded marker is registry state applied after
// the pin was taken, so a build that succeeded once keeps
// succeeding, loudly advised. YAMLIndent sets the indentation width
// of every rendered YAML artifact (default 2, range [2,9]).
type BuildConfig struct {
	AuthoringRelease         string `yaml:"authoring_release"`
	HaltOnDeprecatedSeverity string `yaml:"halt_on_deprecated_severity"`
	YAMLIndent               int    `yaml:"yaml_indent"`
}

// PluginsConfig groups workspace-level Wasm plugin attachments by
// socket. Each socket invokes its plugins' documented entry function
// per the socket contract; PluginRef.Function overrides the socket's
// default when set. Providers is the peer list that binds render-engine
// capabilities to the provider-rendering plugins that serve them. Every
// socket list — the three cross-cutting sockets and the provider bindings
// — is the one unified PluginRef shape, so a single attachment reference
// serves every socket. A Governance entry additionally names the pipeline
// Role it is attached at.
//
// GovernanceAppend / LinterAppend / ImporterAppend /
// ProvidersAppend are the merge-time companions for the base lists. At
// each precedence layer the entries here are concatenated onto the
// resolved base list for that socket. LoadWithGlobal zeroes these
// fields on the returned Config.
//
// CLI is the exception to the whole-replace-per-layer base semantic the
// three cross-cutting sockets follow: it pins the CLI-extension plugins
// installed as top-level aggrega commands, and its entries COMPOSE across
// the config cascade rather than shadow. A CLI plugin pinned in the global
// config registers its command(s) everywhere; one pinned in a workspace
// config registers only in that project; both register together. Because
// the base already composes, CLI needs no `_append` companion — it is
// single-named (one `cli` key at every scope), and where the entry sits
// decides its reach.
type PluginsConfig struct {
	Governance       []PluginRef    `yaml:"governance"`
	GovernanceAppend []PluginRef    `yaml:"governance_append"`
	Linter           []PluginRef    `yaml:"linter"`
	LinterAppend     []PluginRef    `yaml:"linter_append"`
	Importer         []PluginRef    `yaml:"importer"`
	ImporterAppend   []PluginRef    `yaml:"importer_append"`
	Providers        []PluginRef    `yaml:"providers"`
	ProvidersAppend  []PluginRef    `yaml:"providers_append"`
	CLI              []CLIPluginRef `yaml:"cli"`
}

// CLIPluginRef pins one CLI-extension plugin installed as a top-level aggrega
// command: the resolved, version-pinned artifact reference the command resolves
// and runs, and the content digest of the guest bytes that reference resolved to
// at install time. Together they pin exactly which bytes the command executes;
// the digest is also the key into the on-disk describe cache that supplies the
// command's names and help without loading the guest.
type CLIPluginRef struct {
	// URI is the resolved, version-pinned artifact reference the command
	// resolves and runs (for example oci://hub.aggrega.ai/plugin/cli/greet:1.2.0),
	// pinned at install.
	URI string `yaml:"uri"`
	// Digest is the canonical content digest ("sha256:<hex>") of the guest .wasm
	// bytes URI resolved to at install. It pins the exact bytes the command runs —
	// a later resolution whose bytes hash differently halts the command — and keys
	// both the workspace cache slot the bytes are read from offline and the
	// describe cache that renders the command's help without loading the guest.
	Digest string `yaml:"digest"`
}

// ProviderTargetURIs projects the composed provider list into a
// target→URI map for the by-key provider-plugin resolution
// (ProviderPluginForTarget). The composed list carries exactly one
// entry per target — a duplicate target halts resolution before this
// projection runs — so the map never collapses two distinct bindings.
// The map is consulted strictly by key and is never ranged to produce
// rendered output, so a plain map is determinism-safe. A nil or empty
// list yields an empty map, which disables the by-target selection
// filter (every serving provider plugin is a candidate).
func (p PluginsConfig) ProviderTargetURIs() map[string]string {
	m := make(map[string]string, len(p.Providers))
	for _, ref := range p.Providers {
		m[ref.Capability] = ref.URI
	}
	return m
}

// PluginRef references a Wasm plugin attached at a workspace-level socket.
// It is the one unified attachment shape every socket uses — the three
// cross-cutting sockets (governance, linter, importer) and the
// provider-rendering bindings alike — so a single reference type serves all
// of them.
//
// URI and Capability together select the plugin, and their combination reads
// as the attachment intent:
//
//   - URI only — direct attach: the plugin at URI (a Plugin entity's bare
//     identity, a local path, or a registry alias) is attached verbatim. This
//     is the cross-cutting sockets' path: their plugins declare no served
//     capabilities, so a capability lookup would match nothing.
//   - Capability only — resolve-by-capability: the socket resolves the
//     plugin serving Capability (the bare engine name a Recipe binds through
//     spec.capability, e.g. "helm") among the catalog's serving plugins.
//   - Both — disambiguate/override: Capability names what to resolve and URI
//     selects which publisher's plugin serves it, by that plugin's bare
//     identity, in a federated catalog where more than one plugin serves the
//     capability.
//
// Function overrides the socket's default entry function (governance
// annotation-transform → transform_annotations, governance label-transform →
// mutate, governance ir-verdict → verdict_ir, governance render-verdict →
// verdict_render, linter → lint, importer → import); when omitted, the
// socket's documented default applies. The empty-string value of Function is
// the "use default" sentinel; consumers apply the per-socket default when
// Function == "". Role names the governance attachment role and is read on a
// governance entry only; it is the empty value on every other socket's
// entries. With carries optional attachment-time inputs the plugin reads; nil
// when the attachment declares none.
type PluginRef struct {
	URI        string          `yaml:"uri,omitempty"`
	Capability string          `yaml:"capability,omitempty"`
	Function   string          `yaml:"function,omitempty"`
	Role       plugintype.Role `yaml:"role,omitempty"`
	With       *PluginRefWith  `yaml:"with,omitempty"`
}

// PluginRefWith carries optional attachment-time inputs a plugin reads.
// Sources names definition file paths the host reads at compile time and
// hands to the plugin; Settings is a free-form settings object handed to the
// plugin verbatim. Both are optional and socket-agnostic — the surface is
// general so any socket may reuse it. Path resolution and reading of Sources
// is performed by the host wiring; this type only carries the authored values.
type PluginRefWith struct {
	Sources  []string       `yaml:"sources,omitempty"`
	Settings map[string]any `yaml:"settings,omitempty"`
	// PinnedSources carries source-file contents reconstructed from a lockfile
	// pin — each entry the file's base name and its bytes. It is populated only
	// when an attachment is rebuilt from the lockfile, never authored, and never
	// serialized (the yaml/json tag drops it), so a socket may read pinned
	// content rather than re-reading the host filesystem — keeping build output
	// invariant to a post-pull change to the source file.
	PinnedSources []PinnedSource `yaml:"-" json:"-"`
}

// PinnedSource is one lockfile-reconstructed source file: its base-name Path and
// the file's Content, carried verbatim. It lets a socket read the exact bytes a
// pull recorded rather than re-reading the host filesystem at build time.
type PinnedSource struct {
	Path    string
	Content string
}

// LintConfig groups per-rule strictness dials. Future lint
// sub-categories (build-time discouragement, FinOps lint, etc.)
// land as peer fields alongside PublishingDiscipline.
type LintConfig struct {
	PublishingDiscipline PublishingDisciplineConfig `yaml:"publishing_discipline"`
}

// PublishingDisciplineConfig carries workspace-wide gates for the
// publishing-linter's rule set. Each flag relaxes an individual
// discouragement check.
//
// JSON marshalling matches the schema's Policies $def in
// aggrega-schemas/json/wasm-linter-v1alpha1.schema.json.
// The publishing-linter Wasm plugin consumes a JSON object with the
// same field set; the symmetry is pinned at unit-test time by
// pkg/validator's TestPoliciesRoundTripThroughSchema. Adding or
// renaming a field here requires a coordinated update to the schema
// and to the plugin's policies.rs mirror.
type PublishingDisciplineConfig struct {
	AllowDynamicRefs              bool `yaml:"allow_dynamic_refs"`
	AllowIDOverrides              bool `yaml:"allow_id_overrides"`
	AllowDirectXAggregaExtensions bool `yaml:"allow_direct_x_aggrega_extensions"`
	AllowAggregaAIPrefix          bool `yaml:"allow_aggrega_ai_prefix"`
	AllowNonstandardNames         bool `yaml:"allow_nonstandard_names"`
	AllowCategoryMismatches       bool `yaml:"allow_category_mismatches"`
	// AllowUndeclaredRecipeProvides relaxes the check that every key a Recipe
	// publishes is declared by the Component it implements.
	AllowUndeclaredRecipeProvides bool `yaml:"allow_undeclared_recipe_provides"`
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

// CompiledModuleCacheConfig carries the size cap for the on-disk
// compiled-module store — the machine-code cache that lets a Wasm
// plugin be compiled once per machine instead of once per build.
//
// The store is shared across every workspace on the machine and grows
// only when a plugin is compiled for the first time, so the cap is a
// ceiling on accumulation rather than a per-build budget. It is applied
// once per invocation, before any plugin is loaded, by removing whole
// entries oldest-compiled first; every removed byte is recoverable by
// recompiling, so lowering the cap costs compile time and nothing else.
// Distinct from Config.ArtifactCacheDir, which holds pulled registry
// content, and from ParseCacheConfig, which caps an in-memory cache.
type CompiledModuleCacheConfig struct {
	// MaxBytes caps the bytes the compiled-module store holds on
	// disk. Default 53_687_091_200 (50 GiB); 0 = uncapped, so the
	// store grows without bound and nothing is removed automatically.
	//
	// It belongs above what one build compiles. The cap is applied
	// before a build compiles anything, so a cap below a single
	// build's working set makes every build discard and recompile
	// the same modules and the store stops saving anything. For the
	// same reason the store can sit above the cap between builds —
	// the cap is applied first and the build then writes what it
	// compiled — and is trimmed again at the start of the next one.
	MaxBytes int64 `yaml:"max_bytes"`
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

// ServeConfig carries behavior settings for the daemon (`aggrega serve`),
// the compiler-as-service consumed by IDE integrations.
type ServeConfig struct {
	// BuildOnInspect selects whether the IR-graph inspection request runs the
	// artifact-rendering stage, so the advisories `aggrega build` raises while
	// rendering also reach the editor as a second diagnostic layer. The default
	// (false) skips that stage, matching `aggrega inspect`, which does not reach
	// it either.
	//
	// It selects a stage, never a posture, and the distinction is the one a
	// reader most often gets wrong: the inspection stands down the same
	// refusing checks under either value, so this key adds none of the checks a
	// build halts on. What it adds is the advisories the rendering stage itself
	// produces.
	//
	// Nothing is written to disk under either value — no rendered artifacts, no
	// output directory, no lockfile — and the rendering stage never mutates the
	// graph, so where the inspection answers with one it is the same graph
	// either way. A serve --build-on-inspect flag overrides this key for one
	// daemon invocation.
	BuildOnInspect bool `yaml:"build_on_inspect"`

	// LintOnValidate selects whether a validation request also runs the
	// publishing-conventions check over the document it validated, so the
	// findings `aggrega lint` reports on a catalog entity reach the editor as a
	// third diagnostic layer. The default (false) runs the correctness checks
	// alone.
	//
	// It gates the per-validation layer and nothing else. The dedicated lint
	// request answers whatever the caller asks for under either value, because a
	// caller sending it has asked for the check; what this key decides is whether
	// the check also runs where nobody asked for it by name.
	//
	// Only the mode that reads local content runs on that layer, whatever this key
	// says: the pre-publication comparison consults a registry, and a check
	// running on every keystroke is exactly where a registry must not be reached.
	//
	// The findings are advisory under either value — a document that draws them
	// still validates and its Release still renders — so turning the key on adds a
	// layer and never a refusal.
	//
	// There is no flag counterpart. The dial answers a question about a
	// workspace's own content, and the person who has that question is the one
	// authoring the workspace rather than whoever launched the daemon.
	LintOnValidate bool `yaml:"lint_on_validate"`
}

// The two transports the Model Context Protocol server speaks, spelled as
// MCPConfig.Transport and the command's --transport flag take them. The
// configuration schema's enumeration for the key is a second statement of the
// same two words, held to these by a test in this package.
const (
	// MCPTransportStdio speaks the protocol over the process's standard
	// input and output. It is the default.
	MCPTransportStdio = "stdio"
	// MCPTransportHTTP serves the protocol over HTTP on the address Listen
	// names, and over HTTPS where TLS names a certificate and key.
	MCPTransportHTTP = "http"
)

// MCPConfig carries the Model Context Protocol server's settings: which
// transport it speaks, the address a listener binds, the certificate and
// key that let the process terminate TLS itself, the origins it
// accepts, the caps it applies to the traffic it takes, what it
// declares about the tokens its callers present, and which tools it
// announces.
//
// Transport decides which transport the rest configures: with it at
// MCPTransportStdio the server speaks the protocol over standard input
// and output and no listener exists for the listener fields to configure.
// AnnounceTools is read on both transports, because which tools a client
// is offered is a property of the server rather than of how a client
// reached it. Each field below says which it is. Distinct from
// ServeConfig, which carries the editor-backend daemon's settings.
type MCPConfig struct {
	// Transport is how the server is reached: MCPTransportStdio (the
	// default) or MCPTransportHTTP. A --transport flag on the command
	// overrides this key for one invocation. The configuration schema
	// holds a file to those two values when it loads.
	//
	// It is the only setting that chooses a transport. Listen names where
	// a listener binds and chooses nothing, so a configuration carrying an
	// address still speaks over standard input and output until this is
	// MCPTransportHTTP.
	Transport string `yaml:"transport"`

	// Listen is the address the listener binds when the HTTP transport is
	// chosen, in one of two forms: "host:port", or "unix:" followed by a
	// filesystem path. Empty (the default) names no address, and the HTTP
	// transport chosen with no address from this key or the --listen flag
	// is refused before anything binds. A --listen flag on the command
	// overrides this key for one invocation. Under MCPTransportStdio it is
	// not used.
	//
	// A plaintext address — one reached with neither TLS file set — is
	// permitted only on loopback or on a Unix socket, which is the shape
	// a deployment behind a terminating proxy uses. Any other plaintext
	// address is refused before anything binds. The refusal is a
	// startup-time decision rather than a schema one: the schema decides
	// the value's shape and cannot tell one address from another.
	//
	// The same two forms are the only ones accepted, certificate or not,
	// where Authorization.TrustedProxy is set, and for a different
	// reason: there the credential is a header, which is trustworthy
	// only where nothing but the proxy can set it.
	Listen string `yaml:"listen"`

	// TLS carries the certificate and key for terminating TLS inside the
	// serving process. Both unset is the plaintext case above.
	TLS MCPTLSConfig `yaml:"tls"`

	// AllowedOrigins are the origins accepted on a request's Origin
	// header. A request naming an origin absent from this list is
	// refused; one naming a listed origin, or carrying no Origin header,
	// is served. Empty (the default) therefore serves only callers that
	// send no Origin header, which is the shape a client that is not a
	// browser takes.
	AllowedOrigins []string `yaml:"allowed_origins"`

	// Limits carries the caps the listener applies to the traffic it
	// accepts.
	Limits MCPLimitsConfig `yaml:"limits"`

	// Authorization carries what this deployment declares about the
	// bearer tokens its callers present. Issuer and Resource are
	// required whenever the HTTP transport is chosen, and a listener is
	// refused before it binds without them; none of it is read while the
	// server speaks over standard input and output.
	Authorization MCPAuthorizationConfig `yaml:"authorization"`

	// DenyReach names the kinds of reach this deployment does not
	// carry, whatever a caller's token says. Every operation declares
	// how far its own effects travel, and a kind named here withdraws
	// every tool of that kind: it is absent from every listing and a
	// call to it is refused naming this setting, because no scope
	// could admit it. Empty (the default) carries whatever the
	// callers' own scopes admit.
	//
	// It is the deployment's bound on itself, sitting above the
	// per-caller bound the scopes set, which is why it holds against a
	// token carrying every scope. The values are the reach vocabulary
	// the operation set declares, held to that vocabulary by the
	// schema's enum at load and again where the listener reads them.
	//
	// Read only while an HTTP listener starts. A server speaking over
	// standard input and output reads none of this block, and there
	// the caller is the person who launched the process.
	DenyReach []string `yaml:"deny_reach"`

	// AnnounceTools names the tools this server announces, spelled as
	// the tool list reports them. Absent announces every tool the
	// server carries; a list announces exactly those; an empty list
	// announces none.
	//
	// Absent and empty are therefore different statements, which is
	// why this field carries no seeded default: nil is "whoever runs
	// this server asked for no narrowing" and an empty slice is "they
	// asked for nothing to be announced", and a default value would
	// have to be one of the two.
	//
	// It narrows the announcement alone. A tool it omits is absent from
	// every listing and refused at the call, but naming a tool lifts
	// neither DenyReach nor the scopes a caller's token carries: this
	// is a bound on the surface, not a grant of reach. A name no tool
	// answers to is refused when the server starts, because whoever
	// asked for a narrower surface would otherwise be served a
	// different one with nothing saying so.
	//
	// Read on BOTH transports, unlike every other field here. The
	// client a narrowing exists for is usually one that launched this
	// process over its own streams and caps how many tools it will
	// register, so a setting read only by a listener would not reach
	// the case it is for.
	AnnounceTools []string `yaml:"announce_tools"`

	// Health carries where the liveness and readiness checks are
	// answered. Empty (the default) answers them nowhere.
	Health MCPHealthConfig `yaml:"health"`
}

// MCPHealthConfig carries where a supervisor asks whether this process is
// alive and whether it is ready to answer a call.
//
// The checks are answered on an address of their own and never on the
// address Listen names: the endpoint refuses every request carrying no
// credential, so answering a probe there would give any caller that
// reaches it an unauthenticated reading of the process and a free way to
// make it work. The endpoint answers both paths with the same not-found
// it gives every path it does not serve.
type MCPHealthConfig struct {
	// Listen is the address the two checks are answered on, in the
	// same two forms MCPConfig's own Listen takes. Empty (the
	// default) starts no second listener and answers neither check
	// anywhere.
	//
	// Unlike MCPConfig.Listen this address carries no confinement:
	// it is refused for no address, and a deployment that puts it
	// somewhere other than loopback or a Unix socket has chosen
	// that. The checks carry no body and ask for nothing, and where
	// the operator's own interface to their process sits is theirs
	// to decide.
	Listen string `yaml:"listen"`
}

// MCPAuthorizationConfig carries what a deployment declares about the
// bearer tokens its callers present: which authorization server issues
// them, the address this deployment publishes for itself as the
// resource they are issued for, and optionally the file its
// verification keys are read from.
//
// The two addresses are published, with no credential asked for, in the
// protected-resource metadata document the listener serves — a client
// reads that document before it can hold a token, so what is there is
// meant to be known. None of it holds secret material, which is the
// same split SigningConfig draws: a token is presented by a caller on
// the request it makes and is never a configuration field, and the key
// set below holds public verification keys rather than anything that
// signs.
type MCPAuthorizationConfig struct {
	// Issuer is the authorization server that issues the tokens this
	// deployment accepts, written as that server states its own issuer
	// identifier. Empty (the default) is not a usable value: a
	// listener is refused before it binds without it.
	Issuer string `yaml:"issuer"`

	// Resource is the address this deployment publishes for itself, as
	// a caller reaches it. It is what a client hands the authorization
	// server when it asks for a token for this deployment, and the
	// path the metadata document is served at is derived from it.
	// Empty (the default) is not a usable value: a listener is refused
	// before it binds without it.
	Resource string `yaml:"resource"`

	// JWKSFile is the path to a file holding the JSON Web Key Set the
	// tokens this deployment accepts are verified against. Empty (the
	// default) reads those keys from the authorization server itself.
	// An offline workspace makes no network request and so has no other
	// way to obtain them: a listener there is refused before it binds
	// without this.
	//
	// A relative path is resolved against the directory of the config
	// file that declared it, on the same terms as MCPTLSConfig's two
	// paths and every other declaring-file-relative path the cascade
	// carries.
	JWKSFile string `yaml:"jwks_file"`

	// TrustedProxy carries the headers a proxy in front sets when that
	// proxy validates the tokens instead of this server. Its zero value
	// is a deployment that validates tokens itself, which is the
	// default.
	TrustedProxy MCPTrustedProxyConfig `yaml:"trusted_proxy"`
}

// MCPTrustedProxyConfig carries the two headers a deployment reads its
// caller's identity from where a proxy in front has already validated the
// token.
//
// It is an opt-in and it is confined: with PrincipalHeader set, the
// listen address must be loopback or a Unix socket or the listener is
// refused before anything binds. A header is trustworthy exactly as far
// as the set of callers that can set it, so a listener in this mode on an
// address the network reaches would let every caller claim to be anyone.
//
// The schema requires principal_header wherever the object is present, so
// a non-empty PrincipalHeader is what "this deployment reads its caller
// from a proxy" means, and there is no separate flag saying so.
type MCPTrustedProxyConfig struct {
	// PrincipalHeader is the header the proxy puts the caller's identity
	// in. Its value is taken exactly as written and compared to nothing.
	// Empty (the default) is a deployment that validates tokens itself.
	PrincipalHeader string `yaml:"principal_header"`
	// ScopesHeader is the header the proxy puts the caller's granted
	// scopes in, separated by spaces. Empty (the default) asserts no
	// scopes for any caller.
	ScopesHeader string `yaml:"scopes_header"`
}

// MCPTLSConfig carries the paths to the certificate chain and private key
// the serving process reads at startup to terminate TLS itself. Both are
// paths to files; neither holds key material inline, which is the same
// split SigningConfig draws between an inline public key and a key path.
//
// A relative path is resolved against the directory of the config file
// that declared it, on the same terms as every other declaring-file-
// relative path the cascade carries, so a certificate named beside the
// configuration that names it is found wherever the process was launched
// from.
type MCPTLSConfig struct {
	// CertFile is the path to the PEM certificate chain the listener
	// presents. Set together with KeyFile; empty (the default) serves
	// plaintext.
	CertFile string `yaml:"cert_file"`
	// KeyFile is the path to the PEM private key for CertFile's
	// certificate. Set together with it; empty (the default) serves
	// plaintext.
	KeyFile string `yaml:"key_file"`
}

// MCPLimitsConfig carries the caps the HTTP listener applies to the
// traffic it accepts, bounding what one caller may ask of the serving
// process so that no caller can exhaust it.
//
// No cap here has an off value, which is the deliberate difference from
// ParseCacheConfig and CompiledModuleCacheConfig: those bound a cache,
// where an unbounded setting costs memory the operator chose to spend,
// while these bound what an unauthenticated network peer can ask for
// before it is refused.
type MCPLimitsConfig struct {
	// MaxBodyBytes is the largest request body the listener accepts. A
	// larger body is refused and is not read past the cap. Default
	// 4_194_304 (4 MiB); the schema's minimum is 1.
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// MaxConcurrentRequests is how many requests the listener runs at
	// once. A request arriving while that many are in flight is refused
	// rather than queued. Default 16; the schema's minimum is 1.
	MaxConcurrentRequests int `yaml:"max_concurrent_requests"`
	// RequestsPerMinute is how many requests one caller may send in a
	// minute, counted against that caller rather than against the
	// deployment, so one caller cannot spend another's allowance. A
	// request arriving with none left is refused. Default 120; the
	// schema's minimum is 1.
	RequestsPerMinute int `yaml:"requests_per_minute"`
}

// Default returns a Config populated with the documented defaults.
// Which fields those are is stated by the body below and is deliberately
// not repeated here: a list beside the literal it describes goes stale
// at the next field that joins it, and nothing compares the two.
// Every field the body does not name stays at Go's zero-value
// (AutoPull false, every Allow* flag false, every list nil, every other
// string empty) — including Build.HaltOnDeprecatedSeverity, whose empty
// value is the documented default and means no recorded deprecation
// marker halts the build.
//
// Callers that want a true zero-value Config use Config{} directly;
// callers that want the documented baseline use Default(). The
// loader's overlay pattern (`p := Default(); yaml.Unmarshal(data, &p)`)
// preserves these defaults when the workspace file omits a key.
func Default() Config {
	return Config{
		DefaultRegistry:  "hub.aggrega.ai",
		ArtifactCacheDir: ".aggrega/cache",
		Build: BuildConfig{
			YAMLIndent: 2,
		},
		ParseCache: ParseCacheConfig{
			MaxEntries: 1000,
			MaxBytes:   100_000_000,
		},
		CompiledModuleCache: CompiledModuleCacheConfig{
			MaxBytes: 53_687_091_200,
		},
		Concurrency: ConcurrencyConfig{
			PoolSize: 4,
		},
		MCP: MCPConfig{
			Transport: MCPTransportStdio,
			Limits: MCPLimitsConfig{
				MaxBodyBytes:          4_194_304,
				MaxConcurrentRequests: 16,
				RequestsPerMinute:     120,
			},
		},
	}
}
