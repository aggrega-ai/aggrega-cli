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
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newPoolTestServer builds a Server wired for an in-memory dispatch-loop
// run with the supplied worker-pool size and stdin/stdout streams. The
// hook, when non-nil, runs at the top of every handler — tests use it to
// hold workers and exercise queue back-pressure.
func newPoolTestServer(t *testing.T, in io.Reader, out io.Writer, poolSize int, hook func()) *Server {
	t.Helper()
	// A real directory, because a request naming a workspace root is refused
	// unless that root is one — so a pool test driving real requests needs a
	// workspace that exists rather than a plausible-looking path.
	return newPoolTestServerAt(t, t.TempDir(), in, out, poolSize, hook)
}

// newPoolTestServerAt is newPoolTestServer over a workspace root the caller
// already holds.
//
// A test that writes its request script BEFORE building the server needs the
// root first, because each request in that script names the workspace it acts
// on — so the root cannot be something the constructor invents.
func newPoolTestServerAt(t *testing.T, root string, in io.Reader, out io.Writer, poolSize int, hook func()) *Server {
	t.Helper()
	cfg := workspaceconfig.Default()
	cfg.Concurrency.PoolSize = poolSize
	sess := newTestSession(t, root, in, out)
	sess.launchCfg = cfg
	sess.testInDispatchHook = hook
	return newTestWorkspace(sess, root)
}

// readResponses reads exactly n Content-Length-framed responses from r.
func readResponses(t *testing.T, r *bufio.Reader, n int) []jsonRPCResponse {
	t.Helper()
	out := make([]jsonRPCResponse, 0, n)
	for i := 0; i < n; i++ {
		body, err := readMessage(r)
		if err != nil {
			t.Fatalf("read response %d/%d: %v", i+1, n, err)
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("unmarshal response %d: %v (body %s)", i+1, err, body)
		}
		out = append(out, resp)
	}
	return out
}

