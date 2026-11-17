// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package lockfile parses and writes the per-Release lockfile
// (`<release-stem>.lock`) that pins every URI resolved from a Release
// for deterministic future builds. The lockfile is produced by
// `aggrega pull --release <release>` and consumed by
// `aggrega build --release <release>`.
//
// Two provenance shapes capture two distinct closure-graph
// relationships:
//
//   - referenced_from (list of strings, dotted authoring-site paths)
//     appears on entities and plugins. It records where in the
//     Release / Aggregate / Manifest / .aggrega-config.yaml each URI
//     was pulled in from — answers "why is this in the closure?".
//   - consumers (list of {consumer_uri, fragments} records) appears on
//     schema_libraries, mapping_libraries, workload_libraries, and
//     artifact_libraries. It records which catalog entity references
//     which <entry-name> fragments of the library — answers "if this
//     library bumps, who is affected and at which fragments?". The
//     structured shape captures the library's fan-in (many consumers,
//     each potentially at many fragments) cleanly.
//
// The Lockfile struct field declaration order is alphabetical — with
// ArtifactLibraries the single exception, appended rather than
// inserted — so yaml.v3's marshaller (which emits struct fields in
// declaration order) produces a fixed top-level key ordering. A field
// added later goes in its alphabetical position. WriteLockfile
// further canonicalises slice ordering by URI / prefix / consumer
// URI / fragment string so two independent WriteLockfile invocations
// against the same in-memory *Lockfile produce byte-identical
// output. Pull-time determinism (same Release → same lockfile bytes
// over time) is NOT a target: generated_at records wall-clock
// provenance and intentionally changes between pulls.
//
// Two sections describe the pull rather than the closure, and a
// comparison asking whether two pulls resolved the same dependencies
// waives both through ParityWaived: generated_at, and the toolchain
// stamp, which records the compiler and core-plugin versions that
// produced the file. Neither is enforced — see ToolchainStamp for why
// the stamp in particular must not become a gate.
package lockfile

import "github.com/aggrega-ai/aggrega-cli/pkg/closure"

// Lockfile is the typed Go representation of `<release-stem>.lock`.
// Field declaration order drives yaml.v3's canonical key emission and
// is alphabetical, with ArtifactLibraries the single exception —
// appended rather than inserted, so the emitted order is fixed rather
// than strictly alphabetical. A field added later goes in its
// alphabetical position.
type Lockfile struct {
	Entities          []Entity            `yaml:"entities"`
	ExternalInputs    []ExternalInput     `yaml:"external_inputs"`
	GeneratedAt       string              `yaml:"generated_at"`
	MappingLibraries  []MappingLibrary    `yaml:"mapping_libraries"`
	Mutators          []MutatorAttachment `yaml:"mutators"`
	Plugins           []Plugin            `yaml:"plugins"`
	Providers         []ProviderBinding   `yaml:"providers"`
	Release           string              `yaml:"release"`
	ReleaseDocuments  []ReleaseDocument   `yaml:"release_documents,omitempty"`
	SchemaLibraries   []SchemaLibrary     `yaml:"schema_libraries"`
	SourceArtifacts   []SourceArtifact    `yaml:"source_artifacts"`
	Toolchain         *ToolchainStamp     `yaml:"toolchain,omitempty"`
	VerdictInputs     []VerdictInput      `yaml:"verdict_inputs"`
	Version           int                 `yaml:"version"`
	WorkloadLibraries []WorkloadLibrary   `yaml:"workload_libraries"`
	ArtifactLibraries []ArtifactLibrary   `yaml:"artifact_libraries"`
}

