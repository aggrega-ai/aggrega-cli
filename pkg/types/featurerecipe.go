// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// FeatureRecipe is the Ops-authored mapping connecting Dev abstract features to concrete plugin pipelines.
// Schema: featurerecipe-v1alpha1.schema.json
type FeatureRecipe struct {
	APIVersion string                `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                `json:"kind" yaml:"kind"`
	Metadata   FeatureRecipeMetadata `json:"metadata" yaml:"metadata"`
	Spec       FeatureRecipeSpec     `json:"spec" yaml:"spec"`
}

// FeatureRecipeMetadata carries identity for a FeatureRecipe entity.
type FeatureRecipeMetadata struct {
	Name        string       `json:"name" yaml:"name"`
	Version     string       `json:"version" yaml:"version"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string       `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string       `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string       `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string     `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated  bool         `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
}

// FeatureRecipeSpec defines the provider binding, mapping, and pipeline
// for a FeatureRecipe. Compared to [RecipeSpec], FeatureRecipes support
// only the sourced input kind (Source + ValuesMapping*); the synthesized
// (Workload / WorkloadFrom) and artifacts (Artifacts / ArtifactsFrom)
// modes are Recipe-only.
//
// InjectMapping is unique to FeatureRecipes and carries the templated
// string that maps Aggrega's typed inject block to the provider's
// expected delivery format (e.g., Helm value paths, environment-variable
// names). Where ValuesMapping transforms values data, InjectMapping
// transforms inject delivery.
type FeatureRecipeSpec struct {
	Provider             string             `json:"provider" yaml:"provider"`
	Implements           string             `json:"implements" yaml:"implements"`
	Locals               OrderedMap[string] `json:"locals,omitempty,omitzero" yaml:"locals,omitempty"`
	Provides             OrderedMap[string] `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
	Source               interface{}        `json:"source,omitempty" yaml:"source,omitempty"` // string URI or structured object
	ValuesMapping        string             `json:"valuesMapping,omitempty" yaml:"valuesMapping,omitempty"`
	ValuesMappingFile    string             `json:"valuesMappingFile,omitempty" yaml:"valuesMappingFile,omitempty"`
	ValuesMappingJsonnet string             `json:"valuesMappingJsonnet,omitempty" yaml:"valuesMappingJsonnet,omitempty"`
	InjectMapping        string             `json:"injectMapping,omitempty" yaml:"injectMapping,omitempty"`
	Pipeline             []PipelineStep     `json:"pipeline,omitempty" yaml:"pipeline,omitempty"`
}
