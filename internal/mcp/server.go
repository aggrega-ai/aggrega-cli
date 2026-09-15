// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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

	// Toolchain is the versions a dependency resolution records into the
	// lockfile it writes: the compiler, and each core plugin this binary
	// carries. The command layer supplies it for the same reason it supplies
	// the command surface — it is the only layer that can see both the
	// binary's own identity and this server, and the halves of the stamp are
	// derived where the version command derives them, so a lockfile written
	// through this server records what a lockfile written at a terminal
	// records for the same binary and the same input.
	//
	// A nil stamp records none. That is the honest value for a caller with no
	// binary identity to report, and it is not the same statement as a stamp
	// carrying an empty core-plugin list, which says the binary compiles its
	// guests out.
	Toolchain *lockfile.ToolchainStamp

	// Commands is the command surface the long-tail command tools are derived
	// from, in catalog order. The command layer supplies it because it is the
	// only layer that can see both the command tree and this server; this
	// package holds no list of commands of its own, so a command reaches the
	// tool surface by existing in the tree rather than by being restated here.
	// An empty value yields no command tools, which is the shape every test
	// that is not about them runs in.
	Commands []CommandSurface
}

// server holds the per-session transport state: the sole stdout writer
// every response is framed through, the stderr sink every log line goes
// to, and the reported build identity. It is constructed once in Run and
// drives the message loop to stdin EOF.
//
// The tool-execution seams carry no session state — each tool call
// resolves its own workspace and builds its own request-scoped inputs — so
// they hold only the substitution points a test injects: where workspace
// resolution starts and how the catalog-generation helper runs. A nil seam
// selects the production default.
type server struct {
	out           io.Writer
	logw          io.Writer
	serverVersion string

	// toolchain is the stamp a dependency resolution driven through this
	// server records into the lockfile it writes. Nil records none; a test
	// that does not assert on the stamp leaves it unset.
	toolchain *lockfile.ToolchainStamp

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

	// docsTarget resolves the OCI target the documentation bundle is pulled
	// from at the start of the run. Nil reaches the registry through the
	// workspace's egress gate. A test injects one to drive the acquisition
	// without a registry; everything after the target — the pull, the
	// media-type check, the cache write and the read — stays production.
	docsTarget docsTargetFor

	// docsBundleOnce holds that acquisition to a single attempt per serving
	// process. A cold cache and an unreachable registry are the same case from
	// here, so a second attempt would mean paying the connect timeout again with
	// nothing having changed.
	docsBundleOnce sync.Once

	// runGenCatalog runs the catalog-generation helper for the gen-catalog
	// tool. Nil calls the in-process catalog-generation core. A test injects a
	// fake that records the options and returns canned output.
	runGenCatalog genCatalogRunner

	// runLint runs the lint helper for the deferred lint tool. Nil runs the
	// shared lint core in-process on the server's resolved workspace. A test
	// injects a fake that records the request and returns canned output.
	runLint lintRunner

	// runCommand runs an aggrega command by argument vector, for the cli://
	// resource and for the generated command tools. Nil self-execs the running
	// binary, so a command's behavior and its help text have a single home in
	// the command layer. A test injects a fake that records the arguments and
	// returns canned output.
	runCommand commandRunner

	// commandTools is the long-tail tool set derived from the command surface
	// the caller supplied, built once per session in Run and never mutated, so
	// every listing over it is byte-stable for the life of the session.
	commandTools []toolSpec
}

// Run reads newline-delimited JSON-RPC 2.0 messages from cfg.Stdin,
// answers each on cfg.Stdout, and returns when stdin reaches EOF or the run's
// context ends. It is the entry point the `aggrega mcp` command calls.
//
// Stream discipline is strict: stdout carries only framed protocol
// messages — one compact JSON object per line — while every log and
// diagnostic is written to stderr, so a connected client can parse the
// stdout stream without heuristics. Run returns nil on a clean stdin EOF;
// it returns a non-nil error only when a stdout write fails or the input
// stream faults, since a broken transport leaves no channel to report the
// failure on.
//
// # The caller's context
//
// ctx is the run's own, and it reaches the start-of-run documentation
// acquisition, every tool invocation and every resource read: the tools that
// publish, pull, build, lint and search all carry it into the work they drive,
// and the CLI-help resource carries it into the subprocess it starts. So ending
// the run ends whatever a call was doing, rather than only declining to accept
// the next message.
//
// Ending the run ends the message loop too, whether it is answering a message or
// waiting for one, so a run ends on the first request to end it rather than
// waiting for the client to close the connection. One bound remains, and it is
// structural rather than an oversight: a tool whose work ignores the context runs
// to its own completion — the context reaches it, but what it does with it is that
// path's own contract.
func Run(ctx context.Context, cfg Config) error {
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
		toolchain:     cfg.Toolchain,
		commandTools:  deriveCommandTools(cfg.Commands),
	}
	return s.run(ctx, stdin)
}

