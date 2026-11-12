// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// A dependency resolution that accepts a context and one that observes it look
// identical from the outside: both resolve, both write the same lockfile, and the
// difference appears only when the reader asks the run to stop. A withheld reply
// looks the same either way, which is why reading the code settles nothing and the
// proofs here go to the stack.
//
// parkedControlRegistryFetch and parkedStoppedRegistryFetch are the same function
// written twice, deliberately. The comparison needs ONE stack dump to distinguish
// two goroutines, and a stack frame is identified by the function it is in, so the
// two fetches need two names. Sharing an implementation would erase the distinction
// the proof rests on.
//
// Each stands in for a registry fetch as it really behaves: a step that has left
// the process and is waiting on a registry to answer. It returns when its context
// ends, and otherwise not until the test lets it go.
func parkedControlRegistryFetch(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errPullFetchReleased
	}
}

func parkedStoppedRegistryFetch(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errPullFetchReleased
	}
}

var errPullFetchReleased = errors.New("registry fetch released by the test")

// parkingLibraryPuller is the Tier-3 registry client the resolution reaches when a
// referenced library is not in the local catalog. Rather than serving the library
// it stays inside the fetch, which is what makes "did the stop reach the work"
// answerable from the stack.
type parkingLibraryPuller struct {
	park    func(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error
	entered chan struct{}
	release <-chan struct{}
}

func (p *parkingLibraryPuller) Pull(ctx context.Context, _, _ string) (*oci.PullResult, error) {
	return nil, p.park(ctx, p.entered, p.release)
}

func (p *parkingLibraryPuller) PullLibrary(ctx context.Context, _, _, _ string) (*oci.PullResult, string, error) {
	return nil, "", p.park(ctx, p.entered, p.release)
}

// pullStopCeiling bounds "returns" against "hangs forever" and is NOT a performance
// budget: nothing here measures how long a stop takes, only that a stopped pull
// stops at all. A machine slow enough to exceed this is a machine on which the whole
// suite has already failed.
const pullStopCeiling = 60 * time.Second

// remoteOnlyLibraryWorkspace builds a workspace whose Release reaches a SchemaLibrary
// that is NOT in the local catalog, so the resolution can only satisfy it through the
// registry tier — which is the injected puller. It returns the release path, the
// catalog directory, and the workspace the command layer resolves against.
func remoteOnlyLibraryWorkspace(t *testing.T) (releasePath, catalogDir string, ws *workspaceconfig.ResolvedWorkspace) {
	t.Helper()
	root := t.TempDir()
	releasePath = remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	catalogDir = filepath.Join(root, "catalog")
	// Removing the local copy is what makes the reference genuinely remote: with it
	// present the resolution answers from Tier-1 and never reaches the puller, so the
	// park below would never be entered and the comparison would have nothing to say.
	if err := os.Remove(filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml")); err != nil {
		t.Fatalf("remove the local library so only the registry tier can resolve it: %v", err)
	}
	return releasePath, catalogDir, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
}

// TestStoppingAPullEndsTheRegistryFetch is the load-bearing proof for the pull
// command's own resolution.
//
// Two pulls are driven to the same point — inside a registry fetch — and one caller
// is then withdrawn. A single stack dump, taken while both fetches exist, must show
// the stopped pull's fetch frame GONE and the control's STILL PRESENT. The control
// is what makes the absence mean something: without it a dump missing both frames
// would read as success.
//
// A planted defect makes this fail: hand pullRunWithResult a background context
// instead of the one pullRun was given, and the stopped pull's fetch parks exactly
// as the control does, leaving parkedStoppedRegistryFetch on the stack.
func TestStoppingAPullEndsTheRegistryFetch(t *testing.T) {
	release := make(chan struct{})

	controlPuller := &parkingLibraryPuller{
		park: parkedControlRegistryFetch, entered: make(chan struct{}), release: release,
	}
	stoppedPuller := &parkingLibraryPuller{
		park: parkedStoppedRegistryFetch, entered: make(chan struct{}), release: release,
	}

	// The fixtures are built on the test's own goroutine: the workspace helpers are
	// the harness's, and driving them from a spawned goroutine mixes harness calls
	// into a goroutine the harness does not own.
	controlRelease, controlCatalog, controlWS := remoteOnlyLibraryWorkspace(t)
	stoppedRelease, stoppedCatalog, stoppedWS := remoteOnlyLibraryWorkspace(t)

	// Registered after the workspaces so the parked fetches are let go before any
	// cleanup that waits on them runs.
	defer close(release)

	controlDone := make(chan error, 1)
	go func() {
		controlDone <- pullRun(context.Background(), pullConfig{
			ws: controlWS, ReleasePath: controlRelease, CatalogDirs: []string{controlCatalog},
			puller: controlPuller, DiagOut: io.Discard, Out: io.Discard,
			diag: buildDiagOptions{quiet: true},
		})
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		stoppedDone <- pullRun(stopCtx, pullConfig{
			ws: stoppedWS, ReleasePath: stoppedRelease, CatalogDirs: []string{stoppedCatalog},
			puller: stoppedPuller, DiagOut: io.Discard, Out: io.Discard,
			diag: buildDiagOptions{quiet: true},
		})
	}()

	// Both resolutions are now inside a registry fetch. Until this point there is
	// nothing to compare: a frame that has not been entered is absent for a reason
	// that has nothing to do with cancellation.
	awaitEntered(t, controlPuller.entered, "the control pull's registry fetch")
	awaitEntered(t, stoppedPuller.entered, "the stopped pull's registry fetch")

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached and
	// a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case err := <-stoppedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped pull returned %v; want an error wrapping context.Canceled — "+
				"the resolution ended for some reason other than the withdrawal", err)
		}
	case <-time.After(pullStopCeiling):
		t.Errorf("the stopped pull never returned within the liveness ceiling: " +
			"the withdrawal did not reach the fetch")
	}

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the stopped
	// one is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlRegistryFetch") {
		t.Fatalf("the control pull's fetch frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be inside its fetch")
	}
	if strings.Contains(frames, "parkedStoppedRegistryFetch") {
		t.Errorf("the stopped pull's registry fetch is STILL on the stack while the control's is " +
			"there too: the stop reached the caller but not the work")
	}

	select {
	case err := <-controlDone:
		t.Fatalf("the control pull returned early (%v); it was supposed to stay inside its fetch "+
			"for the whole comparison", err)
	default:
	}
}

