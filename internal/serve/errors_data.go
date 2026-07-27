// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// errorData is the structured payload carried in JSON-RPC error
// envelopes' optional `data` field. File, Line, and Column locate
// the source position the error refers to. Line and Column are
// 1-based per the daemon's LSP-aligned convention (matching the
// existing Diagnostic wire shape).
//
// All fields are optional; the JSON encoding via omitempty drops
// zero-valued fields so callers can populate partial information
// without leaking placeholders. A site that knows only the file
// populates File alone; the wire emits {"file": "..."}; the
// extension reads file, finds line and column undefined, and falls
// back to file:1:1 positioning for the diagnostic.
//
// CascadeAttempt carries the multi-line cascade trace for 1002
// (release-not-resolved) emissions. The short summary that goes in
// the toast lives in the envelope's Message; the full trace surfaces
// through the extension's OutputChannel auto-log. Other error codes
// leave CascadeAttempt empty.
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
// Cumulative shape across consecutive evolutions:
//
//	File           — initial shape (envelope's source-file label)
//	Line           — initial shape (1-based, omitempty)
//	Column         — initial shape (1-based, omitempty)
//	CascadeAttempt — added for the release-not-resolved emission path
//	EndLine        — added for wide diagnostic ranges (1-based, omitempty)
//	EndColumn      — added for wide diagnostic ranges (1-based, omitempty)
//	Release        — added for the lockfile-absent emission path
//
// All fields are additive and omitempty so older clients ignore
// new fields and newer clients consume them when present.
//
// Construct via newErrorData when a clean call-site one-liner is
// preferable; use newErrorDataWithRange when both start and end
// positions are known; use newErrorDataWithCascade for the 1002
// emission path; use a struct literal when the position fields would
// otherwise be omitted.
type errorData struct {
	File           string `json:"file,omitempty"`
	Line           int    `json:"line,omitempty"`
	Column         int    `json:"column,omitempty"`
	CascadeAttempt string `json:"cascade_attempt,omitempty"`
	EndLine        int    `json:"end_line,omitempty"`
	EndColumn      int    `json:"end_column,omitempty"`
	Release        string `json:"release,omitempty"`
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

// newErrorDataWithCascade returns a populated *errorData carrying
// the cascade trace alongside the source-file label. Used by the
// 1002 (release-not-resolved) emission path so the extension can
// consume the full multi-line trace through its OutputChannel
// while the toast displays the short summary from the envelope's
// Message. Position fields stay zero (omitted from the wire) — the
// 1002 emission is file-level only.
func newErrorDataWithCascade(file, cascadeAttempt string) *errorData {
	return &errorData{File: file, CascadeAttempt: cascadeAttempt}
}
