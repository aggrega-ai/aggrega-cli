// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	err := ExpandAnnotations(rr, CatalogLookup{})
	if err == nil {
		t.Fatalf("expected OPS_ERROR on ambiguous routing; got nil")
	}
	// The whole chain on one line, not the outermost frame alone. The scope wrap
	// is a categorised envelope, whose Error renders a single frame deliberately
	// so the rich multi-line shape stays with the formatter — so a caller reading
	// Error alone gets the scope and drops the finding beneath it. CauseChain is
	// the flattened form for exactly this kind of single-line read.
	chain := cerrors.CauseChain(err)
	if !strings.Contains(chain, "OPS_ERROR") {
		t.Errorf("error missing the OPS_ERROR category: %v", chain)
	}
	if !strings.Contains(chain, "multiple subkey paths") {
		t.Errorf("error missing 'multiple subkey paths' diagnostic: %v", chain)
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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
  environment: testing
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
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
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

// Leaf-writer unit-test coverage for the dotted-path writer lives in
// pkg/types/orderedmap_setleaf_test.go alongside the shared
// types.SetLeafAtPath primitive. Resolver-level call sites that
// depended on the writer's value-typed-intermediate, pointer-typed-
// intermediate, and top-level-leaf disciplines are exercised by the
// integration tests above through ExpandAnnotations.

// Aggregate-side expansion tests. These tests pin the Aggregate-side
// leg of the annotation-driven Feature expansion pass: the
// expandAggregateSubsystems walk, the side-vs-pillar enforcement
// (Aggregate-side routes only through values-pillar paths), null
// opt-out across the subsystem cascade, explicit-feature preservation,
// top-level component expansion, provenance recording, deeply nested
// subsystem cascade, and cross-walker consistency between
// WireAggregateAnnotations and ExpandAnnotations.

const testValuesManifestYAML = `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/db-pool: db-pool-helm:1.0.0
      annotations:
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - pool
`

// loadTestAggregate parses an Aggregate and normalizes it, so a fixture written
// at the top-level `.spec.components` surface reaches the expansion pass in the
// same shape every production load path produces: folded into the synthesized
// default subsystem. Skipping the normalization here would exercise a shape the
// product never builds.
func loadTestAggregate(t *testing.T, src string) *types.Aggregate {
	t.Helper()
	var a types.Aggregate
	if err := yaml.Unmarshal([]byte(src), &a); err != nil {
		t.Fatalf("aggregate unmarshal: %v", err)
	}
	return types.Normalize(&a).Aggregate()
}

func loadValuesManifest(t *testing.T) *types.Manifest {
	t.Helper()
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(testValuesManifestYAML), &m); err != nil {
		t.Fatalf("values manifest unmarshal: %v", err)
	}
	return &m
}

func TestExpand_AggregateSide_ScalarAnnotation_RoutesToValues(t *testing.T) {
	t.Parallel()
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "shared-pool"
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	data, _ := rr.Aggregate.Spec.Subsystems.Get("data")
	api, _ := data.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/db-pool")
	if !ok {
		t.Fatalf("api missing synthetic Feature attachment")
	}
	if feat.Type != "core/db-pool:1.0.0" {
		t.Errorf("Feature.Type = %q, want core/db-pool:1.0.0", feat.Type)
	}
	pool, ok := feat.Values.Get("pool")
	if !ok {
		t.Fatalf("Feature.Values.pool missing — Aggregate-side annotation should route into the values pillar")
	}
	if pool != "shared-pool" {
		t.Errorf("pool = %v, want shared-pool", pool)
	}
}

