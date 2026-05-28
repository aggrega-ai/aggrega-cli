// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "encoding/json"

// API version constant for IR documents validated against ir-v1alpha1.schema.json.
const IRAPIVersion = "aggrega.ai/v1alpha1"

// IRKind is the canonical kind value for IR documents.
const IRKind = "IR"

// IRGraph is the in-memory, target-agnostic intermediate representation emitted by the
// topology builder. It strictly mirrors ir-v1alpha1.schema.json.
type IRGraph struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   IRMetadata `json:"metadata"`
	Spec       IRSpec     `json:"spec"`
}

// IRMetadata carries document-level metadata for an IR graph.
type IRMetadata struct {
	AggregateName string `json:"aggregate_name"`
}

// IRSpec is the spec subtree of an IR document, carrying the eight
// data classes that downstream stages consume:
//
//   - Nodes / Edges — the topological graph (nodes keyed by node ID,
//     edges as a slice of directed source/target pairs).
//   - FeatureMarkers — the marker labels collected from feature
//     attachments, used by analyzer's policy-input assembly and by
//     downstream selector enforcement.
//   - ValuesMapping / InfraMapping — per-node opaque developer payloads
//     keyed by node ID, carried as json.RawMessage so the renderer
//     hands them through to provider engines without re-marshalling.
//   - FeatureValuesMapping — per-node Feature attachment map (node ID
//     → instance name → IRFeatureEntry) for the per-node Feature cascade.
//   - Secrets — per-node credential attachment map paralleling
//     FeatureValuesMapping but typed for credentials.
//   - Provenance — compiler / aggregate / release / manifest version
//     traceability for the IR build.
//
// Schema: ir-v1alpha1.schema.json#/properties/spec
type IRSpec struct {
	Nodes                OrderedMap[IRNode]                     `json:"nodes"`
	Edges                []IREdge                               `json:"edges"`
	FeatureMarkers       []string                               `json:"feature_markers"`
	ValuesMapping        OrderedMap[json.RawMessage]            `json:"values_mapping"`
	InfraMapping         OrderedMap[json.RawMessage]            `json:"infra_mapping"`
	FeatureValuesMapping OrderedMap[OrderedMap[IRFeatureEntry]] `json:"feature_values_mapping"`
	Secrets              OrderedMap[OrderedMap[IRSecretEntry]]  `json:"secrets"`
	Provenance           IRProvenance                           `json:"provenance"`
}

// IRNode is a single node in the IR graph. Purely topological — no opaque payload.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/nodes
type IRNode struct {
	Description          string             `json:"description,omitempty"`
	Labels               OrderedMap[string] `json:"labels"`
	Component            string             `json:"component"`
	ResolvedVersion      string             `json:"resolved_version"`
	SyncWave             int                `json:"sync_wave,omitempty"`
	BoundaryPath         string             `json:"boundary_path,omitempty"`
	SubsystemPath        string             `json:"subsystem_path,omitempty"`
	CatalogMetadata      *CatalogMetadata   `json:"catalog_metadata,omitempty"`
	// CatalogAnnotations, ReleaseAnnotations, and AggregateAnnotations
	// carry the renderer-facing Scalar projection of the post-cascade
	// annotation set for this IR node, partitioned by authoring side.
	// Only scalar annotation values surface in these maps; structured
	// annotation values participate in the cascade but flow through
	// Feature expansion rather than template substitution.
	//
	// Render-context template substitution sites (`.context.annotations.*`,
	// `.parent_boundary.annotations`) consume these maps directly.
	// Consumers needing the full AnnotationValue shape — per-subkey
	// routing, structured-value attribution, future programmatic
	// access to the structured payload — read fresh from the
	// authoring types (Release.Spec.Boundaries.<...>.Annotations,
	// Aggregate.Spec.Subsystems.<...>.Annotations, etc.) rather than
	// from these fields. The annotation-expansion phase does exactly
	// that.
	CatalogAnnotations   OrderedMap[string] `json:"catalog_annotations,omitempty,omitzero"`
	ReleaseAnnotations   OrderedMap[string] `json:"release_annotations,omitempty,omitzero"`
	AggregateAnnotations OrderedMap[string] `json:"aggregate_annotations,omitempty,omitzero"`

	SelfInject *InjectBlock `json:"self_inject,omitempty"` // Component-level self-inject data for Pass 2a resolution

	// SourceFile is the filesystem path of the Aggregate file where this
	// node was authored. Root-tier nodes carry the root Aggregate's path;
	// sub-aggregate nodes carry the imported Aggregate's file path. Empty
	// when topology cannot resolve a source file (synthetic test IR,
	// loaders returning empty paths). Consumers that filter diagnostics
	// per file (the daemon's analyzer-error-to-diagnostic conversion)
	// drop entries whose SourceFile names a file other than the one
	// being validated, and backfill empty values to the validated file.
	SourceFile string `json:"source_file,omitempty"`
}

// CatalogMetadata is the Enterprise Metadata Suite extracted from a catalog entity.
// Schema: ir-v1alpha1.schema.json node catalog_metadata
type CatalogMetadata struct {
	Maintainers []Maintainer `json:"maintainers,omitempty"`
	License     string       `json:"license,omitempty"`
	Repository  string       `json:"repository,omitempty"`
	Home        string       `json:"home,omitempty"`
	Keywords    []string     `json:"keywords,omitempty"`
	Deprecated  bool         `json:"deprecated,omitempty"`
}

