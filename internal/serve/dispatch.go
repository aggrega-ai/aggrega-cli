// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// jsonRPCRequest is the wire shape for an incoming request. ID is
// nullable for notifications (JSON-RPC 2.0 notifications carry no id
// field and expect no response); the daemon supports both request and
// notification framing for forward compatibility, but every method
// shipped today returns a response.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is the wire shape for an outgoing response. Exactly
// one of Result or Error is populated; the json.RawMessage encoding
// supports either shape without distinct response types.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError carries the error envelope shape per JSON-RPC 2.0.
// Data, when non-nil, carries structured source-location information
// that the IDE client uses to position diagnostics at a precise
// line/column. See errorData (errors_data.go) for the schema. When
// nil, omitempty drops the field from the wire and the client falls
// back to file-level positioning.
//
// cause is the error Message was rendered from, kept beside the
// rendered text so a caller that turns this envelope into something
// richer than a string still has the failure itself in hand — the
// condition a diagnostic reports its code from, for one. Rendering an
// error to text is one-way, so a caller that is handed only the text
// cannot recover what the failure was; carrying the error alongside is
// what keeps that open.
//
// It is unexported, so it never travels: the wire shape of an error
// envelope is exactly the three tagged fields above. Code stays the
// only code on this object and is the numeric JSON-RPC one, which
// names a state of the workspace rather than a condition of the
// toolchain. A site that renders no error, or that holds none, leaves
// cause nil, and a reader treats nil as nothing to report rather than
// as an omission.
type jsonRPCError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *errorData `json:"data,omitempty"`
	cause   error
}

