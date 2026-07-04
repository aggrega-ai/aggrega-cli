// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Bidirectional schema↔struct alignment suite — the generalization of
// the single-struct TestPoliciesRoundTripThroughSchema precedent to
// every entity kind. Two directions:
//
//   - Direction 1 (Go → schema): a maximally-populated instance of each
//     entity struct marshals and validates against its kind's embedded
//     schema, catching Go fields the schema does not admit.
//   - Direction 2 (schema → Go): a reflection walk of each schema's full
//     property surface ($defs included) against the struct's yaml tags,
//     catching schema properties the Go type would silently drop — the
//     retired-surface failure class, detected at test time rather than
//     after it ships.
//
// Genuinely non-1:1 positions (polymorphic SelectorTerm, content-shape
// $import dispatch, deferred-decode contracts, well-known label-map
// keys) are explicit per-kind, per-path exceptions, each commented with
// WHY the mapping is not mechanical. The suite also asserts no exception
// is STALE — every exception must still name a property the walk reports
// missing — so the list cannot rot with dead entries.
package types_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"

	"go.yaml.in/yaml/v3"
)

// schemaPropertyNames walks a parsed JSON-schema value collecting every
// `properties` key across the whole tree — descending into `$defs`,
// `patternProperties`, `additionalProperties`, `items`, the boolean
// combinators, and the if/then/else trio. The flattened name set is the
// Direction-2 subject.
func schemaPropertyNames(root any) map[string]bool {
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for k, sub := range props {
				out[k] = true
				walk(sub)
			}
		}
		for _, key := range []string{"$defs", "definitions"} {
			if defs, ok := m[key].(map[string]any); ok {
				for _, sub := range defs {
					walk(sub)
				}
			}
		}
		if pp, ok := m["patternProperties"].(map[string]any); ok {
			for _, sub := range pp {
				walk(sub)
			}
		}
		if ap, ok := m["additionalProperties"].(map[string]any); ok {
			walk(ap)
		}
		if items, ok := m["items"].(map[string]any); ok {
			walk(items)
		}
		for _, key := range []string{"allOf", "anyOf", "oneOf"} {
			if arr, ok := m[key].([]any); ok {
				for _, sub := range arr {
					walk(sub)
				}
			}
		}
		for _, key := range []string{"if", "then", "else"} {
			if sub, ok := m[key].(map[string]any); ok {
				walk(sub)
			}
		}
	}
	walk(root)
	return out
}

// structYAMLTags walks a Go type collecting every yaml tag name across
// the whole type tree: struct fields, pointer/slice/array elements, map
// values, and OrderedMap's value type (reached through its unexported
// `values` map field's element type — type info is readable across
// packages even when the field is unexported).
func structYAMLTags(t reflect.Type, seen map[reflect.Type]bool, out map[string]bool) {
	if t == nil || seen[t] {
		return
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Map:
		structYAMLTags(t.Elem(), seen, out)
	case reflect.Struct:
		if strings.HasPrefix(t.Name(), "OrderedMap[") {
			if vf, ok := t.FieldByName("values"); ok && vf.Type.Kind() == reflect.Map {
				structYAMLTags(vf.Type.Elem(), seen, out)
			}
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if tag := f.Tag.Get("yaml"); tag != "" && tag != "-" {
				if name := strings.Split(tag, ",")[0]; name != "" {
					out[name] = true
				}
			}
			structYAMLTags(f.Type, seen, out)
		}
	}
}

// alignmentCase is one row of the bidirectional suite.
type alignmentCase struct {
	kind envelope.Kind
	// schema is the kind's embedded JSON-schema document.
	schema string
	// roots are the Go types whose yaml tags collectively mirror the
	// schema. The primary entity struct first; supplementary roots cover
	// shapes the entity stores as a deferred-decode `any` rather than a
	// static field type (e.g. ParameterContract behind Parameters).
	roots []reflect.Type
	// exceptions maps a schema property name to WHY it has no struct yaml
	// tag — a genuinely non-mechanical position. Each must still be
	// reported missing by the walk, or the staleness guard fails.
	exceptions map[string]string
	// maximal is a fully-populated instance for Direction-1.
	maximal any
}

// labelKeyException documents the well-known-label-key class: catalog
// schemas enumerate specific label keys as `properties` of the `labels`
// object so an IDE can complete them, but Go models labels as a string
// map — the key is data, not a field.
const labelKeyException = "well-known label key enumerated under the labels object; Go models labels as a string map, so the key is data not a struct field"

// importException documents the content-shape dispatch class: $import is
// the whole-content-replacement dispatch key handled by unmarshalImportable
// inside an Importable/yaml.Node field, never a static struct field.
const importException = "content-shape dispatch key ($import whole-content replacement), handled by unmarshalImportable inside the field, not a static struct field"

// injectDeliveryException documents the deferred inject-delivery class:
// InjectBlock.Env/File/Arg are typed `any`; the verbose [{in, from, value}]
// entries are decoded dynamically and validated by the envelope schema +
// the self-inject guard, not against a static {in,from,value} struct.
const injectDeliveryException = "verbose inject-delivery entry key; InjectBlock mechanisms are typed any and decoded dynamically, validated by schema + self-inject guard, not a static field map"