// TestSerializerOrdersByArrivalSequence is the load-bearing proof that
// the ordering mechanism is correct at concurrency > 1 even though 3a
// runs serial: frames handed to the serializer in completion order
// {3, 1, 2} emit in arrival-sequence order {1, 2, 3}. It also pins the
// two subtle cases — a response-less slot (a body-less done-frame must
// advance the cursor without stalling) and an immediate notification
// (must emit on receipt, not wait for ordering).
func TestSerializerOrdersByArrivalSequence(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	ser := newSerializer(&out)
	done := make(chan struct{})
	go func() {
		ser.run()
		close(done)
	}()

	frame := func(seq int) []byte {
		b, _ := marshalErrorResponse(json.RawMessage([]byte(`"x"`)), seq, "")
		return b
	}

	// Hand seq 3 first (out of order), then an immediate notification,
	// then seq 1, then a response-less done-frame for seq 2.
	ser.emit(3, frame(3))
	ser.emitNow([]byte(`{"jsonrpc":"2.0","method":"note"}`))
	ser.emit(1, frame(1))
	ser.emit(2, nil) // response-less slot: advances the cursor, writes nothing

	ser.closeAndWait()
	<-done

	select {
	case err := <-ser.errCh:
		t.Fatalf("serializer reported write error: %v", err)
	default:
	}

	// Expected wire order: the immediate notification (emitted on
	// receipt, before any ordered frame could flush because seq 1 had
	// not yet arrived), then response 1, then response 3 (seq 2 produced
	// no bytes but advanced the cursor so seq 3 could flush).
	r := bufio.NewReader(&out)
	var codes []int
	var sawNotification bool
	for {
		body, err := readMessage(r)
		if err != nil {
			break
		}
		var probe struct {
			Method string `json:"method"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if uerr := json.Unmarshal(body, &probe); uerr != nil {
			t.Fatalf("unmarshal emitted frame: %v (body %s)", uerr, body)
		}
		if probe.Method == "note" {
			sawNotification = true
			if len(codes) != 0 {
				t.Errorf("notification emitted after an ordered response; want immediate (before any ordered frame)")
			}
			continue
		}
		if probe.Error != nil {
			codes = append(codes, probe.Error.Code)
		}
	}
	if !sawNotification {
		t.Errorf("immediate notification never emitted")
	}
	want := []int{1, 3}
	if len(codes) != len(want) {
		t.Fatalf("ordered response codes = %v; want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Errorf("ordered response[%d] code = %d; want %d (arrival-sequence order, seq 2 was response-less)", i, codes[i], want[i])
		}
	}
}

// TestDispatchIDCorrelatedResponsesThroughSerializer drives several
// distinct requests through the real dispatch loop at concurrency 1 and
// asserts each response is well-formed and id-correlated to its request.
func TestDispatchIDCorrelatedResponsesThroughSerializer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var stdin bytes.Buffer
	ids := []int{7, 8, 9}
	for _, id := range ids {
		stdin.Write(framedRequestAt(t, root, id, "aggrega/releaseCandidates", map[string]any{}))
	}
	stdin.Write(framedNotification("aggrega/shutdown", nil))

	var stdout bytes.Buffer
	srv := newPoolTestServerAt(t, root, &stdin, &stdout, 1, nil)
	if err := srv.dispatchLoop(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}

	r := bufio.NewReader(&stdout)
	resps := readResponses(t, r, len(ids))
	for i, resp := range resps {
		if resp.Error != nil {
			t.Errorf("response %d carried error: %+v", i, resp.Error)
		}
		var id int
		if err := json.Unmarshal(resp.ID, &id); err != nil {
			t.Fatalf("response %d id parse: %v", i, err)
		}
		// At concurrency 1 in arrival order, responses come back in the
		// submitted id order.
		if id != ids[i] {
			t.Errorf("response %d id = %d; want %d (arrival order)", i, id, ids[i])
		}
	}
}

// TestDispatchIDCorrelatedUnderOutOfOrderCompletion is the live-loop
// proof of the serializer's ordering contract at concurrency > 1: with
// handlers driven to COMPLETE out of submission order, every response is
// still well-formed, id-correlated to its request, and emitted on the
// wire in arrival-sequence order — with no torn or interleaved frames.
//
// The forced reordering uses testCtxHook (which runs at the top of each
// id-bearing handler, in its worker goroutine) to park every handler on
// a per-id gate. A controller goroutine then releases the parked handlers
// strictly highest-id-first, waiting for each released handler to exit
// the hook before releasing the next-lower id. So among any set of
// handlers concurrently in flight, the higher id always completes first —
// the live analogue of the unit-level {3,1,2} reordering. This holds at
// any pool size: with pool < N only a window of handlers is in flight at
// once, and the controller reverses completion within each window, which
// is enough to drive the serializer's reordering buffer.
//
// Assertions: (i) every submitted id appears exactly once on the wire (no
// drop, no duplicate); (ii) each response is well-formed JSON-RPC
// (jsonrpc "2.0", exactly one of result/error, an id we submitted); (iii)
// the wire id sequence is the submission order regardless of completion
// interleave (the serializer's arrival-order emission contract). The
// well-formed-framing check (ii), driven against concurrently-produced
// stdout, is the framing-integrity-under-concurrency guarantee. Run under
// -race.
func TestDispatchIDCorrelatedUnderOutOfOrderCompletion(t *testing.T) {
	t.Parallel()
	for _, pool := range []int{4, 16} {
		pool := pool
		t.Run(fmt.Sprintf("pool=%d", pool), func(t *testing.T) {
			t.Parallel()
			const n = 16
			ids := make([]int, n)
			for i := range ids {
				ids[i] = i + 1
			}

			// release[id] gates handler id inside the hook; entered carries
			// each id as its handler parks; exited[id] is closed by the
			// handler as it leaves the hook so the controller can serialize
			// completions.
			release := make(map[int]chan struct{}, n)
			exited := make(map[int]chan struct{}, n)
			for _, id := range ids {
				release[id] = make(chan struct{})
				exited[id] = make(chan struct{})
			}
			entered := make(chan int, n)

			stdinR, stdinW := io.Pipe()
			// io.Pipe + concurrent collecting reader: at pool 4 the queue
			// holds 4*queueDepthFactor and 4 sit in workers, so all 16 held
			// handlers can exceed in-flight capacity; reading concurrently
			// keeps submission from deadlocking on a full queue.
			stdoutR, stdoutW := io.Pipe()
			srv := newPoolTestServer(t, stdinR, stdoutW, pool, nil)
			srv.testCtxHook = func(ctx context.Context, id json.RawMessage) {
				var n int
				if json.Unmarshal(id, &n) != nil {
					return // the shutdown notification carries no id
				}
				entered <- n
				<-release[n]
				close(exited[n])
			}

			// Controller: release the highest currently-parked handler
			// first, wait for it to leave the hook, then the next-highest,
			// so completion order is strictly descending within every
			// in-flight window.
			ctrlDone := make(chan struct{})
			go func() {
				defer close(ctrlDone)
				var parked []int
				for seen := 0; seen < n; {
					// Drain whatever has parked so far, then settle briefly
					// so a full in-flight window (up to pool handlers) is
					// captured before we pick the highest to release.
					parked = append(parked, <-entered)
					seen++
				drainSettle:
					for {
						select {
						case id := <-entered:
							parked = append(parked, id)
							seen++
						case <-time.After(20 * time.Millisecond):
							break drainSettle
						}
					}
					// Release highest-first, serializing on each exit.
					sort.Sort(sort.Reverse(sort.IntSlice(parked)))
					for _, id := range parked {
						close(release[id])
						<-exited[id]
					}
					parked = parked[:0]
				}
			}()

			// Collect responses concurrently so submission never blocks on a
			// full queue. Read exactly n framed responses, then return.
			type collected struct {
				resps []jsonRPCResponse
				err   error
			}
			readDone := make(chan collected, 1)
			go func() {
				r := bufio.NewReader(stdoutR)
				out := make([]jsonRPCResponse, 0, n)
				for i := 0; i < n; i++ {
					body, err := readMessage(r)
					if err != nil {
						readDone <- collected{err: err}
						return
					}
					var resp jsonRPCResponse
					if err := json.Unmarshal(body, &resp); err != nil {
						readDone <- collected{err: fmt.Errorf("response %d not well-formed JSON: %w (body %s)", i+1, err, body)}
						return
					}
					out = append(out, resp)
				}
				readDone <- collected{resps: out}
			}()

			loopDone := make(chan error, 1)
			go func() {
				err := srv.dispatchLoop(context.Background(), 500*time.Millisecond)
				stdoutW.Close()
				loopDone <- err
			}()

			go func() {
				for _, id := range ids {
					stdinW.Write(framedRequestIn(t, srv, id, "aggrega/releaseCandidates", map[string]any{}))
				}
				stdinW.Write(framedNotification("aggrega/shutdown", nil))
				stdinW.Close()
			}()

			var got collected
			select {
			case got = <-readDone:
			case <-time.After(10 * time.Second):
				t.Fatal("did not read all responses; likely a stalled reorder window")
			}
			if got.err != nil {
				t.Fatalf("read responses: %v", got.err)
			}

			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch loop did not exit")
			}
			<-ctrlDone

			if len(got.resps) != n {
				t.Fatalf("response count = %d; want %d", len(got.resps), n)
			}
			submitted := make(map[int]bool, n)
			for _, id := range ids {
				submitted[id] = true
			}
			seen := make(map[int]int, n)
			wire := make([]int, 0, n)
			for i, resp := range got.resps {
				// (ii) well-formed JSON-RPC: version, exactly one of
				// result/error, an id we submitted.
				if resp.JSONRPC != "2.0" {
					t.Errorf("response %d jsonrpc = %q; want \"2.0\"", i, resp.JSONRPC)
				}
				hasResult := len(resp.Result) > 0
				hasError := resp.Error != nil
				if hasResult == hasError {
					t.Errorf("response %d must carry exactly one of result/error (result=%v error=%v)", i, hasResult, hasError)
				}
				var id int
				if err := json.Unmarshal(resp.ID, &id); err != nil {
					t.Fatalf("response %d id parse: %v", i, err)
				}
				if !submitted[id] {
					t.Errorf("response %d carries unsubmitted id %d", i, id)
				}
				seen[id]++
				wire = append(wire, id)
			}
			// (i) every submitted id appears exactly once.
			for _, id := range ids {
				if seen[id] != 1 {
					t.Errorf("id %d appeared %d times on the wire; want exactly 1", id, seen[id])
				}
			}
			// (iii) wire order is the submission (arrival) order regardless
			// of the forced reverse completion interleave.
			for i, id := range wire {
				if id != ids[i] {
					t.Errorf("wire[%d] id = %d; want %d (arrival-sequence emission, not completion order)", i, id, ids[i])
				}
			}
		})
	}
}

// TestBoundedQueueBackpressureBlocks proves the bounded queue blocks the
// submitting side when full and never drops a request: with workers held
// by a hook, submission stalls once the queue saturates, and after the
// workers are released every submitted request produces a response.
func TestBoundedQueueBackpressureBlocks(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var hookOnce sync.Once
	firstEntered := make(chan struct{})
	hook := func() {
		hookOnce.Do(func() { close(firstEntered) })
		<-release
	}

	// Pool size 1 ⇒ queue capacity queueDepthFactor. One request is in
	// the worker (held by the hook), queueDepthFactor sit in the queue,
	// and the next submit must block.
	total := 1 + queueDepthFactor + 1
	stdinR, stdinW := io.Pipe()
	var stdout bytes.Buffer
	srv := newPoolTestServer(t, stdinR, &stdout, 1, hook)

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

	// Feed the saturating requests, then wait for the first worker to be
	// held, then confirm one more submission is in flight but blocked.
	go func() {
		for i := 0; i < total; i++ {
			stdinW.Write(framedRequestIn(t, srv, i+1, "aggrega/releaseCandidates", map[string]any{}))
		}
	}()

	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first worker never entered the hook")
	}

	// Give the reader time to fill the queue; the loop should now be
	// parked on a full-queue submit and NOT have exited.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-loopDone:
		t.Fatalf("dispatch loop exited while back-pressured: %v", err)
	default:
	}

	// Release the held workers; every request must now drain. Then send
	// shutdown so the loop exits and we can read all responses.
	close(release)
	stdinW.Write(framedNotification("aggrega/shutdown", nil))
	stdinW.Close()

	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("dispatchLoop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch loop did not exit after release")
	}

	r := bufio.NewReader(&stdout)
	resps := readResponses(t, r, total)
	if len(resps) != total {
		t.Fatalf("response count = %d; want %d (no request dropped under back-pressure)", len(resps), total)
	}
}

// TestByteIdenticalToSequentialAtConcurrencyOne pins the determinism
// invariant: the dispatch loop's stdout bytes at pool size 1 are
// byte-identical to the bytes produced by running the same request
// script through the handler directly with no pool. The serializer
// changes which goroutine writes, not the bytes or their order.
func TestByteIdenticalToSequentialAtConcurrencyOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := [][]byte{
		framedRequestAt(t, root, 1, "aggrega/releaseCandidates", map[string]any{}),
		framedRequestAt(t, root, 2, "aggrega/bogus-method", map[string]any{}),
		framedRequestAt(t, root, 3, "aggrega/releaseCandidates", map[string]any{}),
	}

	// Reference bytes: drive each request straight through handle via the
	// per-request serializer flow, one at a time, into one buffer.
	var reference bytes.Buffer
	for _, framed := range script {
		bodyStart := bytes.Index(framed, []byte("\r\n\r\n")) + 4
		var req jsonRPCRequest
		if err := json.Unmarshal(framed[bodyStart:], &req); err != nil {
			t.Fatalf("unmarshal script request: %v", err)
		}
		srv := newPoolTestServer(t, nil, &reference, 1, nil)
		srv.serverCtx, srv.serverCancel = context.WithCancel(context.Background())
		srv.cancelTracker = requestcancel.New()
		ser := newSerializer(&reference)
		go ser.run()
		ser.emit(1, srv.handle(ser, 1, &req))
		ser.closeAndWait()
		srv.serverCancel()
	}

	// Actual bytes: run the same script through the full dispatch loop at
	// pool size 1.
	var stdin, stdout bytes.Buffer
	for _, framed := range script {
		stdin.Write(framed)
	}
	stdin.Write(framedNotification("aggrega/shutdown", nil))
	srv := newPoolTestServer(t, &stdin, &stdout, 1, nil)
	if err := srv.dispatchLoop(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}

	if !bytes.Equal(reference.Bytes(), stdout.Bytes()) {
		t.Errorf("dispatch-loop output not byte-identical to sequential reference\n ref: %q\n got: %q",
			reference.String(), stdout.String())
	}
}
