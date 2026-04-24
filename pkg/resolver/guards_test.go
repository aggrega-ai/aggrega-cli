// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func testCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server:1.0.0":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "server",
						Version: "1.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
					},
					Spec: types.ComponentSpec{
						RequiredFeatures: []string{"ops/logging:1.0.0"},
					},
				}, true
			case "core/worker:1.0.0":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "worker",
						Version: "1.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "worker"); return _m }(),
					},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "ops/logging:1.0.0":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "logging", Version: "1.0.0"},
				}, true
			case "security/vault:1.0.0":
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:    "vault",
						Version: "1.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.FeatureKindLabel, types.FeatureKindSecret); return _m }(),
					},
				}, true
			case "ops/metrics:1.0.0":
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
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type: "core/server:1.0.0",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("logging", types.FeatureInstance{Type: "ops/logging:1.0.0"}); return _m }(),
					Secrets: func() types.OrderedMap[types.SecretInstance] { _m := types.OrderedMap[types.SecretInstance]{}; _m.Set("vault", types.SecretInstance{Type: "security/vault:1.0.0"}); return _m }(),
				}); return _m }(),
		},
	}

	errs := guardCredentialPartition(agg, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors for correct placement, got %d: %v", len(errs), errs[0].Error())
	}
}

func TestGuardCredentialPartition_CredentialInFeatures(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type: "core/server:1.0.0",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("vault", types.FeatureInstance{Type: "security/vault:1.0.0"}); return _m }(),
				}); return _m }(),
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
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type: "core/server:1.0.0",
					Secrets: func() types.OrderedMap[types.SecretInstance] { _m := types.OrderedMap[types.SecretInstance]{}; _m.Set("logging", types.SecretInstance{Type: "ops/logging:1.0.0"}); return _m }(),
				}); return _m }(),
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
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type: "core/server:1.0.0",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("logging", types.FeatureInstance{Type: "ops/logging:1.0.0"}); return _m }(),
				}); return _m }(),
		},
	}

	errs := guardRequiredFeatures(agg, nil, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors when required features satisfied, got: %v", errs[0].Error())
	}
}

func TestGuardRequiredFeatures_Missing(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type:     "core/server:1.0.0",
					Features: types.OrderedMap[types.FeatureInstance]{}, 
				}); return _m }(),
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
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type:     "core/server:1.0.0",
					Features: types.OrderedMap[types.FeatureInstance]{},
				}); return _m }(),
		},
	}
	release := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: func() types.OrderedMap[types.Boundary] { _m := types.OrderedMap[types.Boundary]{}; _m.Set("prod", types.Boundary{
					Components: func() types.OrderedMap[types.BoundaryComponent] { _m := types.OrderedMap[types.BoundaryComponent]{}; _m.Set("web", types.BoundaryComponent{
							Features: func() types.OrderedMap[types.BoundaryFeature] { _m := types.OrderedMap[types.BoundaryFeature]{}; _m.Set("logging", types.BoundaryFeature{Type: "ops/logging:1.0.0"}); return _m }(),
						}); return _m }(),
				}); return _m }(),
		},
	}

	errs := guardRequiredFeatures(agg, release, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors when Release satisfies required feature, got: %v", errs[0].Error())
	}
}

func TestGuardRequiredFeatures_NoRequirements(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("bg", types.ComponentInstance{Type: "core/worker:1.0.0"}); return _m }(),
		},
	}

	errs := guardRequiredFeatures(agg, nil, testCatalog())
	if len(errs) > 0 {
		t.Fatalf("expected no errors for component without requiredFeatures, got: %v", errs[0].Error())
	}
}

// --- Feature merge tests ---

func TestGuardFeatureMerge_MatchingTypes(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type: "core/server:1.0.0",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("metrics", types.FeatureInstance{Type: "ops/metrics:1.0.0"}); return _m }(),
				}); return _m }(),
		},
	}
	release := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: func() types.OrderedMap[types.Boundary] { _m := types.OrderedMap[types.Boundary]{}; _m.Set("prod", types.Boundary{
					Components: func() types.OrderedMap[types.BoundaryComponent] { _m := types.OrderedMap[types.BoundaryComponent]{}; _m.Set("web", types.BoundaryComponent{
							Features: func() types.OrderedMap[types.BoundaryFeature] { _m := types.OrderedMap[types.BoundaryFeature]{}; _m.Set("metrics", types.BoundaryFeature{Type: "ops/metrics:1.0.0", Infra: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("cpu", "500m"); return _m }()}); return _m }(),
						}); return _m }(),
				}); return _m }(),
		},
	}

	errs := guardFeatureMerge(agg, release)
	if len(errs) > 0 {
		t.Fatalf("expected no errors for matching types, got: %v", errs[0].Error())
	}
}

func TestGuardFeatureMerge_TypeMismatch(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type: "core/server:1.0.0",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("metrics", types.FeatureInstance{Type: "ops/metrics:1.0.0"}); return _m }(),
				}); return _m }(),
		},
	}
	release := &types.Release{
		Spec: types.ReleaseSpec{
			Boundaries: func() types.OrderedMap[types.Boundary] { _m := types.OrderedMap[types.Boundary]{}; _m.Set("prod", types.Boundary{
					Components: func() types.OrderedMap[types.BoundaryComponent] { _m := types.OrderedMap[types.BoundaryComponent]{}; _m.Set("web", types.BoundaryComponent{
							Features: func() types.OrderedMap[types.BoundaryFeature] { _m := types.OrderedMap[types.BoundaryFeature]{}; _m.Set("metrics", types.BoundaryFeature{Type: "ops/logging:1.0.0"}); return _m }(),
						}); return _m }(),
				}); return _m }(),
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
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("web", types.ComponentInstance{
					Type: "core/server:1.0.0",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("vault", types.FeatureInstance{Type: "security/vault:1.0.0"}); return _m }(),
					
				}); return _m }(),
		},
	}

	errs := RunGuards(agg, nil, testCatalog(), nil)
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
	errs := RunGuards(nil, nil, CatalogLookup{}, nil)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for nil inputs, got %d", len(errs))
	}
}
