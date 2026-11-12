// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// shortDrain is the drain timeout integration-style shutdown tests
// pass into the dispatch loop. Short enough that a "did the drain
// timer fire?" check exercises in subsecond wall time.
const shortDrain = 50 * time.Millisecond

// framedNotification builds a Content-Length-framed JSON-RPC
// notification (no id field, no response expected) for the supplied
// method + params.
func framedNotification(method string, params any) []byte {
	paramBytes, _ := json.Marshal(params)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  json.RawMessage(paramBytes),
	})
	return append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))), body...)
}

// startServerWithPipes spawns a Server and runs its dispatch loop in
// a goroutine. Returns the writable end of stdin, the readable end
// of stdout, and a channel that signals when the dispatch loop
// returns (carrying its terminal error, if any).
func startServerWithPipes(t *testing.T) (stdinW io.WriteCloser, stdoutR io.Reader, done <-chan error) {
	t.Helper()
	stdinR, stdinWriter := io.Pipe()
	stdoutBuf := &bytes.Buffer{}
	srv := newTestServer(t, t.TempDir(), stdinR, stdoutBuf)
	ch := make(chan error, 1)
	go func() {
		ch <- srv.dispatchLoop(context.Background(), shortDrain)
	}()
	return stdinWriter, stdoutBuf, ch
}

// TestShutdownNotificationCausesCleanExit verifies that a single
// aggrega/shutdown notification, with no in-flight work, drains the
// (empty) worker pool and returns cleanly within a generous multiple
// of the drain budget. The first shutdown enters DRAINING; with no
// accepted work to finish, the background drain completes at once and
// the loop exits.
func TestShutdownNotificationCausesCleanExit(t *testing.T) {
	stdin, _, done := startServerWithPipes(t)

	// Write the shutdown notification.
	if _, err := stdin.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}

	// Loop should exit promptly once the empty pool drains.
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("dispatchLoop exited with error: %v", err)
		}
	case <-time.After(shortDrain * 20):
		t.Fatal("dispatch loop did not exit after the empty pool drained")
	}
}

// TestShutdownFirstSignalDrainsInFlight is the drain-side proof of the
// drain-then-abort ruling: the FIRST aggrega/shutdown does NOT cancel an
// in-flight handler — it lets it run to completion. The handler races
// its own per-request ctx.Done against a fixed timer; under drain
// semantics the timer wins (the handler completed), and the loop exits
// only after that completion.
func TestShutdownFirstSignalDrainsInFlight(t *testing.T) {
	t.Parallel()
	const handlerRun = 150 * time.Millisecond

	var completed, cancelled int32
	entered := make(chan struct{})

	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv := newPoolTestServer(t, stdinR, stdout, 1, nil)
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		if len(id) == 0 {
			return // the shutdown notification carries no id
		}
		close(entered)
		select {
		case <-ctx.Done():
			// The first shutdown must NOT reach here.
			atomic.AddInt32(&cancelled, 1)
		case <-time.After(handlerRun):
			atomic.AddInt32(&completed, 1)
		}
	}

	done := make(chan error, 1)
	go func() { done <- srv.dispatchLoop(context.Background(), shortDrain) }()

	if _, err := stdinW.Write(framedRequestIn(t, srv, 1, "aggrega/releaseCandidates", map[string]any{})); err != nil {
		t.Fatalf("write request: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never entered in-flight")
	}

	// First shutdown: DRAIN, not abort. serverCtx must stay live so the
	// in-flight handler runs to completion.
	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("dispatchLoop error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop did not exit after the in-flight handler drained")
	}

	if got := atomic.LoadInt32(&cancelled); got != 0 {
		t.Errorf("in-flight handler was cancelled on the FIRST shutdown (%d); drain must let it finish", got)
	}
	if got := atomic.LoadInt32(&completed); got != 1 {
		t.Errorf("in-flight handler did not run to completion under drain; completed=%d", got)
	}
}

