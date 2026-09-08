// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package buildops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The seam under test is the one the build attaches — the shared frozen
// composition — called here exactly as the build calls it, so these assertions
// hold the build's own wiring rather than a service assembled for the test. A
// build has no acquiring posture to exercise: acquisition is the pull step's,
// and the acquiring composition is asserted where it is composed.

// TestBuildFetchSeamRefusesUnpinned proves the frozen-closure wiring the build
// renders through: an unpinned URL is refused with the acquisition-disabled
// condition instead of being fetched, and the reference is recorded as declined
// so the surface can name it.
func TestBuildFetchSeamRefusesUnpinned(t *testing.T) {
	t.Parallel()
	seam := compilerops.FrozenExternalInputs(t.TempDir(), &lockfile.Lockfile{})

	_, err := seam.Fetch(context.Background(), "https://example.com/never-pinned", "unit-test")
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled()) {
		t.Fatalf("a build's render must refuse an unpinned URL with ErrAcquisitionDisabled; got %v", err)
	}
	if declined := seam.Declined(); len(declined) != 1 || declined[0] != "https://example.com/never-pinned" {
		t.Errorf("Declined = %+v, want the one refused reference", declined)
	}
}

// TestBuildFetchSeamNeverAcquires proves the property the removal of the build's
// acquisition dial rests on: with a reachable endpoint and no pin, the build's
// seam still refuses and never touches the network. A fetch count of zero is the
// observable — an error alone cannot distinguish a refusal from a failed fetch.
func TestBuildFetchSeamNeverAcquires(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("must never be served to a build"))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	seam := compilerops.FrozenExternalInputs(cacheDir, &lockfile.Lockfile{})

	if _, err := seam.Fetch(context.Background(), srv.URL+"/a", "unit-test"); err == nil {
		t.Fatal("a build's render must refuse an unpinned reference even when the endpoint is reachable")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("the endpoint was reached %d time(s); a build's render must not acquire", got)
	}
	sum := sha256.Sum256([]byte("must never be served to a build"))
	blobPath := filepath.Join(cacheDir, "inputs", "sha256", hex.EncodeToString(sum[:]))
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Errorf("a build's render wrote a blob into the store: %v", err)
	}
}

// TestBuildFetchSeamSeedsLockfilePins proves the lockfile view threads into the
// build's seam: a pinned URL whose blob is already in the store is served
// without any network reachability (the host name does not resolve), so a
// complete lockfile keeps the build hermetic.
func TestBuildFetchSeamSeedsLockfilePins(t *testing.T) {
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

	// A build's seam AND an unresolvable host: only the store can serve this.
	seam := compilerops.FrozenExternalInputs(cacheDir, lf)
	got, err := seam.Fetch(context.Background(), pinnedURL, "unit-test")
	if err != nil {
		t.Fatalf("pinned URL must be served from the store without network: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("served %q, want the pinned payload", got)
	}
	if declined := seam.Declined(); len(declined) != 0 {
		t.Errorf("serving a pinned URL must record no decline; got %+v", declined)
	}
}
