// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
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
	warnings := LintAnnotationBindingMaps(manifest)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings; got %d: %v", len(warnings), warnings)
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
	warnings := LintAnnotationBindingMaps(manifest)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning; got %d: %v", len(warnings), warnings)
	}
	for _, want := range []string{
		"feature.myco/db-pool",
		"core/db-pool:1.0.0",
		"shared_subkey",
		"Maps.Infra",
		"Maps.Values",
	} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning missing %q: %s", want, warnings[0])
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
	warnings := LintAnnotationBindingMaps(manifest)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings; got %d: %v", len(warnings), warnings)
	}
	wantOrder := []string{"alpha", "beta", "gamma"}
	for i, p := range wantOrder {
		if !strings.Contains(warnings[i], "\""+p+"\"") {
			t.Errorf("warning %d should mention path %q; got: %s", i, p, warnings[i])
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
	warnings := LintAnnotationBindingMaps(manifest)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings; got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "feature.myco/b-key") {
		t.Errorf("warning 0 should name b-key (author-first); got: %s", warnings[0])
	}
	if !strings.Contains(warnings[1], "feature.myco/a-key") {
		t.Errorf("warning 1 should name a-key (author-second); got: %s", warnings[1])
	}
}

func TestLintAnnotationBindingMaps_NilManifest_NoPanic(t *testing.T) {
	t.Parallel()
	warnings := LintAnnotationBindingMaps(nil)
	if warnings != nil {
		t.Errorf("nil manifest: expected nil warnings; got %v", warnings)
	}
}

func TestLintAnnotationBindingMaps_LegacyTierOverlap_Surfaces(t *testing.T) {
	t.Parallel()
	// Legacy-provider tier (per-provider sub-section under mappings).
	// The lint walks this tier parallel to how the existing
	// manifest-load validators cover it during the migration window.
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
	warnings := LintAnnotationBindingMaps(manifest)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning from legacy tier; got %d: %v", len(warnings), warnings)
	}
	for _, want := range []string{
		"feature.myco/legacy-pool",
		"core/legacy-pool:1.0.0",
		"duplicated",
	} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("legacy-tier warning missing %q: %s", want, warnings[0])
		}
	}
}
