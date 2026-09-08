// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

// The frozen external-input seam every rendering surface that must match a
// build's bytes renders through. The tests live here, beside the one builder,
// rather than beside any surface that calls it — a surface's tests prove what
// that surface attaches, and these prove what the thing attached DOES.
//
// A pinned reference is SERVED from the content-addressed store, which is the
// whole mechanism by which a frozen render produces what a build produces: the
// alternative is no seam, and no seam means a not-available envelope in place of
// the content. An unpinned reference is DECLINED and remembered, because a render
// that quietly stopped short of the bytes a build would emit is indistinguishable
// to the reader from an accurate one.
//
// One fault per fixture, asserted against the condition that fault raises. Each
// absence assertion carries a needle, because a zero is a claim about the
// instrument until something shows the instrument fires.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// pinnedInputWorkspace seeds a workspace artifact cache with one external input
// pinned into the content-addressed store, and returns the workspace root, the
// cache directory configured within it, and the lockfile pinning the input.
//
// The cache directory is RELATIVE, which is the shape a workspace configures and
// therefore the shape production resolves: a helper handing back an absolute one
// would let a seam that ignored the workspace root pass every assertion below.
//
// The blob is written under the digest the service will recompute, so a read
// that verifies against the record succeeds. Size is recorded too: the service
// checks it, so a record carrying the wrong one fails for the wrong reason.
func pinnedInputWorkspace(t *testing.T, url string, body []byte) (workspaceRoot, cacheDir string, lf *lockfile.Lockfile) {
	t.Helper()
	workspaceRoot = t.TempDir()
	cacheDir = ".aggrega/cache"
	sum := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(sum[:])

	blobDir := filepath.Join(externalinputs.StoreRoot(workspaceRoot, cacheDir), "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, hexDigest), body, 0o644); err != nil {
		t.Fatalf("seed store blob: %v", err)
	}
	return workspaceRoot, cacheDir, &lockfile.Lockfile{
		Version: 1,
		ExternalInputs: []lockfile.ExternalInput{{
			URL:        url,
			Sha256:     hexDigest,
			Size:       int64(len(body)),
			RequiredBy: []string{"test"},
		}},
	}
}

// storeEntries lists the store's blob names, so an assertion about what a Fetch
// wrote names what appeared rather than reporting a count.
func storeEntries(t *testing.T, workspaceRoot, cacheDir string) []string {
	t.Helper()
	dir := filepath.Join(externalinputs.StoreRoot(workspaceRoot, cacheDir), "sha256")
	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read store: %v", err)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name())
	}
	return names
}

// TestFrozenSeamServesAPinnedReference is the byte half. A pinned reference must
// come back as the store's exact bytes, because that is the whole mechanism by
// which a frozen render produces what a build produces.
//
// The assertion is on the bytes rather than on the absence of an error. A seam
// that returned no error and empty content would satisfy the weaker check while
// rendering something a build never would.
func TestFrozenSeamServesAPinnedReference(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/chart-values.yaml"
	body := []byte("replicaCount: 3\nimage: demo:1.0.0\n")
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, url, body)

	seam := FrozenExternalInputs(wsRoot, cacheDir, lf)
	got, err := seam.Fetch(context.Background(), url, "test-node")
	if err != nil {
		t.Fatalf("a pinned reference must be served from the store: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("served bytes differ from the pinned blob:\n got %q\nwant %q", got, body)
	}
	// Nothing was declined, so a surface reading this seam owes no divergence
	// report. This is the negative control for the decline assertion below:
	// without it, a recorder that reported everything would still pass there.
	if declined := seam.Declined(); len(declined) != 0 {
		t.Errorf("a served reference must not be recorded as declined; got %v", declined)
	}
}