func TestExpand_AggregateSide_BindingMapsToInfra_ErrorsLoud(t *testing.T) {
	t.Parallel()
	// Aggregate-side annotation paired with a binding that declares
	// only infra paths: the side-vs-pillar mismatch halts the build.
	// The annotation value is authored in the Aggregate, so the halt
	// names the developer's domain; the symmetric Release-side test
	// below names the operator's, and the pair is what shows the two
	// wraps follow their own documents.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/rewrite-host: "api.example.com"
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             loadTestManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, CatalogLookup{})
	if err == nil {
		t.Fatal("expected side-vs-pillar mismatch error; got nil")
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		string(cerrors.DeveloperError),
		"feature.myco/rewrite-host",
		"upstream",
		"infra",
		"aggregate",
		"values",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull message: %s", want, msg)
		}
	}
	if strings.Contains(msg, string(cerrors.OpsError)) {
		t.Errorf("an Aggregate-authored annotation names the operator's domain\nfull message: %s", msg)
	}
}

func TestExpand_ReleaseSide_BindingMapsToValues_ErrorsLoud(t *testing.T) {
	t.Parallel()
	// Release-side annotation paired with a binding that declares
	// only values paths: the symmetric mismatch halts the build.
	releaseSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
  environment: testing
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/db-pool: "from-release"
      components:
        api:
          description: api comp
`
	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Release:              loadTestRelease(t, releaseSrc),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, CatalogLookup{})
	if err == nil {
		t.Fatal("expected side-vs-pillar mismatch error; got nil")
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		"OPS_ERROR",
		"feature.myco/db-pool",
		"pool",
		"values",
		"release",
		"infra",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull message: %s", want, msg)
		}
	}
}

func TestExpand_ScalarRoutesToNestedAggregateValuesPath(t *testing.T) {
	t.Parallel()
	// Aggregate-side scalar annotation routes through a dotted-path
	// values leaf; the synthesized FeatureInstance carries nested
	// OrderedMap intermediates with the leaf value at the named tip.
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
        core/ingress: ingress-helm:1.0.0
      annotations:
        feature.myco/host:
          feature: core/ingress:1.0.0
          maps:
            values:
              - ingress.host
`
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server:1.0.0
      annotations:
        feature.myco/host: "api.example.com"
`
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(manifestSrc), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	rr := &ResolveResult{
		Manifest:             &m,
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	def, _ := rr.Aggregate.Spec.Subsystems.Get("default")
	api, _ := def.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/host")
	if !ok {
		t.Fatalf("api missing synthetic Feature attachment")
	}
	ingressRaw, ok := feat.Values.Get("ingress")
	if !ok {
		t.Fatalf("Feature.Values.ingress missing — dotted path should create nested intermediate")
	}
	ingress, ok := ingressRaw.(*types.OrderedMap[any])
	if !ok {
		t.Fatalf("ingress should be *OrderedMap[any]; got %T", ingressRaw)
	}
	host, ok := ingress.Get("host")
	if !ok {
		t.Fatal("ingress.host missing — dotted leaf write did not reach the nested tip")
	}
	if host != "api.example.com" {
		t.Errorf("host = %v, want api.example.com", host)
	}
}

func TestExpand_AggregateSide_NullOptOut_SubsystemSuppressesContribution(t *testing.T) {
	t.Parallel()
	// Subsystem-level annotation propagates by default; component-level
	// `key: null` opts the component out. Sibling components in the same
	// subsystem retain inheritance.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "shared-pool"
      components:
        api:
          type: core/server:1.0.0
          annotations:
            feature.myco/db-pool: null
        worker:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	data, _ := rr.Aggregate.Spec.Subsystems.Get("data")
	api, _ := data.Components.Get("api")
	if _, present := api.Features.Get("feature.myco/db-pool"); present {
		t.Error("api should have opted out (explicit null at component level removes inheritance)")
	}
	worker, _ := data.Components.Get("worker")
	feat, ok := worker.Features.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("worker should still inherit; sibling opt-out must not affect it")
	}
	pool, _ := feat.Values.Get("pool")
	if pool != "shared-pool" {
		t.Errorf("worker pool = %v, want shared-pool", pool)
	}
}

func TestExpand_AggregateSide_ExplicitFeaturePreserved_NoOverwrite(t *testing.T) {
	t.Parallel()
	// A component that declares an explicit FeatureInstance under the
	// same key as a registered binding keeps the explicit attachment;
	// expansion does not overwrite.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      components:
        api:
          type: core/server:1.0.0
          annotations:
            feature.myco/db-pool: "annotation-value"
          features:
            feature.myco/db-pool:
              type: core/db-pool:1.0.0
              values:
                pool: "explicit-value"
`
	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	data, _ := rr.Aggregate.Spec.Subsystems.Get("data")
	api, _ := data.Components.Get("api")
	feat, _ := api.Features.Get("feature.myco/db-pool")
	pool, _ := feat.Values.Get("pool")
	if pool != "explicit-value" {
		t.Errorf("pool = %v, want explicit-value (annotation expansion must not overwrite explicit attachment)", pool)
	}
}