// Entity carries a catalog-entity pin: Component, Feature, Recipe,
// FeatureRecipe, Plugin, or Aggregate, and the Release's own Aggregate and
// Manifest. Kind and URI together are the row's identity (Key), so two entities of
// different kinds sharing a name are two rows; ResolvedVersion and Digest are the
// content-addressing pair that anchor deterministic builds. ReferencedFrom
// records the authoring-site origins that pulled the URI into closure.
type Entity struct {
	// Kind is the kind the entity was collected as: one of closure.DeclaredEntityKinds,
	// read from the collection-site hint through closure.EntityKindOf.
	Kind            string `yaml:"kind"`
	URI             string `yaml:"uri"`
	ResolvedVersion string `yaml:"resolved_version"`
	// Digest is the digest of what a publication of the entity carries: the
	// SHA-256 over its files sorted by path, each contributing its path and the
	// SHA-256 of its bytes — a document published alone as a set of one. It is
	// one subject for every entity, recorded alike by an author building from a
	// catalog directory and by a consumer building from a registry, and nothing
	// about the archive's compression or a checkout's permission bits, the
	// execute bit included, enters it.
	Digest string `yaml:"digest"`
	// ManifestDigest is the OCI manifest digest the artifact's signature
	// commits to — the verify subject. The manifest names the digest of the
	// published bytes, which is what a cached read holds a download to; Digest
	// is the entity's content, compared after that. Present only for a
	// registry-resolved entity; empty (and omitted) otherwise.
	ManifestDigest string   `yaml:"manifest_digest,omitempty"`
	ReferencedFrom []string `yaml:"referenced_from"`
	// Deprecation records a verified retroactive deprecation marker discovered
	// at pull time. Present only when a signed, trusted marker was found on the
	// artifact; empty (and omitted) otherwise. Build reads this recorded fact
	// and never touches a registry (the ahead-of-time boundary).
	Deprecation *DeprecationRecord `yaml:"deprecation,omitempty"`
	Signature   *VerifiedSignature `yaml:"signature,omitempty"`
}