// TestThePullCommandRunsUnderTheRunsOwnContext closes the one link the frame
// comparison does not drive: that the `pull` command hands its own run's context to
// the resolution rather than starting a fresh one.
//
// It is asked by withdrawing the run before it begins and reading which failure comes
// back. The control is the same invocation under a live context, which must fail for
// its own reason — its Release names an Aggregate that is not there — so the
// assertion distinguishes a withdrawn run from any other failure rather than matching
// whatever error happens to arrive.
//
// A planted defect makes this fail: replace the command's context with a background
// one anywhere between the command and the resolution, and the withdrawn invocation
// reports the missing Aggregate exactly as the control does.
func TestThePullCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	endedRoot, endedRelease, endedCatalog := localLibraryWorkspace(t)
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runPullThroughTheCommandTree(ended, endedRoot, endedRelease, endedCatalog)
	if endedErr == nil {
		t.Fatal("a pull whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn pull ended with %v; want a failure naming the withdrawal, which is "+
			"what the resolution reports when the command's own context reaches it", endedErr)
	}

	// What a withdrawn pull leaves on disk is the other half of the guarantee, and
	// it is the half a reader has to live with: the lockfile is written last, past
	// the final point the context is observed, so a run that stopped wrote none. A
	// half-written pin set would be worse than no stop at all, because the next
	// build would read it as a complete one.
	if _, err := os.Stat(filepath.Join(endedRoot, "release.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a withdrawn pull left %s behind (stat reported %v); a stopped resolution must "+
			"leave the pins that were on disk exactly as they were",
			filepath.Join(endedRoot, "release.lock"), err)
	}

	// A fresh workspace for the control, because the withdrawn run above may have
	// left partial state behind and the control has to be judged on its own inputs.
	liveRoot, liveRelease, liveCatalog := localLibraryWorkspace(t)
	if liveErr := runPullThroughTheCommandTree(context.Background(), liveRoot, liveRelease, liveCatalog); liveErr != nil {
		t.Errorf("the control pull failed (%v); the assertion above can only distinguish a "+
			"withdrawn run from a working one if the same invocation succeeds under a live context", liveErr)
	}
}

// localLibraryWorkspace is the same fixture the frame comparison uses with its
// library left in place, so every reference resolves from the local catalog and a
// pull under a live context completes. It returns the workspace root, the Release
// path and the catalog directory.
func localLibraryWorkspace(t *testing.T) (wsRoot, releasePath, catalogDir string) {
	t.Helper()
	root := t.TempDir()
	releasePath = remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	return root, releasePath, filepath.Join(root, "catalog")
}

// runPullThroughTheCommandTree dispatches the real `pull` command against the
// declared workspace and returns what it reported, with both of cobra's own streams
// discarded so the test's output carries only its own findings.
func runPullThroughTheCommandTree(ctx context.Context, wsRoot, releasePath, catalogDir string) error {
	root := newRootCmd()
	root.SetArgs([]string{
		"--workspace-root", wsRoot,
		"pull", "--release", releasePath, "--catalog", catalogDir, "--quiet",
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	_, err := root.ExecuteContextC(ctx)
	return err
}

// awaitEntered blocks until a parked operation reports that it is inside the work,
// and fails with the operation's name rather than hanging the package if it never
// gets there.
func awaitEntered(t *testing.T, entered <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(pullStopCeiling):
		t.Fatalf("%s was never entered within the liveness ceiling, so there is nothing to "+
			"compare: the fixture did not reach the registry tier", what)
	}
}
