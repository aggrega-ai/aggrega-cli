// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	comp := validator.CompositionFromResult("core/test/child-feat", validator.ResolveFeatureSchemaChain(childEntry, lookup))
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
// validates an extends-ing Feature against the COMPOSED effective schema rather
// than against the child-only structural one, driven through the declared rule's
// own entry point.
//
// The value path and the binding-target path below now read one compiled
// contract, produced once by the presentation both rules are asked over, so the
// composed-schema property holds for both by construction rather than by two
// producers agreeing. The pair is kept as two tests because the two rules answer
// different questions about the same Feature — one about what the Manifest
// declares, one about what an author wrote through it — and each has to be shown
// reaching the inherited key.
func TestAnnotationPath_ExtendsFeature_ValuePath(t *testing.T) {
	composed, feat := extendsFeatureFixture(t)

	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.test/tune:
          feature: core/test/child-feat
          maps:
            values:
              - threshold
`)
	featureLookup := func(uri string) (types.Feature, bool) {
		if uri == "core/test/child-feat" {
			return *feat, true
		}
		return types.Feature{}, false
	}
	composedLookup := CatalogLookup{
		Feature:         featureLookup,
		EffectiveSchema: func(string) (compactform.Schema, bool) { return composed, true },
	}

	// The INHERITED threshold is validated: 5 conforms, 99 exceeds the inherited
	// maximum of 10.
	if err := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.test/tune:\n  threshold: 5\n"),
		nil, composedLookup); err != nil {
		t.Errorf("threshold=5 is valid under the inherited maximum 10; got %v", err)
	}
	if err := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.test/tune:\n  threshold: 99\n"),
		nil, composedLookup); err == nil {
		t.Error("threshold=99 exceeds the inherited maximum 10; must be rejected against the composed schema")
	}
	// Structural-only lookup: the child's own schema declares no threshold, so the
	// inherited key is unrecognised and the conforming value is refused — which is
	// what shows the composed reading above is doing the work.
	structuralLookup := CatalogLookup{Feature: featureLookup}
	if err := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.test/tune:\n  threshold: 5\n"),
		nil, structuralLookup); err == nil {
		t.Error("the structural child-only schema lacks the inherited threshold; expected an undeclared-subkey refusal")
	}
}

// TestAnnotationPath_ExtendsFeature_BindingTarget proves the annotation
// BINDING-TARGET path recognises a binding routing through a pillar key
// INHERITED from the parent, against the composed effective schema. It is a
// distinct entry point keyed by the binding's Feature, so it carries its own
// end-to-end proof.
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
	if err := validateBindingTargets(manifest, composedLookup); err != nil {
		t.Errorf("binding to the inherited values.threshold must be accepted against the composed schema; got %v", err)
	}

	// Structural-only (nil accessor, pre-fix): the child-only schema lacks
	// threshold → the binding is rejected as routing through an undeclared path.
	structuralLookup := CatalogLookup{Feature: featureLookup}
	err := validateBindingTargets(manifest, structuralLookup)
	if err == nil {
		t.Fatal("structural child-only schema lacks the inherited threshold; binding must be rejected pre-fix")
	}
	if !strings.Contains(err.Error(), "threshold") {
		t.Errorf("expected the rejection to name the undeclared path threshold; got %v", err)
	}
}
