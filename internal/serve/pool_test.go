// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// newPoolTestServer builds a Server wired for an in-memory dispatch-loop
// run with the supplied worker-pool size and stdin/stdout streams. The
// hook, when non-nil, runs at the top of every handler — tests use it to
// hold workers and exercise queue back-pressure.
func newPoolTestServer(in io.Reader, out io.Writer, poolSize int, hook func()) *Server {
	cfg := workspaceconfig.Default()
	cfg.Concurrency.PoolSize = poolSize
	return &Server{
		workspaceRoot:      "/tmp/test-workspace",
		wsConfig:           cfg,
		lazyCatalog:        catalog.NewLazyLookup("/tmp/test-workspace", "", nil, nil),
		parseCache:         newCache(0, 0),
		diagnostics:        map[string][]Diagnostic{},
		in:                 bufio.NewReader(in),
		out:                out,
		stderr:             io.Discard,
		positionEncoding:   yamlpos.PositionEncodingUTF16,
		testInDispatchHook: hook,
	}
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
	var stdin bytes.Buffer
	ids := []int{7, 8, 9}
	for _, id := range ids {
		stdin.Write(framedRequest(id, "aggrega/releaseCandidates", map[string]any{}))
	}
	stdin.Write(framedNotification("aggrega/shutdown", nil))

	var stdout bytes.Buffer
	srv := newPoolTestServer(&stdin, &stdout, 1, nil)
	if err := srv.dispatchLoop(500 * time.Millisecond); err != nil {
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
	srv := newPoolTestServer(stdinR, &stdout, 1, hook)

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(500 * time.Millisecond) }()

	// Feed the saturating requests, then wait for the first worker to be
	// held, then confirm one more submission is in flight but blocked.
	go func() {
		for i := 0; i < total; i++ {
			stdinW.Write(framedRequest(i+1, "aggrega/releaseCandidates", map[string]any{}))
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

	script := [][]byte{
		framedRequest(1, "aggrega/releaseCandidates", map[string]any{}),
		framedRequest(2, "aggrega/bogus-method", map[string]any{}),
		framedRequest(3, "aggrega/releaseCandidates", map[string]any{}),
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
		srv := newPoolTestServer(nil, &reference, 1, nil)
		srv.serverCtx, srv.serverCancel = context.WithCancel(context.Background())
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
	srv := newPoolTestServer(&stdin, &stdout, 1, nil)
	if err := srv.dispatchLoop(500 * time.Millisecond); err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}

	if !bytes.Equal(reference.Bytes(), stdout.Bytes()) {
		t.Errorf("dispatch-loop output not byte-identical to sequential reference\n ref: %q\n got: %q",
			reference.String(), stdout.String())
	}
}