// readMessage parses one Content-Length-framed JSON-RPC message from r.
// Returns the raw JSON body. The reader is positioned at the start of
// the next message after the call returns.
//
// Returns io.EOF when the stream is fully consumed.
func readMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && line == "" {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			n, perr := strconv.Atoi(value)
			if perr != nil {
				return nil, fmt.Errorf("parse content-length %q: %w", value, perr)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

// writeMessage emits one Content-Length-framed JSON-RPC message to w.
// The body is written verbatim; the caller is responsible for ensuring
// it is valid JSON.
func writeMessage(w io.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// incomingMessage is the wire-frame payload the reader goroutine
// forwards to the main loop. body is the JSON-RPC body verbatim;
// err is non-nil only when the reader hit io.EOF or an
// unrecoverable framing failure, at which point the channel closes.
type incomingMessage struct {
	body []byte
	err  error
}

// dispatchLoop spawns a reader goroutine to consume stdin and runs a
// select-driven submission loop in the main goroutine. The reader peeks
// at each message's method field to drive the drain-then-abort shutdown
// sequence; all messages — including the shutdown notifications — are
// forwarded onto the message channel, and the main loop assigns each an
// arrival sequence.
//
// Shutdown is a two-step, drain-then-abort sequence:
//
//   - The FIRST aggrega/shutdown (or stdin EOF) enters DRAINING. The
//     main loop stops accepting new work — subsequent requests are
//     rejected and subsequent notifications dropped at the acceptance
//     boundary — and begins draining the worker pool in the background,
//     letting in-flight work (an in-flight validation) run to
//     completion. serverCtx is NOT cancelled, so accepted work is never
//     cut short. When the background drain completes, the loop exits
//     cleanly.
//   - The SECOND aggrega/shutdown ABORTS. The reader cancels serverCtx
//     at the instant it parses the message, so in-flight handlers return
//     at their next phase boundary, and it arms a tight drain-budget
//     backstop timer that gives the loop an exit trigger once
//     cancellation has propagated. The background pool drain still
//     precedes serializer teardown, so a handler that does not observe
//     cancellation promptly is bounded only by its own completion; the
//     spawning client's force escalation is the ultimate stop for such
//     work.
//
// Each message off the single stdin stream gets a dense, gap-free
// arrival sequence. The id-order stdout serializer emits responses in
// that sequence regardless of which worker completes first, so output
// is in request order. At the default pool size of one, workers run
// serially in arrival order and the serializer's reordering buffer
// stays empty. The serializer is the sole owner of stdout, so
// concurrent workers never race the writer.
//
// Per-request error handling:
//   - Header parse error (malformed framing): logged to stderr, loop
//     continues — the framing reader cannot resync without consuming
//     more bytes, so the next request likely fails too; the IDE
//     client is responsible for terminating and respawning.
//   - JSON body parse error: returns the JSON-RPC -32700 parse-error
//     envelope; loop continues.
//   - Unknown method: returns the -32601 method-not-found envelope;
//     loop continues.
//   - Handler internal error: returns the -32603 internal-error
//     envelope or an application-code envelope; loop continues.
func (s *Server) dispatchLoop(drainTimeout time.Duration) error {
	// serverCtx is the session-level context every per-request context
	// derives from. It is initialised HERE, before the reader goroutine
	// below is spawned and before any handle() call — that ordering is
	// what makes the reader's later read of s.serverCancel and handle()'s
	// reads of s.serverCtx race-free (the fields are written once, then
	// only read across goroutines, with no concurrent writer). The nil
	// guard lets Run and the subprocess test harness construct a Server
	// as a struct literal and reach this single init point; the in-memory
	// unit-test harness, which drives handle() without a reader goroutine,
	// initialises serverCtx at construction instead. The deferred cancel
	// is the exit backstop (cancels derived contexts on EOF, read error,
	// or drain); the primary cancel trigger is the reader goroutine on
	// aggrega/shutdown arrival — see below.
	if s.serverCtx == nil {
		s.serverCtx, s.serverCancel = context.WithCancel(context.Background())
	}
	if s.cancelTracker == nil {
		s.cancelTracker = newCancelRegistry()
	}
	defer s.serverCancel()

	messages := make(chan incomingMessage)
	drainTimerInstall := make(chan (<-chan time.Time), 1)

	// The serializer owns stdout for the dispatch loop's lifetime; every
	// write surface routes through it so concurrent workers never race
	// the single writer. It starts before the pool so a worker can never
	// emit a frame before the serializer is receiving.
	ser := newSerializer(s.out)
	go ser.run()

	// The worker pool runs handlers off the bounded queue. A pool size
	// of one runs requests serially in arrival order; the value comes
	// from workspace config, clamped to a minimum of one. Each worker
	// closes the request's ordered serializer slot exactly once after
	// the handler returns.
	pool := newWorkerPool(s.wsConfig.Concurrency.PoolSize, func(q queuedRequest) {
		body := s.handle(ser, q.seq, q.req)
		ser.emit(q.seq, body)
	})

	go func() {
		defer close(messages)
		// shutdownCount is reader-local and never read by any other
		// goroutine. It distinguishes the FIRST aggrega/shutdown (drain)
		// from the SECOND (abort). The FIRST does NOT cancel serverCtx: an
		// in-flight handler runs to completion so accepted work is never cut
		// short. The SECOND cancels serverCtx here — at the instant the
		// message is parsed, so in-flight work is signalled to stop without
		// waiting for the main loop to dequeue the message — and arms the
		// tight drain-budget backstop timer, emitting its channel onto
		// drainTimerInstall (cap 1) for the main loop to watch. serverCtx is
		// idempotent to cancel and the install channel is cap 1, so a
		// third-or-later shutdown is a no-op.
		shutdownCount := 0
		for {
			body, err := readMessage(s.in)
			if err != nil {
				messages <- incomingMessage{err: err}
				return
			}
			// Peek the method before forwarding so the reader can arm the
			// second-shutdown abort without waiting for the main goroutine
			// to observe.
			var peek struct {
				Method string `json:"method"`
			}
			if jerr := json.Unmarshal(body, &peek); jerr == nil && peek.Method == "aggrega/shutdown" {
				shutdownCount++
				if shutdownCount == 2 {
					s.serverCancel()
					timer := time.NewTimer(drainTimeout)
					drainTimerInstall <- timer.C
				}
			}
			messages <- incomingMessage{body: body}
		}
	}()

	// drainStarted guards a single pool.drain. The FIRST aggrega/shutdown
	// begins draining the worker pool in the background so the main loop
	// stays responsive to a SECOND shutdown while accepted in-flight work
	// finishes; drainDone closes when that background drain completes and
	// is the loop's clean-exit trigger. Both are touched only by the main
	// goroutine (startDrain, teardown, and the select all run there).
	var drainStarted bool
	var drainDone chan struct{}
	startDrain := func() {
		if drainStarted {
			return
		}
		drainStarted = true
		drainDone = make(chan struct{})
		go func(d chan struct{}) {
			pool.drain()
			close(d)
		}(drainDone)
	}

	// teardown completes the pool drain — waiting on the background drain
	// if one is already running, else draining inline — then flushes and
	// stops the serializer, returning any fatal stdout write error the
	// serializer reported. It runs on every loop exit: EOF, read error,
	// background-drain completion, or the second-shutdown backstop timer.
	// The pool drain always precedes the serializer close so no worker can
	// emit a frame after the serializer has stopped.
	teardown := func() error {
		if drainStarted {
			<-drainDone
		} else {
			pool.drain()
		}
		ser.closeAndWait()
		select {
		case err := <-ser.errCh:
			return err
		default:
			return nil
		}
	}

	// seq is the dense, monotonic, gap-free arrival sequence assigned to
	// every message off the single stdin stream. It is the canonical
	// request order the serializer emits responses in. Each message
	// consumes exactly one seq, and each seq's ordered slot is closed
	// exactly once — by a worker after the handler returns, or directly
	// here for a message that never reaches a worker (a parse failure).
	var seq int64
	var drainCh <-chan time.Time
	for {
		select {
		case d := <-drainTimerInstall:
			drainCh = d
		case werr := <-ser.errCh:
			// A fatal stdout write failed; a broken stdout terminates
			// the daemon. Tear down the pool and serializer, then
			// surface the error so the process exits non-zero.
			_ = teardown()
			return werr
		case msg, ok := <-messages:
			if !ok {
				return teardown()
			}
			if msg.err != nil {
				if errors.Is(msg.err, io.EOF) {
					return teardown()
				}
				_ = teardown()
				return fmt.Errorf("read message: %w", msg.err)
			}
			seq++
			var req jsonRPCRequest
			if jerr := json.Unmarshal(msg.body, &req); jerr != nil {
				body, merr := marshalErrorResponse(nil, jsonRPCParseError, "parse error: "+jerr.Error())
				if merr != nil {
					_ = teardown()
					return merr
				}
				ser.emit(seq, body)
				continue
			}
			// $/cancelRequest is handled inline on the main loop, not
			// submitted to the pool: the pickup is a fast lock-guarded map
			// lookup, and queueing it behind other work would defeat the
			// point — at pool size one it would sit behind the very request
			// it cancels, which holds the only worker. Handling it here
			// makes cancellation prompt. It touches neither shuttingDown nor
			// the pool, and runs BEFORE the drain gate below so a cancel
			// arriving mid-shutdown still interrupts its in-flight request.
			// The notification carries no id and yields no response; the
			// seq's ordered slot is closed with a body-less done-frame so
			// the serializer cursor advances. Malformed params are dropped
			// silently — a notification has no response channel to report an
			// error on.
			if req.Method == "$/cancelRequest" {
				var cp cancelParams
				if json.Unmarshal(req.Params, &cp) == nil {
					s.cancelTracker.cancel(cp.ID)
				}
				ser.emit(seq, nil)
				continue
			}
			// Acceptance-boundary drain gate. The single-threaded main
			// loop is the serialization point for shutdown ordering: the
			// shutdown message is handled HERE, on acceptance, and never
			// submitted to the worker pool, so every message the loop
			// accepts afterward strictly observes the draining state.
			// Handling it off the main loop would let the loop accept and
			// submit a following message before the drain state was
			// visible, under a multi-worker pool.
			if req.Method == "aggrega/shutdown" {
				// Acceptance of a shutdown: enter draining. The FIRST
				// shutdown sets the flag and begins the background pool
				// drain so accepted in-flight work finishes naturally — it
				// does NOT cancel serverCtx. The SECOND shutdown's abort
				// (serverCancel + backstop timer) is armed by the reader at
				// arrival. The shutdown message itself yields no response, so
				// close its ordered slot with a body-less done-frame.
				s.shuttingDown.Store(true)
				startDrain()
				ser.emit(seq, nil)
				continue
			}
			if s.shuttingDown.Load() {
				// Accepted after shutdown was observed: drop at the loop
				// rather than submitting. The seq is already assigned, so
				// its ordered slot must be closed or the serializer
				// stalls waiting for it.
				if isNotification(&req) {
					// Drop silently; close the slot with a body-less
					// done-frame so the serializer cursor advances.
					ser.emit(seq, nil)
					continue
				}
				body, merr := marshalErrorResponse(req.ID, errShuttingDown, "daemon shutting down")
				if merr != nil {
					_ = teardown()
					return merr
				}
				ser.emit(seq, body)
				continue
			}
			pool.submit(queuedRequest{seq: seq, req: &req})
		case <-drainDone:
			// The first-shutdown (or EOF) background pool drain finished:
			// all accepted in-flight work completed. Exit cleanly. A nil
			// drainDone (no drain started yet) makes this case never ready.
			return teardown()
		case <-drainCh:
			// The second-shutdown backstop timer fired: complete teardown.
			return teardown()
		}
	}
}

// handle dispatches one request and returns the response body for its
// ordered serializer slot (nil when the request produces no response —
// a notification or a request dropped during drain — in which case the
// worker closes the slot with a body-less done-frame).
//
// The per-request sink carrying the request's serializer and arrival
// sequence is installed on the context here; every write surface the
// handler reaches reads it back, so there is a single route from any
// handler to stdout. Response surfaces record their body on the sink;
// notification surfaces emit immediately through the serializer.
func (s *Server) handle(ser *serializer, seq int64, req *jsonRPCRequest) []byte {
	if s.testInDispatchHook != nil {
		s.testInDispatchHook()
	}
	// Per-request context derived from the session-level serverCtx.
	// dispatchLoop initialises serverCtx before spawning the reader
	// goroutine and the worker pool (the test harness initialises it at
	// Server construction), so this read is never concurrent with a
	// write. When the reader cancels serverCtx on the SECOND
	// aggrega/shutdown (the abort), this child cancels too, so an
	// in-flight handler observes ctx.Err() at its next interruption point
	// and returns within the drain budget. cancel runs when handle
	// returns, releasing the child promptly.
	ctx, cancel := context.WithCancel(s.serverCtx)
	defer cancel()
	// The render pipeline's per-request Loader draws on the process-wide shared
	// compilation cache automatically (see wasm.NewCachingLoaderFromContext), so
	// concurrent requests reuse warm compiled guests with no per-request cache
	// plumbing. The cache memoizes only compiled machine code — a pure function
	// of the guest bytes — so sharing it across workers leaves output unchanged.
	// Register the SAME per-request cancel handle under the request's id
	// so a specific in-flight request is addressable by id. This is a
	// second address for the child handle above, not a second cancel
	// authority. Notifications carry no id and are not tracked (track is a
	// no-op on an empty id). untrack pairs with the defer'd cancel, so the
	// registry holds only currently in-flight requests.
	s.cancelTracker.track(req.ID, cancel)
	defer s.cancelTracker.untrack(req.ID)
	sink := &requestSink{ser: ser, seq: seq}
	ctx = withRequestSink(ctx, sink)
	if s.testCtxHook != nil {
		s.testCtxHook(ctx, req.ID)
	}

	// The optional progress token is accepted HERE rather than in each
	// handler, and that is a property of the routing rather than a
	// convenience: the routing index pairs a wire method with a handler
	// and never with a params shape, so one field carrying one meaning on
	// every method has exactly one place it can be read without being
	// read fifty times. A request that carries none — the ordinary case —
	// installs nothing, and every handler then reports into a discard.
	//
	// Only an id-bearing request is read for a token. A notification has
	// no response channel to refuse on, which is the same reason the
	// cancellation notification drops malformed params silently rather
	// than reporting them.
	if !isNotification(req) {
		token, tokenErr := workDoneTokenFrom(req.Params)
		if tokenErr == nil && len(token) > 0 && !s.workDoneProgress() {
			tokenErr = errProgressNotNegotiated
		}
		if tokenErr != nil {
			if err := s.writeError(ctx, req.ID, jsonRPCInvalidParams, tokenErr.Error()); err != nil {
				fmt.Fprintf(s.stderr, "  handle %s: %v\n", req.Method, err)
				return nil
			}
			return sink.response
		}
		if len(token) > 0 {
			ctx = withProgressToken(ctx, token)
		}
	}

	if err := s.dispatch(ctx, req); err != nil {
		// The only errors reaching here are genuine marshal failures of
		// an otherwise-valid response — they cannot be reported to the
		// client (the response is what failed to marshal), so log to
		// stderr and close the slot with no body.
		fmt.Fprintf(s.stderr, "  handle %s: %v\n", req.Method, err)
		return nil
	}
	return sink.response
}

// methodHandler answers one request method.
type methodHandler func(*Server, context.Context, *jsonRPCRequest) error

// dispatch routes a parsed request to the appropriate per-method handler,
// through the routing index the availability table produces.
//
// The aggrega/shutdown message never reaches dispatch: the main loop
// intercepts it at the acceptance boundary to drive the drain-then-abort
// sequence. Post-shutdown rejection happens there too — the main loop
// sets s.shuttingDown when it accepts the first shutdown and drops every
// message it accepts afterward — so dispatch only ever sees
// pre-shutdown-accepted messages, which process normally.
func (s *Server) dispatch(ctx context.Context, req *jsonRPCRequest) error {
	if handler, ok := methodIndex[req.Method]; ok {
		return handler(s, ctx, req)
	}
	return s.writeError(ctx, req.ID, jsonRPCMethodNotFound, "method not found: "+req.Method)
}

// isNotification reports whether req is a JSON-RPC 2.0 notification
// (id field absent). The wire-level encoding of "id omitted" varies:
// a missing id field produces a nil RawMessage; an explicit
// "id":null produces a four-byte "null" RawMessage. Both shapes
// count as notifications for response-suppression purposes.
func isNotification(req *jsonRPCRequest) bool {
	if len(req.ID) == 0 {
		return true
	}
	return string(req.ID) == "null"
}

// emitResponse records a fully-marshalled response body on the
// request's sink. It is the single route from every response write
// surface to stdout: the dispatch path flushes the recorded body
// through the serializer (the sole owner of the underlying writer) in
// arrival-sequence order once the handler returns, so workers never
// race the writer and responses emit in request order regardless of
// which worker finishes first.
func (s *Server) emitResponse(ctx context.Context, body []byte) error {
	sinkFromContext(ctx).response = body
	return nil
}

// writeResult marshals result as the response Result field and routes
// the message through the request's serializer slot.
func (s *Server) writeResult(ctx context.Context, id json.RawMessage, result any) error {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return s.writeError(ctx, id, jsonRPCInternalError, "marshal result: "+err.Error())
	}
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resultBytes,
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	return s.emitResponse(ctx, respBytes)
}

// marshalErrorResponse encodes a code/message error envelope (no Data
// field) into a wire body. Shared by writeError and the dispatch loop's
// parse-error path so the error-envelope encoding has a single home.
func marshalErrorResponse(id json.RawMessage, code int, message string) ([]byte, error) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal error response: %w", err)
	}
	return respBytes, nil
}

