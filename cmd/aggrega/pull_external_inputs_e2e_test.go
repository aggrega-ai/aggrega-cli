// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The dependency resolution pins external inputs, which means it renders: which
// URLs a build consumes is a product of the render rather than a declaration in
// any document, so nothing short of a render can enumerate them.
//
// Every case here drives the real command paths and observes the fetch at the
// endpoint, which is what makes each assertion a measurement rather than a
// restatement of the code. The endpoint is a loopback server: the scheme
// allowlist admits plain http to a loopback host precisely so a local server can
// stand in for a remote one, and closing it is what turns "no network" into an
// observation instead of a hope about the machine the suite runs on.
//
// 🔴 THE FETCH COUNTER IS THE LOAD-BEARING INSTRUMENT, and the reason is that
// every case below would pass without it. A lockfile carrying a pin proves
// nothing about which run put it there, and an offline build serving a pinned
// input renders identically to one that never needed the input at all. So each
// case reads the counter across a boundary — before and after a run — and
// attributes the movement.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// remoteSchemaBody is the schema document the loopback endpoint serves. It is a
// permissive object schema, so the chart's values validate against it and the
// render reaches its output — a schema that rejected the values would fail the
// render for a reason that has nothing to do with pinning.
const remoteSchemaBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`

// remoteSchemaServer starts the loopback endpoint the chart's values schema
// points at and returns its URL together with a reader of how many times it has
// been asked for the document.
//
// The count is returned as a function rather than as a pointer so a caller
// cannot accidentally reset it, and it is read at boundaries rather than
// asserted as a total: what each case needs to know is which run moved it.
func remoteSchemaServer(t *testing.T) (schemaURL string, fetches func() int, close func()) {
	t.Helper()
	served := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		_, _ = w.Write([]byte(remoteSchemaBody))
	}))
	return srv.URL + "/values-schema.json", func() int { return served }, srv.Close
}

// remoteSchemaDigest is the pin the endpoint's document hashes to, computed here
// rather than copied so the assertion cannot agree with a stale constant.
func remoteSchemaDigest() (string, int64) {
	sum := sha256.Sum256([]byte(remoteSchemaBody))
	return hex.EncodeToString(sum[:]), int64(len(remoteSchemaBody))
}

// declareEgress writes the workspace declaration for a fixture that needs to
// choose whether the network is reachable, which no test-helper option
// expresses.
//
// The offline switch is a top-level key and the catalog directories sit under
// the root marker, which is the shape the configuration actually carries: a
// misplaced offline key parses as an unknown field and the workspace comes back
// connected, so the case that depends on the denial would pass having denied
// nothing. Each caller therefore reads the resolved value back rather than
// trusting the file it wrote.
func declareEgress(t *testing.T, root, catalogDir string, offline bool) {
	t.Helper()
	writeWorkspaceConfig(t, root, fmt.Sprintf("offline: %v\nworkspace:\n  catalog_dirs:\n    - %s", offline, catalogDir))
}

// findExternalInput returns the pin recorded for url, and whether one was found.
func findExternalInput(lf *lockfile.Lockfile, url string) (lockfile.ExternalInput, bool) {
	if lf == nil {
		return lockfile.ExternalInput{}, false
	}
	for _, rec := range lf.ExternalInputs {
		if rec.URL == url {
			return rec, true
		}
	}
	return lockfile.ExternalInput{}, false
}

// TestPullPinsTheExternalInputsTheRenderRequests is the first half of the one
// pinning moment: a resolution over a Release whose render asks for a remote
// document fetches it, stores it under its digest, and records the pin.
//
// The endpoint counter is asserted to move DURING the resolution, which is what
// separates this from a lockfile that happens to carry a pin. The store is
// asserted separately, because a recorded pin whose bytes are not in the store
// is a lockfile that promises an offline build it cannot deliver.
func TestPullPinsTheExternalInputsTheRenderRequests(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	schemaURL, fetches, closeSrv := remoteSchemaServer(t)
	defer closeSrv()

	releasePath, catalogDir := dryRunFixture(t, root, schemaURL)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	before := fetches()
	var diag, out bytes.Buffer
	if err := pullRun(pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, DiagOut: &diag, Out: &out}); err != nil {
		t.Fatalf("resolve: %v\ndiagnostics:\n%s", err, diag.String())
	}
	if fetches() == before {
		t.Fatalf("the resolution never reached the endpoint, so it did not render to discover anything; diagnostics:\n%s", diag.String())
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	rec, found := findExternalInput(lf, schemaURL)
	if !found {
		t.Fatalf("the resolution fetched %s and did not pin it; recorded: %+v", schemaURL, lf.ExternalInputs)
	}
	wantDigest, wantSize := remoteSchemaDigest()
	if rec.Sha256 != wantDigest || rec.Size != wantSize {
		t.Errorf("pin records sha256=%s size=%d, want sha256=%s size=%d", rec.Sha256, rec.Size, wantDigest, wantSize)
	}
	if len(rec.RequiredBy) == 0 {
		t.Errorf("pin records no provenance, so the lockfile cannot say which plugin asked for it: %+v", rec)
	}

	blob := filepath.Join(externalinputs.StoreRoot(ws.Config().ResolvedArtifactCacheDir()), "sha256", wantDigest)
	if _, err := os.Stat(blob); err != nil {
		t.Errorf("the pinned bytes are not in the store, so an offline build cannot serve them: %v", err)
	}

	if !strings.Contains(out.String(), "external_inputs=1") {
		t.Errorf("the completion line does not report the pin it wrote; got:\n%s", out.String())
	}
}

// TestPullThenBuildOfflineSucceeds is the whole point of collapsing the pinning
// moments: resolve once while connected, then build with the network denied.
//
// Both denials are applied, and they refuse at different places rather than
// twice at the same one. Closing the endpoint means a fetch that is attempted
// fails; declaring the workspace offline means the egress gate refuses before
// any dial, so nothing even tries. A build that renders under both was served
// entirely from what the resolution pinned.
func TestPullThenBuildOfflineSucceeds(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	schemaURL, fetches, closeSrv := remoteSchemaServer(t)

	releasePath, catalogDir := dryRunFixture(t, root, schemaURL)
	// Connected for the resolution; the same file is rewritten offline below, so
	// the build reads the same workspace with one field moved.
	declareEgress(t, root, catalogDir, false)

	var pullDiag, pullOut bytes.Buffer
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		DiagOut:     &pullDiag,
		Out:         &pullOut,
	}); err != nil {
		closeSrv()
		t.Fatalf("resolve while connected: %v\ndiagnostics:\n%s", err, pullDiag.String())
	}
	if fetches() == 0 {
		closeSrv()
		t.Fatal("the resolution never fetched the remote document, so this fixture proves nothing about pinning")
	}

	closeSrv()
	fetchesAfterPull := fetches()
	declareEgress(t, root, catalogDir, true)
	offlineWS := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if !offlineWS.Config().Offline {
		t.Fatal("the workspace did not come back offline, so the denial this case rests on is not in effect")
	}

	var diag, out bytes.Buffer
	if err := dispatchBuildWithDiag(offlineWS, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{}, &diag, &out); err != nil {
		t.Fatalf("an offline build over a fully-pinned Release must render: %v\ndiagnostics:\n%s", err, diag.String())
	}
	if fetches() != fetchesAfterPull {
		t.Errorf("the offline build reached the endpoint: fetch count moved from %d to %d", fetchesAfterPull, fetches())
	}
	if !strings.Contains(out.String(), "Build completed") {
		t.Errorf("the offline build did not report a completed render; got:\n%s", out.String())
	}
	// A build that rendered zero artifacts satisfies everything above while
	// proving the render never happened.
	rendered := treeSnapshot(t, filepath.Join(root, "dist"))
	if len(rendered) == 0 {
		t.Error("the offline build wrote no artifacts, so it cannot have rendered the chart")
	}
}

// TestPullOfflineCannotPinAndSaysSo is the negative control for the case above,
// and without it that case reads as "an offline build works", which is not the
// claim. Denying egress to the RESOLUTION leaves it unable to pin, so the
// offline build that follows halts naming the reference.
//
// It is also the second of the two behaviours this run degrades on rather than
// failing: the dependency pins are written, the run succeeds, and what could not
// be discovered is said out loud.
func TestPullOfflineCannotPinAndSaysSo(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	schemaURL, fetches, closeSrv := remoteSchemaServer(t)
	defer closeSrv()

	releasePath, catalogDir := dryRunFixture(t, root, schemaURL)
	declareEgress(t, root, catalogDir, true)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	var diag, out bytes.Buffer
	if err := pullRun(pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, DiagOut: &diag, Out: &out}); err != nil {
		t.Fatalf("an offline resolution must still write its dependency pins: %v\ndiagnostics:\n%s", err, diag.String())
	}
	if fetches() != 0 {
		t.Errorf("the offline resolution reached the endpoint %d time(s); the egress gate must refuse before any dial", fetches())
	}

	// The dependency pins landed: that is the half that is not degraded.
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.Entities) == 0 {
		t.Error("the offline resolution wrote no dependency pins, so it did not keep the work it completed")
	}
	if _, found := findExternalInput(lf, schemaURL); found {
		t.Errorf("the offline resolution recorded a pin it could not have fetched: %+v", lf.ExternalInputs)
	}

	// The diagnostic has to say which stage stopped and what is on disk anyway,
	// or a reader cannot tell whether they have a lockfile at all.
	notice := diag.String()
	for _, want := range []string{
		"external inputs: could not finish rendering",
		"dependency pins were written and are complete",
		"halts naming the unpinned URL",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("the diagnostics do not carry %q; got:\n%s", want, notice)
		}
	}
	if !strings.Contains(out.String(), "External inputs were not fully discovered") {
		t.Errorf("the completion output does not say the pin set is incomplete; got:\n%s", out.String())
	}
}

// TestBuildAutoPullFetchesOncePerFill establishes what the automatic fill does,
// which is the cost question the single acquisition moment owes an answer to.
//
// There is one behaviour rather than a grid: no build's render acquires, so the
// fill is always the only thing that can pin what that render will ask for. It
// discovers, and the build's render is then served from the store. The fetch
// count is the observable that pins both halves at once — more than one would
// mean the remote document was acquired twice, and none would mean nothing
// acquired and the build somehow rendered anyway.
//
// It is deliberately a plain test rather than a one-row table: the row that made
// this a table was the build-acquires cell, which no invocation can now reach.
func TestBuildAutoPullFetchesOncePerFill(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	schemaURL, fetches, closeSrv := remoteSchemaServer(t)
	defer closeSrv()

	releasePath, catalogDir := dryRunFixture(t, root, schemaURL)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	var diag, out bytes.Buffer
	if err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{AutoPull: true}, &diag, &out); err != nil {
		t.Fatalf("build with the automatic fill: %v\ndiagnostics:\n%s", err, diag.String())
	}
	if got := fetches(); got != 1 {
		t.Errorf("the remote document was fetched %d time(s), want exactly 1: more means it was acquired twice, and none means nothing acquired", got)
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if _, found := findExternalInput(lf, schemaURL); !found {
		t.Fatalf("nothing pinned %s; recorded: %+v", schemaURL, lf.ExternalInputs)
	}
}

// TestPullUpgradeRediscoversExternalInputs pins the refresh semantic. A full
// refresh carries no prior pin forward, so before this change it left the
// external-input list empty and a later build re-acquired. It now re-discovers, so
// the refreshed lockfile is complete on its own.
//
// The endpoint is asked again, which is the observable that separates
// re-discovery from a pin that merely survived.
func TestPullUpgradeRediscoversExternalInputs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	schemaURL, fetches, closeSrv := remoteSchemaServer(t)
	defer closeSrv()

	releasePath, catalogDir := dryRunFixture(t, root, schemaURL)

	pull := func(upgrade bool) {
		t.Helper()
		var diag, out bytes.Buffer
		if err := pullRun(pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath,
			CatalogDirs: []string{catalogDir},
			Upgrade:     upgrade,
			DiagOut:     &diag,
			Out:         &out,
		}); err != nil {
			t.Fatalf("resolve (upgrade=%v): %v\ndiagnostics:\n%s", upgrade, err, diag.String())
		}
	}

	pull(false)
	afterFirst := fetches()
	if afterFirst == 0 {
		t.Fatal("the first resolution never fetched, so the refresh below has nothing to re-discover")
	}

	pull(true)
	if fetches() == afterFirst {
		t.Errorf("the refresh did not ask the endpoint again, so it carried a pin forward instead of re-discovering it")
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if _, found := findExternalInput(lf, schemaURL); !found {
		t.Fatalf("the refresh left the external input unpinned; recorded: %+v", lf.ExternalInputs)
	}
}

// TestPullWithNoExternalInputsPinsNothing is the control that keeps every case
// above honest about what it is measuring. The identical fixture with no remote
// reference in its schema renders, pins no external input, and reports none — so a
// pin appearing in the cases above is the reference doing it and not the
// resolution recording something unconditionally.
func TestPullWithNoExternalInputsPinsNothing(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	releasePath, catalogDir := dryRunFixture(t, root, "")
	var diag, out bytes.Buffer
	if err := pullRun(pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		DiagOut:     &diag,
		Out:         &out,
	}); err != nil {
		t.Fatalf("resolve: %v\ndiagnostics:\n%s", err, diag.String())
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.ExternalInputs) != 0 {
		t.Errorf("a Release whose render asks for nothing recorded %d external input(s): %+v", len(lf.ExternalInputs), lf.ExternalInputs)
	}
	if !strings.Contains(out.String(), "external_inputs=0") {
		t.Errorf("the completion line does not report zero external inputs; got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "External inputs were not fully discovered") {
		t.Errorf("a render that asked for nothing was reported as an incomplete discovery; got:\n%s", out.String())
	}
}
