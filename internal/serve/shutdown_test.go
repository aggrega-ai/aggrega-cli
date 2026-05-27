// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
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
	srv := &Server{
		workspaceRoot:    "/tmp/test-workspace",
		wsConfig:         workspaceconfig.Default(),
		lazyCatalog:      catalog.NewLazyLookup("/tmp/test-workspace", "", nil, nil),
		parseCache:       newCache(0, 0),
		diagnostics:      map[string][]Diagnostic{},
		in:               bufio.NewReader(stdinR),
		out:              stdoutBuf,
		stderr:           io.Discard,
		positionEncoding: yamlpos.PositionEncodingUTF16,
	}
	ch := make(chan error, 1)
	go func() {
		ch <- srv.dispatchLoop(shortDrain)
	}()
	return stdinWriter, stdoutBuf, ch
}

// TestShutdownNotificationCausesCleanExit verifies that an
// aggrega/shutdown notification reaches the daemon → drain timer
// arms in the reader goroutine → dispatch loop returns within the
// drain budget. No in-flight handler is exercised here; the test
// pins the basic clean-exit semantic.
func TestShutdownNotificationCausesCleanExit(t *testing.T) {
	stdin, _, done := startServerWithPipes(t)

	// Write the shutdown notification.
	if _, err := stdin.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}

	// Loop should exit within ~drainTimeout * a generous multiplier.
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("dispatchLoop exited with error: %v", err)
		}
	case <-time.After(shortDrain * 20):
		t.Fatal("dispatch loop did not exit within drain budget multiple")
	}
}

// TestShutdownRejectsSubsequentRequests verifies the request-path
// behavior during drain: after aggrega/shutdown arrives, a
// subsequent JSON-RPC request gets an errShuttingDown envelope.
func TestShutdownRejectsSubsequentRequests(t *testing.T) {
	// Compose stdin payload up front: shutdown notification, then a
	// validate request. Both bytes hit the read pipe before the
	// reader goroutine gets a chance to drain — the order is
	// deterministic via a single write because the pipe is unbuffered
	// and the reader consumes sequentially.
	payload := append([]byte{}, framedNotification("aggrega/shutdown", nil)...)
	payload = append(payload, framedRequest(42, "aggrega/validate", map[string]string{
		"file":    "/dev/null",
		"content": "",
	})...)

	stdinR, stdinW := io.Pipe()
	stdoutBuf := &bytes.Buffer{}
	srv := &Server{
		workspaceRoot:    "/tmp/test-workspace",
		wsConfig:         workspaceconfig.Default(),
		lazyCatalog:      catalog.NewLazyLookup("/tmp/test-workspace", "", nil, nil),
		parseCache:       newCache(0, 0),
		diagnostics:      map[string][]Diagnostic{},
		in:               bufio.NewReader(stdinR),
		out:              stdoutBuf,
		stderr:           io.Discard,
		positionEncoding: yamlpos.PositionEncodingUTF16,
	}
	done := make(chan error, 1)
	// Use a longer drain here so the dispatcher has time to process
	// BOTH messages before the timer fires. The test is asserting on
	// the response envelope, not on exit latency.
	go func() {
		done <- srv.dispatchLoop(500 * time.Millisecond)
	}()

	if _, err := stdinW.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	// Wait for the loop to exit on drain.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}

	// Inspect responses on stdout. The shutdown notification produces
	// no response; the validate request produces a 1005 envelope.
	r := bufio.NewReader(stdoutBuf)
	body, err := readMessage(r)
	if err != nil {
		t.Fatalf("read first response: %v (stdout: %s)", err, stdoutBuf.String())
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal first response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error envelope, got result: %s", resp.Result)
	}
	if resp.Error.Code != errShuttingDown {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, errShuttingDown)
	}
	// id round-trips as the request's id (42).
	var id int
	if err := json.Unmarshal(resp.ID, &id); err != nil {
		t.Fatalf("id parse: %v", err)
	}
	if id != 42 {
		t.Errorf("id: got %d, want 42", id)
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
	srv := &Server{
		workspaceRoot:    "/tmp/test-workspace",
		wsConfig:         workspaceconfig.Default(),
		lazyCatalog:      catalog.NewLazyLookup("/tmp/test-workspace", "", nil, nil),
		parseCache:       newCache(0, 0),
		diagnostics:      map[string][]Diagnostic{},
		in:               bufio.NewReader(stdinR),
		out:              stdoutBuf,
		stderr:           io.Discard,
		positionEncoding: yamlpos.PositionEncodingUTF16,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.dispatchLoop(500 * time.Millisecond)
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

// TestShutdownDrainBudgetEnforced verifies that the loop exits
// within the drain budget after shutdown, even when no further
// messages arrive on stdin. The drain timer is the load-bearing
// exit cause.
func TestShutdownDrainBudgetEnforced(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutBuf := &bytes.Buffer{}
	srv := &Server{
		workspaceRoot:    "/tmp/test-workspace",
		wsConfig:         workspaceconfig.Default(),
		lazyCatalog:      catalog.NewLazyLookup("/tmp/test-workspace", "", nil, nil),
		parseCache:       newCache(0, 0),
		diagnostics:      map[string][]Diagnostic{},
		in:               bufio.NewReader(stdinR),
		out:              stdoutBuf,
		stderr:           io.Discard,
		positionEncoding: yamlpos.PositionEncodingUTF16,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.dispatchLoop(shortDrain)
	}()

	if _, err := stdinW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}

	start := time.Now()
	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("dispatchLoop returned error: %v", err)
		}
		// Exit within a generous multiple of the drain budget — the
		// goroutine scheduler plus reader-pipe handshake adds tens
		// of milliseconds on slow CI.
		if elapsed > shortDrain*20 {
			t.Errorf("drain budget exceeded: exited after %v (budget %v)", elapsed, shortDrain)
		}
	case <-time.After(shortDrain * 50):
		t.Fatal("dispatch loop did not exit on drain budget")
	}
}

