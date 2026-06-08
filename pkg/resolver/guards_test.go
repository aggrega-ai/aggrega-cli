// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func testCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server:1.0.0":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "server",
						Version: "1.0.0",
						Labels:  labels,
					},
					Spec: types.ComponentSpec{
						RequiredFeatures: []string{"core/logging:1.0.0"},
					},
				}, true
			case "core/worker:1.0.0":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "worker")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "worker",
						Version: "1.0.0",
						Labels:  labels2,
					},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/logging:1.0.0":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "logging", Version: "1.0.0"},
				}, true
			case "security/vault:1.0.0":
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:    "vault",
						Version: "1.0.0",
						Labels:  labels,
					},
				}, true
			case "core/metrics:1.0.0":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "metrics", Version: "1.0.0"},
				}, true
			}
			return types.Feature{}, false
		},
	}
}

// --- Credential partition tests ---

func TestGuardCredentialPartition_CorrectPlacement(t *testing.T) {
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("logging", types.FeatureInstance{Type: "core/logging:1.0.0"})
	secrets := types.OrderedMap[types.SecretInstance]{}
	secrets.Set("vault", types.SecretInstance{Type: "security/vault:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: features, Secrets: secrets})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardCredentialPartition(agg, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors for correct placement, got %d: %v", len(errs), errs[0].Error())
	}
}

func TestGuardCredentialPartition_CredentialInFeatures(t *testing.T) {
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("vault", types.FeatureInstance{Type: "security/vault:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardCredentialPartition(agg, testCatalog())
	if len(errs) == 0 {
		t.Fatal("expected error for credential Feature in features: block")
	}
	if !errors.Is(errs[0], ErrCredentialLabelPartition) {
		t.Errorf("expected ErrCredentialLabelPartition, got %v", errs[0].Err)
	}
}

func TestGuardCredentialPartition_NonCredentialInSecrets(t *testing.T) {
	secrets := types.OrderedMap[types.SecretInstance]{}
	secrets.Set("logging", types.SecretInstance{Type: "core/logging:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Secrets: secrets})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardCredentialPartition(agg, testCatalog())
	if len(errs) == 0 {
		t.Fatal("expected error for non-credential Feature in secrets: block")
	}
	if !errors.Is(errs[0], ErrCredentialLabelPartition) {
		t.Errorf("expected ErrCredentialLabelPartition, got %v", errs[0].Err)
	}
}

// --- RequiredFeatures tests ---

func TestGuardRequiredFeatures_AllSatisfied(t *testing.T) {
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("logging", types.FeatureInstance{Type: "core/logging:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardRequiredFeatures(agg, nil, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors when required features satisfied, got: %v", errs[0].Error())
	}
}

func TestGuardRequiredFeatures_Missing(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: types.OrderedMap[types.FeatureInstance]{}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardRequiredFeatures(agg, nil, testCatalog())
	if len(errs) == 0 {
		t.Fatal("expected error for missing required feature")
	}
	if !errors.Is(errs[0], ErrMissingRequiredFeature) {
		t.Errorf("expected ErrMissingRequiredFeature, got %v", errs[0].Err)
	}
}

func TestGuardRequiredFeatures_SatisfiedByRelease(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: types.OrderedMap[types.FeatureInstance]{}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	features := types.OrderedMap[types.BoundaryFeature]{}
	features.Set("logging", types.BoundaryFeature{Type: "core/logging:1.0.0"})
	components2 := types.OrderedMap[types.BoundaryComponent]{}
	components2.Set("web", types.BoundaryComponent{Features: features})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: components2})
	release := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: boundaries,
		},
	}

	errs := guardRequiredFeatures(agg, release, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors when Release satisfies required feature, got: %v", errs[0].Error())
	}
}

func TestGuardRequiredFeatures_NoRequirements(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("bg", types.ComponentInstance{Type: "core/worker:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardRequiredFeatures(agg, nil, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors for component without requiredFeatures, got: %v", errs[0].Error())
	}
}

// --- Feature merge tests ---

func TestGuardFeatureMerge_MatchingTypes(t *testing.T) {
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("metrics", types.FeatureInstance{Type: "core/metrics:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	infra := types.OrderedMap[any]{}
	infra.Set("cpu", "500m")
	features2 := types.OrderedMap[types.BoundaryFeature]{}
	features2.Set("metrics", types.BoundaryFeature{Type: "core/metrics:1.0.0", Infra: infra})
	components2 := types.OrderedMap[types.BoundaryComponent]{}
	components2.Set("web", types.BoundaryComponent{Features: features2})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: components2})
	release := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: boundaries,
		},
	}

	errs := guardFeatureMerge(agg, release)
	if len(errs) > 0 {
		t.Fatalf("expected no errors for matching types, got: %v", errs[0].Error())
	}
}

func TestGuardFeatureMerge_TypeMismatch(t *testing.T) {
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("metrics", types.FeatureInstance{Type: "core/metrics:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	features2 := types.OrderedMap[types.BoundaryFeature]{}
	features2.Set("metrics", types.BoundaryFeature{Type: "core/logging:1.0.0"})
	components2 := types.OrderedMap[types.BoundaryComponent]{}
	components2.Set("web", types.BoundaryComponent{Features: features2})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: components2})
	release := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: boundaries,
		},
	}

	errs := guardFeatureMerge(agg, release)
	if len(errs) == 0 {
		t.Fatal("expected error for type mismatch")
	}
	if !errors.Is(errs[0], ErrFeatureMergeConflict) {
		t.Errorf("expected ErrFeatureMergeConflict, got %v", errs[0].Err)
	}
}

// --- Guard ordering test ---

func TestRunGuards_MultipleViolationsCollected(t *testing.T) {
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("vault", types.FeatureInstance{Type: "security/vault:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := RunGuards(agg, nil, testCatalog(), nil, nil)
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 violations (credential + required), got %d", len(errs))
	}

	// Verify both error types are present
	var hasCredential, hasRequired bool
	for _, e := range errs {
		if errors.Is(e, ErrCredentialLabelPartition) {
			hasCredential = true
		}
		if errors.Is(e, ErrMissingRequiredFeature) {
			hasRequired = true
		}
	}
	if !hasCredential {
		t.Error("expected ErrCredentialLabelPartition in results")
	}
	if !hasRequired {
		t.Error("expected ErrMissingRequiredFeature in results")
	}
}

func TestRunGuards_NilInputs(t *testing.T) {
	errs := RunGuards(nil, nil, CatalogLookup{}, nil, nil)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for nil inputs, got %d", len(errs))
	}
}
