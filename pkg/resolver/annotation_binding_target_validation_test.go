// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// Annotation binding target validation tests. Each test constructs a
// manifest with one or more annotation bindings plus a per-test mock
// CatalogLookup returning Features with specific spec.schema shapes.
// The validator's job is to verify every binding-declared path
// (in Maps.Infra and Maps.Values) corresponds to a property the target
// Feature's spec.schema actually declares at the named pillar.

// validateBindingTargets asks the binding-target rule of a Manifest through the
// entry the pipeline itself calls, wrapping the document in the resolve product
// that entry takes.
//
// The wrapper exists so a test states the Manifest and the catalog it is about
// and nothing else. The entry is asked over the resolve's products because the
// rule reads the catalog, which is reachable only once the resolve holds one; a
// test naming an Aggregate document as well would be fixing a fact this rule
// does not read.
func validateBindingTargets(manifest *types.Manifest, catalog CatalogLookup) error {
	return ValidateAnnotationBindingTargets(&ResolveResult{Manifest: manifest}, catalog)
}

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
	if err := validateBindingTargets(manifest, catalog); err != nil {
		t.Fatalf("expected nil for clean bindings; got %v", err)
	}
}

// mockCatalogLookupWithLibs is mockCatalogLookup plus a SchemaLibrary set, so a
// bound Feature whose schema carries a cross-document $ref can resolve it.
func mockCatalogLookupWithLibs(features map[string]types.Feature, libs map[string]types.SchemaLibrary) CatalogLookup {
	c := mockCatalogLookup(features)
	c.SchemaLibraries = libs
	return c
}

func schemaLibrary(t *testing.T, src string) types.SchemaLibrary {
	t.Helper()
	var lib types.SchemaLibrary
	if err := yaml.Unmarshal([]byte(src), &lib); err != nil {
		t.Fatalf("schemalibrary unmarshal: %v", err)
	}
	return lib
}

