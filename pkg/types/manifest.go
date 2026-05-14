// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Manifest is the global mapping artifact connecting Developer-side type
// references in the Aggregate to concrete Ops-side Recipe / FeatureRecipe
// implementations.
// Schema: manifest-v1alpha1.schema.json
type Manifest struct {
	APIVersion string           `json:"apiVersion" yaml:"apiVersion"`
	Kind       string           `json:"kind" yaml:"kind"`
	Metadata   ManifestMetadata `json:"metadata" yaml:"metadata"`
	Defs       []string         `json:"defs,omitempty" yaml:"defs,omitempty"` // YAML anchor definitions for DRY
	Spec       ManifestSpec     `json:"spec" yaml:"spec"`
}

// AnnotationBinding declares how a Release-level annotation on a boundary
// or component expands into a synthetic Feature attachment when the
// annotation-expansion sub-phase runs. Bindings are declared on the
// Manifest under `mappings.features.annotations`; the binding key is the
// annotation name (for example `feature.myco/rewrite-host`). The
// `aggrega.ai/` prefix is reserved for platform-native provider
// annotations and is rejected at expansion time.
// Schema: manifest-v1alpha1.schema.json#/properties/spec/properties/mappings/properties/features/properties/annotations/additionalProperties
type AnnotationBinding struct {
	// Feature is the Feature type URI this annotation expands into
	// (for example `core/rewrite-host:1.0.0`). The referenced type
	// must also appear in `features.type` — the FeatureRecipe version
	// binding lives there, not on the annotation binding.
	Feature string `json:"feature" yaml:"feature"`

	// Infra is the validating-schema source for the ops-facing payload
	// produced by the annotation value. The shape is an arbitrary
	// mapping mirroring the schema's `additionalProperties: true`; the
	// expansion sub-phase interprets the content as a JSON Schema
	// 2020-12 source. OrderedMap[any] preserves author-declared key
	// order through JSON re-emission for byte-identical output across
	// compile runs, matching the convention used on BoundaryComponent.Infra
	// and IRSecretEntry.Values for structurally parallel inline-schema
	// payloads.
	Infra OrderedMap[any] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`

	// Values is the validating-schema source for the dev-facing payload.
	// Empty for annotations that only expose ops-facing knobs.
	Values OrderedMap[any] `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
}

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

// ManifestSpec carries the Manifest's mapping declarations under a
// discriminated-union surface (Mappings) plus optional base inheritance.
type ManifestSpec struct {
	Extends  string          `json:"extends,omitempty" yaml:"extends,omitempty"`
	Mappings ManifestMappings `json:"mappings" yaml:"mappings"`
}

// ManifestMappings captures the two coexisting Manifest mapping shapes
// during the migration window:
//
//   - Flat shape: reserved top-level keys `components` and `features`
//     under `mappings:` carry component-type / feature-type URIs mapped
//     directly to Recipe / FeatureRecipe URIs. Provider determination
//     flows from each resolved Recipe's spec.provider at orchestrator-
//     time; the mapping is provider-agnostic at the Manifest layer.
//
//   - Legacy v2 shape: top-level keys under `mappings:` are provider
//     names (e.g., `helm`, `compose`), each carrying its own
//     `components` / `features` sub-sections.
//
// Dispatch precedence at consumption time is flat > legacy >
// matchImplements-walking-spec.implements (pre-migration fallback).
// Each layer is the active path for fixtures that opt in. Authors
// authoring partial-migration Manifests can mix flat entries and
// legacy entries; flat entries take precedence on overlapping
// component types.
//
// Provider-name discriminator constraint (transitional): provider
// entities cannot be named "components" or "features" while the
// legacy shape is still accepted, because the discriminator would
// mis-route. The constraint dissolves when downstream WIs complete
// the legacy-shape retirement.
type ManifestMappings struct {
	// Components and Features carry the flat-shape mappings.
	Components *MappingTier `json:"components,omitempty" yaml:"components,omitempty"`
	Features   *MappingTier `json:"features,omitempty" yaml:"features,omitempty"`

	// Legacy carries the v2 per-provider shape keyed by provider name.
	// Empty for fixtures that author only the flat shape.
	//
	// Both `yaml:"-"` and `json:"-"` elide the field from default wire
	// encoding; the custom UnmarshalYAML / MarshalYAML below is the
	// single source of truth for ManifestMappings wire shape. Any
	// future JSON-marshal consumer of Manifest would need a parallel
	// MarshalJSON to unify the discriminated union under `mappings:`
	// the same way MarshalYAML does today. There are no JSON-marshal
	// consumers of Manifest in production code at this WI's close;
	// the tag pairing prevents asymmetric wire shapes if one ships
	// later without the parallel MarshalJSON.
	Legacy OrderedMap[ProviderMappings] `json:"-" yaml:"-"`
}

// UnmarshalYAML dispatches under `mappings:` per the discriminator:
// reserved top-level keys `components` and `features` populate the
// flat-shape fields; any other top-level key populates Legacy as a
// provider-named entry. Mixed shapes coexist.
func (m *ManifestMappings) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("manifest mappings: expected map, got kind %d at line %d", node.Kind, node.Line)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		switch keyNode.Value {
		case "components":
			var mt MappingTier
			if err := valNode.Decode(&mt); err != nil {
				return fmt.Errorf("manifest mappings.components: %w", err)
			}
			m.Components = &mt
		case "features":
			var mt MappingTier
			if err := valNode.Decode(&mt); err != nil {
				return fmt.Errorf("manifest mappings.features: %w", err)
			}
			m.Features = &mt
		default:
			var pm ProviderMappings
			if err := valNode.Decode(&pm); err != nil {
				return fmt.Errorf("manifest mappings.%s: %w", keyNode.Value, err)
			}
			m.Legacy.Set(keyNode.Value, pm)
		}
	}
	return nil
}

// MarshalYAML emits both shapes (flat fields if populated, legacy
// providers if populated) under the same `mappings:` map. The order
// is flat keys (`components`, `features`) first, then legacy entries
// in their author-order from the OrderedMap.
func (m ManifestMappings) MarshalYAML() (any, error) {
	out := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, val any) error {
		valNode := &yaml.Node{}
		if err := valNode.Encode(val); err != nil {
			return err
		}
		out.Content = append(out.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			valNode,
		)
		return nil
	}
	if m.Components != nil {
		if err := add("components", m.Components); err != nil {
			return nil, err
		}
	}
	if m.Features != nil {
		if err := add("features", m.Features); err != nil {
			return nil, err
		}
	}
	for prov, pm := range m.Legacy.All() {
		if err := add(prov, pm); err != nil {
			return nil, err
		}
	}
	if len(out.Content) == 0 {
		return nil, nil
	}
	return out, nil
}

// ProviderMappings holds the component and feature mapping tiers for one provider.
// Used under the legacy v2 per-provider shape of Manifest.Spec.Mappings.
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
