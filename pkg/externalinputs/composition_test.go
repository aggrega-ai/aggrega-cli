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
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// seedPinnedBlob writes payload into the store beneath cacheDir at the layout
// the composed postures own, and returns the lockfile view pinning url to it.
// It seeds the store directly rather than through an acquisition so that a test
// asserting a store read cannot pass by having fetched.
func seedPinnedBlob(t *testing.T, cacheDir, url string, payload []byte) *lockfile.Lockfile {
	t.Helper()
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	dir := filepath.Join(cacheDir, "inputs", "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, digest), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return &lockfile.Lockfile{ExternalInputs: []lockfile.ExternalInput{{
		URL:    url,
		Sha256: digest,
		Size:   int64(len(payload)),
	}}}
}

// TestNewFrozenRefusesAnUnpinnedReference proves the consumption posture: an
// unpinned reference is refused with the frozen-closure condition rather than
// fetched. The nil lockfile is the case worth pinning down, because a caller
// with no lockfile has nothing pinned and the posture must still refuse loudly
// rather than fall through to a fetch.
func TestNewFrozenRefusesAnUnpinnedReference(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		lf   *lockfile.Lockfile
	}{
		{name: "no lockfile", lf: nil},
		{name: "lockfile pinning nothing", lf: &lockfile.Lockfile{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := externalinputs.NewFrozen(t.TempDir(), tc.lf)
			_, err := svc.Fetch(context.Background(), "https://example.com/never-pinned", "unit-test")
			if !errors.Is(err, externalinputs.ErrAcquisitionDisabled()) {
				t.Fatalf("the consumption posture must refuse an unpinned reference with the frozen-closure condition; got %v", err)
			}
		})
	}
}

// TestNewFrozenServesAPinnedReferenceFromTheStore proves the other half of the
// consumption posture, and proves it against a host that cannot resolve: only a
// store read can satisfy this, so a pass cannot have come from the network.
func TestNewFrozenServesAPinnedReferenceFromTheStore(t *testing.T) {
	t.Parallel()
	const url = "https://unreachable.invalid/pinned.txt"
	payload := []byte("already pinned")
	cacheDir := t.TempDir()
	lf := seedPinnedBlob(t, cacheDir, url, payload)

	svc := externalinputs.NewFrozen(cacheDir, lf)
	got, err := svc.Fetch(context.Background(), url, "unit-test")
	if err != nil {
		t.Fatalf("a pinned reference must be served from the store: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("served %q, want the pinned payload", got)
	}
	if recs := svc.AcquiredRecords(); len(recs) != 0 {
		t.Errorf("serving a pinned reference must not re-pin; got %+v", recs)
	}
}

// TestNewAcquiringPinsUnderTheStoreRoot proves the acquisition posture, and
// asserts WHERE the bytes land. The store root is derived inside the posture
// rather than by the caller, so an edit that dropped that derivation would still
// acquire correctly and would quietly write outside the directory every
// cache-inspection surface enumerates.
func TestNewAcquiringPinsUnderTheStoreRoot(t *testing.T) {
	t.Parallel()
	served := []byte("acquired through the composed posture")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(served)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	svc := externalinputs.NewAcquiring(cacheDir, workspaceconfig.Config{}, nil)

	got, err := svc.Fetch(context.Background(), srv.URL+"/a", "unit-test")
	if err != nil {
		t.Fatalf("the acquisition posture must fetch an unpinned reference: %v", err)
	}
	if string(got) != string(served) {
		t.Fatalf("Fetch returned %q, want the served bytes", got)
	}

	sum := sha256.Sum256(served)
	blob := filepath.Join(cacheDir, "inputs", "sha256", hex.EncodeToString(sum[:]))
	if _, err := os.Stat(blob); err != nil {
		t.Errorf("the pinned blob is not under the store root the posture owns: %v", err)
	}
	if recs := svc.AcquiredRecords(); len(recs) != 1 || recs[0].URL != srv.URL+"/a" {
		t.Errorf("AcquiredRecords = %+v, want one record for the fetched reference", recs)
	}
}

// TestNewAcquiringWithoutEgressStillServesPinnedBytes proves the claim the
// acquisition posture makes about a refused transport, which is the half easiest
// to get wrong: the posture asks for acquisition, so a reader could reasonably
// expect it to fetch, and an offline workspace means it holds no client to fetch
// with. Both halves are asserted together because either alone would be
// misleading — a pinned reference is still served from the store, and an
// unpinned one refuses with the offline condition instead of being acquired.
//
// Asserting the pinned half against a host that cannot resolve is what makes it
// a store read rather than a lucky fetch.
func TestNewAcquiringWithoutEgressStillServesPinnedBytes(t *testing.T) {
	t.Parallel()
	const pinnedURL = "https://unreachable.invalid/pinned.txt"
	payload := []byte("pinned before the workspace went offline")
	cacheDir := t.TempDir()
	lf := seedPinnedBlob(t, cacheDir, pinnedURL, payload)

	svc := externalinputs.NewAcquiring(cacheDir, workspaceconfig.Config{Offline: true}, lf)

	got, err := svc.Fetch(context.Background(), pinnedURL, "unit-test")
	if err != nil {
		t.Fatalf("a pinned reference must still be served from the store with no egress: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("served %q, want the pinned payload", got)
	}

	_, err = svc.Fetch(context.Background(), "https://example.com/never-pinned", "unit-test")
	if !errors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("with no egress an unpinned reference must refuse with the offline condition, not be acquired; got %v", err)
	}
	if recs := svc.AcquiredRecords(); len(recs) != 0 {
		t.Errorf("nothing can have been acquired with no transport; got %+v", recs)
	}
}
