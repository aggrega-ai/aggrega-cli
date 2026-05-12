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
//
// Three input kinds are supported (sourced / synthesized / artifacts), surfaced
// through the field groups below:
//
//   - Sourced mode: Source + ValuesMapping. The compiler fetches a chart/module
//     pointed at by Source and overlays the templated values dictionary. The
//     ValuesMapping field carries one of three modes per its own type
//     documentation (inline Go template, file $import:, or library $import:).
//
//   - Synthesized mode: Workload (a WorkloadTemplate discriminator: inline Go
//     template OR file $import:). The compiler templates a Workload YAML body
//     and hands the parsed Workload to the provider's WorkloadSynthesizer for
//     translation into provider-native manifests.
//
//   - Artifacts mode: Artifacts (OrderedMap[ArtifactEntry] keyed by output
//     filename). Each entry's value carries one of two modes (inline Go template
//     OR file $import:). The pre-migration ArtifactsFrom []string list-form
//     retires; basename-derived entry names replace it.
type RecipeSpec struct {
	Provider      string                    `json:"provider" yaml:"provider"`
	Implements    string                    `json:"implements" yaml:"implements"`
	Source        any                       `json:"source,omitempty" yaml:"source,omitempty"` // string URI or structured object
	Locals        OrderedMap[string]        `json:"locals,omitempty,omitzero" yaml:"locals,omitempty"`
	Provides      OrderedMap[string]        `json:"provides,omitempty,omitzero" yaml:"provides,omitempty"`
	ValuesMapping ValuesMapping             `json:"valuesMapping,omitempty" yaml:"valuesMapping,omitempty"`
	Workload      WorkloadTemplate          `json:"workload,omitempty" yaml:"workload,omitempty"`
	Artifacts     OrderedMap[ArtifactEntry] `json:"artifacts,omitempty,omitzero" yaml:"artifacts,omitempty"`
	Pipeline      []PipelineStep            `json:"pipeline,omitempty" yaml:"pipeline,omitempty"`
	// Assertions carries build-time cross-field validation as a map of named
	// Go template rules. Evaluation runs at Step 5b (post-binding, post-
	// allProvides-evaluation, pre-render); the first rule whose template
	// invokes {{ fail "message" }} halts the build. Recipe assertions
	// express implementation invariants of the specific wrapped chart or
	// module, distinct from Component-level architectural invariants which
	// live at the Component entity. Per-instance: one evaluation per node
	// bound to this Recipe. Strict reference semantics via missingkey=error.
	// Template context adds .provides / .locals / .recipe on top of the
	// Component-shape base. See internal/compiler/renderer/assertions.go
	// and internal/compiler/renderer/assertions_step5b.go.
	Assertions OrderedMap[string] `json:"assertions,omitempty,omitzero" yaml:"assertions,omitempty"`
}
