// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// bundledEntity returns the entity as a tar.gz with the document at the archive
// root, packed by the real bundler.
//
// The document names itself as carried, which is what makes the bundler pack an
// archive rather than hand back the document: a reader must open an archive
// whatever kind it holds, and this is how one helper produces that shape for
// every kind a reader below may meet, rather than a hand-rolled tar that could
// drift from what the bundler emits. The kind handed to Bundle picks only the
// media type the bundler reports, which these readers take from the puller.
func bundledEntity(t *testing.T, fileName, body string) []byte {
	t.Helper()
	dir := filepath.Join(t.TempDir(), entityVersionOf(t, body))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	blob, _, err := oci.Bundle("Recipe", path, []string{fileName})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if !oci.IsTarGzBundle(blob) {
		t.Fatal("the bundler did not produce an archive; the reads below would not be about anything")
	}
	return blob
}

func entityVersionOf(t *testing.T, body string) string {
	t.Helper()
	v, err := entityMetadataVersion([]byte(body))
	if err != nil {
		t.Fatalf("fixture does not read as an entity: %v", err)
	}
	if v == "" {
		t.Fatalf("fixture declares no metadata.version:\n%s", body)
	}
	return v
}

const bundledRecipeBody = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: service
  version: "1.0.0"
spec:
  capability: helm
  implements: example/compute/service
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
`

// bundledSchemaLibraryBody is a library its own schema admits, because the
// library tier puts a fetched body through that schema before it caches it.
const bundledSchemaLibraryBody = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: SharedSchemas
  version: "2.0.0"
spec:
  schemas:
    Port:
      type: integer
`

const bundledAggregateBody = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: db-module
  version: "3.0.0"
spec:
  components:
    child-db:
      type: core/datastore/postgres
  exportedComponents:
    db: child-db
