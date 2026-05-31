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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

// TestResolvedVersionIsFeatureVersionNotComponent pins the source-fix: a
// feature/secret entry's resolved_version is the attachment's OWN catalog version
// (catalogLookup.Feature(type).Metadata.Version), NOT the host component's version.
// Existing fixtures use version-less features (resolved_version stays ""), so this
// test supplies a versioned catalog and asserts the entry carries the feature's
// version — the proof the populate landed against the right source.
func TestResolvedVersionIsFeatureVersionNotComponent(t *testing.T) {
	t.Parallel()
	cat := resolver.CatalogLookup{
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/monitoring":
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/monitoring", Version: "2.5.0"}}, true
			case "core/db-creds":
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/db-creds", Version: "3.1.0", Labels: labels}}, true
			}
			return types.Feature{}, false
		},
	}
	result := &featureResult{
		Features: types.OrderedMap[types.IRFeatureEntry]{},
		Secrets:  types.OrderedMap[types.IRSecretEntry]{},
	}
	empty := types.OrderedMap[any]{}
	if err := mergeContribution(result, "mon", "core/monitoring", empty, empty, map[string]bool{}, cat, "api", explicitProvenance); err != nil {
		t.Fatalf("mergeContribution feature: %v", err)
	}
	if err := mergeContribution(result, "creds", "core/db-creds", empty, empty, map[string]bool{}, cat, "api", explicitProvenance); err != nil {
		t.Fatalf("mergeContribution credential: %v", err)
	}
	if f, _ := result.Features.Get("mon"); f.ResolvedVersion != "2.5.0" {
		t.Errorf("feature resolved_version = %q; want 2.5.0 (feature catalog version)", f.ResolvedVersion)
	}
	if s, _ := result.Secrets.Get("creds"); s.ResolvedVersion != "3.1.0" {
		t.Errorf("credential resolved_version = %q; want 3.1.0 (feature catalog version)", s.ResolvedVersion)
	}
}

// TestCollectNodeFeatures_TopologySecretResolvedVersion pins resolved_version on the
// topology SecretAttachments path (the :237 constructor), distinct from the
// mergeContribution credential branch.
func TestCollectNodeFeatures_TopologySecretResolvedVersion(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds"}}},
		},
	}
	cat := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/db-creds" {
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/db-creds", Version: "3.1.0"}}, true
			}
			return types.Feature{}, false
		},
	}
	resolveResult := &resolver.ResolveResult{Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "t"}}}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, cat)
	if err != nil {
		t.Fatalf("collectNodeFeatures: %v", err)
	}
	if s, _ := result.Secrets.Get("db-creds"); s.ResolvedVersion != "3.1.0" {
		t.Errorf("topology secret resolved_version = %q; want 3.1.0", s.ResolvedVersion)
	}
}

// TestMergeContribution_CredentialCarriesPerInstanceInfra pins the secret/feature
// IR-entry unification: a credential routed into the secrets bucket carries its
// per-instance infra (the Release-tier infra in scope at the merge site), exactly
// as a feature does — not the former lossy {Type, Values}-only projection that
// dropped it. Before the unification this infra was discarded and the secret
// render path read an empty bag.
func TestMergeContribution_CredentialCarriesPerInstanceInfra(t *testing.T) {
	t.Parallel()
	result := &featureResult{
		Features: types.OrderedMap[types.IRFeatureEntry]{},
		Secrets:  types.OrderedMap[types.IRSecretEntry]{},
	}
	values := types.OrderedMap[any]{}
	values.Set("vault_path", "/secret/db")
	infra := types.OrderedMap[any]{}
	infra.Set("replica_count", "3")
	markerSet := map[string]bool{}

	if err := mergeContribution(result, "db-creds", "core/db-creds", values, infra, markerSet, featureCatalog(), "api", explicitProvenance); err != nil {
		t.Fatalf("mergeContribution: %v", err)
	}
	if result.Features.Len() != 0 {
		t.Errorf("credential should route to secrets, not features; got %d features", result.Features.Len())
	}
	s, ok := result.Secrets.Get("db-creds")
	if !ok {
		t.Fatal("expected secret db-creds")
	}
	got, ok := s.Infra.Get("replica_count")
	if !ok || got != "3" {
		t.Errorf("secret per-instance infra not carried: Infra=%v; want replica_count=3", s.Infra)
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

func TestIRBuilder_NestedLeafProvenance_KeyedByDottedPath(t *testing.T) {
	t.Parallel()
	// Annotation routes to a nested binding path values.ingress.host.
	// The synthesized contribution carries a nested OrderedMap shape:
	//   Values = { ingress: { host: "api.example.com" } }
	// The IR builder must record FieldProvenance at the flat-dotted-
	// leaf key "ingress.host" — NOT at the top-level key "ingress".

	innerVals := types.OrderedMap[any]{}
	innerVals.Set("host", "api.example.com")
	values := types.OrderedMap[any]{}
	values.Set("ingress", &innerVals)

	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/host", Feature: types.FeatureInstance{
					Type:   "core/ingress:1.0.0",
					Values: values,
				}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
			resolver.AnnotationProvenanceKey("components.api", "", "feature.myco/host"): {
				AnnotationKey:    "feature.myco/host",
				AnnotationSource: "aggregate.yaml:components.api.annotations.feature.myco/host",
			},
		},
	}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	feat, _ := result.Features.Get("feature.myco/host")

	// The flat-dotted-leaf key is "ingress.host" — not "ingress".
	prov, ok := feat.ValuesProvenance.Get("ingress.host")
	if !ok {
		t.Fatalf("ValuesProvenance.Get(\"ingress.host\") missing; got keys: %v", provenanceKeys(feat.ValuesProvenance))
	}
	if prov.Origin != "annotation" || prov.Side != "aggregate" {
		t.Errorf("nested-leaf provenance Origin=%q Side=%q; want annotation/aggregate", prov.Origin, prov.Side)
	}
	// And the top-level "ingress" key should NOT exist as a
	// provenance entry — provenance lives at the leaf granularity.
	if _, present := feat.ValuesProvenance.Get("ingress"); present {
		t.Error("ValuesProvenance should NOT carry a top-level 'ingress' entry; provenance is per-leaf, not per-intermediate")
	}
}

