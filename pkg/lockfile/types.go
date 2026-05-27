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
//     appears on entities and wasm_plugins. It records where in the
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
	Entities         []Entity         `yaml:"entities"`
	GeneratedAt      string           `yaml:"generated_at"`
	MappingLibraries []MappingLibrary `yaml:"mapping_libraries"`
	Release          string           `yaml:"release"`
	SchemaLibraries  []SchemaLibrary  `yaml:"schema_libraries"`
	SourceArtifacts  []SourceArtifact `yaml:"source_artifacts"`
	Version          int              `yaml:"version"`
	WasmPlugins      []WasmPlugin     `yaml:"wasm_plugins"`
}

// Entity carries a catalog-entity pin: Component, Feature, Recipe,
// FeatureRecipe, Provider, or Aggregate. URI is the canonical
// identifier; ResolvedVersion and Digest are the content-addressing
// pair that anchor deterministic builds. ReferencedFrom records the
// authoring-site origins that pulled the URI into closure.
type Entity struct {
	URI             string   `yaml:"uri"`
	ResolvedVersion string   `yaml:"resolved_version"`
	Digest          string   `yaml:"digest"`
	ResolutionPath  string   `yaml:"resolution_path"`
	ReferencedFrom  []string `yaml:"referenced_from"`
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
	ResolutionPath  string            `yaml:"resolution_path"`
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
	ResolutionPath  string            `yaml:"resolution_path"`
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
// or FeatureRecipe `spec.source` resolution. Identity is the triple
// {Kind, URL, PinnedVersion}; the lockfile dedupes multiple Recipes
// pinning the same artifact into one entry with multiple ReferencedBy
// records. Digest verifies the cached archive; Mediatype carries the
// canonical type the registry advertised.
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
	Kind          string                    `yaml:"kind"`
	URL           string                    `yaml:"url"`
	PinnedVersion string                    `yaml:"pinned_version"`
	Digest        string                    `yaml:"digest"`
	Mediatype     string                    `yaml:"mediatype"`
	ReferencedBy  []SourceArtifactReference `yaml:"referenced_by"`
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

// WasmPlugin carries a Wasm-plugin pin. ReferencedFrom records the
// authoring-site origins (e.g. `.aggrega-config.yaml.plugins.policy`,
// `recipe.<provider>.<name>.spec.pipeline`) that pulled the URI into
// closure.
type WasmPlugin struct {
	URI             string   `yaml:"uri"`
	ResolvedVersion string   `yaml:"resolved_version"`
	Digest          string   `yaml:"digest"`
	ResolutionPath  string   `yaml:"resolution_path"`
	ReferencedFrom  []string `yaml:"referenced_from"`
}

