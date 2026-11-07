// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestServeValidateVerifiesCacheAgainstPinBeforeTier4 is the validate-handler
// counterpart of TestServeIrGraphVerifiesCacheAgainstPinBeforePreview. It proves
// the Tier-4 validation path threads the active Release's lockfile pins before its
// resolution reads, so a cache-only Component whose cached content has DRIFTED
// from the lockfile pin is verified-on-read and SKIPPED — never re-fetched.
//
// With the per-request pins threaded before RunTier4Pipeline (the production
// ordering), the drifted slot fails the pin and is skipped: the cascade stops at
// the cache rather than running on past it, so validate cannot silently pass on a
// locally-drifted cache the build halts on. The spy puller is the probe that keeps
// this assertion non-vacuous — production wires none, so a reorder/removal of
// SetPins would otherwise fall through to nothing and leave no trace. Wired to
// SUCCEED, it records the call and fails the assertion.
//
// Reuses recordingPuller / contentDigestHex / writeFixtureFile from
// irgraph_verify_on_read_test.go (same package).
func TestServeValidateVerifiesCacheAgainstPinBeforeTier4(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := ".aggrega/cache"
	const compURI = "demo/server:1.0.0"

	pinnedComponent := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	cachedComponent := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
    drift.aggrega.ai/marker: "local-edit"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	if string(pinnedComponent) == string(cachedComponent) {
		t.Fatal("fixture error: pinned and cached content must differ to model drift")
	}

	// Cache-only Component: the entity lives only in the Tier-2 cache slot (drifted),
	// never authored in the catalog dir. The slot carries the persisted OCI manifest
	// committing to the ORIGINAL (pinned) content, so the drift is caught as an
	// integrity skip on read, not a fail-closed miss that would re-fetch via Tier-3.
	if err := oci.WriteCached(tmp, cacheDir, "Component", compURI, "1.0.0", cachedComponent); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	if err := oci.WriteCachedManifest(tmp, cacheDir, "Component", compURI, "1.0.0", componentManifestFor(t, pinnedComponent)); err != nil {
		t.Fatalf("WriteCachedManifest: %v", err)
	}

	// Catalog dir carries the Recipe + Provider; the Component itself is absent.
	catalogDir := filepath.Join(tmp, "catalog")
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/server
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)

	// Versioned type so the Component's resolution URI is the versioned form the
	// Tier-2 cache is keyed by.
	writeFixtureFile(t, filepath.Join(tmp, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: app-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        app: {}
`)
	aggBody := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/server:1.0.0"
`
	writeFixtureFile(t, filepath.Join(tmp, "app-agg.yaml"), aggBody)
	writeFixtureFile(t, filepath.Join(tmp, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`)

	releasePath := filepath.Join(tmp, "release.yaml")
	aggPath := filepath.Join(tmp, "app-agg.yaml")

	// Lockfile pins the component to the ORIGINAL digest; the drifted cache slot
	// does not match.
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{{
			Kind: "component", URI: compURI,
			ResolvedVersion: "1.0.0",
			Digest:          contentDigestHex(pinnedComponent),
		}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	// The puller is wired to SUCCEED — a drift skip must not reach it. A reorder of
	// the pin threading (pins unset at read time) runs the cascade past the cache
	// and into Tier-3, which would call this puller and fail the assertion below.
	puller := &recordingPuller{body: cachedComponent}

	srv := newTestServer(t, tmp, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
	srv.wsConfig.ArtifactCacheDir = cacheDir
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, cacheDir, []string{catalogDir}, puller)

	var in, out bytes.Buffer
	in.Write(framedRequest(7, "aggrega/validate", validateParams{
		File:    aggPath,
		Content: aggBody,
		Release: "release.yaml",
	}))
	resp := runOneRequest(t, srv, &in, &out)

	// The drift skip must NOT have reached Tier-3. This is the ordering guard: a
	// SetPins reorder/removal would surface here as a puller call.
	if len(puller.calls) != 0 {
		t.Fatalf("validate must verify-on-read and skip a drifted cache slot, never resolve past it; "+
			"puller was called with %v (the pin threading was reordered after the Tier-4 reads)", puller.calls)
	}

	// The handler must return a well-formed validate result (not an error envelope
	// and not a stray drift notification — the quiet pin load publishes nothing).
	if resp.Error != nil {
		t.Fatalf("validate returned an error envelope: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal validate result (a stray notification here would mean drift was published): %v", err)
	}
}
