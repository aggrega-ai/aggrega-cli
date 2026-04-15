// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// TemplateContext is the runtime view assembled by the Go compiler and injected into
// Recipe and FeatureRecipe Go templates during Step 5. This is NOT persisted to disk;
// it exists only in compiler memory during rendering.
// Schema: template-context-v1alpha1.schema.json
type TemplateContext struct {
	Context    ContextBlock               `json:"context" yaml:"context"`
	Values     map[string]interface{}     `json:"values" yaml:"values"`
	Infra      map[string]interface{}     `json:"infra" yaml:"infra"`
	Locals     map[string]string          `json:"locals" yaml:"locals"`
	Provides   map[string]string          `json:"provides,omitempty" yaml:"provides,omitempty"`
	Features   map[string]TemplateFeature `json:"features,omitempty" yaml:"features,omitempty"`
	Secrets    map[string]TemplateSecret  `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Metadata   *TemplateMetadata          `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	SecretName string                     `json:"secret_name,omitempty" yaml:"secret_name,omitempty"`
	Inject     *InjectPipe                `json:"inject,omitempty" yaml:"inject,omitempty"`         // secret-specific inject
	InjectRaw  interface{}                `json:"inject_raw,omitempty" yaml:"inject_raw,omitempty"` // immutable Developer intent; null if omitted
	Steps      map[string]StepOutput      `json:"steps,omitempty" yaml:"steps,omitempty"`
}

// ContextBlock is the compiler-assembled execution context providing identity, topology, and delivery data.
// Schema: template-context-v1alpha1.schema.json#/properties/context
type ContextBlock struct {
	InstanceID  string                  `json:"instance_id" yaml:"instance_id"`
	Labels      map[string]string       `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations *AnnotationBuckets      `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Inject      InjectPipe              `json:"inject" yaml:"inject"`
	Uses        map[string]EnrichedEdge `json:"uses" yaml:"uses"`
}

// AnnotationBuckets holds the three cascading annotation origins for a node.
// Schema: template-context-v1alpha1.schema.json context.annotations
type AnnotationBuckets struct {
	Release   map[string]string `json:"release,omitempty" yaml:"release,omitempty"`
	Catalog   map[string]string `json:"catalog,omitempty" yaml:"catalog,omitempty"`
	Aggregate map[string]string `json:"aggregate,omitempty" yaml:"aggregate,omitempty"`
}

// InjectPipe is the pre-hydrated delivery pipe available in templates.
// Known mechanisms: env, file, arg, all. Custom mechanisms use Additional.
// Schema: template-context-v1alpha1.schema.json context.inject
type InjectPipe struct {
	Env        map[string]string            `json:"env,omitempty" yaml:"env,omitempty"`
	File       map[string]string            `json:"file,omitempty" yaml:"file,omitempty"`
	Arg        map[string]string            `json:"arg,omitempty" yaml:"arg,omitempty"`
	All        map[string]string            `json:"all,omitempty" yaml:"all,omitempty"`
	Additional map[string]map[string]string `json:"-" yaml:"-"` // custom mechanisms; populated by custom unmarshal
}

// EnrichedEdge is an enriched topological edge assembled by the compiler for template consumption.
// Schema: template-context-v1alpha1.schema.json#/$defs/enriched_edge
type EnrichedEdge struct {
	Target          string                       `json:"target" yaml:"target"`
	Socket          string                       `json:"socket" yaml:"socket"`
	Provides        map[string]string            `json:"provides" yaml:"provides"`
	Inject          map[string]map[string]string `json:"inject" yaml:"inject"`
	Labels          map[string]string            `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations     *AnnotationBuckets           `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Component       string                       `json:"component,omitempty" yaml:"component,omitempty"`
	ResolvedVersion string                       `json:"resolved_version,omitempty" yaml:"resolved_version,omitempty"`
	BoundaryPath    string                       `json:"boundary_path,omitempty" yaml:"boundary_path,omitempty"`
	OriginFeature   string                       `json:"origin_feature,omitempty" yaml:"origin_feature,omitempty"`
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
	Type   string                 `json:"type" yaml:"type"`
	Values map[string]interface{} `json:"values,omitempty" yaml:"values,omitempty"`
	Infra  map[string]interface{} `json:"infra,omitempty" yaml:"infra,omitempty"`
}

// TemplateSecret is a credential feature attachment available in the template context.
// Schema: template-context-v1alpha1.schema.json#/properties/secrets
type TemplateSecret struct {
	Type   string                 `json:"type" yaml:"type"`
	Values map[string]interface{} `json:"values,omitempty" yaml:"values,omitempty"`
}

// StepOutput holds the outputs exported by one pipeline step for inter-step communication.
// Schema: template-context-v1alpha1.schema.json#/properties/steps
type StepOutput struct {
	Outputs map[string]string `json:"outputs" yaml:"outputs"`
}
