// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Maintainer identifies an entity owner.
// Shared across all entity metadata blocks.
type Maintainer struct {
	Name  string `json:"name" yaml:"name"`
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
	URL   string `json:"url,omitempty" yaml:"url,omitempty"`
}

// ComponentType is the 7-type Cloud-Native Ontology from component-v1alpha1.schema.json.
type ComponentType string

const (
	ComponentTypeServer         ComponentType = "server"
	ComponentTypeWorker         ComponentType = "worker"
	ComponentTypeTask           ComponentType = "task"
	ComponentTypeFunction       ComponentType = "function"
	ComponentTypeStatefulServer ComponentType = "stateful-server"
	ComponentTypeResource       ComponentType = "resource"
	ComponentTypeMessaging      ComponentType = "messaging"
)

// ValidComponentTypes is the set of valid ComponentType values for quick lookup.
var ValidComponentTypes = map[ComponentType]bool{
	ComponentTypeServer:         true,
	ComponentTypeWorker:         true,
	ComponentTypeTask:           true,
	ComponentTypeFunction:       true,
	ComponentTypeStatefulServer: true,
	ComponentTypeResource:       true,
	ComponentTypeMessaging:      true,
}

// IsValidComponentType returns true if t is one of the 7-type ontology values.
func IsValidComponentType(t ComponentType) bool {
	return ValidComponentTypes[t]
}

// ComponentTypeLabel is the reserved label key for the 7-type ontology.
const ComponentTypeLabel = "component.aggrega.ai/type"

// FeatureKindLabel is the reserved label key for credential feature discrimination.
const FeatureKindLabel = "feature.aggrega.ai/kind"

// FeatureKindSecret is the label value marking a Feature as a credential Feature.
const FeatureKindSecret = "secret"

// InjectBlock is the agnostic injection routing block from aggregate-v1alpha1.schema.json#/$defs/inject_block.
// Known mechanisms are env, file, arg. Custom mechanisms use Additional.
// Each mechanism value is an InjectDelivery: either compact map[string]string or verbose []{in,from}.
// Both forms are represented as interface{} and normalized to map[string]string during Step 2.
type InjectBlock struct {
	Env        interface{}            `json:"env,omitempty" yaml:"env,omitempty"`
	File       interface{}            `json:"file,omitempty" yaml:"file,omitempty"`
	Arg        interface{}            `json:"arg,omitempty" yaml:"arg,omitempty"`
	Additional map[string]interface{} `json:"-" yaml:"-"` // custom mechanisms; populated by custom unmarshal — opaque payload, kept as plain map
}

// FeatureInstance is a feature attachment on a component or subsystem.
// Used in Aggregate component.features, subsystem.features, and Release boundary features.
type FeatureInstance struct {
	Type        string                     `json:"type" yaml:"type"`
	Description string                     `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[interface{}]    `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Uses        OrderedMap[interface{}]    `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
	Inject      *InjectBlock               `json:"inject,omitempty" yaml:"inject,omitempty"`
	Secrets     OrderedMap[SecretInstance] `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
}

// SecretInstance is a credential feature attachment (labeled
// feature.aggrega.ai/kind: secret). Used in Aggregate component.secrets,
// feature.secrets, and Release boundary secrets. Structurally parallel to
// [FeatureInstance] except for nested Secrets: credentials are leaf
// attachments and a SecretInstance cannot itself carry nested secrets.
type SecretInstance struct {
	Type        string                  `json:"type" yaml:"type"`
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[interface{}] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Uses        OrderedMap[interface{}] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
	Inject      *InjectBlock            `json:"inject,omitempty" yaml:"inject,omitempty"`
}

// PipelineStep is one Wasm plugin invocation in a Recipe or FeatureRecipe pipeline.
// Defined in recipe-v1alpha1.schema.json#/properties/spec/properties/pipeline/items.
type PipelineStep struct {
	ID       string `json:"id" yaml:"id"`
	Plugin   string `json:"plugin" yaml:"plugin"`
	Function string `json:"function" yaml:"function"`
	With     string `json:"with,omitempty" yaml:"with,omitempty"`
}