// TestShutdownSecondNotificationAborts is the abort-side proof: the
// FIRST aggrega/shutdown drains (an in-flight handler blocked on its
// per-request ctx.Done is NOT cancelled and the loop does not exit),
// while the SECOND aggrega/shutdown ABORTS — it cancels the in-flight
// handler and the loop exits within the drain budget.
func TestShutdownSecondNotificationAborts(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	released := make(chan struct{})
	var cancelled int32

	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv := newPoolTestServer(t, stdinR, stdout, 1, nil)
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		if len(id) == 0 {
			return
		}
		close(entered)
		select {
		case <-ctx.Done():
			atomic.StoreInt32(&cancelled, 1)
		case <-released:
		}
	}

	done := make(chan error, 1)
	go func() { done <- srv.dispatchLoop(context.Background(), shortDrain) }()

	if _, err := stdinW.Write(framedRequestIn(t, srv, 1, "aggrega/releaseCandidates", map[string]any{})); err != nil {
		t.Fatalf("write request: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(released)
		t.Fatal("handler never entered in-flight")
	}

	// First shutdown drains: the loop must stay alive and the handler
	// must stay uncancelled while it is in-flight.
	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		close(released)
		t.Fatalf("write first shutdown: %v", err)
	}
	select {
	case err := <-done:
		close(released)
		t.Fatalf("loop exited on the FIRST shutdown while a handler was in-flight (err=%v); the first signal must drain", err)
	case <-time.After(100 * time.Millisecond):
	}
	if atomic.LoadInt32(&cancelled) != 0 {
		close(released)
		t.Fatal("in-flight handler cancelled on the FIRST shutdown; the first signal must drain, not abort")
	}

	// Second shutdown aborts: it cancels the in-flight handler, which is what
	// lets the loop exit at all.
	//
	// Nothing here measures how LONG the exit took, and that is deliberate. The
	// two designs are separated structurally instead: the handler below can be
	// released only by its per-request ctx being cancelled, so a loop that exits
	// proves the abort reached the handler, and `cancelled` below proves which
	// branch released it. An abort that failed to cancel would leave the handler
	// parked and the loop unable to exit, which the liveness ceiling reports as
	// a hang rather than as a slow machine. A wall-clock bound on the exit added
	// nothing to that and reported contention from unrelated packages as a
	// shutdown regression.
	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		close(released)
		t.Fatalf("write second shutdown: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("dispatchLoop error: %v", err)
		}
	case <-time.After(2 * time.Second):
		close(released)
		t.Fatal("dispatch loop did not exit after the SECOND shutdown (abort)")
	}
	if atomic.LoadInt32(&cancelled) != 1 {
		t.Error("in-flight handler was not cancelled by the SECOND shutdown (abort)")
	}
	close(released)
}

// TestShutdownAbortIsPromptOnSecondSignal pins that the SECOND
// shutdown's abort reaches the in-flight handler rather than the handler
// merely completing on its own. The handler races ctx.Done against a long
// (2s) self-completion fallback, and it RECORDS WHICH OF THE TWO released
// it. Under a working abort the second signal's cancellation fires the
// ctx.Done branch; a regression that failed to abort would leave the
// cancellation unfired and the handler would be released by the fallback
// instead.
//
// Which branch fired is the whole assertion, and it is deliberately not a
// duration. The two designs differ in WHAT releases the handler, not in how
// fast it happens, so recording the branch discriminates them at any machine
// speed — where the previous wall-clock bound (500ms against the 2s fallback,
// a 4x margin) reported a contended runner as a shutdown regression. The only
// time bound left is the liveness ceiling on the loop's exit below, which
// separates "exits" from "hangs forever" and no contention can blur.
func TestShutdownAbortIsPromptOnSecondSignal(t *testing.T) {
	t.Parallel()
	const fallback = 2 * time.Second

	entered := make(chan struct{})
	// 1 once the per-request ctx cancellation released the handler, which only
	// the abort can cause; it stays 0 if the fallback released it instead.
	var releasedByCancel int32

	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv := newPoolTestServer(t, stdinR, stdout, 1, nil)
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		if len(id) == 0 {
			return
		}
		close(entered)
		select {
		case <-ctx.Done():
			atomic.StoreInt32(&releasedByCancel, 1)
		case <-time.After(fallback):
		}
	}

	done := make(chan error, 1)
	go func() { done <- srv.dispatchLoop(context.Background(), shortDrain) }()

	if _, err := stdinW.Write(framedRequestIn(t, srv, 1, "aggrega/releaseCandidates", map[string]any{})); err != nil {
		t.Fatalf("write request: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never entered in-flight")
	}

	// Drain, then abort. The pipe stays open (no EOF): the abort's cancel
	// — armed by the reader at the second signal's arrival — is what
	// releases the handler and lets the loop exit.
	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write first shutdown: %v", err)
	}
	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write second shutdown: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("dispatchLoop error: %v", err)
		}
	case <-time.After(fallback + time.Second):
		t.Fatal("dispatch loop did not exit after the second shutdown")
	}

	if atomic.LoadInt32(&releasedByCancel) != 1 {
		t.Error("the in-flight handler was released by its own self-completion fallback, not by a cancellation; " +
			"the second shutdown's abort did not reach it")
	}
}

