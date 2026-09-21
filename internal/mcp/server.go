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

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
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
	//
	// A session reached over HTTP derives none of them, whatever this carries:
	// a generated tool runs its command in a separate process, where a location
	// the call named is reached on the host as that command reads it, and a
	// caller reaching a listener is not the one whose host that is. Supplying
	// the same surface to both transports is therefore correct rather than
	// careless — where it is honoured is settled where the session is composed.
	Commands []CommandSurface

	// ResolveImportPlugin turns an importer or parser plugin reference into the
	// module bytes that plugin is run from. The command layer supplies it for
	// the reason it supplies the two fields around it: the resolution cascade it
	// binds is assembled there and shared with the install path, the plugin
	// listing and the runtime plugin runner, so a reference one path accepts
	// every other path resolves identically. Building a second cascade inside
	// this package would be a copy that agreed with that one on the day it was
	// written, and the agreement is what would hide the first divergence.
	//
	// A nil resolver reaches no importer at all: the tool that converts a
	// foreign description refuses, saying so, rather than reporting a conversion
	// that did not happen. That is the shape every test that is not about it
	// runs in.
	ResolveImportPlugin importops.PluginBytesResolver

	// RenderHelp renders a command's help text for the cli:// resource family,
	// from the command tree this process already holds. The command layer
	// supplies it for the same reason it supplies the command surface above:
	// that layer is the only one that can see both the command tree and this
	// server, so the help keeps its single home there.
	//
	// A nil renderer serves no cli:// resource: every read in the family is
	// answered as a resource that is not there, on every transport. That is the
	// shape every test that is not about the family runs in.
	RenderHelp HelpRenderer

	// WorkspaceRoot is the workspace this process serves, fixed when it was
	// launched: the --workspace-root flag when the operator set it, otherwise
	// AGGREGA_WORKSPACE_ROOT. Empty resolves from the process working directory,
	// which is what a client launching this server inside a project gets.
	//
	// It is where resolution begins for a call that names no workspace of its
	// own; a call that does name one is answered in the workspace it named. It
	// binds on every transport, because which workspace a process serves is a
	// property of the process rather than of how a client reached it.
	WorkspaceRoot string

	// AnnounceTools names the tools the server announces, spelled as the tool
	// list reports them. Nil announces every tool the session serves, a list
	// announces exactly those, and an empty non-nil list announces none — so
	// the two empty spellings are different statements and are carried as
	// such.
	//
	// It is honoured on both transports, unlike the listener settings, because
	// which tools a client is offered is a property of the server rather than
	// of how a client reached it. A name no tool answers to is refused where
	// the server is built rather than dropped, so a caller that asked for a
	// particular surface is never quietly served a different one.
	AnnounceTools []string
}

// transportMode is the transport a session speaks.
//
// It is read wherever what a call may name depends on who is calling. A client
// that launched this process reaches it over the process's own streams and is
// at the machine's keyboard, so it names the workspace it means on each call. A
// caller reaching a listener is somewhere else entirely, and is served the one
// workspace the process was started over.
type transportMode int

