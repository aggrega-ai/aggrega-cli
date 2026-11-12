// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package yamlpos

import (
	"go.yaml.in/yaml/v3"
)

// EntryRange reports the range a document gives the entry a structural path
// names: from the key the path's last segment names — or, for a last segment
// naming a sequence position, the dash introducing that element — through the
// end of the last line the entry's block reaches. The second return is false
// where the document does not carry the whole path.
//
// # It resolves a path and never guesses one
//
// The path is the structural breadcrumb [InsertionPointIn] takes: each element a
// mapping key, or a sequence position written in decimal. Every segment must be
// there exactly, so a path the document carries only part of names no entry —
// unlike [FieldNode], which falls back to the deepest node it reached, because a
// finding needs SOME position and an entry that is not there has none. A path
// crossing a flow collection names no entry either: a flow collection's members
// share lines with one another, so no line range is any one member's alone.
//
// # What the range covers
//
// The key's own line from the key onwards, and every following line indented
// further than the key, which is the block YAML reads as that key's value. A
// blank line inside the block does not end it and one after it is not counted.
// A block sequence whose dashes sit at the key's own column is the one value that
// is not indented further, and its lines at that column belong where they open a
// dash, as they do in [InsertionPointIn]. A line indented no further than the key
// ends the block, and so does a document separator at the margin.
//
// The end is the end of that last line rather than the end of the value's last
// token, so a trailing comment written on the block's last line falls inside the
// entry, as a reader looking at the file would expect.
//
// idx must be built over the bytes doc was parsed from; the range's character
// offsets are in that index's encoding.
func EntryRange(doc *yaml.Node, idx LineIndex, path []string) (Range, bool) {
	if len(path) == 0 {
		return Range{}, false
	}
	node := doc
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}

	// line and column locate what stands for the entry's key — the key itself, or
	// the dash of a sequence element — as a 0-based line and a yaml.v3 column.
	var line, column int
	for _, seg := range path {
		if node == nil || node.Style&yaml.FlowStyle != 0 {
			return Range{}, false
		}
		switch node.Kind {
		case yaml.MappingNode:
			key, val := mappingEntry(node, seg)
			if key == nil {
				return Range{}, false
			}
			line, column = key.Line-1, key.Column
			node = val
		case yaml.SequenceNode:
			item := sequenceEntry(node, seg)
			if item == nil {
				return Range{}, false
			}
			// A sequence element has no key. What stands in for one is the dash
			// introducing it, whose column is the sequence's own.
			line, column = item.Line-1, node.Column
			node = item
		default:
			return Range{}, false
		}
	}

	indent := column - 1
	scanIndent, dashRequired := indent+1, false
	if node != nil && node.Kind == yaml.SequenceNode && node.Style&yaml.FlowStyle == 0 && node.Column-1 == indent {
		scanIndent, dashRequired = indent, true
	}
	last := idx.blockEnd(line+1, scanIndent, dashRequired)
	if last < line {
		last = line
	}

	start := Position{Line: line, Character: idx.ByteToLSPChar(line, idx.nodeColumnToByte(line, column))}
	return Range{Start: start, End: idx.endOfLine(last)}, true
}

// Contains reports whether p falls inside r, both ends included: a position at
// the range's first character and one at its last line's end are both inside
// it. Positions compare by line first and by character within a line, so the two
// must be in the same encoding.
func (r Range) Contains(p Position) bool {
	return !positionBefore(p, r.Start) && !positionBefore(r.End, p)
}

// positionBefore reports whether a comes strictly before b.
func positionBefore(a, b Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character < b.Character
}
