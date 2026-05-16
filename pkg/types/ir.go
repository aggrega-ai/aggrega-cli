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
	CatalogAnnotations   OrderedMap[string] `json:"catalog_annotations,omitempty,omitzero"`
	ReleaseAnnotations   OrderedMap[string] `json:"release_annotations,omitempty,omitzero"`
	AggregateAnnotations OrderedMap[string] `json:"aggregate_annotations,omitempty,omitzero"`
	SelfInject           *InjectBlock       `json:"self_inject,omitempty"` // Component-level self-inject data for Pass 2a resolution
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
// "annotation"). For annotation-driven attachments, AnnotationKey
// carries the annotation name that triggered the expansion and
// AnnotationSource locates the source declaration as
// <release-file-path>:<dotted-yaml-key-path>. Both fields are empty
// when Origin is "explicit"; the JSON tags `omitempty` keep the wire
// shape backwards-compatible — pre-provenance fixtures and golden
// files re-marshal without spurious empty-field entries.
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
