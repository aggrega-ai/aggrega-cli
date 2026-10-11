// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noEnvironment is the environment the rows below resolve against: nothing set,
// so no variable names a root and no global file is read, and a row is held to
// the folder it lays down.
func noEnvironment(string) string { return "" }

// resolveRow resolves the folder a row laid down, from that folder and with
// nothing naming a root, and fails where it does not resolve the way the row
// needs: as a folder nobody marked, or as a marked workspace.
func resolveRow(t *testing.T, folder string, marked bool) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, err := workspaceconfig.Resolve(folder, "", noEnvironment)
	if err != nil {
		t.Fatalf("resolve %s: %v", folder, err)
	}
	if rw.FoundNoWorkspace() == marked {
		t.Fatalf("precondition: %s resolves as found-nothing=%v (marker %v at %s), the row needs %v",
			folder, rw.FoundNoWorkspace(), rw.Marker(), rw.Root(), !marked)
	}
	return rw
}

// countingChartServer serves the pin fixtures' chart archive at every path and
// counts the requests it answers, so a row can say whether anything was fetched.
func countingChartServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	blob := pinChartTgz(t)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(blob)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// concreteChartSource is a Recipe source block whose identity is written out in
// full, served at chartURL: the source the pin step and a publication's hold
// resolve into the artifact cache.
func concreteChartSource(chartURL string) string {
	return "  source:\n    kind: helm-https-chart\n    url: " + chartURL + "\n    version: \"1.0.0\"\n"
}

// assertArtifactCacheUnder fails where folder holds no artifact cache: the marked
// control of each row below, which is what makes the other row's missing cache
// the refusal's doing rather than an act that never writes one.
func assertArtifactCacheUnder(t *testing.T, folder, act string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(folder, ".aggrega")); err != nil {
		t.Fatalf("%s in a workspace left no artifact cache under it (%v), so the refused row's missing cache proves nothing", act, err)
	}
}

// TestThePinStepIsRefusedInAFolderNoWorkspaceResolvesFrom runs the author's own
// pin step — the run of `aggrega pin` — over a Recipe whose remote source the
// counting chart server serves, once in a folder nobody marked with nothing
// naming a root and once in the same layout marked as a workspace.
//
// Marked, the step fetches the source, caches it under the folder and writes the
// lock. Unmarked, it is refused before anything is resolved, naming the folder:
// the server answers nothing, no lock is written, no .aggrega appears, and a
// resolution started beneath the folder still anchors nothing.
//
// 🔴 PLANTED DEFECT: delete the CacheWriteRefusal block in the pin step's
// planNamed, and the unmarked row fails — the step fetches the source, creates the
// folder's .aggrega directory and writes the lock.
func TestThePinStepIsRefusedInAFolderNoWorkspaceResolvesFrom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		marked bool
	}{
		{name: "a folder nobody marked and nothing named"},
		{name: "the same layout marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, hits := countingChartServer(t)
			folder := folderNoWorkspaceResolvesFrom(t)
			recipe := holdFixtureRecipe(t, folder, "server", concreteChartSource(srv.URL+"/pinchart.tgz"))
			if tc.marked {
				writeWorkspaceConfig(t, folder, "workspace: {}\n")
			}
			rw := resolveRow(t, folder, tc.marked)

			err := pinRun(context.Background(), io.Discard, rw, recipe, false)

			if tc.marked {
				if err != nil {
					t.Fatalf("the pin step in a workspace failed, so the refused row proves nothing: %v", err)
				}
				if hits.Load() == 0 {
					t.Fatal("the pin step in a workspace fetched nothing, so the refused row's empty fetch count proves nothing")
				}
				assertArtifactCacheUnder(t, folder, "the pin step")
				return
			}
			var refused *workspaceconfig.CacheWriteRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("the pin step returned %v, want the refusal of a cache written in a folder that is no workspace", err)
			}
			if refused.Root() != folder {
				t.Errorf("the refusal names %s, want %s", refused.Root(), folder)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("the refused pin step fetched the source %d time(s)", n)
			}
			if _, serr := os.Stat(recipepin.LockPath(recipe)); !os.IsNotExist(serr) {
				t.Errorf("the refused pin step wrote the lock (stat: %v)", serr)
			}
			assertNoCacheAndStillNoWorkspace(t, folder)
		})
	}
}

