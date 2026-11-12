// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

//go:generate go run ../../internal/tools/typegen manifest_gen.go

// Manifest's struct declarations (Manifest, ManifestMetadata, ManifestSpec)
// are generated into manifest_gen.go from the published manifest-v1beta1
// schema; their godoc is the schema's descriptions. This hand-written
// companion holds the mapping types the generated spec references —
// ManifestMappings (with its custom UnmarshalYAML/MarshalYAML), MappingTier,
// AnnotationBinding, and AnnotationBindingMaps.

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
// Schema: manifest-v1beta1.schema.json#/properties/spec/properties/mappings/properties/features/properties/annotations/additionalProperties
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

// ManifestMappings carries a Manifest's mapping declarations: the
// top-level keys `components` and `features` under `mappings:`, each
// mapping component-type / feature-type URIs directly to Recipe /
// FeatureRecipe URIs. Those two keys are the whole surface, and
// UnmarshalYAML refuses any other key.
//
// The mapping is capability-agnostic at the Manifest layer: capability
// determination flows from each resolved Recipe's spec.capability at
// orchestrator-time, so a mapping never names a target.
//
// An unmapped component type is a resolution miss that halts at the
// completeness pass — mappings are explicit, and the resolver never
// walks spec.implements to bind a build.
//
// Schema: manifest-v1beta1.schema.json#/properties/spec/properties/mappings
type ManifestMappings struct {
	Components *MappingTier `json:"components,omitempty" yaml:"components,omitempty"`
	Features   *MappingTier `json:"features,omitempty" yaml:"features,omitempty"`
}

// UnmarshalYAML decodes the `components` and `features` keys under
// `mappings:` and refuses every other key by name.
//
// The refusal is here as well as in the schema because a caller decoding a
// Manifest through this type directly does not pass the schema gate, and
// yaml.v3's default behaviour for an unrecognised key is to drop it. A
// dropped mapping key is a mapping the author wrote and the build will not
// honour, with nothing said — so the key set is enforced where the decode
// happens rather than only where the document is validated.
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
			return fmt.Errorf(
				"manifest mappings: unexpected key %q at line %d; mappings accepts only components and features. Declare component mappings under components: and feature mappings under features:",
				keyNode.Value, keyNode.Line)
		}
	}
	return nil
}

// MarshalYAML emits the populated keys under `mappings:`, `components`
// before `features`.
//
// It pairs with UnmarshalYAML as the single home for this type's wire
// shape. A Manifest declaring no mapping at all emits a null `mappings:`
// rather than an empty map — the one byte difference from what the default
// struct encoder would produce, and the reason this method is kept rather
// than left to the struct tags.
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
	if len(out.Content) == 0 {
		return nil, nil
	}
	return out, nil
}

// MappingTier implements two-tier resolution: type for bulk DRY mapping, name for granular overrides.
// Resolution order: name wins over type. Versioned type keys win over unversioned.
// Schema: manifest-v1beta1.schema.json#/$defs/mapping_tier
type MappingTier struct {
	Type OrderedMap[string] `json:"type,omitempty,omitzero" yaml:"type,omitempty"`
	Name OrderedMap[string] `json:"name,omitempty,omitzero" yaml:"name,omitempty"`
	// Pointer rather than value, because "absent" and "declared empty" are
	// two different declarations here and the value form collapses them into
	// one. A tier that omits the key inherits the annotation bindings of the
	// manifest it extends; a tier that declares an empty dictionary replaces
	// them, which is how an author opts out of an inherited registry.
	// Bindings are replaced wholesale rather than composed key by key, so an
	// empty dictionary clears the inherited set rather than merging into it.
	Annotations *OrderedMap[AnnotationBinding] `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// MappingTierEntries is one tier of a [MappingTier], as [MappingTier.Tiers]
// presents it: the key the tier is written under, what its keys name, and the
// realization each key binds.
type MappingTierEntries struct {
	// Key is the key the tier is written under in the Manifest, "name" or
	// "type", so a reader can say where an entry was authored.
	Key string

	// ByInstanceName reports that the tier is keyed by instance name: an entry
	// applies to the one instance its key names. Otherwise the tier is keyed by
	// type reference and an entry applies to every instance of that type.
	ByInstanceName bool

	// Entries maps each key to the Recipe or FeatureRecipe reference it binds,
	// in author order.
	Entries OrderedMap[string]
}

// Tiers returns the mapping's tiers in the order a lookup consults them: the
// name tier, whose entry for an instance wins, then the type tier. A nil
// mapping has none.
//
// It is the one statement of which tiers a mapping carries. The lookup that
// picks the realization one instance renders through reads the tiers through
// it, and so does the lock-time walk that pins every realization a mapping
// binds, so the set of references the lookup can return is the set the walk
// pins: a realization bound by instance name is pinned and verified exactly as
// one bound by type. A tier read directly from a field rather than through here
// is the shape that lets the two disagree.
func (t *MappingTier) Tiers() []MappingTierEntries {
	if t == nil {
		return nil
	}
	return []MappingTierEntries{
		{Key: "name", ByInstanceName: true, Entries: t.Name},
		{Key: "type", Entries: t.Type},
	}
}
