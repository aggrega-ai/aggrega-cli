// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compose

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

// These park on the SERVER side, and that is the point. A resolution that
// returns when its caller withdraws proves only that the caller was released; it
// says nothing about whether the download stopped. A server still sitting inside
// its handler is a fetch still in flight. So the question asked here is whether
// the other end of the connection noticed — which it can only do if the transfer
// was actually abandoned on the wire.
//
// parkedControlFragmentFetch and parkedStoppedFragmentFetch are the same function
// written twice, deliberately: ONE stack dump distinguishes two goroutines only
// if the two frames carry two names. Unifying them would erase the distinction
// the proof rests on.
func parkedControlFragmentFetch(reqCtx context.Context, entered chan<- struct{}, release <-chan struct{}) {
	close(entered)
	select {
	case <-reqCtx.Done():
	case <-release:
	}
}

func parkedStoppedFragmentFetch(reqCtx context.Context, entered chan<- struct{}, release <-chan struct{}) {
	close(entered)
	select {
	case <-reqCtx.Done():
	case <-release:
	}
}

// parkingFragmentServer serves a fragment URL that never answers: the handler
// stays put until the client goes away or the test lets it go. A real slow
// upstream behaves this way, which is why a stop has to reach the transfer rather
// than the step after it.
func parkingFragmentServer(
	t *testing.T,
	park func(reqCtx context.Context, entered chan<- struct{}, release <-chan struct{}),
	release <-chan struct{},
) (url string, entered chan struct{}) {
	t.Helper()
	entered = make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		park(r.Context(), entered, release)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/compose.yaml", entered
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

// fetchReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped fetch stops at all. A machine slow enough to exceed this is a machine
// on which the whole suite has already failed.
const fetchReturnCeiling = 30 * time.Second

// TestStoppingAComposeResolutionEndsTheFetchOnTheWire is the load-bearing proof
// for this adapter.
//
// Two fragment fetches are driven to the same point — the server is inside its
// handler with the response unwritten — and one caller is then withdrawn. A
// single stack dump must show the stopped fetch's SERVER frame GONE, because the
// client abandoned the connection and the server observed it, and the control's
// server frame STILL PRESENT. The control is what makes the absence mean
// something: without it a dump missing both frames would read as success.
//
// A planted defect makes this fail: build the request with http.NewRequest
// instead of http.NewRequestWithContext, and the withdrawal reaches nothing — the
// connection stays up, the server stays in its handler, and
// parkedStoppedFragmentFetch is still on the stack.
//
// The same failure catches a design that watches for the withdrawal beside the
// fetch and returns early: the caller is released on time, so a timing assertion
// would pass, while the download runs on and the server never moves.
func TestStoppingAComposeResolutionEndsTheFetchOnTheWire(t *testing.T) {
	release := make(chan struct{})

	// Each resolution runs on its own goroutine and writes under the temp
	// workspace taken for it below, which the framework removes once this body
	// returns. The control one is still inside its fetch when the comparison
	// ends, which is exactly what the comparison asserts, so it resumes the
	// instant the servers are released — its server answers with an empty
	// fragment — and writes that fragment's slot into a directory the removal may
	// already be walking. The removal then fails on a non-empty directory and
	// reports as a failure of this test, having measured nothing this test is
	// about.
	//
	// So the body waits for both resolutions to return before it hands back. The
	// two defers below are registered in this order so that they run in the
	// opposite one — the release first, the wait second — and both run ahead of
	// every registered cleanup. Registering them before the goroutines start is
	// what keeps the pair on every exit path, a fatal assertion included.
	var inFlight sync.WaitGroup
	defer func() {
		settled := make(chan struct{})
		go func() { inFlight.Wait(); close(settled) }()
		select {
		case <-settled:
		case <-time.After(fetchReturnCeiling):
			t.Errorf("a resolution was still running after both servers were released, so " +
				"the workspace it writes into is about to be removed underneath it")
		}
	}()
	defer close(release)

	controlURL, controlEntered := parkingFragmentServer(t, parkedControlFragmentFetch, release)
	stoppedURL, stoppedEntered := parkingFragmentServer(t, parkedStoppedFragmentFetch, release)

	// Temp dirs are taken on the test's own goroutine, not inside the workers.
	controlRoot, stoppedRoot := t.TempDir(), t.TempDir()

	// The production shape: the adapter carries no client of its own and
	// transports the fetch over the client threaded through the spec.
	resolve := func(ctx context.Context, url, root string) error {
		_, err := NewAdapter().(*adapter).Resolve(ctx, sourceresolver.SourceSpec{
			Kind:          Kind,
			URL:           url,
			Version:       "1.0.0",
			WorkspaceRoot: root,
			CacheDir:      ".aggrega/cache",
			Client:        &http.Client{},
		})
		return err
	}

	inFlight.Add(2)

	controlDone := make(chan error, 1)
	go func() {
		defer inFlight.Done()
		controlDone <- resolve(context.Background(), controlURL, controlRoot)
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		defer inFlight.Done()
		stoppedDone <- resolve(stopCtx, stoppedURL, stoppedRoot)
	}()

	// Both fetches are now in flight, with both servers inside their handlers.
	// Until this point there is nothing to compare: a frame that has not been
	// entered is absent for a reason that has nothing to do with cancellation.
	<-controlEntered
	<-stoppedEntered

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case err := <-stoppedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped resolution returned %v; want an error wrapping context.Canceled — "+
				"the fetch ended for some reason other than the withdrawal", err)
		}
	case <-time.After(fetchReturnCeiling):
		t.Errorf("the stopped resolution never returned within the liveness ceiling: " +
			"the withdrawal did not reach the fetch")
	}

	// The server needs a moment to notice the closed connection; it is a separate
	// goroutine reacting to an event rather than a step in this one. The wait is
	// bounded by the same liveness ceiling and polls, so a fast machine does not
	// pay for it.
	waitForFrameToClear(t, "parkedStoppedFragmentFetch")

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the
	// stopped one is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlFragmentFetch") {
		t.Fatalf("the control's server frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control's fetch must still be in flight")
	}
	if strings.Contains(frames, "parkedStoppedFragmentFetch") {
		t.Errorf("the stopped fetch's SERVER frame is STILL on the stack while the control's is " +
			"there too: the caller was released but the download was never abandoned")
	}

	select {
	case err := <-controlDone:
		t.Fatalf("the control resolution returned early (%v); it was supposed to stay inside "+
			"its fetch for the whole comparison", err)
	default:
	}
}

