// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"
)

//go:generate go run ../../internal/tools/typegen aggregate_gen.go

// ActiveFlag is a presence toggle authored on an Aggregate component
// instance or a sub-aggregate module. It accepts a YAML/JSON boolean or a
// string and carries the value verbatim as its canonical literal —
// "true"/"false" for a boolean, or the raw string otherwise. A string is a
// parameter template the resolver renders to a literal before the topology
// reads it; the topology then interprets the literal strictly as a boolean.
// The empty value means the toggle was omitted and the entity is present by
// default. ActiveFlag is a compile-time authoring input only; it never
// appears in compiled output.
type ActiveFlag string

// UnmarshalYAML accepts a scalar boolean or string and stores the node's
// literal value ("true"/"false" for a boolean, the raw text for a string).
// Non-scalar nodes are rejected naming the two accepted forms.
func (a *ActiveFlag) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("active must be a boolean or a string template (line %d)", node.Line)
	}
	*a = ActiveFlag(node.Value)
	return nil
}

// UnmarshalJSON accepts a JSON boolean or string, normalizing a boolean to
// its "true"/"false" literal and storing a string verbatim.
func (a *ActiveFlag) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*a = ActiveFlag(s)
		return nil
	case 't', 'f':
		var b bool
		if err := json.Unmarshal(trimmed, &b); err != nil {
			return err
		}
		*a = ActiveFlag(strconv.FormatBool(b))
		return nil
	default:
		return fmt.Errorf("active must be a boolean or a string, got %s", trimmed)
	}
}

// Aggregate's struct declarations (Aggregate, AggregateMetadata, AggregateSpec)
// are generated into aggregate_gen.go from the published aggregate-v1alpha1
// schema; their godoc is the schema's descriptions. This hand-written companion
// holds the per-instance carrier types the generated spec references —
// SubAggregate, Subsystem, and ComponentInstance — which carry the
// Annotations / Uses / Inject / Features surfaces and their polymorphic decode.
// Annotations live on those per-instance carriers; AggregateSpec itself carries
// no top-level Annotations.

// SubAggregate is a nested aggregate acting as an imported module.
// Schema: aggregate-v1alpha1.schema.json#/properties/spec/properties/aggregates
type SubAggregate struct {
	Type string `json:"type" yaml:"type"`
	// Active is the module's presence toggle; empty means present. See [ActiveFlag].
	Active     ActiveFlag      `json:"active,omitempty" yaml:"active,omitempty"`
	Parameters OrderedMap[any] `json:"parameters,omitempty,omitzero" yaml:"parameters,omitempty"`
}

