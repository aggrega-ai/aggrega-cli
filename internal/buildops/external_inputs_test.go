// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The seam under test is the one the build attaches — the shared pinned
// composition — called here exactly as the build calls it, so these assertions
// hold the build's own wiring rather than a service assembled for the test. A
// build has no acquiring posture to exercise: acquisition is the pull step's,
// and the acquiring composition is asserted where it is composed.
//
// Every fixture hands the seam a CONNECTED workspace, and that is what gives the
// assertions below their force. The build's seam holds a live transport, so a
// refusal here is evidence that acquisition is off rather than evidence that
// there was nothing to fetch with — and those two causes are indistinguishable
// under a fixture with no transport, while pointing a reader at different things.
//
// Each fixture below supplies a workspace root and a RELATIVE cache directory,
// which is what a build resolves: the configured directory names a location
// inside the workspace, so a fixture handing over an absolute one would let a
// seam that ignored the root pass. The store path an assertion looks at is read
// from the same derivation the seam locates it by, so the layout is spelled in
// one place and a fixture cannot come to disagree with the store it inspects.

// testCacheDir is the workspace-relative artifact cache directory these fixtures
// configure — the same default shape a workspace carries.
const testCacheDir = ".aggrega/cache"

// connectedBuildWorkspace is the configuration a build renders under by default:
// the workspace default, which permits egress. It is named so that every seam
// construction below reads as "a build that COULD have fetched".
func connectedBuildWorkspace() workspaceconfig.Config {
	return workspaceconfig.Default()
}

// TestBuildFetchSeamRefusesUnpinned proves the pinned-closure wiring the build
// renders through: an unpinned URL is refused with the acquisition-disabled
// condition instead of being fetched, and the reference is recorded as declined
// so the surface can name it.
func TestBuildFetchSeamRefusesUnpinned(t *testing.T) {
	t.Parallel()
	seam := compilerops.PinnedExternalInputs(t.TempDir(), testCacheDir, connectedBuildWorkspace(), &lockfile.Lockfile{}, nil)

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
//
// The workspace is connected and the seam therefore HOLDS a working transport,
// which is what makes the zero meaningful. The seam can reach that endpoint; it
// declines to, because acquisition is off. A seam with no transport would score
// the same zero while proving only that it had no client, and the build's own
// no-acquisition guarantee would then rest on nothing this test observed.
func TestBuildFetchSeamNeverAcquires(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("must never be served to a build"))
	}))
	defer srv.Close()

	wsRoot := t.TempDir()
	seam := compilerops.PinnedExternalInputs(wsRoot, testCacheDir, connectedBuildWorkspace(), &lockfile.Lockfile{}, nil)

	if _, err := seam.Fetch(context.Background(), srv.URL+"/a", "unit-test"); err == nil {
		t.Fatal("a build's render must refuse an unpinned reference even when the endpoint is reachable")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("the endpoint was reached %d time(s); a build's render must not acquire", got)
	}
	sum := sha256.Sum256([]byte("must never be served to a build"))
	blobPath := filepath.Join(externalinputs.StoreRoot(wsRoot, testCacheDir), "sha256", hex.EncodeToString(sum[:]))
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

	wsRoot := t.TempDir()
	storeDir := filepath.Join(externalinputs.StoreRoot(wsRoot, testCacheDir), "sha256")
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

	// A build's seam AND an unresolvable host: only the store can serve this. The
	// workspace is connected, so the seam holds a transport and simply has no need
	// of it — which is the hermeticity claim stated at its real strength. A pinned
	// blob already in the store is served without a dial even being attempted.
	seam := compilerops.PinnedExternalInputs(wsRoot, testCacheDir, connectedBuildWorkspace(), lf, nil)
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
