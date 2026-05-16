// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// Test fixtures: a Manifest declaring a single annotation binding
// `feature.myco/rewrite-host` whose infra schema declares an
// `upstream` string field followed by an `enforce` boolean field with
// default true. The schema is JSON Schema 2020-12.

const testManifestYAML = `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/rewrite-host: rewrite-host-helm:1.0.0
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          infra:
            type: object
            properties:
              upstream:
                type: string
              enforce:
                type: boolean
                default: true
`

func loadTestManifest(t *testing.T) *types.Manifest {
	t.Helper()
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(testManifestYAML), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	return &m
}

func loadTestRelease(t *testing.T, src string) *types.Release {
	t.Helper()
	var r types.Release
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("release unmarshal: %v", err)
	}
	return &r
}

func TestExpandAnnotations_BoundaryCascade_AllComponentsInherit(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "upstream.example.com"
      components:
        api:
          description: api comp
        worker:
          description: worker comp
`
	rr := &ResolveResult{Manifest: loadTestManifest(t), Release: loadTestRelease(t, src)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	for _, name := range []string{"api", "worker"} {
		comp, ok := prod.Components.Get(name)
		if !ok {
			t.Fatalf("component %q missing", name)
		}
		feat, ok := comp.Features.Get("feature.myco/rewrite-host")
		if !ok {
			t.Errorf("component %q missing inherited synthetic Feature", name)
			continue
		}
		if feat.Type != "core/rewrite-host:1.0.0" {
			t.Errorf("component %q Feature.Type = %q, want core/rewrite-host:1.0.0", name, feat.Type)
		}
		upstream, _ := feat.Infra.Get("upstream")
		if upstream != "upstream.example.com" {
			t.Errorf("component %q Feature.Infra.upstream = %v, want upstream.example.com", name, upstream)
		}
	}
}

func TestExpandAnnotations_ComponentOverride_ComponentValueWins(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "boundary.example.com"
      components:
        api:
          description: api comp
        special:
          description: overrides
          annotations:
            feature.myco/rewrite-host: "override.example.com"
`
	rr := &ResolveResult{Manifest: loadTestManifest(t), Release: loadTestRelease(t, src)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	special, _ := prod.Components.Get("special")
	apiFeat, _ := api.Features.Get("feature.myco/rewrite-host")
	specialFeat, _ := special.Features.Get("feature.myco/rewrite-host")
	apiUpstream, _ := apiFeat.Infra.Get("upstream")
	specialUpstream, _ := specialFeat.Infra.Get("upstream")
	if apiUpstream != "boundary.example.com" {
		t.Errorf("api upstream = %v, want boundary.example.com", apiUpstream)
	}
	if specialUpstream != "override.example.com" {
		t.Errorf("special upstream = %v, want override.example.com (component override)", specialUpstream)
	}
}

func TestExpandAnnotations_ScalarValueMapsToFirstSchemaField(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            feature.myco/rewrite-host: "scalar.example.com"
`
	rr := &ResolveResult{Manifest: loadTestManifest(t), Release: loadTestRelease(t, src)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatalf("api missing synthetic Feature")
	}
	upstream, ok := feat.Infra.Get("upstream")
	if !ok {
		t.Fatalf("Feature.Infra.upstream missing — scalar should map to first schema field")
	}
	if upstream != "scalar.example.com" {
		t.Errorf("upstream = %v, want scalar.example.com", upstream)
	}
}

func TestExpandAnnotations_RecursiveNestedBoundariesCascade(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    outer:
      annotations:
        feature.myco/rewrite-host: "outer.example.com"
      boundaries:
        middle:
          annotations:
            feature.myco/rewrite-host: "middle.example.com"
          boundaries:
            inner:
              components:
                leaf:
                  description: leaf comp
`
	rr := &ResolveResult{Manifest: loadTestManifest(t), Release: loadTestRelease(t, src)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	outer, _ := rr.Release.Spec.Boundaries.Get("outer")
	middle, _ := outer.Boundaries.Get("middle")
	inner, _ := middle.Boundaries.Get("inner")
	leaf, ok := inner.Components.Get("leaf")
	if !ok {
		t.Fatalf("leaf component missing")
	}
	feat, ok := leaf.Features.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatalf("leaf missing inherited synthetic Feature from outer/middle cascade")
	}
	upstream, _ := feat.Infra.Get("upstream")
	if upstream != "middle.example.com" {
		t.Errorf("leaf upstream = %v, want middle.example.com (middle overrides outer)", upstream)
	}
}

func TestExpandAnnotations_ValidationFailure_ReturnsOpsError(t *testing.T) {
	t.Parallel()
	manifestSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/rewrite-host: rewrite-host-helm:1.0.0
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          infra:
            type: object
            properties:
              upstream:
                type: integer
`
	releaseSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            feature.myco/rewrite-host: "not-an-integer"
`
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(manifestSrc), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	rr := &ResolveResult{Manifest: &m, Release: loadTestRelease(t, releaseSrc)}
	err := ExpandAnnotations(rr)
	if err == nil {
		t.Fatalf("expected OPS_ERROR on type mismatch; got nil")
	}
	if !strings.Contains(err.Error(), "OPS_ERROR") {
		t.Errorf("error missing OPS_ERROR prefix: %v", err)
	}
}

func TestExpandAnnotations_NoBindings_NoOp(t *testing.T) {
	t.Parallel()
	manifestSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/server: server-helm:1.0.0
`
	releaseSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            unrelated.example/key: "value"
`
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(manifestSrc), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	rr := &ResolveResult{Manifest: &m, Release: loadTestRelease(t, releaseSrc)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	if api.Features.Len() != 0 {
		t.Errorf("api should have no synthetic Features when registry is empty; got %d", api.Features.Len())
	}
}

func TestExpandAnnotations_ScalarValue_DefaultsNotMergedHere(t *testing.T) {
	t.Parallel()
	// Schema declares two fields: `upstream` (string, no default) and
	// `enforce` (boolean, default: true). A scalar annotation value maps
	// to the first field (upstream); the binding's `enforce: true`
	// default is NOT merged into the synthetic Feature's Infra at this
	// layer — default materialization lives in the FeatureRecipe
	// rendering layer so explicit and synthetic attachments take the
	// same default-fill path downstream. This test pins the policy so
	// future readers can't silently flip it.
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            feature.myco/rewrite-host: "scalar.example.com"
`
	rr := &ResolveResult{Manifest: loadTestManifest(t), Release: loadTestRelease(t, src)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatalf("api missing synthetic Feature")
	}
	if _, hasEnforce := feat.Infra.Get("enforce"); hasEnforce {
		t.Errorf("synthetic Feature should NOT carry schema-default enforce=true; that responsibility belongs to the FeatureRecipe rendering layer. Got Infra keys: %v", feat.Infra.Keys())
	}
	if feat.Infra.Len() != 1 {
		t.Errorf("synthetic Feature Infra should carry exactly the one author-populated field (upstream); got %d fields: %v", feat.Infra.Len(), feat.Infra.Keys())
	}
}

func TestExpandAnnotations_EmptyStringValue_EmptyInfra(t *testing.T) {
	t.Parallel()
	// An empty-string annotation value means "accept all schema
	// defaults". The synthetic Feature's Infra is empty
	// at this layer; FeatureRecipe rendering supplies the defaults
	// downstream. The test pins the empty-string code path that
	// parseAnnotationValue treats as the no-fields case.
	manifestSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/rewrite-host: rewrite-host-helm:1.0.0
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          infra:
            type: object
            properties:
              upstream:
                type: string
                default: "default-upstream.example.com"
              enforce:
                type: boolean
                default: true
`
	releaseSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            feature.myco/rewrite-host: ""
`
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(manifestSrc), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	rr := &ResolveResult{Manifest: &m, Release: loadTestRelease(t, releaseSrc)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatalf("api missing synthetic Feature for empty-string annotation")
	}
	if feat.Type != "core/rewrite-host:1.0.0" {
		t.Errorf("synthetic Feature Type = %q, want core/rewrite-host:1.0.0", feat.Type)
	}
	if feat.Infra.Len() != 0 {
		t.Errorf("empty-string annotation should produce empty Infra (defaults applied downstream by FeatureRecipe rendering); got %d fields: %v", feat.Infra.Len(), feat.Infra.Keys())
	}
}

func TestExpandAnnotations_ExplicitFeaturePreserved_NoOverwrite(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "annotation.example.com"
      components:
        api:
          features:
            feature.myco/rewrite-host:
              type: core/rewrite-host:1.0.0
              description: explicit
`
	rr := &ResolveResult{Manifest: loadTestManifest(t), Release: loadTestRelease(t, src)}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	feat, _ := api.Features.Get("feature.myco/rewrite-host")
	if feat.Description != "explicit" {
		t.Errorf("explicit Feature was overwritten by synthetic injection; description = %q, want explicit", feat.Description)
	}
}

func TestExpandAnnotations_RecordsProvenance_BoundaryLevel(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "upstream.example.com"
      components:
        api:
          description: api comp
`
	rr := &ResolveResult{
		Manifest:             loadTestManifest(t),
		Release:              loadTestRelease(t, src),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	key := AnnotationProvenanceKey("prod", "api", "feature.myco/rewrite-host")
	rec, ok := rr.AnnotationProvenance[key]
	if !ok {
		t.Fatalf("provenance missing for boundary-inherited annotation; keys: %v", keysOf(rr.AnnotationProvenance))
	}
	if rec.AnnotationKey != "feature.myco/rewrite-host" {
		t.Errorf("AnnotationKey = %q, want feature.myco/rewrite-host", rec.AnnotationKey)
	}
	wantSource := "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host"
	if rec.AnnotationSource != wantSource {
		t.Errorf("AnnotationSource = %q, want %q (must point to boundary where author wrote it, not where it applied)", rec.AnnotationSource, wantSource)
	}
}

func TestExpandAnnotations_RecordsProvenance_ComponentLevel(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            feature.myco/rewrite-host: "component-level.example.com"
`
	rr := &ResolveResult{
		Manifest:             loadTestManifest(t),
		Release:              loadTestRelease(t, src),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	rec, ok := rr.AnnotationProvenance[AnnotationProvenanceKey("prod", "api", "feature.myco/rewrite-host")]
	if !ok {
		t.Fatalf("provenance missing for component-level annotation")
	}
	wantSource := "release.yaml:boundaries.prod.components.api.annotations.feature.myco/rewrite-host"
	if rec.AnnotationSource != wantSource {
		t.Errorf("AnnotationSource = %q, want %q", rec.AnnotationSource, wantSource)
	}
}

func TestExpandAnnotations_RecordsProvenance_ComponentOverrideUpdatesSource(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "boundary.example.com"
      components:
        api:
          description: inherits boundary
        special:
          annotations:
            feature.myco/rewrite-host: "override.example.com"
`
	rr := &ResolveResult{
		Manifest:             loadTestManifest(t),
		Release:              loadTestRelease(t, src),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	apiRec, _ := rr.AnnotationProvenance[AnnotationProvenanceKey("prod", "api", "feature.myco/rewrite-host")]
	specialRec, _ := rr.AnnotationProvenance[AnnotationProvenanceKey("prod", "special", "feature.myco/rewrite-host")]
	if apiRec.AnnotationSource != "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host" {
		t.Errorf("api source should point to boundary; got %q", apiRec.AnnotationSource)
	}
	if specialRec.AnnotationSource != "release.yaml:boundaries.prod.components.special.annotations.feature.myco/rewrite-host" {
		t.Errorf("special source should point to component override; got %q", specialRec.AnnotationSource)
	}
}

func TestExpandAnnotations_NullOptOut_BoundaryToComponent(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "boundary.example.com"
      components:
        api:
          description: inherits boundary
        opted-out:
          annotations:
            feature.myco/rewrite-host: null
`
	rr := &ResolveResult{
		Manifest:             loadTestManifest(t),
		Release:              loadTestRelease(t, src),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")

	// Sibling component inherits the boundary annotation and gets the
	// synthetic Feature.
	api, _ := prod.Components.Get("api")
	if _, ok := api.Features.Get("feature.myco/rewrite-host"); !ok {
		t.Errorf("sibling component should inherit the boundary-cascaded Feature")
	}
	if _, ok := rr.AnnotationProvenance[AnnotationProvenanceKey("prod", "api", "feature.myco/rewrite-host")]; !ok {
		t.Errorf("provenance missing for sibling component")
	}

	// Opt-out component does NOT get the synthetic Feature.
	optedOut, _ := prod.Components.Get("opted-out")
	if _, ok := optedOut.Features.Get("feature.myco/rewrite-host"); ok {
		t.Errorf("opt-out component should NOT have the inherited Feature")
	}
	if _, ok := rr.AnnotationProvenance[AnnotationProvenanceKey("prod", "opted-out", "feature.myco/rewrite-host")]; ok {
		t.Errorf("opt-out component should produce no provenance record (no synthetic Feature, no record)")
	}
}

func TestExpandAnnotations_NullOptOut_NoInheritance_IsNoOp(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      components:
        api:
          annotations:
            feature.myco/rewrite-host: null
`
	rr := &ResolveResult{
		Manifest:             loadTestManifest(t),
		Release:              loadTestRelease(t, src),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	if _, ok := api.Features.Get("feature.myco/rewrite-host"); ok {
		t.Errorf("explicit null with no inheritance should be a no-op, not produce a Feature")
	}
	if len(rr.AnnotationProvenance) != 0 {
		t.Errorf("no provenance should be recorded for a no-op opt-out; got %d entries", len(rr.AnnotationProvenance))
	}
}

func TestExpandAnnotations_NullOptOut_PreservesOtherKeys(t *testing.T) {
	t.Parallel()
	manifestSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/rewrite-host: rewrite-host-helm:1.0.0
        core/metrics-exporter: metrics-helm:1.0.0
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          infra:
            type: object
            properties:
              upstream:
                type: string
        feature.myco/metrics:
          feature: core/metrics-exporter:1.0.0
          infra:
            type: object
            properties:
              endpoint:
                type: string
`
	releaseSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/rewrite-host: "boundary.example.com"
        feature.myco/metrics: "metrics.example.com"
      components:
        api:
          annotations:
            feature.myco/rewrite-host: null
`
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(manifestSrc), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	rr := &ResolveResult{
		Manifest:             &m,
		Release:              loadTestRelease(t, releaseSrc),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")

	// Opted-out key: no Feature.
	if _, ok := api.Features.Get("feature.myco/rewrite-host"); ok {
		t.Errorf("opted-out key should not produce a Feature on the api component")
	}
	// Non-opted-out key: Feature inherited from the boundary.
	if _, ok := api.Features.Get("feature.myco/metrics"); !ok {
		t.Errorf("non-opted-out key should still produce a Feature on the api component")
	}
}

func keysOf(m map[string]AnnotationProvenance) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
