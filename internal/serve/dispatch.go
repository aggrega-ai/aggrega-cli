// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
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
type jsonRPCError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *errorData `json:"data,omitempty"`
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
// select-driven dispatcher in the main goroutine. The reader peeks
// at each message's method field: when it sees aggrega/shutdown it
// creates a time.Timer for the drain budget and emits the timer's
// channel onto drainTimerInstall (cap 1) so the main loop can begin
// watching it. All messages — including the shutdown notification —
// are also forwarded onto the message channel so dispatchShutdown
// runs in the main goroutine and the no-mutex invariant holds.
//
// The drain timer is arrival-based: it starts when the reader parses
// the shutdown notification, not when the main loop observes the
// notification (which could be delayed by an in-flight handler).
// Worst-case exit latency from the client's POV is
// max(in_flight_handler_runtime, drainTimeout).
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
	messages := make(chan incomingMessage)
	drainTimerInstall := make(chan (<-chan time.Time), 1)

	go func() {
		defer close(messages)
		// timerArmed is local to the reader goroutine and never read
		// by any other goroutine; it gates the timer creation so
		// only the FIRST aggrega/shutdown notification arms a drain
		// timer. Subsequent shutdown notifications during the reader's
		// lifetime are no-ops — no timer allocated, no resource leak,
		// no race (the flag is reader-local). This protects against
		// spaced-duplicate shutdowns where the main loop has already
		// installed the first timer and would otherwise overwrite the
		// drainCh with a second one, leaking the first timer's
		// resources until it fires.
		timerArmed := false
		for {
			body, err := readMessage(s.in)
			if err != nil {
				messages <- incomingMessage{err: err}
				return
			}
			// Peek the method before forwarding so the reader can
			// arm the drain timer without waiting for the main
			// goroutine to observe.
			var peek struct {
				Method string `json:"method"`
			}
			if jerr := json.Unmarshal(body, &peek); jerr == nil && peek.Method == "aggrega/shutdown" && !timerArmed {
				timerArmed = true
				timer := time.NewTimer(drainTimeout)
				drainTimerInstall <- timer.C
			}
			messages <- incomingMessage{body: body}
		}
	}()

	var drainCh <-chan time.Time
	for {
		select {
		case d := <-drainTimerInstall:
			drainCh = d
		case msg, ok := <-messages:
			if !ok {
				return nil
			}
			if msg.err != nil {
				if errors.Is(msg.err, io.EOF) {
					return nil
				}
				return fmt.Errorf("read message: %w", msg.err)
			}
			var req jsonRPCRequest
			if jerr := json.Unmarshal(msg.body, &req); jerr != nil {
				if werr := s.writeError(nil, jsonRPCParseError, "parse error: "+jerr.Error()); werr != nil {
					return werr
				}
				continue
			}
			if err := s.handle(&req); err != nil {
				return err
			}
		case <-drainCh:
			return nil
		}
	}
}

// handle dispatches a parsed request to the appropriate per-method
// handler. The method routing table is the load-bearing surface for
// the daemon's wire protocol: adding a new method means adding a
// case here.
//
// Drain-state pre-check: when s.shuttingDown is set (an
// aggrega/shutdown notification has already arrived), requests
// receive an errShuttingDown envelope and notifications are
// dropped. The pre-check fires before per-method dispatch so it
// applies uniformly. aggrega/shutdown itself is exempt — receiving
// a second shutdown notification during drain is a no-op (the flag
// is already set).
func (s *Server) handle(req *jsonRPCRequest) error {
	if s.testInDispatchHook != nil {
		s.testInDispatchHook()
	}
	if req.Method == "aggrega/shutdown" {
		return s.dispatchShutdown(req)
	}
	if s.shuttingDown {
		if isNotification(req) {
			return nil
		}
		return s.writeError(req.ID, errShuttingDown, "daemon shutting down")
	}
	switch req.Method {
	case "aggrega/initialize":
		return s.dispatchInitialize(req)
	case "aggrega/validate":
		return s.dispatchValidate(req)
	case "aggrega/invalidate":
		return s.dispatchInvalidate(req)
	case "aggrega/diagnostics":
		return s.dispatchDiagnostics(req)
	case "aggrega/irGraph":
		return s.dispatchIrGraph(req)
	case "aggrega/resolve":
		return s.dispatchResolve(req)
	case "aggrega/findReferences":
		return s.dispatchFindReferences(req)
	case "aggrega/findImplementations":
		return s.dispatchFindImplementations(req)
	case "aggrega/hover":
		return s.dispatchHover(req)
	case "aggrega/completion":
		return s.dispatchCompletion(req)
	default:
		return s.writeError(req.ID, jsonRPCMethodNotFound, "method not found: "+req.Method)
	}
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

// writeResult marshals result as the response Result field and writes
// the message to s.out.
func (s *Server) writeResult(id json.RawMessage, result any) error {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return s.writeError(id, jsonRPCInternalError, "marshal result: "+err.Error())
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
	return writeMessage(s.out, respBytes)
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
func (s *Server) writeError(id json.RawMessage, code int, message string) error {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal error response: %w", err)
	}
	return writeMessage(s.out, respBytes)
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
func (s *Server) writeErrorEnvelope(id json.RawMessage, env *jsonRPCError) error {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   env,
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal error response: %w", err)
	}
	return writeMessage(s.out, respBytes)
}
