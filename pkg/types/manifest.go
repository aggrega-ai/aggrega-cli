// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

//go:generate go run ../../internal/tools/typegen manifest_gen.go

// Manifest's struct declarations (Manifest, ManifestMetadata, ManifestSpec)
// are generated into manifest_gen.go from the published manifest-v1alpha1
// schema; their godoc is the schema's descriptions. This hand-written
// companion holds the discriminated-union mapping types the generated spec
// references — ManifestMappings (with its custom flat-vs-legacy
// UnmarshalYAML/MarshalYAML), MappingTier, ProviderMappings, AnnotationBinding,
// and AnnotationBindingMaps.

// AnnotationBinding declares how an annotation key in a Manifest's
// annotation registry routes to a Feature attachment's pillar fields.
// The binding associates an annotation key with a Feature URI and a
// pillar-grouped list of dotted-path subkey routes naming which Feature
// schema properties the annotation may populate. Bindings are declared
// on the Manifest under `mappings.features.annotations`; the binding key
// is the annotation name (for example `feature.myco/rewrite-host`). The
// `aggrega.ai/` prefix is reserved for platform-native provider
// annotations and is rejected at expansion time.
//
// The annotation value's structure determines how Maps is consumed:
//   - A scalar annotation value with a single declared path (across both
//     pillar lists, total) routes the scalar to that path.
//   - A scalar annotation value with multiple declared paths is an error
//     (ambiguous routing).
//
// Dotted-path subkey routes navigate into nested Feature schema
// properties. Path "ingress.host" addresses Feature.Spec.Schema.values
// (or .infra).properties.ingress.properties.host. Each path component
// must be a JSON-Schema-valid identifier; wildcards are not supported.
//
// Validation derives from the Feature's own spec.schema: for each path
// in Maps.Infra the compiler navigates Feature.Spec.Schema.infra through
// the dotted components and verifies the leaf exists; same for Maps.Values
// against Feature.Spec.Schema.values. The Feature is the single source
// of truth for declared property paths; the binding declares only the
// routing.
//
// The authoring side (Aggregate Subsystem/Component or Release
// Boundary/BoundaryComponent) constrains which pillar's path list fires:
//   - Aggregate-side annotations may only route via Maps.Values paths
//     (per the Aggregate-as-Authoring-Surface invariant).
//   - Release-side annotations may only route via Maps.Infra paths
//     (per the same invariant's symmetric corollary).
//
// A binding can carry paths in both pillar lists; each side fires only
// its compatible subset. Cross-side path routing mismatches halt the
// build with a clear error.
//
// Schema: manifest-v1alpha1.schema.json#/properties/spec/properties/mappings/properties/features/properties/annotations/additionalProperties
type AnnotationBinding struct {
	// Feature is the Feature URI this annotation routes to (for example
	// `core/rewrite-host:1.0.0`). The referenced type must also appear
	// in `features.type` — the FeatureRecipe version binding lives
	// there, not on the annotation binding.
	Feature string `json:"feature" yaml:"feature"`

	// Maps is the pillar-grouped routing table: each pillar (infra,
	// values) carries an author-ordered list of dotted-path subkey
	// routes naming which Feature schema properties the annotation may
	// populate. At least one of Maps.Infra or Maps.Values must be
	// non-empty; the Manifest JSON Schema enforces this.
	Maps AnnotationBindingMaps `json:"maps" yaml:"maps"`
}

// AnnotationBindingMaps groups subkey-path routes by their target pillar.
// At least one of Infra or Values must declare at least one path (enforced
// by the Manifest JSON Schema). Paths within a list are unique; cross-list
// path appearances are allowed only when the target Feature's spec.schema
// declares the property in both pillars (the schema-pillar-overlap lint
// surfaces the smell).
type AnnotationBindingMaps struct {
	// Infra carries the dotted-path subkey routes targeting the Feature's
	// infra pillar. Release-side annotations may only route via Infra
	// paths; Aggregate-side annotations cannot fire Infra entries.
	Infra []string `json:"infra,omitempty" yaml:"infra,omitempty"`

	// Values carries the dotted-path subkey routes targeting the Feature's
	// values pillar. Aggregate-side annotations may only route via Values
	// paths; Release-side annotations cannot fire Values entries.
	Values []string `json:"values,omitempty" yaml:"values,omitempty"`
}

// Len returns the total number of declared subkey paths across both pillar
// lists. Used by expansion-time checks to detect the multi-path case
// (which is ambiguous when paired with a scalar annotation value).
func (m AnnotationBindingMaps) Len() int {
	return len(m.Infra) + len(m.Values)
}

// Lookup returns the pillar ("infra" or "values") that contains the
// given dotted-path string and a found flag. Annotation expansion uses
// this for per-subkey routing of structured annotation values: each
// subkey in a Structured AnnotationValue resolves through Lookup to
// the binding-declared pillar, and the side-vs-pillar enforcement
// gates whether the authoring side can fire that subkey.
//
// Returns ("", false) when the path appears in neither pillar list —
// signals "this subkey isn't declared in the binding map" to the
// caller's collect-and-emit-all error chain.
//
// Tie-breaking on duplicate paths. When a path appears in BOTH
// pillar lists (an authoring anti-pattern: declaring "this subkey
// routes through both pillars" is incoherent — annotation subkeys
// fire one pillar's contribution per side), Lookup returns "infra"
// deterministically. The Aggregate side then fails side-vs-pillar
// on every such subkey, making the path Aggregate-inaccessible
// despite its Values declaration. Manifest binding-map overlap is
// surfaced by resolver.LintAnnotationBindingMaps, which runs after
// Resolve and emits one advisory warning per duplicated path
// (parallel to the schema-pillar-overlap lint that catches same-
// name overlap on the Feature schema side). The deterministic tie-
// breaking + side-vs-pillar halt remain the runtime safety net for
// authors who run with the warning ignored.
func (m AnnotationBindingMaps) Lookup(path string) (string, bool) {
	for _, p := range m.Infra {
		if p == path {
			return "infra", true
		}
	}
	for _, p := range m.Values {
		if p == path {
			return "values", true
		}
	}
	return "", false
}

// ManifestMappings captures the two coexisting Manifest mapping shapes:
//
//   - Flat shape: reserved top-level keys `components` and `features`
//     under `mappings:` carry component-type / feature-type URIs mapped
//     directly to Recipe / FeatureRecipe URIs. Provider determination
//     flows from each resolved Recipe's spec.capability at orchestrator-
//     time; the mapping is provider-agnostic at the Manifest layer.
//
//   - Legacy v2 shape: top-level keys under `mappings:` are provider
//     names (e.g., `helm`, `compose`), each carrying its own
//     `components` / `features` sub-sections.
//
// Dispatch precedence at consumption time is flat > legacy; an
// unmapped component type is a resolution miss that halts at the
// completeness pass (mappings are explicit — the resolver never walks
// spec.implements to bind a build). Authors can mix flat entries and
// legacy entries in one Manifest; flat entries take precedence on
// overlapping component types.
//
// Provider-name discriminator constraint: a provider entity cannot be
// named "components" or "features" while the legacy shape is accepted,
// because the discriminator would mis-route.
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
	// consumers of Manifest in production code today; the tag pairing
	// prevents asymmetric wire shapes if one ships later without the
	// parallel MarshalJSON.
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