// Key returns the row's identity: the kind it records and its reference. It is
// the closure's own identity type, so a row and the closure member it pins are
// joined on one key.
func (e Entity) Key() closure.EntityKey {
	return closure.EntityKey{Kind: e.Kind, URI: e.URI}
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
// digest of the signature bundle cached in the closure. Build does not
// re-run verification. It checks this fact on every row it reads — an
// entity of any kind, a library, a plugin — before anything renders, through
// the one decision whether a recorded fact is accepted
// (oci.HoldRecordedSignature): it re-hashes the cached bundle against
// BundleDigest, and, for a row the content cache served, it reads from that
// bundle which artifact the signature was made for and refuses a fact made for
// another than the one whose manifest the build read for the row — so a fact
// copied from one row onto another is refused whatever the signing
// requirement says (oci.ErrSignatureForAnotherArtifact). Where the trust
// configuration requires a signature for a row the content cache served, a
// row recording no fact refuses the build (ErrSignatureNotRecorded). A
// plugin's fact is held the same way again when its bytes are resolved, which
// is what reaches the surfaces that load a plugin outside a build.
//
// No signature is verified at build: what a build cannot tell is a bundle
// written into the cache by hand, made out for the row's own artifact, from one
// a pull verified.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical
// key emission, matching the Lockfile-wide discipline.
//
// It is the one signature-fact shape, and it is encoded in two conventions: as
// YAML in the snake_case the lockfile uses throughout — on a lockfile row, and on
// a verdict-role plugin's entry in the record of plugin identities beside the
// lockfile, which holds the same fact under the same key — and as JSON in the
// lowerCamel the local registry index uses throughout. Both encodings are declared
// here, on the single type, so the documents cannot come to describe the same
// recorded fact under different key names. Each field's optionality is decided once and expressed
// identically in both: an empty identity or issuer — a keyed signature has no
// issuer — is omitted from either encoding rather than written as an empty value.
type VerifiedSignature struct {
	// BundleDigest is the SHA-256 (sha256:...) of the cached signature
	// bundle blob stored in the closure. It names the cache blob, and on
	// every row a build reads it is what the build re-hashes that blob
	// against before reading from it which artifact the signature was made
	// for.
	BundleDigest string `yaml:"bundle_digest" json:"bundleDigest"`
	// Identity is the verified signer of the signature: the certificate
	// subject for a keyless signature, or a stable fingerprint of the
	// pinned public key for a keyed signature. It records which signer
	// matched, and is the value the same-signer check compares across a
	// plugin's entity and its .wasm artifact.
	Identity string `yaml:"identity,omitempty" json:"identity,omitempty"`
	// Issuer is the verified OIDC issuer for a keyless signature (empty
	// for keyed).
	Issuer string `yaml:"issuer,omitempty" json:"issuer,omitempty"`
	// Mode is "keyless" or "keyed" — the verification mode that admitted
	// the signature.
	Mode string `yaml:"mode" json:"mode"`
}

// ReleaseDocument records one Release file the resolution read, by content: the
// Release this lockfile belongs to, or one it extends. The section lists the
// Release first and then each Release along its extends chain, in the order the
// chain is walked, and that order is kept as recorded: it is fixed by what the
// documents themselves declare, so it is the same on every run, and it reads as
// the chain it describes.
//
// Release names which Release the lockfile belongs to; this section records what
// that Release held when it was resolved. The distinction is what lets a build
// notice an edit to the Release: the name stays the same whatever the file holds,
// while the digest moves with every byte. Everything a Release asks for is
// resolved from what it holds, so a Release edited after the pull that wrote this
// file is a Release the recorded pins may no longer describe, and the drift
// comparison reports it as one.
//
// The digest is taken over the file's bytes as they are, with nothing normalised
// away, which is the rule every other document a lockfile pins by content follows.
// A comment or a whitespace change therefore counts as a change, exactly as it
// does for the Aggregate and the Manifest the Release names.
//
// The section is omitted only from a lockfile that records no Release document at
// all. A pull always records at least the Release itself, and a build holding a
// lockfile without the section reports the Release as not recorded rather than
// passing it unchecked.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type ReleaseDocument struct {
	// Digest is the sha256:... content digest of the Release file's bytes when
	// the resolution read them.
	Digest string `yaml:"digest"`
	// Path is the Release file's path relative to the directory holding the
	// Release this lockfile belongs to, with forward slashes — never an absolute
	// host path, so the record is the same on every machine.
	Path string `yaml:"path"`
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
	URI             string `yaml:"uri"`
	ResolvedVersion string `yaml:"resolved_version"`
	Digest          string `yaml:"digest"`
	// ManifestDigest is the OCI manifest digest the library artifact's signature
	// commits to — the verify subject. It differs from Digest, the portable content
	// digest used for the cache pin and integrity check. Present only for a
	// registry-resolved library; empty (and omitted) otherwise.
	ManifestDigest string            `yaml:"manifest_digest,omitempty"`
	Consumers      []LibraryConsumer `yaml:"consumers"`
	// Deprecation records a verified retroactive deprecation marker discovered at
	// pull time. Present only when a signed, trusted marker was found on the
	// library artifact; empty (and omitted) otherwise. Build reads this recorded
	// fact and never touches a registry (the ahead-of-time boundary).
	Deprecation *DeprecationRecord `yaml:"deprecation,omitempty"`
	Signature   *VerifiedSignature `yaml:"signature,omitempty"`
}

// MappingLibrary is structurally identical to SchemaLibrary —
// declared as a distinct type so the lockfile's two library sections
// remain visibly separate at the Go API surface even though their
// shape coincides today.
type MappingLibrary struct {
	URI             string            `yaml:"uri"`
	ResolvedVersion string            `yaml:"resolved_version"`
	Digest          string            `yaml:"digest"`
	ManifestDigest  string            `yaml:"manifest_digest,omitempty"`
	Consumers       []LibraryConsumer `yaml:"consumers"`

	Deprecation *DeprecationRecord `yaml:"deprecation,omitempty"`
	Signature   *VerifiedSignature `yaml:"signature,omitempty"`
}

