// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"oras.land/oras-go/v2/content/memory"
)

// componentManifestFor round-trips body through an in-memory CAS to obtain the
// OCI manifest bytes a real component pull persists — the manifest whose single
// layer commits to body's content digest. Seeded beside a drifted cache blob so
// verify-on-read catches the drift via manifest authority (an integrity skip)
// rather than treating the manifest-less slot as a fail-closed miss and issuing
// a Tier-3 re-fetch.
func componentManifestFor(t *testing.T, body []byte) []byte {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", oci.MediaTypeComponent, body); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	res, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	return res.ManifestBytes
}

// recordingPuller is a Tier-3 stand-in that records every Pull it receives and
// can serve a body. In the verify-on-read ordering test it is wired so that a
// Tier-3 fetch WOULD succeed — so if the cache-drift skip silently degrades to a
// re-fetch (the failure mode a SetPins reorder introduces), the puller is called
// and the test catches it.
type recordingPuller struct {
	body  []byte
	calls []string
}

func (p *recordingPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	p.calls = append(p.calls, reference+":"+tag)
	if p.body == nil {
		return nil, oci.ErrFetchNotFound
	}
	return &oci.PullResult{Bytes: p.body, MediaType: oci.MediaTypeComponent}, nil
}

func contentDigestHex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// drainFramedMessages reads every Content-Length-framed JSON-RPC message that
// the handler wrote to out. handleIrGraph emits a publishDiagnostics
// notification (drift) followed by the response, so the response is not
// necessarily the first message on the wire.
func drainFramedMessages(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	r := bufio.NewReader(out)
	var msgs []map[string]any
	for {
		body, err := readMessage(r)
		if err != nil {
			break
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("unmarshal framed message: %v (body: %s)", err, body)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// TestServeIrGraphVerifiesCacheAgainstPinBeforePreview is the end-to-end ordering
// guard for serve verify-on-read. It drives the real handleIrGraph against a
// workspace whose only copy of a referenced Component lives in the Tier-2 entity
// cache, with cache content that has DRIFTED from the lockfile pin.
//
// With the per-request pins threaded before the pipeline reads (the production
// ordering), the drifted slot is verified-on-read, fails the pin, and is SKIPPED:
// the entity is unavailable, the daemon returns a graph (resilient inspection,
// no crash), and Tier-3 is NOT consulted (no self-heal re-fetch that would
// preview content a local-cache-drifted build halts on).
//
// This is the regression guard the unit tests cannot provide: a unit test that
// constructs the lookup with pins already set cannot catch a handler reorder
// that moves SetPins after the reads. Here the puller is wired to SUCCEED, so a
// reorder (pins unset at read time -> no-pin -> Tier-3) would call the puller and
// fail the assertion below.
func TestServeIrGraphVerifiesCacheAgainstPinBeforePreview(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := ".aggrega/cache"
	const compURI = "demo/server:1.0.0"

	// pinnedComponent is the content the lockfile pins. cachedComponent is what
	// actually sits in the cache slot — a valid Component, but drifted (an extra
	// label), so its digest differs from the pin.
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

	// Cache-only Component: the entity lives ONLY in the Tier-2 cache slot (drifted
	// bytes), never authored in the catalog dir. The slot carries the persisted OCI
	// manifest committing to the ORIGINAL (pinned) content — the independent record
	// against which the drifted blob is caught as an integrity skip, not a miss.
	if err := oci.WriteCached(tmp, cacheDir, compURI, "1.0.0", cachedComponent); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	if err := oci.WriteCachedManifest(tmp, cacheDir, compURI, "1.0.0", componentManifestFor(t, pinnedComponent)); err != nil {
		t.Fatalf("WriteCachedManifest: %v", err)
	}

	// Catalog dir carries the Recipe + Provider so the recipe catalog loads and the
	// closure can resolve the recipe; the Component itself is deliberately absent.
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
	writeFixtureFile(t, filepath.Join(catalogDir, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  workloadSchema: |
    type: object
    additionalProperties: true
`)

	// The Component is referenced by a VERSIONED type so its resolution URI is
	// versioned (demo/server:1.0.0) — the form the Tier-2 cache is keyed by.
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
	writeFixtureFile(t, filepath.Join(tmp, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/server:1.0.0"
`)
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

	// Lockfile pins the component to the ORIGINAL content digest — which the
	// drifted cache slot does not match.
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{{
			URI:             compURI,
			ResolvedVersion: "1.0.0",
			Digest:          contentDigestHex(pinnedComponent),
		}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	// The puller is wired to SUCCEED — a drift skip must not reach it. A reorder of
	// SetPins (pins unset at read time) degrades to a no-pin Tier-3 fetch, which
	// would call this puller and fail the assertion.
	puller := &recordingPuller{body: cachedComponent}

	srv := newTestServer(t, tmp, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
	srv.wsConfig.ArtifactCacheDir = cacheDir
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, cacheDir, []string{catalogDir}, puller)

	var in, out bytes.Buffer
	in.Write(framedRequest(7, "aggrega/irGraph", irGraphParams{Release: "release.yaml"}))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)

	// The drift skip must NOT have reached Tier-3. This is the ordering guard: a
	// SetPins reorder would surface here as a puller call.
	if len(puller.calls) != 0 {
		t.Fatalf("drift must be skipped, never self-healed via Tier-3; puller was called with %v "+
			"(a SetPins reorder degrades verify-on-read to a no-pin Tier-3 re-fetch)", puller.calls)
	}

	// The daemon must stay up and return a graph response (resilient inspection):
	// an honestly-incomplete IR, not a crash and not a 1xxx error envelope.
	msgs := drainFramedMessages(t, &out)
	var sawResponse bool
	for _, m := range msgs {
		if _, hasID := m["id"]; !hasID {
			continue // notification (e.g. publishDiagnostics) — not the response
		}
		if errObj, ok := m["error"]; ok && errObj != nil {
			t.Fatalf("irGraph returned an error envelope instead of a resilient graph: %v", errObj)
		}
		result, ok := m["result"].(map[string]any)
		if !ok {
			t.Fatalf("response missing result object; got %v", m)
		}
		if _, ok := result["graph"]; !ok {
			t.Fatalf("response result missing graph field; got %v", result)
		}
		sawResponse = true
	}
	if !sawResponse {
		t.Fatalf("no irGraph response observed on the wire; messages=%v", msgs)
	}
}

// TestServeIrGraphResilientToSubAggregateUsesViewInconsistency closes the
// coverage hole the root-level drift test cannot reach: a drifted-out component
// referenced by a SUB-AGGREGATE node that carries a `uses` block. Sub-aggregate
// nodes are validated at Step 2 (topology), not Step 1 — through the same shared
// uses-socket validator — so their view-inconsistency surfaces as a Step-2 halt,
// on a different code path from the Step-1 guard tolerance.
//
// The component lives only in the Tier-2 cache, drifted from its pin, so it is
// skipped on read and absent from the lazy view. The sub-aggregate's node has a
// `uses` block, so the topology guard reaches the validator and the absent
// component would halt the build. Resilient inspection must still return the
// graph: the drift is a Problems-panel signal, not an inspection failure. A
// regression that tolerates only the Step-1 path (and not Step 2) fails here.
func TestServeIrGraphResilientToSubAggregateUsesViewInconsistency(t *testing.T) {
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

	// The drifted component lives ONLY in the Tier-2 cache, never in the catalog
	// dir — so the lazy view, verifying on read, skips it as drifted and the
	// guard sees it absent. The slot carries the persisted OCI manifest committing
	// to the ORIGINAL (pinned) content, so the drift is an integrity skip, not a
	// fail-closed miss.
	if err := oci.WriteCached(tmp, cacheDir, compURI, "1.0.0", cachedComponent); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	if err := oci.WriteCachedManifest(tmp, cacheDir, compURI, "1.0.0", componentManifestFor(t, pinnedComponent)); err != nil {
		t.Fatalf("WriteCachedManifest: %v", err)
	}

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
	writeFixtureFile(t, filepath.Join(catalogDir, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  workloadSchema: |
    type: object
    additionalProperties: true
`)

	// The sub-aggregate lives in the catalog dir (NOT drifted, so it loads and
	// flattens). Its `svc` node references the drifted component AND carries a
	// `uses` block, so topology's sub-aggregate guard reaches the uses-socket
	// validator with the component absent. `helper` is the local uses target.
	writeFixtureFile(t, filepath.Join(catalogDir, "aggregates", "mod.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
  version: "1.0.0"
spec:
  components:
    svc:
      type: "demo/server:1.0.0"
      uses:
        link:
          primary:
            target: helper
    helper:
      type: "demo/server:1.0.0"
`)

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
        mod.svc: {}
        mod.helper: {}
`)
	writeFixtureFile(t, filepath.Join(tmp, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  aggregates:
    mod:
      type: mod
`)
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

	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{{
			URI:             compURI,
			ResolvedVersion: "1.0.0",
			Digest:          contentDigestHex(pinnedComponent),
		}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	puller := &recordingPuller{body: cachedComponent}

	srv := newTestServer(t, tmp, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
	srv.wsConfig.ArtifactCacheDir = cacheDir
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, cacheDir, []string{catalogDir}, puller)

	var in, out bytes.Buffer
	in.Write(framedRequest(7, "aggrega/irGraph", irGraphParams{Release: "release.yaml"}))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)

	msgs := drainFramedMessages(t, &out)
	var sawResponse bool
	for _, m := range msgs {
		if _, hasID := m["id"]; !hasID {
			continue // notification (e.g. publishDiagnostics) — not the response
		}
		if errObj, ok := m["error"]; ok && errObj != nil {
			t.Fatalf("irGraph halted on a sub-aggregate uses view-inconsistency instead of "+
				"returning a resilient graph (Step-2 tolerance regression): %v", errObj)
		}
		result, ok := m["result"].(map[string]any)
		if !ok {
			t.Fatalf("response missing result object; got %v", m)
		}
		if _, ok := result["graph"]; !ok {
			t.Fatalf("response result missing graph field; got %v", result)
		}
		sawResponse = true
	}
	if !sawResponse {
		t.Fatalf("no irGraph response observed on the wire; messages=%v", msgs)
	}
}
