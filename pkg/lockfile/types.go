// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package lockfile parses and writes the per-Release lockfile
// (`<release-stem>.lock`) that pins every URI resolved from a Release
// for deterministic future builds. The lockfile is produced by
// `aggrega pull <release>` and consumed by `aggrega build <release>`.
//
// Two provenance shapes capture two distinct closure-graph
// relationships:
//
//   - referenced_from (list of strings, dotted authoring-site paths)
//     appears on entities and plugins. It records where in the
//     Release / Aggregate / Manifest / .aggrega-config.yaml each URI
//     was pulled in from — answers "why is this in the closure?".
//   - consumers (list of {consumer_uri, fragments} records) appears on
//     schema_libraries and mapping_libraries. It records which
//     catalog entity references which <entry-name> fragments of the
//     library — answers "if this library bumps, who is affected and
//     at which fragments?". The structured shape captures the
//     library's fan-in (many consumers, each potentially at many
//     fragments) cleanly.
//
// The Lockfile struct field declaration order is alphabetical so
// yaml.v3's marshaller (which emits struct fields in declaration
// order) produces canonical top-level key ordering. WriteLockfile
// further canonicalises slice ordering by URI / prefix / consumer
// URI / fragment string so two independent WriteLockfile invocations
// against the same in-memory *Lockfile produce byte-identical
// output. Pull-time determinism (same Release → same lockfile bytes
// over time) is NOT a target: generated_at records wall-clock
// provenance and intentionally changes between pulls.
package lockfile

// Lockfile is the typed Go representation of `<release-stem>.lock`.
// Field declaration order is alphabetical to drive yaml.v3's
// canonical key emission.
type Lockfile struct {
	Entities         []Entity          `yaml:"entities"`
	ExternalInputs   []ExternalInput   `yaml:"external_inputs"`
	GeneratedAt      string            `yaml:"generated_at"`
	MappingLibraries []MappingLibrary  `yaml:"mapping_libraries"`
	Plugins          []Plugin          `yaml:"plugins"`
	ProviderBindings []ProviderBinding `yaml:"provider_bindings"`
	Release          string            `yaml:"release"`
	SchemaLibraries  []SchemaLibrary   `yaml:"schema_libraries"`
	SourceArtifacts  []SourceArtifact  `yaml:"source_artifacts"`
	Version          int               `yaml:"version"`
}

// Entity carries a catalog-entity pin: Component, Feature, Recipe,
// FeatureRecipe, Plugin, or Aggregate. URI is the canonical
// identifier; ResolvedVersion and Digest are the content-addressing
// pair that anchor deterministic builds. ReferencedFrom records the
// authoring-site origins that pulled the URI into closure.
type Entity struct {
	URI             string `yaml:"uri"`
	ResolvedVersion string `yaml:"resolved_version"`
	Digest          string `yaml:"digest"`
	// ManifestDigest is the OCI manifest digest the artifact's signature
	// commits to — the verify subject. It differs from Digest, the portable
	// content digest used for the cache pin and integrity check. Present only
	// for a registry-resolved entity; empty (and omitted) otherwise.
	ManifestDigest string   `yaml:"manifest_digest,omitempty"`
	ReferencedFrom []string `yaml:"referenced_from"`
	// Deprecation records a verified retroactive deprecation marker discovered
	// at pull time. Present only when a signed, trusted marker was found on the
	// artifact; empty (and omitted) otherwise. Build reads this recorded fact
	// and never touches a registry (the ahead-of-time boundary).
	Deprecation *DeprecationRecord `yaml:"deprecation,omitempty"`
	Signature   *VerifiedSignature `yaml:"signature,omitempty"`
}

// DeprecationRecord records that a retroactive deprecation marker was
// discovered and its signature verified at pull time, and how. It is the
// durable answer to "is this artifact deprecated, and who said so": the
// marker's severity and message, plus the verified signer that authored the
// marker. Build reads this fact from the lockfile — it never re-discovers or
// re-verifies the marker (the ahead-of-time boundary: build is offline and
// lockfile-only).
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type DeprecationRecord struct {
	// Message is the human-readable deprecation message carried by the marker
	// — typically a migration pointer.
	Message string `yaml:"message"`
	// Severity is the marker's severity: "warning", "error", or "retracted".
	// It is the value the build-time threshold gate compares against
	// halt_on_deprecated_severity.
	Severity string `yaml:"severity"`
	// Signer is the verified signer of the marker, recorded the same way an
	// artifact signature's signer is: the certificate subject/issuer for
	// keyless, or the pinned-key fingerprint for keyed.
	Signer *VerifiedSignature `yaml:"signer,omitempty"`
}