// WorkloadLibrary is structurally identical to SchemaLibrary and
// MappingLibrary — declared as a distinct type so the lockfile's
// library sections remain visibly separate at the Go API surface even
// though their shape coincides today.
type WorkloadLibrary struct {
	URI             string            `yaml:"uri"`
	ResolvedVersion string            `yaml:"resolved_version"`
	Digest          string            `yaml:"digest"`
	ManifestDigest  string            `yaml:"manifest_digest,omitempty"`
	Consumers       []LibraryConsumer `yaml:"consumers"`

	Deprecation *DeprecationRecord `yaml:"deprecation,omitempty"`
	Signature   *VerifiedSignature `yaml:"signature,omitempty"`
}

// ArtifactLibrary is structurally identical to SchemaLibrary,
// MappingLibrary, and WorkloadLibrary — declared as a distinct type so the
// lockfile's library sections remain visibly separate at the Go API surface
// even though their shape coincides today.
type ArtifactLibrary struct {
	URI             string            `yaml:"uri"`
	ResolvedVersion string            `yaml:"resolved_version"`
	Digest          string            `yaml:"digest"`
	ManifestDigest  string            `yaml:"manifest_digest,omitempty"`
	Consumers       []LibraryConsumer `yaml:"consumers"`

	Deprecation *DeprecationRecord `yaml:"deprecation,omitempty"`
	Signature   *VerifiedSignature `yaml:"signature,omitempty"`
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
// or FeatureRecipe `spec.source` resolution. Identity is
// {Kind, URL, Chart, ModuleSubdir, PinnedVersion} — URL is the authored
// identity URL, and the two selector terms name whatever the URL alone
// does not: Chart names the chart within a repo root, so two charts served
// from one repo root at the same version pin to distinct entries, and
// ModuleSubdir names the directory within a git-sourced repository, so two
// directories of one repository at one commit pin to distinct entries. The
// lockfile dedupes multiple Recipes pinning the same artifact into one
// entry with multiple ReferencedBy records. Digest verifies the cached
// archive; Mediatype carries the canonical type the registry advertised (for an
// OCI module, the media type of the layer the kind selects).
// ResolvedURL is provenance only (the concrete .tgz a repo-root helm
// source resolved to), never part of identity.
//
// PinnedVersion semantics vary by Kind:
//
//   - helm-oci-chart, helm-https-chart, compose-https-fragment:
//     PinnedVersion is the user-authored version (the resolver does
//     not transform it).
//     A build finds such an entry by that same authored version.
//   - git-repository: PinnedVersion is the resolved commit
//     SHA. The authored ref is preserved on each ReferencedBy entry's
//     RequestedRef field whenever the Recipe authored one; a Recipe
//     that authors no ref leaves it empty. A build finds such an
//     entry by the authored ref rather than by the commit, which is
//     what lets a Recipe name a tag and still resolve to the commit
//     that tag was pinned at.
//   - bicep-module-oci, terraform-module-oci: PinnedVersion is the
//     user-authored tag or manifest digest, recorded verbatim. A
//     terraform-module-oci entry carries the authored module_subdir
//     in ModuleSubdir when the source names one.
type SourceArtifact struct {
	Kind string `yaml:"kind"`
	// URL is the authored identity URL — spec.source.url as authored (a repo root
	// or a direct .tgz for helm-https), NOT the resolved artifact location. Build
	// reconstructs this same URL offline, so it is the stable identity the pin is
	// keyed on.
	URL string `yaml:"url"`
	// Chart names the chart within a repo-root helm source, part of the identity
	// so two charts served from one repo root at the same version pin distinctly.
	// Empty (and omitted) for a direct .tgz, OCI, compose, or git source, none of
	// which selects a chart within the URL.
	Chart string `yaml:"chart,omitempty"`
	// ModuleSubdir names the directory within a git-sourced repository or an OCI
	// module package that is the artifact, part of the identity so two directories
	// of one repository at one commit, or of one package at one version, pin
	// distinctly. Empty (and omitted) when the whole repository or package is the
	// artifact, and for every kind that reaches its artifact by URL alone.
	ModuleSubdir  string `yaml:"module_subdir,omitempty"`
	PinnedVersion string `yaml:"pinned_version"`
	Digest        string `yaml:"digest"`
	// ResolvedURL is provenance only: the concrete artifact location the authored
	// URL resolved to at pull time — a repo-root helm source's direct .tgz URL.
	// Empty (and omitted) when the resolved location equals URL.
	ResolvedURL string `yaml:"resolved_url,omitempty"`
	// ManifestDigest is the OCI manifest digest the cached archive is verified
	// against — the independent root of trust. It differs from Digest, the
	// portable content digest used for the cache pin and integrity check. Present
	// only for an OCI-distributed source (a Helm chart or an OCI module); empty (and omitted)
	// for an https or git source, which carries no manifest by construction.
	ManifestDigest string                    `yaml:"manifest_digest,omitempty"`
	Mediatype      string                    `yaml:"mediatype"`
	ReferencedBy   []SourceArtifactReference `yaml:"referenced_by"`
}

// SourceArtifactReference is a single back-pointer from a catalog
// entity (Recipe or FeatureRecipe) to a source-artifact entry.
// Entity + URI together identify the catalog entity; Field names the
// source-side path inside the entity's spec (always "spec.source" in the
// current implementation). RequestedRef preserves the Recipe's authored
// spec.source.ref, and is written whenever the Recipe authored one
// (git-repository tags/branches → commit SHA); empty when it authored none,
// which is every kind whose resolver pins the authored version verbatim.
//
// It carries both halves of what an authored reference is needed for, and
// recording it unconditionally is what keeps them from excluding each other.
// PinnedVersion is part of the identity a resolution computes, so a reference
// that moves produces a new identity rather than a changed one — the recorded
// ref is the term a later run joins on to notice that the same question now has
// a different answer (see VerifySourceReferences), and the term a build joins on
// to find this row from a document that names the reference rather than the
// commit (see FindSourceArtifact).
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

// MutatorAttachment records one transform-role governance attachment the pull
// resolved, keeping its position in the authored attachment order. A transform
// role (label-transform, which rewrites node labels; or annotation-transform,
// which injects annotations before expansion) shapes the rendered output bytes,
// so its attachment set — the ordered list, plus per attachment the role, plugin
// coordinate, and export function — is a byte-determining input the lockfile
// pins. Build and serve reconstruct the attachment list from this section rather
// than from live workspace config, so a plugins.governance transform change made
// after the pull does not move build output (the change is adopted only on the
// next pull).
//
// The plugin's content digest is NOT duplicated here — it stays single-sourced
// in the Entities and Plugins sections, which this record points into by
// identity. This section pins the attachment set, order, role, and export
// function; the .wasm bytes are pinned and drift-checked on the entity / plugin
// axes.
//
// Order is SEMANTIC: each transform runner applies its role's attachments in
// list order, so WriteLockfile preserves this slice's order — it does NOT sort
// it, unlike the URI-sorted Plugins / Providers sections.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type MutatorAttachment struct {
	// Function is the export override the attachment names; empty (and omitted)
	// selects the role's default export (mutate for label-transform,
	// transform_annotations for annotation-transform).
	Function string `yaml:"function,omitempty"`
	// Plugin is the resolved plugin coordinate — the governance Plugin entity's
	// <pluginType>/<name> identity, the same coordinate form ProviderBinding.Plugin
	// uses and the identity the Entities / Plugins sections pin under.
	Plugin string `yaml:"plugin"`
	// Role is the transform role this attachment is bound to, fixing which runner
	// (and therefore which facet) reconstruction dispatches it at (label-transform
	// to the topology-construction label runner, annotation-transform to the
	// resolution annotation
	// runner). It is recorded verbatim from the authored attachment and is
	// required: reconstruction dispatches at the exact role the attachment was
	// authored for rather than inferring one, and the schema-validating load
	// rejects an attachment that omits it.
	Role string `yaml:"role"`
	// Settings is the free-form settings object the attachment declared, pinned
	// as authored and handed to the plugin verbatim. The transform reads its
	// settings from here — never live config — so a post-pull settings change
	// does not move build output. Absent when the attachment declares none.
	Settings map[string]any `yaml:"settings,omitempty"`
	// Sources are the content-addressed pins of the attachment's declared source
	// files, in base-name order. Each pin carries the file's content so build
	// reconstructs the source the plugin reads from the lockfile rather than a
	// live host read — keeping build output invariant to a post-pull change to
	// the source file. Absent when the attachment declares none.
	Sources []MutatorSourcePin `yaml:"sources,omitempty"`
}

