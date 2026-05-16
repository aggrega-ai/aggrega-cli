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

// Cross-side feature composition + per-leaf provenance tests.
//
// These tests pin the per-pillar-per-leaf merge introduced by the
// IR-builder restructure: ValuesProvenance / InfraProvenance maps
// carry FieldProvenance per leaf; explicit-wins-at-leaf overrides
// annotation contributions and records OverridesAnnotation; top-level
// Origin derives to "explicit" / "annotation" / "mixed" per the
// compatibility rule.

func valuesMap(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func TestAggregateCanonicalPath_Shapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		node *topology.TopologyNode
		want string
	}{
		{
			name: "top-level root-aggregate component",
			node: &topology.TopologyNode{InstanceID: "api"},
			want: "components.api",
		},
		{
			name: "subsystem-contained root-aggregate component",
			node: &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"},
			want: "subsystems.data.components.api",
		},
		{
			name: "nested-subsystem root-aggregate component",
			node: &topology.TopologyNode{InstanceID: "leaf", SubsystemPath: "outer.inner"},
			want: "subsystems.outer.subsystems.inner.components.leaf",
		},
		{
			name: "sub-aggregate component returns empty (no provenance lookup)",
			node: &topology.TopologyNode{InstanceID: "data-module.child", SourceAggregate: "data-module"},
			want: "",
		},
		{
			name: "nil node returns empty",
			node: nil,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := aggregateCanonicalPath(tt.node)
			if got != tt.want {
				t.Errorf("aggregateCanonicalPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIRBuilder_AnnotationOnlyValues_PopulatesValuesProvenance(t *testing.T) {
	t.Parallel()
	// Aggregate-side annotation routes to the values pillar; the
	// per-leaf provenance reflects the annotation source + side.
	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("pool", "shared-pool"),
				}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("subsystems.data.components.api", "", "feature.myco/db-pool"): {
				AnnotationKey:    "feature.myco/db-pool",
				AnnotationSource: "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool",
			},
		},
	}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("feature.myco/db-pool")
	prov, ok := feat.ValuesProvenance.Get("pool")
	if !ok {
		t.Fatal("ValuesProvenance.pool missing")
	}
	if prov.Origin != "annotation" {
		t.Errorf("Origin = %q, want annotation", prov.Origin)
	}
	if prov.Side != "aggregate" {
		t.Errorf("Side = %q, want aggregate", prov.Side)
	}
	if prov.AnnotationKey != "feature.myco/db-pool" {
		t.Errorf("AnnotationKey = %q, want feature.myco/db-pool", prov.AnnotationKey)
	}
	if prov.AnnotationSource != "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool" {
		t.Errorf("AnnotationSource = %q (mismatch)", prov.AnnotationSource)
	}
	if feat.Origin != "annotation" {
		t.Errorf("top-level Origin = %q, want annotation (all leaves agree)", feat.Origin)
	}
}

func TestIRBuilder_AnnotationOnlyInfra_PopulatesInfraProvenance(t *testing.T) {
	t.Parallel()
	// Release-side annotation routes to the infra pillar; per-leaf
	// provenance reflects the annotation source + side.
	apiNode := &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{Nodes: nodes}

	comp := types.BoundaryComponent{}
	bf := types.BoundaryFeature{Type: "core/rewrite-host:1.0.0", Infra: valuesMap("upstream", "api.example.com")}
	comp.Features.Set("feature.myco/rewrite-host", bf)
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
	feat, _ := result.Features.Get("feature.myco/rewrite-host")
	prov, ok := feat.InfraProvenance.Get("upstream")
	if !ok {
		t.Fatal("InfraProvenance.upstream missing")
	}
	if prov.Origin != "annotation" || prov.Side != "release" {
		t.Errorf("Origin=%q Side=%q; want annotation/release", prov.Origin, prov.Side)
	}
	if feat.Origin != "annotation" {
		t.Errorf("top-level Origin = %q, want annotation", feat.Origin)
	}
}

