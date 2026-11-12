// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
)

// The message loop answered one message at a time and its cancellation
// notification did nothing. The two facts were one fact: a cancellation can only
// arrive while the request it names is running, and a loop that reads the next
// message only after answering the previous one is never reading while a request
// runs. So neither half could be proved without the other, and these proofs
// drive both together through the real entry point.
//
// Every absence assertion here carries a positive control taken from the same
// output, because "no response for that id" is also what a server that stopped
// writing anything at all looks like. The only time bound anywhere in this file
// is runReturnCeiling, declared beside the run-cancellation proofs and labelled
// there as a liveness ceiling: nothing here measures how long anything takes.

// parkedTool is the seam a tool call parks in. It announces that the call has
// started, waits for that call's own context to end, and records what ended it.
//
// The released channel is what makes "still in flight" observable without a
// stack dump: a call that has announced itself and not yet released is inside
// its work by construction, at any machine speed. Each parkedTool serves one
// call — a second would close an already-closed channel — which is what every
// test here drives.
type parkedTool struct {
	entered  chan struct{}
	released chan struct{}

	mu    sync.Mutex
	cause error
}

func newParkedTool() *parkedTool {
	return &parkedTool{entered: make(chan struct{}), released: make(chan struct{})}
}

func (p *parkedTool) run(ctx context.Context, _ string, _ []string, _, _ io.Writer) error {
	close(p.entered)
	<-ctx.Done()
	p.mu.Lock()
	p.cause = context.Cause(ctx)
	p.mu.Unlock()
	close(p.released)
	return ctx.Err()
}

// recordedCause reports what ended the call's context, which is the
// discriminator between a cancellation by id and the run ending.
func (p *parkedTool) recordedCause() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cause
}

// unusedCommand is the runner for a loop no test drives a command through. It
// fails rather than parking, so a test that reaches it by accident says so
// instead of hanging until the liveness ceiling.
func unusedCommand(context.Context, string, []string, io.Writer, io.Writer) error {
	return errors.New("this server was not built to run a command")
}

// chunkedWriter is the output stream, and it deliberately does NOT make one
// Write atomic: it lays each call's bytes down in small pieces with a scheduling
// point between them, which is what a real pipe does for a payload larger than
// the operating system's atomic-write bound.
//
// A synchronised buffer would be the wrong instrument here and would mask the
// defect the one-line-per-response proof exists to catch — it makes every Write
// atomic, so two responses could never interleave however the server was
// written, and the proof would pass on a server with no serialisation at all.
// The mutex below guards the buffer itself, not the boundary between two
// callers' writes.
type chunkedWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (w *chunkedWriter) Write(p []byte) (int, error) {
	const chunk = 64
	for off := 0; off < len(p); off += chunk {
		end := off + chunk
		if end > len(p) {
			end = len(p)
		}
		w.mu.Lock()
		w.buf.Write(p[off:end])
		w.mu.Unlock()
		// Hands the scheduler an opening between chunks. A server that serialises
		// its writes is unaffected; one that does not interleaves here.
		runtime.Gosched()
	}
	return len(p), nil
}