func TestExpand_AggregateSide_TopLevelComponent(t *testing.T) {
	t.Parallel()
	// A component authored at the top-level `.spec.components` surface carrying
	// its own annotation. Normalization folds that surface into the synthesized
	// default subsystem, so expansion reaches the component there and the
	// canonical component path in the provenance side-table is
	// "subsystems.default.components.<name>".
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server:1.0.0
      annotations:
        feature.myco/db-pool: "top-level-pool"
`
	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	def, _ := rr.Aggregate.Spec.Subsystems.Get("default")
	api, _ := def.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/db-pool")
	if !ok {
		t.Fatalf("top-level api missing synthetic Feature attachment")
	}
	pool, _ := feat.Values.Get("pool")
	if pool != "top-level-pool" {
		t.Errorf("pool = %v, want top-level-pool", pool)
	}
	provKey := AnnotationProvenanceKey("subsystems.default.components.api", "", "feature.myco/db-pool")
	prov, ok := rr.AnnotationProvenance[provKey]
	if !ok {
		t.Fatalf("provenance entry missing at key %q; got keys: %v", provKey, keysOf(rr.AnnotationProvenance))
	}
	// The source string carries the synthesized default scope. Recorded here as
	// what the pass actually emits for a component authored at the top-level
	// surface — every other diagnostic surface elides the generated scope name and
	// renders the shape the author typed, so this one is out of step with them.
	if prov.AnnotationSource != "aggregate.yaml:subsystems.default.components.api.annotations.feature.myco/db-pool" {
		t.Errorf("provenance source = %q, want canonical aggregate.yaml-rooted path", prov.AnnotationSource)
	}
}

func TestExpand_RecordsAggregateProvenance(t *testing.T) {
	t.Parallel()
	// A subsystem-level annotation propagating to a contained component
	// produces a provenance entry whose AnnotationSource reports the
	// subsystem authoring path (the cascade reports the deepest authoring
	// scope, not the consuming scope).
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "from-subsystem"
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	provKey := AnnotationProvenanceKey("subsystems.data.components.api", "", "feature.myco/db-pool")
	prov, ok := rr.AnnotationProvenance[provKey]
	if !ok {
		t.Fatalf("provenance entry missing at key %q; got keys: %v", provKey, keysOf(rr.AnnotationProvenance))
	}
	if prov.AnnotationKey != "feature.myco/db-pool" {
		t.Errorf("provenance key = %q, want feature.myco/db-pool", prov.AnnotationKey)
	}
	wantSource := "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool"
	if prov.AnnotationSource != wantSource {
		t.Errorf("provenance source = %q, want %q", prov.AnnotationSource, wantSource)
	}
}

func TestExpand_AggregateSide_DeeplyNestedSubsystemExpansion(t *testing.T) {
	t.Parallel()
	// Three-level nesting: an outer-level annotation propagates down
	// through middle into the inner-most contained component. The
	// canonical component path is rooted at "subsystems.<outer>." and
	// chained through ".subsystems.<middle>." down to the leaf.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    outer:
      annotations:
        feature.myco/db-pool: "outer-pool"
      subsystems:
        middle:
          subsystems:
            inner:
              components:
                leaf:
                  type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	outer, _ := rr.Aggregate.Spec.Subsystems.Get("outer")
	middle, _ := outer.Subsystems.Get("middle")
	inner, _ := middle.Subsystems.Get("inner")
	leaf, ok := inner.Components.Get("leaf")
	if !ok {
		t.Fatal("leaf component missing from inner subsystem after expansion")
	}
	feat, ok := leaf.Features.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("leaf should inherit annotation across the deeply nested cascade")
	}
	pool, _ := feat.Values.Get("pool")
	if pool != "outer-pool" {
		t.Errorf("leaf pool = %v, want outer-pool", pool)
	}

	provKey := AnnotationProvenanceKey("subsystems.outer.subsystems.middle.subsystems.inner.components.leaf", "", "feature.myco/db-pool")
	if _, ok := rr.AnnotationProvenance[provKey]; !ok {
		t.Errorf("provenance entry missing at key %q; got keys: %v", provKey, keysOf(rr.AnnotationProvenance))
	}
}

// TestExpand_CrossWalkerConsistency pins the cross-walker contract: the
// per-component effective annotation set computed by
// WireAggregateAnnotations matches the keys ExpandAnnotations
// materializes as synthetic FeatureInstance attachments on the same
// components, modulo bindings that are not registered. Both walkers
// share the cascade primitives (mergePointerStringMaps +
// flattenPointerStringMap), so per-component effective sets stay aligned;
// this test exists to catch divergence if either walker drifts.
func TestExpand_CrossWalkerConsistency(t *testing.T) {
	t.Parallel()
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    top-comp:
      type: core/server:1.0.0
      annotations:
        feature.myco/db-pool: "top-value"
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "shared-pool"
      components:
        api:
          type: core/server:1.0.0
        worker:
          type: core/server:1.0.0
          annotations:
            feature.myco/db-pool: null
      subsystems:
        inner:
          components:
            cron:
              type: core/server:1.0.0
`
	agg := loadTestAggregate(t, aggSrc)
	walked := WireAggregateAnnotations(normalized(t, agg))

	rr := &ResolveResult{
		Manifest:             loadValuesManifest(t),
		Aggregate:            agg,
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, CatalogLookup{}); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}

	postExpansion := map[string]types.ComponentInstance{}
	if top, ok := rr.Aggregate.Spec.Components.Get("top-comp"); ok {
		postExpansion["components.top-comp"] = top
	}
	data, _ := rr.Aggregate.Spec.Subsystems.Get("data")
	if api, ok := data.Components.Get("api"); ok {
		postExpansion["subsystems.data.components.api"] = api
	}
	if worker, ok := data.Components.Get("worker"); ok {
		postExpansion["subsystems.data.components.worker"] = worker
	}
	inner, _ := data.Subsystems.Get("inner")
	if cron, ok := inner.Components.Get("cron"); ok {
		postExpansion["subsystems.data.subsystems.inner.components.cron"] = cron
	}

	for path, effective := range walked {
		comp, ok := postExpansion[path]
		if !ok {
			t.Errorf("walker reported component path %q but expansion did not visit it", path)
			continue
		}
		for key := range effective.All() {
			if _, registered := loadValuesManifest(t).Spec.Mappings.Features.Annotations.Get(key); !registered {
				continue
			}
			if _, ok := comp.Features.Get(key); !ok {
				t.Errorf("path %q: walker reports key %q but expansion did not synthesize a Feature", path, key)
			}
		}
	}
}