func TestIRBuilder_ExplicitOnly_PopulatesProvenanceAsExplicit(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{
					Type:   "core/monitoring",
					Values: valuesMap("enabled", true),
				}},
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
	feat, _ := result.Features.Get("monitoring")
	prov, ok := feat.ValuesProvenance.Get("enabled")
	if !ok {
		t.Fatal("ValuesProvenance.enabled missing")
	}
	if prov.Origin != "explicit" {
		t.Errorf("Origin = %q, want explicit", prov.Origin)
	}
	if prov.Side != "" {
		t.Errorf("Side = %q, want empty for explicit", prov.Side)
	}
	if feat.Origin != "explicit" {
		t.Errorf("top-level Origin = %q, want explicit", feat.Origin)
	}
}

func TestIRBuilder_CrossSideCompose_PopulatesBothPillarProvenance(t *testing.T) {
	t.Parallel()
	// Aggregate-side annotation contributes values; Release-side
	// annotation contributes infra on the same (Component, Feature)
	// pair. The merged IRFeatureEntry carries both per-leaf provenance
	// records; top-level Origin derives to "mixed".
	apiNode := &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)

	comp := types.BoundaryComponent{}
	bf := types.BoundaryFeature{Type: "core/db-pool:1.0.0", Infra: valuesMap("pool_size", 20)}
	comp.Features.Set("feature.myco/db-pool", bf)
	prod := types.Boundary{}
	prod.Components.Set("api", comp)
	release := &types.Release{}
	release.Spec.Boundaries.Set("prod", prod)

	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("pool", "shared-pool"),
				}},
			},
		},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release:   release,
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("components.api", "", "feature.myco/db-pool"): {
				AnnotationKey:    "feature.myco/db-pool",
				AnnotationSource: "aggregate.yaml:components.api.annotations.feature.myco/db-pool",
			},
			resolver.AnnotationProvenanceKey("prod", "api", "feature.myco/db-pool"): {
				AnnotationKey:    "feature.myco/db-pool",
				AnnotationSource: "release.yaml:boundaries.prod.annotations.feature.myco/db-pool",
			},
		},
	}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("feature.myco/db-pool")

	valuesProv, _ := feat.ValuesProvenance.Get("pool")
	if valuesProv.Side != "aggregate" {
		t.Errorf("values pool Side = %q, want aggregate", valuesProv.Side)
	}
	infraProv, _ := feat.InfraProvenance.Get("pool_size")
	if infraProv.Side != "release" {
		t.Errorf("infra pool_size Side = %q, want release", infraProv.Side)
	}
	if feat.Origin != "mixed" {
		t.Errorf("top-level Origin = %q, want mixed (cross-side composition)", feat.Origin)
	}
}

func TestIRBuilder_ExplicitOverridesAnnotation_FieldLevel_Provenance(t *testing.T) {
	t.Parallel()
	// Tier 3 explicit subsystem contribution lands a leaf BEFORE Tier
	// 5 annotation tries to contribute the same leaf — wait, ordering
	// matters: explicit-wins-at-leaf works for both directions. The
	// canonical test: annotation contribution arrives first (e.g., via
	// Tier 1/2), explicit subsystem contribution arrives second (Tier
	// 3), and the explicit overrides.
	//
	// Sequence: Tier 1/2 with annotation provenance writes pool=annot;
	// Tier 3 explicit writes pool=explicit. The explicit override
	// records OverridesAnnotation.

	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)

	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("pool", "annot-value"),
				}},
			},
		},
	}

	subVals := valuesMap("pool", "explicit-value")
	subsystem := types.Subsystem{}
	subsystem.Features.Set("feature.myco/db-pool", types.FeatureInstance{
		Type:   "core/db-pool:1.0.0",
		Values: subVals,
	})
	agg := &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}}
	agg.Spec.Subsystems.Set("data", subsystem)

	resolveResult := &resolver.ResolveResult{
		Aggregate: agg,
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("subsystems.data.components.api", "", "feature.myco/db-pool"): {
				AnnotationKey:    "feature.myco/db-pool",
				AnnotationSource: "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool",
			},
		},
	}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("feature.myco/db-pool")
	pool, _ := feat.Values.Get("pool")
	if pool != "explicit-value" {
		t.Errorf("pool = %v, want explicit-value (explicit wins)", pool)
	}
	prov, _ := feat.ValuesProvenance.Get("pool")
	if prov.Origin != "explicit" {
		t.Errorf("pool prov Origin = %q, want explicit", prov.Origin)
	}
	if prov.OverridesAnnotation == nil {
		t.Fatal("OverridesAnnotation should be populated with the suppressed annotation provenance")
	}
	if prov.OverridesAnnotation.AnnotationKey != "feature.myco/db-pool" {
		t.Errorf("suppressed AnnotationKey = %q, want feature.myco/db-pool", prov.OverridesAnnotation.AnnotationKey)
	}
}

