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
//	1001 — File contents have not been validated yet; the requesting
//	       operation depends on a parse-cache entry the client must
//	       seed via aggrega/validate before retrying.
//	1002 — No active Release could be resolved by the multi-Manifest
//	       disambiguation cascade; the requested operation needs one.
//	1003 — The workspace lockfile is absent; the requested operation
//	       requires a generated lockfile.
//	1004 — The requested entity could not be resolved from any
//	       catalog tier or the active Release.
//	1005 — The daemon has received an aggrega/shutdown notification
//	       and is draining; new requests are refused while in this
//	       state. The client should not retry against the same
//	       subprocess.
const (
	errFileNotValidated    = 1001
	errReleaseNotResolved  = 1002
	errLockfileAbsent      = 1003
	errEntityNotResolvable = 1004
	errShuttingDown        = 1005
)