// VerdictInput records the external rule inputs one verdict-role governance
// attachment (ir-verdict / render-verdict) reads at compile time — a Kyverno /
// Rego rule file supplied through the attachment's with.sources. Unlike
// MutatorAttachment this is PROVENANCE, not a build input: a verdict plugin is
// cache-not-pin and reads its rule inputs LIVE (the verdict shapes no output
// bytes, so it is outside the deterministic-output pin closure). This section
// records only the content DIGEST of each rule input — never its content — so
// the inputs that determined an accept/reject decision are auditable and the
// digest that shaped a verdict is diffable across pulls: a changed rule file
// surfaces as a lockfile diff on the next pull. Build does not read this section.
//
// A verdict plugin is cache-not-pin and so is absent from the Entities / Plugins
// sections; this record identifies the attachment by its authored URI —
// self-standing provenance, not a closure join.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type VerdictInput struct {
	// Plugin is the verdict attachment's authored plugin URI, as written in
	// .aggrega-config.yaml plugins.governance. It identifies which attachment the
	// recorded rule inputs belong to; the cache-not-pin verdict plugin has no
	// entry in the Plugins / Entities sections to join to.
	Plugin string `yaml:"plugin"`
	// Role is the verdict role this attachment is bound to (ir-verdict /
	// render-verdict), distinguishing the same plugin attached at both verdict
	// stages, each potentially with its own rule inputs.
	Role string `yaml:"role"`
	// Sources are the content-digest records of the attachment's declared rule
	// inputs, in base-name order. Absent when the attachment declares none.
	Sources []VerdictSourcePin `yaml:"sources,omitempty"`
}