// VerifiedSignature records that an entity's signature was verified at
// pull time, and how. It is the durable answer to "why is this entity
// trusted": the mode that verified it, the identity/issuer (keyless) or
// pinned-key fingerprint (keyed) that matched, and the content-addressed
// digest of the signature bundle cached in the closure for air-gapped
// re-verification. Build does not re-run verification; it reads this fact
// and re-verifies the cached bundle's digest only.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical
// key emission, matching the Lockfile-wide discipline.
type VerifiedSignature struct {
	// BundleDigest is the SHA-256 (sha256:...) of the cached signature
	// bundle blob stored in the closure. It both names the cache blob and
	// is the integrity gate for an air-gapped re-verify.
	BundleDigest string `yaml:"bundle_digest"`
	// Identity is the verified signer of the signature: the certificate
	// subject for a keyless signature, or a stable fingerprint of the
	// pinned public key for a keyed signature. It records which signer
	// matched, and is the value the same-signer check compares across a
	// plugin's entity and its .wasm artifact.
	Identity string `yaml:"identity,omitempty"`
	// Issuer is the verified OIDC issuer for a keyless signature (empty
	// for keyed).
	Issuer string `yaml:"issuer,omitempty"`
	// Mode is "keyless" or "keyed" — the verification mode that admitted
	// the signature.
	Mode string `yaml:"mode"`
}

// ExternalInput pins one remote input a render consumed: the URL requested
// at the render site, the SHA-256 pin that names its bytes in the
// content-addressed input store, the payload size, and the provenance of the
// consumers that required it. Acquisition writes these records; build reads
// them and serves the pinned bytes from the store without touching the
// network. For an https input the pin is the digest of the payload bytes;
// for an oci input it is the manifest digest the reference resolved to.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type ExternalInput struct {
	// RequiredBy records the render sites that requested this input.
	// WriteLockfile sorts and deduplicates the list.
	RequiredBy []string `yaml:"required_by"`
	// Sha256 is the lowercase hex SHA-256 pin. It names the store blob
	// (`sha256/<digest>`) and gates every refill of it.
	Sha256 string `yaml:"sha256"`
	// Size is the pinned payload's size in bytes.
	Size int64 `yaml:"size"`
	// URL is the input URL as authored at the render site — the lookup key
	// matching a render-time request to its pin.
	URL string `yaml:"url"`
}

// SchemaLibrary carries a SchemaLibrary pin plus a Consumers list
// recording which catalog entities reference which <entry-name>
// fragments of the library. The structured Consumers shape makes
// library-version-bump impact diffs traceable without re-running
// closure.
type SchemaLibrary struct {
	URI             string            `yaml:"uri"`
	ResolvedVersion string            `yaml:"resolved_version"`
	Digest          string            `yaml:"digest"`
	Consumers       []LibraryConsumer `yaml:"consumers"`
}

// MappingLibrary is structurally identical to SchemaLibrary —
// declared as a distinct type so the lockfile's two library sections
// remain visibly separate at the Go API surface even though their
// shape coincides today.
type MappingLibrary struct {
	URI             string            `yaml:"uri"`
	ResolvedVersion string            `yaml:"resolved_version"`
	Digest          string            `yaml:"digest"`
	Consumers       []LibraryConsumer `yaml:"consumers"`
}

// LibraryConsumer records a single catalog-graph edge from a
// consuming catalog entity to a library entry. ConsumerURI is the
// canonical URI of the consuming Component or Feature; Fragments is
// the set of <entry-name> paths that consumer references within the
// library (e.g. `#/schemas/PortSpec`). WriteLockfile sorts Fragments
// lexicographically.
type LibraryConsumer struct {
	ConsumerURI string   `yaml:"consumer_uri"`
	Fragments   []string `yaml:"fragments"`
}