// TestAStoppedComposeResolutionWritesNothingToTheCache is the other half of the
// contract: a resolution that ends part way must leave the cache exactly as it
// found it, rather than leaving a slot that a later run could mistake for a
// complete artifact.
//
// The ordering is what makes this hold, and it is the same in every adapter: the
// content digest is computed from the complete fetched bytes, and the slot path
// is derived from that digest. A fetch that never finishes never produces a
// digest, so it never reaches the slot computation — nothing is created to be
// half-written. This test pins that ordering rather than restating it.
//
// A planted defect makes this fail: create the slot directory before the fetch
// instead of after the digest.
func TestAStoppedComposeResolutionWritesNothingToTheCache(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	url, entered := parkingFragmentServer(t, parkedStoppedFragmentFetch, release)
	root := t.TempDir()

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewAdapter().(*adapter).Resolve(ctx, sourceresolver.SourceSpec{
			Kind:          Kind,
			URL:           url,
			Version:       "1.0.0",
			WorkspaceRoot: root,
			CacheDir:      ".aggrega/cache",
			Client:        &http.Client{},
		})
		done <- err
	}()

	// Withdrawn only once the fetch is genuinely in flight; a withdrawal before
	// the request went out would prove nothing about a fetch interrupted part way.
	<-entered
	stop()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the stopped resolution succeeded; a fetch that was abandoned cannot have " +
				"produced a fragment")
		}
	case <-time.After(fetchReturnCeiling):
		t.Fatal("the stopped resolution never returned within the liveness ceiling")
	}

	// The path comes from the package that owns the layout, so this asserts
	// against the real slot root rather than against a second spelling of it.
	artifacts := cacheslot.SourceArtifactsRoot(root, ".aggrega/cache")
	if _, err := os.Stat(artifacts); !os.IsNotExist(err) {
		t.Errorf("an interrupted fetch left something under %s (stat: %v); the cache was supposed "+
			"to be untouched", artifacts, err)
	}
}

// waitForFrameToClear polls the stack until the named frame is gone or the
// liveness ceiling expires. It does NOT assert: the comparison below it does
// that, on one dump, with the control alongside. This only removes the race
// between the client abandoning a connection and the server noticing, so a
// failure reported there is a real one rather than a dump taken too early.
func waitForFrameToClear(t *testing.T, frame string) {
	t.Helper()
	deadline := time.Now().Add(fetchReturnCeiling)
	for time.Now().Before(deadline) {
		if !strings.Contains(goroutineFrames(), frame) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