// TestAPublicationsHoldIsRefusedInAFolderNoWorkspaceResolvesFrom runs a
// publication — the run of `aggrega push`, not asked to pin — of a Recipe whose
// remote source the counting chart server serves and whose lock records no pin,
// once in a folder nobody marked with nothing naming a root and once in the same
// layout marked as a workspace.
//
// Marked, the publication reaches the hold, which resolves the source into the
// folder's artifact cache to compare against the lock and then refuses the
// unpinned entity. Unmarked, the hold is refused before it resolves anything,
// naming the folder: the server answers nothing, no .aggrega appears, and a
// resolution started beneath the folder still anchors nothing.
//
// 🔴 PLANTED DEFECT: in the pin step's planNamed, ask CacheWriteRefusal only where
// the plan is computed for writing, and the unmarked row fails — the hold fetches
// the source, creates the folder's .aggrega directory and refuses the unpinned
// entity instead.
func TestAPublicationsHoldIsRefusedInAFolderNoWorkspaceResolvesFrom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		marked bool
	}{
		{name: "a folder nobody marked and nothing named"},
		{name: "the same layout marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, hits := countingChartServer(t)
			folder := folderNoWorkspaceResolvesFrom(t)
			recipe := holdFixtureRecipe(t, folder, "server", concreteChartSource(srv.URL+"/pinchart.tgz"))
			if tc.marked {
				writeWorkspaceConfig(t, folder, "workspace: {}\n")
			}
			rw := resolveRow(t, folder, tc.marked)

			err := pushRun(context.Background(), pushConfig{
				Workspace: rw, EntityPath: recipe, Registry: "example.com", PushTarget: memory.New(), Out: io.Discard,
			})

			if tc.marked {
				if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
					t.Fatalf("the publication in a workspace returned %v, want the hold's unpinned-entity refusal, so the refused row proves nothing", err)
				}
				if hits.Load() == 0 {
					t.Fatal("the hold in a workspace fetched nothing, so the refused row's empty fetch count proves nothing")
				}
				assertArtifactCacheUnder(t, folder, "the hold")
				return
			}
			var refused *workspaceconfig.CacheWriteRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("the publication returned %v, want the refusal of a cache written in a folder that is no workspace", err)
			}
			if refused.Root() != folder {
				t.Errorf("the refusal names %s, want %s", refused.Root(), folder)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("the refused hold fetched the source %d time(s)", n)
			}
			assertNoCacheAndStillNoWorkspace(t, folder)
		})
	}
}

// looseFetchingRelease lays down, in folder, the external-input seam fixture — a
// Release whose one node renders through the test guest that requests one
// external input and writes the outcome into its emitted bytes — under file names
// no root marker matches, with its catalog in the conventional directory beside
// it, and returns the Release's path.
//
// The workspace the seam fixture writes is laid down in a directory of its own
// and its files are carried across, so the entities and the guest are that
// fixture's own rather than a second copy of them.
func looseFetchingRelease(t *testing.T, folder string) string {
	t.Helper()
	stage := t.TempDir()
	writeFetchingProviderWorkspace(t, stage)

	carried := []string{
		filepath.Join("catalog", "components", "fetcher.yaml"),
		filepath.Join("catalog", "recipes", "fetcher.yaml"),
		filepath.Join("catalog", "plugins", "fetching-provider.yaml"),
	}
	for _, ref := range []string{"provider/fetching", "provider/fetching:1.0.0"} {
		carried = append(carried, filepath.Join("catalog", "plugins", pluginpath.PluginLeaf(ref)))
	}
	for _, rel := range carried {
		writeFixtureBytes(t, filepath.Join(folder, rel), mustReadFile(t, filepath.Join(stage, rel)))
	}

	renamed := map[string]string{"aggregate.yaml": "fetch-aggregate.yaml", "manifest.yaml": "fetch-mappings.yaml"}
	for from, to := range renamed {
		writeFixtureBytes(t, filepath.Join(folder, to), mustReadFile(t, filepath.Join(stage, from)))
	}
	release := string(mustReadFile(t, filepath.Join(stage, "release.yaml")))
	for from, to := range renamed {
		if !strings.Contains(release, "./"+from) {
			t.Fatalf("the seam fixture's Release no longer names ./%s, so it cannot be carried across:\n%s", from, release)
		}
		release = strings.ReplaceAll(release, "./"+from, "./"+to)
	}
	path := filepath.Join(folder, "fetch.yaml")
	writeFixture(t, path, release)
	return path
}