// writeError writes an error-envelope response with the supplied code
// and message. Used by direct-emit callers (parse error, method not
// found, invalid params) that do not carry structured source-location
// data. The resulting envelope has no Data field.
//
// Callers that already hold a typed *jsonRPCError envelope from a
// handler return — including envelopes carrying a populated Data
// field — use writeErrorEnvelope instead so the Data reaches the
// wire.
func (s *Server) writeError(ctx context.Context, id json.RawMessage, code int, message string) error {
	respBytes, err := marshalErrorResponse(id, code, message)
	if err != nil {
		return err
	}
	return s.emitResponse(ctx, respBytes)
}

// writeErrorEnvelope writes a pre-constructed error envelope to the
// wire verbatim, preserving its Data field (when set). Used by
// dispatcher wrappers that receive a *jsonRPCError from their
// handler and want the envelope's optional Data payload to reach the
// client. Direct-emit callers without source-location info continue
// using writeError instead.
//
// Behavior on a Data-free envelope is identical to writeError —
// omitempty drops the Data field from the JSON encoding, so the wire
// is byte-identical to today for envelopes that did not populate
// Data.
func (s *Server) writeErrorEnvelope(ctx context.Context, id json.RawMessage, env *jsonRPCError) error {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   env,
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal error response: %w", err)
	}
	return s.emitResponse(ctx, respBytes)
}

