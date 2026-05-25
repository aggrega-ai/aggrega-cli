// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// mockPuller is a stand-in for the production OCI puller. Records
// per-key call counts so tests can assert per-tier behavior without
// touching the network. The mediaTypeByURI map lets a test override
// the response media type per URI; entries default to
// oci.MediaTypeComponent so the downstream writeEntityToCacheSlot
// switch dispatches correctly without relying on the
// placeholder-default branch.
type mockPuller struct {
	bytesByURI     map[string][]byte
	mediaTypeByURI map[string]string
	pullCalls      map[string]int
}

func newMockPuller() *mockPuller {
	return &mockPuller{
		bytesByURI:     map[string][]byte{},
		mediaTypeByURI: map[string]string{},
		pullCalls:      map[string]int{},
	}
}

func (m *mockPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	key := reference + ":" + tag
	m.pullCalls[key]++
	bytes, ok := m.bytesByURI[key]
	if !ok {
		return nil, oci.ErrFetchNotFound
	}
	mt, ok := m.mediaTypeByURI[key]
	if !ok {
		mt = oci.MediaTypeComponent
	}
	return &oci.PullResult{Bytes: bytes, MediaType: mt}, nil
}

// fullstackComponentYAML returns the canonical Component fixture text
// used across the cascade tests. The metadata.name is "test/api-server"
// and the kind is "Component".
func fullstackComponentYAML() string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`
}

func TestLazyLookupComponentTier1HitFromCatalogDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	catalogDir := filepath.Join(tmp, "catalog")
	componentDir := filepath.Join(catalogDir, "component")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "api-server.yaml"), []byte(fullstackComponentYAML()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	puller := newMockPuller()
	lookup := catalog.NewLazyLookup(tmp, "", []string{catalogDir}, puller).AsCatalogLookup()
	comp, ok := lookup.Component("test/api-server")
	if !ok {
		t.Fatalf("Component(\"test/api-server\") not found at Tier-1")
	}
	if comp.Metadata.Name != "test/api-server" {
		t.Errorf("Metadata.Name = %q; want %q", comp.Metadata.Name, "test/api-server")
	}
	if len(puller.pullCalls) != 0 {
		t.Errorf("Tier-3 puller should not be invoked when Tier-1 hits; got %v", puller.pullCalls)
	}
}

// TestLazyLookupComponentTier1HitFromNonStrictLayout pins the Aggrega
// Invariant 1 contract: the lazy primitive is compiler infrastructure
// and stays layout-agnostic. A Component authored at a flat path
// (no kind-directory convention) MUST resolve identically to a
// strict-layout Component, because the index is built from authored
// metadata.name, not from filesystem path.
func TestLazyLookupComponentTier1HitFromNonStrictLayout(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	catalogDir := filepath.Join(tmp, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Flat path, no kind-directory, name unrelated to URI.
	if err := os.WriteFile(filepath.Join(catalogDir, "whatever.yaml"), []byte(fullstackComponentYAML()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	lookup := catalog.NewLazyLookup(tmp, "", []string{catalogDir}, newMockPuller()).AsCatalogLookup()
	comp, ok := lookup.Component("test/api-server")
	if !ok {
		t.Fatalf("Component(\"test/api-server\") not found at Tier-1 for non-strict layout — layout-agnostic invariant violated")
	}
	if comp.Metadata.Name != "test/api-server" {
		t.Errorf("Metadata.Name = %q; want %q", comp.Metadata.Name, "test/api-server")
	}
}

func TestLazyLookupComponentTier2HitFromCacheDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cacheDir := ".aggrega/cache"
	// Pre-populate the OCI cache slot for a versioned URI.
	uri := "test/cached-component:2.0.0"
	yamlBody := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/cached-component
  version: "2.0.0"
spec: {}
`
	if err := oci.WriteCached(tmp, cacheDir, uri, "2.0.0", []byte(yamlBody)); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	puller := newMockPuller()
	lookup := catalog.NewLazyLookup(tmp, cacheDir, nil, puller).AsCatalogLookup()
	comp, ok := lookup.Component(uri)
	if !ok {
		t.Fatalf("Component(%q) not found at Tier-2", uri)
	}
	if comp.Metadata.Name != "test/cached-component" {
		t.Errorf("Metadata.Name = %q; want %q", comp.Metadata.Name, "test/cached-component")
	}
	if len(puller.pullCalls) != 0 {
		t.Errorf("Tier-3 puller should not be invoked when Tier-2 hits; got %v", puller.pullCalls)
	}
}

