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

// manifestFromYAML unmarshals an inline Manifest YAML body for lint
// tests. Mirrors the shape of loadManifest in
// annotation_binding_target_validation_test.go but inlined here to
// keep the lint tests free of cross-file fixture dependencies.
func manifestFromYAML(t *testing.T, src string) *types.Manifest {
	t.Helper()
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	return &m
}

func TestLintAnnotationBindingMaps_NoOverlap_NoWarning(t *testing.T) {
	t.Parallel()
	manifest := manifestFromYAML(t, `
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
            infra:
              - pool_size
            values:
              - db_name
`)
	findings := LintAnnotationBindingMaps(manifest)
	if len(findings) != 0 {
		t.Errorf("expected no findings; got %d: %v", len(findings), findings)
	}
}

func TestLintAnnotationBindingMaps_SinglePathOverlap_OneWarning(t *testing.T) {
	t.Parallel()
	manifest := manifestFromYAML(t, `
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
            infra:
              - pool_size
              - shared_subkey
            values:
              - db_name
              - shared_subkey
`)
	findings := LintAnnotationBindingMaps(manifest)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding; got %d: %v", len(findings), findings)
	}
	if !stderrors.Is(findings[0], ErrAnnotationBindingPillarOverlap()) {
		t.Errorf("finding should carry the pillar-overlap advisory identity; got %v", findings[0])
	}
	rendered := findings[0].Error()
	for _, want := range []string{
		"feature.myco/db-pool",
		"core/db-pool:1.0.0",
		"shared_subkey",
		"maps.infra",
		"maps.values",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("finding missing %q: %s", want, rendered)
		}
	}
}

func TestLintAnnotationBindingMaps_MultiplePathOverlap_StableOrder(t *testing.T) {
	t.Parallel()
	// Three duplicates declared in author-arbitrary order; warnings
	// should emit in sort.Strings order: "alpha", "beta", "gamma".
	manifest := manifestFromYAML(t, `
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
            infra:
              - gamma
              - alpha
              - beta
            values:
              - alpha
              - beta
              - gamma
`)
	findings := LintAnnotationBindingMaps(manifest)
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings; got %d: %v", len(findings), findings)
	}
	wantOrder := []string{"alpha", "beta", "gamma"}
	for i, p := range wantOrder {
		if !strings.Contains(findings[i].Error(), "\""+p+"\"") {
			t.Errorf("finding %d should mention path %q; got: %s", i, p, findings[i])
		}
	}
}

func TestLintAnnotationBindingMaps_MultipleBindings_AuthorOrder(t *testing.T) {
	t.Parallel()
	// Two bindings each with one duplicate. Warnings must emit in
	// author-declared binding order (b-key first, then a-key —
	// OrderedMap preserves author order, not lexical order).
	manifest := manifestFromYAML(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/b-key:
          feature: core/b-feature:1.0.0
          maps:
            infra:
              - shared
            values:
              - shared
        feature.myco/a-key:
          feature: core/a-feature:1.0.0
          maps:
            infra:
              - shared
            values:
              - shared
`)
	findings := LintAnnotationBindingMaps(manifest)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings; got %d: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Error(), "feature.myco/b-key") {
		t.Errorf("finding 0 should name b-key (author-first); got: %s", findings[0])
	}
	if !strings.Contains(findings[1].Error(), "feature.myco/a-key") {
		t.Errorf("finding 1 should name a-key (author-second); got: %s", findings[1])
	}
}

func TestLintAnnotationBindingMaps_NilManifest_NoPanic(t *testing.T) {
	t.Parallel()
	findings := LintAnnotationBindingMaps(nil)
	if findings != nil {
		t.Errorf("nil manifest: expected nil findings; got %v", findings)
	}
}

func TestLintAnnotationBindingMaps_LegacyTierOverlap_Surfaces(t *testing.T) {
	t.Parallel()
	// Legacy-provider tier (per-provider sub-section under mappings).
	// The lint walks this tier alongside the flat one, so a binding
	// authored under either is covered.
	manifest := manifestFromYAML(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    helm:
      features:
        annotations:
          feature.myco/legacy-pool:
            feature: core/legacy-pool:1.0.0
            maps:
              infra:
                - duplicated
              values:
                - duplicated
`)
	findings := LintAnnotationBindingMaps(manifest)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding from legacy tier; got %d: %v", len(findings), findings)
	}
	// The locator names the tier the binding was authored under, so a
	// legacy-tier finding is placed on that tier's own annotations block
	// rather than on the flat one.
	if got, want := findings[0].Field, ".spec.mappings.helm.features.annotations.feature.myco/legacy-pool"; got != want {
		t.Errorf("legacy-tier locator = %q, want %q", got, want)
	}
	for _, want := range []string{
		"feature.myco/legacy-pool",
		"core/legacy-pool:1.0.0",
		"duplicated",
	} {
		if !strings.Contains(findings[0].Error(), want) {
			t.Errorf("legacy-tier finding missing %q: %s", want, findings[0])
		}
	}
}