func TestIRBuilder_DeepMerge_UnlistedFieldsKeepAnnotationContribution(t *testing.T) {
	t.Parallel()
	// Annotation contributes pool=annot AND replicas=5; explicit
	// contributes only pool=explicit. The explicit override hits pool;
	// replicas keeps its annotation provenance.

	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("pool", "annot-pool", "replicas", 5),
				}},
			},
		},
	}
	subsystem := types.Subsystem{}
	subsystem.Features.Set("feature.myco/db-pool", types.FeatureInstance{
		Type:   "core/db-pool:1.0.0",
		Values: valuesMap("pool", "explicit-pool"),
	})
	agg := &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}}
	agg.Spec.Subsystems.Set("data", subsystem)

	resolveResult := &resolver.ResolveResult{
		Aggregate: agg,
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("subsystems.data.components.api", "", "feature.myco/db-pool"): {
				AnnotationKey:    "feature.myco/db-pool",
				AnnotationSource: "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool",
			},
		},
	}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("feature.myco/db-pool")
	poolProv, _ := feat.ValuesProvenance.Get("pool")
	if poolProv.Origin != "explicit" {
		t.Errorf("pool Origin = %q, want explicit (override)", poolProv.Origin)
	}
	replicasProv, _ := feat.ValuesProvenance.Get("replicas")
	if replicasProv.Origin != "annotation" {
		t.Errorf("replicas Origin = %q, want annotation (unlisted by explicit, keeps annotation)", replicasProv.Origin)
	}
	if feat.Origin != "mixed" {
		t.Errorf("top-level Origin = %q, want mixed (one leaf explicit-overrides, one stays annotation)", feat.Origin)
	}
}

func TestIRBuilder_TopLevelOrigin_AnnotationWhenAllLeavesSameAnnotation(t *testing.T) {
	t.Parallel()
	// Single Aggregate-side annotation contributes two leaves to the
	// values pillar; both share the same FieldProvenance shape.
	// Top-level Origin derives to "annotation" with the singular
	// key/source.
	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("pool", "shared", "size", 100),
				}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("subsystems.data.components.api", "", "feature.myco/db-pool"): {
				AnnotationKey:    "feature.myco/db-pool",
				AnnotationSource: "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool",
			},
		},
	}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("feature.myco/db-pool")
	if feat.Origin != "annotation" {
		t.Errorf("Origin = %q, want annotation", feat.Origin)
	}
	if feat.AnnotationKey != "feature.myco/db-pool" {
		t.Errorf("AnnotationKey = %q, want feature.myco/db-pool", feat.AnnotationKey)
	}
}

func TestIRBuilder_AggregateSideProvenance_LookupViaCanonicalPath_NestedSubsystemCase(t *testing.T) {
	t.Parallel()
	// Verify the canonical-path conversion for deeply nested
	// subsystems: topology's SubsystemPath="outer.inner" must convert
	// to canonical "subsystems.outer.subsystems.inner.components.<comp>"
	// to find the side-table entry the resolver's Aggregate-side
	// expansion wrote.
	leafNode := &topology.TopologyNode{InstanceID: "leaf", SubsystemPath: "outer.inner"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("leaf", leafNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"leaf": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("pool", "deep"),
				}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("subsystems.outer.subsystems.inner.components.leaf", "", "feature.myco/db-pool"): {
				AnnotationKey:    "feature.myco/db-pool",
				AnnotationSource: "aggregate.yaml:subsystems.outer.annotations.feature.myco/db-pool",
			},
		},
	}
	result, err := collectNodeFeatures("leaf", leafNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("feature.myco/db-pool")
	prov, _ := feat.ValuesProvenance.Get("pool")
	if prov.Origin != "annotation" {
		t.Errorf("Origin = %q, want annotation (canonical-path lookup should hit the side-table)", prov.Origin)
	}
	if prov.Side != "aggregate" {
		t.Errorf("Side = %q, want aggregate", prov.Side)
	}
}

