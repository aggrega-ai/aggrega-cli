// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Unit tests for collectNodeFeatures and the helper functions in
// features.go — focused per-behaviour coverage that complements the
// integration-shaped tests in builder_test.go. featureCatalog provides a
// minimal CatalogLookup with one operational feature ("core/monitoring")
// and one credential feature ("core/db-creds", labelled with the
// `feature.aggrega.ai/kind: secret` label).
//
// Test cases cover the three mergeFeature behaviour rules plus the
// determinism discipline at the markers slice:
//
//   - TestCollectNodeFeatures_SimpleComponent — happy path: one
//     operational feature lands in Features with values intact and the
//     instance name appearing in FeatureOrder.
//   - TestCollectNodeFeatures_CredentialToSecrets — pins the
//     credential-routing branch: a kind:secret feature ends up in
//     Secrets and Features stays empty.
//   - TestCollectNodeFeatures_TypeMismatchError — pins the type-match
//     fatal halt: same instance name with two different types returns
//     ErrFeatureTypeMismatch via errors.Is.
//   - TestCollectNodeFeatures_LastWinsMerge — pins the "last wins"
//     deep-merge on values: two attachments of the same instance name
//     merge per-key with the second writer winning where both declare
//     the same key.
//   - TestCollectNodeFeatures_MarkersDeduplicatedAndSorted — pins the
//     determinism discipline at the markers slice: duplicate types
//     collapse to one entry and the slice is sorted.
//   - TestSplitPath — pins the dual-separator path tokeniser used by
//     subsystem and boundary path traversal.

package ir

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func featureCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/monitoring":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/monitoring", Version: "1.0.0"},
				}, true
			case "core/db-creds":
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: labels,
					},
				}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

func TestCollectNodeFeatures_SimpleComponent(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("enabled", true)
	apiNode := &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{Type: "core/monitoring", Values: values}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Features.Len() != 1 {
		t.Fatalf("expected 1 feature, got %d", result.Features.Len())
	}
	f, _ := result.Features.Get("monitoring")
	if f.Type != "core/monitoring" {
		t.Errorf("type: %q", f.Type)
	}
	enabled, _ := f.Values.Get("enabled")
	if enabled != true {
		t.Errorf("values: %v", f.Values.Keys())
	}
	if len(result.FeatureOrder) != 1 || result.FeatureOrder[0] != "monitoring" {
		t.Errorf("order: %v", result.FeatureOrder)
	}
}

func TestCollectNodeFeatures_CredentialToSecrets(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("vault_path", "/secret/db")
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {
				{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds", Values: values}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Features.Len() != 0 {
		t.Errorf("expected 0 features, got %d (credential should be in secrets)", result.Features.Len())
	}
	if result.Secrets.Len() != 1 {
		t.Fatalf("expected 1 secret, got %d", result.Secrets.Len())
	}
	s, _ := result.Secrets.Get("db-creds")
	if s.Type != "core/db-creds" {
		t.Errorf("type: %q", s.Type)
	}
}

func TestCollectNodeFeatures_TypeMismatchError(t *testing.T) {
	t.Parallel()

	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feat", Feature: types.FeatureInstance{Type: "type-a"}},
				{InstanceName: "feat", Feature: types.FeatureInstance{Type: "type-b"}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	_, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
	if !errors.Is(err, ErrFeatureTypeMismatch) {
		t.Errorf("expected ErrFeatureTypeMismatch, got: %v", err)
	}
}

func TestCollectNodeFeatures_LastWinsMerge(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("level", "warn")
	values.Set("port", 9090)
	values2 := types.OrderedMap[any]{}
	values2.Set("level", "info")
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "mon", Feature: types.FeatureInstance{Type: "core/monitoring", Values: values}},
				{InstanceName: "mon", Feature: types.FeatureInstance{Type: "core/monitoring", Values: values2}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, _ := result.Features.Get("mon")
	level, _ := f.Values.Get("level")
	if level != "info" {
		t.Errorf("expected 'info' (last wins), got %v", level)
	}
	port, _ := f.Values.Get("port")
	if port != 9090 {
		t.Errorf("expected port 9090 preserved, got %v", port)
	}
}

func TestCollectNodeFeatures_MarkersDeduplicatedAndSorted(t *testing.T) {
	t.Parallel()

	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "z-feat", Feature: types.FeatureInstance{Type: "z-type"}},
				{InstanceName: "a-feat", Feature: types.FeatureInstance{Type: "a-type"}},
				{InstanceName: "z-feat2", Feature: types.FeatureInstance{Type: "z-type"}}, // duplicate type
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Markers) != 2 {
		t.Fatalf("expected 2 unique markers, got %d: %v", len(result.Markers), result.Markers)
	}
	if result.Markers[0] != "a-type" || result.Markers[1] != "z-type" {
		t.Errorf("markers not sorted/deduped: %v", result.Markers)
	}
}

