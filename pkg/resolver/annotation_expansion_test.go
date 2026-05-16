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
// `feature.myco/rewrite-host` that routes a scalar annotation value
// to the target Feature's infra.upstream field. The binding declares
// only the routing; validating types derive from the target Feature's
// spec.schema.infra.upstream declaration.

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
          maps:
            infra:
              - upstream
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

func TestExpandAnnotations_RoutesToBindingSingleSubkey(t *testing.T) {
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
		t.Fatalf("Feature.Infra.upstream missing — scalar should route to the binding's single declared path")
	}
	if upstream != "scalar.example.com" {
		t.Errorf("upstream = %v, want scalar.example.com", upstream)
	}
}

func TestExpandAnnotations_ScalarRoutesToNestedPath(t *testing.T) {
	t.Parallel()
	// A binding declaring a dotted path routes the scalar value through
	// the path components, creating nested OrderedMap intermediates as
	// it walks. The synthetic Feature's Infra carries the value at the
	// dotted leaf.
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
          maps:
            infra:
              - ingress.host
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
            feature.myco/rewrite-host: "api.example.com"
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
		t.Fatalf("api missing synthetic Feature")
	}
	ingress, ok := feat.Infra.Get("ingress")
	if !ok {
		t.Fatalf("Feature.Infra.ingress missing — dotted path should produce nested intermediate")
	}
	switch n := ingress.(type) {
	case *types.OrderedMap[any]:
		host, ok := n.Get("host")
		if !ok {
			t.Fatalf("Feature.Infra.ingress.host missing on nested OrderedMap")
		}
		if host != "api.example.com" {
			t.Errorf("ingress.host = %v, want api.example.com", host)
		}
	case types.OrderedMap[any]:
		host, ok := n.Get("host")
		if !ok {
			t.Fatalf("Feature.Infra.ingress.host missing on nested OrderedMap")
		}
		if host != "api.example.com" {
			t.Errorf("ingress.host = %v, want api.example.com", host)
		}
	default:
		t.Errorf("Feature.Infra.ingress should be a nested OrderedMap; got %T", ingress)
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

func TestExpandAnnotations_ScalarMultiMap_ReturnsOpsError(t *testing.T) {
	t.Parallel()
	// A binding declaring multiple paths (across the Infra and Values
	// lists, total) paired with a scalar annotation value cannot resolve
	// routing unambiguously — which path receives the value? Expansion
	// halts with an OPS_ERROR naming the binding and its declared paths.
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
          maps:
            infra:
              - upstream
              - timeout
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
            feature.myco/rewrite-host: "scalar-cant-route-to-two-paths"
`
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(manifestSrc), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	rr := &ResolveResult{Manifest: &m, Release: loadTestRelease(t, releaseSrc)}
	err := ExpandAnnotations(rr)
	if err == nil {
		t.Fatalf("expected OPS_ERROR on ambiguous routing; got nil")
	}
	if !strings.Contains(err.Error(), "OPS_ERROR") {
		t.Errorf("error missing OPS_ERROR prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "multiple subkey paths") {
		t.Errorf("error missing 'multiple subkey paths' diagnostic: %v", err)
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
	// An empty-string annotation value populates no field on the
	// synthetic Feature attachment. FeatureRecipe rendering supplies
	// any target-Feature schema defaults downstream — the expansion
	// layer carries only the fields the author populated through the
	// annotation value.
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
          maps:
            infra:
              - upstream
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
          maps:
            infra:
              - upstream
        feature.myco/metrics:
          feature: core/metrics-exporter:1.0.0
          maps:
            infra:
              - endpoint
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

// TestSetPillarLeaf_NavigatesValueTypedIntermediate pins the
// value-typed-intermediate descent path. YAML unmarshalling nested
// objects, programmatic OrderedMap construction without explicit
// pointers, and any caller that stores intermediate maps by value will
// land in this code path. The leaf write MUST propagate to the parent
// pillar tree; a self-referential or local-copy write would silently
// drop the contribution.
func TestSetPillarLeaf_NavigatesValueTypedIntermediate(t *testing.T) {
	t.Parallel()
	pillar := types.OrderedMap[any]{}
	intermediate := types.OrderedMap[any]{}
	intermediate.Set("existing", "preserved")
	pillar.Set("ingress", intermediate) // value-typed, not &intermediate

	setPillarLeaf(&pillar, "ingress.host", "new-host")

	got, ok := pillar.Get("ingress")
	if !ok {
		t.Fatal("ingress missing from pillar after setPillarLeaf")
	}
	var ingressMap types.OrderedMap[any]
	switch n := got.(type) {
	case *types.OrderedMap[any]:
		ingressMap = *n
	case types.OrderedMap[any]:
		ingressMap = n
	default:
		t.Fatalf("ingress entry has unexpected type %T", got)
	}
	host, ok := ingressMap.Get("host")
	if !ok {
		t.Fatal("ingress.host not propagated to parent — value-typed-intermediate descent dropped the write")
	}
	if host != "new-host" {
		t.Errorf("host = %v, want new-host", host)
	}
	existing, ok := ingressMap.Get("existing")
	if !ok {
		t.Fatal("pre-existing sibling entry lost during value-typed-intermediate descent")
	}
	if existing != "preserved" {
		t.Errorf("existing = %v, want preserved", existing)
	}
}

// TestSetPillarLeaf_NavigatesPointerTypedIntermediate pins the
// pointer-typed-intermediate descent path. Subsequent setPillarLeaf
// calls into the same pillar tree should reuse the existing pointer
// intermediate rather than creating a fresh one.
func TestSetPillarLeaf_NavigatesPointerTypedIntermediate(t *testing.T) {
	t.Parallel()
	pillar := types.OrderedMap[any]{}
	setPillarLeaf(&pillar, "ingress.enabled", true)
	setPillarLeaf(&pillar, "ingress.host", "shared-host")

	got, _ := pillar.Get("ingress")
	ingress, ok := got.(*types.OrderedMap[any])
	if !ok {
		t.Fatalf("ingress should be *OrderedMap after first setPillarLeaf; got %T", got)
	}
	enabled, _ := ingress.Get("enabled")
	host, _ := ingress.Get("host")
	if enabled != true {
		t.Errorf("ingress.enabled = %v, want true", enabled)
	}
	if host != "shared-host" {
		t.Errorf("ingress.host = %v, want shared-host", host)
	}
}

// TestSetPillarLeaf_TopLevelLeaf pins the no-intermediate case: a
// single-component path writes a leaf directly into the pillar tree.
func TestSetPillarLeaf_TopLevelLeaf(t *testing.T) {
	t.Parallel()
	pillar := types.OrderedMap[any]{}
	setPillarLeaf(&pillar, "upstream", "api.example.com")
	got, ok := pillar.Get("upstream")
	if !ok {
		t.Fatal("upstream missing")
	}
	if got != "api.example.com" {
		t.Errorf("upstream = %v, want api.example.com", got)
	}
}