// Subsystem is a logical grouping of developer intent. Recursive via nested Subsystems.
// Schema: aggregate-v1alpha1.schema.json#/$defs/subsystem
type Subsystem struct {
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Annotations are Developer-authored annotations at the subsystem
	// scope. Each entry's value is one of three shapes encoded via
	// [AnnotationValue]:
	//
	//   - Scalar — a single string. The binding's Maps must declare
	//     exactly one path; the scalar routes to that path.
	//   - Structured — a map of subkey to subvalue. Each subkey routes
	//     through the binding's Maps to the Feature schema field with
	//     the same name; subkeys whose pillar is incompatible with the
	//     authoring side halt the build.
	//   - Nil pointer (explicit YAML null at the value site) — the
	//     subsystem opts out of the inherited binding wholesale for
	//     this scope.
	//
	// Structured values also support per-subkey null opt-out: a subkey
	// with a null value inside a Structured map suppresses the
	// corresponding inherited subkey while leaving sibling subkeys
	// untouched.
	//
	// Annotations declared here are inherited by nested subsystems and
	// by contained components; they do not propagate sideways to
	// sibling subsystems. The Aggregate-as-Authoring-Surface contract
	// permits routing only through the binding's values-pillar paths;
	// bindings whose maps route an annotation key (or a subkey of a
	// Structured value) through the infra pillar halt at annotation
	// expansion time.
	Annotations OrderedMap[*AnnotationValue] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`

	Features   OrderedMap[FeatureInstance]   `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Subsystems OrderedMap[Subsystem]         `json:"subsystems,omitempty,omitzero" yaml:"subsystems,omitempty"`
	Components OrderedMap[ComponentInstance] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`

	// Synthesized marks a subsystem the compiler created rather than the
	// author — the `default` subsystem synthesized for an Aggregate that
	// declares top-level components instead of named subsystems. Derived
	// provenance, never authored: the json/yaml `-` tags keep it off the
	// authoring surface, and it is set only by [Normalize]. Carried onto the
	// IR node so the synthesized default is distinguishable from a subsystem a
	// user explicitly named "default". Symmetric to [Boundary.Synthesized].
	Synthesized bool `json:"-" yaml:"-"`
}

// ComponentInstance is one logical node in the aggregate topology (Developer-authored).
// Schema: aggregate-v1alpha1.schema.json#/$defs/component_instance
type ComponentInstance struct {
	Type string `json:"type" yaml:"type"`
	// Active is the component's presence toggle; empty means present. See [ActiveFlag].
	Active      ActiveFlag                  `json:"active,omitempty" yaml:"active,omitempty"`
	Description string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[any]             `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Inject      *InjectBlock                `json:"inject,omitempty" yaml:"inject,omitempty"`      // Component-level self-inject
	Uses        OrderedMap[UsesSocketValue] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"` // UsesBlock: socket → simple []string or verbose alias-map
	Features    OrderedMap[FeatureInstance] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`

	// Annotations are Developer-authored annotations at the component
	// scope. Values follow the same [AnnotationValue] shape as on
	// [Subsystem]: Scalar, Structured, or nil-pointer for whole-value
	// opt-out. Structured values additionally support per-subkey null
	// opt-out — a subkey with a null value inside a Structured map
	// suppresses the corresponding inherited subkey while leaving
	// sibling subkeys untouched.
	//
	// Component-level annotations declared here override inherited
	// subsystem-level annotations per key; sibling components in the
	// same subsystem retain their own annotation maps independently.
	// The Aggregate-as-Authoring-Surface contract permits routing only
	// through the binding's values-pillar paths; bindings whose maps
	// route an annotation key (or a subkey of a Structured value)
	// through the infra pillar halt at annotation expansion time.
	Annotations OrderedMap[*AnnotationValue] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
}

// NormalizedAggregate wraps an [Aggregate] that has passed [Normalize] — its
// top-level components folded into the synthesized `default` subsystem, so the
// root component map is empty and every component lives under a subsystem. The
// unexported field plus the sole constructor make "normalized" a compile-time
// invariant: a function taking a NormalizedAggregate cannot be handed a raw,
// un-normalized aggregate. Consumers that iterate or guard an Aggregate take
// this type; they read the underlying value via [NormalizedAggregate.Aggregate].
//
// The symmetric Release-side posture is the single Release parse core; the
// Aggregate carries the stronger guarantee — a distinct type that only
// normalization can produce — because it has many more component-iterating
// consumers.
type NormalizedAggregate struct {
	agg *Aggregate
}

// Normalize is the SOLE constructor for [NormalizedAggregate]. When the
// Aggregate declares top-level components and no named subsystems, it
// synthesizes one subsystem named "default" holding those components, inserted
// first (ahead of any named subsystem, so flatten/IR node order is unchanged),
// marked [Subsystem.Synthesized], and clears the top-level components — they now
// live in the subsystem. The mutual exclusivity of components and subsystems is
// enforced upstream at the document-envelope gate; this runs only on the
// components-only shape and is a no-op for a subsystems-bearing or empty
// Aggregate. Idempotent: a second call finds subsystems already present and
// returns.
//
// NON-MUTATING: when synthesis is needed it operates on a shallow copy of the
// Aggregate and wraps the copy, leaving the caller's *Aggregate untouched. A
// sole constructor that gutted its input would make every caller still holding
// the original a latent aliasing bug; returning a copy dissolves that hazard by
// construction. The copy reassigns only Spec.Subsystems and Spec.Components
// (the moved components map is shared read-only into the synthesized default,
// never mutated), so iteration order is preserved and determinism holds.
func Normalize(agg *Aggregate) NormalizedAggregate {
	if agg == nil || agg.Spec.Subsystems.Len() > 0 || agg.Spec.Components.Len() == 0 {
		return NormalizedAggregate{agg: agg}
	}
	cp := *agg
	def := Subsystem{Components: agg.Spec.Components, Synthesized: true}
	var subs OrderedMap[Subsystem]
	subs.Set("default", def)
	cp.Spec.Subsystems = subs
	cp.Spec.Components = OrderedMap[ComponentInstance]{}
	return NormalizedAggregate{agg: &cp}
}

// Aggregate returns the underlying normalized aggregate for read access. The
// returned pointer is the normalized value (root folded into `default`); it is
// exposed for field reads, not for re-wrapping a mutated copy as normalized.
func (n NormalizedAggregate) Aggregate() *Aggregate { return n.agg }

// ComponentInstanceRef is one component instance yielded by
// [NormalizedAggregate.ComponentInstances]: the developer-chosen instance name,
// the authored instance, and the provenance of the subsystem enclosing it.
//
// SubsystemPath is the dotted path of the enclosing subsystem — "a" for
// `subsystems.a`, "a.b" for a nested `subsystems.a.subsystems.b`. Every
// component instance carries one, because a normalized Aggregate holds no
// components outside a subsystem.
//
// Synthesized is true when the enclosing subsystem is the compiler-synthesized
// default scope, i.e. the component was authored at the top-level
// `.spec.components` surface. A consumer that renders an authoring locator uses
// it to point at the shape the author typed rather than at the generated scope
// name.
type ComponentInstanceRef struct {
	Name          string
	Instance      ComponentInstance
	SubsystemPath string
	Synthesized   bool
}

// ComponentInstances iterates every component instance in the Aggregate: all
// components of every subsystem, recursively, in author order at each level.
//
// It is a method on [NormalizedAggregate] rather than a function over
// *[Aggregate] so the iteration cannot be obtained without the normalization
// guarantee. A normalized Aggregate holds an empty root component map, so a
// subsystem-only descent is complete; a consumer reaching for the walk gets the
// complete one by construction and cannot assemble a partial one that misses
// subsystem components.
//
// Author order is preserved and no sort is applied — the result feeds
// byte-identical output paths. A consumer needing a different order sorts the
// returned slice itself.
//
// Returns nil when the wrapped Aggregate is nil.
func (n NormalizedAggregate) ComponentInstances() []ComponentInstanceRef {
	if n.agg == nil {
		return nil
	}
	var out []ComponentInstanceRef
	appendSubsystemComponents("", n.agg.Spec.Subsystems, &out)
	return out
}

func appendSubsystemComponents(parentPath string, subs OrderedMap[Subsystem], out *[]ComponentInstanceRef) {
	for subName, sub := range subs.All() {
		path := subName
		if parentPath != "" {
			path = parentPath + "." + subName
		}
		for name, inst := range sub.Components.All() {
			*out = append(*out, ComponentInstanceRef{
				Name:          name,
				Instance:      inst,
				SubsystemPath: path,
				Synthesized:   sub.Synthesized,
			})
		}
		appendSubsystemComponents(path, sub.Subsystems, out)
	}
}
