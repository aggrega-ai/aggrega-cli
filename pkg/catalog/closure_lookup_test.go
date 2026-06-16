// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// orasPullerAdapter wraps an in-memory oras Target so it satisfies
// catalog.OCIPuller. Production wires this through a remote.Repository;
// tests use the same shape over an in-memory CAS.
type orasPullerAdapter struct{ target *memory.Store }

func (a *orasPullerAdapter) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	return oci.Pull(ctx, a.target, tag)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeYAMLToCatalog(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestClosureLookupResolvesEntitiesFromCatalog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	compBody := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`
	writeYAMLToCatalog(t, dir, "component/core/api-server/api-server.yaml", compBody)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")

	res, err := lookup.ResolveCatalogEntity("api-server:1.0.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity: %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-1 hit; got NotFound")
	}
	if res.ResolvedVersion != "1.0.0" {
		t.Errorf("ResolvedVersion: got %q; want %q", res.ResolvedVersion, "1.0.0")
	}
	if res.Digest != sha256Hex([]byte(compBody)) {
		t.Errorf("Digest: got %q; want sha256(content)", res.Digest)
	}
	if !strings.Contains(res.ResolutionPath, "tier-1") {
		t.Errorf("ResolutionPath: got %q; want tier-1", res.ResolutionPath)
	}
}

// TestClosureLookupRoutesRecipeURIToMetadataName pins that
// ResolveCatalogEntity finds Recipe entries via their metadata.name
// (the form Manifest mappings pass to the closure walker as recipe
// URIs) — NOT via spec.implements. The Recipe's metadata.name and
// spec.implements are deliberately distinct in this fixture to catch
// any regression that re-introduces spec.implements-keyed Tier-1
// lookups, which would silently miss every Recipe URI the closure
// walker actually produces.
func TestClosureLookupRoutesRecipeURIToMetadataName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recipeBody := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: 2.5.0
spec:
  provider: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      app:
        image: x:1
        ports:
          web: 80
    services:
      svc:
        type: ClusterIP
        ports:
          web: 80
`
	writeYAMLToCatalog(t, dir, "recipes/server.yaml", recipeBody)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")

	// metadata.name form — what Manifest mappings produce. MUST resolve.
	res, err := lookup.ResolveCatalogEntity("server-recipe:2.5.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity(metadata.name URI): %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-1 hit on Recipe metadata.name URI; got NotFound")
	}
	if res.ResolvedVersion != "2.5.0" {
		t.Errorf("ResolvedVersion: got %q; want %q", res.ResolvedVersion, "2.5.0")
	}
	if res.Digest != sha256Hex([]byte(recipeBody)) {
		t.Errorf("Digest: got %q; want sha256(recipe body)", res.Digest)
	}

	// spec.implements form — the COMPONENT-type URI, not a Recipe URI.
	// The closure walker never asks ResolveCatalogEntity for this
	// against the Recipe; it asks for the Component (which is absent
	// in this fixture). Returns NotFound so the walker can surface
	// the missing-Component diagnostic correctly.
	res, err = lookup.ResolveCatalogEntity("core/server:2.5.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity(spec.implements URI): %v", err)
	}
	if !res.NotFound {
		t.Errorf("spec.implements URI should NOT resolve via Recipe lookup; got %+v", res)
	}
}

func TestClosureLookupFallsBackToOCIPullerOnCacheMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	compBody := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: api-server\n  version: 2.0.0\n")

	store := memory.New()
	if _, err := oci.Push(ctx, store, "2.0.0", oci.MediaTypeComponent, compBody); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}

	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")

	res, err := lookup.ResolveCatalogEntity("hub.aggrega.ai/component/core/api-server:2.0.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity: %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-3 hit; got NotFound")
	}
	if res.Digest != sha256Hex(compBody) {
		t.Errorf("Digest: got %q; want sha256(content)", res.Digest)
	}
	if res.ResolvedVersion != "2.0.0" {
		t.Errorf("ResolvedVersion: got %q; want %q", res.ResolvedVersion, "2.0.0")
	}

	// Verify cache slot was populated as <slot>/blob for non-library mediatypes.
	slot := oci.CachePath(dir, ".aggrega/cache", "hub.aggrega.ai/component/core/api-server:2.0.0", "2.0.0")
	if _, err := os.Stat(filepath.Join(slot, "blob")); err != nil {
		t.Errorf("expected cache slot /blob to exist; got %v", err)
	}
}

func TestClosureLookupNilPullerReturnsNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")
	res, err := lookup.ResolveCatalogEntity("missing/component:9.9.9", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity: %v", err)
	}
	if !res.NotFound {
		t.Errorf("expected NotFound with nil puller; got %+v", res)
	}
}

// pulled SchemaLibrary
// artifacts land at <slot>/library.yaml so the catalog walker's
// canonical per-version layout (matching the local-authoring shape)
// is preserved on the pull side.
func TestPullWritesSchemaLibraryAsLibraryYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	libBody := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Probes
  version: 1.0.0
spec:
  schemas:
    LivenessProbe:
      type: object
`)
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeSchemaLibrary, libBody); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}

	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")
	res, err := lookup.ResolveSchemaLibrary("hub.aggrega.ai/schemalibrary/core/Probes:1.0.0")
	if err != nil {
		t.Fatalf("ResolveSchemaLibrary: %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-3 hit; got NotFound")
	}

	slot := oci.CachePath(dir, ".aggrega/cache", "hub.aggrega.ai/schemalibrary/core/Probes:1.0.0", "1.0.0")
	libYAML := filepath.Join(slot, "library.yaml")
	got, err := os.ReadFile(libYAML)
	if err != nil {
		t.Fatalf("expected cache slot /library.yaml: %v", err)
	}
	if string(got) != string(libBody) {
		t.Errorf("library.yaml content mismatch:\n got %q\n want %q", got, libBody)
	}

	// No blob filename in the slot (library kinds use library.yaml exclusively).
	if _, err := os.Stat(filepath.Join(slot, "blob")); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no /blob filename for library slot; got err=%v", err)
	}
}

func TestPullWritesMappingLibraryAsLibraryYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	libBody := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
`)
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeMappingLibrary, libBody); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}

	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")
	if _, err := lookup.ResolveMappingLibrary("hub.aggrega.ai/mappinglibrary/core/BaseMappings:1.0.0"); err != nil {
		t.Fatalf("ResolveMappingLibrary: %v", err)
	}

	slot := oci.CachePath(dir, ".aggrega/cache", "hub.aggrega.ai/mappinglibrary/core/BaseMappings:1.0.0", "1.0.0")
	got, err := os.ReadFile(filepath.Join(slot, "library.yaml"))
	if err != nil {
		t.Fatalf("expected cache slot /library.yaml: %v", err)
	}
	if string(got) != string(libBody) {
		t.Errorf("library.yaml content mismatch")
	}
}