// completeLines returns the newline-terminated lines written so far. A trailing
// fragment is not a line yet and is excluded, so a poll taken while a response is
// mid-write reads only what has landed whole.
func (w *chunkedWriter) completeLines() []string {
	w.mu.Lock()
	written := w.buf.String()
	w.mu.Unlock()
	cut := strings.LastIndexByte(written, '\n')
	if cut < 0 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(written[:cut], "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// mcpLoop is one real message loop running against a pipe, with the stream it
// answers on recorded. It drives s.serve — the entry point Run itself enters —
// so what these tests exercise is what a client exercises.
type mcpLoop struct {
	t      *testing.T
	srv    *server
	stdin  *io.PipeWriter
	out    *chunkedWriter
	served chan error
	stop   context.CancelFunc
}

func startMCPLoop(t *testing.T, run commandRunner) *mcpLoop {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	srv := newCommandToolServer(t, run)
	out := &chunkedWriter{}
	srv.out = out
	// Installed before the loop starts rather than left to the loop's own
	// initialisation, so the in-flight count can be read from this goroutine
	// without racing the write that would otherwise create it.
	srv.cancels = requestcancel.New()

	ctx, stop := context.WithCancel(context.Background())
	l := &mcpLoop{t: t, srv: srv, stdin: stdinW, out: out, served: make(chan error, 1), stop: stop}
	go func() { l.served <- srv.serve(ctx, stdinR) }()
	return l
}

// send writes one framed message. The pipe never reaches EOF until finish, so
// the loop stays alive between messages exactly as it does against a connected
// client.
func (l *mcpLoop) send(line string) {
	l.t.Helper()
	if _, err := l.stdin.Write([]byte(line)); err != nil {
		l.t.Fatalf("write to the server's input: %v", err)
	}
}

// finish ends the run, closes the input, and reports how the loop came back.
// Ending the run releases every parked tool, which is what lets the loop's wait
// for its in-flight requests return.
func (l *mcpLoop) finish() {
	l.t.Helper()
	l.stop()
	l.stdin.Close()
	select {
	case err := <-l.served:
		if err != nil {
			l.t.Errorf("the message loop returned %v; ending a run is a clean stop rather than "+
				"a transport failure", err)
		}
	case <-time.After(runReturnCeiling):
		l.t.Errorf("the message loop never returned within the liveness ceiling after the run " +
			"ended: it is still waiting for something")
	}
}

// waitForEntered blocks until the tool call has announced itself, or the
// liveness ceiling expires. Until it has, there is nothing to compare: a call
// that has not started is not in flight for a reason that has nothing to do with
// concurrency.
func (l *mcpLoop) waitForEntered(p *parkedTool) {
	l.t.Helper()
	select {
	case <-p.entered:
	case <-time.After(runReturnCeiling):
		l.t.Fatalf("the tool call never started within the liveness ceiling, so there is nothing " +
			"to compare")
	}
}

func (l *mcpLoop) waitForReleased(p *parkedTool) {
	l.t.Helper()
	select {
	case <-p.released:
	case <-time.After(runReturnCeiling):
		l.t.Fatalf("the tool call never came back within the liveness ceiling: its context was " +
			"never ended")
	}
}

// waitForAnsweredCount blocks until at least n complete response lines have been
// written, and reports what it did see when the ceiling expires.
func (l *mcpLoop) waitForAnsweredCount(n int) {
	l.t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		if len(l.out.completeLines()) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	l.t.Fatalf("only %d complete response line(s) were written within the liveness ceiling; "+
		"wanted at least %d\nwhat was written:\n%s",
		len(l.out.completeLines()), n, strings.Join(l.out.completeLines(), "\n"))
}

// waitForAnsweredID blocks until a response carrying id has been written.
func (l *mcpLoop) waitForAnsweredID(id int) {
	l.t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		for _, got := range l.answeredIDsQuietly() {
			if got == id {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	l.t.Fatalf("id %d was never answered within the liveness ceiling\nwhat was written:\n%s",
		id, strings.Join(l.out.completeLines(), "\n"))
}

// waitForInFlight blocks until the cancel registry holds n entries. The registry
// entry is released after the request's response has been written or dropped, so
// a count that has come back to n is also the point after which nothing more will
// be written for the requests that have left it.
func (l *mcpLoop) waitForInFlight(n int) {
	l.t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		if l.srv.cancels.Len() == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	l.t.Fatalf("the cancel registry held %d in-flight request(s) at the liveness ceiling; wanted %d",
		l.srv.cancels.Len(), n)
}

// answeredIDs parses every complete line and returns the ids answered, failing
// the test on any line that is not a well-formed frame. An unparseable line is
// the observable form of two responses interleaving their bytes.
func (l *mcpLoop) answeredIDs() []int {
	l.t.Helper()
	var ids []int
	for _, line := range l.out.completeLines() {
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			l.t.Errorf("a line on the output stream is not a well-formed response frame, which is "+
				"what two responses interleaving their bytes looks like: %q: %v", line, err)
			continue
		}
		var id int
		if err := json.Unmarshal(resp.ID, &id); err != nil {
			l.t.Errorf("a response carried an id this test did not send: %q: %v", string(resp.ID), err)
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// answeredIDsQuietly is answeredIDs without the assertions, for polling: a line
// that has not landed whole yet is not a defect while the poll is still running.
func (l *mcpLoop) answeredIDsQuietly() []int {
	var ids []int
	for _, line := range l.out.completeLines() {
		var resp rpcResponse
		if json.Unmarshal([]byte(line), &resp) != nil {
			continue
		}
		var id int
		if json.Unmarshal(resp.ID, &id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func containsID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// toolCallLine frames a call to the fixture command tool, whose runner is the
// test's own seam. The tool name is derived rather than spelled, so a change to
// the naming rule moves this with it instead of leaving a request no tool answers.
func toolCallLine(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call",`+
		`"params":{"name":%q,"arguments":{}}}`+"\n", id, commandToolName("aggrega work"))
}

// listToolsLine frames a request that is answered out of what the server was
// built with and reaches nothing outside the process, so it comes back promptly
// whatever else is running. Its payload is large enough to take many writes
// through chunkedWriter, which is what makes an unserialised writer observable.
func listToolsLine(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`+"\n", id)
}

// cancelledLine frames the protocol's cancellation notification. The field is
// requestId, which is the notification's own spelling and not the one the other
// frontend uses; a fixture that sent id would prove the handler reads nothing.
func cancelledLine(requestID int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled",`+
		`"params":{"requestId":%d}}`+"\n", requestID)
}

// TestASecondRequestIsAnsweredWhileTheFirstIsStillWorking is the proof of
// concurrent dispatch, and a loop that answers one message at a time cannot pass
// it: the second request would not be read until the first was answered, and the
// first never finishes on its own.
//
// A planted defect makes it fail: take the dispatch back onto the loop's own
// goroutine and the second request is never read, so the liveness ceiling
// expires with nothing answered.
func TestASecondRequestIsAnsweredWhileTheFirstIsStillWorking(t *testing.T) {
	parked := newParkedTool()
	loop := startMCPLoop(t, parked.run)
	defer loop.finish()

	loop.send(toolCallLine(1))
	// The first request must be genuinely inside its work before the second is
	// sent. Sending both and then looking would let a loop that answered them one
	// after the other pass, because the first would already have finished.
	loop.waitForEntered(parked)

	loop.send(listToolsLine(2))
	loop.waitForAnsweredID(2)

	// The control, and it is clock-free: the first request has announced itself and
	// not released, so it is inside its work at this instant by construction. Without
	// it, an answer to the second request would prove nothing — a loop that had
	// already finished the first would answer the second too.
	select {
	case <-parked.released:
		t.Fatalf("the first request had already come back by the time the second was answered, " +
			"so this proves nothing about concurrent dispatch")
	default:
	}

	if ids := loop.answeredIDs(); containsID(ids, 1) {
		t.Errorf("the first request was answered while its tool is still parked; answered ids %v", ids)
	}
}

// TestCancellingARequestByIdEndsItAndWritesNothingForIt is the proof of
// cancellation by id: the named request's context ends, no response carrying
// that id is written, and the registry comes back to empty.
//
// The ordering is what makes the absence assertion race-free rather than lucky.
// The registry entry is released after the response has been written or dropped,
// so a count back at zero is the point past which nothing more can be written for
// that request — the check below is taken after it, not alongside it.
//
// A planted defect makes it fail and no other test in this file react: make the
// cancellation notification a no-op again. The tool then parks until the run ends
// in finish, so waiting for it to come back expires the liveness ceiling.
func TestCancellingARequestByIdEndsItAndWritesNothingForIt(t *testing.T) {
	parked := newParkedTool()
	loop := startMCPLoop(t, parked.run)
	defer loop.finish()

	loop.send(toolCallLine(1))
	loop.waitForEntered(parked)
	loop.waitForInFlight(1)

	loop.send(cancelledLine(1))

	loop.waitForReleased(parked)
	// The run is still live, so the cause discriminates: a context that ended at
	// all here ended because the client cancelled that request by id, and not
	// because the run was withdrawn under it.
	if cause := parked.recordedCause(); !errors.Is(cause, errCancelledByClient) {
		t.Fatalf("the cancelled request's tool recorded cause %v; wanted the client's cancellation",
			cause)
	}

	loop.waitForInFlight(0)

	// The positive control, read from the same output: a request sent after the
	// cancellation is answered. Without it, "nothing for id 1" is also what a
	// server that stopped writing altogether looks like.
	loop.send(listToolsLine(2))
	loop.waitForAnsweredID(2)

	if ids := loop.answeredIDs(); containsID(ids, 1) {
		t.Errorf("a response carrying the cancelled request's id was written after it was "+
			"cancelled; answered ids %v", ids)
	}
}

// TestCancellingAnIdNothingIsWorkingOnDoesNothing covers the two ways a
// cancellation arrives too late or too early to name anything: an id that was
// never in flight, and an id whose request has already been answered. Neither
// does anything, and the server keeps serving — which is the assertion, since
// "nothing happened" is otherwise indistinguishable from a server that fell over
// quietly.
func TestCancellingAnIdNothingIsWorkingOnDoesNothing(t *testing.T) {
	loop := startMCPLoop(t, unusedCommand)
	defer loop.finish()

	// An id that was never in flight.
	loop.send(cancelledLine(99))

	// An id whose request has already been answered.
	loop.send(listToolsLine(1))
	loop.waitForAnsweredID(1)
	loop.waitForInFlight(0)
	loop.send(cancelledLine(1))

	// The control: a further request is still answered after both.
	loop.send(listToolsLine(2))
	loop.waitForAnsweredID(2)

	if ids := loop.answeredIDs(); len(ids) != 2 || !containsID(ids, 1) || !containsID(ids, 2) {
		t.Errorf("answered ids %v; wanted exactly the two requests that were sent", ids)
	}
}

// TestConcurrentRequestsAreEachAnsweredOnceOnTheirOwnLine is the proof that the
// single writer is serialised: a batch of requests handed to the loop at once
// comes back as one well-formed line each, every id exactly once.
//
// The discriminator is chunkedWriter, which does not make one Write atomic. A
// server that serialises its responses can never interleave them, so this
// direction of the proof is certain; a server that does not is caught the moment
// two chunk streams cross, which with this many concurrent payloads of this size
// is what happens. The asymmetry is deliberate — a correct server cannot fail
// here — and it is why the assertion is on the frames parsing rather than on any
// timing.
//
// A planted defect makes it fail: drop the mutex from write, and lines stop
// parsing.
func TestConcurrentRequestsAreEachAnsweredOnceOnTheirOwnLine(t *testing.T) {
	const requests = 16

	loop := startMCPLoop(t, unusedCommand)
	defer loop.finish()

	var batch strings.Builder
	want := make([]int, 0, requests)
	for id := 1; id <= requests; id++ {
		want = append(want, id)
		batch.WriteString(listToolsLine(id))
	}
	loop.send(batch.String())

	loop.waitForAnsweredCount(requests)
	loop.waitForInFlight(0)

	got := loop.answeredIDs()
	if len(got) != len(want) {
		t.Fatalf("answered %d response line(s) for %d request(s): %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("answered ids %v; wanted every id exactly once: %v", got, want)
		}
	}
}