// jsonRPCNotification is the wire shape for an outgoing JSON-RPC
// 2.0 notification — a server-pushed message with no id field and
// no expected response. The omitempty tag on every optional field
// keeps the wire compact.
type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// writeNotification emits a JSON-RPC 2.0 notification with the
// supplied method and params. Used by server-push surfaces like
// aggrega/publishDiagnostics where the daemon initiates a message
// outside the request/response cycle.
//
// Notifications carry no id and have no id-correlation contract, so the
// serializer emits them immediately on receipt, interleaved between
// whatever ordered response frames surround them, rather than holding
// them for arrival-sequence ordering. Routing still goes through the
// serializer so it remains the sole owner of stdout and no worker races
// the writer.
func (s *Server) writeNotification(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	sinkFromContext(ctx).ser.emitNow(body)
	return nil
}

// publishDiagnosticsParams is the params shape for the
// aggrega/publishDiagnostics server-push notification. The
// diagnostics slice carries the daemon's per-file Diagnostic
// values verbatim; an empty slice means "clear this file's
// diagnostic-collection entry" — the standard JSON-RPC notification
// idiom for state-change-driven publishing.
type publishDiagnosticsParams struct {
	File        string       `json:"file"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// publishDiagnostics fires the aggrega/publishDiagnostics
// notification carrying the current s.diagnostics[file] state.
// Missing entries publish an empty diagnostics array (the standard
// "clear" signal). Callers invoke this after every state mutation
// to s.diagnostics so the extension's DiagnosticCollection stays
// in sync with the daemon's cache.
func (s *Server) publishDiagnostics(ctx context.Context, file string) error {
	diags := s.snapshotDiagnostics(file)
	if diags == nil {
		diags = []Diagnostic{}
	}
	return s.writeNotification(ctx, "aggrega/publishDiagnostics", publishDiagnosticsParams{
		File:        file,
		Diagnostics: diags,
	})
}
