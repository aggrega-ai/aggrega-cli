// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A resolver that accepts a context and mints a fresh one for the registry pull
// looks identical from the outside to one that forwards the caller's: the caller
// gets no reply either way, and the only difference is whether the pull
// underneath is still running. So this is written against the stack rather than
// against the reply.

// parkedControlPull and parkedStoppedPull are the same function written twice,
// deliberately. The comparison needs ONE stack dump to tell two goroutines apart,
// and a frame is identified by the function it is in, so the two sides need two
// names. Unifying them would erase the distinction the proof rests on.
func parkedControlPull(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errPullReleased
	}
}

func parkedStoppedPull(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errPullReleased
	}
}

var errPullReleased = errors.New("puller released by the test")

// parkingPuller stays inside PullPlugin until either the context it was handed
// ends or the test lets it go — the behaviour a real registry transfer has, and
// the reason a stop has to reach the pull rather than the step after it.
type parkingPuller struct {
	park    func(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error
	entered chan struct{}
	release <-chan struct{}
}

func (p *parkingPuller) PullPlugin(ctx context.Context, _ string) ([]byte, error) {
	if err := p.park(ctx, p.entered, p.release); err != nil {
		return nil, err
	}
	return []byte("\x00asm"), nil
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

// pullReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped resolution stops at all. A machine slow enough to exceed this is a
// machine on which the whole suite has already failed.
const pullReturnCeiling = 30 * time.Second

// registryOnlyURI names a plugin no tier above the registry can serve: it is not
// embedded in the binary, it is not a filesystem path, and the resolvers below
// are built with no colocated directories and no content cache. So a resolution
// of it reaches the registry tier and nothing else, which is what makes the pull
// the only thing the frames below can be about.
const registryOnlyURI = "registry.example/parking/plugin:1.0.0"

// TestStoppingAPluginResolutionEndsTheRegistryPull proves the cascade hands the
// caller's context to the pull rather than minting one of its own.
//
// Two resolutions are driven to the same point — inside a puller — and one is
// then stopped. A single stack dump, taken while both goroutines exist, must show
// the stopped one's pull frame GONE and the control's STILL PRESENT. The control
// is what makes the absence mean something: without it a dump missing both frames
// would read as success.
//
// A planted defect makes this fail: hand the puller context.Background() in
// ResolveContext instead of the caller's, and the stopped goroutine parks exactly
// as the control does, leaving parkedStoppedPull on the stack.
func TestStoppingAPluginResolutionEndsTheRegistryPull(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	controlPuller := &parkingPuller{
		park: parkedControlPull, entered: make(chan struct{}), release: release,
	}
	stoppedPuller := &parkingPuller{
		park: parkedStoppedPull, entered: make(chan struct{}), release: release,
	}

	controlResolver := NewResolver(ResolutionContext{Puller: controlPuller}, PinPostureNone, PinMaps{})
	stoppedResolver := NewResolver(ResolutionContext{Puller: stoppedPuller}, PinPostureNone, PinMaps{})

	controlDone := make(chan error, 1)
	go func() {
		_, err := controlResolver.ResolveContext(context.Background(), registryOnlyURI)
		controlDone <- err
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		_, err := stoppedResolver.ResolveContext(stopCtx, registryOnlyURI)
		stoppedDone <- err
	}()

	// Both resolutions are now inside a puller. Until this point there is nothing
	// to compare: a frame that has not been entered is absent for a reason that
	// has nothing to do with cancellation.
	<-controlPuller.entered
	<-stoppedPuller.entered

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case err := <-stoppedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped resolution returned %v; want an error wrapping context.Canceled — "+
				"the pull returned for some reason other than the withdrawal", err)
		}
	case <-time.After(pullReturnCeiling):
		t.Errorf("the stopped resolution never returned within the liveness ceiling: " +
			"the withdrawal did not reach the pull")
	}

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the stopped
	// one below is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlPull") {
		t.Fatalf("the control's pull frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be inside its puller")
	}
	if strings.Contains(frames, "parkedStoppedPull") {
		t.Errorf("the stopped resolution's pull frame is STILL on the stack while the control's " +
			"is there too: the stop reached the reply but not the work")
	}

	select {
	case err := <-controlDone:
		t.Fatalf("the control resolution returned early (%v); it was supposed to stay inside "+
			"its puller for the whole comparison", err)
	default:
	}
}

// TestAPluginResolutionWhoseContextHasAlreadyEndedConsultsNoTier covers the other
// half: a caller that withdrew before resolution began is answered with the
// withdrawal rather than walking the cascade first. The live-context control must
// fail DIFFERENTLY — it walks every tier and reports exhaustion — so a run where
// both rows report the same thing cannot pass.
//
// A planted defect makes this fail: remove the entry check from ResolveContext
// and the withdrawn row reports the tier-exhaustion refusal, matching the control.
func TestAPluginResolutionWhoseContextHasAlreadyEndedConsultsNoTier(t *testing.T) {
	t.Parallel()
	r := NewResolver(ResolutionContext{}, PinPostureNone, PinMaps{})

	withdrawn, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.ResolveContext(withdrawn, registryOnlyURI)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a withdrawn caller got %v; want an error wrapping context.Canceled", err)
	}
	if strings.Contains(errString(err), "not resolvable") {
		t.Errorf("a withdrawn caller was answered by the cascade (%v); the withdrawal should "+
			"have been reported before any tier was consulted", err)
	}

	_, liveErr := r.ResolveContext(context.Background(), registryOnlyURI)
	if !strings.Contains(errString(liveErr), "not resolvable") {
		t.Fatalf("the live-context control got %v; want the tier-exhaustion refusal — without a "+
			"control that fails differently, the assertion above proves nothing", liveErr)
	}
}

// TestTheSeamFacingResolveStillWalksTheCascade pins that giving the cascade a
// cancellation parameter left the seam-facing form behaving exactly as it did.
// Lookup binds Resolve, so a regression here would change every socket's
// resolution while the cancellation tests above went on passing.
func TestTheSeamFacingResolveStillWalksTheCascade(t *testing.T) {
	t.Parallel()
	r := NewResolver(ResolutionContext{}, PinPostureNone, PinMaps{})

	_, err := r.Resolve(registryOnlyURI)
	if !strings.Contains(errString(err), "not resolvable") {
		t.Errorf("Resolve(%q) returned %v; want the tier-exhaustion refusal, exactly as before "+
			"the cascade took a cancellation parameter", registryOnlyURI, err)
	}

	// A Resolver bound as a Lookup is the seam every socket uses; it must still
	// satisfy the type and still resolve.
	var seam Lookup = r.Resolve
	if _, seamErr := seam(registryOnlyURI); !strings.Contains(errString(seamErr), "not resolvable") {
		t.Errorf("the seam-bound method value returned %v; want the tier-exhaustion refusal", seamErr)
	}
}

// errString renders an error for substring matching without tripping over a nil.
func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
