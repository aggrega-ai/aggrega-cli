// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// pinIsolationWorkspace lays down a workspace with TWO Releases (A, B) that each
// reference the SAME component name demo/svc but via DIFFERENT versioned URIs
// (:1.0.0 vs :2.0.0). Each version's bytes live ONLY in the Tier-2 cache, pinned
// by that Release's own lockfile, and the two versions have different schemas, so
// their rendered IR graphs differ. A request resolving Release A against Release
// B's pins (the shared-pin logical interleave) produces B's graph for A — the
// wrong-answer-under-interleaving the per-request view closes.
//
// Returns the workspace root and the two release paths.
func pinIsolationWorkspace(t *testing.T) (ws, releaseA, releaseB string) {
	t.Helper()
	ws = t.TempDir()
	const cacheDir = ".aggrega/cache"

	componentV1 := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	componentV2 := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "2.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
      port: { type: integer, required: true }
`)

	// Both versions live ONLY in the Tier-2 cache (each keyed by its versioned
	// slot), so resolution is decided entirely by the request's pins. Each slot
	// is laid complete, content and manifest, as a registry pull lays it: a slot
	// holding content but no manifest is an incomplete entry every reader misses.
	if err := oci.WriteCached(ws, cacheDir, "demo/svc:1.0.0", "1.0.0", componentV1); err != nil {
		t.Fatalf("WriteCached v1: %v", err)
	}
	if err := oci.WriteCachedManifest(ws, cacheDir, "demo/svc:1.0.0", "1.0.0", componentManifestFor(t, componentV1)); err != nil {
		t.Fatalf("WriteCachedManifest v1: %v", err)
	}
	if err := oci.WriteCached(ws, cacheDir, "demo/svc:2.0.0", "2.0.0", componentV2); err != nil {
		t.Fatalf("WriteCached v2: %v", err)
	}
	if err := oci.WriteCachedManifest(ws, cacheDir, "demo/svc:2.0.0", "2.0.0", componentManifestFor(t, componentV2)); err != nil {
		t.Fatalf("WriteCachedManifest v2: %v", err)
	}

	// A single recipe + provider in the catalog dir so the closure resolves and
	// the pipeline renders. The recipe implements demo/svc (version-agnostic).
	catalogDir := filepath.Join(ws, "catalog")
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
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

	manifest := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`
	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), manifest)

	// Two Release/Aggregate pairs, each referencing a different version of the
	// same component. Distinct names so closure compute treats them independently.
	makeRelease := func(name, version string) string {
		aggName := name + "-agg"
		writeFixtureFile(t, filepath.Join(ws, aggName+".yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: %s
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/svc:%s"
`, aggName, version))
		relPath := filepath.Join(ws, name+".yaml")
		writeFixtureFile(t, relPath, fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: %s
  environment: production
  version: "1.0.0"
spec:
  aggregate: %s
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        app: {}
`, name, aggName))
		// Lockfile pins the referenced version to its (matching) cache digest, so
		// verify-on-read accepts the slot for THIS Release's resolution.
		var digest string
		if version == "1.0.0" {
			digest = contentDigestHex(componentV1)
		} else {
			digest = contentDigestHex(componentV2)
		}
		lf := &lockfile.Lockfile{
			Version:     1,
			Release:     "./" + name + ".yaml",
			GeneratedAt: "2026-06-03T10:00:00Z",
			Entities: []lockfile.Entity{{
				URI:             "demo/svc:" + version,
				ResolvedVersion: version,
				Digest:          digest,
			}},
		}
		if err := lockfile.WriteLockfile(lockfile.PathFor(relPath), lf); err != nil {
			t.Fatalf("WriteLockfile %s: %v", name, err)
		}
		return relPath
	}
	releaseA = makeRelease("release-a", "1.0.0")
	releaseB = makeRelease("release-b", "2.0.0")
	return ws, releaseA, releaseB
}

func newPinIsolationServer(t *testing.T, ws string, in io.Reader, out io.Writer, poolSize int) *Server {
	t.Helper()
	srv := newPoolTestServer(t, in, out, poolSize, nil)
	srv.workspaceRoot = ws
	cacheDir := ".aggrega/cache"
	srv.wsConfig.ArtifactCacheDir = cacheDir
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(ws, "catalog")}
	srv.lazyCatalog = catalog.NewLazyLookup(ws, cacheDir, []string{filepath.Join(ws, "catalog")}, nil)
	reanchorTestWorkspace(t, srv)
	// driveHandle (the serial path) derives per-request contexts from serverCtx;
	// dispatchLoop initializes it on the concurrent path, but the serial path
	// bypasses that, so seed it here.
	srv.serverCtx, srv.serverCancel = context.WithCancel(context.Background())
	srv.cancelTracker = requestcancel.New()
	t.Cleanup(srv.serverCancel)
	return srv
}

// serialIrGraph drives ONE irGraph request to completion at pool=1 and returns
// the response's graph JSON — the authoritative result for that Release against
// its own pins.
func serialIrGraph(t *testing.T, ws, releaseRel string) json.RawMessage {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	in.Write(framedRequestIn(t, srv, 1, "aggrega/irGraph", irGraphParams{Release: releaseRel}))
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
	graph := extractGraph(t, &out)
	if graph == nil {
		t.Fatalf("serial irGraph for %s produced no graph", releaseRel)
	}
	return graph
}

