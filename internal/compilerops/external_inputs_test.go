// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

// The pinned external-input seam every rendering surface that must match a
// build's bytes renders through. The tests live here, beside the one builder,
// rather than beside any surface that calls it — a surface's tests prove what
// that surface attaches, and these prove what the thing attached DOES.
//
// A pinned reference is SERVED from the content-addressed store, which is the
// whole mechanism by which such a render produces what a build produces: the
// alternative is no seam, and no seam means a not-available envelope in place of
// the content. An unpinned reference is DECLINED and remembered, because a render
// that quietly stopped short of the bytes a build would emit is indistinguishable
// to the reader from an accurate one.
//
// Every fixture here hands the seam a workspace that is NOT offline, so the seam
// holds a live transport. That is load-bearing rather than incidental: it is what
// makes a decline below evidence that acquisition is off, rather than evidence
// that there was nothing to fetch with. A fixture with no transport cannot tell
// those two causes apart, and they have different remedies.
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
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// connectedWorkspace is the configuration a seam fixture renders under unless it
// is testing the offline posture itself: the workspace default, which permits
// egress. Named rather than inlined so that a fixture asserting a decline reads
// as asserting it against a seam that COULD have fetched.
func connectedWorkspace() workspaceconfig.Config {
	return workspaceconfig.Default()
}

// offlineWorkspace is the configuration under which the egress gate refuses, so
// the seam holds no transport. It is the one posture in which a fetch refusal is
// attributable to the workspace rather than to the seam.
func offlineWorkspace() workspaceconfig.Config {
	cfg := workspaceconfig.Default()
	cfg.Offline = true
	return cfg
}

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
	hexDigest := hexSHA256(body)

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

