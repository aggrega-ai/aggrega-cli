// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package mcp implements the stdio Model Context Protocol server invoked
// by `aggrega mcp`. The server speaks newline-delimited JSON-RPC 2.0 over
// stdin/stdout, targeting protocol revision 2025-06-18: it completes the
// initialize handshake, advertises tool, resource, and prompt
// capabilities, and answers the list and dispatch methods an AI assistant
// uses to discover and invoke the server's surface.
//
// The transport is strict about stream ownership. stdout carries only
// framed protocol messages — one compact JSON object per line — so a
// connected client can parse the stream without heuristics; every log,
// diagnostic, and error trace is written to stderr instead. The server
// reads one message per line until stdin reaches EOF, then returns
// cleanly.
//
// The package's contract is the wire protocol, not the Go API — no public
// types outside this package consume Run beyond the command layer that
// spawns the server as a subprocess. The tool, resource, and prompt
// bodies are answered by the shared compiler operations, so no operation
// is reimplemented here.
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
package mcp