// Structured-annotation expansion coverage. Structured annotation
// values route per-subkey through binding.Maps, with per-subkey
// side-vs-pillar enforcement, per-subkey Feature schema type
// validation, and collect-and-emit-all error wrapping. The tests
// below pin each of these contracts end-to-end through
// ExpandAnnotations.

const structuredManifestYAML = `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/db-pool: db-pool-helm:1.0.0
      annotations:
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - db_name
              - read_replicas
            infra:
              - pool_size
              - max_connections
`

func structuredManifest(t *testing.T) *types.Manifest {
	t.Helper()
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(structuredManifestYAML), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	return &m
}

func structuredFeatureCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	return mockCatalogLookup(map[string]types.Feature{
		"core/db-pool:1.0.0": featureWithSchema(t, "core/db-pool", `
values:
  db_name:
    type: string
  read_replicas:
    type: integer
infra:
  pool_size:
    type: integer
  max_connections:
    type: integer
`),
	})
}

func TestExpand_StructuredAggregateSide_RoutesAllValuesSubkeys(t *testing.T) {
	t.Parallel()
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool:
          db_name: "prod-db"
          read_replicas: 5
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, structuredFeatureCatalog(t)); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	data, _ := rr.Aggregate.Spec.Subsystems.Get("data")
	api, _ := data.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("api missing synthetic Feature attachment")
	}
	if dbName, _ := feat.Values.Get("db_name"); dbName != "prod-db" {
		t.Errorf("Values.db_name = %v, want prod-db", dbName)
	}
	if replicas, _ := feat.Values.Get("read_replicas"); replicas != 5 {
		t.Errorf("Values.read_replicas = %v, want 5", replicas)
	}
}

