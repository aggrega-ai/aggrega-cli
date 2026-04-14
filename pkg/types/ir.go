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
	AppName string `json:"appName"`
}

// IRSpec is the spec subtree of an IR document.
// Schema: ir-v1alpha1.schema.json#/properties/spec
type IRSpec struct {
	Nodes                map[string]IRNode                    `json:"nodes"`
	Edges                []IREdge                             `json:"edges"`
	FeatureMarkers       []string                             `json:"feature_markers"`
	ValuesMapping        map[string]json.RawMessage           `json:"values_mapping"`
	InfraMapping         map[string]json.RawMessage           `json:"infra_mapping"`
	FeatureValuesMapping map[string]map[string]IRFeatureEntry `json:"feature_values_mapping"`
	Secrets              map[string]map[string]IRSecretEntry  `json:"secrets"`
	Provenance           IRProvenance                         `json:"provenance"`
}

// IRNode is a single node in the IR graph. Purely topological — no opaque payload.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/nodes
type IRNode struct {
	Description          string            `json:"description,omitempty"`
	Labels               map[string]string `json:"labels"`
	Component            string            `json:"component"`
	ResolvedVersion      string            `json:"resolved_version"`
	SyncWave             string            `json:"sync_wave,omitempty"`
	BoundaryPath         string            `json:"boundary_path,omitempty"`
	SubsystemPath        string            `json:"subsystem_path,omitempty"`
	CatalogMetadata      *CatalogMetadata  `json:"catalog_metadata,omitempty"`
	CatalogAnnotations   map[string]string `json:"catalog_annotations,omitempty"`
	ReleaseAnnotations   map[string]string `json:"release_annotations,omitempty"`
	AggregateAnnotations map[string]string `json:"aggregate_annotations,omitempty"`
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
	Source        string                       `json:"source"`
	Target        string                       `json:"target"`
	Socket        string                       `json:"socket,omitempty"`
	Alias         string                       `json:"alias,omitempty"`
	OriginFeature string                       `json:"origin_feature,omitempty"`
	Inject        map[string]map[string]string `json:"inject,omitempty"`
}

// IRFeatureEntry is one feature attached to a node in the feature_values_mapping.
// Schema: ir-v1alpha1.schema.json feature_values_mapping entry
type IRFeatureEntry struct {
	Type            string                       `json:"type"`
	ResolvedVersion string                       `json:"resolved_version"`
	Values          map[string]interface{}       `json:"values,omitempty"`
	Infra           map[string]interface{}       `json:"infra,omitempty"`
	Inject          map[string]map[string]string `json:"inject,omitempty"`
	Secrets         map[string]IRSecretEntry     `json:"secrets,omitempty"`
}

// IRSecretEntry is a validated credential request for a node.
// Schema: ir-v1alpha1.schema.json secrets entry
type IRSecretEntry struct {
	Type            string                       `json:"type"`
	ResolvedVersion string                       `json:"resolved_version"`
	Values          map[string]interface{}       `json:"values,omitempty"`
	Inject          map[string]map[string]string `json:"inject,omitempty"`
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