// VerdictSourcePin is one content-digest record for a verdict attachment's rule
// input. It carries the file's base name and the sha256:... content-address of
// its bytes at pull time — deliberately WITHOUT the content, the distinction from
// MutatorSourcePin: a verdict input is read live and recorded for audit, not
// reconstructed from the lockfile. A re-pull recomputes the digest, so a changed
// rule file surfaces as a lockfile diff.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type VerdictSourcePin struct {
	Digest string `yaml:"digest"`
	Path   string `yaml:"path"`
}

// MutatorSourcePin is one content-addressed source-file pin for a mutator
// attachment. Content carries the file's bytes verbatim (so build reconstructs
// the plugin's source input from the lockfile, never a live host read); Digest
// is the sha256:... content-address of those bytes; Path is the file's base name
// (never an absolute host path — portability and determinism). Recording the
// content alongside its digest is what makes a mutator build byte-identical
// after a post-pull change to the source file: the build reads Content, and the
// digest lets a re-pull detect that the underlying file drifted.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type MutatorSourcePin struct {
	Content string `yaml:"content"`
	Digest  string `yaml:"digest"`
	Path    string `yaml:"path"`
}

// ProviderBinding records the resolved render-engine selection for one
// capability: which provider-rendering plugin serves a capability
// (e.g. "helm"). Pull resolves the binding — from the workspace-config
// plugins.providers list when the capability is ambiguously served, or from
// the sole serving plugin when unambiguous — and records it here so the
// build selects the engine from the lockfile rather than re-reading the
// workspace config. This keeps build output invariant to a
// plugins.providers change made after the pull (the change is adopted
// only on the next pull).
//
// The plugin's content digest is NOT duplicated here — it stays
// single-sourced in the Plugins and Entities sections, which this record
// points into by identity (Plugin is the plugin's bare identity). Version
// coexistence is preserved: this record selects the plugin repository per
// capability; the concrete versions live in Entities (one entry per resolved
// plugin version), and each Recipe's spec.capability version pin selects among
// them at render.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical
// key emission, matching the Lockfile-wide discipline.
type ProviderBinding struct {
	// Capability is the capability this binding selects an engine for
	// — the bare engine name a Recipe binds through spec.capability.
	Capability string `yaml:"capability"`
	// Plugin is the derived coordinate identity of the resolved
	// provider-rendering plugin — its metadata.pluginType and bare metadata.name
	// joined as <pluginType>/<name> (e.g. "provider/helm"). The coordinate is the
	// same identity the catalog indexes under and the registry location derives
	// from; it is derived from the (pluginType, name) pair, not authored.
	Plugin string `yaml:"plugin"`
}

