// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gitrepo

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// The clone already took a context before this change; what it did not take was
// the CALLER's. A clone parked on a context nobody can end is indistinguishable
// from a working one from the outside — the caller waits either way — so the
// question is answered against the stack rather than against the reply.
//
// parkedControlClone and parkedStoppedClone are the same function written twice,
// deliberately: ONE stack dump distinguishes two goroutines only if the two
// frames carry two names. Unifying them would erase the distinction the proof
// rests on.
func parkedControlClone(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errCloneReleased
	}
}

func parkedStoppedClone(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errCloneReleased
	}
}

var errCloneReleased = errors.New("clone released by the test")

// parkingClone builds a clone that stays put until either the context it was
// handed ends or the test lets it go — what a real clone of a large repository
// over a slow link does, and the reason a stop has to reach it.
func parkingClone(
	park func(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error,
	entered chan struct{},
	release <-chan struct{},
) cloneFunc {
	return func(ctx context.Context, _, _ string, _ *http.Client, _ transport.AuthMethod) (*git.Repository, error) {
		return nil, park(ctx, entered, release)
	}
}

// goroutineFrames renders every goroutine's stack, growing the buffer until the
// dump fits. A truncated dump would make an absent frame unreadable from a frame
// that simply did not fit, which is the one way this proof could quietly stop
// proving anything.
func goroutineFrames() string {
	for size := 1 << 20; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			return string(buf[:n])
		}
	}
}

// cloneReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped clone stops at all. A machine slow enough to exceed this is a machine
// on which the whole suite has already failed.
const cloneReturnCeiling = 30 * time.Second

// TestStoppingAGitResolutionEndsTheClone is the load-bearing proof for this
// adapter.
//
// Two resolutions are driven to the same point — inside the clone — and one
// caller is then withdrawn. A single stack dump must show the stopped
// resolution's clone frame GONE and the control's STILL PRESENT. The control is
// what makes the absence mean something: without it a dump missing both frames
// would read as success.
//
// A planted defect makes this fail: mint a fresh background context in Resolve
// instead of using the caller's, and the stopped goroutine parks exactly as the
// control does, leaving parkedStoppedClone on the stack.
func TestStoppingAGitResolutionEndsTheClone(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	controlEntered, stoppedEntered := make(chan struct{}), make(chan struct{})

	// Temp dirs are taken on the test's own goroutine, not inside the workers.
	controlRoot, stoppedRoot := t.TempDir(), t.TempDir()

	resolve := func(ctx context.Context, clone cloneFunc, root string) error {
		_, err := (&adapter{clone: clone}).Resolve(ctx, sourceresolver.SourceSpec{
			Kind:          Kind,
			URL:           "https://git.example.com/org/module.git",
			Ref:           "v1.0.0",
			WorkspaceRoot: root,
			CacheDir:      ".aggrega/cache",
			Client:        &http.Client{},
		})
		return err
	}

	controlDone := make(chan error, 1)
	go func() {
		controlDone <- resolve(context.Background(),
			parkingClone(parkedControlClone, controlEntered, release), controlRoot)
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		stoppedDone <- resolve(stopCtx,
			parkingClone(parkedStoppedClone, stoppedEntered, release), stoppedRoot)
	}()

	// Both resolutions are now inside the clone. Until this point there is nothing
	// to compare: a frame that has not been entered is absent for a reason that
	// has nothing to do with cancellation.
	<-controlEntered
	<-stoppedEntered

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case err := <-stoppedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped resolution returned %v; want an error wrapping context.Canceled — "+
				"the clone ended for some reason other than the withdrawal", err)
		}
	case <-time.After(cloneReturnCeiling):
		t.Errorf("the stopped resolution never returned within the liveness ceiling: " +
			"the withdrawal did not reach the clone")
	}

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the
	// stopped one is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlClone") {
		t.Fatalf("the control's clone frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be inside its clone")
	}
	if strings.Contains(frames, "parkedStoppedClone") {
		t.Errorf("the stopped resolution's clone frame is STILL on the stack while the control's " +
			"is there too: the stop reached the reply but not the work")
	}

	select {
	case err := <-controlDone:
		t.Fatalf("the control resolution returned early (%v); it was supposed to stay inside "+
			"its clone for the whole comparison", err)
	default:
	}
}

// TestAStoppedGitResolutionLeavesNoTemporaryCloneBehind is the No-Ghost-
// Infrastructure half. The clone lands in a temporary directory before anything
// is archived, so a resolution that ends part way must still take that directory
// with it rather than leaving a half-cloned tree on disk.
//
// A planted defect makes this fail: drop the deferred removal of the clone
// directory and the withdrawn run leaves it behind.
func TestAStoppedGitResolutionLeavesNoTemporaryCloneBehind(t *testing.T) {
	t.Parallel()

	var cloneDir string
	withdrawn, cancel := context.WithCancel(context.Background())

	a := &adapter{clone: func(ctx context.Context, destDir, _ string, _ *http.Client, _ transport.AuthMethod) (*git.Repository, error) {
		// Recorded from inside the clone, which is the only place the path is
		// known, and written to before the withdrawal so the check below is
		// looking at a directory that genuinely existed.
		cloneDir = destDir
		if _, err := os.Stat(destDir); err != nil {
			t.Errorf("the clone directory was not created: %v", err)
		}
		if err := os.WriteFile(filepath.Join(destDir, "partial.tf"), []byte("# half a clone\n"), 0o644); err != nil {
			t.Errorf("seeding the partial clone: %v", err)
		}
		cancel()
		return nil, ctx.Err()
	}}

	_, err := a.Resolve(withdrawn, sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "https://git.example.com/org/module.git",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        &http.Client{},
	})
	if err == nil {
		t.Fatal("a withdrawn caller got a resolved source back")
	}
	if cloneDir == "" {
		t.Fatal("the clone was never entered, so this test asserts nothing about cleanup")
	}
	if _, statErr := os.Stat(cloneDir); !os.IsNotExist(statErr) {
		t.Errorf("the temporary clone directory %s survived a withdrawn resolution (stat: %v)",
			cloneDir, statErr)
	}
}
