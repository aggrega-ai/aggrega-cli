// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
)

// A command that accepts the run's context and one that mints its own look identical
// from the outside: both produce the same output, and the difference appears only
// when the reader asks the run to stop. The proofs here ask each command the question
// in the strongest form that command admits — a frame comparison where the work can
// be parked, and a withdrawn-versus-live comparison where it cannot, with the reason
// written at the test.

// parkedControlHint and parkedStoppedHint are the same function written twice,
// deliberately. The comparison needs ONE stack dump to distinguish two goroutines,
// and a stack frame is identified by the function it is in, so the two lookups need
// two names. Sharing an implementation would erase the distinction the proof rests
// on.
//
// Each stands in for the registry lookup the install hint makes: a step that has left
// the process and is waiting on a registry to answer.
func parkedControlHint(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) {
	close(entered)
	select {
	case <-ctx.Done():
	case <-release:
	}
}

func parkedStoppedHint(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) {
	close(entered)
	select {
	case <-ctx.Done():
	case <-release:
	}
}

// hintStopCeiling bounds "returns" against "hangs forever" and is NOT a performance
// budget: nothing here measures how long a stop takes, only that a stopped lookup
// stops at all.
const hintStopCeiling = 60 * time.Second

// TestStoppingARunEndsTheInstallHintLookup proves the unknown-command install hint
// runs under the run's own context rather than one the error path minted.
//
// Two runs are driven to the same point — inside the discoverability lookup — and one
// is then withdrawn. A single stack dump, taken while both lookups exist, must show
// the withdrawn run's lookup frame GONE and the control's STILL PRESENT.
//
// The two runs share one seam, because the seam is process-global; they are told
// apart by the command name each was given, which is the same value the real hint
// looks up. A single seam is what production has, so routing on the name tests the
// real shape rather than a doubled one.
//
// A planted defect makes this fail: hand the seam context.Background() instead of the
// run's, and the withdrawn run's lookup parks exactly as the control does, leaving
// parkedStoppedHint on the stack.
func TestStoppingARunEndsTheInstallHintLookup(t *testing.T) {
	// Not parallel: it swaps the process-global discoverability seam.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, nil)
	withDispatcher(t, &fakeDispatcher{})

	release := make(chan struct{})
	defer close(release)
	controlEntered := make(chan struct{})
	stoppedEntered := make(chan struct{})

	withDiscoverable(t, func(ctx context.Context, name string) bool {
		switch name {
		case "control-probe-command":
			parkedControlHint(ctx, controlEntered, release)
		case "stopped-probe-command":
			parkedStoppedHint(ctx, stoppedEntered, release)
		}
		return false
	})

	controlDone := make(chan int, 1)
	go func() { controlDone <- runUnknownCommand(context.Background(), "control-probe-command") }()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan int, 1)
	go func() { stoppedDone <- runUnknownCommand(stopCtx, "stopped-probe-command") }()

	// Both lookups are now inside the seam. Until this point there is nothing to
	// compare: a frame that has not been entered is absent for a reason that has
	// nothing to do with cancellation.
	awaitEntered(t, controlEntered, "the control run's install-hint lookup")
	awaitEntered(t, stoppedEntered, "the withdrawn run's install-hint lookup")

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached and
	// a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case <-stoppedDone:
	case <-time.After(hintStopCeiling):
		t.Errorf("the withdrawn run's install-hint lookup never returned within the liveness " +
			"ceiling: the withdrawal did not reach it")
	}

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the stopped
	// one is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlHint") {
		t.Fatalf("the control run's lookup frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be inside its lookup")
	}
	if strings.Contains(frames, "parkedStoppedHint") {
		t.Errorf("the withdrawn run's install-hint lookup is STILL on the stack while the control's " +
			"is there too: the stop reached the run but not the lookup")
	}

	select {
	case <-controlDone:
		t.Fatal("the control run returned early; it was supposed to stay inside its lookup for the " +
			"whole comparison")
	default:
	}
}

// runUnknownCommand dispatches a name the command tree does not recognise through the
// real exit-code boundary, which is the one place the install hint is reached from.
func runUnknownCommand(ctx context.Context, name string) int {
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{name})
	return executeRoot(ctx, root)
}

// parkedControlEnumeration and parkedStoppedEnumeration are the same function written
// twice, for the reason given above: one dump, two frames, two names.
func parkedControlEnumeration(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errEnumerationReleased
	}
}

func parkedStoppedEnumeration(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errEnumerationReleased
	}
}

