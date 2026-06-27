// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"go.yaml.in/yaml/v3"
)

// extendsFeatureFixture builds the shared extends-feature setup for the
// annotation-path proofs: a child Feature (extends a parent) whose parent
// declares values.threshold (maximum 10) — inherited, not redeclared — plus the
// composed effective schema a populated lookup would serve for it.
func extendsFeatureFixture(t *testing.T) (composed compactform.Schema, feat *types.Feature) {
	t.Helper()
	parse := func(s string) yaml.Node {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(s), &n); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
			return *n.Content[0]
		}
		return n
	}
	parentEntry := validator.CatalogEntry{
		URI:        "core/test/parent-feat",
		JSONSchema: validator.CompileSchemaForMerge(parse("values:\n  threshold: { type: integer, maximum: 10 }\n"), nil),
	}
	childEntry := validator.CatalogEntry{
		URI:        "core/test/child-feat",
		Extends:    "core/test/parent-feat",
		JSONSchema: validator.CompileSchemaForMerge(parse("values:\n  label: { type: string }\n"), nil),
	}
	lookup := func(uri string) (validator.CatalogEntry, bool) {
		if uri == "core/test/parent-feat" {
			return parentEntry, true
		}
		return validator.CatalogEntry{}, false
	}
	comp := validator.CompositionFromResult("core/test/child-feat", validator.ResolveInheritance(childEntry, lookup))
	if comp == nil || comp.EffectiveSchema == nil {
		t.Fatal("no composed effective schema for the extends-ing feature")
	}
	feat = &types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/test/child-feat", Version: "1.0.0"},
		Spec: types.FeatureSpec{
			Extends: "core/test/parent-feat",
			Schema:  parse("values:\n  label: { type: string }\n"),
		},
	}
	return comp.EffectiveSchema, feat
}

// TestAnnotationPath_ExtendsFeature_ValuePath proves the annotation VALUE path
// (validateSubkeyValueAgainstFeatureSchema → schemaFor) validates an extends-ing
// Feature against the COMPOSED effective schema, not the child-only structural
// schema. Site A of the holdout.
func TestAnnotationPath_ExtendsFeature_ValuePath(t *testing.T) {
	composed, feat := extendsFeatureFixture(t)
	eff := func(string) (compactform.Schema, bool) { return composed, true }

	// Composed cache: the INHERITED threshold is validated — 5 conforms, 99 (over
	// the inherited maximum 10) is rejected.
	if err := validateSubkeyValueAgainstFeatureSchema(newFeatureSchemaCache(eff), feat, "values", "threshold", 5); err != nil {
		t.Errorf("threshold=5 is valid under the inherited maximum 10; got %v", err)
	}
	if err := validateSubkeyValueAgainstFeatureSchema(newFeatureSchemaCache(eff), feat, "values", "threshold", 99); err == nil {
		t.Error("threshold=99 exceeds the inherited maximum 10; must be rejected against the composed schema")
	}
	// Structural-only cache (nil accessor, pre-fix): the inherited key is
	// unrecognised — proving the fix changes behavior.
	if err := validateSubkeyValueAgainstFeatureSchema(newFeatureSchemaCache(nil), feat, "values", "threshold", 5); err == nil {
		t.Error("structural child-only schema lacks the inherited threshold; expected the pre-fix undeclared-subkey rejection")
	}
}

// TestAnnotationPath_ExtendsFeature_BindingTarget proves the annotation
// BINDING-TARGET path (ValidateAnnotationBindingTargets) recognises a binding
// that routes through a pillar key INHERITED from the parent, against the
// composed effective schema. Site B of the holdout — a distinct entry point
// (keyed by binding.Feature), so it needs its own end-to-end proof.
func TestAnnotationPath_ExtendsFeature_BindingTarget(t *testing.T) {
	composed, feat := extendsFeatureFixture(t)

	// A Manifest annotation binding routing through the INHERITED values.threshold.
	ann := types.OrderedMap[types.AnnotationBinding]{}
	ann.Set("feature.test/tune", types.AnnotationBinding{
		Feature: "core/test/child-feat",
		Maps:    types.AnnotationBindingMaps{Values: []string{"threshold"}},
	})
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{
		Features: &types.MappingTier{Annotations: &ann},
	}}}

	featureLookup := func(uri string) (types.Feature, bool) {
		if uri == "core/test/child-feat" {
			return *feat, true
		}
		return types.Feature{}, false
	}

	// Composed accessor: the inherited threshold path is recognised → accepted.
	composedLookup := CatalogLookup{
		Feature:         featureLookup,
		EffectiveSchema: func(string) (compactform.Schema, bool) { return composed, true },
	}
	if err := ValidateAnnotationBindingTargets(manifest, composedLookup); err != nil {
		t.Errorf("binding to the inherited values.threshold must be accepted against the composed schema; got %v", err)
	}

	// Structural-only (nil accessor, pre-fix): the child-only schema lacks
	// threshold → the binding is rejected as routing through an undeclared path.
	structuralLookup := CatalogLookup{Feature: featureLookup}
	err := ValidateAnnotationBindingTargets(manifest, structuralLookup)
	if err == nil {
		t.Fatal("structural child-only schema lacks the inherited threshold; binding must be rejected pre-fix")
	}
	if !strings.Contains(err.Error(), "threshold") {
		t.Errorf("expected the rejection to name the undeclared path threshold; got %v", err)
	}
}
