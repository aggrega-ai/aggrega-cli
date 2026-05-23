// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Provider is the global plugin registrar mapping a rendering engine name to an executable.
// Schema: provider-v1alpha1.schema.json
type Provider struct {
	APIVersion string           `json:"apiVersion" yaml:"apiVersion"`
	Kind       string           `json:"kind" yaml:"kind"`
	Metadata   ProviderMetadata `json:"metadata" yaml:"metadata"`
	Spec       ProviderSpec     `json:"spec" yaml:"spec"`
}

// ProviderMetadata carries identity for a Provider entity.
type ProviderMetadata struct {
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

// ProviderSpec contains the plugin URI for the rendering engine, the
// baseSchema used to validate valuesMapping output in sourceless mode, and
// the workloadSchema declaring how this provider materialises a Workload.
//
// A Provider that declares a non-empty WorkloadSchema supports Recipes
// that use the synthesized input kind (the `workload:` block). A Provider
// that omits WorkloadSchema rejects synthesized-mode Recipes at load time.
type ProviderSpec struct {
	Plugin         string `json:"plugin,omitempty" yaml:"plugin,omitempty"`
	BaseSchema     string `json:"baseSchema,omitempty" yaml:"baseSchema,omitempty"`         // JSON Schema 2020-12 for sourceless-mode valuesMapping validation
	WorkloadSchema string `json:"workloadSchema,omitempty" yaml:"workloadSchema,omitempty"` // JSON Schema 2020-12; presence flags Workload synthesis support
}
