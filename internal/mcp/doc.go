// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package mcp implements the Model Context Protocol server invoked by
// `aggrega mcp`, and answers the list and dispatch methods an AI assistant
// uses to discover and invoke the server's surface.
//
// Two transports reach the same server, and which one a run speaks is the
// command layer's choice. The stdio transport frames newline-delimited
// JSON-RPC 2.0 over stdin/stdout. The HTTP transport answers one message per
// request against a handler NewHTTPHandler builds, reached either on a
// listener ListenAndServe binds and owns — the address it parses, the TLS it
// terminates, the plaintext confinement it refuses on, and the drain it
// performs when the run ends — or on a handler a caller mounts itself. A
// deployment that names a health address gets a second listener beside that
// one, answering liveness and readiness and nothing else; the endpoint answers
// those two paths with the same not-found every unknown path receives. It
// shares the routing and the per-request revision with stdio, and differs in
// how a message is framed on the way in and out and in what ends a call
// already running. Over HTTP the
// connection closing is the cancellation signal the protocol defines there, so
// no request is recorded by id; stdio, which has no per-request stream to
// close, cancels by id through the registry instead.
//
// It speaks two protocol revisions, so a client of either connects. In
// 2026-07-28 a request states the revision it speaks and the capabilities
// its sender offers, and server/discover advertises the revisions, the
// tool, resource and prompt capabilities, and the connect-time guidance.
// Every answer that revision defines a shape for carries the kind of result
// it is and the server's identity, and the ones it makes cacheable carry how
// long the answer keeps and whether it may be shared. In 2025-06-18 the
// initialize handshake advertises the same, and its results carry none of
// that, since that revision defines none of it; a client selects that
// revision by opening with the handshake. Which revision a request speaks is
// read off that request; nothing is remembered between them.
//
// The stdio transport is strict about stream ownership. stdout carries only
// framed protocol messages — one compact JSON object per line — so a
// connected client can parse the stream without heuristics; every log,
// diagnostic, and error trace is written to stderr instead. The server
// reads one message per line until stdin reaches EOF or the run ends,
// then returns cleanly. Requests are answered concurrently and a client
// may cancel one by id, leaving every other one running.
//
// A long call reports its progress when the caller asks to be told. A request
// that mints a progress token in its metadata is told each time the work it
// started moves on, and a request that mints none is answered exactly as it was
// before. Over HTTP the response becomes an event stream carrying those reports
// and then the result as its last event, with a comment line at intervals while
// the work is quiet so whatever sits in between sees a live connection; the
// stream opens only once something actually reports, so a call whose work
// reports nothing is still answered with one JSON object. On stdio the reports
// are framed lines ahead of the response line. Nothing is reported once the
// request is over, on either transport.
//
// The package's contract is the wire protocol, not the Go API — no public
// types outside this package consume Run or ListenAndServe beyond the
// command layer that starts the server. The tool, resource, and prompt
// bodies are answered by the shared compiler operations, so no operation
// is reimplemented here.
//
// # What the serving process records, and what it never sends
//
// A process reached on a listener writes one machine-readable line per request
// to its error stream: who asked, what they asked for, how far it was admitted
// to reach, what became of it, and how long it took, plus the trace context the
// caller sent where it sent one that parses. Its own life — starting, binding,
// being refused, acquiring its documentation, stopping — is recorded in the same
// shape, each line naming the event instead of a caller, and a deployment whose
// workspace names a signing key no caller on a listener can spend is told so once
// among them. No credential, and no
// part of one, is ever written to any of them. A process speaking its own
// streams writes readable prose diagnostics instead, because the client reading
// them launched it and sits at the same machine.
//
// Nothing is sent to the client about any of this. The server emits no message
// notifications on either transport, and it reads the per-request log-level
// metadata for nothing: a request carrying that field is answered exactly as one
// without it. The protocol deprecated that surface in favour of a server's own
// error stream, which is where all of the above goes.
//
// # Where a failure travels
//
// A failing call takes one of two channels, and the choice is decided by a
// single question: can the caller fix it by changing the arguments of a
// tool that exists?
//
// When it can, the failure is a tool result marked as a failure, carrying
// the correction as its text. That covers a refused argument combination,
// a missing or wrong-typed argument, an unresolvable path, and a
// diagnostic from the compiler alike. It is the channel a model is shown:
// whether an error envelope ever reaches the model depends on client
// behavior the server cannot observe, so a failure the caller could have
// corrected must not be routed there.
//
// When it cannot, the failure is a JSON-RPC error envelope. The answer is
// no exactly when there is nothing to attribute a tool result to — the
// named tool does not exist, the request params could not be read so the
// tool is unknown, the request envelope is malformed, the request names no
// method this server serves — or when the failure is internal to the
// server and no change of arguments would alter it. The resource and
// prompt surfaces answer here for the same structural reason: their
// results carry no failure flag for a failure to live in.
//
// The line is therefore positional, and it binds every tool added later:
// once tools/call has resolved a tool by name, that tool's failures answer
// through its result. A rule a tool's arguments must satisfy is also
// declared in that tool's own input schema wherever the schema language
// can express it, so a caller reading the schema does not have to make the
// call to learn it.
//
// # What a failure carries
//
// The section above decides which channel a failure travels on. This one
// decides what travels with it, and the two are independent: a failure
// routed correctly can still arrive at a lower fidelity than the run that
// produced it had to offer.
//
// A refusal is not a reason to drop to a lower fidelity. Whatever findings
// an operation had in hand when it refused are carried in the failing
// result's structured payload, in the same shape a success reports them —
// so the payload is assembled before the verdict is read rather than after
// it. An operation whose findings are readable on success and only prose on
// failure reports the same findings two ways, and the failing case is the
// one a caller most needs to act on programmatically: it is the run whose
// output the caller has to change something about.
//
// Three consequences follow, and they are the whole of the rule:
//
//   - A failure result carries a structured payload whenever the operation
//     produced one. structuredErrorResult is the one route for that, and
//     errorResult is that same route with no payload — so a prose-only
//     failure is a statement that the operation had nothing structured to
//     report, not a shortcut past reporting it.
//   - The payload is built before the verdict is read. Built after a
//     success check, every field in it would be populated only on the path
//     where the caller needs it least.
//   - A field a refusal genuinely cannot fill is left absent rather than
//     filled with an empty value, because absent and empty are different
//     statements to a reader and only one of them is true.
//
// The rule is stated here once and not restated at the sites that follow
// it: two statements of one rule drift, nothing reads a comment, and no
// gate can fail on the copy that went stale. What holds the sites to it is
// a guard over this package's failure returns, which enumerates the ones
// carrying no structured payload and fails on any addition to that set.
package mcp