// ToolchainStamp records the versions of the toolchain that produced the
// lockfile: the compiler's own version and the version of each core plugin the
// compiler carries. It answers, after the fact, which compiler and which core
// plugins resolved and rendered a build — a question the pinned sections cannot
// always answer, because a core provider plugin served from inside the binary is
// left unpinned and so carries no recorded version of its own.
//
// It records VERSIONS ONLY, never where a plugin's bytes came from. A core
// plugin is published on the compiler's own tag at the same version, so the
// recorded versions read the same whether the compiler served the plugin from
// inside itself or fetched it from a registry, while a record of which of those
// happened would differ between two machines that pinned the same closure.
// Keeping the distinction is what makes the stamp admissible at all: it carries
// no machine-specific state, so it does not compromise the property that a
// committed lockfile is byte-identical across machines that resolved the same
// closure.
//
// # Not enforced, by design
//
// Nothing compares this stamp, warns about it, or refuses a build over it, and
// its absence from a lockfile is silent — no error, no diagnostic. That is a
// decision rather than an unfinished edge: the toolchain is not part of the
// dependency closure the lockfile pins, so enforcing the stamp would pin the
// compiler by proxy and impose a version lock nobody chose to adopt. A recorded
// version invites enforcement by increments — an advisory, then a warning, then a
// halt — which is why the prohibition is stated here, where a reader meets it at
// the point they would otherwise write the check, rather than left implicit in
// current behaviour.
//
// For the same reason the stamp is waived by ParityWaived, the byte-identity
// comparison between two pulls of the same inputs, on the same footing as
// GeneratedAt. A stamp that could make two such lockfiles differ would be a
// determinism defect rather than a feature.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type ToolchainStamp struct {
	// CompilerVersion is the version of the compiler that wrote the lockfile, in
	// the v-less form every surface reports (1.2.0, not v1.2.0). A build stamped
	// with a version records that version — a tagged release build its tag — and
	// a build carrying no version stamp records the "(devel)" marker, so a
	// lockfile produced by an unstamped compiler is not mistaken for release output.
	CompilerVersion string `yaml:"compiler_version"`
	// CorePlugins carries one entry per core plugin the compiler carries, ordered
	// by plugin coordinate. Empty for a build that carries no core plugins.
	CorePlugins []CorePluginVersion `yaml:"core_plugins"`
}

// CorePluginVersion is one core plugin's recorded version. Plugin is the
// coordinate identity — a Plugin entity's <pluginType>/<name> form, the same
// identity ProviderBinding.Plugin and MutatorAttachment.Plugin name a plugin by;
// Version is that plugin's version in the same v-less form CompilerVersion uses.
//
// It deliberately carries no digest and no source. A digest would duplicate what
// the Entities and Plugins sections already pin for a pinned plugin, and a source
// is the machine-specific detail ToolchainStamp exists to exclude.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type CorePluginVersion struct {
	Plugin  string `yaml:"plugin"`
	Version string `yaml:"version"`
}
