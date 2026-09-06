// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// defaultIndentStep is the nesting step reported for a document that exhibits
// no nesting of its own.
//
// It is reported alongside Insertion.IndentStepMeasured set to false, so a
// caller can always tell a measurement from this fallback. The fallback exists
// because the alternative — reporting zero — collapses the arithmetic a caller
// performs with the step, and a collapsed indent produces a document that no
// longer parses. Two is the step every conventional YAML document uses, and a
// document with no nesting to measure supplies no evidence against it.
const defaultIndentStep = 2

// EntryShape names the shape an entry appended to a container takes.
//
// It is read off the container the document already carries and is never
// inferred from what the container is called: a key named in the request tells
// this package nothing about what may go inside it, and a table mapping names
// to shapes would be this package deciding an author's meaning from a spelling.
// So a container that does not exist yet, and one that exists carrying nothing,
// both report EntryShapeUnknown — the document has not said, and neither will
// this.
type EntryShape string

const (
	// EntryShapeMapping reports a container the document carries as a block
	// mapping. An entry appended to it is a key and its value.
	EntryShapeMapping EntryShape = "mapping"

	// EntryShapeSequence reports a container the document carries as a block
	// sequence. An entry appended to it is a dash and its value.
	EntryShapeSequence EntryShape = "sequence"

	// EntryShapeUnknown reports a container the document does not carry, or
	// carries with nothing in it. Which shape an entry takes is then the
	// caller's to decide, because the document says nothing either way.
	EntryShapeUnknown EntryShape = "unknown"
)

// InsertionRefusal names a reason no insertion point can be reported for a
// container path in a document, or is empty where one can.
//
// It is a value rather than an error identity on purpose. Each of these refuses
// one REQUEST over one document: nothing halts, nothing is being compiled, and
// the surfaces that carry this capability each already have a channel for
// telling a caller its arguments do not address anything. Carrying the reason as
// data lets every one of those surfaces report the same refusal in its own
// channel, and lets [InsertionRefusal.Reason] be the single home of the wording.
//
// The values are in the descriptive register — lowercase words joined by dashes
// — like every other platform-defined identifier that is not a halting
// condition's short code.
type InsertionRefusal string

const (
	// InsertionAdmitted reports that an insertion point was found. It is the
	// zero value, so a caller that forgets to check reads a refusal rather than
	// an admission.
	InsertionAdmitted InsertionRefusal = ""

	// InsertionRefusedEmptyContainerPath reports a container path that names
	// nothing: one with no segments at all, or one carrying a segment that is
	// the empty string.
	InsertionRefusedEmptyContainerPath InsertionRefusal = "empty-container-path"

	// InsertionRefusedNotAContainer reports a container path that runs through
	// a value the document carries as a scalar. Nothing can be appended inside
	// a scalar, and replacing it would be a rewrite rather than an insertion.
	InsertionRefusedNotAContainer InsertionRefusal = "path-is-not-a-container"

	// InsertionRefusedFlowCollection reports a container path that runs through
	// — or ends at — a collection the author wrote in flow style. Appending a
	// block entry to one requires rewriting the bytes the author already wrote,
	// which is an edit rather than a position, and this reports positions.
	InsertionRefusedFlowCollection InsertionRefusal = "flow-collection"

	// InsertionRefusedSequenceIndex reports a container path whose segment
	// addresses a block sequence with something that is not one of its indices.
	// A sequence's entries are addressed by position, so a name reaches nothing
	// in one and no entry can be created to carry that name.
	InsertionRefusedSequenceIndex InsertionRefusal = "sequence-index-out-of-range"
)

// Reason renders the refusal as a clause a surface embeds in whatever refusal
// channel it has. It is the single home of the wording, so two surfaces
// declining the same request cannot describe it differently.
//
// An admitted insertion has no reason and renders as the empty string.
func (r InsertionRefusal) Reason() string {
	switch r {
	case InsertionRefusedEmptyContainerPath:
		return "the container path names nothing: it has no segments, or one of its segments is empty"
	case InsertionRefusedNotAContainer:
		return "the container path runs through a value the document carries as a scalar, and nothing " +
			"can be appended inside a scalar"
	case InsertionRefusedFlowCollection:
		return "the container path reaches a collection written in flow style, and appending an entry " +
			"to one rewrites bytes the author already wrote rather than adding a line"
	case InsertionRefusedSequenceIndex:
		return "the container path addresses a block sequence with something that is not one of its " +
			"indices, and a sequence's entries are addressed by position"
	default:
		return ""
	}
}