// TestShutdownRejectsSubsequentRequests verifies the request-path
// behavior during drain: after aggrega/shutdown arrives, a subsequent
// JSON-RPC request gets an errShuttingDown envelope. A prior request is
// held in-flight so the drain does not complete before the follow-on
// request is accepted and rejected, making the 1005 deterministic.
func TestShutdownRejectsSubsequentRequests(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})

	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv := newPoolTestServer(t, stdinR, stdout, 4, nil)
	srv.testCtxHook = func(_ context.Context, id json.RawMessage) {
		var n int
		if json.Unmarshal(id, &n) != nil || n != 1 {
			return
		}
		close(entered)
		<-release // hold the worker so the drain stays in progress
	}

	done := make(chan error, 1)
	go func() { done <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

	// Hold request 1 in-flight.
	if _, err := stdinW.Write(framedRequestIn(t, srv, 1, "aggrega/releaseCandidates", map[string]any{})); err != nil {
		t.Fatalf("write held request: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("held request never entered in-flight")
	}

	// Shutdown (drain begins), then a follow-on request that must be
	// rejected with 1005 while the drain is still in progress.
	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		close(release)
		t.Fatalf("write shutdown: %v", err)
	}
	if _, err := stdinW.Write(framedRequestIn(t, srv, 42, "aggrega/validate", map[string]string{
		"file":    "/dev/null",
		"content": "",
	})); err != nil {
		close(release)
		t.Fatalf("write follow-on request: %v", err)
	}

	// Release the held request; the drain completes and the loop exits.
	// The serializer emits in arrival order: request 1's response, then
	// the shutdown's body-less slot, then request 42's 1005.
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}

	r := bufio.NewReader(bytes.NewReader(stdout.bytes()))
	resps := readResponses(t, r, 2)

	// First response: the held request 1, no error.
	if resps[0].Error != nil {
		t.Errorf("held request response carried error: %+v", resps[0].Error)
	}
	var id0 int
	if err := json.Unmarshal(resps[0].ID, &id0); err != nil {
		t.Fatalf("held response id parse: %v", err)
	}
	if id0 != 1 {
		t.Errorf("first response id = %d; want 1", id0)
	}

	// Second response: the follow-on request 42, rejected with 1005.
	if resps[1].Error == nil {
		t.Fatalf("expected error envelope for follow-on request, got result: %s", resps[1].Result)
	}
	if resps[1].Error.Code != errShuttingDown {
		t.Errorf("error code: got %d, want %d", resps[1].Error.Code, errShuttingDown)
	}
	var id1 int
	if err := json.Unmarshal(resps[1].ID, &id1); err != nil {
		t.Fatalf("follow-on id parse: %v", err)
	}
	if id1 != 42 {
		t.Errorf("follow-on id: got %d, want 42", id1)
	}
}