// SourceArtifact carries a source-artifact pin produced from a Recipe
// or FeatureRecipe `spec.source` resolution. Identity is the quad
// {Kind, URL, Chart, PinnedVersion} — URL is the authored identity URL
// and Chart names the chart within a repo root, so two charts served
// from one repo root at the same version pin to distinct entries; the
// lockfile dedupes multiple Recipes pinning the same artifact into one
// entry with multiple ReferencedBy records. Digest verifies the cached
// archive; Mediatype carries the canonical type the registry advertised.
// ResolvedURL is provenance only (the concrete .tgz a repo-root helm
// source resolved to), never part of identity.
//
// PinnedVersion semantics vary by Kind:
//
//   - helm-oci-chart, helm-https-chart, compose-https-fragment:
//     PinnedVersion is the user-authored version (the resolver does
//     not transform it).
//   - terraform-module-git: PinnedVersion is the resolved commit
//     SHA. When the user authored a tag or branch ref, the original
//     string is preserved on each ReferencedBy entry's RequestedRef
//     field; for unambiguous SHA refs RequestedRef is empty.
//   - terraform-module-oci: reserved for the future terraform
//     provider; no adapter ships in the current implementation.
type SourceArtifact struct {
	Kind string `yaml:"kind"`
	// URL is the authored identity URL — spec.source.url as authored (a repo root
	// or a direct .tgz for helm-https), NOT the resolved artifact location. Build
	// reconstructs this same URL offline, so it is the stable identity the pin is
	// keyed on.
	URL string `yaml:"url"`
	// Chart names the chart within a repo-root helm source, part of the identity
	// so two charts served from one repo root at the same version pin distinctly.
	// Empty (and omitted) for a direct .tgz, OCI, compose, or git source.
	Chart         string `yaml:"chart,omitempty"`
	PinnedVersion string `yaml:"pinned_version"`
	Digest        string `yaml:"digest"`
	// ResolvedURL is provenance only: the concrete artifact location the authored
	// URL resolved to at pull time — a repo-root helm source's direct .tgz URL.
	// Empty (and omitted) when the resolved location equals URL.
	ResolvedURL string `yaml:"resolved_url,omitempty"`
	// ManifestDigest is the OCI manifest digest the cached archive is verified
	// against — the independent root of trust. It differs from Digest, the
	// portable content digest used for the cache pin and integrity check. Present
	// only for an OCI-distributed source (a helm-oci-chart); empty (and omitted)
	// for an https or git source, which carries no manifest by construction.
	ManifestDigest string                    `yaml:"manifest_digest,omitempty"`
	Mediatype      string                    `yaml:"mediatype"`
	ReferencedBy   []SourceArtifactReference `yaml:"referenced_by"`
}

// SourceArtifactReference is a single back-pointer from a catalog
// entity (Recipe or FeatureRecipe) to a source-artifact entry.
// Entity + URI together identify the catalog entity; Field names the
// source-side path inside the entity's spec (always "spec.source" in the
// current implementation). RequestedRef preserves the user-authored ref
// string when the resolved PinnedVersion is a transformed value
// (terraform-module-git tags/branches → commit SHA); empty for
// kinds where PinnedVersion IS the authored version.
type SourceArtifactReference struct {
	Entity       string `yaml:"entity"`
	URI          string `yaml:"uri"`
	Field        string `yaml:"field"`
	RequestedRef string `yaml:"requested_ref,omitempty"`
}

// Plugin carries a Wasm-plugin pin. ReferencedFrom records the
// authoring-site origins (e.g. `.aggrega-config.yaml.plugins.policy`,
// `recipe.<provider>.<name>.spec.pipeline`) that pulled the URI into
// closure.
type Plugin struct {
	URI             string `yaml:"uri"`
	ResolvedVersion string `yaml:"resolved_version"`
	Digest          string `yaml:"digest"`
	// ManifestDigest is the OCI manifest digest the .wasm artifact's signature
	// commits to — the verify subject. It differs from Digest, the portable
	// content digest used for the cache pin and integrity check. Present only
	// for a registry-resolved artifact; empty (and omitted) otherwise.
	ManifestDigest string   `yaml:"manifest_digest,omitempty"`
	ReferencedFrom []string `yaml:"referenced_from"`
	// Deprecation records a verified retroactive deprecation marker discovered
	// at pull time. Present only when a signed, trusted marker was found on the
	// .wasm artifact; empty (and omitted) otherwise. Build reads this recorded
	// fact and never touches a registry (the ahead-of-time boundary).
	Deprecation *DeprecationRecord `yaml:"deprecation,omitempty"`
	Signature   *VerifiedSignature `yaml:"signature,omitempty"`
}

// ProviderBinding records the resolved render-engine selection for one
// render target kind: which provider-rendering plugin renders a target
// (e.g. "helm"). Pull resolves the binding — from the workspace-config
// provider_bindings map when the target is ambiguously served, or from
// the sole serving plugin when unambiguous — and records it here so the
// build selects the engine from the lockfile rather than re-reading the
// workspace config. This keeps build output invariant to a
// provider_bindings change made after the pull (the change is adopted
// only on the next pull).
//
// The plugin's content digest is NOT duplicated here — it stays
// single-sourced in the Plugins and Entities sections, which this record
// points into by identity (Plugin is the digest-free spec.plugin OCI
// ref). Version coexistence is preserved: this record selects the plugin
// repository per target; the concrete versions live in Entities (one
// entry per resolved plugin version), and each Recipe's spec.target
// version pin selects among them at render.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical
// key emission, matching the Lockfile-wide discipline.
type ProviderBinding struct {
	// Plugin is the digest-free OCI identity of the resolved
	// provider-rendering plugin (matching a Plugin entity's spec.plugin,
	// e.g. "oci://hub.aggrega.ai/plugin/provider/helm").
	Plugin string `yaml:"plugin"`
	// Target is the render target kind this binding selects an engine for
	// — the bare engine name a Recipe binds through spec.target.
	Target string `yaml:"target"`
}
