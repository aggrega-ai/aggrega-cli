// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// TemplateContext is the runtime view assembled by the Go compiler and injected into
// Recipe and FeatureRecipe Go templates during Step 5. This is NOT persisted to disk;
// it exists only in compiler memory during rendering.
// Schema: template-context-v1alpha1.schema.json
//
// Phase B sub-pass field availability:
//   - .source — populated at Phase B.0 from Recipe.Spec.Source after Go-template
//     evaluation. nil before B.0; nil for Recipes that don't declare source.
//   - .workload — populated at Phase B.2 after Workload yaml.Unmarshal +
//     validator.ValidateWorkload + validator.ValidateWorkloadSecrets succeed.
//     nil before B.2; nil for Recipes that don't declare workload / workloadFrom.
type TemplateContext struct {
	Context    ContextBlock                `json:"context" yaml:"context"`
	Values     OrderedMap[interface{}]     `json:"values" yaml:"values"`
	Infra      OrderedMap[interface{}]     `json:"infra" yaml:"infra"`
	Locals     OrderedMap[string]          `json:"locals" yaml:"locals"`
	Provides   OrderedMap[string]          `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
	Features   OrderedMap[TemplateFeature] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets    OrderedMap[TemplateSecret]  `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
	Metadata   *TemplateMetadata           `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	SecretName string                      `json:"secret_name,omitempty" yaml:"secret_name,omitempty"`
	Inject     *InjectPipe                 `json:"inject,omitempty" yaml:"inject,omitempty"`         // secret-specific inject
	InjectRaw  interface{}                 `json:"inject_raw,omitempty" yaml:"inject_raw,omitempty"` // immutable Developer intent; null if omitted
	Steps      OrderedMap[StepOutput]      `json:"steps,omitempty,omitzero" yaml:"steps,omitempty"`
	Source     interface{}                 `json:"source,omitempty" yaml:"source,omitempty"`     // resolved Recipe.Spec.Source (Phase B.0+); nil for sourceless Recipes
	Workload   *Workload                   `json:"workload,omitempty" yaml:"workload,omitempty"` // parsed Workload (Phase B.2+); nil for non-synthesized Recipes
	Boundary   *BoundaryContext            `json:"boundary,omitempty" yaml:"boundary,omitempty"` // populated only for boundary-scope FRs (spec.executionScope: boundary); nil otherwise
}

// ContextBlock is the compiler-assembled execution context providing identity, topology, and delivery data.
// Schema: template-context-v1alpha1.schema.json#/properties/context
type ContextBlock struct {
	InstanceID  string                   `json:"instance_id" yaml:"instance_id"`
	Labels      OrderedMap[string]       `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations *AnnotationBuckets       `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Inject      InjectPipe               `json:"inject" yaml:"inject"`
	Uses        OrderedMap[EnrichedEdge] `json:"uses" yaml:"uses"`
}

// AnnotationBuckets holds the three cascading annotation origins for a node.
// Schema: template-context-v1alpha1.schema.json context.annotations
type AnnotationBuckets struct {
	Release   OrderedMap[string] `json:"release,omitempty,omitzero" yaml:"release,omitempty"`
	Catalog   OrderedMap[string] `json:"catalog,omitempty,omitzero" yaml:"catalog,omitempty"`
	Aggregate OrderedMap[string] `json:"aggregate,omitempty,omitzero" yaml:"aggregate,omitempty"`
}

// InjectPipe is the pre-hydrated delivery pipe available in templates.
// Known mechanisms: env, file, arg, all. Custom mechanisms use Additional.
// Schema: template-context-v1alpha1.schema.json context.inject
type InjectPipe struct {
	Env        OrderedMap[string]           `json:"env,omitempty,omitzero" yaml:"env,omitempty"`
	File       OrderedMap[string]           `json:"file,omitempty,omitzero" yaml:"file,omitempty"`
	Arg        OrderedMap[string]           `json:"arg,omitempty,omitzero" yaml:"arg,omitempty"`
	All        OrderedMap[string]           `json:"all,omitempty,omitzero" yaml:"all,omitempty"`
	Additional map[string]map[string]string `json:"-" yaml:"-"` // custom mechanisms; populated by custom unmarshal — opaque payload, kept as plain map
}

// EnrichedEdge is an enriched topological edge assembled by the compiler for template consumption.
// Schema: template-context-v1alpha1.schema.json#/$defs/enriched_edge
type EnrichedEdge struct {
	Target          string                         `json:"target" yaml:"target"`
	Socket          string                         `json:"socket" yaml:"socket"`
	Provides        OrderedMap[string]             `json:"provides" yaml:"provides"`
	Inject          OrderedMap[OrderedMap[string]] `json:"inject" yaml:"inject"`
	Labels          OrderedMap[string]             `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations     *AnnotationBuckets             `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Component       string                         `json:"component,omitempty" yaml:"component,omitempty"`
	ResolvedVersion string                         `json:"resolved_version,omitempty" yaml:"resolved_version,omitempty"`
	BoundaryPath    string                         `json:"boundary_path,omitempty" yaml:"boundary_path,omitempty"`
	OriginFeature   string                         `json:"origin_feature,omitempty" yaml:"origin_feature,omitempty"`
}

// TemplateMetadata provides Release and compilation identity for deterministic naming.
// Schema: template-context-v1alpha1.schema.json#/properties/metadata
type TemplateMetadata struct {
	ReleaseName    string `json:"release_name" yaml:"release_name"`
	ReleaseVersion string `json:"release_version" yaml:"release_version"`
	Environment    string `json:"environment" yaml:"environment"`
	AggregateName  string `json:"aggregate_name" yaml:"aggregate_name"`
}

// TemplateFeature is a feature attachment available in the template context.
// Schema: template-context-v1alpha1.schema.json#/properties/features
type TemplateFeature struct {
	Type   string                  `json:"type" yaml:"type"`
	Values OrderedMap[interface{}] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Infra  OrderedMap[interface{}] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
}

// TemplateSecret is a credential feature attachment available in the template context.
// Schema: template-context-v1alpha1.schema.json#/properties/secrets
type TemplateSecret struct {
	Type   string                  `json:"type" yaml:"type"`
	Values OrderedMap[interface{}] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
}

// StepOutput holds the outputs exported by one pipeline step for inter-step communication.
// Schema: template-context-v1alpha1.schema.json#/properties/steps
type StepOutput struct {
	Outputs OrderedMap[string] `json:"outputs" yaml:"outputs"`
}

// BoundaryContext is the boundary-scope view assembled by the compiler when
// evaluating a FeatureRecipe with spec.executionScope: boundary. It replaces
// the per-component .context block: an FR with executionScope: boundary
// receives .boundary.* in templates instead of .context.*. Population happens
// at Phase B.6's boundary-scope dispatch branch; component-scope FRs leave the
// pointer nil. Schema: template-context-v1alpha1.schema.json#/properties/boundary
type BoundaryContext struct {
	Path                           string                    `json:"path" yaml:"path"`
	BoundaryName                   string                    `json:"boundary_name" yaml:"boundary_name"`
	Components                     OrderedMap[ComponentInfo] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	InternalUsesEdges              []EnrichedEdge            `json:"internal_uses_edges,omitempty" yaml:"internal_uses_edges,omitempty"`
	OutgoingCrossBoundaryUsesEdges []EnrichedEdge            `json:"outgoing_cross_boundary_uses_edges,omitempty" yaml:"outgoing_cross_boundary_uses_edges,omitempty"`
	IncomingCrossBoundaryUsesEdges []EnrichedEdge            `json:"incoming_cross_boundary_uses_edges,omitempty" yaml:"incoming_cross_boundary_uses_edges,omitempty"`
	CrossBoundaryUsesEdges         []EnrichedEdge            `json:"cross_boundary_uses_edges,omitempty" yaml:"cross_boundary_uses_edges,omitempty"`
	Labels                         OrderedMap[string]        `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations                    OrderedMap[string]        `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
	FeatureAttachments             []TemplateFeature         `json:"feature_attachments,omitempty" yaml:"feature_attachments,omitempty"`
}

// ComponentInfo is the per-component view exposed via .boundary.components
// in boundary-scope FeatureRecipe templates. One entry per component in
// the boundary's subtree, keyed by instance ID.
//
// The Go type fixes the field set to a contract; the JSON schema for
// .boundary.components stays open (additionalProperties: true) so future
// pillars can be added without a schema-version bump. When new fields
// surface, they extend ComponentInfo and the schema description picks
// them up via documentation rather than schema constraint.
//
// Schema: template-context-v1alpha1.schema.json#/properties/boundary/properties/components
type ComponentInfo struct {
	InstanceID      string `json:"instance_id" yaml:"instance_id"`
	Component       string `json:"component" yaml:"component"`
	ResolvedVersion string `json:"resolved_version,omitempty" yaml:"resolved_version,omitempty"`
	BoundaryPath    string `json:"boundary_path,omitempty" yaml:"boundary_path,omitempty"`
}
