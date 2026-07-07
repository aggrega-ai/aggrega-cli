// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"
	"strconv"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Segment is one YAML document carved from a single emitted file, addressed by
// an opaque positional token and recorded as a byte-range into the original
// file bytes. The segments of a file tile its bytes exactly: they are
// contiguous, disjoint, and gap-free, so concatenating every segment's range
// reproduces the original file byte-for-byte.
//
// A segment is split into three sub-regions of the original bytes:
//
//   - [Start, bodyStart)   leading affix — the `---`/`...` separator that
//     introduces the document, plus any leading comments or directive lines
//     that precede the document's first content token.
//   - [bodyStart, bodyEnd) body — the document's content.
//   - [bodyEnd, End)       trailing affix — a trailing `...` document-end
//     marker, trailing comments, and trailing blank lines that follow the
//     content but precede the next document's separator.
//
// On apply-back an unpatched segment is emitted as [Start, End) verbatim; a
// patched segment is emitted as the leading affix verbatim, then the
// re-serialized document body, then the trailing affix verbatim. Preserving
// the affixes verbatim keeps the structural `---`/`...` markers and surrounding
// comments intact, so the lossy parse→emit round-trip is confined to exactly
// the body of the document that was intentionally changed.
type Segment struct {
	// Token is the opaque positional address of this document: the bare
	// emitted filename for a single-document file, and "<filename>#<ordinal>"
	// (0-based ordinal, file order) for each document of a multi-document file.
	Token string

	// Start and End are the byte offsets of this segment's range into the
	// original file bytes — Start inclusive, End exclusive.
	Start int
	End   int

	// bodyStart and bodyEnd bound the document body within [Start, End): the
	// leading affix is [Start, bodyStart) and the trailing affix is
	// [bodyEnd, End). Both are byte offsets into the original file bytes.
	bodyStart int
	bodyEnd   int
}

// Range returns the segment's full bytes from the original file: the leading
// affix, body, and trailing affix concatenated, verbatim.
func (s Segment) Range(original []byte) []byte { return original[s.Start:s.End] }

