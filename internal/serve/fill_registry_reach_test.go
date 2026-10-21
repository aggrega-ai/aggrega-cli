// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What a daemon request that fills dependencies does with the registry its
// workspace names, for the one read that leaves the machine when everything the
// Release reaches is held locally: the parent an extends chain names, which no
// local tier holds.
//
// Two properties, each read from the registry's side of the connection rather
// than from the answer, because an answer looks the same whether the request
// asked the registry the workspace names, asked another one, or stopped waiting
// on it:
//
//   - the read goes to the registry the workspace names — here one the test owns
//     — and not to the built-in default, which this package refuses outright;
//   - a request whose client withdraws ends that read: the registry sees the
//     client abandon it, rather than holding it open until the test lets go.

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fillStopCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// withdrawn request stops at all. A machine slow enough to exceed it is one on
// which the whole suite has already failed.
const fillStopCeiling = 60 * time.Second

// remoteParentIdentity is the parent the fixture's Component extends. No local
// tier holds it, so asking a registry for it is the one network read the fill
// makes.
const remoteParentIdentity = "demo/remote-base"

// extendingFillWorkspace is the filling fixture with its one Component made to
// extend a parent no local tier holds, and its configuration naming registryHost
// as the default registry.
func extendingFillWorkspace(t *testing.T, registryHost string) (ws, releaseRel string) {
	t.Helper()
	ws, releaseRel = renderDialWorkspace(t)
	writeFixtureFile(t, filepath.Join(ws, "catalog", "components", "svc.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: `+remoteParentIdentity+`
  schema: inherited
`)
	writeFixtureFile(t, filepath.Join(ws, ".aggrega-config.yaml"),
		"workspace:\n  catalog_dirs: [catalog]\n"+"default_registry: "+strconv.Quote(registryHost)+"\n")
	return ws, releaseRel
}

// TestADaemonFillAsksItsWorkspacesOwnRegistryForWhatNoLocalTierHolds is the
// positive half of this package's refusal of the built-in default registry: a
// fill whose workspace names a registry the test owns asks THAT registry for the
// parent nothing local holds.
//
// It is what makes the refusal falsifiable. Point this fixture's configuration
// back at the built-in default — drop its default_registry line — and the fill
// asks the default instead: the refusal turns the request away before it is
// dialled, this registry is never asked and this test fails, and the binary
// fails on the refused request it records.
func TestADaemonFillAsksItsWorkspacesOwnRegistryForWhatNoLocalTierHolds(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	host := loopbackRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		http.NotFound(w, r)
	})
	ws, releaseRel := extendingFillWorkspace(t, host)
	t.Chdir(ws)
	srv := newFillCapableServer(t, ws)

	// The outcome is not what this test is about — a parent nothing serves leaves
	// the chain unmerged — only where the fill went looking for it.
	_, _ = srv.handleResolveDependencies(t.Context(),
		resolveDependenciesParams{Release: releaseRel}, srv.progressFor(t.Context()))

	mu.Lock()
	defer mu.Unlock()
	if !slices.ContainsFunc(asked, func(p string) bool { return strings.Contains(p, remoteParentIdentity) }) {
		t.Errorf("the registry the workspace names was never asked for %s, the parent no local tier holds; "+
			"it was asked for %v", remoteParentIdentity, asked)
	}
}

// TestCancellingADaemonFillEndsTheRegistryReadItWaitsOn is the withdrawal proof
// for a read the walk's own context never covered: the parent of an extends chain,
// read by the lazy catalog view after the walk, from a registry that never
// answers.
//
// The registry holds every request open until either the client abandons it or
// the test ends. The request is withdrawn once the registry reports the read
// arrived, and then two things must happen: the registry sees the client abandon
// the read, and the request returns. The first is the clock-free half — a fill
// that read under a context of its own would leave the read open, and the
// registry would never see it abandoned. The second is bounded only by the
// liveness ceiling.
//
// A planted defect makes this fail: hand the extends chain's parent lookup a
// background context instead of the caller's, and the read stays open, the
// request never returns within the ceiling, and the registry never sees it
// abandoned.
func TestCancellingADaemonFillEndsTheRegistryReadItWaitsOn(t *testing.T) {
	entered := make(chan string, 1)
	abandoned := make(chan struct{})
	var abandonOnce sync.Once
	release := make(chan struct{})
	host := loopbackRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- r.URL.Path:
		default:
		}
		select {
		case <-r.Context().Done():
			abandonOnce.Do(func() { close(abandoned) })
		case <-release:
			http.NotFound(w, r)
		}
	})
	// Registered after the registry, so it runs first: the held reads are let go
	// before the registry's own cleanup waits for them to finish.
	t.Cleanup(func() { close(release) })

	ws, releaseRel := extendingFillWorkspace(t, host)
	t.Chdir(ws)
	srv := newFillCapableServer(t, ws)

	ctx, withdraw := context.WithCancel(t.Context())
	defer withdraw()
	done := make(chan *jsonRPCError, 1)
	go func() {
		_, errEnvelope := srv.handleResolveDependencies(ctx,
			resolveDependenciesParams{Release: releaseRel}, srv.progressFor(ctx))
		done <- errEnvelope
	}()

	select {
	case path := <-entered:
		if !strings.Contains(path, remoteParentIdentity) {
			t.Errorf("the first read the registry received was for %s; want the read of %s, the parent no "+
				"local tier holds", path, remoteParentIdentity)
		}
	case <-time.After(fillStopCeiling):
		t.Fatalf("the fill never reached the registry within the liveness ceiling, so there is no read to " +
			"withdraw from")
	}

	withdraw()

	select {
	case <-abandoned:
	case <-time.After(fillStopCeiling):
		t.Errorf("the registry never saw its read abandoned after the request was withdrawn: the read ran " +
			"under a context the withdrawal does not reach")
	}
	select {
	case errEnvelope := <-done:
		if errEnvelope == nil {
			t.Error("the withdrawn fill answered as a completed one")
		}
	case <-time.After(fillStopCeiling):
		t.Error("the withdrawn fill never returned within the liveness ceiling")
	}
}
