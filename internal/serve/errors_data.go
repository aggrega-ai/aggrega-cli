// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// errorData is the structured payload carried in JSON-RPC error
// envelopes' optional `data` field. File, Line, and Column locate
// the source position the error refers to. Line and Column are
// 1-based per the daemon's LSP-aligned convention (matching the
// existing Diagnostic wire shape).
//
// Each path-valued field names one artifact and only that artifact, so
// a client learns what it received from the field it read rather than
// from the error code alongside it. File is the source document the
// position fields locate — the document being validated, or the
// Release whose load failed. Lockfile is the lockfile a lockfile-scoped
// failure is about. Release is the Release the error refers to. A site
// populates as many as it has in hand; none of them stands in for
// another.
//
// All fields are optional; the JSON encoding via omitempty drops
// zero-valued fields so callers can populate partial information
// without leaking placeholders. A site that knows only the file
// populates File alone; the wire emits {"file": "..."}; the
// extension reads file, finds line and column undefined, and falls
// back to file:1:1 positioning for the diagnostic.
//
// No field here carries an account of how a Release was chosen. That
// account belongs to the request whose subject it is — the one reporting
// which Release a client should select by default — and travels on its
// successful answer, because a workspace pointing at no Release is an
// ordinary state rather than a failed request. See activeReleaseResult.
//
// EndLine and EndColumn carry the end position of the source token the
// error refers to (1-based, exclusive of nothing — they name the
// position one past the token's last character, matching LSP Range
// end semantics). When present alongside Line and Column, the
// extension renders a diagnostic spanning the whole token rather than
// a one-character squiggle. They honor the same single-line caveat as
// the scalar-end computation: a multi-line block scalar falls back to
// omitting them, leaving the diagnostic point-style.
//
// Release names the Release the error refers to, spelled the way the
// request named it — the same value the `--release` flag takes. It is
// carried structurally so a client can compose the remediating command
// without parsing it back out of Message.
//
// Lockfile names the lockfile a lockfile-scoped failure is about: the
// path the daemon looked for and did not find, or could not stat. It is
// a separate field from File because a lockfile is a different artifact
// from the source document a diagnostic anchors at, and a client that
// reads one must not have to determine from the error code which of the
// two it was handed.
//
// Unreachable says the refusal is a registry the request named that
// could not be reached, as oci.Unreachable reads it from the failure
// itself — so a client names the registry as not reached without
// matching the words of Message, which follow the machine's resolver as
// much as the condition. A registry that answered, whatever it answered,
// leaves it false and so absent from the wire.
//
// Every field is additive and omitempty, so a client that does not
// recognise a field ignores it and one that does consumes it when
// present.
//
// Construct via newErrorData when a clean call-site one-liner is
// preferable; use newErrorDataWithRange when both start and end
// positions are known; use a struct literal when the position fields
// would otherwise be omitted.
type errorData struct {
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Column      int    `json:"column,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	EndColumn   int    `json:"end_column,omitempty"`
	Release     string `json:"release,omitempty"`
	Lockfile    string `json:"lockfile,omitempty"`
	Unreachable bool   `json:"unreachable,omitempty"`
}

// newLockfileErrorData returns a populated *errorData for a
// lockfile-scoped failure: the lockfile the daemon looked for, plus the
// Release it belongs to, spelled the way the request named it. The
// position fields stay zero (omitted from the wire) — a lockfile-scoped
// failure is about a whole file, not a position in one. File is left
// empty on purpose: the failing artifact is the lockfile, and it has its
// own field.
func newLockfileErrorData(lockfilePath, release string) *errorData {
	return &errorData{Lockfile: lockfilePath, Release: release}
}

// newErrorData returns a populated *errorData. Line and column are
// 1-based at the call site; the function does not adjust them. A
// zero line or column is acceptable (omitted from the wire via
// omitempty) — pass zero when the source position is file-level
// only.
func newErrorData(file string, line, column int) *errorData {
	return &errorData{File: file, Line: line, Column: column}
}

// newErrorDataWithRange returns a populated *errorData carrying both
// the start (line, column) and end (endLine, endColumn) positions of
// the source token. All four are 1-based at the call site; the
// function does not adjust them. Zero end values are acceptable
// (omitted from the wire via omitempty) — pass zero when the token
// has no recoverable end position, in which case the extension falls
// back to a point-style range at the start position.
func newErrorDataWithRange(file string, line, column, endLine, endColumn int) *errorData {
	return &errorData{File: file, Line: line, Column: column, EndLine: endLine, EndColumn: endColumn}
}
