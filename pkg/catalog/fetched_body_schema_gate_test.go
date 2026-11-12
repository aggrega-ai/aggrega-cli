// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// schemaRefusedPuller serves, for a bare-identity fetch, a body that is a valid
// Component in every respect but one its own schema refuses — here a
// metadata.version that is not a version. It is the shape a registry an attacker
// controls serves under a valid tag.
type schemaRefusedPuller struct{ body []byte }

func (p *schemaRefusedPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound()
}

func (p *schemaRefusedPuller) PullEntity(_ context.Context, _, _, floorVersion string) (*oci.PullResult, string, error) {
	return &oci.PullResult{Bytes: p.body, MediaType: oci.MediaTypeComponent}, "1.0.0", nil
}

// TestTier3PullRefusesABodyItsSchemaRejectsAndWritesNothing holds the registry tier
// to putting a fetched body through its kind's schema before the cache write: a
// body the Component schema refuses halts the fetch under the envelope condition,
// and the cache directory the write would have populated does not exist afterwards.
func TestTier3PullRefusesABodyItsSchemaRejectsAndWritesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const cacheDir = ".aggrega/cache"
	// A Component whose metadata.version is a traversal — the exact route a hostile
	// registry takes. Its own schema requires a version there, so the gate refuses it.
	body := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: remote/db
  version: "../../../../../escape-target/db"
  labels:
    component.aggrega.ai/type: server
spec: {}
`)
	puller := &schemaRefusedPuller{body: body}

	_, _, err := tier3PullCatalogEntity(context.Background(), root, cacheDir, puller, "remote/db", "Component")
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Fatalf("tier3PullCatalogEntity: err = %v; want the envelope refusal for a body its schema rejects", err)
	}

	// Nothing was written: neither the slot the traversal version names, nor the
	// entities subtree the write would have created for it.
	if _, statErr := os.Stat(filepath.Join(root, "escape-target")); !os.IsNotExist(statErr) {
		t.Errorf("the refused fetch wrote to the escape target: stat err = %v", statErr)
	}
	if entries, _ := os.ReadDir(oci.EntitiesRoot(root, cacheDir)); len(entries) != 0 {
		t.Errorf("the refused fetch wrote %d entries into the entities cache; want none", len(entries))
	}
}

// TestTier3PullCachesABodyItsSchemaAccepts is the positive control: a well-formed
// Component of the same identity is cached, so the gate above refuses on the body's
// shape rather than on the path being unreachable. The bare reference is cached at
// the version it resolved to, under the key a read of its pin addresses.
func TestTier3PullCachesABodyItsSchemaAccepts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const cacheDir = ".aggrega/cache"
	body := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: remote/db
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`)
	puller := &schemaRefusedPuller{body: body}

	bytes, res, err := tier3PullCatalogEntity(context.Background(), root, cacheDir, puller, "remote/db", "Component")
	if err != nil {
		t.Fatalf("tier3PullCatalogEntity refused a well-formed Component: %v", err)
	}
	if res.ResolvedVersion != "1.0.0" || len(bytes) == 0 {
		t.Fatalf("resolution did not carry the fetched body: version=%q bytes=%d", res.ResolvedVersion, len(bytes))
	}
	slot, err := oci.CachePath(root, cacheDir, "Component", "remote/db:1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(slot, "blob")); statErr != nil {
		t.Errorf("the accepted fetch wrote no blob into its slot: %v", statErr)
	}
}
