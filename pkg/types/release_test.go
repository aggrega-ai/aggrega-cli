// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// TestBoundary_AnnotationsTristateRoundTrip pins the YAML decode shape
// of Boundary.Annotations. The pointer-tristate (OrderedMap[*string])
// distinguishes three cases:
//
//   - key with a non-nil string value (explicit annotation),
//   - key with explicit YAML null (opt-out signal),
//   - key absent entirely (inherit from parent cascade).
//
// yaml.v3 + OrderedMap's reflection-decode handles all three natively.
// This test catches future regressions where pointer-typed decoding
// drops the distinction.
func TestBoundary_AnnotationsTristateRoundTrip(t *testing.T) {
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
        has-value: "v"
        opted-out: null
`
	var rel types.Release
	if err := yaml.Unmarshal([]byte(src), &rel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	prod, _ := rel.Spec.Boundaries.Get("prod")

	keys := prod.Annotations.Keys()
	wantKeys := []string{"has-value", "opted-out"}
	if !slices.Equal(keys, wantKeys) {
		t.Errorf("Annotations.Keys() = %v, want %v (two entries in author order; the absent third key produces no entry)", keys, wantKeys)
	}

	hasValue, ok := prod.Annotations.Get("has-value")
	if !ok {
		t.Fatal("has-value entry missing")
	}
	if hasValue == nil {
		t.Errorf("has-value should be a non-nil pointer (explicit string)")
	} else if !hasValue.IsScalar() || *hasValue.Scalar != "v" {
		t.Errorf("has-value Scalar = %v, want v", hasValue.Scalar)
	}

	optedOut, ok := prod.Annotations.Get("opted-out")
	if !ok {
		t.Fatal("opted-out entry missing — YAML null should decode to a present entry with nil pointer, not absence")
	}
	if optedOut != nil {
		t.Errorf("opted-out should be a nil pointer (the opt-out signal); got %v", optedOut)
	}

	if _, ok := prod.Annotations.Get("never-mentioned"); ok {
		t.Errorf("absent key should produce no entry; key was 'never-mentioned'")
	}
}

// TestBoundaryFeature_TypedDecodeWithUses pins the typed decode of a
// boundary-wide feature attachment. Boundary.Features is
// OrderedMap[BoundaryFeature]; the type and infra fields decode exactly
// as the per-component boundary feature shape always has, and the uses
// field decodes via UsesSocketValue's own unmarshaler — compact form
// (a socket → list of targets) and verbose form (a socket → alias →
// {target, inject}) both round-trip without a bespoke boundary parser.
func TestBoundaryFeature_TypedDecodeWithUses(t *testing.T) {
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
      features:
        telemetry:
          type: feature.myco/tier-telemetry
          infra:
            tier: prod-tier
          uses:
            collector:
              - telemetry-collector
`
	var rel types.Release
	if err := yaml.Unmarshal([]byte(src), &rel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	prod, _ := rel.Spec.Boundaries.Get("prod")
	feat, ok := prod.Features.Get("telemetry")
	if !ok {
		t.Fatal("boundary-wide feature 'telemetry' missing")
	}
	if feat.Type != "feature.myco/tier-telemetry" {
		t.Errorf("Type = %q; want feature.myco/tier-telemetry", feat.Type)
	}
	if v, ok := feat.Infra.Get("tier"); !ok || v != "prod-tier" {
		t.Errorf("Infra.tier = (%v, %v); want (prod-tier, true)", v, ok)
	}
	usesEntry, ok := feat.Uses.Get("collector")
	if !ok {
		t.Fatal("uses socket 'collector' missing — UsesSocketValue must decode under a boundary feature")
	}
	if got := usesEntry.Compact; len(got) != 1 || got[0] != "telemetry-collector" {
		t.Errorf("uses.collector compact targets = %v; want [telemetry-collector]", got)
	}
}

// TestBoundary_EmptyAnnotationsOmitempty pins the omitempty/omitzero
// behaviour on OrderedMap[*string]. An empty Annotations map drops
// from JSON marshal; a populated map serializes with the pointer
// values intact (nil → null, non-nil → quoted string).
func TestBoundary_EmptyAnnotationsOmitempty(t *testing.T) {
	t.Parallel()

	empty := types.Boundary{}
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(data), `"annotations"`) {
		t.Errorf("empty Annotations should be omitted; got: %s", data)
	}

	populated := types.Boundary{}
	v := "val"
	populated.Annotations.Set("present", &types.AnnotationValue{Scalar: &v})
	populated.Annotations.Set("opt-out", nil)
	data2, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	if !strings.Contains(string(data2), `"present"`) || !strings.Contains(string(data2), `"val"`) {
		t.Errorf("non-nil scalar AnnotationValue should serialize with its scalar string; got: %s", data2)
	}
	if !strings.Contains(string(data2), `"opt-out":null`) {
		t.Errorf("nil pointer should serialize as JSON null; got: %s", data2)
	}
}