`

// archivePuller serves one archive-shaped artifact on every capability the
// registry tier can bind, under an OCI tag deliberately unequal to the entity's
// own metadata.version — so a pin taken from the tag and a pin taken from the
// entity are distinguishable in the assertion.
type archivePuller struct {
	blob      []byte
	mediaType string
	tag       string
}

func (p *archivePuller) result() *oci.PullResult {
	return &oci.PullResult{Bytes: p.blob, MediaType: p.mediaType}
}

func (p *archivePuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return p.result(), nil
}

func (p *archivePuller) PullEntity(context.Context, string, string, string) (*oci.PullResult, string, error) {
	return p.result(), p.tag, nil
}

func (p *archivePuller) PullLibrary(context.Context, string, string, string) (*oci.PullResult, string, error) {
	return p.result(), p.tag, nil
}

func (p *archivePuller) PullAggregate(context.Context, string, string) (*oci.PullResult, string, error) {
	return p.result(), p.tag, nil
}

// mediaTypeFor returns the media type a registry declares for kind in the shape
// archive names. The library and Aggregate rows below declare their kind's
// document type over archive bytes on purpose: what is under test is a reader
// that discriminates on the bytes it was served rather than on what the manifest
// says they are.
func mediaTypeFor(t *testing.T, kind string, archive bool) string {
	t.Helper()
	mt, err := oci.MediaTypeFor(kind, archive)
	if err != nil {
		t.Fatalf("MediaTypeFor(%q, %v): %v", kind, archive, err)
	}
	return mt
}

// TestRegistryTierPinsTheEntityIdentityInsideAnArchive drives the three registry
// tiers that pin a fetched entity's identity. Each is given an artifact whose
// OCI tag says 9.9.9 and whose entity document says something else; the pin must
// be the document's, which is the identity the build side re-derives.
func TestRegistryTierPinsTheEntityIdentityInsideAnArchive(t *testing.T) {
	t.Run("entity tier", func(t *testing.T) {
		p := &archivePuller{
			blob:      bundledEntity(t, "recipe.yaml", bundledRecipeBody),
			mediaType: mediaTypeFor(t, "Recipe", true),
			tag:       "9.9.9",
		}
		_, res, err := tier3PullCatalogEntity(context.Background(), t.TempDir(), ".aggrega/cache", p, "helm/service", "Recipe")
		if err != nil {
			t.Fatalf("tier3PullCatalogEntity: %v", err)
		}
		if res.ResolvedVersion != "1.0.0" {
			t.Errorf("pinned %q; want the entity's own 1.0.0, not the OCI tag it was fetched under",
				res.ResolvedVersion)
		}
	})

	t.Run("library tier", func(t *testing.T) {
		p := &archivePuller{
			blob:      bundledEntity(t, "library.yaml", bundledSchemaLibraryBody),
			mediaType: mediaTypeFor(t, "SchemaLibrary", false),
			tag:       "9.9.9",
		}
		l := &catalogLookup{catalog: &Catalog{}, ociPuller: p, workspaceRoot: t.TempDir(), cacheDir: ".aggrega/cache"}
		res, err := l.tier3Library(context.Background(), "aggrega/shared-schemas", "SchemaLibrary")
		if err != nil {
			t.Fatalf("tier3Library: %v", err)
		}
		if res.ResolvedVersion != "2.0.0" {
			t.Errorf("pinned %q; want the entity's own 2.0.0, not the OCI tag it was fetched under",
				res.ResolvedVersion)
		}
	})

	t.Run("aggregate tier", func(t *testing.T) {
		p := &archivePuller{
			blob:      bundledEntity(t, "aggregate.yaml", bundledAggregateBody),
			mediaType: mediaTypeFor(t, "Aggregate", false),
			tag:       "9.9.9",
		}
		_, version, _, _, found, err := PullAndCacheAggregate(context.Background(), t.TempDir(), ".aggrega/cache", p, "acme/db-module")
		if err != nil {
			t.Fatalf("PullAndCacheAggregate: %v", err)
		}
		if !found {
			t.Fatal("PullAndCacheAggregate reported not-found for a served artifact")
		}
		if version != "3.0.0" {
			t.Errorf("pinned %q; want the entity's own 3.0.0, not the OCI tag it was fetched under", version)
		}
	})
}

// TestPullAndCacheAggregateHandsBackTheEntityDocument pins the second half of
// the aggregate tier: the bytes it returns are what the pull path parses as an
// Aggregate and what the lockfile's external-entity scan reads, so they must be
// the entity document rather than the artifact it arrived in. The digest it
// returns is separately asserted to still cover the WHOLE artifact, because that
// is what the pin commits to.
func TestPullAndCacheAggregateHandsBackTheEntityDocument(t *testing.T) {
	blob := bundledEntity(t, "aggregate.yaml", bundledAggregateBody)
	p := &archivePuller{blob: blob, mediaType: mediaTypeFor(t, "Aggregate", false), tag: "9.9.9"}

	data, _, digest, _, _, err := PullAndCacheAggregate(context.Background(), t.TempDir(), ".aggrega/cache", p, "acme/db-module")
	if err != nil {
		t.Fatalf("PullAndCacheAggregate: %v", err)
	}
	if oci.IsTarGzBundle(data) {
		t.Error("the returned bytes are still the published artifact; a caller parsing them meets the archive")
	}
	// A DELIBERATE second decode, recorded as such: routing this assertion through
	// the production helper would route it through the very unwrap under test, and
	// the check would pass over an archive. It must read the returned bytes as a
	// plain document or it is not checking anything.
	var hdr struct {
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(data, &hdr); err != nil {
		t.Fatalf("the returned bytes do not decode as a YAML document: %v", err)
	}
	if hdr.Metadata.Version != "3.0.0" {
		t.Errorf("the returned bytes read as metadata.version %q; want the entity document declaring 3.0.0",
			hdr.Metadata.Version)
	}
	if digest != contentDigest(blob) {
		t.Errorf("digest %s does not cover the whole published artifact; the pin must commit to the artifact, not to the document inside it", digest)
	}
}

// TestSubAggregateDescentReadsAnArchivedCacheSlot drives the closure's
// sub-aggregate loader against a cache slot holding an archive, through the
// verifying read it uses in production — the slot is written from a real
// push-then-pull so the persisted manifest the read demands is a real one.
func TestSubAggregateDescentReadsAnArchivedCacheSlot(t *testing.T) {
	ctx := context.Background()
	blob := bundledEntity(t, "aggregate.yaml", bundledAggregateBody)

	store := memory.New()
	if _, err := oci.Push(ctx, store, "3.0.0", mediaTypeFor(t, "Aggregate", false), blob, nil); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}
	pulled, err := oci.Pull(ctx, store, "3.0.0")
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}

	root := t.TempDir()
	const uri = "acme/db-module"
	if err := writeEntityToCacheSlot(root, ".aggrega/cache", uri+":3.0.0", "3.0.0", pulled); err != nil {
		t.Fatalf("writeEntityToCacheSlot: %v", err)
	}

	l := &catalogLookup{catalog: &Catalog{}, workspaceRoot: root, cacheDir: ".aggrega/cache"}
	agg, err := l.LoadAggregate(uri, "3.0.0", contentDigest(pulled.Bytes))
	if err != nil {
		t.Fatalf("LoadAggregate over an archived cache slot: %v", err)
	}
	if agg.Metadata.Name != "db-module" {
		t.Errorf("descent loaded %q; want db-module", agg.Metadata.Name)
	}
}