// TestShutdownDropsSubsequentNotifications verifies the
// notification-path behavior during drain: a notification arriving
// during drain is silently dropped (no response, no error). The
// test verifies stdout is empty after sending shutdown then a
// follow-on notification.
func TestShutdownDropsSubsequentNotifications(t *testing.T) {
	payload := append([]byte{}, framedNotification("aggrega/shutdown", nil)...)
	payload = append(payload, framedNotification("aggrega/invalidate", map[string]string{
		"file": "/dev/null",
	})...)

	stdinR, stdinW := io.Pipe()
	stdoutBuf := &bytes.Buffer{}
	// Pin pool=4 explicitly. The acceptance-boundary drop this test
	// guards is a no-op at pool=1 (serial dispatch already orders the
	// shutdown before the follow-on notification); the logical race it
	// regresses against only surfaces under a multi-worker pool. The
	// default is 4 today, but pinning here keeps the guard sharp if the
	// default ever changes.
	srv := newPoolTestServer(t, stdinR, stdoutBuf, 4, nil)
	done := make(chan error, 1)
	go func() {
		done <- srv.dispatchLoop(context.Background(), 500*time.Millisecond)
	}()

	if _, err := stdinW.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}

	if stdoutBuf.Len() != 0 {
		t.Errorf("expected empty stdout (no responses for shutdown+notification), got: %s",
			stdoutBuf.String())
	}
}

// TestAStopRequestToTheProcessDrainsTheDispatchLoop is the proof that asking
// the PROCESS to stop reaches the daemon's work, rather than merely reaching a
// signature that holds a context.
//
// The run context the process was launched with is withdrawn while a handler is
// in flight. The loop must treat that exactly as it treats the FIRST
// aggrega/shutdown: enter draining, let the accepted handler run to completion
// uncancelled, and exit cleanly. Three observations separate that design from
// every neighbouring one, and none of them is a duration:
//
//   - The loop exits at all. Stdin is an open pipe that never reaches EOF and
//     no shutdown notification is ever written, so the withdrawal is the only
//     thing that can end the loop. A loop that does not observe it hangs, which
//     the liveness ceiling below reports as a hang rather than as a slow
//     machine.
//   - The in-flight handler is released by its own completion and not by its
//     per-request context, which is what distinguishes a drain from an abort. A
//     withdrawal wired as an abort would cancel it.
//   - The draining state was entered. An exit reached any other way — EOF, a
//     write failure — leaves the flag unset, so reading it after the loop has
//     returned attributes the exit.
func TestAStopRequestToTheProcessDrainsTheDispatchLoop(t *testing.T) {
	t.Parallel()
	const handlerRun = 150 * time.Millisecond

	var completed, cancelled int32
	entered := make(chan struct{})

	stdinR, stdinW := io.Pipe()
	stdout := &syncBuffer{}
	srv := newPoolTestServer(t, stdinR, stdout, 1, nil)
	srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
		if len(id) == 0 {
			return // a notification carries no id
		}
		close(entered)
		select {
		case <-ctx.Done():
			// A stop request drains, so it must NOT reach here.
			atomic.AddInt32(&cancelled, 1)
		case <-time.After(handlerRun):
			atomic.AddInt32(&completed, 1)
		}
	}

	runCtx, stop := context.WithCancel(context.Background())
	defer stop()

	done := make(chan error, 1)
	go func() { done <- srv.dispatchLoop(runCtx, shortDrain) }()

	if _, err := stdinW.Write(framedRequestIn(t, srv, 1, "aggrega/releaseCandidates", map[string]any{})); err != nil {
		t.Fatalf("write request: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never entered in-flight")
	}

	// Nothing has asked the loop to stop yet, so an exit here would mean the
	// exit had a cause this test is not measuring. The window cannot flake the
	// other way: a loaded machine makes the loop slower to exit, never quicker
	// to exit when nothing asked it to.
	select {
	case err := <-done:
		t.Fatalf("the dispatch loop exited before any stop request arrived (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}

	stop()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("dispatchLoop exited with an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the dispatch loop did not exit after the run context was withdrawn; " +
			"a stop request to the process must reach the loop")
	}

	if got := atomic.LoadInt32(&cancelled); got != 0 {
		t.Errorf("the in-flight handler was cancelled by the stop request (%d); the first request drains, it does not abort", got)
	}
	if got := atomic.LoadInt32(&completed); got != 1 {
		t.Errorf("the in-flight handler did not run to completion under drain; completed=%d", got)
	}
	if !srv.shuttingDown.Load() {
		t.Error("the loop exited without entering the draining state; a stop request must be accepted the way a shutdown notification is")
	}
}
