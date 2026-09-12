// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The tool protocol's entry point took no context at all. Every tool that
// publishes, pulls, builds, lints or searches declares one and threads it into
// the work it drives, and the per-invocation dispatch handed each of them a fresh
// background context — so the whole surface's cancellation plumbing terminated
// one line above the tools that needed it. The resource reads were the same: the
// CLI-help read starts a subprocess and the handshake pulls the documentation
// bundle from a registry, both on a context nobody could end.
//
// These proofs ask whether ending the run ends what a call is doing. The
// discriminator is the FRAME of the goroutine inside the tool, compared against a
// control driven to the same point and left alone — a call that returns when its
// run is withdrawn proves only that the caller stopped waiting.
//
// parkedControlCommand and parkedStoppedCommand are the same function written
// twice, deliberately: ONE stack dump distinguishes two goroutines only if the
// two frames carry two names. Unifying them would erase the distinction the proof
// rests on.

// runReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped call stops at all. A machine slow enough to exceed this is a machine on
// which the whole suite has already failed.
const runReturnCeiling = 30 * time.Second

func parkedControlCommand(ctx context.Context, _ []string, _, _ io.Writer) error {
	<-ctx.Done()
	return ctx.Err()
}

func parkedStoppedCommand(ctx context.Context, _ []string, _, _ io.Writer) error {
	<-ctx.Done()
	return ctx.Err()
}

// mcpGoroutineFrames renders every goroutine's stack, growing the buffer until
// the dump fits. A truncated dump would make an absent frame unreadable from a
// frame that simply did not fit, which is the one way this proof could quietly
// stop proving anything.
func mcpGoroutineFrames() string {
	for size := 1 << 20; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			return string(buf[:n])
		}
	}
}

// waitForMCPFrameToClear polls the stack until the named frame is gone or the
// liveness ceiling expires. It does NOT assert: the comparison below it does
// that, on one dump, with the control alongside. This only removes the race
// between a call returning and this goroutine looking.
func waitForMCPFrameToClear(t *testing.T, frame string) {
	t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		if !strings.Contains(mcpGoroutineFrames(), frame) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// blockingReader hands the message loop one message and then never reaches EOF,
// so the loop stays alive exactly as it does against a connected client. A reader
// that returned EOF would end the loop for a reason that has nothing to do with
// cancellation, and the comparison would pass for the wrong reason.
type blockingReader struct {
	remaining string
	hold      chan struct{}
}

func (r *blockingReader) Read(p []byte) (int, error) {
	if r.remaining != "" {
		n := copy(p, r.remaining)
		r.remaining = r.remaining[n:]
		return n, nil
	}
	<-r.hold
	return 0, io.EOF
}

// The tool name is derived rather than spelled, so a change to the naming rule
// moves this fixture with it instead of leaving a request no tool answers.
var callFixtureCommand = `{"jsonrpc":"2.0","id":1,"method":"tools/call",` +
	`"params":{"name":"` + commandToolName("aggrega work") + `","arguments":{}}}` + "\n"

const readCliHelp = `{"jsonrpc":"2.0","id":1,"method":"resources/read",` +
	`"params":{"uri":"cli://validate"}}` + "\n"

// driveWithContext runs the real entry point under a caller's context and
// collects whatever it answered. It is the harness the package already uses,
// with the context supplied rather than fixed, which is the whole point here.
func driveWithContext(t *testing.T, ctx context.Context, requests ...string) (
	responses []rpcResponse, stdout, stderr string, runErr error) {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out, errBuf bytes.Buffer
	runErr = Run(ctx, Config{Stdin: in, Stdout: &out, Stderr: &errBuf, ServerVersion: "test-version"})
	stdout = out.String()
	stderr = errBuf.String()
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("stdout line is not a valid JSON-RPC frame: %q: %v", line, err)
		}
		responses = append(responses, resp)
	}
	return responses, stdout, stderr, runErr
}

