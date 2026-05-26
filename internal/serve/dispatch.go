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
)

// JSON-RPC 2.0 standard error codes.
const (
	jsonRPCParseError     = -32700
	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	jsonRPCInternalError  = -32603
)

// Application-level error codes in the 1xxx range.
const (
	errFileNotValidated    = 1001
	errReleaseNotResolved  = 1002
	errLockfileAbsent      = 1003
	errEntityNotResolvable = 1004
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
type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
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

// dispatchLoop reads requests, dispatches to handlers by method name,
// and writes responses until stdin reaches EOF or an unrecoverable
// I/O error occurs.
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
func (s *Server) dispatchLoop() error {
	for {
		body, err := readMessage(s.in)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}
		var req jsonRPCRequest
		if jerr := json.Unmarshal(body, &req); jerr != nil {
			if werr := s.writeError(nil, jsonRPCParseError, "parse error: "+jerr.Error()); werr != nil {
				return werr
			}
			continue
		}
		if err := s.handle(&req); err != nil {
			return err
		}
	}
}

// handle dispatches a parsed request to the appropriate per-method
// handler. The method routing table is the load-bearing surface for
// the daemon's wire protocol: adding a new method means adding a
// case here.
func (s *Server) handle(req *jsonRPCRequest) error {
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
// and message.
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