// IREdge is a directed logical relationship between IR nodes.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/edges/items
type IREdge struct {
	Source        string                         `json:"source"`
	Target        string                         `json:"target"`
	Socket        string                         `json:"socket,omitempty"`
	Alias         string                         `json:"alias,omitempty"`
	OriginFeature string                         `json:"origin_feature,omitempty"`
	Inject        OrderedMap[OrderedMap[string]] `json:"inject,omitempty,omitzero"`
}

// IRFeatureEntry is one feature attached to a node in the feature_values_mapping.
// Schema: ir-v1alpha1.schema.json feature_values_mapping entry
//
// Origin distinguishes Feature attachments authored in an explicit
// features: block on the Aggregate, Subsystem, Boundary, or
// BoundaryComponent (Origin = "explicit") from synthetic attachments
// produced by the annotation-driven expansion pass (Origin =
// "annotation"). When values-pillar contributions and infra-pillar
// contributions on the same (Component, Feature) pair come from
// different authoring sides — or when explicit and annotation
// contributions land on the same attachment at different leaves —
// Origin widens to "mixed" and per-leaf detail lives in the two
// provenance maps below. For annotation-driven attachments,
// AnnotationKey carries the annotation name that triggered the
// expansion and AnnotationSource locates the source declaration as
// <file-path>:<dotted-yaml-key-path>. The top-level provenance
// fields populate to the singular value when every leaf shares the
// same FieldProvenance shape; when leaves diverge, the top-level
// fields stay empty and consumers must read ValuesProvenance /
// InfraProvenance per leaf. JSON tags `omitempty` keep the wire
// shape backwards-compatible — pre-provenance fixtures and golden
// files re-marshal without spurious empty-field entries.
//
// ValuesProvenance and InfraProvenance carry per-leaf FieldProvenance
// keyed by the same leaf names that appear in the Values / Infra
// OrderedMaps. The maps populate at IR-build time during the per-
// pillar per-leaf merge; downstream consumers (inspect, lint) read
// them to surface per-leaf origin markers and emit explicit-overrides-
// annotation warnings.
type IRFeatureEntry struct {
	Type             string                         `json:"type"`
	ResolvedVersion  string                         `json:"resolved_version"`
	Values           OrderedMap[any]                `json:"values,omitempty,omitzero"`
	Infra            OrderedMap[any]                `json:"infra,omitempty,omitzero"`
	Inject           OrderedMap[OrderedMap[string]] `json:"inject,omitempty,omitzero"`
	Secrets          OrderedMap[IRSecretEntry]      `json:"secrets,omitempty,omitzero"`
	Origin           string                         `json:"origin,omitempty"`
	AnnotationKey    string                         `json:"annotation_key,omitempty"`
	AnnotationSource string                         `json:"annotation_source,omitempty"`
	ValuesProvenance OrderedMap[FieldProvenance]    `json:"values_provenance,omitempty,omitzero"`
	InfraProvenance  OrderedMap[FieldProvenance]    `json:"infra_provenance,omitempty,omitzero"`
}

// FieldProvenance records the origin of one leaf in an IR feature
// attachment's values or infra pillar.
//
// Origin is "explicit" for leaves declared by an author-written
// features: block; "annotation" for leaves contributed by the
// annotation-driven expansion pass.
//
// Side is "aggregate" or "release" when Origin is "annotation",
// naming the authoring surface the annotation was declared on; empty
// when Origin is "explicit".
//
// AnnotationKey and AnnotationSource carry the triggering annotation
// for "annotation"-origin leaves: the key as declared in the manifest
// binding registry, and the source location as
// <file-path>:<dotted-yaml-key-path>. Both fields are empty for
// "explicit"-origin leaves.
//
// OverridesAnnotation is populated when an explicit contribution
// overwrites an annotation contribution at the same leaf, capturing
// the suppressed FieldProvenance so inspect rendering can surface the
// override and the lint pass can emit explicit-overrides-annotation
// warnings. Nil when no override occurred at this leaf.
type FieldProvenance struct {
	Origin              string           `json:"origin"`
	Side                string           `json:"side,omitempty"`
	AnnotationKey       string           `json:"annotation_key,omitempty"`
	AnnotationSource    string           `json:"annotation_source,omitempty"`
	OverridesAnnotation *FieldProvenance `json:"overrides_annotation,omitempty"`
}

// IRSecretEntry is a validated credential request for a node.
// Schema: ir-v1alpha1.schema.json secrets entry
type IRSecretEntry struct {
	Type            string                         `json:"type"`
	ResolvedVersion string                         `json:"resolved_version"`
	Values          OrderedMap[any]                `json:"values,omitempty,omitzero"`
	Inject          OrderedMap[OrderedMap[string]] `json:"inject,omitempty,omitzero"`
}

// IRProvenance records compiler and source traceability for an IR build.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/provenance
type IRProvenance struct {
	SchemaVersion     string   `json:"schema_version"`
	CompilerVersion   string   `json:"compiler_version"`
	AggregateVersion  string   `json:"aggregate_version"`
	ManifestVersion   string   `json:"manifest_version"`
	ReleaseVersion    string   `json:"release_version"`
	SourceIdentifiers []string `json:"source_identifiers"`
}