// Split parses one emitted file's bytes into its constituent YAML documents in
// file order, returning one Segment per document. Boundary detection is
// parser-driven: the document count and each document's start are taken from
// the YAML decoder, so a `---` inside a block or flow scalar is never mistaken
// for a separator.
//
// name is the emitted filename and becomes the token base. A single-document
// file yields one segment tokened with the bare name; a multi-document file
// yields one segment per document tokened "<name>#<ordinal>".
//
// A file whose bytes are not parseable structured YAML returns a wrapped
// ErrPipelinePatchTargetNotStructured: such a file has no addressable document
// tree and cannot be a PATCH target. A file that parses to zero documents
// (empty, whitespace-only, or comment-only) returns no segments and no error —
// it carries no addressable document, so there is nothing to patch, and the
// caller emits its original bytes unchanged.
func Split(name string, body []byte) ([]Segment, error) {
	s := string(body)
	docs, startLines, err := decodeAllDocsWithLines(s)
	if err != nil {
		// Wrap the typed sentinel (so callers match with errors.Is) and carry
		// the parser's detail in the message for the operator's diagnostic.
		return nil, cerrors.Wrap(ErrPipelinePatchTargetNotStructured, cerrors.CatalogError,
			cerrors.SourceLocation{File: name},
			ErrPipelinePatchTargetNotStructured.Error()+": "+err.Error())
	}
	nDoc := len(docs)
	if nDoc == 0 {
		if len(body) == 0 {
			return nil, nil
		}
		// Non-empty bytes that parse to zero documents (whitespace-only,
		// comment-only). There is no addressable document — the file carries no
		// PATCH target — but the bytes must round-trip. Emit one affix-only
		// segment spanning the whole file: its whole range is leading affix, its
		// body is empty, so apply-back reproduces the bytes verbatim and the
		// segment is never a patch target (an empty body is never substituted).
		return []Segment{{
			Token:     name,
			Start:     0,
			End:       len(body),
			bodyStart: len(body),
			bodyEnd:   len(body),
		}}, nil
	}

	offsets := lineStartOffsets(s)
	raw := strings.Split(s, "\n")
	// lineByteOffset maps a 1-based line number to its byte offset, clamping to
	// the file length for a line past the last newline (e.g. a document whose
	// implied content sits on a line that has no bytes, as with a bare "---"
	// null document in a file with no trailing newline).
	lineByteOffset := func(oneBased int) int {
		idx := oneBased - 1
		if idx < 0 {
			return 0
		}
		if idx >= len(offsets) {
			return len(s)
		}
		return offsets[idx]
	}

	// Region-start line for each document. The leading document always starts
	// at line 1, byte offset 0, so the first segment captures any BOM, leading
	// comments, or leading directive/separator lines and Start[0] == 0 — the
	// precondition that makes the partition reproduce the original on join. A
	// subsequent document starts at the separator line that introduces it: the
	// last `---`/`...` boundary line at or before that document's start line and
	// after the previous document's start line.
	regionStart := make([]int, nDoc)
	regionStart[0] = 1
	for i := 1; i < nDoc; i++ {
		prev := startLines[i-1]
		cur := startLines[i]
		sep := cur
		for ln := prev + 1; ln <= cur && ln <= len(raw); ln++ {
			if isDocumentSeparatorLine(raw[ln-1]) {
				sep = ln
			}
		}
		regionStart[i] = sep
	}

	segs := make([]Segment, nDoc)
	for i := 0; i < nDoc; i++ {
		start := lineByteOffset(regionStart[i])
		end := len(s)
		if i+1 < nDoc {
			end = lineByteOffset(regionStart[i+1])
		}

		// bodyStart is the document's root content node position — NOT the
		// DocumentNode line, which is the separator line. Using the content
		// node keeps the `---` separator and any leading comments in the leading
		// affix. The column is load-bearing for the inline shape `--- {a: 1}`,
		// where the separator and content share a line: addressing the line
		// start alone would fold the `--- ` bytes into the body and drop the
		// separator when the document is patched. Adding (column-1) bytes lands
		// bodyStart on the exact content byte, so the `--- ` prefix stays in the
		// leading affix. For a block document whose content is at column 1 the
		// adjustment is zero, so bodyStart is unchanged.
		contentLine := startLines[i]
		contentCol := 1
		if dc := docs[i].Content; len(dc) > 0 {
			contentLine = dc[0].Line
			if dc[0].Column > 0 {
				contentCol = dc[0].Column
			}
		}
		bodyStart := lineByteOffset(contentLine) + (contentCol - 1)
		// yaml.v3 Column is 1-based and rune-based. The bytes before content on
		// a separator/indent line are ASCII (`-`, space, tab), so column-1 maps
		// to the same byte count there; the clamps below keep bodyStart inside
		// the segment regardless, so a hypothetical multibyte leading region can
		// never push bodyStart past the line into the next document.
		if bodyStart < start {
			bodyStart = start
		}
		nextLineStart := lineByteOffset(contentLine + 1)
		if bodyStart > nextLineStart {
			bodyStart = nextLineStart
		}
		if bodyStart > end {
			bodyStart = end
		}

		// bodyEnd is derived by scanning backwards from the segment end over
		// the trailing run of blank, comment-only, and `...` end-marker lines.
		// This is independent of node-line reporting, which does not record a
		// block scalar's span, so an indented `...` inside a `|`/`>` scalar
		// stays in the body rather than being mistaken for an end-marker.
		bodyEnd := start + bodyEndWithinSegment(s[start:end])
		if bodyEnd < bodyStart {
			bodyEnd = bodyStart
		}

		segs[i] = Segment{
			Token:     documentToken(name, i, nDoc),
			Start:     start,
			End:       end,
			bodyStart: bodyStart,
			bodyEnd:   bodyEnd,
		}
	}
	return segs, nil
}