// Insertion is where a new entry under a named container belongs in an authored
// document, expressed so a caller applies it without parsing the document
// itself.
//
// # Applying it
//
// The text goes in at Position, which is zero-width: nothing already in the
// document is replaced or shifted. Where PrecededByLineBreak is set — which is
// every document that carries anything — the text begins with a line break,
// because the position sits at the end of the line the new entry follows. That
// is the one shape that is in range whether or not the document ends in a
// newline, and it disturbs no byte the author wrote.
//
// After the break the caller writes one line per MissingContainerKeys entry,
// outermost first, each being the key followed by a colon, and each indented
// IndentStep further than the one before it. The first of them is indented
// EntryIndent minus IndentStep times the number of missing keys. Then the entry
// itself, indented EntryIndent. Where MissingContainerKeys is empty the
// container is already there and the entry is the only line written.
//
// # What it does not decide
//
// EntryShape reports what the document carries, and reports EntryShapeUnknown
// for a container that is not there yet — so the entry's own text is the
// caller's, always. This locates; it does not compose, and it does not write.
type Insertion struct {
	// Position is the zero-width point the text goes in at.
	//
	// Its Character is measured in the units of the LineIndex the insertion was
	// computed against, which is the encoding the caller negotiated.
	Position Position `json:"position"`

	// PrecededByLineBreak reports that the text written at Position must begin
	// with a line break, because the position sits at the end of an existing
	// line. It is false only where the document carries nothing to sit after.
	PrecededByLineBreak bool `json:"precededByLineBreak"`

	// MissingContainerKeys are the container path's segments the document does
	// not carry, outermost first. Each has to be written as a key of its own
	// before the entry can go inside the innermost one. It is empty where the
	// whole container path is already there.
	MissingContainerKeys []string `json:"missingContainerKeys"`

	// EntryIndent is the column the entry's own first line starts at, counted
	// in spaces from the start of the line.
	EntryIndent int `json:"entryIndent"`

	// IndentStep is how much further each nesting level is indented.
	IndentStep int `json:"indentStep"`

	// IndentStepMeasured reports whether IndentStep was measured from this
	// document's own nesting. Where it is false the document exhibits no
	// nesting to measure and the step is this package's fallback, so a caller
	// that would rather impose its own knows it is free to.
	IndentStepMeasured bool `json:"indentStepMeasured"`

	// EntryShape is the shape the container carries its entries in, or
	// EntryShapeUnknown where the document does not say.
	EntryShape EntryShape `json:"entryShape"`
}