const (
	// transportStdio frames newline-delimited messages on the process's own
	// standard input and output. It is the zero value, so a server built without
	// a transport behaves as a client-launched one does.
	transportStdio transportMode = iota
	// transportHTTP answers one message per request on a listener.
	transportHTTP
)

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

	// transport is which transport composed this session, and it decides what a
	// call may name rather than how a message is framed. Framing is settled by
	// the frontend that reads the message; this is read where the answer depends
	// on whether the caller is at this machine's keyboard.
	transport transportMode

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

	// resolveImportPlugin turns an importer or parser plugin reference into the
	// module bytes it is run from, handed in from the command layer because the
	// resolution cascade it binds is assembled there and shared with every other
	// path that loads a plugin. Nil reaches no importer, and the import tool
	// refuses saying so rather than reporting a conversion that did not happen —
	// there is no fallback, because a cascade built here would be a second one.
	resolveImportPlugin importops.PluginBytesResolver

	// runLint runs the lint helper for the deferred lint tool. Nil runs the
	// shared lint core in-process on the server's resolved workspace. A test
	// injects a fake that records the request and returns canned output.
	runLint lintRunner

	// runCommand runs an aggrega command by argument vector, for the generated
	// command tools. Nil self-execs the running binary, so a command's behavior
	// has a single home in the command layer. A test injects a fake that records
	// the arguments and returns canned output.
	runCommand commandRunner

	// resolveDependencies performs the dependency resolution the resolution tool
	// and a build asked to fill its lockfile both run. Nil runs the shared
	// operation. A test injects one that reports stages of its own choosing and
	// stalls where it likes, which is the only way to drive the reporting a long
	// call does without a workspace whose real resolution takes seconds.
	resolveDependencies dependencyResolver

	// renderHelp renders a command's help text for the cli:// resource family,
	// read from the command tree the command layer handed this server. Nil
	// answers every read in that family as a resource that is not there: there is
	// no fallback, because nothing else here can see a command tree.
	renderHelp HelpRenderer

	// commandTools is the long-tail tool set derived from the command surface
	// the caller supplied, built once per session in Run and never mutated, so
	// every listing over it is byte-stable for the life of the session.
	commandTools []toolSpec

	// deniedReach is the kinds of reach this deployment declared it does not
	// carry. A tool whose operation reaches one of them is absent from every
	// listing and refused at every call, whatever a caller's credential says,
	// because the bound is the deployment's rather than the caller's.
	//
	// It is installed where an HTTP handler is built and nowhere else, so a
	// session speaking the process's own streams carries none: the setting
	// configures a listener, and a caller at this machine's keyboard is
	// bounded by owning the machine. Nil is the ordinary case and means
	// nothing is withdrawn.
	deniedReach map[opsurface.Reach]bool

	// announcedTools names the tools this server announces, read from the
	// configuration when the process starts. Nil is the ordinary case and
	// announces every tool this session serves; a set announces exactly those
	// names, and an empty set announces none.
	//
	// It is installed where a server is built, on both transports, because
	// which tools a client is offered is a property of the server rather than
	// of how a client reached it — and the client a narrowing exists for is
	// usually the one that launched this process. Nothing changes it after a
	// client has connected, which is what keeps the declaration that the tool
	// list does not change true.
	announcedTools map[string]bool

	// cancels maps an in-flight request's id to the cancel handle of that
	// request's own context, so the client can end one call and leave every other
	// one running. It is the registry both request-serving frontends share rather
	// than a second one written here.
	//
	// serve is its single initialisation point: it runs before the loop accepts
	// anything, so every goroutine that reads this field reads a write that already
	// happened. A test may install its own beforehand to read the in-flight count.
	cancels *requestcancel.Registry

	// writeMu serialises the marshal-and-write of every response and guards the
	// two fields below. Requests are answered on their own goroutines, so without
	// it two responses emitted at once would interleave their bytes on the output
	// stream and neither line would parse.
	//
	// writeErr holds the first failure any response write met, and writeFailed is
	// closed when that first failure is recorded. Together they are how a failure
	// met off the loop goroutine still reaches Run's caller and still stops the
	// loop reading on into a stream nothing can be written to.
	writeMu     sync.Mutex
	writeErr    error
	writeFailed chan struct{}

	// logMu serialises every write this server makes to the log stream. Requests
	// are answered on their own goroutines and each may write a line, so without
	// it two lines emitted at once would interleave their bytes — which costs a
	// prose reader a garbled line and costs a machine-readable one the whole
	// line, since neither half of a split object parses.
	logMu sync.Mutex
}

// errCancelledByClient is the cause recorded on a request's context when the
// client cancels that request by id.
//
// It is what separates a cancellation from the run ending. Both end the same
// per-request context, and only the first means the client has said it will not
// use the result — which is the case the protocol asks a server to send nothing
// further for. A run that ends still answers the request it was answering.
var errCancelledByClient = errors.New("request cancelled by the client")

