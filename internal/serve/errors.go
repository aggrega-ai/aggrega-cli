// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// JSON-RPC 2.0 standard error codes, per the spec's reserved range.
const (
	jsonRPCParseError     = -32700
	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	jsonRPCInternalError  = -32603
)

// Application-level error codes in the 1xxx range. Each code surfaces
// a daemon-specific condition the IDE client can branch on.
//
//	1001 — The daemon holds no parse of the file and is producing none;
//	       the requesting operation depends on a parse-cache entry the
//	       client must seed via aggrega/validate before retrying.
//	1003 — The lockfile for the Release the request named is absent;
//	       the requested operation requires a generated lockfile.
//	1004 — The requested entity could not be resolved from any
//	       catalog tier or from the Release the request named.
//	1005 — The daemon has received an aggrega/shutdown notification
//	       and is draining; new requests are refused while in this
//	       state. The client should not retry against the same
//	       subprocess.
//	1006 — A validation of the file is running right now, and it did
//	       not finish within the budget the waiting request allowed
//	       it. The client retries and sends nothing; the state it
//	       needs is on its way.
//
// 1001 and 1006 report opposite states and are kept apart for that reason.
// Under 1001 the daemon holds no parse of the file and is producing none, so a
// client seeds the state with a validation. Under 1006 a validation is under way,
// so sending another adds work and answers nothing — the client waits and asks
// again. One code covering both would leave a client unable to tell which action
// its situation called for.
//
// 1002 is not allocated. It reported that the daemon could not resolve a
// Release for an operation that needed one, which no request can be in:
// every operation acting on a Release is told which one, so there is no
// choosing left for the daemon to fail at. A request that names none asks
// for the work that needs no Release and is answered, not refused; a
// request naming one that cannot be loaded is answered with a diagnostic
// on the offending document, which is something its author can act on.
// The number is available for the next condition that needs a code.
const (
	errFileNotValidated    = 1001
	errLockfileAbsent      = 1003
	errEntityNotResolvable = 1004
	errShuttingDown        = 1005
	errValidationInFlight  = 1006
)