// InsertionPointIn reports where a new entry under the named container belongs
// in doc, and refuses where no such point can be derived.
//
// # It reads structure and never meaning
//
// The container is named by the caller as a structural breadcrumb from the
// document root — the same breadcrumb vocabulary [Context.Path] carries — and
// every part of the answer is read from the bytes the author wrote: which
// segments the document already has, the column its entries start at, the step
// its own nesting uses, and the last line its innermost block reaches. Nothing
// here consults what a key is called, what type a value carries, or what any of
// it might mean. A caller that knows an attachment belongs under some particular
// key supplies that key; this answers where, in THIS document, such a key's
// entries go.
//
// # Where the answer sits
//
// At the end of the last line the container's block reaches, so a new entry
// follows everything already in that block, in the order the author wrote. A
// blank line inside the block does not end it and a blank line after it is not
// counted, so the entry lands against the content rather than after trailing
// whitespace. A line at a shallower indentation ends the block, which is what
// keeps a trailing comment at the margin below the entry rather than above it, and
// a document separator at the margin ends it too, so the entry cannot land in a
// document the request did not name.
//
// idx must be built over the same bytes doc was parsed from; the position's
// character offsets are reported in that index's encoding.
func InsertionPointIn(doc *yaml.Node, idx LineIndex, container []string) (Insertion, InsertionRefusal) {
	if len(container) == 0 {
		return Insertion{}, InsertionRefusedEmptyContainerPath
	}
	for _, seg := range container {
		if seg == "" {
			return Insertion{}, InsertionRefusedEmptyContainerPath
		}
	}

	root := doc
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	step, measured := measureIndentStep(root)

	node := root
	// keyLine and keyIndent describe the key whose value node is. keyLine is
	// negative at the document root, where there is no such key, and that is
	// what tells the two apart below: a value the document does not carry sits
	// one step inside its key, while a document root that carries nothing sits
	// at the margin.
	keyLine, keyIndent := -1, 0
	var missing []string

walk:
	for i, seg := range container {
		if empty(node) {
			missing = container[i:]
			break walk
		}
		if node.Style&yaml.FlowStyle != 0 {
			return Insertion{}, InsertionRefusedFlowCollection
		}
		switch node.Kind {
		case yaml.MappingNode:
			key, val := mappingEntry(node, seg)
			if val == nil {
				missing = container[i:]
				break walk
			}
			keyLine, keyIndent = key.Line-1, key.Column-1
			node = val
		case yaml.SequenceNode:
			item := sequenceEntry(node, seg)
			if item == nil {
				return Insertion{}, InsertionRefusedSequenceIndex
			}
			// A sequence entry has no key. What stands in for one is the dash
			// that introduces it, whose column is the sequence's own.
			keyLine, keyIndent = item.Line-1, node.Column-1
			node = item
		default:
			return Insertion{}, InsertionRefusedNotAContainer
		}
	}

	// anchorIndent is the column of the FIRST line the caller writes — the
	// outermost missing key where there is one, and otherwise the entry itself.
	// scanFrom is where the block scan starts, and floor a line the block is
	// known to reach whatever that scan finds.
	var anchorIndent, scanFrom int
	floor := -1
	dashRequired := false
	shape := EntryShapeUnknown

	switch {
	case empty(node):
		if keyLine < 0 {
			// The document carries no root node at all: an empty file, or one
			// holding only comments. Everything goes at the margin, after
			// whatever is already there.
			anchorIndent, scanFrom = 0, 0
		} else {
			// The key is there and carries nothing. Its entries sit one step
			// inside it, and the key's own line is the block whatever follows.
			anchorIndent, scanFrom, floor = keyIndent+step, keyLine+1, keyLine
		}
	case node.Style&yaml.FlowStyle != 0:
		return Insertion{}, InsertionRefusedFlowCollection
	case node.Kind == yaml.MappingNode:
		anchorIndent, scanFrom = node.Column-1, node.Line-1
		if len(missing) == 0 {
			shape = EntryShapeMapping
		}
	case node.Kind == yaml.SequenceNode:
		// A block sequence's own column is the column of its dashes, which YAML
		// permits to equal its key's. So a line at exactly that column belongs
		// to the sequence only when it opens with a dash — without that, the
		// key FOLLOWING the sequence would read as part of it and the entry
		// would land after the wrong block.
		anchorIndent, scanFrom, dashRequired = node.Column-1, node.Line-1, true
		if len(missing) == 0 {
			shape = EntryShapeSequence
		}
	default:
		return Insertion{}, InsertionRefusedNotAContainer
	}

	last := idx.blockEnd(scanFrom, anchorIndent, dashRequired)
	if last < floor {
		last = floor
	}

	ins := Insertion{
		MissingContainerKeys: append([]string(nil), missing...),
		EntryIndent:          anchorIndent + len(missing)*step,
		IndentStep:           step,
		IndentStepMeasured:   measured,
		EntryShape:           shape,
	}
	if last >= 0 {
		ins.Position = idx.endOfLine(last)
		ins.PrecededByLineBreak = true
	}
	return ins, InsertionAdmitted
}