// ApplyBack rebuilds an emitted file from its segments. Every segment whose
// token is absent from patchedByToken is re-joined from its original byte-range
// verbatim; a segment whose token is present has its body replaced with the
// patched bytes, while its leading and trailing affixes are preserved verbatim.
// Re-join is pure concatenation of the segments in file order, so an unpatched
// document — including an unpatched sibling in a file where another document was
// patched — is reproduced byte-for-byte.
//
// patchedByToken is consulted only by key lookup; output order is driven by the
// ordered segs slice, never by iterating the map, so the result is
// deterministic across runs of identical input.
func ApplyBack(original []byte, segs []Segment, patchedByToken map[string][]byte) []byte {
	var b strings.Builder
	b.Grow(len(original))
	for _, sg := range segs {
		if repl, ok := patchedByToken[sg.Token]; ok {
			b.Write(original[sg.Start:sg.bodyStart])
			b.Write(repl)
			b.Write(original[sg.bodyEnd:sg.End])
			continue
		}
		b.Write(original[sg.Start:sg.End])
	}
	return []byte(b.String())
}

// documentToken returns the opaque positional token for document i of a file
// that parsed to nDoc documents: the bare name when the file holds a single
// document, "<name>#<ordinal>" otherwise. The token is a pure function of the
// filename and the 0-based ordinal in file order.
func documentToken(name string, i, nDoc int) string {
	if nDoc == 1 {
		return name
	}
	return name + "#" + strconv.Itoa(i)
}

// lineStartOffsets returns the byte offset of the start of each line in s. The
// first entry is always 0; a new entry follows every '\n'. A trailing '\n'
// therefore yields a final past-the-end offset, which is never indexed because
// a region-start line index is always within the parsed line count.
func lineStartOffsets(s string) []int {
	offs := make([]int, 1, strings.Count(s, "\n")+1)
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			offs = append(offs, i+1)
		}
	}
	return offs
}

// isDocumentSeparatorLine reports whether a raw line (terminator already
// excluded by the caller's line split) is a YAML document boundary marker at
// column 0: a `---` document-start or a `...` document-end, either standing
// alone or introducing content on the same line (e.g. `--- {a: 1}`). A trailing
// carriage return (CRLF input) is tolerated. This scan only runs over the line
// window the parser already bracketed between two document start lines, so it
// never inspects a line inside a block or flow scalar.
func isDocumentSeparatorLine(line string) bool {
	t := strings.TrimSuffix(line, "\r")
	return t == "---" || strings.HasPrefix(t, "--- ") || strings.HasPrefix(t, "---\t") ||
		t == "..." || strings.HasPrefix(t, "... ") || strings.HasPrefix(t, "...\t")
}

// bodyEndWithinSegment returns the offset, relative to the start of seg, at
// which the document body ends and the trailing affix begins. It scans the
// segment's lines from the end, treating a blank line, a comment-only line (a
// `#` after optional whitespace), and a `...` document-end marker at column 0
// as trailing affix; the first line from the end that is none of these is the
// body's last line. A segment that is entirely affix (an empty/null document
// whose region is just its separator) yields 0.
func bodyEndWithinSegment(seg string) int {
	lines := splitKeepingTerminators(seg)
	last := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if !isTrailingAffixLine(lines[i]) {
			last = i
			break
		}
	}
	if last < 0 {
		return 0
	}
	off := 0
	for i := 0; i <= last; i++ {
		off += len(lines[i])
	}
	return off
}

// isTrailingAffixLine reports whether a line (terminator retained) belongs to a
// document's trailing affix: a blank line, a comment-only line at any indent, or
// a `...` document-end marker at column 0. A line whose `...` is indented (an
// end-marker-looking line inside a block scalar) is not column 0 and is treated
// as body.
func isTrailingAffixLine(line string) bool {
	core := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	trimmed := strings.TrimSpace(core)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "#") {
		return true
	}
	return core == "..." || strings.HasPrefix(core, "... ") || strings.HasPrefix(core, "...\t")
}

// splitKeepingTerminators splits s into lines, retaining each line's '\n'
// terminator, and drops the empty trailing element strings.SplitAfter yields
// when s ends with '\n'. The retained terminators make summed line lengths an
// exact byte offset into the original segment.
func splitKeepingTerminators(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// segmentDebugString renders a segment's three sub-regions for test
// diagnostics. It is unexported and used only by tests; keeping it here keeps
// the offset arithmetic in one place.
func segmentDebugString(original []byte, s Segment) string {
	return fmt.Sprintf("lead=%q body=%q trail=%q",
		string(original[s.Start:s.bodyStart]),
		string(original[s.bodyStart:s.bodyEnd]),
		string(original[s.bodyEnd:s.End]))
}