// Run reads newline-delimited JSON-RPC 2.0 messages from cfg.Stdin,
// answers each on cfg.Stdout, and returns when stdin reaches EOF or the run's
// context ends. It is the entry point the `aggrega mcp` command calls.
//
// Stream discipline is strict: stdout carries only framed protocol
// messages — one compact JSON object per line — while every log and
// diagnostic is written to stderr, so a connected client can parse the
// stdout stream without heuristics. Run returns nil on a clean stdin EOF.
// Once it is serving, it returns a non-nil error only when a stdout write
// fails or the input stream faults, since a broken transport leaves no
// channel to report the failure on. Before it serves anything it returns the
// refusal a configuration naming a tool this server does not have earns,
// which reaches the person who started the process rather than a client that
// is not connected yet.
//
// # The caller's context
//
// ctx is the run's own, and it reaches the start-of-run documentation
// acquisition and every tool invocation: the tools that publish, pull, build,
// lint and search all carry it into the work they drive. So ending the run ends
// whatever a call was doing, rather than only declining to accept the next
// message.
//
// Ending the run ends the message loop too, whether it is answering a message or
// waiting for one, so a run ends on the first request to end it rather than
// waiting for the client to close the connection. One bound remains, and it is
// structural rather than an oversight: a tool whose work ignores the context runs
// to its own completion — the context reaches it, but what it does with it is that
// path's own contract.
//
// # One call at a time, ended by its own id
//
// Each request is answered on its own goroutine under a context derived from
// ctx, so a client gets an answer to one call while another is still working,
// and a notification — a cancellation above all — is read while a call runs
// rather than queueing behind it. A cancellation names the request it ends; that
// request's context ends and nothing further is written for it, while every
// other call carries on. Cancelling an id nothing is working on does nothing.
// The same bound applies as above: a tool that ignores its context runs to its
// own completion.
//
// Run returns only once every request it accepted has finished.
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
	commandTools := deriveCommandTools(cfg.Commands)
	announced, err := announcedToolNames(cfg.AnnounceTools, commandTools)
	if err != nil {
		return err
	}
	s := &server{
		out:            stdout,
		logw:           logw,
		serverVersion:  cfg.ServerVersion,
		toolchain:      cfg.Toolchain,
		commandTools:   commandTools,
		announcedTools: announced,
		renderHelp:     cfg.RenderHelp,
		startDir:       cfg.WorkspaceRoot,
		transport:      transportStdio,

		resolveImportPlugin: cfg.ResolveImportPlugin,
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

// serve runs the message loop and reports what ended it. It is the loop's outer
// half: it installs the per-run state the loop and the requests it dispatches
// share, runs the loop, and reports a write failure in preference to whatever
// the loop itself returned.
//
// The preference is the point. A response is now written on the goroutine of the
// request it answers, where a returned error reaches nobody, so the loop can
// reach a clean end-of-stream while a write has already failed. Reading the
// recorded failure here — after messageLoop has returned, which is after every
// in-flight request has finished — is what keeps Run's contract that a broken
// transport is reported to its caller rather than swallowed.
func (s *server) serve(ctx context.Context, stdin io.Reader) error {
	if s.cancels == nil {
		s.cancels = requestcancel.New()
	}
	s.writeMu.Lock()
	if s.writeFailed == nil {
		s.writeFailed = make(chan struct{})
	}
	writeFailed := s.writeFailed
	s.writeMu.Unlock()

	err := s.messageLoop(ctx, stdin, writeFailed)
	if writeErr := s.writeFailure(); writeErr != nil {
		return writeErr
	}
	return err
}

// messageLoop reads one newline-delimited message, puts it on its path, and
// repeats until stdin reaches EOF. A blank line is skipped. The final
// message may arrive without a trailing newline (ReadBytes returns the
// bytes alongside io.EOF); it is handled before the loop returns, so no
// message is dropped at end-of-stream.
//
// # Notifications inline, requests concurrently
//
// A notification is handled on this goroutine, immediately, because the one
// thing a client sends while a call is running is a request to stop it: queueing
// that behind the work it cancels would defeat it. A request is dispatched on its
// own goroutine, so a second request is answered while the first is still
// working, and so the id-keyed cancellation has something to reach.
//
// Every dispatched request finishes before this returns, which is what makes
// "every id answered exactly once" true: a loop that returned out from under its
// own goroutines would drop whatever they had not yet written, and a run that
// ends still answers the request it was answering.
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
func (s *server) messageLoop(ctx context.Context, stdin io.Reader, writeFailed <-chan struct{}) error {
	var inFlight sync.WaitGroup
	// Deferred FIRST, so it runs LAST: the reader is released from a delivery
	// nobody will take before the loop settles down to wait for the requests still
	// working, rather than staying parked for the length of the longest one.
	defer inFlight.Wait()

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
		case <-writeFailed:
			// A response write failed on the goroutine of the request it answered.
			// Reading on would mean accepting messages to answer into a stream
			// nothing can be written to, so the loop stops here and serve reports
			// the recorded failure.
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
			if writeErr := s.accept(ctx, &inFlight, trimmed); writeErr != nil {
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

// accept parses one message body and puts it on its path. A parse failure is
// answered inline with the JSON-RPC parse-error envelope and a null id. A
// notification (absent id) is handled inline for its side effect and yields no
// response. A request is dispatched on its own goroutine, under a context
// derived from the run's and registered under the request's id.
//
// The registration happens before the goroutine starts, so a cancellation
// arriving in the very next message cannot find the id untracked. It is released
// when the handler returns, so the registry holds only the requests currently in
// flight.
//
// The returned error is non-nil only when an inline write fails — a fatal
// transport condition. A write from a dispatched request cannot report one that
// way and records it instead; see write.
func (s *server) accept(ctx context.Context, inFlight *sync.WaitGroup, body []byte) error {
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return s.writeError(nil, codeParseError, "parse error: "+err.Error())
	}
	if len(req.ID) == 0 {
		s.handleNotification(&req)
		return nil
	}
	reqCtx, cancel := context.WithCancelCause(ctx)
	// The sink is installed before the request is routed, so a report written
	// while the work runs travels ahead of the response it belongs to rather than
	// after it. A request that minted no token reaches the same sink and puts
	// nothing on it.
	reqCtx = withProgressSink(reqCtx, s.progressSink())
	s.cancels.Track(req.ID, func() { cancel(errCancelledByClient) })
	inFlight.Add(1)
	go func() {
		defer inFlight.Done()
		// Releases the context when the request is done with it. It records no
		// cause where the client's cancellation already recorded one, so answer's
		// reading of the cause is unaffected by the order these run in.
		defer cancel(nil)
		defer s.cancels.Untrack(req.ID)
		s.answer(reqCtx, &req)
	}()
	return nil
}

// answer dispatches one request and frames what it produced to stdout, unless
// the client cancelled that request while it ran.
//
// The drop is keyed on the client's cancellation rather than on the request's
// context being done, and that difference is the whole of it: ending the run
// ends every per-request context too, and a run that ends still answers the
// request it was answering. Only a cancellation by id means the client has said
// it will not use the result.
//
// The race the protocol describes is accepted: a response already written when
// the cancellation arrives stands.
//
// It returns nothing. A write failure here is met on a request's own goroutine,
// where a returned error reaches nobody; write records it and serve reports it.
func (s *server) answer(ctx context.Context, req *rpcRequest) {
	result, rpcErr := s.dispatch(ctx, req)
	if errors.Is(context.Cause(ctx), errCancelledByClient) {
		return
	}
	if rpcErr != nil {
		_ = s.writeErrorEnvelope(req.ID, rpcErr)
		return
	}
	_ = s.writeResult(req.ID, result)
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

// write marshals resp and emits it as one newline-terminated line on stdout.
func (s *server) write(resp *rpcResponse) error {
	return s.writeFrame(resp, "response")
}

// writeNotification emits one notification carrying method and params, on the
// same stream and under the same lock every response is framed on.
//
// A failure is returned rather than swallowed, for the same reason a response's
// is: a stream that will not take a notification will not take the response
// following it either, and the run has one place to report that from.
func (s *server) writeNotification(method string, params any) error {
	return s.writeFrame(&rpcNotification{JSONRPC: "2.0", Method: method, Params: params}, "notification")
}

// progressSink is how this transport reports a request's progress: one framed
// notification through the writer the response itself is framed on, so a report
// and the answer that follows it cannot interleave their bytes.
//
// A write failure is dropped here rather than raised. Progress is an account of
// work whose outcome the response reports, so failing an operation because its
// narration could not be written would turn a cosmetic loss into a functional
// one — and the failure is recorded by the write itself, which is what the run
// reports from.
func (s *server) progressSink() progressSink {
	return func(note progressNotification) {
		_ = s.writeNotification(progressMethod, note)
	}
}

// writeFrame marshals v and emits it as one newline-terminated line on stdout.
// It is the single route from every outbound surface to the output stream, so
// stdout carries nothing but framed protocol messages.
//
// Requests are answered on their own goroutines, so the marshal-and-write runs
// under a mutex: two frames emitted at once would otherwise interleave their
// bytes and neither line would parse. Nothing else acquires it.
//
// It also records the first failure any write meets, because a write from a
// dispatched request returns to a goroutine nobody is reading, and serve still
// owes its caller that error — a broken transport leaves no channel to report the
// failure on.
//
// what names the frame in the failure, so a reader of the error is told which
// kind of message could not be written rather than only that one could not.
func (s *server) writeFrame(v any, what string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return s.recordWriteFailureLocked(fmt.Errorf("marshal %s: %w", what, err))
	}
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		return s.recordWriteFailureLocked(fmt.Errorf("write %s: %w", what, err))
	}
	return nil
}

// recordWriteFailureLocked records err as the run's first write failure and
// wakes the message loop, then returns err unchanged. The caller holds writeMu,
// which is what makes the wake-up channel close exactly once.
func (s *server) recordWriteFailureLocked(err error) error {
	if s.writeErr == nil {
		s.writeErr = err
		if s.writeFailed != nil {
			close(s.writeFailed)
		}
	}
	return err
}

// writeFailure reports the first failure a response write met, or nil.
func (s *server) writeFailure() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writeErr
}

