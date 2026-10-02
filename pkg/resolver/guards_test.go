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

// --- Guard ordering test ---

func TestRunGuards_MultipleViolationsCollected(t *testing.T) {
	// RunGuards collects every violation class; no guard short-circuits the
	// others. The graph below violates two independent guards at once: the
	// core/logging attachment names a socket that Feature does not declare (so
	// the socket is unknown on a surface that requires a declared one) and
	// core/server declares no spec.inject allowlist (so the component-level
	// inject block is forbidden). Both must appear in one result.
	//
	// The unknown socket sits on the attachment rather than on the component's
	// own uses block, because a component's own uses may name a socket the
	// catalog did not declare and so raises nothing.
	uses := types.OrderedMap[types.FeatureUsesSocketValue]{}
	uses.Set("databases", types.FeatureUsesSocketValue{Compact: []string{"db"}})
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("log", types.FeatureInstance{Type: "core/logging:1.0.0", Uses: uses})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{
		Type:     "core/server:1.0.0",
		Features: features,
		Inject:   &types.InjectBlock{Env: map[string]any{"PORT": "8080"}},
	})
	components.Set("db", types.ComponentInstance{Type: "core/worker:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs, err := RunGuards(nil, normalized(t, agg), nil, testCatalog(), nil, nil)
	if err != nil {
		t.Fatalf("the suite returned an error: %v", err)
	}
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 violations (unknown socket + self-inject), got %d: %v", len(errs), errs)
	}

	var hasUnknownSocket, hasSelfInject bool
	for _, e := range errs {
		if errors.Is(e, ErrUnknownUsesSocket()) {
			hasUnknownSocket = true
		}
		if errors.Is(e, ErrSelfInjectForbidden()) {
			hasSelfInject = true
		}
	}
	if !hasUnknownSocket {
		t.Error("expected ErrUnknownUsesSocket in results")
	}
	if !hasSelfInject {
		t.Error("expected ErrSelfInjectForbidden in results")
	}
}

func TestRunGuards_NilInputs(t *testing.T) {
	errs, err := RunGuards(nil, normalized(t, nil), nil, CatalogLookup{}, nil, nil)
	if err != nil {
		t.Fatalf("the suite returned an error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors for nil inputs, got %d", len(errs))
	}
}
