// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Recipe is the Ops-authored mapping connecting Dev intent to a specific infrastructure engine.
// Schema: recipe-v1alpha1.schema.json
type Recipe struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind" yaml:"kind"`
	Metadata   RecipeMetadata `json:"metadata" yaml:"metadata"`
	Spec       RecipeSpec     `json:"spec" yaml:"spec"`
}

// RecipeMetadata carries identity for a Recipe entity.
type RecipeMetadata struct {
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

// RecipeSpec defines the provider binding, source, mapping, and pipeline for a Recipe.
type RecipeSpec struct {
	Provider             string             `json:"provider" yaml:"provider"`
	Implements           string             `json:"implements" yaml:"implements"`
	Source               interface{}        `json:"source,omitempty" yaml:"source,omitempty"` // string URI or structured object
	Locals               OrderedMap[string] `json:"locals,omitempty,omitzero" yaml:"locals,omitempty"`
	Provides             OrderedMap[string] `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
	ValuesMapping        string             `json:"valuesMapping,omitempty" yaml:"valuesMapping,omitempty"`
	ValuesMappingFile    string             `json:"valuesMappingFile,omitempty" yaml:"valuesMappingFile,omitempty"`
	ValuesMappingJsonnet string             `json:"valuesMappingJsonnet,omitempty" yaml:"valuesMappingJsonnet,omitempty"`
	Pipeline             []PipelineStep     `json:"pipeline,omitempty" yaml:"pipeline,omitempty"`
}