var errEnumerationReleased = errors.New("registry enumeration released by the test")

// parkingRepoLister stands in for a registry that stays inside the repository walk
// until either the context it was handed ends or the test lets it go.
type parkingRepoLister struct {
	park    func(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error
	entered chan struct{}
	release <-chan struct{}
}

func (p *parkingRepoLister) Repositories(ctx context.Context, _ string, _ func([]string) error) error {
	return p.park(ctx, p.entered, p.release)
}

var _ registryops.RepoLister = (*parkingRepoLister)(nil)

// TestStoppingADiscoverabilityProbeEndsTheEnumeration closes the second half of the
// install-hint path: that the context the seam is handed reaches the registry
// enumeration underneath it.
//
// A planted defect makes this fail: hand searchRegistry a background context instead
// of the one discoverableViaSearch was given, and the stopped probe's walk parks
// exactly as the control's does, leaving parkedStoppedEnumeration on the stack.
func TestStoppingADiscoverabilityProbeEndsTheEnumeration(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	controlLister := &parkingRepoLister{
		park: parkedControlEnumeration, entered: make(chan struct{}), release: release,
	}
	stoppedLister := &parkingRepoLister{
		park: parkedStoppedEnumeration, entered: make(chan struct{}), release: release,
	}

	controlDone := make(chan bool, 1)
	go func() { controlDone <- discoverableViaSearch(context.Background(), controlLister, "", "anything") }()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan bool, 1)
	go func() { stoppedDone <- discoverableViaSearch(stopCtx, stoppedLister, "", "anything") }()

	awaitEntered(t, controlLister.entered, "the control probe's registry enumeration")
	awaitEntered(t, stoppedLister.entered, "the stopped probe's registry enumeration")

	stop()

	select {
	case got := <-stoppedDone:
		if got {
			t.Errorf("the stopped probe reported the name discoverable; a walk that was withdrawn " +
				"found nothing and must degrade the hint to not-discoverable")
		}
	case <-time.After(hintStopCeiling):
		t.Errorf("the stopped probe never returned within the liveness ceiling: the withdrawal did " +
			"not reach the enumeration")
	}

	frames := goroutineFrames()

	if !strings.Contains(frames, "parkedControlEnumeration") {
		t.Fatalf("the control probe's enumeration frame is ABSENT from the dump, so the comparison " +
			"below would pass for the wrong reason; the control must still be inside its walk")
	}
	if strings.Contains(frames, "parkedStoppedEnumeration") {
		t.Errorf("the stopped probe's enumeration is STILL on the stack while the control's is there " +
			"too: the stop reached the caller but not the walk")
	}

	select {
	case <-controlDone:
		t.Fatal("the control probe returned early; it was supposed to stay inside its walk for the " +
			"whole comparison")
	default:
	}
}

// TestTheValidateCommandRunsUnderTheRunsOwnContext proves `validate --release` hands
// its own run's context to the validation rather than starting a fresh one.
//
// There is no seam beneath this command a test can park in — the closure resolution
// and the validation build their own catalog view — so the question is asked by
// withdrawing the run before it begins and reading which failure comes back. The
// control is the same invocation under a live context, which must fail for its own
// reason, so the assertion distinguishes a withdrawn run from any other failure
// rather than matching whatever error happens to arrive.
//
// A planted defect makes this fail: replace the command's context with a background
// one anywhere between the command and the validation, and the withdrawn invocation
// reports the control's diagnostics instead of the withdrawal.
func TestTheValidateCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	root, releasePath, catalogDir := probeReleaseWorkspace(t)

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runThroughTheCommandTree(ended, []string{
		"--workspace-root", root, "validate", "--release", releasePath, "--catalog", catalogDir,
	})
	if endedErr == nil {
		t.Fatal("a validation whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn validation ended with %v; want a failure naming the withdrawal, "+
			"which is what it reports when the command's own context reaches it", endedErr)
	}

	liveErr := runThroughTheCommandTree(context.Background(), []string{
		"--workspace-root", root, "validate", "--release", releasePath, "--catalog", catalogDir,
	})
	if liveErr == nil {
		t.Fatal("the control validation reported success; this Release has no Recipe for its " +
			"component and must report diagnostics")
	}
	if strings.Contains(liveErr.Error(), context.Canceled.Error()) {
		t.Errorf("the control validation reported the withdrawal too, so the assertion above cannot "+
			"distinguish a withdrawn run from any other failure: %v", liveErr)
	}
}