func provenanceKeys(m types.OrderedMap[types.FieldProvenance]) []string {
	out := []string{}
	for k := range m.All() {
		out = append(out, k)
	}
	return out
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

// Structured-annotation IR builder coverage. The IR builder consumes
// structured-annotation contributions transparently through the same
// per-leaf merge already in place — each subkey of a structured
// annotation lands as its own leaf in the contribution's pillar
// OrderedMap. The tests below pin (a) values-pillar leaves populating
// from an Aggregate-side structured annotation; (b) infra-pillar
// leaves populating from a Release-side structured annotation;
// (c) cross-side composition into one IRFeatureEntry with both pillars
// populated; (d) the "same annotation → same source → all leaves
// uniform" provenance expectation when one structured annotation
// produces multiple leaves.

func TestIRBuilder_StructuredAggregateAnnotation_FillsValuesFromSubkeys(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("db_name", "prod-db", "read_replicas", 5),
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
	if dbName, _ := feat.Values.Get("db_name"); dbName != "prod-db" {
		t.Errorf("Values.db_name = %v, want prod-db", dbName)
	}
	if replicas, _ := feat.Values.Get("read_replicas"); replicas != 5 {
		t.Errorf("Values.read_replicas = %v, want 5", replicas)
	}
}

func TestIRBuilder_StructuredReleaseAnnotation_FillsInfraFromSubkeys(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{Nodes: nodes}

	comp := types.BoundaryComponent{}
	bf := types.BoundaryFeature{
		Type:  "core/db-pool:1.0.0",
		Infra: valuesMap("pool_size", 20, "max_connections", 100),
	}
	comp.Features.Set("feature.myco/db-pool", bf)
	prod := types.Boundary{}
	prod.Components.Set("api", comp)
	release := &types.Release{}
	release.Spec.Boundaries.Set("prod", prod)

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release:   release,
		AnnotationProvenance: map[string]resolver.AnnotationProvenance{
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
	if poolSize, _ := feat.Infra.Get("pool_size"); poolSize != 20 {
		t.Errorf("Infra.pool_size = %v, want 20", poolSize)
	}
	if maxConn, _ := feat.Infra.Get("max_connections"); maxConn != 100 {
		t.Errorf("Infra.max_connections = %v, want 100", maxConn)
	}
}

func TestIRBuilder_StructuredCrossSide_ComposesBothPillars(t *testing.T) {
	t.Parallel()
	// Aggregate-side structured annotation contributes two
	// values-pillar leaves; Release-side structured annotation
	// contributes two infra-pillar leaves on the same Feature key.
	// The IR builder composes both into one IRFeatureEntry with all
	// four pillar fields populated and the top-level Origin derives
	// to "mixed" (cross-side composition).
	apiNode := &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)

	comp := types.BoundaryComponent{}
	bf := types.BoundaryFeature{
		Type:  "core/db-pool:1.0.0",
		Infra: valuesMap("pool_size", 20, "max_connections", 100),
	}
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
					Values: valuesMap("db_name", "prod-db", "read_replicas", 5),
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

	// All four leaves populated.
	if dbName, _ := feat.Values.Get("db_name"); dbName != "prod-db" {
		t.Errorf("Values.db_name = %v, want prod-db", dbName)
	}
	if replicas, _ := feat.Values.Get("read_replicas"); replicas != 5 {
		t.Errorf("Values.read_replicas = %v, want 5", replicas)
	}
	if poolSize, _ := feat.Infra.Get("pool_size"); poolSize != 20 {
		t.Errorf("Infra.pool_size = %v, want 20", poolSize)
	}
	if maxConn, _ := feat.Infra.Get("max_connections"); maxConn != 100 {
		t.Errorf("Infra.max_connections = %v, want 100", maxConn)
	}
	if feat.Origin != "mixed" {
		t.Errorf("top-level Origin = %q, want mixed (cross-side composition)", feat.Origin)
	}
}

func TestIRBuilder_StructuredAnnotation_PerSubkeyProvenance(t *testing.T) {
	t.Parallel()
	// One structured annotation produces multiple leaves in one
	// pillar. The side-table records ONE entry per
	// (componentPath, "", featureKey) — both leaves derive from the
	// same annotation source. The IR builder's Tier 1/2/5 lookup
	// hits the side-table entry once and stamps the same
	// AnnotationKey + AnnotationSource onto every leaf produced by
	// the contribution. This pins the "same annotation → same
	// source → all leaves uniform" expectation.
	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feature.myco/db-pool", Feature: types.FeatureInstance{
					Type:   "core/db-pool:1.0.0",
					Values: valuesMap("db_name", "prod-db", "read_replicas", 5),
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

	dbNameProv, _ := feat.ValuesProvenance.Get("db_name")
	replicasProv, _ := feat.ValuesProvenance.Get("read_replicas")

	// All subkey leaves carry the SAME AnnotationKey + AnnotationSource.
	if dbNameProv.AnnotationKey != replicasProv.AnnotationKey {
		t.Errorf("db_name key=%q vs read_replicas key=%q — both leaves should share the same annotation key",
			dbNameProv.AnnotationKey, replicasProv.AnnotationKey)
	}
	if dbNameProv.AnnotationSource != replicasProv.AnnotationSource {
		t.Errorf("db_name source=%q vs read_replicas source=%q — both leaves should share the same annotation source",
			dbNameProv.AnnotationSource, replicasProv.AnnotationSource)
	}
	// All carry the same Side.
	if dbNameProv.Side != replicasProv.Side || dbNameProv.Side != "aggregate" {
		t.Errorf("Side differs or wrong: db_name=%q replicas=%q (want both aggregate)",
			dbNameProv.Side, replicasProv.Side)
	}
	// Per-subkey provenance differentiation is NOT expected: per-leaf
	// provenance already does the right thing by uniform attribution
	// to the single annotation source.
}

// TestParseBoundaryFeature_TypedMapForm pins the *types.OrderedMap[any]
// branch of the boundary-feature parser. Release boundary YAML decodes
// into the typed OrderedMap; the parser must extract the type field
// and the infra OrderedMap unchanged.
func TestParseBoundaryFeature_TypedMapForm(t *testing.T) {
	t.Parallel()
	infra := types.OrderedMap[any]{}
	infra.Set("upstream", "host.example.com")
	m := types.OrderedMap[any]{}
	m.Set("type", "feature.myco/rewrite-host")
	m.Set("infra", &infra)
	got := parseBoundaryFeature(&m)
	if got.Type != "feature.myco/rewrite-host" {
		t.Errorf("Type = %q; want feature.myco/rewrite-host", got.Type)
	}
	if v, ok := got.Infra.Get("upstream"); !ok || v != "host.example.com" {
		t.Errorf("Infra.upstream = (%v, %v); want (host.example.com, true)", v, ok)
	}
}

// TestParseBoundaryFeature_PlainMapForm pins the map[string]any branch
// of the parser — boundary YAML payloads that decode via the
// alternative untyped map path. The parser must still extract type and
// infra correctly, sorting the converted infra keys for determinism.
func TestParseBoundaryFeature_PlainMapForm(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"type": "feature.myco/rewrite-host",
		"infra": map[string]any{
			"upstream":   "host.example.com",
			"alt_target": "fallback.example.com",
		},
	}
	got := parseBoundaryFeature(raw)
	if got.Type != "feature.myco/rewrite-host" {
		t.Errorf("Type = %q; want feature.myco/rewrite-host", got.Type)
	}
	if v, ok := got.Infra.Get("upstream"); !ok || v != "host.example.com" {
		t.Errorf("Infra.upstream = (%v, %v); want (host.example.com, true)", v, ok)
	}
}