// TestStoppingARunEndsTheToolsWork is the load-bearing proof for the
// per-invocation dispatch.
//
// Two runs are driven to the same point — each inside a tool that has started its
// work and not finished — and one run is then withdrawn. A single stack dump must
// show the stopped run's frame GONE, because the tool received the run's own
// context and its work ended, and the control's frame STILL PRESENT. The control
// is what makes the absence mean something: without it a dump missing both frames
// would read as success.
//
// A planted defect makes this fail: hand the resolved tool context.Background()
// instead of the run's context, and the withdrawal reaches nothing — the tool
// stays inside its work and parkedStoppedCommand is still on the stack.
//
// The same failure catches a design that watches for the withdrawal beside the
// dispatch and returns early: the caller is released on time, so a timing
// assertion would pass, while the tool's work runs on.
func TestStoppingARunEndsTheToolsWork(t *testing.T) {
	controlHold := make(chan struct{})
	stoppedHold := make(chan struct{})
	defer close(controlHold)
	defer close(stoppedHold)

	controlSrv := newCommandToolServer(t, parkedControlCommand)
	stoppedSrv := newCommandToolServer(t, parkedStoppedCommand)

	controlDone := make(chan error, 1)
	go func() {
		controlDone <- controlSrv.serve(context.Background(),
			&blockingReader{remaining: callFixtureCommand, hold: controlHold})
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		stoppedDone <- stoppedSrv.serve(stopCtx,
			&blockingReader{remaining: callFixtureCommand, hold: stoppedHold})
	}()

	// Both tools are now inside their work. Until this point there is nothing to
	// compare: a frame that has not been entered is absent for a reason that has
	// nothing to do with cancellation.
	waitForMCPFrame(t, "parkedControlCommand")
	waitForMCPFrame(t, "parkedStoppedCommand")

	stop()

	waitForMCPFrameToClear(t, "parkedStoppedCommand")

	frames := mcpGoroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the
	// stopped one is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlCommand") {
		t.Fatalf("the control's tool frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control's tool must still be working")
	}
	if strings.Contains(frames, "parkedStoppedCommand") {
		t.Errorf("the stopped run's tool frame is STILL on the stack while the control's is " +
			"there too: the run was withdrawn but the tool's work was never ended")
	}

	// Recorded after the frame comparison so a failing run reports BOTH what the
	// stack says and whether the loop itself came back.
	select {
	case <-stoppedDone:
	case <-time.After(runReturnCeiling):
		t.Errorf("the stopped run's message loop never returned within the liveness ceiling")
	}
}

// TestStoppingARunEndsTheCliResourceRead is the same proof for the resource-read
// surface, which reaches the runner through a different route: resources/read on
// a cli:// URI rather than tools/call.
//
// It matters separately because that route is what starts a SUBPROCESS in
// production: the runner injected here parks instead, and the production runner
// builds its child with the context this proof shows arriving, so an ended run
// ends the child. That last hop is the operating system's, not this test's, and
// is stated rather than claimed.
//
// The planted defect is the same shape: hand the runner a background context.
func TestStoppingARunEndsTheCliResourceRead(t *testing.T) {
	controlHold := make(chan struct{})
	stoppedHold := make(chan struct{})
	defer close(controlHold)
	defer close(stoppedHold)

	controlSrv := newCommandToolServer(t, parkedControlCommand)
	stoppedSrv := newCommandToolServer(t, parkedStoppedCommand)

	controlDone := make(chan error, 1)
	go func() {
		controlDone <- controlSrv.serve(context.Background(),
			&blockingReader{remaining: readCliHelp, hold: controlHold})
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		stoppedDone <- stoppedSrv.serve(stopCtx,
			&blockingReader{remaining: readCliHelp, hold: stoppedHold})
	}()

	waitForMCPFrame(t, "parkedControlCommand")
	waitForMCPFrame(t, "parkedStoppedCommand")

	stop()

	waitForMCPFrameToClear(t, "parkedStoppedCommand")

	frames := mcpGoroutineFrames()
	if !strings.Contains(frames, "parkedControlCommand") {
		t.Fatalf("the control's runner frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control's read must still be running")
	}
	if strings.Contains(frames, "parkedStoppedCommand") {
		t.Errorf("the stopped run's runner frame is STILL on the stack while the control's is " +
			"there too: the run was withdrawn but the command it started was never ended")
	}

	select {
	case <-stoppedDone:
	case <-time.After(runReturnCeiling):
		t.Errorf("the stopped run's message loop never returned within the liveness ceiling")
	}
}

// TestARunWhoseContextHasAlreadyEndedAnswersNothing pins the hand-off the two
// proofs above cannot see: that the entry point's own parameter is the context
// the loop reads, rather than one it mints.
//
// The control is what discriminates. A live run answers the ping; a withdrawn one
// must answer nothing at all. A run where both answered, or neither, would prove
// nothing about which context the loop consulted.
func TestARunWhoseContextHasAlreadyEndedAnswersNothing(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	resps, _, _, err := driveWithContext(t, withdrawn, ping)
	if err != nil {
		t.Fatalf("a withdrawn run returned an error rather than stopping cleanly: %v", err)
	}
	if len(resps) != 0 {
		t.Errorf("a run whose context had already ended answered %d message(s); it was supposed "+
			"to accept none", len(resps))
	}

	live, _, _, liveErr := driveWithContext(t, context.Background(), ping)
	if liveErr != nil {
		t.Fatalf("the live control returned an error: %v", liveErr)
	}
	if len(live) != 1 {
		t.Fatalf("the live control answered %d message(s) rather than 1, so the withdrawn arm "+
			"above proves nothing: this loop does not answer a ping under any circumstances",
			len(live))
	}
}

// waitForMCPFrame polls the stack until the named frame appears, so the
// comparison is taken once both arms are genuinely inside their work.
func waitForMCPFrame(t *testing.T, frame string) {
	t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		if strings.Contains(mcpGoroutineFrames(), frame) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared on the stack within the liveness ceiling: the arm never reached "+
		"its work, so there is nothing to compare", frame)
}
