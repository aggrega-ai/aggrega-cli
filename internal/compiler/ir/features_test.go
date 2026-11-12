// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Unit tests for collectNodeFeatures and the helper functions in
// features.go — focused per-behaviour coverage that complements the
// integration-shaped tests in builder_test.go. featureCatalog provides a
// minimal CatalogLookup with one operational feature ("core/monitoring")
// and one credential feature ("core/db-creds", labelled with the
// `feature.aggrega.ai/kind: secret` label).
//
// Test cases cover the three mergeFeature behaviour rules:
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
	apiNode := &topology.TopologyNode{InstanceID: "api", ComponentType: "core/compute/api-server"}
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

// TestCollectNodeFeatures_CredentialLandsInTheOneBucket pins that a credential
// attachment is collected into the same bucket, through the same merge, as any
// other attachment — and that it appears in the one order vector, so Phase A and
// Phase B.6 both reach it.
func TestCollectNodeFeatures_CredentialLandsInTheOneBucket(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("vault_path", "/secret/db")
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "db-creds", Feature: types.FeatureInstance{Type: "core/db-creds", Values: values}},
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
		t.Fatalf("expected 1 attachment, got %d", result.Features.Len())
	}
	s, _ := result.Features.Get("db-creds")
	if s.Type != "core/db-creds" {
		t.Errorf("type: %q", s.Type)
	}
	if len(result.FeatureOrder) != 1 || result.FeatureOrder[0] != "db-creds" {
		t.Errorf("a credential must appear in the one order vector; got %v", result.FeatureOrder)
	}
}

// TestResolvedVersionIsFeatureVersionNotComponent pins the source-fix: a
// attachment entry's resolved_version is the attachment's OWN catalog version
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
	result := newFeatureResult()
	empty := types.OrderedMap[any]{}
	if err := mergeContribution(result, "mon", "core/monitoring", empty, empty, nil, cat, "api", explicitProvenance, leafOwner{scope: scopeComponent}); err != nil {
		t.Fatalf("mergeContribution feature: %v", err)
	}
	if err := mergeContribution(result, "creds", "core/db-creds", empty, empty, nil, cat, "api", explicitProvenance, leafOwner{scope: scopeComponent}); err != nil {
		t.Fatalf("mergeContribution credential: %v", err)
	}
	if f, _ := result.Features.Get("mon"); f.ResolvedVersion != "2.5.0" {
		t.Errorf("feature resolved_version = %q; want 2.5.0 (feature catalog version)", f.ResolvedVersion)
	}
	if s, _ := result.Features.Get("creds"); s.ResolvedVersion != "3.1.0" {
		t.Errorf("credential resolved_version = %q; want 3.1.0 (feature catalog version)", s.ResolvedVersion)
	}
}

// TestCollectNodeFeatures_CredentialResolvedVersion pins resolved_version on a
// credential attachment arriving through the topology attachment path: it is the
// credential Feature's OWN catalog version, resolved by the one construction site.
func TestCollectNodeFeatures_CredentialResolvedVersion(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {{InstanceName: "db-creds", Feature: types.FeatureInstance{Type: "core/db-creds"}}},
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
	if s, _ := result.Features.Get("db-creds"); s.ResolvedVersion != "3.1.0" {
		t.Errorf("credential attachment resolved_version = %q; want 3.1.0", s.ResolvedVersion)
	}
}

// TestCollectNodeFeatures_ProjectsAuthoredAttachmentInject pins the IR-build half
// of attachment-inject delivery: the authored feature.inject / secret.inject blocks
// project into the IR entry's Inject field (mechanism -> local key -> provides-key
// reference) via convertInjectBlock, so the renderer has the delivery declaration to
// resolve against the attachment's own provides. Without this projection the authored
// block is validated and dropped.
func TestCollectNodeFeatures_ProjectsAuthoredAttachmentInject(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "cache", Feature: types.FeatureInstance{
					Type:   "core/cache",
					Inject: &types.InjectBlock{Env: []any{map[string]any{"in": "CACHE_URL", "from": "url"}}},
				}},
				{InstanceName: "db-creds", Feature: types.FeatureInstance{
					Type:   "core/db-creds",
					Inject: &types.InjectBlock{Env: []any{map[string]any{"in": "DB_PASSWORD", "from": "root_password"}}},
				}},
			},
		},
	}
	cat := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
		Feature:   func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
	resolveResult := &resolver.ResolveResult{Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "t"}}}
	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, cat)
	if err != nil {
		t.Fatalf("collectNodeFeatures: %v", err)
	}

	f, ok := result.Features.Get("cache")
	if !ok {
		t.Fatal("feature 'cache' absent from result")
	}
	if env, ok := f.Inject.Get("env"); !ok {
		t.Fatalf("feature entry inject missing env mechanism; got %v", f.Inject.Keys())
	} else if v, _ := env.Get("CACHE_URL"); v != "url" {
		t.Errorf("feature inject env.CACHE_URL = %q, want url (provides-key reference)", v)
	}

	s, ok := result.Features.Get("db-creds")
	if !ok {
		t.Fatal("credential attachment 'db-creds' absent from result")
	}
	if env, ok := s.Inject.Get("env"); !ok {
		t.Fatalf("credential entry inject missing env mechanism; got %v", s.Inject.Keys())
	} else if v, _ := env.Get("DB_PASSWORD"); v != "root_password" {
		t.Errorf("credential inject env.DB_PASSWORD = %q, want root_password (provides-key reference)", v)
	}
}

