// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Config carries the I/O streams the server reads from and writes to plus
// its reported build identity. The cobra-bound entry point passes
// cmd.InOrStdin / OutOrStdout / ErrOrStderr and the compiler version;
// tests inject bytes.Buffer (or io.Pipe) values to drive the message loop
// in-process without spawning a subprocess. A nil Stdout or Stderr falls
// back to the process stream.
type Config struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// ServerVersion is the binary's build identity, reported to the
	// client in the initialize handshake's serverInfo block so the
	// connected assistant can record which server version it spoke to. An
	// empty value reports an empty version.
	ServerVersion string
}

// server holds the per-session transport state: the sole stdout writer
// every response is framed through, the stderr sink every log line goes
// to, and the reported build identity. It is constructed once in Run and
// drives the message loop to stdin EOF.
//
// The tool-execution seams carry no session state — each tool call
// resolves its own workspace and builds its own request-scoped inputs — so
// they hold only the substitution points a test injects: where workspace
// resolution starts, how a search-plugin URI resolves to bytes, and how the
// catalog-generation helper runs. A nil seam selects the production default.
type server struct {
	out           io.Writer
	logw          io.Writer
	serverVersion string

	// startDir is where workspace resolution begins for a tool call that
	// supplies no explicit workspace_root. Empty resolves from the process
	// working directory. A test points it at a fixture workspace.
	startDir string

	// docsBundleRef and docsBundleVersion pin the documentation bundle the
	// resource surface reads pages from. Empty values derive the reference from
	// the workspace registry under the reserved docs namespace and the version
	// from the server's build identity. A test sets them to a fixture bundle.
	docsBundleRef     string
	docsBundleVersion string

	// searchResolve turns a search-plugin URI into its resolved bytes for the
	// search tool. Nil builds a workspace plugin-path resolver cascade per
	// call, which resolves offline (no registry puller). A test injects a
	// fixture seam that hands the guest bytes directly.
	searchResolve searchResolver

	// runGenCatalog runs the catalog-generation helper for the gen-catalog
	// tool. Nil self-execs the running binary's catalog-generation subcommand.
	// A test injects a fake that records the arguments and returns canned
	// output.
	runGenCatalog genCatalogRunner

	// runLint runs the lint helper for the deferred lint tool. Nil self-execs
	// the running binary's lint subcommand. A test injects a fake that records
	// the arguments and returns canned output.
	runLint lintRunner

	// runHelp runs a command's help output for the cli:// resource. Nil
	// self-execs the running binary with the requested command path and
	// --help, so the help text has a single home in the command layer. A test
	// injects a fake that records the arguments and returns canned output.
	runHelp helpRunner
}

// Run reads newline-delimited JSON-RPC 2.0 messages from cfg.Stdin,
// answers each on cfg.Stdout, and returns when stdin reaches EOF. It is
// the entry point the `aggrega mcp` command calls.
//
// Stream discipline is strict: stdout carries only framed protocol
// messages — one compact JSON object per line — while every log and
// diagnostic is written to stderr, so a connected client can parse the
// stdout stream without heuristics. Run returns nil on a clean stdin EOF;
// it returns a non-nil error only when a stdout write fails or the input
// stream faults, since a broken transport leaves no channel to report the
// failure on.
func Run(cfg Config) error {
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	logw := cfg.Stderr
	if logw == nil {
		logw = os.Stderr
	}
	stdin := cfg.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	s := &server{
		out:           stdout,
		logw:          logw,
		serverVersion: cfg.ServerVersion,
	}
	return s.serve(stdin)
}

// serve runs the message loop: read one newline-delimited message, handle
// it, repeat until stdin reaches EOF. A blank line is skipped. The final
// message may arrive without a trailing newline (ReadBytes returns the
// bytes alongside io.EOF); it is handled before the loop returns, so no
// message is dropped at end-of-stream.
func (s *server) serve(stdin io.Reader) error {
	r := bufio.NewReader(stdin)
	for {
		line, err := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if writeErr := s.handleMessage(trimmed); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read message: %w", err)
		}
	}
}

// handleMessage parses one message body and routes it. A parse failure
// returns the JSON-RPC parse-error envelope with a null id. A
// notification (absent id) is handled for its side effect and yields no
// response. A request is dispatched and its result or error envelope is
// framed to stdout. The returned error is non-nil only when writing the
// response to stdout fails — a fatal transport condition.
func (s *server) handleMessage(body []byte) error {
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return s.writeError(nil, codeParseError, "parse error: "+err.Error())
	}
	if len(req.ID) == 0 {
		s.handleNotification(&req)
		return nil
	}
	result, rpcErr := s.dispatch(&req)
	if rpcErr != nil {
		return s.writeErrorEnvelope(req.ID, rpcErr)
	}
	return s.writeResult(req.ID, result)
}

// writeResult frames a successful response carrying result to stdout.
func (s *server) writeResult(id json.RawMessage, result any) error {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return s.writeError(id, codeInternalError, "marshal result: "+err.Error())
	}
	return s.write(&rpcResponse{JSONRPC: "2.0", ID: id, Result: resultBytes})
}

// writeError frames an error response with the supplied code and message
// to stdout. A nil id yields a null-id envelope, used for a parse error
// where the request id could not be recovered.
func (s *server) writeError(id json.RawMessage, code int, message string) error {
	return s.writeErrorEnvelope(id, &rpcError{Code: code, Message: message})
}

// writeErrorEnvelope frames a pre-constructed error envelope to stdout,
// preserving its Data field when set.
func (s *server) writeErrorEnvelope(id json.RawMessage, env *rpcError) error {
	return s.write(&rpcResponse{JSONRPC: "2.0", ID: id, Error: env})
}

// write marshals resp and emits it as one newline-terminated line on
// stdout. It is the single route from every response surface to the
// output stream, so stdout carries nothing but framed protocol messages.
func (s *server) write(resp *rpcResponse) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

// logf writes a diagnostic line to stderr. Every log surface routes here
// so no diagnostic ever reaches stdout, keeping the protocol stream pure.
func (s *server) logf(format string, args ...any) {
	fmt.Fprintf(s.logw, format+"\n", args...)
}
