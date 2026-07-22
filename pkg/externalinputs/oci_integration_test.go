// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// External-input oci-scheme tests against an in-process real HTTP OCI
// registry (go-containerregistry's registry.New wrapped in httptest.Server,
// on 127.0.0.1 loopback so the OCI client speaks plain HTTP). Build-tagged
// `integration` like the other real-registry suites so the fast unit run
// excludes them.

package externalinputs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// startCountingRegistry stands up an in-process real OCI registry and returns
// its loopback host:port plus a counter of every HTTP request that reaches
// it — the network-silence probe for the pinned-wins assertions.
func startCountingRegistry(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	inner := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &calls
}

// TestOCIAcquirePinsByManifestDigest proves the oci:// acquisition channel:
// the artifact is pulled from the registry, pinned by its manifest digest,
// the payload blob lands in the store under that digest, a record is
// produced, and a second fetch is served from the store with zero registry
// calls.
func TestOCIAcquirePinsByManifestDigest(t *testing.T) {
	t.Parallel()
	host, calls := startCountingRegistry(t)
	ctx := context.Background()

	payload := []byte("chart archive payload bytes")
	repoRef := host + "/inputs/demo"
	repo, err := oci.NewRemoteRepository(repoRef, &http.Client{})
	if err != nil {
		t.Fatalf("remote repository: %v", err)
	}
	manifestDigest, err := oci.Push(ctx, repo, "1.0.0", "application/octet-stream", payload, nil)
	if err != nil {
		t.Fatalf("push fixture artifact: %v", err)
	}
	wantHex := strings.TrimPrefix(manifestDigest, "sha256:")

	root := t.TempDir()
	svc := externalinputs.NewService(root, true, &http.Client{}, nil)
	inputURL := "oci://" + repoRef + ":1.0.0"

	got, err := svc.Fetch(ctx, inputURL, "recipe.helm.demo")
	if err != nil {
		t.Fatalf("oci acquire: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("oci acquire returned wrong bytes: %q", got)
	}

	blob, err := os.ReadFile(filepath.Join(root, "sha256", wantHex))
	if err != nil {
		t.Fatalf("store blob missing under the manifest digest: %v", err)
	}
	if string(blob) != string(payload) {
		t.Errorf("store blob content differs from payload")
	}

	recs := svc.AcquiredRecords()
	if len(recs) != 1 {
		t.Fatalf("want 1 acquired record, got %+v", recs)
	}
	rec := recs[0]
	if rec.URL != inputURL || rec.Sha256 != wantHex || rec.Size != int64(len(payload)) {
		t.Errorf("record should pin the manifest digest: %+v (want sha256 %s)", rec, wantHex)
	}
	if len(rec.RequiredBy) != 1 || rec.RequiredBy[0] != "recipe.helm.demo" {
		t.Errorf("record provenance wrong: %+v", rec.RequiredBy)
	}

	// Pinned-wins: the second fetch is served from the store.
	calls.Store(0)
	got2, err := svc.Fetch(ctx, inputURL, "recipe.helm.other")
	if err != nil {
		t.Fatalf("second oci fetch: %v", err)
	}
	if string(got2) != string(payload) {
		t.Errorf("second oci fetch returned wrong bytes: %q", got2)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("pinned oci fetch must not touch the registry; it saw %d calls", n)
	}
}

// TestOCIRefillPullsByPinnedDigest proves the manifest-digest pin survives a
// moved tag: with the store blob missing and the tag re-pushed to different
// content, the refill pulls by the recorded digest and returns the original
// payload — the recorded digest always wins, even in frozen mode.
func TestOCIRefillPullsByPinnedDigest(t *testing.T) {
	t.Parallel()
	host, _ := startCountingRegistry(t)
	ctx := context.Background()

	payload := []byte("original chart payload")
	repoRef := host + "/inputs/moving"
	repo, err := oci.NewRemoteRepository(repoRef, &http.Client{})
	if err != nil {
		t.Fatalf("remote repository: %v", err)
	}
	manifestDigest, err := oci.Push(ctx, repo, "1.0.0", "application/octet-stream", payload, nil)
	if err != nil {
		t.Fatalf("push original: %v", err)
	}
	wantHex := strings.TrimPrefix(manifestDigest, "sha256:")

	// Move the tag to different content; the pinned digest must be immune.
	if _, err := oci.Push(ctx, repo, "1.0.0", "application/octet-stream", []byte("drifted content"), nil); err != nil {
		t.Fatalf("re-push tag: %v", err)
	}

	inputURL := "oci://" + repoRef + ":1.0.0"
	pinned := []lockfile.ExternalInput{{
		RequiredBy: []string{"recipe.helm.demo"},
		Sha256:     wantHex,
		Size:       int64(len(payload)),
		URL:        inputURL,
	}}
	root := t.TempDir()
	svc := externalinputs.NewService(root, false, &http.Client{}, pinned)

	got, err := svc.Fetch(ctx, inputURL, "recipe.helm.demo")
	if err != nil {
		t.Fatalf("oci refill: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("refill must serve the pinned content, not the moved tag: %q", got)
	}
	blob, err := os.ReadFile(filepath.Join(root, "sha256", wantHex))
	if err != nil {
		t.Fatalf("blob not rewritten after oci refill: %v", err)
	}
	if string(blob) != string(payload) {
		t.Errorf("rewritten blob content differs from pinned payload")
	}
}