// TestShutdownDrainTimerIsArrivalBased pins the arrival-based timer
// semantic against an observation-based regression. The reader
// goroutine MUST create the drain timer at the moment it parses the
// shutdown notification, NOT when the main loop later observes it
// after the in-flight handler returns.
//
// Setup: handler sleeps 300ms (via testInDispatchHook); drain budget
// is 100ms. Wire sequence: validate request, then shutdown.
//
// Expected timing under ARRIVAL-based semantics:
//
//	t=0      reader parses validate, forwards to messages channel
//	t=0      main loop receives validate, calls handle() → sleeps 300ms
//	t=0+     reader parses shutdown, arms 100ms timer (timer fires t=100)
//	t=100    timer fires; main loop is still sleeping
//	t=300    handle() returns; main loop selects; drainCh is ready → exit
//	→ total elapsed ≈ 300ms
//
// Expected timing under OBSERVATION-based semantics (regression
// shape):
//
//	t=0      reader forwards validate
//	t=0      main loop sleeps 300ms in handle()
//	t=300    handle() returns; main loop receives shutdown; handler
//	         sets shuttingDown, dispatch loop then arms 100ms timer
//	t=400    timer fires; main loop exits
//	→ total elapsed ≈ 400ms
//
// Bound: exit must complete within 350ms. Arrival-based PASSES;
// observation-based FAILS by ≥50ms.
func TestShutdownDrainTimerIsArrivalBased(t *testing.T) {
	const (
		handlerDelay = 300 * time.Millisecond
		drainBudget  = 100 * time.Millisecond
		upperBound   = 350 * time.Millisecond
	)

	stdinR, stdinW := io.Pipe()
	stdoutBuf := &bytes.Buffer{}
	// One-shot delay: only the first handle() call sleeps. Without
	// the gate the hook would also delay the shutdown handler,
	// adding another handlerDelay and masking the arrival-vs-
	// observation timing distinction.
	hookFired := false
	srv := &Server{
		workspaceRoot:    "/tmp/test-workspace",
		wsConfig:         workspaceconfig.Default(),
		lazyCatalog:      catalog.NewLazyLookup("/tmp/test-workspace", "", nil, nil),
		parseCache:       newCache(0, 0),
		diagnostics:      map[string][]Diagnostic{},
		in:               bufio.NewReader(stdinR),
		out:              stdoutBuf,
		stderr:           io.Discard,
		positionEncoding: yamlpos.PositionEncodingUTF16,
		testInDispatchHook: func() {
			if hookFired {
				return
			}
			hookFired = true
			time.Sleep(handlerDelay)
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- srv.dispatchLoop(drainBudget)
	}()

	payload := append([]byte{}, framedRequest(1, "aggrega/validate", map[string]string{
		"file":    "/dev/null",
		"content": "",
	})...)
	payload = append(payload, framedNotification("aggrega/shutdown", nil)...)

	start := time.Now()
	if _, err := stdinW.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("dispatchLoop returned error: %v", err)
		}
		if elapsed > upperBound {
			t.Errorf("exit elapsed %v exceeds %v — drain timer appears observation-based "+
				"(expected arrival-based, where elapsed ≈ %v not %v)",
				elapsed, upperBound, handlerDelay, handlerDelay+drainBudget)
		}
		t.Logf("arrival-based exit confirmed: elapsed %v (bound %v)", elapsed, upperBound)
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}
}

// TestShutdownSecondNotificationIsIdempotent covers an edge case:
// receiving aggrega/shutdown twice during drain is a no-op. The
// dispatchShutdown handler sets s.shuttingDown again (already true)
// and returns nil; the reader's cap-1 install channel discards the
// duplicate timer install.
func TestShutdownSecondNotificationIsIdempotent(t *testing.T) {
	payload := append([]byte{}, framedNotification("aggrega/shutdown", nil)...)
	payload = append(payload, framedNotification("aggrega/shutdown", nil)...)

	stdinR, stdinW := io.Pipe()
	stdoutBuf := &bytes.Buffer{}
	srv := &Server{
		workspaceRoot:    "/tmp/test-workspace",
		wsConfig:         workspaceconfig.Default(),
		lazyCatalog:      catalog.NewLazyLookup("/tmp/test-workspace", "", nil, nil),
		parseCache:       newCache(0, 0),
		diagnostics:      map[string][]Diagnostic{},
		in:               bufio.NewReader(stdinR),
		out:              stdoutBuf,
		stderr:           io.Discard,
		positionEncoding: yamlpos.PositionEncodingUTF16,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.dispatchLoop(shortDrain)
	}()

	if _, err := stdinW.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("dispatchLoop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}
	if stdoutBuf.Len() != 0 {
		t.Errorf("expected no responses for shutdown notifications, got: %s", stdoutBuf.String())
	}
}
