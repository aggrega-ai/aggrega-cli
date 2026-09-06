// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package scaffoldops is the shared drafting layer that front ends drive to turn
// an entity kind into a starter document. It single-sources the one operation a
// front end needs — deriving a minimal YAML skeleton from that kind's embedded
// JSON Schema — so every front end runs one implementation rather than its own
// copy, and two front ends asked for the same kind return the same bytes.
//
// The layer takes a kind token and an optional name and returns text. It holds
// no session state and reads no front-end fields: a caller passes what it was
// asked for and adapts the result into its own wire shape, keeping its own
// argument decoding and result shaping to itself.
//
// It touches no filesystem. It opens no file, creates none, and chooses no path:
// what it returns is a document to write, not a document that was written, and
// where the text goes — or whether it goes anywhere at all — is the caller's
// decision.
//
// The corrective text for a kind token no schema answers to lives here too, so
// every surface that resolves a kind token rejects an unrecognised one in the
// same words.
package scaffoldops