// TestMergeContribution_CredentialCarriesPerInstanceInfra pins the attachment
// IR-entry unification: a credential attachment carries its per-instance infra
// (the Release-tier infra in scope at the merge site) exactly as any other
// attachment does, rather than a lossy {Type, Values}-only projection.
func TestMergeContribution_CredentialCarriesPerInstanceInfra(t *testing.T) {
	t.Parallel()
	result := newFeatureResult()
	values := types.OrderedMap[any]{}
	values.Set("vault_path", "/secret/db")
	infra := types.OrderedMap[any]{}
	infra.Set("replica_count", "3")

	if err := mergeContribution(result, "db-creds", "core/db-creds", values, infra, nil, featureCatalog(), "api", explicitProvenance, leafOwner{scope: scopeComponent}); err != nil {
		t.Fatalf("mergeContribution: %v", err)
	}
	s, ok := result.Features.Get("db-creds")
	if !ok {
		t.Fatal("expected the credential attachment db-creds")
	}
	got, ok := s.Infra.Get("replica_count")
	if !ok || got != "3" {
		t.Errorf("credential per-instance infra not carried: Infra=%v; want replica_count=3", s.Infra)
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
	if !errors.Is(err, ErrFeatureTypeMismatch()) {
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

func TestCollectNodeFeatures_ProvenanceExplicit_Tier1(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api", ComponentType: "core/compute/api-server"}
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
// annotation contributions and records OverriddenAnnotation; top-level
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

func TestIRBuilder_OverriddenAnnotation_FieldLevel_Provenance(t *testing.T) {
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
	// records OverriddenAnnotation.

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
	if prov.OverriddenAnnotation == nil {
		t.Fatal("OverriddenAnnotation should be populated with the suppressed annotation provenance")
	}
	if prov.OverriddenAnnotation.AnnotationKey != "feature.myco/db-pool" {
		t.Errorf("suppressed AnnotationKey = %q, want feature.myco/db-pool", prov.OverriddenAnnotation.AnnotationKey)
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

func TestIRBuilder_LeavesNearestScopeWins_SameOrigin_ProvenanceRefreshes(t *testing.T) {
	t.Parallel()
	// Two explicit contributions to the same leaf, from two different
	// authoring scopes: the NARROWER scope owns the leaf. The component
	// block is the narrower scope, so it refines the subsystem-wide
	// attachment even though the subsystem tier is folded in later — the
	// tier sequence is the mechanism, not the precedence rule. Per-leaf
	// provenance stays on the winning contribution (still explicit), and
	// same-origin precedence never records a suppressed annotation.
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
	if enabled != false {
		t.Errorf("enabled = %v, want false (the component-scope fill refines the subsystem-wide attachment)", enabled)
	}
	prov, _ := feat.ValuesProvenance.Get("enabled")
	if prov.Origin != "explicit" {
		t.Errorf("Origin = %q, want explicit", prov.Origin)
	}
	if prov.OverriddenAnnotation != nil {
		t.Error("same-origin precedence should not populate OverriddenAnnotation")
	}
}

// TestMergeContribution_NearestScopeWins_BothAttachmentFamilies is the guard
// against precedence acquiring a second home. One merge body decides
// precedence for both attachment families, so this drives a credential-
// labelled attachment AND a non-credential one through ONE graph and one
// collectNodeFeatures call: a test exercising only one family could not see
// the two paths diverge, which is the whole risk.
//
// Each family gets the same instance name at two scopes, and each
// contribution mentions one leaf the other does not, so the assertions pin
// both halves of the rule: the narrower scope wins the CONTESTED leaf, and
// the broader scope's UNCONTESTED leaf survives. The second half is what
// distinguishes a per-leaf refinement from a wholesale replace — a
// wholesale replace would drop the leaf the winning contribution omitted.
func TestMergeContribution_NearestScopeWins_BothAttachmentFamilies(t *testing.T) {
	t.Parallel()
	apiNode := &topology.TopologyNode{InstanceID: "api", SubsystemPath: "data"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{
					Type:   "core/monitoring",
					Values: valuesMap("contested", "component"),
				}},
				{InstanceName: "creds", Feature: types.FeatureInstance{
					Type:   "core/db-creds",
					Values: valuesMap("contested", "component"),
				}},
			},
		},
	}
	subsystem := types.Subsystem{}
	subsystem.Features.Set("monitoring", types.FeatureInstance{
		Type:   "core/monitoring",
		Values: valuesMap("contested", "shared", "shared_only", "kept"),
	})
	subsystem.Features.Set("creds", types.FeatureInstance{
		Type:   "core/db-creds",
		Values: valuesMap("contested", "shared", "shared_only", "kept"),
	})
	agg := &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}}
	agg.Spec.Subsystems.Set("data", subsystem)

	result, err := collectNodeFeatures("api", apiNode, topoResult, &resolver.ResolveResult{Aggregate: agg}, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both attachments land in the ONE bucket and are merged by the same body,
	// so nearest-scope-wins-per-leaf is proven over the folded key space rather
	// than once per former bucket.
	feat, ok := result.Features.Get("monitoring")
	if !ok {
		t.Fatal("non-credential attachment missing from the attachment bucket")
	}
	cred, ok := result.Features.Get("creds")
	if !ok {
		t.Fatal("credential-labelled attachment missing from the attachment bucket")
	}

	for _, tc := range []struct {
		family string
		values types.OrderedMap[any]
	}{
		{"non-credential", feat.Values},
		{"credential", cred.Values},
	} {
		if got, _ := tc.values.Get("contested"); got != "component" {
			t.Errorf("%s: contested leaf = %v, want \"component\" (the narrower scope wins)", tc.family, got)
		}
		if got, _ := tc.values.Get("shared_only"); got != "kept" {
			t.Errorf("%s: shared_only leaf = %v, want \"kept\" (an uncontested broader-scope leaf survives, so the merge is per leaf and not a wholesale replace)", tc.family, got)
		}
	}
}

// TestAttachmentScope_RuleOrder_AuthorityBeforeSpecificity pins the rule
// ORDER, which is the contract: composition authority is compared before
// specificity, so a composing document's broader declaration beats a
// composed document's narrower one. Reversing the two comparisons would let
// a composed sub-Aggregate defeat its composer by declaring at a narrower
// scope, and composition would stop being authoritative.
func TestAttachmentScope_RuleOrder_AuthorityBeforeSpecificity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b attachmentScope
		want bool
	}{
		{"composing shared beats composed component (authority before reach)", scopeShared, scopeComposedComponent, true},
		{"composed component does not beat composing shared", scopeComposedComponent, scopeShared, false},
		{"composing component beats composing shared (reach, same authority)", scopeComponent, scopeShared, true},
		{"composing shared does not beat composing component", scopeShared, scopeComponent, false},
		{"composing component beats composed component", scopeComponent, scopeComposedComponent, true},
		{"equal scopes do not outrank each other, so author order decides", scopeShared, scopeShared, false},
		{"equal component scopes do not outrank each other", scopeComponent, scopeComponent, false},
	}
	for _, tc := range tests {
		if got := tc.a.outranks(tc.b); got != tc.want {
			t.Errorf("%s: outranks = %v, want %v", tc.name, got, tc.want)
		}
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
// repo, no home, no keywords, no maintainers collapses to nil so the
// IR's CatalogMetadata pointer stays unset and the `omitempty` JSON
// tag suppresses the field entirely.
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
	if len(got.Keywords) != 2 || len(got.Maintainers) != 1 {
		t.Errorf("multi-shape fields not propagated: %+v", got)
	}
}

// TestCollectNodeFeatures_CredentialLabelSteersNothing is the negative proof for
// the label's retirement: two attachments identical except that one's catalog
// Feature carries feature.aggrega.ai/kind: secret produce entries that agree on
// every field the IR carries. The label survives on the catalog entity as opaque
// metadata; no branch in this package reads it.
func TestCollectNodeFeatures_CredentialLabelSteersNothing(t *testing.T) {
	t.Parallel()
	labelled := types.OrderedMap[string]{}
	labelled.Set(types.FeatureKindLabel, types.FeatureKindSecret)
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/plain":
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/plain", Version: "1.0.0"}}, true
			case "core/labelled":
				return types.Feature{Metadata: types.FeatureMetadata{Name: "core/labelled", Version: "1.0.0", Labels: labelled}}, true
			}
			return types.Feature{}, false
		},
	}
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	vals := valuesMap("k", "v")
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "plain", Feature: types.FeatureInstance{Type: "core/plain", Values: vals}},
				{InstanceName: "labelled", Feature: types.FeatureInstance{Type: "core/labelled", Values: vals}},
			},
		},
	}
	rr := &resolver.ResolveResult{Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "t"}}}
	result, err := collectNodeFeatures("api", apiNode, topoResult, rr, cl)
	if err != nil {
		t.Fatalf("collectNodeFeatures: %v", err)
	}
	plain, okP := result.Features.Get("plain")
	labelledEntry, okL := result.Features.Get("labelled")
	if !okP || !okL {
		t.Fatalf("both attachments must land in the one bucket; keys=%v", result.Features.Keys())
	}
	if plain.Origin != labelledEntry.Origin || plain.ResolvedVersion != labelledEntry.ResolvedVersion {
		t.Errorf("label steered a field: plain=%+v labelled=%+v", plain, labelledEntry)
	}
	if plain.ValuesProvenance.Len() != labelledEntry.ValuesProvenance.Len() {
		t.Errorf("label steered provenance recording: %d vs %d", plain.ValuesProvenance.Len(), labelledEntry.ValuesProvenance.Len())
	}
	if len(result.FeatureOrder) != 2 {
		t.Errorf("both attachments must appear in the one order vector; got %v", result.FeatureOrder)
	}
}
