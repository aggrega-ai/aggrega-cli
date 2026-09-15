// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The registry's own unit tests — the track / untrack / cancel lifecycle and the
// concurrent fan-out — live with the registry in internal/requestcancel. What
// stays here drives the daemon's wiring: the $/cancelRequest notification, the
// per-request context chain, and the balance of the registry under the pool.
// cancellation.go names which test covers which call site the moved ones left.

// TestDeadlineComposesOverPerRequestContext proves source (d): the
// per-request context shape is a plain cancellable chain, so a deadline
// layers onto it by construction. A WithTimeout child cancels on timeout,
// and a parent cancel still propagates to the deadline child. No deadline
// is set in production; this asserts the chain ADMITS one.
func TestDeadlineComposesOverPerRequestContext(t *testing.T) {
	t.Parallel()

	// Shape mirrors handle's chain: a per-request child of a session
	// parent, then a deadline layered on top.
	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	perRequest, perRequestCancel := context.WithCancel(parent)
	defer perRequestCancel()

	withDeadline, cancelDeadline := context.WithTimeout(perRequest, 20*time.Millisecond)
	defer cancelDeadline()
	select {
	case <-withDeadline.Done():
		if withDeadline.Err() != context.DeadlineExceeded {
			t.Errorf("deadline child err = %v; want DeadlineExceeded", withDeadline.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("deadline child did not fire on timeout")
	}

	// A parent cancel still propagates through a deadline child.
	perRequest2, perRequest2Cancel := context.WithCancel(parent)
	defer perRequest2Cancel()
	withDeadline2, cancelDeadline2 := context.WithTimeout(perRequest2, time.Hour)
	defer cancelDeadline2()
	parentCancel()
	select {
	case <-withDeadline2.Done():
		if withDeadline2.Err() != context.Canceled {
			t.Errorf("deadline child err after parent cancel = %v; want Canceled", withDeadline2.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancel did not propagate through the deadline child")
	}
}

// TestShutdownCancelsConcurrentInFlightHandlers proves source (a) under
// the pool: with N handlers held in-flight, the SECOND aggrega/shutdown
// (the abort) cancels every one of their per-request contexts within the
// drain budget, and the dispatch loop exits. The first shutdown drains
// (it does not cancel); the second aborts. This extends the
// single-in-flight drain-then-abort tests into the multiple-concurrent
// case the pool enables.
func TestShutdownCancelsConcurrentInFlightHandlers(t *testing.T) {
	t.Parallel()
	for _, pool := range []int{4, 16} {
		pool := pool
		t.Run(fmt.Sprintf("pool=%d", pool), func(t *testing.T) {
			t.Parallel()
			const inFlight = 4

			var entered sync.WaitGroup
			entered.Add(inFlight)
			var enteredOnce int32
			var cancelledCount int32
			held := make(chan struct{})

			stdinR, stdinW := io.Pipe()
			// A mutex-guarded buffer keeps the serializer's concurrent
			// writes race-free; the test reads it only after the loop
			// returns. The in-flight count (4) stays under the queue
			// capacity at both pool sizes, so submission never blocks on
			// a drain and no concurrent reader is needed.
			stdout := &syncBuffer{}
			srv := newPoolTestServer(t, stdinR, stdout, pool, nil)
			srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
				// Only the first inFlight id-bearing handlers block; the
				// shutdown notification (no id) and any later request fall
				// through so the loop can progress.
				if len(id) == 0 {
					return
				}
				if atomic.AddInt32(&enteredOnce, 1) > inFlight {
					return
				}
				entered.Done()
				// Block until the request context is cancelled (by
				// shutdown) or the test releases.
				select {
				case <-ctx.Done():
					atomic.AddInt32(&cancelledCount, 1)
				case <-held:
				}
			}

			loopDone := make(chan error, 1)
			go func() { loopDone <- srv.dispatchLoop(context.Background(), 200*time.Millisecond) }()

			for i := 0; i < inFlight; i++ {
				if _, err := stdinW.Write(framedRequestIn(t, srv, i+1, "aggrega/releaseCandidates", map[string]any{})); err != nil {
					t.Fatalf("write request: %v", err)
				}
			}

			// Wait until all inFlight handlers are parked on ctx.Done().
			waitDone := make(chan struct{})
			go func() { entered.Wait(); close(waitDone) }()
			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
				close(held)
				t.Fatal("handlers never all entered in-flight")
			}

			// First shutdown drains (no cancel); the second aborts —
			// serverCancel then cancels every per-request ctx.
			if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
				t.Fatalf("write first shutdown: %v", err)
			}
			if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
				t.Fatalf("write second shutdown: %v", err)
			}
			stdinW.Close()

			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(5 * time.Second):
				close(held)
				t.Fatal("dispatch loop did not exit on shutdown")
			}

			if got := atomic.LoadInt32(&cancelledCount); got != inFlight {
				t.Errorf("cancelled handlers = %d; want %d (every in-flight ctx cancelled on shutdown)", got, inFlight)
			}
		})
	}
}