// logf writes a diagnostic line to stderr. Every log surface routes here
// so no diagnostic ever reaches stdout, keeping the protocol stream pure.
//
// The line is composed in memory and handed to the stream in one write, under
// the lock every write to that stream is taken under. Requests are answered on
// their own goroutines, so a line assembled across several writes could be split
// by another goroutine's line and neither would be readable.
func (s *server) logf(format string, args ...any) {
	line := fmt.Sprintf(format+"\n", args...)
	s.logMu.Lock()
	defer s.logMu.Unlock()
	_, _ = io.WriteString(s.logw, line)
}

// logRecord writes one structured line to the same stream logf writes prose to.
//
// It is the second form of the one log route rather than a route of its own,
// which is what keeps the stream single: every line an operator collects comes
// from here or from logf, and both go to the stream this server was built with.
// Which form a line takes is decided where the session is composed, never at a
// call site — see structuredLog.
func (s *server) logRecord(line any) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	writeAuditLine(s.logw, line)
}

// structuredLog reports whether this session's own lines are machine-readable.
//
// It is read from the transport the session was composed for, and from nothing
// else, so the question is settled once — when the server is built — rather than
// at each site that writes a line. A client that launched this process reads its
// error stream itself and is served the prose it has always been served; a
// deployment an operator supervises has its stream collected by something that
// parses it.
func (s *server) structuredLog() bool {
	return s.transport == transportHTTP
}

// logLifecycle reports one thing the serving process did on its own account, in
// the form this session's transport fixes.
//
// prose is what a client-launched process writes, byte for byte as it has always
// written it. The structured form carries the same facts as fields. Both are
// composed here so the two cannot drift into reporting different things, and so
// no caller has to know which form its session takes.
func (s *server) logLifecycle(line lifecycleAudit, prose string, args ...any) {
	if !s.structuredLog() {
		s.logf(prose, args...)
		return
	}
	line.Time = auditNow()
	line.Transport = transportName(s.transport)
	if line.WorkspaceRoot == "" {
		line.WorkspaceRoot = s.startDir
	}
	s.logRecord(line)
}