func TestExpand_StructuredReleaseSide_RoutesAllInfraSubkeys(t *testing.T) {
	t.Parallel()
	releaseSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
  environment: testing
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/db-pool:
          pool_size: 20
          max_connections: 100
      components:
        api:
          description: api comp
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Release:              loadTestRelease(t, releaseSrc),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, structuredFeatureCatalog(t)); err != nil {
		t.Fatalf("ExpandAnnotations: %v", err)
	}
	prod, _ := rr.Release.Spec.Boundaries.Get("prod")
	api, _ := prod.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("api missing synthetic BoundaryFeature attachment")
	}
	if poolSize, _ := feat.Infra.Get("pool_size"); poolSize != 20 {
		t.Errorf("Infra.pool_size = %v, want 20", poolSize)
	}
	if maxConn, _ := feat.Infra.Get("max_connections"); maxConn != 100 {
		t.Errorf("Infra.max_connections = %v, want 100", maxConn)
	}
}

func TestExpand_StructuredCrossSidePartition_AggregateOnlyValuesSubkeysFire(t *testing.T) {
	t.Parallel()
	// Aggregate-side annotation declares both values-pillar AND
	// infra-pillar subkeys. The infra-pillar subkeys fail
	// side-vs-pillar and accumulate to the collect-and-emit-all
	// error list; the build halts with the composed error so the IR
	// layer never sees the partial contribution.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool:
          db_name: "prod-db"
          pool_size: 20
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, structuredFeatureCatalog(t))
	if err == nil {
		t.Fatal("expected side-vs-pillar mismatch error for the infra-pillar subkey on the Aggregate side; got nil")
	}
	msg := cerrors.CauseChain(err)
	if !strings.Contains(msg, "pool_size") {
		t.Errorf("error should name the offending subkey (pool_size); got: %s", msg)
	}
	if !strings.Contains(msg, "aggregate") || !strings.Contains(msg, "infra") {
		t.Errorf("error should name the authoring side (aggregate) and the routed pillar (infra); got: %s", msg)
	}
}

