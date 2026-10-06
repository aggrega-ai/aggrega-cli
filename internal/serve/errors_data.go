// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import "strings"

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
// Code and ShortCode name the condition the refusal reports, where it reports
// a registered one: the descriptive code a client branches on and the short
// code a reader quotes, under the names a published [Diagnostic] carries the
// same two identifiers with. They are what a client reads to tell one refusal
// from another — a configuration file leading out of the workspace from a
// configuration file that does not validate, say — without reading the message,
// and without leaning on the numeric code beside them, which names the state of
// the request and is the same for many conditions. They are never set at a
// construction site: [conditionRefusal] fills both from the failure the answer
// was rendered from, at the one place every envelope reaches the wire, so a site
// cannot report one without the other or report a condition its failure does not
// name.
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
	Code        string `json:"code,omitempty"`
	ShortCode   string `json:"shortCode,omitempty"`
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

// conditionRefusal is an error answer with the condition it reports identified:
// where the failure the answer was rendered from names a registered condition,
// the condition's descriptive code and short code ride in the answer's data, and
// the short code opens its message. An answer whose failure names no condition,
// and one rendered from no failure at all, is returned as it is. Which condition
// that is, refusalCodesOf decides.
//
// It is the one place a refusal's identity is decided, and every error answer
// carrying a failure reaches the wire through it — writeErrorEnvelope applies it
// — so no construction site sets either identifier, and an answer cannot carry a
// code its failure does not name, or one identifier without the other. The two
// are read off the failure by the derivation a published diagnostic reads them by
// ([conditionCodesOf]), so a refusal and a diagnostic reporting one condition
// name it alike. What a site owes is the failure itself: an answer rendered from
// an error carries that error as its cause (writeRefusal, or the envelope's
// cause), which is what lets this see the condition however many frames wrap
// it.
//
// The identifiers in the data are what a client branches on. The short code at
// the head of the message is for the person reading it: a terminal prints the
// same code in the bracketed head of the same refusal, and this answer has no
// head of its own, so the code leads the message — the same form on every
// refusal that names a condition, whatever the request and whatever its numeric
// code. A message that already opens with the code — the one-line refusal of a
// catalog document carries it in a bracketed head of its own — is not given it a
// second time.
//
// The answer it was handed is not changed; a copy is returned, so an envelope a
// request also records for publication keeps the message it was composed with.
func conditionRefusal(env *jsonRPCError) *jsonRPCError {
	if env == nil {
		return env
	}
	codes := refusalCodesOf(env)
	if codes.Code == "" || codes.ShortCode == "" {
		return env
	}
	out := *env
	data := errorData{}
	if env.Data != nil {
		data = *env.Data
	}
	data.Code, data.ShortCode = codes.Code, codes.ShortCode
	out.Data = &data
	if !opensWithShortCode(out.Message, codes.ShortCode) {
		out.Message = codes.ShortCode + ": " + out.Message
	}
	return &out
}

// refusalCodesOf returns the identifiers of the condition env reports. A refusal
// raised while rendering reports its first finding's — the finding its data names
// and its message opens with, whose identifiers were decided off that finding's
// own chain when it was positioned — because the refusal's whole chain can carry
// an outer frame registered under a condition of its own, which would identify the
// answer as something none of its findings is. Every other answer reports the
// condition its cause names, and an answer with no cause reports none.
func refusalCodesOf(env *jsonRPCError) ConditionCodes {
	if entries := env.refusal.entriesOrNone(); len(entries) > 0 {
		return entries[0].diagnostic.ConditionCodes
	}
	if env.cause == nil {
		return ConditionCodes{}
	}
	return conditionCodesOf(env.cause)
}

// opensWithShortCode reports whether message already opens with short: as the
// head conditionRefusal gives a message, or inside a bracketed head the message
// opens with.
func opensWithShortCode(message, short string) bool {
	if strings.HasPrefix(message, short+": ") {
		return true
	}
	if !strings.HasPrefix(message, "[") {
		return false
	}
	end := strings.Index(message, "]")
	return end > 0 && strings.Contains(message[:end], short)
}