// TestParseBoundaryFeature_NilAndUnknownReturnZero pins the defensive
// branches: nil typed-map pointer and unrecognised raw shape both
// produce the zero-value BoundaryFeature.
func TestParseBoundaryFeature_NilAndUnknownReturnZero(t *testing.T) {
	t.Parallel()
	var nilMap *types.OrderedMap[any]
	if got := parseBoundaryFeature(nilMap); got.Type != "" {
		t.Errorf("nil typed-map: Type = %q; want zero", got.Type)
	}
	if got := parseBoundaryFeature("unrelated string"); got.Type != "" {
		t.Errorf("unknown shape: Type = %q; want zero", got.Type)
	}
}

// TestBuildCatalogMetadata_NilLookupReturnsNil pins the early-return
// guard: a CatalogLookup with no Component function returns nil rather
// than panic on the nil function pointer.
func TestBuildCatalogMetadata_NilLookupReturnsNil(t *testing.T) {
	t.Parallel()
	if got := buildCatalogMetadata("anything", resolver.CatalogLookup{}); got != nil {
		t.Errorf("nil lookup should return nil; got %+v", got)
	}
}

// TestBuildCatalogMetadata_LookupMissReturnsNil pins the not-found
// branch: a Component not present in the catalog produces nil rather
// than a zero-value CatalogMetadata.
func TestBuildCatalogMetadata_LookupMissReturnsNil(t *testing.T) {
	t.Parallel()
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
	}
	if got := buildCatalogMetadata("not/in/catalog", cl); got != nil {
		t.Errorf("lookup miss should return nil; got %+v", got)
	}
}

