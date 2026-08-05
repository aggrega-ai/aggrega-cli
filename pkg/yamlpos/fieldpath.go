// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

// FieldSpan is the source span of a document key, in the coordinate space
// of the LineIndex it was resolved against. Line and Column are 1-based
// and always set. EndLine and EndColumn are 1-based and span the key
// token; both are zero when the key has no single-line span (a block
// scalar), which callers render point-style.
type FieldSpan struct {
	Line      int
	Column    int
	EndLine   int
	EndColumn int
}

// FieldNode resolves a dotted field path against a parsed document and
// returns the key node it names. It is the single implementation of the
// field-path-to-node descent: every surface that turns a validation
// finding's field path into a source position resolves it here, so a
// position reported for the same document and the same path cannot differ
// between one surface and another.
//
// Path syntax:
//
//   - A leading "." is optional and marks an absolute path from the
//     document root. Both ".spec.parameters.host" and
//     "spec.parameters.host" resolve identically, because the two finding
//     shapes that feed this walk spell the same path differently.
//   - Segments are dot-separated, and each segment is a literal key in a
//     mapping node.
//
// Resolution returns the LAST segment's key node on a full match, and the
// deepest key node actually reached when the document runs out before the
// path does. That fallback is deliberate and is what positions a
// missing-required-property finding: the key it names is by definition
// absent, so the nearest enclosing key that does exist is the closest true
// position available. Returns nil when the path is empty or its first
// segment matches nothing.
//
// Sequence indices and keys containing a literal dot are not addressable:
// a path crossing either resolves to the deepest key reached before it,
// which is a coarser position rather than a wrong one.
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

// FieldRange resolves fieldPath through FieldNode and converts the key
// node it finds into a 1-based span in idx's coordinate space. The second
// return reports whether the path resolved at all; on false the span is
// the zero value and the caller decides what an unpositioned finding looks
// like on its own surface — a diagnostic protocol needs some position and
// substitutes the document start, while a textual diagnostic is better off
// naming no position than a position the reader would have to distrust.
func FieldRange(doc *yaml.Node, idx LineIndex, fieldPath string) (FieldSpan, bool) {
	node := FieldNode(doc, fieldPath)
	if node == nil {
		return FieldSpan{}, false
	}
	start := idx.NodeToPosition(node)
	span := FieldSpan{Line: start.Line + 1, Column: start.Character + 1}
	// A multi-line scalar has no single-line span to underline, so the end
	// is left unset rather than pointing at a coordinate on another line.
	if strings.Contains(node.Value, "\n") {
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
	for i := 0; i+1 < len(n.Content); i += 2 {
		keyNode := n.Content[i]
		valNode := n.Content[i+1]
		if keyNode.Value != target {
			continue
		}
		if len(rest) == 0 {
			return keyNode
		}
		if inner := walkFieldSegments(valNode, rest); inner != nil {
			return inner
		}
		return keyNode
	}
	return nil
}
