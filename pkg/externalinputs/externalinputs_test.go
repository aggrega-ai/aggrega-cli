// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package externalinputs_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// countingServer serves body on every request over loopback HTTP and counts
// how many requests reach it — the network-silence probe for the hermetic
// tests.
func countingServer(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func hexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// seedBlob writes a store blob directly under root/sha256/<hexDigest>,
// simulating a store populated by a prior acquisition.
func seedBlob(t *testing.T, root, hexDigest string, b []byte) {
	t.Helper()
	dir := filepath.Join(root, "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, hexDigest), b, 0o644); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
}

// TestHermeticMiss proves the frozen-closure guarantee: an unpinned URL with
// acquisition disabled halts loudly, names the URL and the remedy, and never
// touches the network.
func TestHermeticMiss(t *testing.T) {
	t.Parallel()
	srv, calls := countingServer(t, []byte("never served"))
	svc := externalinputs.NewService(t.TempDir(), false, &http.Client{}, nil)

	inputURL := srv.URL + "/input.yaml"
	_, err := svc.Fetch(context.Background(), inputURL, "recipe.helm.demo")
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled) {
		t.Fatalf("want ErrAcquisitionDisabled, got %v", err)
	}
	if !strings.Contains(err.Error(), inputURL) {
		t.Errorf("error should name the URL, got: %v", err)
	}
	if !strings.Contains(err.Error(), "re-run with acquisition enabled") ||
		!strings.Contains(err.Error(), "acquisition pass") {
		t.Errorf("error should name the remedy, got: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("frozen miss must not touch the network; server saw %d calls", got)
	}
}

// TestAcquirePins proves acquisition: an unpinned URL in acquire mode is
// fetched, its bytes land in the store under their sha256, and the record
// carries url, sha256, size, and provenance.
func TestAcquirePins(t *testing.T) {
	t.Parallel()
	body := []byte("acquired payload bytes")
	srv, calls := countingServer(t, body)
	root := t.TempDir()
	svc := externalinputs.NewService(root, true, &http.Client{}, nil)

	inputURL := srv.URL + "/input.yaml"
	got, err := svc.Fetch(context.Background(), inputURL, "recipe.helm.demo")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("acquire returned wrong bytes: %q", got)
	}
	if calls.Load() != 1 {
		t.Errorf("acquire should fetch exactly once, server saw %d calls", calls.Load())
	}

	wantHex := hexOf(body)
	blob, err := os.ReadFile(filepath.Join(root, "sha256", wantHex))
	if err != nil {
		t.Fatalf("store blob missing after acquisition: %v", err)
	}
	if string(blob) != string(body) {
		t.Errorf("store blob content differs from payload")
	}

	recs := svc.AcquiredRecords()
	if len(recs) != 1 {
		t.Fatalf("want 1 acquired record, got %+v", recs)
	}
	rec := recs[0]
	if rec.URL != inputURL || rec.Sha256 != wantHex || rec.Size != int64(len(body)) {
		t.Errorf("acquired record wrong: %+v", rec)
	}
	if len(rec.RequiredBy) != 1 || rec.RequiredBy[0] != "recipe.helm.demo" {
		t.Errorf("acquired record provenance wrong: %+v", rec.RequiredBy)
	}
}

// TestAcquiredRecordsSortedByURL proves AcquiredRecords emits URL-sorted
// records regardless of acquisition order.
func TestAcquiredRecordsSortedByURL(t *testing.T) {
	t.Parallel()
	body := []byte("payload")
	srv, _ := countingServer(t, body)
	svc := externalinputs.NewService(t.TempDir(), true, &http.Client{}, nil)

	ctx := context.Background()
	urlB := srv.URL + "/b.yaml"
	urlA := srv.URL + "/a.yaml"
	if _, err := svc.Fetch(ctx, urlB, "x"); err != nil {
		t.Fatalf("acquire b: %v", err)
	}
	if _, err := svc.Fetch(ctx, urlA, "x"); err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	recs := svc.AcquiredRecords()
	if len(recs) != 2 || recs[0].URL != urlA || recs[1].URL != urlB {
		t.Errorf("AcquiredRecords not URL-sorted: %+v", recs)
	}
}

// TestPinnedWins proves the pin discipline: a pinned URL with its blob
// present is served from the store with zero network calls, and a second
// fetch in acquire mode still makes zero calls — a pinned entry never
// re-pins or silently re-fetches. Provenance accumulates on the record.
func TestPinnedWins(t *testing.T) {
	t.Parallel()
	body := []byte("pinned payload")
	srv, calls := countingServer(t, []byte("drifted remote content"))
	root := t.TempDir()
	wantHex := hexOf(body)
	seedBlob(t, root, wantHex, body)

	inputURL := srv.URL + "/pinned.yaml"
	pinned := []lockfile.ExternalInput{{
		RequiredBy: []string{"recipe.helm.original"},
		Sha256:     wantHex,
		Size:       int64(len(body)),
		URL:        inputURL,
	}}
	svc := externalinputs.NewService(root, true, &http.Client{}, pinned)

	ctx := context.Background()
	got, err := svc.Fetch(ctx, inputURL, "recipe.helm.second")
	if err != nil {
		t.Fatalf("pinned fetch: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("pinned fetch returned wrong bytes: %q", got)
	}
	got2, err := svc.Fetch(ctx, inputURL, "recipe.helm.second")
	if err != nil {
		t.Fatalf("second pinned fetch: %v", err)
	}
	if string(got2) != string(body) {
		t.Errorf("second pinned fetch returned wrong bytes: %q", got2)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("pinned fetches must not touch the network; server saw %d calls", n)
	}
	if recs := svc.AcquiredRecords(); len(recs) != 0 {
		t.Errorf("pinned fetches must not produce acquired records: %+v", recs)
	}

	all := svc.Records()
	if len(all) != 1 {
		t.Fatalf("want 1 record, got %+v", all)
	}
	rb := all[0].RequiredBy
	if len(rb) != 2 || rb[0] != "recipe.helm.original" || rb[1] != "recipe.helm.second" {
		t.Errorf("provenance should accumulate the new site once: %v", rb)
	}
}

// TestRefillVerifies proves the cache-refill path: a pinned URL with a
// missing blob is refetched once (even in frozen mode), verified against the
// recorded digest, and the blob rewritten.
func TestRefillVerifies(t *testing.T) {
	t.Parallel()
	body := []byte("refillable payload")
	srv, calls := countingServer(t, body)
	root := t.TempDir()
	wantHex := hexOf(body)

	inputURL := srv.URL + "/refill.yaml"
	pinned := []lockfile.ExternalInput{{
		RequiredBy: []string{"recipe.helm.demo"},
		Sha256:     wantHex,
		Size:       int64(len(body)),
		URL:        inputURL,
	}}
	// Frozen mode: a refill is a cache fill gated by the recorded digest, so
	// it runs even with acquisition disabled.
	svc := externalinputs.NewService(root, false, &http.Client{}, pinned)

	got, err := svc.Fetch(context.Background(), inputURL, "recipe.helm.demo")
	if err != nil {
		t.Fatalf("refill fetch: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("refill returned wrong bytes: %q", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("refill should fetch exactly once, server saw %d calls", n)
	}
	blob, err := os.ReadFile(filepath.Join(root, "sha256", wantHex))
	if err != nil {
		t.Fatalf("blob not rewritten after refill: %v", err)
	}
	if string(blob) != string(body) {
		t.Errorf("rewritten blob content differs from payload")
	}
}

// TestRefillMismatchHalts proves the negative refill path: when the server
// returns bytes that do not hash to the recorded digest, the fetch halts
// loudly naming the URL and both digests, and nothing is written to the
// store.
func TestRefillMismatchHalts(t *testing.T) {
	t.Parallel()
	original := []byte("original pinned payload")
	drifted := []byte("drifted server payload")
	srv, _ := countingServer(t, drifted)
	root := t.TempDir()
	wantHex := hexOf(original)

	inputURL := srv.URL + "/drift.yaml"
	pinned := []lockfile.ExternalInput{{
		RequiredBy: []string{"recipe.helm.demo"},
		Sha256:     wantHex,
		Size:       int64(len(original)),
		URL:        inputURL,
	}}
	svc := externalinputs.NewService(root, true, &http.Client{}, pinned)

	_, err := svc.Fetch(context.Background(), inputURL, "recipe.helm.demo")
	if !errors.Is(err, externalinputs.ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, inputURL) {
		t.Errorf("error should name the URL: %v", err)
	}
	if !strings.Contains(msg, wantHex) || !strings.Contains(msg, hexOf(drifted)) {
		t.Errorf("error should name both the pinned and the fetched digest: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "sha256", wantHex)); !os.IsNotExist(statErr) {
		t.Errorf("mismatching refill must not write a blob; stat: %v", statErr)
	}
}

// TestTamperedStore proves the every-read verification: a store blob whose
// bytes no longer hash to the recorded digest is a loud error naming the
// expected and actual digests.
func TestTamperedStore(t *testing.T) {
	t.Parallel()
	body := []byte("authentic payload")
	tampered := []byte("tampered payload!!")
	srv, calls := countingServer(t, body)
	root := t.TempDir()
	wantHex := hexOf(body)
	seedBlob(t, root, wantHex, tampered)

	inputURL := srv.URL + "/tampered.yaml"
	pinned := []lockfile.ExternalInput{{
		RequiredBy: []string{"recipe.helm.demo"},
		Sha256:     wantHex,
		Size:       int64(len(body)),
		URL:        inputURL,
	}}
	svc := externalinputs.NewService(root, true, &http.Client{}, pinned)

	_, err := svc.Fetch(context.Background(), inputURL, "recipe.helm.demo")
	if !errors.Is(err, externalinputs.ErrStoreCorrupt) {
		t.Fatalf("want ErrStoreCorrupt, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, inputURL) {
		t.Errorf("error should name the URL: %v", err)
	}
	if !strings.Contains(msg, wantHex) || !strings.Contains(msg, hexOf(tampered)) {
		t.Errorf("error should name both the pinned and the observed digest: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("tampered-store detection must not silently refetch; server saw %d calls", calls.Load())
	}
}

// TestSchemeRejection proves the allowlist: schemes outside {https,
// loopback-http, oci} are rejected with an error naming the scheme and the
// allowed set, before any pin lookup or network activity.
func TestSchemeRejection(t *testing.T) {
	t.Parallel()
	svc := externalinputs.NewService(t.TempDir(), true, &http.Client{}, nil)
	ctx := context.Background()

	_, err := svc.Fetch(ctx, "ftp://example.com/x.tgz", "recipe.helm.demo")
	if !errors.Is(err, externalinputs.ErrSchemeNotAllowed) {
		t.Fatalf("ftp: want ErrSchemeNotAllowed, got %v", err)
	}
	if !strings.Contains(err.Error(), `"ftp"`) ||
		!strings.Contains(err.Error(), "https") ||
		!strings.Contains(err.Error(), "oci") {
		t.Errorf("ftp error should name the scheme and the allowed set: %v", err)
	}

	_, err = svc.Fetch(ctx, "http://example.com/x.tgz", "recipe.helm.demo")
	if !errors.Is(err, externalinputs.ErrSchemeNotAllowed) {
		t.Fatalf("non-loopback http: want ErrSchemeNotAllowed, got %v", err)
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("non-loopback http error should explain the loopback-only rule: %v", err)
	}

	// The loopback set that permits plain http covers reserved ".localhost"
	// names and the unspecified 0.0.0.0 address, not just literal loopback IPs.
	// With acquisition disabled these stop at the acquisition guard, proving the
	// scheme gate let them through rather than rejecting the plain-http scheme.
	noAcq := externalinputs.NewService(t.TempDir(), false, &http.Client{}, nil)
	for _, loopbackHTTP := range []string{
		"http://app.localhost/x.tgz",
		"http://0.0.0.0/x.tgz",
	} {
		_, err = noAcq.Fetch(ctx, loopbackHTTP, "recipe.helm.demo")
		if errors.Is(err, externalinputs.ErrSchemeNotAllowed) {
			t.Errorf("%q: plain http to a loopback host must pass the scheme gate, got scheme rejection: %v", loopbackHTTP, err)
		}
	}
}

// TestOCIReferenceRequiresTagOrDigest proves an unversioned oci reference is
// rejected at acquisition: without a tag or digest there is nothing
// reproducible to pin.
func TestOCIReferenceRequiresTagOrDigest(t *testing.T) {
	t.Parallel()
	svc := externalinputs.NewService(t.TempDir(), true, &http.Client{}, nil)
	_, err := svc.Fetch(context.Background(), "oci://localhost:5000/inputs/chart", "recipe.helm.demo")
	if err == nil || !strings.Contains(err.Error(), "must carry a tag or digest") {
		t.Fatalf("want tag-or-digest error, got %v", err)
	}
}