func TestLazyLookupComponentTier3HitFromOCIPuller(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cacheDir := ".aggrega/cache"
	uri := "test/registry-component:3.0.0"
	yamlBody := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/registry-component
  version: "3.0.0"
spec: {}
`
	puller := newMockPuller()
	puller.bytesByURI[uri] = []byte(yamlBody)

	lookup := catalog.NewLazyLookup(tmp, cacheDir, nil, puller).AsCatalogLookup()
	comp, ok := lookup.Component(uri)
	if !ok {
		t.Fatalf("Component(%q) not found at Tier-3", uri)
	}
	if comp.Metadata.Name != "test/registry-component" {
		t.Errorf("Metadata.Name = %q; want %q", comp.Metadata.Name, "test/registry-component")
	}
	if puller.pullCalls[uri] != 1 {
		t.Errorf("Tier-3 puller should have been invoked once; got %v", puller.pullCalls)
	}
}

func TestLazyLookupExtendsRecursionMergesParent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	catalogDir := filepath.Join(tmp, "catalog")
	componentDir := filepath.Join(catalogDir, "component")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	parent := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/base
  version: "1.0.0"
  labels:
    inherited-key: parent-value
spec: {}
`
	child := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/derived
  version: "1.0.0"
  labels:
    child-key: child-value
spec:
  extends: test/base
`
	if err := os.WriteFile(filepath.Join(componentDir, "base.yaml"), []byte(parent), 0o644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "derived.yaml"), []byte(child), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	lookup := catalog.NewLazyLookup(tmp, "", []string{catalogDir}, newMockPuller()).AsCatalogLookup()
	comp, ok := lookup.Component("test/derived")
	if !ok {
		t.Fatalf("Component(\"test/derived\") not found")
	}
	inheritedValue, hasInherited := comp.Metadata.Labels.Get("inherited-key")
	if !hasInherited || inheritedValue != "parent-value" {
		t.Errorf("child should carry merged label inherited-key=parent-value via validator extends-recursion; got %v (present=%v)", inheritedValue, hasInherited)
	}
	childValue, hasChild := comp.Metadata.Labels.Get("child-key")
	if !hasChild || childValue != "child-value" {
		t.Errorf("child should retain its own label child-key=child-value; got %v (present=%v)", childValue, hasChild)
	}
}

func TestLazyLookupHotCacheAvoidsReResolution(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cacheDir := ".aggrega/cache"
	uri := "test/hotcache-component:1.0.0"
	yamlBody := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/hotcache-component
  version: "1.0.0"
spec: {}
`
	puller := newMockPuller()
	puller.bytesByURI[uri] = []byte(yamlBody)

	lookup := catalog.NewLazyLookup(tmp, cacheDir, nil, puller).AsCatalogLookup()
	if _, ok := lookup.Component(uri); !ok {
		t.Fatalf("first Component(%q) call missed", uri)
	}
	if _, ok := lookup.Component(uri); !ok {
		t.Fatalf("second Component(%q) call missed", uri)
	}
	if puller.pullCalls[uri] != 1 {
		t.Errorf("hot cache should have served the second call; mock Pull invoked %d times for %q", puller.pullCalls[uri], uri)
	}
}

func TestLazyLookupComponentCascadeExhaustedReturnsFalse(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	puller := newMockPuller() // no fixtures; Pull always returns ErrFetchNotFound
	lookup := catalog.NewLazyLookup(tmp, ".aggrega/cache", nil, puller).AsCatalogLookup()
	uri := "test/missing-component:1.0.0"
	if _, ok := lookup.Component(uri); ok {
		t.Fatalf("Component(%q) should miss but reported hit", uri)
	}
	callsAfterFirst := puller.pullCalls[uri]
	// Second call: negative cache should short-circuit the cascade.
	if _, ok := lookup.Component(uri); ok {
		t.Fatalf("Component(%q) second call should still miss", uri)
	}
	if puller.pullCalls[uri] != callsAfterFirst {
		t.Errorf("negative cache should have short-circuited the second call; mock Pull invoked %d times after second call (was %d after first)", puller.pullCalls[uri], callsAfterFirst)
	}
}

func TestLazyLookupFeatureTier1HitFromCatalogDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	catalogDir := filepath.Join(tmp, "catalog")
	featureDir := filepath.Join(catalogDir, "feature")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	featureYAML := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/health-check
  version: "1.0.0"
spec: {}
`
	if err := os.WriteFile(filepath.Join(featureDir, "health-check.yaml"), []byte(featureYAML), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	lookup := catalog.NewLazyLookup(tmp, "", []string{catalogDir}, newMockPuller()).AsCatalogLookup()
	feat, ok := lookup.Feature("test/health-check")
	if !ok {
		t.Fatalf("Feature(\"test/health-check\") not found")
	}
	if feat.Metadata.Name != "test/health-check" {
		t.Errorf("Metadata.Name = %q; want %q", feat.Metadata.Name, "test/health-check")
	}
}