// empty reports a node the document does not carry a value at: a missing node,
// the zero node a document with no content parses to, or an explicit null.
//
// The explicit null matters as much as the missing node: a key written with
// nothing after it parses to a null scalar carrying the position just past the
// colon, and a caller asking where that key's entries go is asking about a
// container that is declared and empty rather than one that is absent.
func empty(n *yaml.Node) bool {
	if n == nil || n.Kind == 0 {
		return true
	}
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// mappingEntry returns the key and value nodes of the pair m carries under the
// given key, or two nils when m carries no such pair.
func mappingEntry(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// sequenceEntry returns the item s carries at the index the segment spells, or
// nil when the segment is not a decimal index inside the sequence.
//
// The spelling is the one the structural breadcrumb uses for a sequence
// position, so a path reaching into a sequence is written the same way here and
// wherever else a breadcrumb is read.
func sequenceEntry(s *yaml.Node, segment string) *yaml.Node {
	idx, err := strconv.Atoi(segment)
	if err != nil || idx < 0 || idx >= len(s.Content) {
		return nil
	}
	return s.Content[idx]
}

// measureIndentStep reports the nesting step the document uses, and whether it
// was measured at all.
//
// The step is read off the pairs the document already carries: a key whose value
// is a block collection declares, by where that collection starts, how much
// further this author indents one level. The most frequent positive step wins,
// and the smallest of the equally frequent breaks a tie, so the answer is a
// function of the document rather than of the order a walk happened to reach it
// in.
//
// A step of zero is not counted, and that is not a rounding convenience: YAML
// lets a block sequence's dashes sit at its key's own column, so a document
// using that style would otherwise report that one nesting level costs nothing.
// A document that yields no positive step at all is reported as unmeasured, with
// the fallback step, rather than as a document whose step is zero.
func measureIndentStep(root *yaml.Node) (int, bool) {
	counts := map[int]int{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode && n.Style&yaml.FlowStyle == 0 {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, val := n.Content[i], n.Content[i+1]
				if val == nil || val.Style&yaml.FlowStyle != 0 {
					continue
				}
				if val.Kind != yaml.MappingNode && val.Kind != yaml.SequenceNode {
					continue
				}
				if step := val.Column - key.Column; step > 0 {
					counts[step]++
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(root)

	best, bestCount := 0, 0
	for step, c := range counts {
		if c > bestCount || (c == bestCount && step < best) {
			best, bestCount = step, c
		}
	}
	if bestCount == 0 {
		return defaultIndentStep, false
	}
	return best, true
}

// blockEnd returns the index of the last line belonging to a block whose entries
// start at column indent, scanning forward from line from. It returns from-1
// when nothing from there on belongs to the block.
//
// A blank line neither extends the block nor ends it, so a block separated by
// blank lines is walked whole while a trailing blank line is not counted as
// content. A line indented further belongs. A line indented less ends the walk,
// and so does a document separator at the margin, which is what stops the answer
// pointing into a document the request did not name.
//
// dashRequiredAtIndent governs the one ambiguous case: a line at exactly indent.
// For a mapping it is another entry of the same block and belongs. For a
// sequence whose dashes sit at its key's column it belongs only if it opens with
// a dash, because the key that follows the sequence sits at that same column.
//
// Indentation is counted in spaces alone. YAML admits no tab in an indentation
// position, so a line a tab could have shifted is a line the parse would already
// have refused.
func (idx LineIndex) blockEnd(from, indent int, dashRequiredAtIndent bool) int {
	last := from - 1
	for line := from; line < idx.LineCount(); line++ {
		raw, err := idx.lineBytes(line)
		if err != nil {
			break
		}
		text := string(raw)
		trimmed := strings.TrimLeft(text, " ")
		if trimmed == "" {
			continue
		}
		li := len(text) - len(trimmed)
		if li == 0 && (strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "...")) {
			break
		}
		switch {
		case li > indent:
			last = line
		case li < indent:
			return last
		case dashRequiredAtIndent && !opensASequenceEntry(trimmed):
			return last
		default:
			last = line
		}
	}
	return last
}

// opensASequenceEntry reports whether a line's content opens a block sequence
// entry: a dash alone, or a dash followed by the space or tab that separates it
// from the entry's value.
func opensASequenceEntry(trimmed string) bool {
	return trimmed == "-" || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "-\t")
}

// endOfLine returns the position one past the last character of a line, with the
// line terminator excluded. A line outside the document reports its own start.
func (idx LineIndex) endOfLine(line int) Position {
	b, err := idx.lineBytes(line)
	if err != nil {
		return Position{Line: line}
	}
	return Position{Line: line, Character: idx.ByteToLSPChar(line, len(b))}
}
