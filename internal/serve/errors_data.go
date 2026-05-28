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
// Cumulative shape across consecutive evolutions:
//
//	File           — initial shape (envelope's source-file label)
//	Line           — initial shape (1-based, omitempty)
//	Column         — initial shape (1-based, omitempty)
//	CascadeAttempt — added for the release-not-resolved emission path
//
// All fields are additive and omitempty so older clients ignore
// new fields and newer clients consume them when present.
//
// Construct via newErrorData when a clean call-site one-liner is
// preferable; use newErrorDataWithCascade for the 1002 emission
// path; use a struct literal when the position fields would
// otherwise be omitted.
type errorData struct {
	File           string `json:"file,omitempty"`
	Line           int    `json:"line,omitempty"`
	Column         int    `json:"column,omitempty"`
	CascadeAttempt string `json:"cascade_attempt,omitempty"`
}

// newErrorData returns a populated *errorData. Line and column are
// 1-based at the call site; the function does not adjust them. A
// zero line or column is acceptable (omitted from the wire via
// omitempty) — pass zero when the source position is file-level
// only.
func newErrorData(file string, line, column int) *errorData {
	return &errorData{File: file, Line: line, Column: column}
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
