// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// slotGraph builds a one-node graph: component "db" (type compType) with the
// given slot declarations, and an attached fill "password" of type
// core/secret-value whose labels are kind=secret, provider=helm.
func slotGraph(compType string, slots types.OrderedMap[types.AttachmentSlot]) (*types.IRGraph, resolver.CatalogLookup) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: compType})
	secrets := types.OrderedMap[types.IRSecretEntry]{}
	secrets.Set("password", types.IRSecretEntry{Type: "core/secret-value"})
	nodeSecrets := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
	nodeSecrets.Set("db", secrets)
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, Secrets: nodeSecrets}}

	featLabels := types.OrderedMap[string]{}
	featLabels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
	featLabels.Set("provider", "helm")

	lookup := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == compType {
				return types.Component{Spec: types.ComponentSpec{SecretsSchema: slots}}, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/secret-value" {
				return types.Feature{Metadata: types.FeatureMetadata{Labels: featLabels}}, true
			}
			return types.Feature{}, false
		},
	}
	return graph, lookup
}

func slotWithSelector(sel string) types.OrderedMap[types.AttachmentSlot] {
	m := types.OrderedMap[types.AttachmentSlot]{}
	m.Set("password", types.AttachmentSlot{Selector: sel})
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

func TestValidateSlotSelectors_SatisfiedWithoutKindClause(t *testing.T) {
	t.Parallel()
	// A selector that narrows on an arbitrary label and is satisfied by the fill
	// is accepted. No clause pinning the fill to a particular Feature kind is
	// required for the selector to be admissible.
	graph, lookup := slotGraph("core/datastore/postgres", slotWithSelector("provider=helm"))
	errs, err := validateSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Errorf("a satisfied selector carrying no kind clause should produce no errors; got %+v", errs)
	}
}

func TestValidateSlotSelectors_Satisfied(t *testing.T) {
	t.Parallel()
	graph, lookup := slotGraph("core/datastore/postgres", slotWithSelector("feature.aggrega.ai/kind=secret,provider=helm"))
	errs, err := validateSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Errorf("satisfied selector should produce no errors; got %+v", errs)
	}
}

func TestValidateSlotSelectors_Unsatisfied(t *testing.T) {
	t.Parallel()
	// The selector requires a label the attached Feature lacks.
	graph, lookup := slotGraph("core/datastore/postgres", slotWithSelector("provider=vault"))
	errs, err := validateSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSentinel(errs, ErrSlotSelectorUnsatisfied) {
		t.Errorf("unsatisfied fill should halt with ErrSlotSelectorUnsatisfied; got %+v", errs)
	}
}

func TestValidateSlotSelectors_NoSelectorClean(t *testing.T) {
	t.Parallel()
	// A slot declaring no selector places no constraint on its fill.
	graph, lookup := slotGraph("core/datastore/postgres", slotWithSelector(""))
	errs, err := validateSlotSelectors(context.Background(), graph, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Errorf("absent selector should produce no errors; got %+v", errs)
	}
}
