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