// TestTheInspectCommandRunsUnderTheRunsOwnContext proves `inspect <release>` hands its
// own run's context to the graph construction rather than starting a fresh one. It is
// the second of the two sites the per-kind renderer reshape opened, and it is the one
// that reaches work: the resolved view builds the graph a build would render.
//
// A planted defect makes this fail: pass context.Background() to BuildGraph instead of
// the renderer's parameter, and the withdrawn invocation renders the entity exactly as
// the control does.
func TestTheInspectCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	root, releasePath, catalogDir := probeReleaseWorkspace(t)

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runThroughTheCommandTree(ended, []string{
		"--workspace-root", root, "inspect", releasePath, "--catalog", catalogDir,
	})
	if endedErr == nil {
		t.Fatal("an inspection whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn inspection ended with %v; want a failure naming the withdrawal, "+
			"which is what the graph construction reports when the renderer's context reaches it",
			endedErr)
	}

	if liveErr := runThroughTheCommandTree(context.Background(), []string{
		"--workspace-root", root, "inspect", releasePath, "--catalog", catalogDir,
	}); liveErr != nil {
		t.Errorf("the control inspection failed (%v); the assertion above can only distinguish a "+
			"withdrawn run from a working one if the same invocation succeeds under a live context",
			liveErr)
	}
}

// TestThePluginSyncCommandRunsUnderTheRunsOwnContext proves `plugin sync` hands its
// own run's context to the record alignment, and that the context reaches the guest
// the alignment describes rather than stopping at the operation's edge.
//
// The withdrawn run's failure comes from the runtime closing the module, which is
// what a guest load looks like when the context it runs under ends — so this asserts
// the stop reached the guest, not merely that the caller was released.
//
// A planted defect makes this fail: hand pluginops.Sync a background context instead
// of the command's, and the withdrawn invocation records the host functions exactly
// as the control does.
func TestThePluginSyncCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	wsRoot, entityPath, pluginPath := writeLargeGuestPlugin(t, "")

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runThroughTheCommandTree(ended, []string{
		"--workspace-root", wsRoot, "plugin", "sync", entityPath, "--plugin", pluginPath,
	})
	if endedErr == nil {
		t.Fatal("a record alignment whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn alignment ended with %v; want a failure naming the withdrawal, "+
			"which is what the guest load reports when the command's own context reaches it", endedErr)
	}

	if liveErr := runThroughTheCommandTree(context.Background(), []string{
		"--workspace-root", wsRoot, "plugin", "sync", entityPath, "--plugin", pluginPath,
	}); liveErr != nil {
		t.Errorf("the control alignment failed (%v); the assertion above can only distinguish a "+
			"withdrawn run from a working one if the same invocation succeeds under a live context",
			liveErr)
	}
}

// runThroughTheCommandTree dispatches the real command tree with the given arguments
// and returns what it reported, with both of cobra's own streams discarded so the
// test's output carries only its own findings.
func runThroughTheCommandTree(ctx context.Context, args []string) error {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	_, err := root.ExecuteContextC(ctx)
	return err
}

// probeReleaseWorkspace writes a Release with a resolvable Aggregate, Manifest and
// Component, and a declared workspace so resolution never climbs above the temporary
// tree. It carries no Recipe, which is what makes a live validation fail for its own
// reason while a live inspection still renders.
func probeReleaseWorkspace(t *testing.T) (wsRoot, releasePath, catalogDir string) {
	t.Helper()
	root := t.TempDir()
	catalogDir = filepath.Join(root, "catalog")
	if err := os.MkdirAll(filepath.Join(catalogDir, "components"), 0o755); err != nil {
		t.Fatalf("create the probe catalog: %v", err)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1alpha1\n"+
		"kind: Aggregate\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n"+
		"spec:\n  components:\n    api:\n      type: server\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1alpha1\n"+
		"kind: Manifest\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n"+
		"spec:\n  mappings:\n    components:\n      type:\n        server: server\n")
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, "apiVersion: aggrega.ai/v1alpha1\nkind: Release\n"+
		"metadata:\n  name: probe\n  environment: testing\n  version: \"1.0.0\"\n"+
		"spec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n"+
		"  boundaries:\n    main:\n      directory: ./dist/main\n      components:\n        api: {}\n")
	writeFixture(t, filepath.Join(catalogDir, "components", "server.yaml"), "apiVersion: aggrega.ai/v1alpha1\n"+
		"kind: Component\nmetadata:\n  name: server\n  version: \"1.0.0\"\n  labels:\n"+
		"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs: []\n")
	return root, releasePath, catalogDir
}