// hexSHA256 is the store's blob name for a payload — the digest the service
// recomputes on every read and gates every refill against.
func hexSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// removeStoredBlob deletes the store copy of a pinned payload, leaving the
// lockfile record intact. That is exactly the state a refill exists to repair: a
// pin the lockfile still commits to and content the store no longer holds.
func removeStoredBlob(t *testing.T, workspaceRoot, cacheDir string, body []byte) {
	t.Helper()
	path := filepath.Join(externalinputs.StoreRoot(workspaceRoot, cacheDir), "sha256", hexSHA256(body))
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove store blob: %v", err)
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

// TestPinnedSeamServesAPinnedReference is the byte half. A pinned reference must
// come back as the store's exact bytes, because that is the whole mechanism by
// which a pinned render produces what a build produces.
//
// The assertion is on the bytes rather than on the absence of an error. A seam
// that returned no error and empty content would satisfy the weaker check while
// rendering something a build never would.
func TestPinnedSeamServesAPinnedReference(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/chart-values.yaml"
	body := []byte("replicaCount: 3\nimage: demo:1.0.0\n")
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, url, body)

	seam := PinnedExternalInputs(wsRoot, cacheDir, connectedWorkspace(), lf, nil)
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

// TestPinnedSeamDeclinesAnUnpinnedReferenceWithATransportInHand is the fault the
// reporting half exists for, pinned to the condition that raises it rather than
// to any message text: the text is presentation and free to change, while the
// condition is what the refusal IS.
//
// The fixture pins one reference and requests a DIFFERENT one, so the single
// fault is "this reference is not pinned" and nothing else — a store with no
// pins at all would also fail a reader's expectation that the store loads.
//
// The workspace is connected, which is what the test is really for. The refusal
// has to come from acquisition being off, not from an absent transport, and only
// a seam holding a live client can show that. Under a seam with no transport the
// same assertion passes for the wrong reason, and the wrong reason points a
// reader at the network when the answer is that the reference wants pinning.
func TestPinnedSeamDeclinesAnUnpinnedReferenceWithATransportInHand(t *testing.T) {
	t.Parallel()
	const pinned = "https://example.com/pinned.yaml"
	const unpinned = "https://example.com/not-pinned.yaml"
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, pinned, []byte("pinned: true\n"))

	seam := PinnedExternalInputs(wsRoot, cacheDir, connectedWorkspace(), lf, nil)
	_, err := seam.Fetch(context.Background(), unpinned, "test-node")
	if err == nil {
		t.Fatal("an unpinned reference must refuse loudly under the pinned posture")
	}
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled()) {
		t.Fatalf("the refusal must be the acquisition-disabled condition, not some other failure: %v", err)
	}
	// The refusal must NOT be the offline condition: the workspace is connected,
	// so naming offline here would send a reader to unset something that is not
	// set. This is the assertion that keeps the two conditions distinct.
	if errors.Is(err, egress.ErrOfflineEgress()) {
		t.Errorf("a connected workspace must not raise the offline condition: %v", err)
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

// TestPinnedSeamRefillsAMissingPinnedBlob is the repair, invoked and its result
// read, rather than asserted to be available.
//
// The state under test is the one a reader actually reaches: the lockfile still
// pins the reference and the store no longer holds the content. A build that
// declined here would halt on a pin it is holding itself to, which is why the
// repair is on by default.
//
// Two things are asserted together because either alone would pass on a broken
// implementation: the bytes come back, AND the blob is back in the store under
// the digest the lockfile recorded. A refill that served the bytes without
// writing them would refetch on every node of a render.
func TestPinnedSeamRefillsAMissingPinnedBlob(t *testing.T) {
	t.Parallel()
	body := []byte("replicaCount: 7\n")
	served := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	url := srv.URL + "/values.yaml"
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, url, body)
	removeStoredBlob(t, wsRoot, cacheDir, body)
	if entries := storeEntries(t, wsRoot, cacheDir); len(entries) != 0 {
		t.Fatalf("precondition: the store must hold no blob for the pinned input; got %v", entries)
	}

	seam := PinnedExternalInputs(wsRoot, cacheDir, connectedWorkspace(), lf, nil)
	got, err := seam.Fetch(context.Background(), url, "test-node")
	if err != nil {
		t.Fatalf("a pinned reference whose blob is missing must be refilled: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("refilled bytes differ from the pinned payload:\n got %q\nwant %q", got, body)
	}
	if served != 1 {
		t.Errorf("the refill must have gone to the network exactly once; the endpoint served %d request(s)", served)
	}
	if entries := storeEntries(t, wsRoot, cacheDir); strings.Join(entries, ",") != hexSHA256(body) {
		t.Errorf("the refill must land in the store under the pinned digest; store holds %v", entries)
	}
	// A refill is not an acquisition: nothing was declined and nothing new was
	// pinned, so the closure the render saw is the closure the lockfile records.
	if declined := seam.Declined(); len(declined) != 0 {
		t.Errorf("a refilled reference must not be recorded as declined; got %v", declined)
	}
}

// TestPinnedSeamRefusesARefillWhoseBytesMissThePin is the fault that makes the
// repair safe to have on by default: the digest gates the result before anything
// is written or returned, so a refill cannot introduce bytes the pin did not
// already name.
//
// The endpoint serves content that does not hash to the recorded digest — the
// shape a moved URL or a tampered mirror produces — and the seam must refuse
// rather than serve it, and must leave the store empty rather than caching it.
func TestPinnedSeamRefusesARefillWhoseBytesMissThePin(t *testing.T) {
	t.Parallel()
	pinnedBody := []byte("replicaCount: 7\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("replicaCount: 9000\n"))
	}))
	defer srv.Close()

	url := srv.URL + "/values.yaml"
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, url, pinnedBody)
	removeStoredBlob(t, wsRoot, cacheDir, pinnedBody)

	seam := PinnedExternalInputs(wsRoot, cacheDir, connectedWorkspace(), lf, nil)
	_, err := seam.Fetch(context.Background(), url, "test-node")
	if err == nil {
		t.Fatal("a refill whose bytes miss the pinned digest must refuse")
	}
	if !errors.Is(err, externalinputs.ErrDigestMismatch()) {
		t.Fatalf("the refusal must be the digest-mismatch condition: %v", err)
	}
	if entries := storeEntries(t, wsRoot, cacheDir); len(entries) != 0 {
		t.Errorf("a refused refill must write nothing to the store; store holds %v", entries)
	}
}

// TestPinnedSeamUnderOfflineRefusesTheRefillNamingOffline is the other half of
// the same decision, and it is what makes the offline condition truthful.
//
// "Not refillable" is a consequence of being offline rather than a posture of its
// own: the egress gate refuses for an offline workspace, the seam is left with no
// transport, and a missing blob then refuses NAMING offline — which is now a
// statement a reader can act on, because offline is genuinely set. The connected
// fixture above is this one's needle: the same missing-blob state is repaired
// there, so the refusal here is attributable to the posture and to nothing else.
func TestPinnedSeamUnderOfflineRefusesTheRefillNamingOffline(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/values.yaml"
	body := []byte("replicaCount: 7\n")
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, url, body)
	removeStoredBlob(t, wsRoot, cacheDir, body)

	seam := PinnedExternalInputs(wsRoot, cacheDir, offlineWorkspace(), lf, nil)
	_, err := seam.Fetch(context.Background(), url, "test-node")
	if err == nil {
		t.Fatal("an offline workspace cannot refill, so the missing blob must refuse")
	}
	if !errors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("the refusal must name the offline posture that is actually set: %v", err)
	}
}