// TestFrozenSeamDeclinesAnUnpinnedReference is the fault the reporting half
// exists for, pinned to the condition that raises it rather than to any message
// text: the text is presentation and free to change, while the condition is what
// the refusal IS.
//
// The fixture pins one reference and requests a DIFFERENT one, so the single
// fault is "this reference is not pinned" and nothing else — a store with no
// pins at all would also fail a reader's expectation that the store loads.
func TestFrozenSeamDeclinesAnUnpinnedReference(t *testing.T) {
	t.Parallel()
	const pinned = "https://example.com/pinned.yaml"
	const unpinned = "https://example.com/not-pinned.yaml"
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, pinned, []byte("pinned: true\n"))

	seam := FrozenExternalInputs(wsRoot, cacheDir, lf)
	_, err := seam.Fetch(context.Background(), unpinned, "test-node")
	if err == nil {
		t.Fatal("an unpinned reference must refuse loudly under a frozen closure")
	}
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled()) {
		t.Fatalf("the refusal must be the frozen-closure condition, not some other failure: %v", err)
	}
	declined := seam.Declined()
	if len(declined) != 1 || declined[0] != unpinned {
		t.Fatalf("Declined() = %v; want exactly the unpinned reference", declined)
	}
	// The needle for the assertion above: the pinned reference on the SAME seam
	// still serves, so "declined" is a fact about that reference rather than about
	// a seam that refuses everything.
	if _, err := seam.Fetch(context.Background(), pinned, "test-node"); err != nil {
		t.Fatalf("the pinned reference on the same seam must still serve: %v", err)
	}
}

// TestFrozenSeamWritesNothingAndCannotReachTheNetwork holds the two standing
// promises of the frozen seam, and keeps them at the scope they are made at
// rather than a broader one.
//
// The claim is about the external-input store: neither serving a pinned
// reference nor declining an unpinned one adds a blob to it. It deliberately
// does not assert that a render as a whole writes nothing anywhere — a render
// populates a compiled-guest cache, so a broadened claim would be false the
// moment a reader checked that directory.
//
// The network half is structural rather than observed: the service is
// constructed with acquisition off AND a nil transport, which are independent
// switches, so a later edit turning acquisition back on would still not reach
// the network. Asserting the declined outcome is what shows the closed path.
func TestFrozenSeamWritesNothingAndCannotReachTheNetwork(t *testing.T) {
	t.Parallel()
	const pinned = "https://example.com/pinned.yaml"
	const unpinned = "https://example.com/not-pinned.yaml"
	body := []byte("pinned: true\n")
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, pinned, body)

	before := storeEntries(t, wsRoot, cacheDir)
	if len(before) != 1 {
		t.Fatalf("precondition: the store must hold exactly the seeded blob; got %v", before)
	}

	seam := FrozenExternalInputs(wsRoot, cacheDir, lf)
	if _, err := seam.Fetch(context.Background(), pinned, "n"); err != nil {
		t.Fatalf("serve a pinned reference: %v", err)
	}
	if _, err := seam.Fetch(context.Background(), unpinned, "n"); err == nil {
		t.Fatal("the unpinned reference must refuse, or the write assertion below covers nothing")
	}

	after := storeEntries(t, wsRoot, cacheDir)
	if strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("the frozen seam changed the store:\nbefore %v\nafter  %v", before, after)
	}
}

// TestFrozenSeamWithNoLockfileDeclinesEverything covers the posture the tool
// protocol most often renders under: a Release with no lockfile at all. Nothing
// is pinned, so every reference is declined, and the seam still refuses loudly
// rather than falling back to a fetch.
//
// It matters because a nil lockfile is the path where a seam built from
// lf.ExternalInputs could plausibly have been skipped as pointless. It is not
// pointless: a decline is a REPORTABLE event and a not-available envelope is not,
// so the two produce different answers for the caller even though neither
// produces content.
func TestFrozenSeamWithNoLockfileDeclinesEverything(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/anything.yaml"

	seam := FrozenExternalInputs(t.TempDir(), ".aggrega/cache", nil)
	if seam == nil {
		t.Fatal("a nil lockfile must still yield a seam")
	}
	_, err := seam.Fetch(context.Background(), url, "test-node")
	if err == nil {
		t.Fatal("with nothing pinned, every reference must be declined")
	}
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled()) {
		t.Fatalf("the refusal must be the frozen-closure condition: %v", err)
	}
	if declined := seam.Declined(); len(declined) != 1 || declined[0] != url {
		t.Fatalf("Declined() = %v; want exactly %q", declined, url)
	}
}