// extractGraph drains the framed messages on out and returns the irGraph
// response's graph field (skipping notifications). nil when none found.
func extractGraph(t *testing.T, out *bytes.Buffer) json.RawMessage {
	t.Helper()
	r := bufio.NewReader(out)
	for {
		body, err := readMessage(r)
		if err != nil {
			return nil
		}
		var m struct {
			ID     *int `json:"id"`
			Result *struct {
				Graph json.RawMessage `json:"graph"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("unmarshal framed message: %v", err)
		}
		if m.ID == nil {
			continue // notification
		}
		if m.Error != nil {
			t.Fatalf("irGraph returned error envelope: %s", m.Error)
		}
		if m.Result != nil {
			return m.Result.Graph
		}
	}
}

// TestServeIrGraphPinIsolationUnderConcurrency is the load-bearing logical-
// consistency proof for per-request pin resolution. Two Releases referencing
// different versions of the same component (each pinned to its own cache slot)
// are hammered concurrently on a HIGH pool; every response must byte-match that
// Release's serial (pool=1) graph. A handler that resolved against another
// in-flight request's pins would emit the wrong Release's graph and fail the
// byte-match — the interleave the per-request view closes. The batch runs many
// rounds because the interleave is timing-dependent.
func TestServeIrGraphPinIsolationUnderConcurrency(t *testing.T) {
	t.Parallel()
	ws, releaseA, releaseB := pinIsolationWorkspace(t)
	relA := filepath.Base(releaseA)
	relB := filepath.Base(releaseB)

	wantA := serialIrGraph(t, ws, relA)
	wantB := serialIrGraph(t, ws, relB)
	if bytes.Equal(wantA, wantB) {
		t.Fatal("fixture error: the two Releases' serial graphs are identical; the test " +
			"cannot distinguish a cross-pin interleave")
	}

	for _, pool := range []int{4, 32} {
		pool := pool
		t.Run(fmt.Sprintf("pool=%d", pool), func(t *testing.T) {
			stdinR, stdinW := io.Pipe()
			// stdout is written by the serializer's worker goroutines and read by
			// the test only AFTER the dispatch loop returns (teardown flushes every
			// response before loopDone fires), so a mutex-guarded buffer keeps the
			// concurrent writes race-free while the deferred read sees the full batch.
			stdout := &syncBuffer{}
			srv := newPinIsolationServer(t, ws, stdinR, stdout, pool)

			loopDone := make(chan error, 1)
			go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

			const rounds = 80
			// Even ids -> Release A, odd ids -> Release B.
			var feed [][]byte
			feed = append(feed, framedRequestIn(t, srv, 1, "aggrega/initialize", initializeParams{}))
			for i := 0; i < rounds; i++ {
				idA := 100 + i*2
				idB := 101 + i*2
				feed = append(feed,
					framedRequestIn(t, srv, idA, "aggrega/irGraph", irGraphParams{Release: relA}),
					framedRequestIn(t, srv, idB, "aggrega/irGraph", irGraphParams{Release: relB}),
				)
			}
			// Interleave an invalidate to also exercise Reset racing the views.
			// Closing stdin (EOF) — not a shutdown notification — drains the pool
			// to completion (teardown waits for in-flight workers), so every
			// submitted irGraph response flushes and the per-id assertions below
			// see the full batch rather than a drain-dropped subset.
			feed = append(feed, framedRequestIn(t, srv, 99, "aggrega/invalidate", invalidateParams{Granularity: "workspace"}))

			go func() {
				for _, msg := range feed {
					if _, err := stdinW.Write(msg); err != nil {
						return
					}
				}
				stdinW.Close()
			}()

			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("dispatch loop did not exit")
			}

			seen := 0
			r := bufio.NewReader(bytes.NewReader(stdout.bytes()))
			for {
				body, err := readMessage(r)
				if err != nil {
					break
				}
				var m struct {
					ID     *int `json:"id"`
					Result *struct {
						Graph json.RawMessage `json:"graph"`
					} `json:"result"`
					Error json.RawMessage `json:"error"`
				}
				if err := json.Unmarshal(body, &m); err != nil {
					continue
				}
				if m.ID == nil || *m.ID < 100 {
					continue // notification / initialize / invalidate
				}
				if m.Error != nil {
					t.Fatalf("id=%d: irGraph returned an error envelope: %s", *m.ID, m.Error)
				}
				if m.Result == nil {
					continue
				}
				var want json.RawMessage
				var label string
				if *m.ID%2 == 0 {
					want, label = wantA, relA
				} else {
					want, label = wantB, relB
				}
				if !bytes.Equal(m.Result.Graph, want) {
					t.Fatalf("id=%d (%s): irGraph resolved against another request's pins — "+
						"response graph does not byte-match the serial result for %s\n got: %s\nwant: %s",
						*m.ID, label, label, m.Result.Graph, want)
				}
				seen++
			}
			if seen == 0 {
				t.Fatal("no irGraph responses observed; the test asserted nothing")
			}
		})
	}
}

// syncBuffer is a mutex-guarded bytes.Buffer: the dispatch loop's serializer
// writes responses from worker goroutines while the test reads the accumulated
// bytes after the loop returns. The lock keeps the concurrent writes race-free.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