// TestPinnedSeamWritesNothingWhenEveryPinnedBlobIsPresent holds the standing
// no-write promise at the scope it is made at rather than a broader one.
//
// The claim is about the external-input store: with every pinned blob already
// present, neither serving a pinned reference nor declining an unpinned one adds
// a blob to it. It deliberately does not assert that a render as a whole writes
// nothing anywhere — a render populates a compiled-guest cache, so a broadened
// claim would be false the moment a reader checked that directory.
//
// It is also scoped to a present blob deliberately. A missing one IS repaired,
// and that repair writes; the promise a reader relies on is that the store is not
// EXTENDED by a render, not that it is never touched.
func TestPinnedSeamWritesNothingWhenEveryPinnedBlobIsPresent(t *testing.T) {
	t.Parallel()
	const pinned = "https://example.com/pinned.yaml"
	const unpinned = "https://example.com/not-pinned.yaml"
	body := []byte("pinned: true\n")
	wsRoot, cacheDir, lf := pinnedInputWorkspace(t, pinned, body)

	before := storeEntries(t, wsRoot, cacheDir)
	if len(before) != 1 {
		t.Fatalf("precondition: the store must hold exactly the seeded blob; got %v", before)
	}

	seam := PinnedExternalInputs(wsRoot, cacheDir, connectedWorkspace(), lf, nil)
	if _, err := seam.Fetch(context.Background(), pinned, "n"); err != nil {
		t.Fatalf("serve a pinned reference: %v", err)
	}
	if _, err := seam.Fetch(context.Background(), unpinned, "n"); err == nil {
		t.Fatal("the unpinned reference must refuse, or the write assertion below covers nothing")
	}

	after := storeEntries(t, wsRoot, cacheDir)
	if strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("the pinned seam extended the store:\nbefore %v\nafter  %v", before, after)
	}
}

// TestPinnedSeamWithNoLockfileDeclinesEverything covers the posture the tool
// protocol most often renders under: a Release with no lockfile at all. Nothing
// is pinned, so every reference is declined, and the seam still refuses loudly
// rather than falling back to a fetch.
//
// It matters because a nil lockfile is the path where a seam built from
// lf.ExternalInputs could plausibly have been skipped as pointless. It is not
// pointless: a decline is a REPORTABLE event and a not-available envelope is not,
// so the two produce different answers for the caller even though neither
// produces content.
func TestPinnedSeamWithNoLockfileDeclinesEverything(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/anything.yaml"

	seam := PinnedExternalInputs(t.TempDir(), ".aggrega/cache", connectedWorkspace(), nil, nil)
	if seam == nil {
		t.Fatal("a nil lockfile must still yield a seam")
	}
	_, err := seam.Fetch(context.Background(), url, "test-node")
	if err == nil {
		t.Fatal("with nothing pinned, every reference must be declined")
	}
	if !errors.Is(err, externalinputs.ErrAcquisitionDisabled()) {
		t.Fatalf("the refusal must be the acquisition-disabled condition: %v", err)
	}
	if declined := seam.Declined(); len(declined) != 1 || declined[0] != url {
		t.Fatalf("Declined() = %v; want exactly %q", declined, url)
	}
}