// TestBuildCatalogMetadata_EmptyContentReturnsNil pins the all-fields-
// empty branch: a Component whose metadata carries no license, no
// repo, no home, no keywords, not-deprecated, no maintainers collapses
// to nil so the IR's CatalogMetadata pointer stays unset and the
// `omitempty` JSON tag suppresses the field entirely.
func TestBuildCatalogMetadata_EmptyContentReturnsNil(t *testing.T) {
	t.Parallel()
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) {
			return types.Component{Metadata: types.ComponentMetadata{Name: "x"}}, true
		},
	}
	if got := buildCatalogMetadata("x", cl); got != nil {
		t.Errorf("empty metadata should return nil; got %+v", got)
	}
}

// TestBuildCatalogMetadata_PopulatedReturnsAllFields pins the happy-path
// branch where every metadata field carries content. Asserts each
// surviving field round-trips into the result.
func TestBuildCatalogMetadata_PopulatedReturnsAllFields(t *testing.T) {
	t.Parallel()
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) {
			return types.Component{
				Metadata: types.ComponentMetadata{
					Name:        "x",
					License:     "Apache-2.0",
					Repository:  "https://example.com/repo",
					Home:        "https://example.com",
					Keywords:    []string{"k1", "k2"},
					Deprecated:  true,
					Maintainers: []types.Maintainer{{Name: "alice"}},
				},
			}, true
		},
	}
	got := buildCatalogMetadata("x", cl)
	if got == nil {
		t.Fatal("populated metadata should not return nil")
	}
	if got.License != "Apache-2.0" || got.Repository == "" || got.Home == "" {
		t.Errorf("scalar fields not propagated: %+v", got)
	}
	if !got.Deprecated || len(got.Keywords) != 2 || len(got.Maintainers) != 1 {
		t.Errorf("multi-shape fields not propagated: %+v", got)
	}
}

// TestIsCredential_NilFeatureLookupReturnsFalse pins the nil-Feature
// guard. Without a Feature lookup function the discriminator returns
// false rather than panic.
func TestIsCredential_NilFeatureLookupReturnsFalse(t *testing.T) {
	t.Parallel()
	if isCredential("anything", resolver.CatalogLookup{}) {
		t.Errorf("nil Feature lookup should return false")
	}
}

// TestIsCredential_LookupMissReturnsFalse pins the not-found branch.
// A Feature URI absent from the catalog returns false (treating the
// missing Feature as non-credential).
func TestIsCredential_LookupMissReturnsFalse(t *testing.T) {
	t.Parallel()
	cl := resolver.CatalogLookup{
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
	if isCredential("not/in/catalog", cl) {
		t.Errorf("lookup miss should return false")
	}
}