// pinTheFixtureInputWithItsBlobMissing records the seam fixture's external input
// in the Release's lockfile and puts nothing in the store, which is the state a
// render repairs by refilling the blob: a pin the lockfile commits to and content
// the artifact cache does not hold.
func pinTheFixtureInputWithItsBlobMissing(t *testing.T, release string) {
	t.Helper()
	lockPath := lockfile.PathFor(release)
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil || lf == nil {
		t.Fatalf("load the primed lockfile %s: %v", lockPath, err)
	}
	sum := sha256.Sum256([]byte(seamFixtureBody))
	lf.ExternalInputs = []lockfile.ExternalInput{{
		URL:        seamFixtureURL,
		Sha256:     hex.EncodeToString(sum[:]),
		Size:       int64(len(seamFixtureBody)),
		RequiredBy: []string{"fetching"},
	}}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile carrying the external-input pin: %v", err)
	}
}

// TestABuildsRefillOfAPinnedInputIsDeclinedInAFolderNoWorkspaceResolvesFrom runs
// a build through the command layer's dispatch over a Release whose lockfile pins
// an external input the artifact cache does not hold, rendered by the test guest
// that writes the outcome of its fetch into the bytes it emits. Each folder is
// offline, so no row reaches the network whatever it does.
//
// In a folder nobody marked, with nothing naming a root, the refill is declined
// with the refusal of a cache write — the guest's emitted bytes carry that
// sentence, naming the folder and the ways out — and no .aggrega appears. In the
// same layout marked as a workspace the refill is attempted, and the only thing
// that stops it is the offline posture, which the bytes name instead: the
// refusal is the predicate's doing and nothing else's. That the refill writes the
// cache where it is allowed to fetch is the seam's own test,
// TestPinnedSeamInAFolderNoWorkspaceResolvesFromDeclinesTheRefillBeforeFetching.
//
// 🔴 PLANTED DEFECT: hand the build's seam nil in place of the resolution's
// CacheWriteRefusal, and the unmarked row fails — its bytes name the offline
// posture rather than the refusal.
func TestABuildsRefillOfAPinnedInputIsDeclinedInAFolderNoWorkspaceResolvesFrom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		marked bool
	}{
		{name: "a folder nobody marked and nothing named"},
		{name: "the same layout marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			folder := folderNoWorkspaceResolvesFrom(t)
			release := looseFetchingRelease(t, folder)
			config := "auto_pull: false\noffline: true\n"
			if tc.marked {
				config = "workspace: {}\n" + config
			}
			writeWorkspaceConfig(t, folder, config)

			// The lockfile is primed by a pull naming the folder as its root, which a
			// cache write is allowed in; it writes no cache here, because every entity
			// is local and the input it would acquire is refused for the offline posture.
			primed, err := workspaceconfig.Resolve(folder, folder, noEnvironment)
			if err != nil {
				t.Fatalf("resolve %s as a named root: %v", folder, err)
			}
			var primeDiag bytes.Buffer
			if err := pullRun(context.Background(), pullConfig{
				ws: primed, ReleasePath: release, DiagOut: &primeDiag, Out: io.Discard, diag: buildDiagOptions{quiet: true},
			}); err != nil {
				t.Fatalf("prime the lockfile: %v\n%s", err, primeDiag.String())
			}
			pinTheFixtureInputWithItsBlobMissing(t, release)
			if !tc.marked {
				if _, serr := os.Stat(filepath.Join(folder, ".aggrega")); !os.IsNotExist(serr) {
					t.Fatalf("precondition: priming the lockfile left %s/.aggrega (stat: %v), so the row cannot tell what the build wrote", folder, serr)
				}
			}
			rw := resolveRow(t, folder, tc.marked)

			var diag bytes.Buffer
			if err := dispatchBuildInRoot(t, rw, release, nil, false, &diag, io.Discard); err != nil {
				t.Fatalf("the build failed; the guest writes its fetch's outcome rather than failing, so it should finish: %v\n%s", err, diag.String())
			}
			emitted := renderedFileNamed(t, readRenderedTree(t, filepath.Join(folder, "dist")), "the build")

			const offline = "network access refused because the workspace is offline"
			if tc.marked {
				if strings.Contains(emitted, "nothing is written to an artifact cache there") {
					t.Errorf("the build in a workspace declined the refill for the cache:\n%s", emitted)
				}
				if !strings.Contains(emitted, offline) {
					t.Errorf("the build in an offline workspace did not attempt the refill, so the other row's refusal is not shown to be the predicate's doing:\n%s", emitted)
				}
				return
			}
			refusal := rw.CacheWriteRefusal()
			if refusal == nil || !strings.Contains(emitted, refusal.Error()) {
				t.Errorf("the guest was not told the refusal of a cache write;\nemitted:\n%s\nwant it to carry:\n%v", emitted, refusal)
			}
			if !strings.Contains(emitted, seamFixtureURL) {
				t.Errorf("the decline does not name the reference it declined:\n%s", emitted)
			}
			assertNoCacheAndStillNoWorkspace(t, folder)
		})
	}
}
