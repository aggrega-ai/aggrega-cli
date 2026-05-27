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
// Construct via newErrorData when a clean call-site one-liner is
// preferable; use a struct literal when the position fields would
// otherwise be omitted.
type errorData struct {
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// newErrorData returns a populated *errorData. Line and column are
// 1-based at the call site; the function does not adjust them. A
// zero line or column is acceptable (omitted from the wire via
// omitempty) — pass zero when the source position is file-level
// only.
func newErrorData(file string, line, column int) *errorData {
	return &errorData{File: file, Line: line, Column: column}
}