// TestPerRequestCancelFiresOnCompletion proves source (b): each
// in-flight request's per-request context is Done() once its handler
// returns — the defer cancel() released the child. Asserted under the
// pool, capturing the live ctx via testCtxHook.
func TestPerRequestCancelFiresOnCompletion(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	captured := map[string]context.Context{}

	root := t.TempDir()
	var stdin, stdout bytes.Buffer
	ids := []int{1, 2, 3}
	for _, id := range ids {
		stdin.Write(framedRequestAt(t, root, id, "aggrega/releaseCandidates", map[string]any{}))
	}
	stdin.Write(framedNotification("aggrega/shutdown", nil))

	srv := newPoolTestServerAt(t, root, &stdin, &stdout, 4, nil)
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		if len(id) == 0 {
			return
		}
		mu.Lock()
		captured[string(id)] = ctx
		mu.Unlock()
	}

	if err := srv.dispatchLoop(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != len(ids) {
		t.Fatalf("captured %d contexts; want %d", len(captured), len(ids))
	}
	for id, ctx := range captured {
		select {
		case <-ctx.Done():
		default:
			t.Errorf("per-request ctx for id %s not Done() after handler returned", id)
		}
	}
}

// TestCancelTrackerEmptyAfterBurst proves no registry / context leak
// under the pool: after a burst of concurrent id-bearing requests
// completes (no shutdown mid-burst), the cancel registry is empty —
// every tracked id was untracked, so track / untrack stay balanced under
// concurrency.
func TestCancelTrackerEmptyAfterBurst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var stdin, stdout bytes.Buffer
	const n = 50
	for i := 0; i < n; i++ {
		stdin.Write(framedRequestAt(t, root, i+1, "aggrega/releaseCandidates", map[string]any{}))
	}
	stdin.Write(framedNotification("aggrega/shutdown", nil))

	srv := newPoolTestServerAt(t, root, &stdin, &stdout, 16, nil)
	if err := srv.dispatchLoop(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}

	if got := srv.cancelTracker.Len(); got != 0 {
		t.Errorf("cancel registry not empty after burst drained; len = %d; want 0", got)
	}
}

// TestCancelRequestByIdInterruptsHandler exercises the registry pickup
// directly against a live in-flight handler: a handler is held on its
// per-request ctx.Done(); the test reaches the registry by id and calls
// the cancel pickup directly; the handler observes cancellation and
// releases. This is the unit-level proof of the pickup in isolation; the
// end-to-end notification route is covered by
// TestCancelRequestNotificationInterruptsHandler.
func TestCancelRequestByIdInterruptsHandler(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	var observed int32
	const targetID = 7

	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv := newPoolTestServer(t, stdinR, stdout, 4, nil)
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		var n int
		if json.Unmarshal(id, &n) != nil || n != targetID {
			return
		}
		close(entered)
		select {
		case <-ctx.Done():
			atomic.StoreInt32(&observed, 1)
		case <-time.After(2 * time.Second):
		}
	}

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

	if _, err := stdinW.Write(framedRequestIn(t, srv, targetID, "aggrega/releaseCandidates", map[string]any{})); err != nil {
		t.Fatalf("write request: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("target handler never entered in-flight")
	}

	// Reach the registry by id and fire the pickup directly — the
	// unit-level seam check, independent of notification routing.
	srv.cancelTracker.Cancel(json.RawMessage(fmt.Sprintf("%d", targetID)))

	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	stdinW.Close()

	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("dispatchLoop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}

	if atomic.LoadInt32(&observed) != 1 {
		t.Errorf("handler did not observe per-id cancellation from the registry pickup")
	}
}

// TestCancelRequestNotificationInterruptsHandler drives a REAL
// $/cancelRequest notification through the dispatch loop: an id-bearing
// handler is held in-flight on its per-request ctx; the $/cancelRequest
// notification carrying that id routes through the acceptance-boundary
// case to the registry pickup; the handler observes cancellation and
// releases. This is the end-to-end proof that the notification is wired,
// run at pool 4 and 16 to confirm prompt cancellation under the pool and
// -race cleanliness.
func TestCancelRequestNotificationInterruptsHandler(t *testing.T) {
	t.Parallel()
	for _, pool := range []int{4, 16} {
		pool := pool
		t.Run(fmt.Sprintf("pool=%d", pool), func(t *testing.T) {
			t.Parallel()

			entered := make(chan struct{})
			var observed int32
			const targetID = 7

			stdinR, stdinW := io.Pipe()
			stdout := &syncBuffer{}
			srv := newPoolTestServer(t, stdinR, stdout, pool, nil)
			srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
				var n int
				if json.Unmarshal(id, &n) != nil || n != targetID {
					return
				}
				close(entered)
				select {
				case <-ctx.Done():
					atomic.StoreInt32(&observed, 1)
				case <-time.After(2 * time.Second):
				}
			}

			loopDone := make(chan error, 1)
			go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

			if _, err := stdinW.Write(framedRequestIn(t, srv, targetID, "aggrega/releaseCandidates", map[string]any{})); err != nil {
				t.Fatalf("write request: %v", err)
			}

			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("target handler never entered in-flight")
			}

			// The real wire path: a $/cancelRequest notification carrying
			// the in-flight id, routed by the dispatch loop to the pickup.
			if _, err := stdinW.Write(framedNotification("$/cancelRequest", map[string]any{"id": targetID})); err != nil {
				t.Fatalf("write cancel: %v", err)
			}

			if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
				t.Fatalf("write shutdown: %v", err)
			}
			stdinW.Close()

			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch loop did not exit")
			}

			if atomic.LoadInt32(&observed) != 1 {
				t.Errorf("handler did not observe cancellation from the $/cancelRequest notification")
			}
		})
	}
}