func TestIRBuilder_SubAggregateComponent_StampsExplicit_SkipsSideTableLookup(t *testing.T) {
	t.Parallel()
	// Sub-aggregate components carry SourceAggregate != ""; the resolver's
	// Aggregate-side expansion does not walk sub-aggregates, so no
	// AnnotationProvenance entry can ever exist for them. The IR
	// builder's Tier 1/2 lookup must NOT consult the side-table for
	// these components — aggregateCanonicalPath returns empty, the
	// lookup skips, and the attachment retains explicit provenance.
	//
	// Defensive scenario: even when a side-table entry hypothetically
	// exists under a guessed canonical path that includes the sub-
	// aggregate prefix, the IR builder must ignore it. This pins the
	// "sub-aggregate components are always explicit at the IR layer"
	// boundary.
	leafNode := &topology.TopologyNode{
		InstanceID:      "data-module.child-db",
		SourceAggregate: "data-module",
	}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("data-module.child-db", leafNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"data-module.child-db": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{
					Type:   "core/monitoring",
					Values: valuesMap("enabled", true),
				}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			// A side-table entry that the IR builder must NOT match
			// against the sub-aggregate component. Sub-aggregates don't
			// get Aggregate-side provenance entries; this fixture
			// exercises the "skip the lookup entirely" path.
			resolver.AnnotationProvenanceKey("components.data-module.child-db", "", "monitoring"): {
				AnnotationKey:    "monitoring",
				AnnotationSource: "should-not-be-stamped",
			},
		},
	}
	result, err := collectNodeFeatures("data-module.child-db", leafNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, ok := result.Features.Get("monitoring")
	if !ok {
		t.Fatal("monitoring attachment missing from IR features")
	}
	prov, _ := feat.ValuesProvenance.Get("enabled")
	if prov.Origin != "explicit" {
		t.Errorf("Origin = %q, want explicit (sub-aggregate components skip the side-table lookup)", prov.Origin)
	}
	if prov.Side != "" {
		t.Errorf("Side = %q, want empty for explicit", prov.Side)
	}
	if prov.AnnotationKey != "" || prov.AnnotationSource != "" {
		t.Errorf("explicit attachment should have empty key/source; got key=%q source=%q", prov.AnnotationKey, prov.AnnotationSource)
	}
	if feat.Origin != "explicit" {
		t.Errorf("top-level Origin = %q, want explicit", feat.Origin)
	}
}

func TestIRBuilder_LeavesLastWins_SameOrigin_ProvenanceRefreshes(t *testing.T) {
	t.Parallel()
	// Two explicit contributions to the same leaf: the later tier
	// wins on value, and the per-leaf provenance refreshes to the
	// later contribution's provenance (still explicit).
	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{
					Type:   "core/monitoring",
					Values: valuesMap("enabled", false),
				}},
			},
		},
	}
	subsystem := types.Subsystem{}
	subsystem.Features.Set("monitoring", types.FeatureInstance{
		Type:   "core/monitoring",
		Values: valuesMap("enabled", true),
	})
	agg := &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}}
	agg.Spec.Subsystems.Set("data", subsystem)

	resolveResult := &resolver.ResolveResult{Aggregate: agg}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("monitoring")
	enabled, _ := feat.Values.Get("enabled")
	if enabled != true {
		t.Errorf("enabled = %v, want true (Tier 3 last-wins over Tier 1/2)", enabled)
	}
	prov, _ := feat.ValuesProvenance.Get("enabled")
	if prov.Origin != "explicit" {
		t.Errorf("Origin = %q, want explicit", prov.Origin)
	}
	if prov.OverridesAnnotation != nil {
		t.Error("same-origin merge should not populate OverridesAnnotation")
	}
}
