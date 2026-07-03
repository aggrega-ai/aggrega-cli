// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

func TestBuildNoAcquireInputsFlagRegistered(t *testing.T) {
	t.Parallel()
	c := newBuildCmd()
	f := c.Flags().Lookup("no-acquire-inputs")
	if f == nil {
		t.Fatalf("build command missing the --no-acquire-inputs flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-acquire-inputs default = %q, want false (acquisition on by default)", f.DefValue)
	}
}

func TestAcquireCommandRegistered(t *testing.T) {
	t.Parallel()
	root := newRootCmd()
	var found bool
	for _, sub := range root.Commands() {
		if sub.Name() == "acquire" {
			found = true
			if f := sub.Flags().Lookup("release"); f == nil {
				t.Errorf("acquire command missing the --release flag")
			}
			if f := sub.Flags().Lookup("catalog"); f == nil {
				t.Errorf("acquire command missing the --catalog flag")
			}
		}
	}
	if !found {
		t.Fatalf("root command tree does not register the acquire command")
	}
}

// TestNewExternalInputsServiceAcquires proves the build's service wiring
// end-to-end at the unit level: acquisition on, an unpinned URL is
// fetched and its blob lands under <cacheDir>/inputs/sha256/<digest> —
// the store root the build hands the service.
func TestNewExternalInputsServiceAcquires(t *testing.T) {
	t.Parallel()
	served := []byte("wired-through-the-build-service")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(served)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	svc := newExternalInputsService(cacheDir, &lockfile.Lockfile{}, false)

	got, err := svc.Fetch(context.Background(), srv.URL+"/a", "unit-test")
	if err != nil {
		t.Fatalf("Fetch with acquisition on: %v", err)
	}
	if string(got) != string(served) {
		t.Fatalf("Fetch returned %q, want the served bytes", got)
	}

	sum := sha256.Sum256(served)
	blobPath := filepath.Join(cacheDir, "inputs", "sha256", hex.EncodeToString(sum[:]))
	if _, err := os.Stat(blobPath); err != nil {
		t.Errorf("pinned blob not at the build's store root: %v", err)
	}
	if recs := svc.AcquiredRecords(); len(recs) != 1 || recs[0].URL != srv.URL+"/a" {
		t.Errorf("AcquiredRecords = %+v, want one record for the fetched URL", recs)
	}
}

// TestNewExternalInputsServiceFrozenRefusesUnpinned proves the
// --no-acquire-inputs wiring: with the closure frozen, an unpinned URL
// is refused with the acquisition-disabled diagnostic instead of being
// fetched.
func TestNewExternalInputsServiceFrozenRefusesUnpinned(t *testing.T) {
	t.Parallel()
	svc := newExternalInputsService(t.TempDir(), &lockfile.Lockfile{}, true)

	_, err := svc.Fetch(context.Background(), "https://example.com/never-pinned", "unit-test")
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled) {
		t.Fatalf("frozen closure must refuse an unpinned URL with ErrAcquisitionDisabled; got %v", err)
	}
}

// TestNewExternalInputsServiceSeedsLockfilePins proves the lockfile
// view threads into the service: a pinned URL whose blob is already in
// the store is served without any network reachability (the host name
// does not resolve), so a complete lockfile keeps the build hermetic.
func TestNewExternalInputsServiceSeedsLockfilePins(t *testing.T) {
	t.Parallel()
	payload := []byte("pre-pinned payload")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])

	cacheDir := t.TempDir()
	storeDir := filepath.Join(cacheDir, "inputs", "sha256")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, digest), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	pinnedURL := "https://unreachable.invalid/pinned.txt"
	lf := &lockfile.Lockfile{ExternalInputs: []lockfile.ExternalInput{{
		URL:    pinnedURL,
		Sha256: digest,
		Size:   int64(len(payload)),
	}}}

	// Frozen mode AND an unresolvable host: only the store can serve this.
	svc := newExternalInputsService(cacheDir, lf, true)
	got, err := svc.Fetch(context.Background(), pinnedURL, "unit-test")
	if err != nil {
		t.Fatalf("pinned URL must be served from the store without network: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("served %q, want the pinned payload", got)
	}
	if recs := svc.AcquiredRecords(); len(recs) != 0 {
		t.Errorf("serving a pinned URL must not re-pin; got %+v", recs)
	}
}
