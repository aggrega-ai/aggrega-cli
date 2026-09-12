// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceresolver_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

// A dispatcher that accepts a context and hands the adapter a fresh one of its
// own looks identical from the outside to one that forwards the caller's: the
// caller gets no reply either way, and the only difference is whether the fetch
// underneath is still running. So this is written against the stack rather than
// against the reply.

// parkedControlDispatch and parkedStoppedDispatch are the same function written
// twice, deliberately. The comparison needs ONE stack dump to tell two goroutines
// apart, and a frame is identified by the function it is in, so the two sides
// need two names. Unifying them would erase the distinction the proof rests on.
func parkedControlDispatch(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errDispatchReleased
	}
}

func parkedStoppedDispatch(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errDispatchReleased
	}
}

var errDispatchReleased = errors.New("adapter released by the test")

// parkingAdapter stays inside Resolve until either the context it was handed
// ends or the test lets it go — the behaviour a real adapter's transport has,
// and the reason a stop has to reach the adapter rather than the step after it.
type parkingAdapter struct {
	kind    string
	park    func(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error
	entered chan struct{}
	release <-chan struct{}
}

func (p *parkingAdapter) Kind() string { return p.kind }

func (p *parkingAdapter) Resolve(ctx context.Context, _ sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	if err := p.park(ctx, p.entered, p.release); err != nil {
		return nil, err
	}
	return &sourceresolver.ResolvedSource{Kind: p.kind}, nil
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

// resolveReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped resolution stops at all. A machine slow enough to exceed this is a
// machine on which the whole suite has already failed.
const resolveReturnCeiling = 30 * time.Second

// TestStoppingAResolutionEndsTheAdaptersWork proves the dispatcher hands the
// caller's context down rather than substituting one of its own.
//
// Two resolutions are driven to the same point — inside an adapter — and one is
// then stopped. A single stack dump, taken while both goroutines exist, must show
// the stopped one's adapter frame GONE and the control's STILL PRESENT. The
// control is what makes the absence mean something: without it a dump missing
// both frames would read as success.
//
// A planted defect makes this fail: hand the adapter a fresh background context
// in Dispatcher.Resolve instead of the caller's, and the stopped goroutine parks
// exactly as the control does, leaving parkedStoppedDispatch on the stack.
func TestStoppingAResolutionEndsTheAdaptersWork(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	controlAdapter := &parkingAdapter{
		kind: "parked-control", park: parkedControlDispatch,
		entered: make(chan struct{}), release: release,
	}
	stoppedAdapter := &parkingAdapter{
		kind: "parked-stopped", park: parkedStoppedDispatch,
		entered: make(chan struct{}), release: release,
	}

	controlDispatcher := sourceresolver.NewDispatcher()
	if err := controlDispatcher.Register(controlAdapter); err != nil {
		t.Fatalf("Register(control): %v", err)
	}
	stoppedDispatcher := sourceresolver.NewDispatcher()
	if err := stoppedDispatcher.Register(stoppedAdapter); err != nil {
		t.Fatalf("Register(stopped): %v", err)
	}

	controlDone := make(chan error, 1)
	go func() {
		_, err := controlDispatcher.Resolve(context.Background(),
			sourceresolver.SourceSpec{Kind: "parked-control"})
		controlDone <- err
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		_, err := stoppedDispatcher.Resolve(stopCtx,
			sourceresolver.SourceSpec{Kind: "parked-stopped"})
		stoppedDone <- err
	}()

	// Both resolutions are now inside an adapter. Until this point there is
	// nothing to compare: a frame that has not been entered is absent for a
	// reason that has nothing to do with cancellation.
	<-controlAdapter.entered
	<-stoppedAdapter.entered

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case err := <-stoppedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped resolution returned %v; want an error wrapping context.Canceled — "+
				"the adapter returned for some reason other than the withdrawal", err)
		}
	case <-time.After(resolveReturnCeiling):
		t.Errorf("the stopped resolution never returned within the liveness ceiling: " +
			"the withdrawal did not reach the adapter")
	}

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the
	// stopped one below is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlDispatch") {
		t.Fatalf("the control's adapter frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be inside its adapter")
	}
	if strings.Contains(frames, "parkedStoppedDispatch") {
		t.Errorf("the stopped resolution's adapter frame is STILL on the stack while the control's " +
			"is there too: the stop reached the reply but not the work")
	}

	select {
	case err := <-controlDone:
		t.Fatalf("the control resolution returned early (%v); it was supposed to stay inside "+
			"its adapter for the whole comparison", err)
	default:
	}
}

// TestAResolutionWhoseContextHasAlreadyEndedRoutesNowhere covers the other half:
// a caller that withdrew before resolution began is answered with the withdrawal
// rather than with whichever transport failure the adapter's own machinery would
// have produced. The live-context control must fail DIFFERENTLY — it reaches the
// routing table and reports an unknown kind — so a run where both rows report the
// same thing cannot pass.
//
// A planted defect makes this fail: remove the entry check from
// Dispatcher.Resolve and the withdrawn row reports the unknown-kind refusal,
// matching the control.
func TestAResolutionWhoseContextHasAlreadyEndedRoutesNowhere(t *testing.T) {
	t.Parallel()
	d := sourceresolver.NewDispatcher()

	withdrawn, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.Resolve(withdrawn, sourceresolver.SourceSpec{Kind: "no-such-kind"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a withdrawn caller got %v; want an error wrapping context.Canceled", err)
	}
	if errors.Is(err, sourceresolver.ErrUnknownKind()) {
		t.Errorf("a withdrawn caller was answered by the routing table (%v); the withdrawal "+
			"should have been reported before any routing", err)
	}

	_, liveErr := d.Resolve(context.Background(), sourceresolver.SourceSpec{Kind: "no-such-kind"})
	if !errors.Is(liveErr, sourceresolver.ErrUnknownKind()) {
		t.Fatalf("the live-context control got %v; want ErrUnknownKind — without a control that "+
			"fails differently, the assertion above proves nothing", liveErr)
	}
}