func alignmentCases() []alignmentCase {
	paramContract := reflect.TypeOf(types.ParameterContract{})
	return []alignmentCase{
		{
			kind:   envelope.KindComponent,
			schema: schemas.ComponentV1alpha1,
			roots:  []reflect.Type{reflect.TypeOf(types.Component{})},
			exceptions: map[string]string{
				"component.aggrega.ai/type": labelKeyException,
			},
			maximal: maximalComponent(),
		},
		{
			kind:   envelope.KindFeature,
			schema: schemas.FeatureV1alpha1,
			roots:  []reflect.Type{reflect.TypeOf(types.Feature{})},
			exceptions: map[string]string{
				"feature.aggrega.ai/kind": labelKeyException,
			},
			maximal: maximalFeature(),
		},
		{
			kind:       envelope.KindRecipe,
			schema:     schemas.RecipeV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.Recipe{})},
			exceptions: map[string]string{"$import": importException},
			maximal:    maximalRecipe(),
		},
		{
			kind:       envelope.KindFeatureRecipe,
			schema:     schemas.FeatureRecipeV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.FeatureRecipe{})},
			exceptions: map[string]string{"$import": importException},
			maximal:    maximalFeatureRecipe(),
		},
		{
			kind:       envelope.KindPlugin,
			schema:     schemas.PluginV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.Plugin{})},
			exceptions: map[string]string{},
			maximal:    maximalPlugin(),
		},
		{
			kind:       envelope.KindSchemaLibrary,
			schema:     schemas.SchemaLibraryV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.SchemaLibrary{})},
			exceptions: map[string]string{},
			maximal:    maximalSchemaLibrary(),
		},
		{
			kind:       envelope.KindMappingLibrary,
			schema:     schemas.MappingLibraryV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.MappingLibrary{})},
			exceptions: map[string]string{},
			maximal:    maximalMappingLibrary(),
		},
		{
			kind:   envelope.KindAggregate,
			schema: schemas.AggregateV1alpha1,
			// ParameterContract folded in: Aggregate.Spec.Parameters is
			// OrderedMap[any] (raw-decoded), so the compact-form parameter
			// vocabulary is reachable only through the on-demand
			// ParameterContract decode. Including it here resolves the
			// vocabulary names to real tags AND makes the suite catch a
			// future vocab-key added to the schema but not to ParameterContract.
			roots: []reflect.Type{reflect.TypeOf(types.Aggregate{}), paramContract},
			exceptions: map[string]string{
				"in":    injectDeliveryException,
				"from":  injectDeliveryException,
				"value": injectDeliveryException,
			},
			maximal: maximalAggregate(),
		},
		{
			kind:       envelope.KindManifest,
			schema:     schemas.ManifestV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.Manifest{})},
			exceptions: map[string]string{},
			maximal:    maximalManifest(),
		},
		{
			kind:   envelope.KindRelease,
			schema: schemas.ReleaseV1alpha1,
			roots:  []reflect.Type{reflect.TypeOf(types.Release{})},
			exceptions: map[string]string{
				"$import": importException,
				"in":      injectDeliveryException,
				"from":    injectDeliveryException,
				"value":   injectDeliveryException,
			},
			maximal: maximalRelease(),
		},
	}
}

// TestSchemaStructAlignment_Direction2_SchemaToGo is the silent-drop
// detector: every schema property must have a matching struct yaml tag
// (across the kind's walk roots) or an explicit, commented exception.
// Runs first because it surfaces the drop class the envelope corpus
// cannot — the envelope validates documents AS AUTHORED, so a schema
// property present in no fixture is never exercised; this walk traverses
// the full schema surface regardless of fixtures.
func TestSchemaStructAlignment_Direction2_SchemaToGo(t *testing.T) {
	for _, c := range alignmentCases() {
		t.Run(string(c.kind), func(t *testing.T) {
			var root any
			if err := json.Unmarshal([]byte(c.schema), &root); err != nil {
				t.Fatalf("decode %s schema: %v", c.kind, err)
			}
			props := schemaPropertyNames(root)
			tags := map[string]bool{}
			seen := map[reflect.Type]bool{}
			for _, rt := range c.roots {
				structYAMLTags(rt, seen, tags)
			}
			var unexplained []string
			for p := range props {
				if tags[p] || c.exceptions[p] != "" {
					continue
				}
				unexplained = append(unexplained, p)
			}
			sort.Strings(unexplained)
			if len(unexplained) > 0 {
				t.Errorf("%s: %d schema property(ies) with no struct yaml tag and no exception: %v\n"+
					"each is either a silent-drop bug (add the field) or a genuinely non-mechanical position (add a commented exception)",
					c.kind, len(unexplained), unexplained)
			}
			// Staleness guard: every exception must still name a property
			// the walk reports missing. A dead exception is the list rotting.
			for name := range c.exceptions {
				if !props[name] {
					t.Errorf("%s: stale exception %q — schema no longer declares this property; remove the exception", c.kind, name)
					continue
				}
				if tags[name] {
					t.Errorf("%s: needless exception %q — a struct yaml tag now covers it; remove the exception", c.kind, name)
				}
			}
		})
	}
}

// TestSchemaStructAlignment_Direction1_GoToSchema is the Go-field-not-in-
// schema detector: a maximally-populated instance of each kind marshals
// to YAML and passes its kind's envelope schema with zero findings. A Go
// field the schema does not admit surfaces here (the spec-level
// additionalProperties:false makes any unmapped field a finding).
func TestSchemaStructAlignment_Direction1_GoToSchema(t *testing.T) {
	for _, c := range alignmentCases() {
		t.Run(string(c.kind), func(t *testing.T) {
			data, err := yaml.Marshal(c.maximal)
			if err != nil {
				t.Fatalf("marshal maximal %s: %v", c.kind, err)
			}
			if _, err := envelope.ValidateBytes(c.kind, data, "maximal-"+string(c.kind)); err != nil {
				t.Errorf("maximal %s instance fails its own envelope schema — a Go field the schema does not admit:\n%v\n--- marshalled ---\n%s", c.kind, err, data)
			}
		})
	}
}
