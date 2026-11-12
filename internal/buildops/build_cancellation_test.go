// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package buildops

import (
	"context"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// A run that carries a context and a run that carries a cancellable one look
// identical from the outside: both accept the argument, both compile, and both
// produce the same output. The difference is only what happens when the context
// ends — and a step that is waiting on something outside the process is
// indistinguishable, from any return value, from one that is simply slow. So
// the question is asked of the STACK: with one fill parked under the run's own
// context and one parked under a context nothing ends, a single goroutine dump
// says which of the two ended.
//
// parkedControlFill and parkedStoppedFill are the same function written twice,
// deliberately. The comparison needs ONE dump to distinguish two goroutines, and
// a stack frame is identified by the function it is in, so the two operations
// need two names. Sharing an implementation would erase the distinction the
// proof rests on, which is why this duplicate must not be unified.
//
// Each stands in for the dependency fill as it really behaves: a step that has
// left the process and is waiting on a registry to answer. It returns when its
// context ends, and otherwise not at all.
func parkedControlFill(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

func parkedStoppedFill(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

// buildFrames renders every goroutine's stack, growing the buffer until the
// dump fits. A truncated dump would make an absent frame unreadable from a
// frame that simply did not fit, which is the one way this proof could quietly
// stop proving anything.
func buildFrames() string {
	for size := 1 << 20; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			return string(buf[:n])
		}
	}
}

// buildProbeCeiling bounds "returns" against "never returns" and is NOT a
// performance budget: nothing here measures how quickly a run unwinds, only
// that it unwinds at all.
const buildProbeCeiling = 60 * time.Second

// buildSettleWindow is a settle window, not a budget: it gives a run that IS
// going to unwind the time to do so, so the absence of its frame below means it
// stopped rather than that it had not stopped yet. The dump is taken here
// rather than after Run returns because a run that never observed the context
// would never return, and a dump that is never written reports only that
// nothing happened.
const buildSettleWindow = 2 * time.Second

// TestEndingTheCallersContextEndsTheBuildsWork is the load-bearing proof that
// the context handed to Run reaches the work rather than merely the signature.
//
// The run is driven through the real entry point — the real workspace
// resolution, the real catalog load, the real lockfile step — and parks in the
// dependency fill, which is where a build waits on a registry. A control parks
// at the same depth under a context nothing ends. One goroutine dump, taken
// while both goroutines exist, must show the run's fill GONE and the control's
// frame STILL PRESENT.
//
// A planted defect makes this fail: mint a fresh background context inside Run
// instead of deriving one from the caller's, and the run's fill parks exactly
// as the control does, leaving parkedStoppedFill on the stack.
func TestEndingTheCallersContextEndsTheBuildsWork(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, "kind: Release\n")

	// A frozen environment lookup keeps resolution deterministic and
	// parallel-safe, which is what the variadic parameter exists for.
	rw, err := workspaceconfig.ResolveWithoutGlobal(root, root, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolving the probe workspace: %v", err)
	}

	// The control is parked on a context this test never ends, so its frame must
	// still be on the stack when the dump is taken. Without it, a dump missing
	// both frames would read as success.
	controlEntered := make(chan struct{})
	go func() { _ = parkedControlFill(context.Background(), controlEntered) }()
	<-controlEntered

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stoppedEntered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, runErr := Run(ctx, Config{
			Workspace:   rw,
			ReleasePath: releasePath,
			CatalogDirs: []string{root},
			// AutoPull is what routes the lockfile step through the supplied fill,
			// which is the build's own network-reaching seam.
			Options: RunOptions{AutoPull: true},
			Pull: func(pullCtx context.Context, _, _ bool, _ pipeline.Options, _, _ io.Writer) (*compilerops.PreviewResult, error) {
				return nil, parkedStoppedFill(pullCtx, stoppedEntered)
			},
			DiagOut: io.Discard,
			Out:     io.Discard,
		})
		done <- runErr
	}()

	select {
	case <-stoppedEntered:
	case runErr := <-done:
		t.Fatalf("the run ended before it reached the fill, so nothing was parked to stop: %v", runErr)
	case <-time.After(buildProbeCeiling):
		t.Fatal("the run never reached the fill within the liveness ceiling")
	}

	cancel()
	time.Sleep(buildSettleWindow)
	frames := buildFrames()

	// This is the comparison's needle. It runs the same matcher over the same
	// dump and must find the control's parked frame, so a failure to find the
	// stopped one below is a fact about the stack rather than about the
	// instrument reading it.
	if !strings.Contains(frames, "parkedControlFill") {
		t.Fatalf("the control's frame is ABSENT from the dump, so the comparison below would pass "+
			"for the wrong reason; the control must still be parked\ndump:\n%s", frames)
	}
	if strings.Contains(frames, "parkedStoppedFill") {
		t.Errorf("the build's fill is STILL parked while the control's frame is there too: the "+
			"context reached the signature but not the work\ndump:\n%s", frames)
	}

	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("the run reported success after its work was stopped part way")
		}
		if !strings.Contains(runErr.Error(), context.Canceled.Error()) {
			t.Errorf("the run ended with %v; want a failure naming the withdrawal", runErr)
		}
	case <-time.After(buildProbeCeiling):
		t.Fatal("the run never returned within the liveness ceiling after its context ended")
	}
}

// TestARunWhoseContextHasAlreadyEndedDoesNoWork pins the entry check: a caller
// whose reader withdrew the run before it began gets a stated reason rather
// than a build that reads the file system and then reports nothing.
//
// The control is the same call under a live context, which must NOT report the
// withdrawal — it gets as far as the lockfile step and halts there. Without it,
// an entry check that reported the withdrawal unconditionally would pass.
func TestARunWhoseContextHasAlreadyEndedDoesNoWork(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, "kind: Release\n")

	rw, err := workspaceconfig.ResolveWithoutGlobal(root, root, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolving the probe workspace: %v", err)
	}
	cfg := Config{
		Workspace:   rw,
		ReleasePath: releasePath,
		CatalogDirs: []string{root},
		DiagOut:     io.Discard,
		Out:         io.Discard,
	}

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	_, endedErr := Run(ended, cfg)
	if endedErr == nil {
		t.Fatal("a run whose context had already ended reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("a withdrawn run ended with %v; want a failure naming the withdrawal", endedErr)
	}

	_, liveErr := Run(context.Background(), cfg)
	if liveErr == nil {
		t.Fatal("the control run reported success; it has no lockfile and must halt")
	}
	if strings.Contains(liveErr.Error(), context.Canceled.Error()) {
		t.Errorf("the control run reported the withdrawal too, so the assertion above cannot "+
			"distinguish a withdrawn run from any other failure: %v", liveErr)
	}
}
