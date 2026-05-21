// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// Annotation binding target validation tests. Each test constructs a
// manifest with one or more annotation bindings plus a per-test mock
// CatalogLookup returning Features with specific spec.schema shapes.
// The validator's job is to verify every binding-declared path
// (in Maps.Infra and Maps.Values) corresponds to a property the target
// Feature's spec.schema actually declares at the named pillar.

func loadManifest(t *testing.T, src string) *types.Manifest {
	t.Helper()
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	return &m
}

func featureWithSchema(t *testing.T, name, schemaSrc string) types.Feature {
	t.Helper()
	var schemaNode yaml.Node
	if err := yaml.Unmarshal([]byte(schemaSrc), &schemaNode); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	// Unwrap document node.
	node := &schemaNode
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	return types.Feature{
		Metadata: types.FeatureMetadata{Name: name, Version: "1.0.0"},
		Spec:     types.FeatureSpec{Schema: *node},
	}
}

func mockCatalogLookup(features map[string]types.Feature) CatalogLookup {
	return CatalogLookup{
		Feature: func(typeURI string) (types.Feature, bool) {
			f, ok := features[typeURI]
			return f, ok
		},
	}
}

func TestValidateBindingTargets_CleanBindings_ReturnsNil(t *testing.T) {
	t.Parallel()
	// Binding paths resolve cleanly through explicit properties maps
	// for both pillars.
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          maps:
            infra:
              - upstream
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - pool
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/rewrite-host:1.0.0": featureWithSchema(t, "core/rewrite-host", `
infra:
  upstream:
    type: string
`),
		"core/db-pool:1.0.0": featureWithSchema(t, "core/db-pool", `
values:
  pool:
    type: string
`),
	})
	if err := ValidateAnnotationBindingTargets(manifest, catalog); err != nil {
		t.Fatalf("expected nil for clean bindings; got %v", err)
	}
}

func TestValidateBindingTargets_MissingInfraPath_ErrorsLoud(t *testing.T) {
	t.Parallel()
	// Binding routes through infra path "nonexistent" but the
	// Feature schema declares only "upstream".
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          maps:
            infra:
              - nonexistent
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/rewrite-host:1.0.0": featureWithSchema(t, "core/rewrite-host", `
infra:
  upstream:
    type: string
`),
	})
	err := ValidateAnnotationBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected error for missing infra path; got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"OPS_ERROR",
		"feature.myco/rewrite-host",
		"infra",
		"nonexistent",
		"core/rewrite-host:1.0.0",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull: %s", want, msg)
		}
	}
}

func TestValidateBindingTargets_MissingValuesPath_ErrorsLoud(t *testing.T) {
	t.Parallel()
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - replicas
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/db-pool:1.0.0": featureWithSchema(t, "core/db-pool", `
values:
  pool:
    type: string
`),
	})
	err := ValidateAnnotationBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected error for missing values path; got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"OPS_ERROR",
		"feature.myco/db-pool",
		"values",
		"replicas",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull: %s", want, msg)
		}
	}
}

func TestValidateBindingTargets_NestedPath_WalksThroughProperties(t *testing.T) {
	t.Parallel()
	// Dotted path "ingress.host" walks values.properties.ingress.properties.host.
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/host:
          feature: core/ingress:1.0.0
          maps:
            values:
              - ingress.host
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/ingress:1.0.0": featureWithSchema(t, "core/ingress", `
values:
  ingress:
    host:
      type: string
`),
	})
	if err := ValidateAnnotationBindingTargets(manifest, catalog); err != nil {
		t.Fatalf("nested-path walker should resolve cleanly; got %v", err)
	}
}

func TestValidateBindingTargets_NestedPath_MissingTerminalSegment_ErrorsLoud(t *testing.T) {
	t.Parallel()
	// Schema declares "ingress.host" but binding asks for "ingress.port".
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/port:
          feature: core/ingress:1.0.0
          maps:
            values:
              - ingress.port
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/ingress:1.0.0": featureWithSchema(t, "core/ingress", `
values:
  ingress:
    host:
      type: string
`),
	})
	err := ValidateAnnotationBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected error for missing terminal segment; got nil")
	}
	if !strings.Contains(err.Error(), "ingress.port") {
		t.Errorf("error should mention the full bad path 'ingress.port'; got %v", err)
	}
}

func TestValidateBindingTargets_SchemaShapeEscapesWalker_SkipsSoftly(t *testing.T) {
	t.Parallel()
	// Schema carries patternProperties at the pillar root — the walker
	// cannot statically prove the path exists or doesn't exist. Soft
	// no-op; the validator returns nil for this binding.
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/dynamic:
          feature: core/dynamic:1.0.0
          maps:
            values:
              - some.deeply.nested.path
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/dynamic:1.0.0": featureWithSchema(t, "core/dynamic", `
$jsonschema:
  type: object
  properties:
    values:
      type: object
      patternProperties:
        "^.*$":
          type: string
`),
	})
	if err := ValidateAnnotationBindingTargets(manifest, catalog); err != nil {
		t.Fatalf("patternProperties escape should soft-skip; got %v", err)
	}
}

func TestValidateBindingTargets_UnknownFeature_ErrorsLoud(t *testing.T) {
	t.Parallel()
	// Binding points at a Feature type the catalog does not carry.
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/missing:
          feature: core/missing:1.0.0
          maps:
            values:
              - pool
`)
	catalog := mockCatalogLookup(map[string]types.Feature{})
	err := ValidateAnnotationBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected error for missing target Feature; got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"CATALOG_ERROR",
		"feature.myco/missing",
		"core/missing:1.0.0",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull: %s", want, msg)
		}
	}
}
