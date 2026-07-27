// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import "go.yaml.in/yaml/v3"

// nodeSpan is the [Start, End) byte range of a parsed node's source text within
// a document body. Start is the node's reported first source byte; End is the
// byte just past its last source byte.
type nodeSpan struct {
	Start int
	End   int
}

// spanOfNode is the single authoritative node-to-byte-span primitive. Given a
// parsed node and the source bytes it was parsed from, it returns the node's
// true source span, deriving the boundary from parser-verified positions rather
// than a scan that stops at the first newline. It is the one place the codec and
// the segmenter compute "where does this construct end in the byte stream", so
// the scalar-span, mapping-region, and provenance-splice sites cannot disagree
// on a multi-line construct.
//
// The End correctly covers the multi-line constructs yaml.v3 reports only the
// start line of:
//
//   - a block literal (|) or folded (>) scalar, including chomping (-/+) and
//     explicit-indentation variants — its body runs over every following line
//     indented deeper than the construct's parent;
//   - a plain scalar folded across several lines under the same indentation rule;
//   - a multi-line double- or single-quoted scalar — its body runs to the
//     closing quote, honoring the backslash-quote and doubled-single-quote
//     escapes across line breaks;
//   - a flow (inline) collection whose closing } or ] sits on its own line — its
//     body runs to the brace/bracket that balances the opener.
//
// parentIndent is the leading-space count of the construct that introduces the
// node — the mapping key or the sequence dash — which bounds a block or plain
// multi-line scalar: continuation lines are indented deeper than the parent, so
// the scalar ends at the first line indented no deeper. It is unused for quoted
// scalars, flow collections, and flow-context scalars, whose ends are
// self-delimiting. flow marks a scalar that sits inside a flow collection, where
// a value ends at the first unquoted ',', '}' or ']' rather than the end of the
// line.
//
// For a scalar the returned End excludes a trailing inline comment and trailing
// whitespace so those bytes survive an in-place edit; for a block scalar body
// (literal/folded) it is the end of the last non-blank source line, keeping
// indented content that resembles a comment or a document-end marker.
func spanOfNode(body []byte, n *yaml.Node, parentIndent int, flow bool, offs []int, bo func(int, int) int) nodeSpan {
	start := bo(n.Line, n.Column)
	if start < 0 {
		start = 0
	}
	if start > len(body) {
		start = len(body)
	}

	if isFlowCollection(n) {
		return nodeSpan{Start: start, End: flowCollectionSpanEnd(body, start)}
	}

	switch n.Kind {
	case yaml.ScalarNode:
		if flow {
			return nodeSpan{Start: start, End: flowScalarValueEnd(body, start)}
		}
		return nodeSpan{Start: start, End: blockScalarValueEnd(body, n, start, parentIndent)}
	case yaml.MappingNode:
		if len(n.Content) < 2 {
			return nodeSpan{Start: start, End: start}
		}
		lastKey := n.Content[len(n.Content)-2]
		lastVal := n.Content[len(n.Content)-1]
		return nodeSpan{Start: start, End: spanOfNode(body, lastVal, lastKey.Column-1, false, offs, bo).End}
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			return nodeSpan{Start: start, End: start}
		}
		lastItem := n.Content[len(n.Content)-1]
		// A sequence item's continuation is bounded by the dash indentation (the
		// sequence node's own column), not the item content column.
		return nodeSpan{Start: start, End: spanOfNode(body, lastItem, n.Column-1, false, offs, bo).End}
	default:
		// Alias or an unhandled kind occupies a single token on its line.
		return nodeSpan{Start: start, End: lineContentEnd(body, start, false)}
	}
}

// containerIndent returns the leading-space count of the construct that
// introduces a value node — its mapping key, or the sequence dash when the value
// is a sequence element — which is the parentIndent a block or plain multi-line
// scalar's continuation is measured against.
func containerIndent(key, parent *yaml.Node) int {
	if key != nil {
		return key.Column - 1
	}
	if parent != nil && parent.Kind == yaml.SequenceNode {
		return parent.Column - 1
	}
	return 0
}

// isFlowCollection reports whether n is a flow (inline) mapping or sequence,
// whose extent is brace/bracket-matched rather than indentation-derived.
func isFlowCollection(n *yaml.Node) bool {
	return n != nil && (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && isFlow(n)
}

// blockScalarValueEnd returns the end offset of a block-context scalar's source,
// dispatching on the scalar style so a multi-line body is covered in full.
func blockScalarValueEnd(body []byte, n *yaml.Node, start, parentIndent int) int {
	switch n.Style {
	case yaml.DoubleQuotedStyle:
		return quotedScalarEnd(body, start, '"')
	case yaml.SingleQuotedStyle:
		return quotedScalarEnd(body, start, '\'')
	case yaml.LiteralStyle, yaml.FoldedStyle:
		return indentedScalarEnd(body, start, parentIndent, true)
	default:
		return indentedScalarEnd(body, start, parentIndent, false)
	}
}

// flowScalarValueEnd returns the end offset of a scalar sitting inside a flow
// collection: the value runs to the first unquoted flow delimiter (',', '}',
// ']') or an unquoted inline comment on the line, whichever comes first, with a
// trailing run of whitespace trimmed. Delimiters inside a quoted string are
// ignored.
func flowScalarValueEnd(body []byte, start int) int {
	lineEnd := start
	inSingle, inDouble := false, false
	for lineEnd < len(body) {
		ch := body[lineEnd]
		if ch == '\n' {
			break
		}
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && lineEnd > start && (body[lineEnd-1] == ' ' || body[lineEnd-1] == '\t') {
				return trimTrailingSpace(body, start, lineEnd)
			}
		case ',', '}', ']':
			if !inSingle && !inDouble {
				return trimTrailingSpace(body, start, lineEnd)
			}
		}
		lineEnd++
	}
	return trimTrailingSpace(body, start, lineEnd)
}

