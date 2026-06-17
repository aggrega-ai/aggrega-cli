// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the post-flatten required-features enforcement pass:
// validateNodeRequiredFeatures + its wiring into BuildIR. The pass closes the
// coverage gap where a flattened sub-aggregate node's component requiredFeatures
// contract went unenforced (the Step-1 guard covers root and subsystem
// components only). Parity with the Step-3 attachment-conformance gate: it walks
// the same per-node merged Features/Secrets set.

package ir

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// requiredFeaturesCatalog returns a CatalogLookup whose Components declare
// requiredFeatures:
//
//	core/server requires core/monitoring (an operational Feature)
//	core/db     requires core/db-creds   (a credential Feature, kind:secret)
//	core/bare   requires nothing
//
// core/db-creds carries the feature.aggrega.ai/kind: secret label, so an
// attachment of that type routes into the node's Secrets bucket — exercising
// the Features ∪ Secrets satisfying-set union.
func requiredFeaturesCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	required := map[string][]string{
		"core/server": {"core/monitoring"},
		"core/db":     {"core/db-creds"},
		"core/bare":   nil,
	}
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			req, ok := required[typeURI]
			if !ok {
				return types.Component{}, false
			}
			return types.Component{
				Metadata: types.ComponentMetadata{Name: typeURI, Version: "1.0.0"},
				Spec:     types.ComponentSpec{RequiredFeatures: req},
			}, true
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/db-creds":
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/db-creds", Labels: labels}}, true
			case "core/monitoring":
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/monitoring"}}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

// buildWithRequiredFeatures runs BuildIR end-to-end for a single node carrying
// the given component type, source-aggregate provenance, and feature
// attachments, returning the build error (nil on success). A non-empty
// sourceAggregate marks the node as a flattened sub-aggregate node — the only
// scope the post-flatten pass enforces.
func buildWithRequiredFeatures(t *testing.T, nodeID, sourceAggregate, componentType string, feats []topology.FeatureAttachment) error {
	t.Helper()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set(nodeID, &topology.TopologyNode{
		InstanceID:      nodeID,
		ComponentType:   componentType,
		SourceAggregate: sourceAggregate,
	})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{nodeID: feats},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}
	_, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: requiredFeaturesCatalog(t),
	})
	return err
}

func requireMissingFeatureHalt(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an ErrMissingRequiredFeature halt naming %q; got nil", wantSubstr)
	}
	if !errors.Is(err, resolver.ErrMissingRequiredFeature) {
		t.Fatalf("error does not wrap ErrMissingRequiredFeature: %v", err)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("halt does not name %q; got: %v", wantSubstr, err)
	}
}

// A sub-aggregate node whose component requires a feature that is NOT attached
// halts, and the diagnostic is source-addressed to the flattened node id.
func TestSubAggregateRequiredFeature_Unsatisfied_Halts(t *testing.T) {
	err := buildWithRequiredFeatures(t, "subagg.api", "subagg", "core/server", nil)
	requireMissingFeatureHalt(t, err, "subagg.api")
	if !strings.Contains(err.Error(), "core/monitoring") {
		t.Fatalf("halt does not name the missing feature type; got: %v", err)
	}
}

// The same node passes once the required feature is attached — non-vacuous: the
// component flips on the sub-aggregate-level attachment alone.
func TestSubAggregateRequiredFeature_Satisfied_Passes(t *testing.T) {
	feats := []topology.FeatureAttachment{feat("mon", "core/monitoring", types.OrderedMap[any]{})}
	if err := buildWithRequiredFeatures(t, "subagg.api", "subagg", "core/server", feats); err != nil {
		t.Fatalf("expected pass with required feature attached; got: %v", err)
	}
}

// A required feature satisfied by a credential (kind:secret) attachment passes —
// the credential routes into the Secrets bucket, so the satisfying set must
// union Features and Secrets. Excluding Secrets would false-halt here.
func TestSubAggregateRequiredFeature_SatisfiedByCredential_Passes(t *testing.T) {
	feats := []topology.FeatureAttachment{feat("creds", "core/db-creds", types.OrderedMap[any]{})}
	if err := buildWithRequiredFeatures(t, "subagg.db", "subagg", "core/db", feats); err != nil {
		t.Fatalf("expected pass with credential-routed required feature; got: %v", err)
	}
}

// A top-level node (no SourceAggregate) missing a required feature does NOT halt
// in the IR pass — top-level enforcement is the Step-1 resolver guard's job, not
// this post-flatten pass. Confirms the pass is sub-aggregate-scoped (no double
// enforcement).
func TestTopLevelRequiredFeature_NotEnforcedInIRPass(t *testing.T) {
	if err := buildWithRequiredFeatures(t, "api", "", "core/server", nil); err != nil {
		t.Fatalf("post-flatten pass must not enforce required-features on top-level nodes; got: %v", err)
	}
}