func TestCollectNodeFeatures_ProvenanceExplicit_Tier1(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{Type: "core/monitoring"}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, _ := result.Features.Get("monitoring")
	if f.Origin != "explicit" {
		t.Errorf("Origin = %q, want explicit (Tier 1/2 attachments are always explicit)", f.Origin)
	}
	if f.AnnotationKey != "" || f.AnnotationSource != "" {
		t.Errorf("explicit attachment should have empty key/source; got key=%q source=%q", f.AnnotationKey, f.AnnotationSource)
	}
}

func TestCollectNodeFeatures_ProvenanceAnnotation_Tier5(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{Nodes: nodes}

	comp := types.BoundaryComponent{}
	comp.Features.Set("feature.myco/rewrite-host", types.BoundaryFeature{Type: "core/rewrite-host:1.0.0"})
	prod := types.Boundary{}
	prod.Components.Set("api", comp)
	release := &types.Release{}
	release.Spec.Boundaries.Set("prod", prod)

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release:   release,
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("prod", "api", "feature.myco/rewrite-host"): {
				AnnotationKey:    "feature.myco/rewrite-host",
				AnnotationSource: "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host",
			},
		},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := result.Features.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatalf("Tier 5 attachment missing from IR features")
	}
	if f.Origin != "annotation" {
		t.Errorf("Origin = %q, want annotation (side-table entry present)", f.Origin)
	}
	if f.AnnotationKey != "feature.myco/rewrite-host" {
		t.Errorf("AnnotationKey = %q, want feature.myco/rewrite-host", f.AnnotationKey)
	}
	if f.AnnotationSource != "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host" {
		t.Errorf("AnnotationSource = %q (expected from side-table)", f.AnnotationSource)
	}
}

func TestCollectNodeFeatures_ProvenanceTier5_NoSideTableEntry_IsExplicit(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{Nodes: nodes}

	comp := types.BoundaryComponent{}
	comp.Features.Set("core/server-explicit", types.BoundaryFeature{Type: "core/server-explicit:1.0.0"})
	prod := types.Boundary{}
	prod.Components.Set("api", comp)
	release := &types.Release{}
	release.Spec.Boundaries.Set("prod", prod)

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release:   release,
		// Empty side-table — the Tier 5 attachment was author-written, not annotation-driven.
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, _ := result.Features.Get("core/server-explicit")
	if f.Origin != "explicit" {
		t.Errorf("Origin = %q, want explicit (no side-table entry)", f.Origin)
	}
}

func TestIRFeatureEntry_ProvenanceRoundTrip(t *testing.T) {
	t.Parallel()
	entry := types.IRFeatureEntry{
		Type:             "core/rewrite-host:1.0.0",
		ResolvedVersion:  "1.0.0",
		Origin:           "annotation",
		AnnotationKey:    "feature.myco/rewrite-host",
		AnnotationSource: "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round types.IRFeatureEntry
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.Origin != entry.Origin || round.AnnotationKey != entry.AnnotationKey || round.AnnotationSource != entry.AnnotationSource {
		t.Errorf("round-trip lost provenance: got %+v", round)
	}

	// Explicit attachment: Origin is populated ("explicit") and serializes;
	// AnnotationKey and AnnotationSource are empty and omitempty drops them.
	entryExplicit := types.IRFeatureEntry{Type: "core/server:1.0.0", ResolvedVersion: "1.0.0", Origin: "explicit"}
	data2, err := json.Marshal(entryExplicit)
	if err != nil {
		t.Fatalf("marshal explicit: %v", err)
	}
	if !strings.Contains(string(data2), `"origin":"explicit"`) {
		t.Errorf("explicit Origin should serialize; got: %s", data2)
	}
	if strings.Contains(string(data2), `"annotation_key"`) || strings.Contains(string(data2), `"annotation_source"`) {
		t.Errorf("omitempty failed; empty key/source serialized: %s", data2)
	}

	// Truly-empty provenance: Origin, AnnotationKey, AnnotationSource all
	// empty. All three carry `json:",omitempty"`, so all three drop from
	// the wire. Pre-provenance IR fixtures and golden files re-marshal
	// without introducing the new keys, which is the backwards-compat
	// contract the field tags exist to guarantee.
	entryNoProv := types.IRFeatureEntry{Type: "core/server:1.0.0", ResolvedVersion: "1.0.0"}
	data3, err := json.Marshal(entryNoProv)
	if err != nil {
		t.Fatalf("marshal no-provenance: %v", err)
	}
	if strings.Contains(string(data3), `"origin"`) {
		t.Errorf("empty Origin should be omitted; got: %s", data3)
	}
	if strings.Contains(string(data3), `"annotation_key"`) || strings.Contains(string(data3), `"annotation_source"`) {
		t.Errorf("empty key/source should be omitted; got: %s", data3)
	}
}

func TestSplitPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"single", []string{"single"}},
		{"a/b/c", []string{"a", "b", "c"}},
		{"a.b.c", []string{"a", "b", "c"}},
		{"a/b.c", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		parts := splitPath(tt.input)
		if len(parts) != len(tt.want) {
			t.Errorf("splitPath(%q) = %v, want %v", tt.input, parts, tt.want)
			continue
		}
		for i, p := range parts {
			if p != tt.want[i] {
				t.Errorf("splitPath(%q)[%d] = %q, want %q", tt.input, i, p, tt.want[i])
			}
		}
	}
}
