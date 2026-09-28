// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

// FieldSpan is the source span of a document key, or of the array element a
// path ends at, in the coordinate space of the LineIndex it was resolved
// against. Line and Column are 1-based and always set. EndLine and EndColumn
// are 1-based and span the key token, or the element's scalar; both are zero
// when there is no single-line span (a block scalar, or an element that is
// itself a mapping or a sequence), which callers render point-style.
type FieldSpan struct {
	Line      int
	Column    int
	EndLine   int
	EndColumn int
}

// FieldNode resolves a dotted field path against a parsed document and
// returns the node it names: a key, or an array element. It is the single
// implementation of the field-path-to-node descent: every surface that turns
// a validation finding's field path into a source position resolves it here,
// so a position reported for the same document and the same path cannot
// differ between one surface and another.
//
// Path syntax:
//
//   - A leading "." is optional and marks an absolute path from the
//     document root. Both ".spec.parameters.host" and
//     "spec.parameters.host" resolve identically, because the two finding
//     shapes that feed this walk spell the same path differently.
//   - Segments are dot-separated, and each segment is a literal key in a
//     mapping node.
//   - A segment may end in one or more bracketed decimal indices —
//     "allowed_origins[1]", "matrix[0][2]" — each selecting an element of the
//     sequence reached so far. That is how a validation finding names a value
//     inside an array. A mapping key spelled exactly like the whole segment is
//     matched first, so such a key keeps its literal meaning.
//
// Resolution returns the LAST segment's key node on a full match — or, where
// the path ends at an array element, that element's own node — and the
// deepest node actually reached when the document runs out before the path
// does: an enclosing key, or the element the walk had entered. That fallback
// is deliberate and is what positions a missing-required-property finding:
// the key it names is by definition absent, so the nearest enclosing node
// that does exist is the closest true position available. Returns nil when
// the path is empty or its first segment matches nothing.
//
// Keys containing a literal dot, and a key written in a bracketed quoted
// spelling (`components["web"]`), are not addressable; nor is an index past
// the end of its sequence, or on a value that is not one. A path crossing any
// of these resolves to the deepest node reached before it, which is a coarser
// position rather than a wrong one.
func FieldNode(doc *yaml.Node, fieldPath string) *yaml.Node {
	path := strings.TrimPrefix(fieldPath, ".")
	if path == "" {
		return nil
	}
	root := doc
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	return walkFieldSegments(root, strings.Split(path, "."))
}

// FieldRange resolves fieldPath through FieldNode and converts the node it
// finds — a key, or an array element — into a 1-based span in idx's
// coordinate space. The second return reports whether the path resolved at
// all; on false the span is the zero value and the caller decides what an
// unpositioned finding looks like on its own surface — a diagnostic protocol
// needs some position and substitutes the document start, while a textual
// diagnostic is better off naming no position than a position the reader
// would have to distrust.
func FieldRange(doc *yaml.Node, idx LineIndex, fieldPath string) (FieldSpan, bool) {
	node := FieldNode(doc, fieldPath)
	if node == nil {
		return FieldSpan{}, false
	}
	start := idx.NodeToPosition(node)
	span := FieldSpan{Line: start.Line + 1, Column: start.Character + 1}
	// A multi-line scalar has no single-line span to underline, so the end
	// is left unset rather than pointing at a coordinate on another line. An
	// element that is a mapping or a sequence has no text of its own to
	// underline either, and measuring one as a scalar would give it an empty
	// span, so it is drawn at its start the same way.
	if node.Kind != yaml.ScalarNode || strings.Contains(node.Value, "\n") {
		return span, true
	}
	end := idx.ScalarEnd(node)
	span.EndLine = end.Line + 1
	span.EndColumn = end.Character + 1
	return span, true
}

// walkFieldSegments descends the mapping tree following segments,
// implementing FieldNode's documented match-and-fallback contract.
func walkFieldSegments(n *yaml.Node, segments []string) *yaml.Node {
	if n == nil || len(segments) == 0 {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	target := segments[0]
	rest := segments[1:]
	if keyNode, valNode := mappingEntry(n, target); keyNode != nil {
		return walkBeneath(keyNode, valNode, rest)
	}
	key, indices, ok := splitIndexedSegment(target)
	if !ok {
		return nil
	}
	keyNode, element := mappingEntry(n, key)
	if keyNode == nil {
		return nil
	}
	reached := keyNode
	for _, index := range indices {
		if element == nil || element.Kind != yaml.SequenceNode {
			return reached
		}
		next := sequenceEntry(element, index)
		if next == nil {
			return reached
		}
		element, reached = next, next
	}
	return walkBeneath(reached, element, rest)
}

// walkBeneath continues the walk into value, the node the segments so far
// arrived at, and falls back to reached — the key or element standing for
// that node — when nothing beneath it matches.
func walkBeneath(reached, value *yaml.Node, rest []string) *yaml.Node {
	if len(rest) == 0 {
		return reached
	}
	if inner := walkFieldSegments(value, rest); inner != nil {
		return inner
	}
	return reached
}

// splitIndexedSegment reads a segment spelled as a key followed by one or more
// bracketed decimal indices — "allowed_origins[1]", "matrix[0][2]" — and
// returns the key and each index's digits, in order, in the form sequenceEntry
// reads. ok is false for every other spelling, a bracketed quoted key
// included, so such a segment keeps naming a literal key and nothing else.
func splitIndexedSegment(segment string) (key string, indices []string, ok bool) {
	open := strings.IndexByte(segment, '[')
	if open <= 0 {
		return "", nil, false
	}
	key, rest := segment[:open], segment[open:]
	for rest != "" {
		end := strings.IndexByte(rest, ']')
		if rest[0] != '[' || end < 2 {
			return "", nil, false
		}
		digits := rest[1:end]
		for i := 0; i < len(digits); i++ {
			if digits[i] < '0' || digits[i] > '9' {
				return "", nil, false
			}
		}
		indices = append(indices, digits)
		rest = rest[end+1:]
	}
	return key, indices, true
}
