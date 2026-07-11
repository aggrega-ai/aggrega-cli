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
// bodies are answered by the shared compiler operations; the list and
// dispatch surfaces here return well-formed results while those bodies
// are wired in.
package mcp