// quotedScalarEnd returns the offset just past a quoted scalar's closing quote.
// body[start] is the opening quote. A double-quoted scalar honors the
// backslash-quote escape; a single-quoted scalar honors the doubled-single-quote
// escape. The scan crosses line breaks, so a multi-line quoted scalar is covered
// in full.
func quotedScalarEnd(body []byte, start int, quote byte) int {
	i := start + 1
	for i < len(body) {
		ch := body[i]
		if quote == '"' {
			if ch == '\\' {
				i += 2
				continue
			}
			if ch == '"' {
				return i + 1
			}
		} else {
			if ch == '\'' {
				if i+1 < len(body) && body[i+1] == '\'' {
					i += 2
					continue
				}
				return i + 1
			}
		}
		i++
	}
	return len(body)
}

// indentedScalarEnd returns the end offset of a block-literal/folded scalar
// (literal true) or a plain scalar (literal false), whose body may fold across
// several lines. Continuation lines are those indented deeper than parentIndent
// or blank; the scalar ends at the first line indented no deeper. For a plain
// scalar a continuation line that begins with a comment terminates the scalar,
// and each line's trailing inline comment and whitespace are trimmed; for a
// literal/folded scalar every non-blank line up to the dedent is body.
func indentedScalarEnd(body []byte, start, parentIndent int, literal bool) int {
	end := lineContentEnd(body, start, literal)
	nl := indexNewline(body, start)
	for nl >= 0 {
		lineStart := nl + 1
		if lineStart >= len(body) {
			break
		}
		indent := 0
		for lineStart+indent < len(body) && body[lineStart+indent] == ' ' {
			indent++
		}
		at := lineStart + indent
		blank := at >= len(body) || body[at] == '\n' || body[at] == '\r'
		if blank {
			// A blank line belongs to the block but is not itself a content line.
			nl = indexNewline(body, lineStart)
			continue
		}
		if indent <= parentIndent {
			break // dedent: the scalar ended on a previous line
		}
		if !literal && body[at] == '#' {
			break // a plain scalar cannot resume under a comment line
		}
		end = lineContentEnd(body, lineStart, literal)
		nl = indexNewline(body, lineStart)
	}
	return end
}

// lineContentEnd returns the offset just past the content of the line beginning
// at start. For a literal line every byte up to the newline is content (only a
// CRLF carriage return is excluded). For a non-literal line an inline comment
// (a '#' preceded by whitespace) and any trailing whitespace are excluded, so an
// end-of-line comment survives an in-place edit.
func lineContentEnd(body []byte, start int, literal bool) int {
	i := start
	for i < len(body) && body[i] != '\n' {
		i++
	}
	lineEnd := i
	if literal {
		if lineEnd > start && body[lineEnd-1] == '\r' {
			lineEnd--
		}
		return lineEnd
	}
	for k := start; k < lineEnd; k++ {
		if body[k] == '#' && k > start && (body[k-1] == ' ' || body[k-1] == '\t') {
			lineEnd = k
			break
		}
	}
	return trimTrailingSpace(body, start, lineEnd)
}

// flowCollectionSpanEnd returns the offset just past a flow collection's closing
// delimiter. start is the collection node's reported position; leading whitespace
// before the opening brace/bracket is tolerated. An unterminated collection
// (not expected after a successful parse) falls back to the line's content end.
func flowCollectionSpanEnd(body []byte, start int) int {
	open := start
	for open < len(body) && body[open] != '{' && body[open] != '[' && isFlowSpace(body[open]) {
		open++
	}
	if open >= len(body) || (body[open] != '{' && body[open] != '[') {
		return lineContentEnd(body, start, false)
	}
	closeAt, err := matchFlowClose(body, open)
	if err != nil {
		return lineContentEnd(body, start, false)
	}
	return closeAt + 1
}

// trimTrailingSpace returns end reduced past any trailing spaces, tabs, or a
// carriage return between start and end.
func trimTrailingSpace(body []byte, start, end int) int {
	for end > start && (body[end-1] == ' ' || body[end-1] == '\t' || body[end-1] == '\r') {
		end--
	}
	return end
}

// indexNewline returns the index of the first '\n' at or after from, or -1.
func indexNewline(body []byte, from int) int {
	for i := from; i < len(body); i++ {
		if body[i] == '\n' {
			return i
		}
	}
	return -1
}

// byteOffsetFor maps a 1-based (line, column) to a byte offset into body, given
// the precomputed line-start offsets. A non-positive line clamps to 0; a line
// past the last recorded line, or a column past the line, clamps to len(body).
// It is the single (line, column)-to-offset translation the codec's edit planner
// shares with the span primitive.
func byteOffsetFor(body []byte, offs []int) func(line, col int) int {
	return func(line, col int) int {
		idx := line - 1
		if idx < 0 {
			return 0
		}
		if idx >= len(offs) {
			return len(body)
		}
		off := offs[idx] + (col - 1)
		if off < 0 {
			off = 0
		}
		if off > len(body) {
			off = len(body)
		}
		return off
	}
}

// lineOfOffset returns the 1-based source line containing the byte at off, given
// the line-start offsets (offs[i] is the byte offset of line i+1).
func lineOfOffset(offs []int, off int) int {
	lo, hi := 0, len(offs)
	for lo < hi {
		mid := (lo + hi) / 2
		if offs[mid] <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < 1 {
		return 1
	}
	return lo
}
