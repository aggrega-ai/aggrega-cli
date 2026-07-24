// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"errors"
	"strings"
	"testing"
)

// TestEnvelope_ValidComponentPasses pins the green leg: a minimal
// schema-conforming Component passes the envelope with no findings.
func TestEnvelope_ValidComponentPasses(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer }
`)
	if _, err := ValidateBytes(KindComponent, doc, "api.yaml"); err != nil {
		t.Fatalf("valid component should pass the envelope: %v", err)
	}
}

// TestEnvelope_CompactInjectRefused is the permanent pin of the
// verbose-only inject contract: the language admits exactly one inject
// authoring form, the verbose [{in, from|value}] array. A document
// carrying the map shape inject: {env: {NAME: key}} fails the envelope
// loudly with a path-pointed diagnostic. This is a language-contract
// test, not a migration aid.
func TestEnvelope_CompactInjectRefused(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: compact-refusal
  version: "1.0.0"
spec:
  components:
    web:
      type: core/devtool/adminer
      uses:
        databases:
          db:
            target: db
            inject:
              env:
                ADMINER_DEFAULT_SERVER: host
    db:
      type: core/datastore/mariadb
`)
	_, err := ValidateBytes(KindAggregate, doc, "aggregate.yaml")
	if err == nil {
		t.Fatal("compact map-form inject must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
	if !strings.Contains(err.Error(), "aggregate.yaml") || !strings.Contains(err.Error(), "Aggregate") {
		t.Errorf("diagnostic must name the file and kind: %v", err)
	}
	// The verbose form of the same edge passes.
	good := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: compact-refusal
  version: "1.0.0"
spec:
  components:
    web:
      type: core/devtool/adminer
      uses:
        databases:
          db:
            target: db
            inject:
              env:
                - in: ADMINER_DEFAULT_SERVER
                  from: host
    db:
      type: core/datastore/mariadb
`)
	if _, err := ValidateBytes(KindAggregate, good, "aggregate.yaml"); err != nil {
		t.Fatalf("verbose-form inject must pass the envelope: %v", err)
	}
}

// TestEnvelope_ReleaseValuesBanLoud pins the first mechanical ban: a
// Release carrying values: on a boundary component fails the envelope
// loudly instead of being silently dropped. (The full violating-document
// corpus is a separate deliverable; this is the ban's own pin.)
func TestEnvelope_ReleaseValuesBanLoud(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: ban-values
  environment: dev
  version: "1.0.0"
spec:
  aggregate: app
  manifest: m
  boundaries:
    main:
      directory: ./dist
      components:
        api:
          values:
            port: 9999
`)
	_, err := ValidateBytes(KindRelease, doc, "release.yaml")
	if err == nil {
		t.Fatal("Release values: on a boundary component must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
	if !strings.Contains(err.Error(), "values") {
		t.Errorf("diagnostic must name the offending key: %v", err)
	}
}

// TestEnvelope_ReleaseAggregatePointerWithExtendsLoud pins the second
// mechanical ban: a Release using extends: while also declaring an
// aggregate: pointer fails the envelope loudly — the child must deploy
// the exact Developer Intent of its parent.
func TestEnvelope_ReleaseAggregatePointerWithExtendsLoud(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: ban-extends-aggregate
  environment: dev
  version: "1.0.0"
spec:
  extends: parent-release
  aggregate: other-app
  boundaries:
    main:
      directory: ./dist
      components:
        api: {}
`)
	_, err := ValidateBytes(KindRelease, doc, "release.yaml")
	if err == nil {
		t.Fatal("aggregate: alongside extends: must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
}

// TestEnvelope_AggregateComponentInfraRefused is the permanent pin of
// the pillar-ownership ban: infra is the Operator's pillar, authored in
// the Release — an Aggregate component carrying infra: fails the
// envelope loudly instead of being silently dropped.
func TestEnvelope_AggregateComponentInfraRefused(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: infra-refusal
  version: "1.0.0"
spec:
  components:
    api:
      type: core/compute/api-server
      infra:
        port: 8080
`)
	_, err := ValidateBytes(KindAggregate, doc, "aggregate.yaml")
	if err == nil {
		t.Fatal("Aggregate component infra: must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
	if !strings.Contains(err.Error(), "infra") {
		t.Errorf("diagnostic must name the offending key: %v", err)
	}
}

// TestEnvelope_BareStringUsesTargetRefused is the permanent pin of the
// single-form alias contract: a uses alias maps to an entry object
// ({target: ...}); the bare-string shorthand (alias: target-name) is
// not part of the language and fails the envelope loudly.
func TestEnvelope_BareStringUsesTargetRefused(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: bare-string-refusal
  version: "1.0.0"
spec:
  components:
    api:
      type: core/api
      uses:
        backend:
          db: postgres
    postgres:
      type: core/postgres
`)
	_, err := ValidateBytes(KindAggregate, doc, "aggregate.yaml")
	if err == nil {
		t.Fatal("bare-string uses target must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
}

// TestEnvelope_TemplateBearingValuesTolerated pins the template
// tolerance: unquoted Go-template expressions in values content parse
// as mapping-shaped YAML keys, which the product accepts (the typed
// path defers those nodes to parameter interpolation). The envelope
// must validate such documents rather than failing the generic decode.
func TestEnvelope_TemplateBearingValuesTolerated(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: template-bearing
  version: "1.0.0"
spec:
  parameters:
    cache_ttl_seconds:
      type: integer
  components:
    api:
      type: core/server
      values:
        cache_ttl_seconds: {{ .parameters.cache_ttl_seconds | default "3000" }}
        display_name: {{ .parameters.db.display_name }}
`)
	if _, err := ValidateBytes(KindAggregate, doc, "aggregate.yaml"); err != nil {
		t.Fatalf("template-bearing values must pass the envelope: %v", err)
	}
}

// TestEnvelope_StraySecretNamesRefused is the loader-side companion of
// the types-level TestSecretsSchema_UnknownSecretNamesKeyIgnoredAtParse:
// yaml.Unmarshal silently drops the retired secretNames key, and THIS
// gate is where the loud rejection lives — a document carrying it fails
// the envelope before typed unmarshal.
func TestEnvelope_StraySecretNamesRefused(t *testing.T) {
	t.Parallel()
	doc := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/stray
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  secretNames: [old_key]
`)
	_, err := ValidateBytes(KindComponent, doc, "stray.yaml")
	if err == nil {
		t.Fatal("stray secretNames must fail the envelope; got nil")
	}
	if !errors.Is(err, ErrEntityEnvelope) {
		t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
	}
	if !strings.Contains(err.Error(), "secretNames") {
		t.Errorf("diagnostic must name the offending key: %v", err)
	}
}

// frWorkloadImportDoc builds a minimal envelope-valid FeatureRecipe whose
// spec.workload is a single-key `$import:` map carrying imp.
func frWorkloadImportDoc(imp string) []byte {
	return []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: fr-workload-import
  version: "1.0.0"
spec:
  capability: helm
  implements: core/frwl:1.0.0
  workload:
    $import: "` + imp + `"
`)
}

// TestEnvelope_FeatureRecipeWorkloadLibraryImport pins the relaxed
// featurerecipe.workload `$import` envelope: a bare-name WorkloadLibrary
// reference and a local file path both pass; a host-bearing `oci://…`
// reference still fails loudly. A library is addressed by identity, so the
// registry host is rejected at the authoring surface — the version comes
// from the per-Release lockfile.
func TestEnvelope_FeatureRecipeWorkloadLibraryImport(t *testing.T) {
	t.Parallel()

	admit := []struct {
		name string
		imp  string
	}{
		{"bare-name library reference", "BaseWorkloads#/workloads/Deployment"},
		{"local file path", "./workloads/deployment.yaml"},
	}
	for _, tc := range admit {
		tc := tc
		t.Run("admit/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateBytes(KindFeatureRecipe, frWorkloadImportDoc(tc.imp), "fr.yaml"); err != nil {
				t.Fatalf("featurerecipe.workload $import %q should pass the envelope: %v", tc.imp, err)
			}
		})
	}

	t.Run("reject/oci host", func(t *testing.T) {
		t.Parallel()
		_, err := ValidateBytes(KindFeatureRecipe, frWorkloadImportDoc("oci://hub/BaseWorkloads#/workloads/Deployment"), "fr.yaml")
		if err == nil {
			t.Fatal("oci:// host at featurerecipe.workload $import must fail the envelope; got nil")
		}
		if !errors.Is(err, ErrEntityEnvelope) {
			t.Fatalf("want errors.Is(err, ErrEntityEnvelope); got %v", err)
		}
	})
}