// run is the serving process's own lifecycle: acquire the documentation bundle
// once, then answer messages until stdin reaches EOF or the run ends.
//
// The acquisition belongs to the process rather than to any one message, so it
// runs here and not from a handler. A client that never sends a particular
// message still finds the cache warm, and nothing about which message arrives
// first decides whether the documentation resources can answer. It blocks the
// first answer deliberately: a bundle acquired after a page has already been
// asked for arrived too late for that page, and the read path stays incapable of
// acquiring one for itself. It cannot fail the run — ensureDocsBundle reports
// its own outcome to the log stream and returns either way.
//
// Run builds the server from its Config and enters here; a test builds one with
// its own workspace and bundle seams and enters here too, so what a test drives
// is what a client drives.
func (s *server) run(ctx context.Context, stdin io.Reader) error {
	s.ensureDocsBundle(ctx)
	return s.serve(ctx, stdin)
}

// readResult is one turn of the reader goroutine: the bytes ReadBytes returned
// and the error that came with them. The two travel together because a final
// message can arrive alongside io.EOF in a single call, and that message is still
// answered before the loop returns.
type readResult struct {
	line []byte
	err  error
}

// serve runs the message loop: read one newline-delimited message, handle
// it, repeat until stdin reaches EOF. A blank line is skipped. The final
// message may arrive without a trailing newline (ReadBytes returns the
// bytes alongside io.EOF); it is handled before the loop returns, so no
// message is dropped at end-of-stream.
//
// A run whose context has ended stops accepting messages: the loop reads the run
// at the top of each turn and again on a delivered line, so a message already
// being answered finishes and none is accepted after that. The withdrawal also
// ends a turn that is WAITING for a message, which is what lets a stop request
// reach a server whose client is connected and idle: the read runs on its own
// goroutine and the loop selects between a delivered line and the run's end. A
// read blocked in the operating system is unblocked by nothing a context can do,
// so leaving the read on the loop's own goroutine would make an idle client's
// silence the thing that decides when a stop takes effect.
//
// The reader goroutine outlives the loop when the loop leaves on a withdrawal: it
// is parked in a read nothing will complete, and it ends when that read returns or
// when the process does. Joining it would put the blocked read straight back into
// the exit path the select exists to keep it out of, so it is deliberately left
// running rather than waited for. Every other exit path releases it, through
// loopDone below.
func (s *server) serve(ctx context.Context, stdin io.Reader) error {
	lines := make(chan readResult)
	// loopDone releases the reader from a delivery the loop is no longer there to
	// take. The channel is unbuffered, so a reader holding a line for a loop that
	// has already returned would otherwise wait for a receive that never comes.
	loopDone := make(chan struct{})
	defer close(loopDone)
	go func() {
		r := bufio.NewReader(stdin)
		for {
			line, err := r.ReadBytes('\n')
			select {
			case lines <- readResult{line: line, err: err}:
			case <-loopDone:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		var res readResult
		select {
		case <-ctx.Done():
			return nil
		case res = <-lines:
			// A delivered line and the run's end can become ready in the same
			// turn, and a select picks among ready cases at random. Reading the
			// run again here is what keeps the contract single-valued rather than
			// decided by that draw: once the run has ended, nothing more is
			// answered.
			if ctx.Err() != nil {
				return nil
			}
		}
		if trimmed := bytes.TrimSpace(res.line); len(trimmed) > 0 {
			if writeErr := s.handleMessage(ctx, trimmed); writeErr != nil {
				return writeErr
			}
		}
		if res.err != nil {
			if errors.Is(res.err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read message: %w", res.err)
		}
	}
}

// handleMessage parses one message body and routes it. A parse failure
// returns the JSON-RPC parse-error envelope with a null id. A
// notification (absent id) is handled for its side effect and yields no
// response. A request is dispatched and its result or error envelope is
// framed to stdout. The returned error is non-nil only when writing the
// response to stdout fails — a fatal transport condition.
func (s *server) handleMessage(ctx context.Context, body []byte) error {
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return s.writeError(nil, codeParseError, "parse error: "+err.Error())
	}
	if len(req.ID) == 0 {
		s.handleNotification(&req)
		return nil
	}
	result, rpcErr := s.dispatch(ctx, &req)
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