// TestValidateBindingTargets_CrossDocRefSchema_ResolvesWithLibraries pins the
// fix: an annotation-bound Feature whose spec.schema uses the standard
// cross-document $ref (BaseComponents#/schemas/BaseFeatureParameter — the shape
// nearly every core/ Feature, including core/prometheus-exporter, uses) validates
// WITHOUT halting when the catalog's SchemaLibraries are supplied. The
// belt-and-suspenders second arm asserts the same binding still ERRORS without
// them, proving the library supply is causally what resolves the $ref rather than
// a coincidental pass.
func TestValidateBindingTargets_CrossDocRefSchema_ResolvesWithLibraries(t *testing.T) {
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
        feature.aggrega.ai/monitoring:
          feature: core/prometheus-exporter:1.0.0
          maps:
            infra:
              - interval
              - port
`)
	// Standard Feature schema shape: a cross-document $ref in the values pillar,
	// plus a local infra block the binding routes through.
	feature := featureWithSchema(t, "core/prometheus-exporter", `
values:
  $ref: "BaseComponents#/schemas/BaseFeatureParameter"
  properties:
    scrapePath: { type: string }
infra:
  interval: { type: string }
  port: { type: string }
`)
	features := map[string]types.Feature{"core/prometheus-exporter:1.0.0": feature}
	libs := map[string]types.SchemaLibrary{
		"BaseComponents": schemaLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: 1.0.0
spec:
  schemas:
    BaseFeatureParameter:
      enabled: { type: boolean }
`),
	}

	// With the libraries supplied, the cross-document $ref resolves and the infra
	// binding paths validate.
	if err := validateBindingTargets(manifest, mockCatalogLookupWithLibs(features, libs)); err != nil {
		t.Fatalf("expected nil with SchemaLibraries supplied; got %v", err)
	}

	// Without them, the same binding halts on the unresolved $ref — the defect
	// this fix removes.
	if err := validateBindingTargets(manifest, mockCatalogLookup(features)); err == nil {
		t.Fatal("expected a compile error without SchemaLibraries; got nil")
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
	err := validateBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected error for missing infra path; got nil")
	}
	if !stderrors.Is(err, ErrAnnotationBindingTargetUnresolved()) {
		t.Errorf("halt should carry the binding-target condition; got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
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
	err := validateBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected error for missing values path; got nil")
	}
	if !stderrors.Is(err, ErrAnnotationBindingTargetUnresolved()) {
		t.Errorf("halt should carry the binding-target condition; got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
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
	if err := validateBindingTargets(manifest, catalog); err != nil {
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
	err := validateBindingTargets(manifest, catalog)
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
	if err := validateBindingTargets(manifest, catalog); err != nil {
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
	err := validateBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected error for missing target Feature; got nil")
	}
	if !stderrors.Is(err, ErrAnnotationBindingTargetUnresolved()) {
		t.Errorf("halt should carry the binding-target condition; got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"feature.myco/missing",
		"core/missing:1.0.0",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull: %s", want, msg)
		}
	}
}

// TestValidateBindingTargets_NamesEveryUnresolvedBindingAndRoute pins the reading
// an author gets from a Manifest that breaks this rule several ways: every
// offending binding is named, and within a binding every offending route, in one
// pass rather than one per build.
//
// The clean binding in the fixture is what makes the count a verdict rather than
// a constant: a rule raising on every binding regardless would report five, and
// one stopping at the first offender would report one. The binding whose target
// Feature is absent contributes exactly one finding however many routes it
// declares, because with no Feature there is no contract for a route to be
// measured against.
func TestValidateBindingTargets_NamesEveryUnresolvedBindingAndRoute(t *testing.T) {
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
        absent-target:
          feature: core/absent:1.0.0
          maps:
            infra:
              - pool_size
            values:
              - db_name
        clean-binding:
          feature: core/db-pool:1.0.0
          maps:
            infra:
              - pool_size
        two-bad-routes:
          feature: core/db-pool:1.0.0
          maps:
            infra:
              - not_declared_one
              - not_declared_two
        one-bad-route:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - not_declared_three
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/db-pool:1.0.0": featureWithSchema(t, "core/db-pool", `
infra:
  pool_size:
    type: integer
values:
  db_name:
    type: string
`),
	})

	err := validateBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected a refusal on the unresolved bindings; got nil")
	}

	var joined interface{ Unwrap() []error }
	if !stderrors.As(err, &joined) {
		t.Fatalf("several offending bindings should compose into a joined refusal; got %T: %v", err, err)
	}
	if got, want := len(joined.Unwrap()), 4; got != want {
		t.Fatalf("findings: got %d, want %d — one for the absent target, two for the binding "+
			"with two undeclared routes, one for the binding with one, and none for the clean "+
			"binding", got, want)
	}

	msg := err.Error()
	for _, want := range []string{
		"absent-target", "core/absent:1.0.0",
		"two-bad-routes", "not_declared_one", "not_declared_two",
		"one-bad-route", "not_declared_three",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q\nfull: %s", want, msg)
		}
	}
	if strings.Contains(msg, "clean-binding") {
		t.Errorf("refusal names the binding whose routes all resolve, so the rule is raising on "+
			"every binding rather than on the offending ones\nfull: %s", msg)
	}
	if !stderrors.Is(err, ErrAnnotationBindingTargetUnresolved()) {
		t.Errorf("the joined refusal should still match the sentinel by identity; got chain: %v", err)
	}
}

// TestValidateBindingTargets_UnresolvableTargetSchemaIsReportedOncePerFeature pins
// where a target Feature whose declared schema does not resolve is reported: once,
// by the resolution that met the failure, naming that failure verbatim — not once
// per route by the rule.
//
// The fixture declares two routes onto the one unresolvable Feature precisely so
// the count discriminates. A rule raising per route would report two and would
// attribute a catalog entity's own defect to each binding path that happened to
// reach it.
func TestValidateBindingTargets_UnresolvableTargetSchemaIsReportedOncePerFeature(t *testing.T) {
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
        routes-to-broken:
          feature: core/broken:1.0.0
          maps:
            infra:
              - first_route
              - second_route
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/broken:1.0.0": featureWithSchema(t, "core/broken", `
infra:
  first_route:
    $ref: "NoSuchLibrary#/schemas/NoSuchDefinition"
`),
	})

	err := validateBindingTargets(manifest, catalog)
	if err == nil {
		t.Fatal("expected a refusal on the unresolvable target schema; got nil")
	}

	var joined interface{ Unwrap() []error }
	if !stderrors.As(err, &joined) {
		t.Fatalf("expected a joined refusal; got %T: %v", err, err)
	}
	if got, want := len(joined.Unwrap()), 1; got != want {
		t.Fatalf("findings: got %d, want %d — the resolution failure is reported once for the "+
			"Feature, not once for each route reaching it", got, want)
	}

	msg := err.Error()
	// The resolution failure's own text is carried through rather than restated:
	// it already names the offending key and the keyword that broke.
	for _, want := range []string{"core/broken:1.0.0", "NoSuchLibrary"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not carry %q\nfull: %s", want, msg)
		}
	}
	if !stderrors.Is(err, ErrAnnotationBindingTargetUnresolved()) {
		t.Errorf("the refusal should carry the binding-target condition; got chain: %v", err)
	}
}