func TestExpand_StructuredSubkeyNotInBindingMap_Errors(t *testing.T) {
	t.Parallel()
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool:
          unknown_subkey: "value"
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, structuredFeatureCatalog(t))
	if err == nil {
		t.Fatal("expected error for subkey not declared in binding map; got nil")
	}
	msg := cerrors.CauseChain(err)
	if !strings.Contains(msg, "unknown_subkey") {
		t.Errorf("error should name the unknown subkey; got: %s", msg)
	}
	if !strings.Contains(msg, "not declared in binding map") {
		t.Errorf("error should explain the binding-map declaration requirement; got: %s", msg)
	}
	// The category is named by the outer scope-path wrap and by nothing beneath it,
	// so a joined diagnostic says it once however many inner findings it carries.
	// An inner raise site that names it too renders the word twice on one line, and
	// the count is what catches that — a Contains check passes on both shapes.
	//
	// The wrap names the domain of the document the annotation value was authored
	// in, and this value is authored in the Aggregate, so the word counted is the
	// developer's. The Release-side sibling below counts the operator's over the
	// same condition, which is what shows the domain follows the document.
	if n := strings.Count(msg, string(cerrors.DeveloperError)); n != 1 {
		t.Errorf("the category is said %d times, want exactly 1\nfull: %s", n, msg)
	}
	if strings.Contains(msg, string(cerrors.OpsError)) {
		t.Errorf("an Aggregate-authored annotation names the operator's domain\nfull: %s", msg)
	}
}

// The Release side of the undeclared-subkey refusal, which routes through its own
// raise site and its own scope-path wrap.
//
// It is a separate test rather than a row on the Aggregate-side one because the two
// sides are separate implementations by design — the package documents them as
// deliberate mirrors that must be edited in lockstep — so a single test over one
// side proves nothing about the other, and the one-category invariant is exactly
// the kind of property that can hold on one mirror and not the other.
func TestExpand_StructuredSubkeyNotInBindingMap_ReleaseSide_Errors(t *testing.T) {
	t.Parallel()
	releaseSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
  environment: testing
spec:
  aggregate: test
  manifest: test
  boundaries:
    prod:
      annotations:
        feature.myco/db-pool:
          unknown_subkey: "value"
      components:
        api:
          description: api comp
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Release:              loadTestRelease(t, releaseSrc),
		ReleasePath:          "release.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, structuredFeatureCatalog(t))
	if err == nil {
		t.Fatal("expected error for subkey not declared in binding map; got nil")
	}
	msg := cerrors.CauseChain(err)
	if !strings.Contains(msg, "unknown_subkey") {
		t.Errorf("error should name the unknown subkey; got: %s", msg)
	}
	if !strings.Contains(msg, "not declared in binding map") {
		t.Errorf("error should explain the binding-map declaration requirement; got: %s", msg)
	}
	// The boundary and the component are the scope the inner finding cannot know, so
	// they have to survive the raise rather than be replaced by it.
	for _, want := range []string{"prod", "api"} {
		if !strings.Contains(msg, want) {
			t.Errorf("composed halt missing %q\nfull: %s", want, msg)
		}
	}
	if n := strings.Count(msg, string(cerrors.OpsError)); n != 1 {
		t.Errorf("the category is said %d times, want exactly 1\nfull: %s", n, msg)
	}
}

func TestExpand_StructuredSubkeyValueMismatchesFeatureSchemaType_Errors(t *testing.T) {
	t.Parallel()
	// db_name is declared as string; passing an integer fails type
	// validation. read_replicas is declared as integer; passing a
	// string fails too. Both errors accumulate per collect-and-emit-all.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool:
          db_name: 42
          read_replicas: "not-an-integer"
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, structuredFeatureCatalog(t))
	if err == nil {
		t.Fatal("expected type-mismatch errors; got nil")
	}
	msg := cerrors.CauseChain(err)
	if !strings.Contains(msg, "db_name") {
		t.Errorf("error should name db_name (declared type string, got integer); got: %s", msg)
	}
	if !strings.Contains(msg, "read_replicas") {
		t.Errorf("error should name read_replicas (declared type integer, got string); got: %s", msg)
	}
}