// TestCancelRequestUnknownIdIsNoOp proves a $/cancelRequest for an id
// with no in-flight handler is a safe no-op: it produces no response
// frame (it is a notification), does not disturb the loop, and a normal
// request submitted afterward is still served. Mirrors the
// notification-produces-no-stdout-frame assertion shape from the
// shutdown-drop tests.
func TestCancelRequestUnknownIdIsNoOp(t *testing.T) {
	t.Parallel()

	var stdin, stdout bytes.Buffer
	// A cancel for an id that is not (and never was) in flight, then a
	// real request whose response must still appear, then shutdown.
	root := t.TempDir()
	stdin.Write(framedNotification("$/cancelRequest", map[string]any{"id": 999}))
	stdin.Write(framedRequestAt(t, root, 1, "aggrega/releaseCandidates", map[string]any{}))
	stdin.Write(framedNotification("aggrega/shutdown", nil))

	srv := newPoolTestServerAt(t, root, &stdin, &stdout, 4, nil)
	if err := srv.dispatchLoop(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}

	// Exactly one response frame: the releaseCandidates request. The
	// $/cancelRequest and shutdown notifications produce none.
	r := bufio.NewReader(&stdout)
	resp := readResponses(t, r, 1)
	var id int
	if err := json.Unmarshal(resp[0].ID, &id); err != nil {
		t.Fatalf("response id parse: %v", err)
	}
	if id != 1 {
		t.Errorf("served response id = %d; want 1", id)
	}
	// No further frames: the next read must hit EOF.
	if _, err := readMessage(r); err == nil {
		t.Errorf("unexpected extra response frame after the single served request")
	}
}

// TestCancelRequestNormalizesIdWhitespace is the id-normalization
// regression: the in-flight request's id token and the $/cancelRequest
// params.id are JSON-encoded with DIFFERENT insignificant whitespace for
// the same logical id. The cancel must still interrupt the handler,
// proving the registry's key canonicalization rather than a verbatim-byte
// match. framedRequest emits a tight `"id":7`; the cancel params here are
// hand-framed as `{"id": 7}` with a space before the value.
//
// This is the daemon's end-to-end cover for that canonicalization, which the
// registry keeps unexported: no caller here can build a key, so the only way to
// assert the rule from this package is to drive a differing spelling over the
// wire and watch the handler stop.
func TestCancelRequestNormalizesIdWhitespace(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	var observed int32
	const targetID = 7

	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv := newPoolTestServer(t, stdinR, stdout, 4, nil)
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		var n int
		if json.Unmarshal(id, &n) != nil || n != targetID {
			return
		}
		close(entered)
		select {
		case <-ctx.Done():
			atomic.StoreInt32(&observed, 1)
		case <-time.After(2 * time.Second):
		}
	}

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

	if _, err := stdinW.Write(framedRequestIn(t, srv, targetID, "aggrega/releaseCandidates", map[string]any{})); err != nil {
		t.Fatalf("write request: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("target handler never entered in-flight")
	}

	// Hand-frame a $/cancelRequest whose params.id carries whitespace the
	// request's id token did not (`"id": 7` vs the request's `"id":7`).
	// A verbatim-byte match would miss; the registry's json.Compact makes
	// both resolve to the same key.
	cancelBody := `{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id": 7}}`
	if _, err := stdinW.Write(rawFramed(cancelBody)); err != nil {
		t.Fatalf("write cancel: %v", err)
	}

	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	stdinW.Close()

	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("dispatchLoop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}

	if atomic.LoadInt32(&observed) != 1 {
		t.Errorf("whitespace-differing $/cancelRequest did not cancel the handler; the registry's key normalization failed")
	}
}
