package types

import "encoding/json"

// API version constant for IR documents validated against ir-v1alpha1.schema.json.
const IRAPIVersion = "aggrega.ai/v1alpha1"

// IRKind is the canonical kind value for IR documents.
const IRKind = "IR"

// IRGraph is the in-memory, target-agnostic intermediate representation emitted by the
// graph builder (Intent 003). It strictly mirrors the JSON shape of
// ir-v1alpha1.schema.json and forms the typed boundary handed to the security translator
// (Intent 004) and provider engine (Intent 002).
type IRGraph struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   IRMetadata `json:"metadata"`
	Spec       IRSpec     `json:"spec"`
}

// IRMetadata carries document-level metadata for an IR graph.
type IRMetadata struct {
	// AppName identifies the logical application this IR describes.
	AppName string `json:"appName"`
}

// IRSpec is the spec subtree of an IR document: topology, routing, annotations, and
// supporting maps separated per schema (nodes hold no opaque payload).
type IRSpec struct {
	// Nodes maps absolute node IDs to topological node records (resolved type and
	// component name only; opaque values live under ValuesMapping).
	Nodes map[string]IRNode `json:"nodes"`

	Edges []IREdge `json:"edges"`

	// Routing enumerates explicit gateway-mediated paths (source → gateway → sink).
	//
	// At the v1alpha1 JSON-schema layer each entry is only the three node IDs above.
	// Semantically, this slice is also where compilation attaches gateway concerns
	// described in annotations and CUE—including fallback_chain, traffic_shadowing,
	// token_budget, semantic_caching, and concurrency_limit—so downstream engines
	// resolve those concerns in lockstep with the gateway hop they belong to.
	Routing []IRRouting `json:"routing"`

	// Annotations holds generic declarations (for example auth_strategy, retry_policy).
	// Values are arbitrary JSON objects preserved losslessly for round-trip.
	Annotations map[string]json.RawMessage `json:"annotations"`

	// FeatureMarkers lists canonical strings (for example semantic_routing, data_masking)
	// derived from annotations for feature negotiation in later pipeline stages.
	FeatureMarkers []string `json:"feature_markers"`

	// ValuesMapping is the single source of truth for opaque pass-through payloads,
	// keyed by node ID. Each value is arbitrary JSON.
	ValuesMapping map[string]json.RawMessage `json:"values_mapping"`

	// FeatureValuesMapping is a normalized map of Node IDs to their attached Feature
	// configurations. Format: NodeID -> FeatureType -> Arbitrary JSON (from the 'with' block).
	FeatureValuesMapping map[string]map[string]json.RawMessage `json:"feature_values_mapping"`

	// Secrets maps node ID to named credential requests (store reference plus opaque
	// "with" parameters), kept separate from ValuesMapping.
	Secrets map[string]map[string]IRSecretRequest `json:"secrets"`

	// Artifact holds opaque references to external templates or pre-existing artifacts.
	Artifact map[string]json.RawMessage `json:"artifact"`

	// Provenance carries traceability metadata for diagnostics and auditing.
	Provenance IRProvenance `json:"provenance"`
}

// IRNode is a single node in the IR graph without embedded payload (schema: resolvedType,
// component only).
type IRNode struct {
	// ResolvedType is the resolved AI entity type (for example ai-agent, ai-gateway).
	ResolvedType string `json:"resolvedType"`
	// Component is the name of the underlying Component entity.
	Component string `json:"component"`
}

// IREdgeType classifies an edge between two nodes.
type IREdgeType string

const (
	IREdgeTypeNetwork           IREdgeType = "network"
	IREdgeTypeLogicalDependency IREdgeType = "logical_dependency"
	IREdgeTypeDataFlow          IREdgeType = "data_flow"
)

// IREdge is a directed logical relationship between IR nodes.
type IREdge struct {
	Source string     `json:"source"`
	Target string     `json:"target"`
	Type   IREdgeType `json:"type,omitempty"`
}

// IRRouting describes one gateway-mediated path in the IR graph.
type IRRouting struct {
	SourceNodeID  string `json:"source_node_id"`
	GatewayNodeID string `json:"gateway_node_id"`
	SinkNodeID    string `json:"sink_node_id"`
}

// IRSecretRequest is a validated credential request for a node (store + opaque parameters).
type IRSecretRequest struct {
	Store string                     `json:"store"`
	With  map[string]json.RawMessage `json:"with"`
}

// IRProvenance records compiler and source traceability for an IR build.
// The schema allows additional properties beyond the listed fields.
type IRProvenance struct {
	SchemaVersion     string   `json:"schema_version,omitempty"`
	CompilerVersion   string   `json:"compiler_version,omitempty"`
	SourceIdentifiers []string `json:"source_identifiers,omitempty"`
}
