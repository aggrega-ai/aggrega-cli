// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestSelectorPinsToSecretKind(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"feature.aggrega.ai/kind=secret":               true,  // equality
		"feature.aggrega.ai/kind in (secret)":          true,  // in {secret}
		"feature.aggrega.ai/kind=secret,provider=helm": true,  // pinned + extra narrowing
		"feature.aggrega.ai/kind in (secret, config)":  false, // admits non-secret
		"feature.aggrega.ai/kind!=secret":              false, // negation
		"feature.aggrega.ai/kind":                      false, // bare exists
		"!feature.aggrega.ai/kind":                     false, // doesnotexist
		"provider=helm":                                false, // no kind clause
		"feature.aggrega.ai/kind notin (config)":       false, // notin does not pin to secret
	}
	for sel, want := range cases {
		reqs, err := selector.Parse(sel)
		if err != nil {
			t.Fatalf("parse %q: %v", sel, err)
		}
		if got := selectorPinsToSecretKind(reqs); got != want {
			t.Errorf("selectorPinsToSecretKind(%q) = %v, want %v", sel, got, want)
		}
	}
}

// fixture builds a one-node graph: component "db" (type compType) with the given
// secretsSchema, and an attached secret "password" of type featType.
func secretSlotGraph(compType string, slots types.OrderedMap[types.SecretSlot]) (*types.IRGraph, resolver.CatalogLookup) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: compType})
	secrets := types.OrderedMap[types.IRSecretEntry]{}
	secrets.Set("password", types.IRSecretEntry{Type: "core/secret-value"})
	nodeSecrets := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
	nodeSecrets.Set("db", secrets)
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, Secrets: nodeSecrets}}

	secretFeatLabels := types.OrderedMap[string]{}
	secretFeatLabels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
	secretFeatLabels.Set("provider", "helm")

	lookup := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == compType {
				return types.Component{Spec: types.ComponentSpec{SecretsSchema: slots}}, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/secret-value" {
				return types.Feature{Metadata: types.FeatureMetadata{Labels: secretFeatLabels}}, true
			}
			return types.Feature{}, false
		},
	}
	return graph, lookup
}

func slotWithSelector(sel string) types.OrderedMap[types.SecretSlot] {
	m := types.OrderedMap[types.SecretSlot]{}
	m.Set("password", types.SecretSlot{Selector: sel})
	return m
}

func hasSentinel(errs []AnalysisError, sentinel error) bool {
	for _, e := range errs {
		if errors.Is(e.Sentinel, sentinel) {
			return true
		}
	}
	return false
}

func TestValidateSecretSlotSelectors_KindConstraint(t *testing.T) {
	t.Parallel()
	// A selector that does not pin to secret-kind halts at the kind check.
	graph, lookup := secretSlotGraph("core/datastore/postgres", slotWithSelector("provider=helm"))
	errs, err := validateSecretSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSentinel(errs, ErrSecretSlotSelectorNotSecretKind) {
		t.Errorf("non-secret-kind selector should halt with ErrSecretSlotSelectorNotSecretKind; got %+v", errs)
	}
}

func TestValidateSecretSlotSelectors_PinnedAndSatisfied(t *testing.T) {
	t.Parallel()
	// Pinned to secret-kind AND the attached Feature (kind=secret, provider=helm) satisfies it.
	graph, lookup := secretSlotGraph("core/datastore/postgres", slotWithSelector("feature.aggrega.ai/kind=secret,provider=helm"))
	errs, err := validateSecretSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Errorf("pinned + satisfied selector should produce no errors; got %+v", errs)
	}
}

func TestValidateSecretSlotSelectors_Unsatisfied(t *testing.T) {
	t.Parallel()
	// Pinned to secret-kind but requires a label the attached Feature lacks.
	graph, lookup := secretSlotGraph("core/datastore/postgres", slotWithSelector("feature.aggrega.ai/kind=secret,provider=vault"))
	errs, err := validateSecretSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSentinel(errs, ErrSecretSlotSelectorUnsatisfied) {
		t.Errorf("unsatisfied fill should halt with ErrSecretSlotSelectorUnsatisfied; got %+v", errs)
	}
}

func TestValidateSecretSlotSelectors_NoSelectorClean(t *testing.T) {
	t.Parallel()
	// The common existing case: a slot with no selector is valid (partition holds).
	graph, lookup := secretSlotGraph("core/datastore/postgres", slotWithSelector(""))
	errs, err := validateSecretSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Errorf("absent selector should produce no errors; got %+v", errs)
	}
}
