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
// for a FeatureRecipe. FeatureRecipes carry the same three input kinds as
// [RecipeSpec] — sourced (Source + ValuesMapping), synthesized (Workload),
// and artifacts (Artifacts) — and compose them by addition exactly as a
// Recipe does. ValuesMapping carries one of three modes per its own type
// documentation (inline Go template, file $import:, or library $import:).
//
// InjectMapping is the sole field unique to FeatureRecipes: it carries the
// templated string that maps Aggrega's typed inject block to the provider's
// expected delivery format (e.g., Helm value paths, environment-variable
// names). Where ValuesMapping transforms values data, InjectMapping
// transforms inject delivery. A FeatureRecipe declaring only InjectMapping
// (no render input kind) is valid — it contributes Caller-side inject
// mutation and renders nothing.
type FeatureRecipeSpec struct {
	Provider       string                    `json:"provider" yaml:"provider"`
	Implements     string                    `json:"implements" yaml:"implements"`
	Locals         OrderedMap[string]        `json:"locals,omitempty,omitzero" yaml:"locals,omitempty"`
	Provides       OrderedMap[string]        `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
	Source         any                       `json:"source,omitempty" yaml:"source,omitempty"` // string URI or structured object
	ValuesMapping  ValuesMapping             `json:"valuesMapping,omitempty" yaml:"valuesMapping,omitempty"`
	Workload       WorkloadTemplate          `json:"workload,omitempty" yaml:"workload,omitempty"`
	Artifacts      OrderedMap[ArtifactEntry] `json:"artifacts,omitempty,omitzero" yaml:"artifacts,omitempty"`
	InjectMapping  string                    `json:"injectMapping,omitempty" yaml:"injectMapping,omitempty"`
	Pipeline       []PipelineStep            `json:"pipeline,omitempty" yaml:"pipeline,omitempty"`
	ExecutionScope string                    `json:"executionScope,omitempty" yaml:"executionScope,omitempty"`
	// Assertions carries build-time cross-field validation as a map of named
	// Go template rules. Evaluation runs at Step 5b (post-binding, post-
	// allProvides-evaluation, pre-render); the first rule whose template
	// invokes {{ fail "message" }} halts the build. FeatureRecipe assertions
	// express implementation invariants of the specific wrapped chart or
	// module for the attached Feature, distinct from Feature-level
	// architectural invariants which live at the Feature entity. Per-
	// attachment: one evaluation per node-with-Feature-attached. Strict
	// reference semantics via missingkey=error. Template context adds
	// .provides / .locals / .recipe on top of the Feature-shape base. See
	// internal/compiler/renderer/assertions.go and
	// internal/compiler/renderer/assertions_step5b.go.
	Assertions OrderedMap[string] `json:"assertions,omitempty,omitzero" yaml:"assertions,omitempty"`
}
