// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// forRequestComponentV1 and forRequestComponentV2 are two versions of the SAME
// bare-named Component living in two distinct versioned Tier-2 cache slots. A
// view pinned to v1 must resolve v1's bytes; a view pinned to v2 must resolve
// v2's — proving the pin set, not shared session state, decides what a view sees.
const forRequestComponentV1 = `apiVersion: aggrega.ai/v1beta1
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
`

const forRequestComponentV2 = `apiVersion: aggrega.ai/v1beta1
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
`

// TestForRequest_ViewsResolveOwnPins is the pkg-level proof that ForRequest
// gives each request its OWN pin-dependent resolution: two views forked from one
// session catalog, pinned to two different versioned slots of the same Component
// name, each resolve their own slot's bytes — with the resolutions interleaved
// across goroutines and the escaped AsCatalogLookup closures invoked AFTER a peer
// view resolved with the other pin set. Under -race this fails if a view's
// resolution touches another's pins or caches.
func TestForRequest_ViewsResolveOwnPins(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const (
		v1Slot = "demo/svc:1.0.0"
		v2Slot = "demo/svc:2.0.0"
	)
	pinV1 := Pins{{Kind: "Component", URI: v1Slot}: writePinnedSlot(t, ws, cacheDir, v1Slot, oci.MediaTypeComponent, []byte(forRequestComponentV1))}
	pinV2 := Pins{{Kind: "Component", URI: v2Slot}: writePinnedSlot(t, ws, cacheDir, v2Slot, oci.MediaTypeComponent, []byte(forRequestComponentV2))}

	// Session catalog: no catalog dirs, nil puller — the components live only in
	// the pinned Tier-2 cache, so resolution is decided entirely by each view's
	// pin set. A view that leaked the other view's pins would resolve the wrong
	// version (or fail), which the per-version assertions below catch.
	session := NewLazyLookup(ws, cacheDir, nil, nil)

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make(chan error, workers*2)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			<-start
			if w%2 == 0 {
				v := session.ForRequest(pinV1)
				comp, ok := v.Component(v1Slot)
				if !ok || comp.Metadata.Version != "1.0.0" {
					errs <- errResolve("v1 view", v1Slot, comp.Metadata.Version, ok)
					return
				}
				// Invoke the escaped closure (the pipeline's call shape) after a
				// peer may have resolved with the other pin set.
				cl := v.AsCatalogLookup()
				if c2, ok2 := cl.Component(v1Slot); !ok2 || c2.Metadata.Version != "1.0.0" {
					errs <- errResolve("v1 closure", v1Slot, c2.Metadata.Version, ok2)
				}
			} else {
				v := session.ForRequest(pinV2)
				comp, ok := v.Component(v2Slot)
				if !ok || comp.Metadata.Version != "2.0.0" {
					errs <- errResolve("v2 view", v2Slot, comp.Metadata.Version, ok)
					return
				}
				cl := v.AsCatalogLookup()
				if c2, ok2 := cl.Component(v2Slot); !ok2 || c2.Metadata.Version != "2.0.0" {
					errs <- errResolve("v2 closure", v2Slot, c2.Metadata.Version, ok2)
				}
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ForRequest fan-out did not complete; likely a deadlock")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func errResolve(label, uri, gotVersion string, ok bool) error {
	return &resolveErr{label: label, uri: uri, gotVersion: gotVersion, ok: ok}
}

type resolveErr struct {
	label, uri, gotVersion string
	ok                     bool
}

func (e *resolveErr) Error() string {
	return e.label + ": resolving " + e.uri + " got version=" + e.gotVersion + " ok=" + boolStr(e.ok) +
		" — a view resolved against another request's pins"
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestForRequest_ResetRacingForRequest pins the index-sharing concurrency design
// point: a session catalog's Reset (workspace invalidation) racing a concurrent
// ForRequest that snapshots the session Tier-1 indexes. ForRequest copies the
// index maps under the session mutex, and Reset rewrites them under the same
// mutex, so an in-flight view's snapshot is never torn by a reset. Under -race
// this fails if ForRequest aliases a map Reset rewrites.
func TestForRequest_ResetRacingForRequest(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const v1Slot = "demo/svc:1.0.0"
	pinV1 := Pins{{Kind: "Component", URI: v1Slot}: writePinnedSlot(t, ws, cacheDir, v1Slot, oci.MediaTypeComponent, []byte(forRequestComponentV1))}

	session := NewLazyLookup(ws, cacheDir, nil, nil)

	const workers = 24
	var wg sync.WaitGroup
	wg.Add(workers)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 30; i++ {
				if (w+i)%3 == 0 {
					session.Reset()
					continue
				}
				v := session.ForRequest(pinV1)
				v.Component(v1Slot)
				v.AsCatalogLookup().Component(v1Slot)
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Reset/ForRequest race did not complete; likely a deadlock")
	}
}
