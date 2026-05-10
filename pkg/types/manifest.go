// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Manifest is the global lockfile mapping abstract Developer Interfaces to concrete Ops Implementations.
// Schema: manifest-v1alpha1.schema.json
type Manifest struct {
	APIVersion string           `json:"apiVersion" yaml:"apiVersion"`
	Kind       string           `json:"kind" yaml:"kind"`
	Metadata   ManifestMetadata `json:"metadata" yaml:"metadata"`
	Defs       []string         `json:"defs,omitempty" yaml:"defs,omitempty"` // YAML anchor definitions for DRY
	Spec       ManifestSpec     `json:"spec" yaml:"spec"`
}

// AnnotationBinding is an empty stub in this intent; fields land in
// annotation-expansion. Declared here so the deterministic-ordering refactor
// can reserve the MappingTier.Annotations slot with an OrderedMap-typed field.
type AnnotationBinding struct{}

// ManifestMetadata carries identity for a Manifest entity.
type ManifestMetadata struct {
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

// ManifestSpec defines the provider-grouped mappings and optional base inheritance.
type ManifestSpec struct {
	Extends  string                       `json:"extends,omitempty" yaml:"extends,omitempty"`
	Mappings OrderedMap[ProviderMappings] `json:"mappings" yaml:"mappings"` // keyed by provider name
}

// ProviderMappings holds the component and feature mapping tiers for one provider.
// Schema: manifest-v1alpha1.schema.json#/properties/spec/properties/mappings/patternProperties
type ProviderMappings struct {
	Components *MappingTier `json:"components,omitempty" yaml:"components,omitempty"`
	Features   *MappingTier `json:"features,omitempty" yaml:"features,omitempty"`
}

// MappingTier implements two-tier resolution: type for bulk DRY mapping, name for granular overrides.
// Resolution order: name wins over type. Versioned type keys win over unversioned.
// Schema: manifest-v1alpha1.schema.json#/$defs/mapping_tier
type MappingTier struct {
	Type OrderedMap[string] `json:"type,omitempty,omitzero" yaml:"type,omitempty"`
	Name OrderedMap[string] `json:"name,omitempty,omitzero" yaml:"name,omitempty"`
	// *Pointer: nil vs empty matters for the annotation-expansion intent — a nil
	// pointer means the author did not declare annotation bindings at all; an
	// empty OrderedMap is currently authorable but may carry meaning later.
	Annotations *OrderedMap[AnnotationBinding] `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}