func TestExpand_StructuredCollectAllErrors_NotOnlyFirst(t *testing.T) {
	t.Parallel()
	// Three concurrent failures on one annotation: one unknown subkey,
	// one side-vs-pillar mismatch (infra subkey on Aggregate side),
	// one type mismatch. All three accumulate to the returned error
	// chain; the orchestrator's error printer renders each on its own
	// line via Unwrap() []error.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool:
          unknown: "x"
          pool_size: 20
          db_name: 42
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, structuredFeatureCatalog(t))
	if err == nil {
		t.Fatal("expected three collected errors; got nil")
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{"unknown", "pool_size", "db_name"} {
		if !strings.Contains(msg, want) {
			t.Errorf("collect-and-emit-all dropped %q; got: %s", want, msg)
		}
	}
}

func TestExpand_StructuredScalarValueRoutesCompatibly(t *testing.T) {
	t.Parallel()
	// Regression: after the AnnotationValue type lift, scalar values
	// continue to route through the scalar path correctly.
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
        core/db-pool: db-pool-helm:1.0.0
      annotations:
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - db_name
`
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "scalar-db"
      components:
        api:
          type: core/server:1.0.0
`
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(manifestSrc), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	rr := &ResolveResult{
		Manifest:             &m,
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	if err := ExpandAnnotations(rr, structuredFeatureCatalog(t)); err != nil {
		t.Fatalf("scalar regression failed: %v", err)
	}
	data, _ := rr.Aggregate.Spec.Subsystems.Get("data")
	api, _ := data.Components.Get("api")
	feat, ok := api.Features.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("api missing synthetic Feature attachment")
	}
	if dbName, _ := feat.Values.Get("db_name"); dbName != "scalar-db" {
		t.Errorf("Values.db_name = %v, want scalar-db", dbName)
	}
}

func TestExpand_StructuredErrorsImplementUnwrapSlice_OrchestratorRendersEachLine(t *testing.T) {
	t.Parallel()
	// Verifies the error chain returned by ExpandAnnotations
	// implements the Unwrap() []error interface so the orchestrator's
	// error printer (cmd/aggrega/error_format.go) renders each
	// constituent on its own line. The structural assertion via
	// errors.As pins the contract independently of surface
	// formatting — a future refactor that replaced errors.Join with
	// a string-concatenating wrapper would pass a substring check
	// but fail this assertion.
	aggSrc := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool:
          unknown_a: "x"
          unknown_b: "y"
      components:
        api:
          type: core/server:1.0.0
`
	rr := &ResolveResult{
		Manifest:             structuredManifest(t),
		Aggregate:            loadTestAggregate(t, aggSrc),
		AggregatePath:        "aggregate.yaml",
		AnnotationProvenance: map[string]AnnotationProvenance{},
	}
	err := ExpandAnnotations(rr, structuredFeatureCatalog(t))
	if err == nil {
		t.Fatal("expected collected errors; got nil")
	}

	// Structural assertion: the composed error must walk through to
	// the errors.Join'd inner chain and expose its multi-error
	// constituents via the Unwrap() []error interface. The
	// orchestrator's error printer relies on this exact contract.
	type unwrapMulti interface{ Unwrap() []error }
	var um unwrapMulti
	if !errors.As(err, &um) {
		t.Fatalf("composed error must implement Unwrap() []error somewhere in its chain; got: %T (%v)", err, err)
	}
	constituents := um.Unwrap()
	if len(constituents) < 2 {
		t.Errorf("expected >= 2 constituent errors (one per failed subkey); got %d: %v", len(constituents), constituents)
	}

	// Surface check (regression backstop): both subkeys should
	// appear in the rendered error text.
	msg := cerrors.CauseChain(err)
	if !strings.Contains(msg, "unknown_a") || !strings.Contains(msg, "unknown_b") {
		t.Errorf("composed error should carry both subkeys in rendered text; got: %s", msg)
	}
}