// TestPinnedSeamInAFolderNoWorkspaceResolvesFromDeclinesTheRefillBeforeFetching
// hands the seam the refusal each rendering surface hands it — the resolution's
// own CacheWriteRefusal — over three real resolutions, against an endpoint that
// counts what it serves.
//
// In a folder nobody marked, with nothing naming a root, a pinned reference whose
// blob is missing is declined with that refusal before anything is fetched: the
// endpoint serves nothing and no .aggrega appears in the folder. Marked as a
// workspace, the same state is repaired — fetched once and written under the
// folder — which is what makes the first row's empty cache the refusal's doing. A
// blob already in the store of a folder nobody marked is served with the refusal
// in hand, because the refusal is of a write and a read writes nothing.
//
// 🔴 PLANTED DEFECT: delete the writeRefusal block at the top of the store's
// refill, and the first row fails — the endpoint serves the bytes and the folder
// gains an .aggrega directory.
func TestPinnedSeamInAFolderNoWorkspaceResolvesFromDeclinesTheRefillBeforeFetching(t *testing.T) {
	t.Parallel()
	body := []byte("replicaCount: 7\n")
	noEnv := func(string) string { return "" }

	for _, tc := range []struct {
		name string
		// config is the folder's configuration file, or empty for none.
		config string
		// present seeds the pinned blob into the folder's store first.
		present  bool
		refused  bool
		wantHits int
	}{
		{name: "a folder nobody marked and nothing named, the blob missing", refused: true},
		{name: "the same folder marked as a workspace, the blob missing", config: "workspace: {}\n", wantHits: 1},
		{name: "a folder nobody marked, the blob already in its store", config: "artifact_cache_dir: pinned-inputs\n", present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			url := srv.URL + "/values.yaml"

			folder := t.TempDir()
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(folder, workspaceconfig.ConfigFilename), []byte(tc.config), 0o644); err != nil {
					t.Fatalf("write the folder's configuration: %v", err)
				}
			}
			rw, err := workspaceconfig.Resolve(folder, "", noEnv)
			if err != nil {
				t.Fatalf("resolve %s: %v", folder, err)
			}
			marked := strings.Contains(tc.config, "workspace:")
			if rw.FoundNoWorkspace() == marked {
				t.Fatalf("precondition: %s resolves as found-nothing=%v (marker %v at %s), the row needs %v",
					folder, rw.FoundNoWorkspace(), rw.Marker(), rw.Root(), !marked)
			}
			cfg := rw.Config()
			cacheDir := cfg.ResolvedArtifactCacheDir()
			if tc.present {
				if strings.HasPrefix(filepath.ToSlash(cacheDir), ".aggrega") {
					t.Fatalf("precondition: the configured cache directory %q was not read, so seeding it would mark the folder", cacheDir)
				}
				blobDir := filepath.Join(externalinputs.StoreRoot(rw.Root(), cacheDir), "sha256")
				if err := os.MkdirAll(blobDir, 0o755); err != nil {
					t.Fatalf("mkdir store: %v", err)
				}
				if err := os.WriteFile(filepath.Join(blobDir, hexSHA256(body)), body, 0o644); err != nil {
					t.Fatalf("seed store blob: %v", err)
				}
			}
			lf := &lockfile.Lockfile{Version: 1, ExternalInputs: []lockfile.ExternalInput{{
				URL: url, Sha256: hexSHA256(body), Size: int64(len(body)), RequiredBy: []string{"test"},
			}}}

			seam := PinnedExternalInputs(rw.Root(), cacheDir, cfg, lf, rw.CacheWriteRefusal())
			got, err := seam.Fetch(context.Background(), url, "test-node")

			if got := hits.Load(); got != int64(tc.wantHits) {
				t.Errorf("the endpoint served %d request(s), want %d", got, tc.wantHits)
			}
			if tc.refused {
				var refusal *workspaceconfig.CacheWriteRefusedError
				if !errors.As(err, &refusal) {
					t.Fatalf("Fetch returned %v (%q), want the refusal of a cache written in a folder that is no workspace", err, got)
				}
				if refusal.Root() != folder {
					t.Errorf("the refusal names %s, want %s", refusal.Root(), folder)
				}
				if !strings.Contains(err.Error(), url) {
					t.Errorf("the decline does not name the reference it declined:\n%v", err)
				}
				if _, serr := os.Stat(filepath.Join(folder, ".aggrega")); !errors.Is(serr, fs.ErrNotExist) {
					t.Errorf("%s/.aggrega exists after the declined refill (stat: %v)", folder, serr)
				}
				// The decline is not the frozen-closure one: a pull in the same folder is
				// refused for the same reason, so no divergence is reported.
				if declined := seam.Declined(); len(declined) != 0 {
					t.Errorf("a refill declined for the cache was recorded as unpinned: %v", declined)
				}
				return
			}
			if err != nil {
				t.Fatalf("the pinned reference was not served: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("served %q, want the pinned bytes %q", got, body)
			}
			if entries := storeEntries(t, rw.Root(), cacheDir); strings.Join(entries, ",") != hexSHA256(body) {
				t.Errorf("the store under %s holds %v, want the pinned blob", folder, entries)
			}
		})
	}
}
