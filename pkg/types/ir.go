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
	// BoundaryFeatures is the un-folded projection of each Release boundary's
	// Feature attachments, keyed boundary-path -> instance-name. It mirrors
	// release.Spec.Boundaries[*].Features as the single source both lock-time
	// source-descent and the render-time boundary-FR dispatch read, so neither
	// re-walks the Release. omitempty: only graphs with boundary-level Feature
	// attachments carry it, so non-boundary-FR fixtures do not churn IR goldens.
	BoundaryFeatures OrderedMap[OrderedMap[IRBoundaryFeature]] `json:"boundary_features,omitempty,omitzero"`
	Provenance       IRProvenance                              `json:"provenance"`
}

// IRBoundaryFeature is one Release boundary-level Feature attachment projected
// into the IR — the type URI plus the boundary infra. Boundary-level attachments
// carry no values (the Release pillar-split forbids values: at boundary level), so
// only Type and Infra are projected; Type resolves to the FeatureRecipe (source /
// locals / provides) and Infra is the boundary infra a boundary-scope FR renders
// against.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/boundary_features
type IRBoundaryFeature struct {
	Type  string          `json:"type"`
	Infra OrderedMap[any] `json:"infra,omitempty,omitzero"`
}

// IRNode is a single node in the IR graph. Purely topological — no opaque payload.
// Schema: ir-v1alpha1.schema.json#/properties/spec/properties/nodes
type IRNode struct {
	Description     string             `json:"description,omitempty"`
	Labels          OrderedMap[string] `json:"labels"`
	Component       string             `json:"component"`
	ResolvedVersion string             `json:"resolved_version"`
	SyncWave        int                `json:"sync_wave,omitempty"`
	BoundaryPath    string             `json:"boundary_path,omitempty"`
	SubsystemPath   string             `json:"subsystem_path,omitempty"`
	// SynthesizedSubsystem is true when SubsystemPath names the compiler-
	// synthesized `default` subsystem — created for an Aggregate that declares
	// top-level components rather than named subsystems. Derived provenance,
	// never authored; distinguishes the synthesized default from a subsystem a
	// user explicitly named "default". Independent of SynthesizedBoundary.
	// Omitted (false) for authored subsystems.
	SynthesizedSubsystem bool `json:"synthesized_subsystem,omitempty"`
	// SynthesizedBoundary is true when BoundaryPath names the compiler-
	// synthesized `default` boundary — created for a Release that declares
	// top-level components rather than named boundaries. Derived provenance,
	// never authored; distinguishes the synthesized default from a boundary a
	// user explicitly named "default". Omitted (false) for authored boundaries.
	SynthesizedBoundary bool             `json:"synthesized_boundary,omitempty"`
	CatalogMetadata     *CatalogMetadata `json:"catalog_metadata,omitempty"`
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

	// SourceFile is the workspace-relative path of the Aggregate file
	// where this node was authored. Root-tier nodes carry the root
	// Aggregate's relative path (the caller renders it via
	// catalog.RelativeToWorkspace before constructing pipeline.Inputs);
	// sub-aggregate nodes carry the relative path the catalog accessor
	// returned (the catalog applies the same normalization using the
	// workspace root it was constructed with, the first argument to
	// Load/LoadAll). The single coordinate system
	// keeps the IR wire shape byte-identical across developer machines
	// whose workspaces live at different absolute paths. Empty when no
	// workspace was wired or topology cannot resolve a source file
	// (synthetic test IR, loaders returning empty paths). Consumers
	// that filter diagnostics per file (the daemon's
	// analyzer-error-to-diagnostic conversion) render their comparison
	// path through the same catalog.RelativeToWorkspace helper and drop
	// entries whose SourceFile points to a different file from the
	// validated one.
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
	Source string `json:"source"`
	Target string `json:"target"`
	Socket string `json:"socket,omitempty"`
	Alias  string `json:"alias,omitempty"`
	// OriginFeature is the catalog Feature type URI when the edge originated
	// from a Feature's own uses block (empty for Component-own edges).
	OriginFeature string `json:"origin_feature,omitempty"`
	// OriginFeatureInstance is the per-attachment instance name of that
	// Feature. It locates a boundary-scope FeatureRecipe's resolved provides
	// by instance when a consumer's uses edge references them, disambiguating
	// two attachments of the same Feature type on one boundary.
	OriginFeatureInstance string `json:"origin_feature_instance,omitempty"`
	// OriginRelease is true when this edge was created by a Release wiring a
	// declared-but-unbound socket to an existing component. It marks edge
	// AUTHORSHIP, not configuration: a developer- or Feature-declared edge that
	// a Release merely supplied infra for keeps its origin (OriginRelease false)
	// and carries the Release infra in Infra. OriginFeature set => Feature;
	// OriginRelease true => Release; neither => Aggregate.
	OriginRelease bool `json:"origin_release,omitempty"`
	// Values and Infra carry the resolved per-connection config for this edge,
	// validated at Step 1 against the target socket's valueSchema / infraSchema
	// and projected into the consuming node's render context at
	// .uses.<socket>.<alias>.values / .infra. Opaque to the core.
	Values OrderedMap[any]                `json:"values,omitempty,omitzero"`
	Infra  OrderedMap[any]                `json:"infra,omitempty,omitzero"`
	Inject OrderedMap[OrderedMap[string]] `json:"inject,omitempty,omitzero"`
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

// IRSecretEntry is a per-node credential request. A secret is structurally a
// feature with kind=secret (see [FeatureKindSecret]): it carries the same IR
// attachment shape as [IRFeatureEntry] and is partitioned into the secrets bucket
// rather than given a lossier projection. Modelling the two as one shape keeps the
// secret projection from drifting behind the feature projection — the accident that
// dropped per-instance secret infra. No plaintext is implied: Values holds vault
// pointers, and infra/provenance/origin are structural metadata, never resolved
// secret values (the compiler stays blind to plaintext).
//
// Type and ResolvedVersion are always serialised (non-omitempty); ResolvedVersion
// carries the credential's OWN resolved catalog version (the Feature's version, not
// the host component's). Every other field is omitempty, so the serialised KEY set
// for a credential carrying only Type+Values matches the former dedicated shape —
// the added fields (infra, provenance, origin, nested secrets) appear only when
// populated.
//
// Schema: ir-v1alpha1.schema.json secrets entry (shares the feature entry shape).
type IRSecretEntry = IRFeatureEntry

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
