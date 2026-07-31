// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Bidirectional schema↔struct alignment suite — the generalization of
// the single-struct TestPoliciesRoundTripThroughSchema precedent across
// document kinds.
//
// Coverage of the kind population is asserted rather than assumed: the cases
// below are hand-listed, so every kind the envelope package declares must
// either appear here or be named, with its reason, in the exclusion set that
// TestAlignmentSuiteCoversEveryEnvelopeKind walks. A kind added and forgotten
// fails there instead of being checked by nothing. Not every covered kind is an
// entity — the pin record and the workspace configuration file carry no
// envelope and are walked from their own root types.
//
// Two directions:
//
//   - Direction 1 (Go → schema): a maximally-populated instance of each kind's
//     struct marshals and validates against its kind's embedded schema,
//     catching Go fields the schema does not admit. A kind whose schema makes
//     two field groups mutually exclusive at the document root contributes one
//     instance per arm, since no single instance can carry both.
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

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	clischemas "github.com/aggrega-ai/aggrega-cli/pkg/schemas/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

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
	// maximals are the fully-populated instances for Direction-1. Most kinds
	// need exactly one. A kind whose schema makes two field groups mutually
	// exclusive at the document root needs one instance per arm, because no
	// single instance can populate the whole Go surface and still validate —
	// populating one arm makes the other arm's fields forbidden.
	maximals []any
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
			maximals: []any{maximalComponent()},
		},
		{
			kind:   envelope.KindFeature,
			schema: schemas.FeatureV1alpha1,
			roots:  []reflect.Type{reflect.TypeOf(types.Feature{})},
			exceptions: map[string]string{
				"feature.aggrega.ai/kind": labelKeyException,
			},
			maximals: []any{maximalFeature()},
		},
		{
			kind:       envelope.KindRecipe,
			schema:     schemas.RecipeV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.Recipe{})},
			exceptions: map[string]string{"$import": importException},
			maximals:   []any{maximalRecipe()},
		},
		{
			kind:       envelope.KindFeatureRecipe,
			schema:     schemas.FeatureRecipeV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.FeatureRecipe{})},
			exceptions: map[string]string{"$import": importException},
			maximals:   []any{maximalFeatureRecipe()},
		},
		{
			kind:       envelope.KindPlugin,
			schema:     schemas.PluginV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.Plugin{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalPlugin()},
		},
		{
			kind:       envelope.KindSchemaLibrary,
			schema:     schemas.SchemaLibraryV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.SchemaLibrary{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalSchemaLibrary()},
		},
		{
			kind:       envelope.KindMappingLibrary,
			schema:     schemas.MappingLibraryV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.MappingLibrary{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalMappingLibrary()},
		},
		{
			kind:       envelope.KindWorkloadLibrary,
			schema:     schemas.WorkloadLibraryV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.WorkloadLibrary{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalWorkloadLibrary()},
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
			maximals: []any{maximalAggregate()},
		},
		{
			kind:       envelope.KindManifest,
			schema:     schemas.ManifestV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(types.Manifest{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalManifest()},
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
			maximals: []any{maximalRelease()},
		},
		{
			// The pin record is hand-written rather than generated from its
			// schema, and its typed decode does not reject unknown fields, so a
			// schema property with no Go field would validate, be dropped by the
			// decode, and be reported by nothing. Since the pins are what make a
			// build reproducible, a dropped pin field reads as a clean build —
			// which is why this kind is aligned here rather than trusted.
			kind:       envelope.KindLockfile,
			schema:     schemas.LockfileV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(lockfile.Lockfile{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalLockfile()},
		},
		{
			// The workspace configuration file carries no envelope — it is flat
			// top-level keys — so its walk root is the configuration root type
			// rather than an entity struct.
			kind:       envelope.KindAggregaConfig,
			schema:     schemas.AggregaConfigV1alpha1,
			roots:      []reflect.Type{reflect.TypeOf(workspaceconfig.Config{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalAggregaConfig()},
		},
		{
			// Two maximal instances, because the workload schema makes the
			// compute fields and the reference fields mutually exclusive at the
			// document root: declaring either group forbids the other, so no
			// single instance can carry the whole Go surface.
			kind:       envelope.KindWorkload,
			schema:     clischemas.WorkloadJSONSchema,
			roots:      []reflect.Type{reflect.TypeOf(types.Workload{})},
			exceptions: map[string]string{},
			maximals:   []any{maximalWorkloadCompute(), maximalWorkloadReference()},
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
			if len(c.maximals) == 0 {
				t.Fatalf("%s carries no maximal instance, so this direction checks nothing for it", c.kind)
			}
			for i, m := range c.maximals {
				data, err := yaml.Marshal(m)
				if err != nil {
					t.Fatalf("marshal maximal %s (instance %d): %v", c.kind, i, err)
				}
				if _, err := envelope.ValidateBytes(c.kind, data, "maximal-"+string(c.kind)); err != nil {
					t.Errorf("maximal %s instance %d fails its own envelope schema — a Go field the schema does not admit:\n%v\n--- marshalled ---\n%s", c.kind, i, err, data)
				}
			}
		})
	}
}
