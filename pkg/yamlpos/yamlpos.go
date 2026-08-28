// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package yamlpos carries LSP-shape Position / Range / Location types
// alongside helpers that map LSP cursor positions to yaml.v3 nodes and
// classify the structural location a cursor sits in.
//
// LSP Position.Character is 0-based UTF-16 code units. yaml.v3 Node
// coordinates are 1-based UTF-8 bytes. LineIndex bridges the two with
// rune-by-rune conversion via unicode/utf8 + unicode/utf16; both
// directions are supported with no ASCII-only approximation.
//
// The package is consumed by the daemon's navigation handlers (Lookup)
// and by the hover + completion handlers (Classify).
package yamlpos

import (
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrPositionOutOfRange is returned when a Position addresses a line
// outside the document or when conversion against an empty LineIndex
// is attempted.
func ErrPositionOutOfRange() error { return errPositionOutOfRange }

var errPositionOutOfRange = cerrors.Register("yamlpos/position-out-of-range", cerrors.Meta{
	ShortCode: "E0229",
	Message:   "position out of range",
})

// Position matches the LSP shape: 0-based line + 0-based character
// offset measured in UTF-16 code units.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range matches the LSP shape.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location matches the LSP shape: file URI (workspace-relative path)
// plus a position range.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// ContextKind categorizes the structural shape a cursor lies in.
// Navigation handlers consume Kind to decide which reference shape
// (if any) applies; completion handlers consume Kind plus Path to
// decide which candidate set to offer.
type ContextKind int

const (
	// ContextKindUnknown is returned when no recognized structural shape
	// matches the cursor's path.
	ContextKindUnknown ContextKind = iota

	// ContextKindMetadataName matches a cursor on the value of
	// metadata.name (any entity kind).
	ContextKindMetadataName

	// ContextKindExtendsValue matches a cursor on spec.extends (the
	// scalar value naming the parent entity URI). Applies to
	// Component, Feature, and any entity with extends.
	ContextKindExtendsValue

	// ContextKindComponentsType matches a cursor on a Component URI at
	// the suffix "components/<name>/type", after stripping a leading
	// "spec" and any number of "subsystems/<key>" pairs.
	ContextKindComponentsType

	// ContextKindFeaturesType matches a cursor on a Feature URI at
	// either "components/<comp>/features/<feat>/type" (Feature
	// attached to a Component) or "features/<feat>/type" (Feature
	// attached directly to a Subsystem; reachable only when at least
	// one "subsystems/<key>" pair was stripped).
	ContextKindFeaturesType

	// ContextKindRecipeImplements matches a cursor on spec.implements
	// (the scalar value naming the Component URI a Recipe or
	// FeatureRecipe implements).
	ContextKindRecipeImplements

	// ContextKindSchemaProperty matches a cursor anywhere under
	// spec.schema.properties.<field>. Consumed by completion +
	// hover handlers; navigation does not act on it.
	ContextKindSchemaProperty

	// ContextKindUsesTarget matches a cursor on the value of a uses-
	// target reference inside an Aggregate. Three YAML shapes resolve
	// to this Kind:
	//
	//   - Verbose alias-map: uses.<socket>.<alias>.target: <name>
	//   - Simple list: uses.<socket>.<idx>: <name> (index is decimal)
	//   - Verbose bare-string shorthand: uses.<socket>.<alias>: <name>
	//     (alias is non-numeric AND not "target"; cursor Side == Value)
	//
	// The target value is a LOCAL Component name within the same
	// Aggregate (not a catalog URI). Resolution stays intra-document.
	ContextKindUsesTarget

	// ContextKindManifestRecipeReference matches a cursor on the VALUE
	// side of a Manifest mapping pair under spec.mappings — a Recipe
	// URI reference. Four Manifest mapping paths classify to this
	// Kind (components vs features × type vs name). Key-side cursor on
	// the same mappings is NOT classified at this Kind (the key carries
	// a Component / Feature URI with different resolution semantics).
	ContextKindManifestRecipeReference

	// ContextKindReleaseAggregatePath matches a cursor on the value of
	// spec.aggregate inside a Release. The value is a workspace-
	// relative path (without .yaml suffix) — not a catalog URI.
	ContextKindReleaseAggregatePath

	// ContextKindReleaseManifestPath matches a cursor on the value of
	// spec.manifest inside a Release. Same path-reference semantic as
	// ContextKindReleaseAggregatePath.
	ContextKindReleaseManifestPath
)

// ContextSide reports whether the cursor landed on a mapping key
// scalar or a mapping value scalar (or, for leaves outside a mapping
// pair, the structurally meaningful side, defaulting to Value).
//
// The distinction matters when a single YAML breadcrumb path can be
// reached from either side — Manifest mapping pairs are the canonical
// case where the key holds one URI shape and the value holds another,
// and the classifier must dispatch on Side to disambiguate.
type ContextSide int

const (
	// ContextSideUnknown is returned when the cursor did not land on a
	// scalar inside a mapping pair (e.g., cursor in whitespace, or on
	// a sequence element).
	ContextSideUnknown ContextSide = iota

	// ContextSideKey marks the cursor as landing on a mapping key
	// scalar.
	ContextSideKey

	// ContextSideValue marks the cursor as landing on a mapping value
	// scalar, OR on a leaf scalar reachable through a sequence index
	// (where there is no key/value pair — the leaf IS the value).
	ContextSideValue
)

// Context describes what the cursor is pointing at.
//
// Path is the YAML structural breadcrumb from the document root to the
// node at the cursor; each element is a mapping key or a sequence index
// rendered as a decimal string.
//
// Kind is the categorical classification of Path (see ContextKind).
//
// Value is the raw scalar text of the node at the cursor, when the
// cursor resolves to a scalar; empty otherwise. For navigation, Value
// IS the referenced URI when Kind is ExtendsValue, ComponentsType,
// FeaturesType, or RecipeImplements.
type Context struct {
	Path  []string
	Kind  ContextKind
	Value string
	// Side reports whether the cursor's leaf scalar is a mapping key
	// or value (or unknown, for non-mapping leaves). Set by descend
	// when it walks into a child node; consumed by classifyPath to
	// dispatch shapes that can be reached from either side.
	Side ContextSide
}

// PositionEncoding names one of the three LSP 3.17 position
// encodings. UTF-16 is the protocol default; UTF-8 and UTF-32 are
// negotiated via general.positionEncodings at initialize time.
type PositionEncoding string

const (
	// PositionEncodingUTF8 reports positions in UTF-8 code units —
	// equivalent to UTF-8 byte offsets within a line. Native to
	// yaml.v3's storage; zero per-rune conversion cost.
	PositionEncodingUTF8 PositionEncoding = "utf-8"

	// PositionEncodingUTF16 reports positions in UTF-16 code units.
	// LSP 3.17 default. Requires per-rune decoding (utf16.RuneLen
	// returns 1 for BMP, 2 for supplementary plane).
	PositionEncodingUTF16 PositionEncoding = "utf-16"

	// PositionEncodingUTF32 reports positions in UTF-32 code units —
	// equivalent to Unicode code points (one unit per rune).
	PositionEncodingUTF32 PositionEncoding = "utf-32"
)

// LineIndex provides position-encoding ↔ UTF-8-byte conversion for a
// single document's raw bytes. Constructed once per document via
// BuildLineIndex (UTF-16 default for backward compatibility) or
// BuildLineIndexWithEncoding, and reused across Lookup, Classify, and
// Location emission. The index handles LF, CRLF, and lone-CR line
// endings.
type LineIndex struct {
	lineStarts []int
	content    []byte
	encoding   PositionEncoding
}

// BuildLineIndex constructs a LineIndex with the default UTF-16
// encoding (LSP 3.17 default; matches existing callers that predate
// position-encoding negotiation).
func BuildLineIndex(content []byte) LineIndex {
	return BuildLineIndexWithEncoding(content, PositionEncodingUTF16)
}

// BuildLineIndexWithEncoding constructs a LineIndex pinned to the
// supplied position encoding. Used by the daemon after the
// aggrega/initialize handshake selects an encoding.
//
// lineStarts has one entry per line. lineStarts[i] is the byte offset
// of the first byte of line i; lineStarts[0] is always 0.
func BuildLineIndexWithEncoding(content []byte, encoding PositionEncoding) LineIndex {
	idx := LineIndex{content: content, lineStarts: []int{0}, encoding: encoding}
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case '\n':
			idx.lineStarts = append(idx.lineStarts, i+1)
		case '\r':
			if i+1 < len(content) && content[i+1] == '\n' {
				idx.lineStarts = append(idx.lineStarts, i+2)
				i++
			} else {
				idx.lineStarts = append(idx.lineStarts, i+1)
			}
		}
	}
	return idx
}

// LineCount returns the number of lines in the indexed content. An
// empty document has line count 1 (one empty line).
func (idx LineIndex) LineCount() int {
	return len(idx.lineStarts)
}

// Encoding returns the position encoding the index was built with.
// Empty string is treated as UTF-16 for backward compatibility with
// zero-value LineIndex values constructed outside BuildLineIndex.
func (idx LineIndex) Encoding() PositionEncoding {
	if idx.encoding == "" {
		return PositionEncodingUTF16
	}
	return idx.encoding
}

// runeUnits returns the per-encoding unit cost of a rune:
//
//   - UTF-8: utf8.RuneLen(r) (1..4 bytes per rune)
//   - UTF-16: utf16.RuneLen(r) (1 for BMP, 2 for supplementary plane)
//   - UTF-32: 1 (one code point per rune)
//
// Used by both the byte/char converters and ScalarEnd's multi-line
// walk to dispatch per the index's bound encoding.
func (idx LineIndex) runeUnits(r rune) int {
	switch idx.Encoding() {
	case PositionEncodingUTF8:
		return utf8.RuneLen(r)
	case PositionEncodingUTF32:
		return 1
	default:
		return utf16.RuneLen(r)
	}
}

// lineBytes returns the bytes of line line (0-based), excluding the
// trailing line terminator. Returns ErrPositionOutOfRange when line is
// outside the document.
func (idx LineIndex) lineBytes(line int) ([]byte, error) {
	if line < 0 || line >= len(idx.lineStarts) {
		return nil, ErrPositionOutOfRange()
	}
	start := idx.lineStarts[line]
	var end int
	if line+1 < len(idx.lineStarts) {
		end = idx.lineStarts[line+1]
		for end > start && (idx.content[end-1] == '\n' || idx.content[end-1] == '\r') {
			end--
		}
	} else {
		end = len(idx.content)
	}
	return idx.content[start:end], nil
}

// LSPCharToByte converts an LSP character offset within line (0-based
// line) to a 0-based byte offset within that line's bytes (line
// terminator excluded). The character offset is interpreted per the
// index's bound encoding (UTF-8 / UTF-16 / UTF-32).
//
// When char exceeds the number of code units on the line, the byte
// offset is clamped to the line's byte length — matching the LSP
// convention that an end-of-line position addresses one past the last
// character. When char splits a UTF-16 surrogate pair (lands inside a
// supplementary-plane rune's UTF-16 encoding), the byte offset snaps
// to the byte position after the full rune.
func (idx LineIndex) LSPCharToByte(line, char int) (int, error) {
	bytes, err := idx.lineBytes(line)
	if err != nil {
		return 0, err
	}
	if char <= 0 {
		return 0, nil
	}
	// UTF-8: char offset == byte offset (zero conversion cost).
	if idx.Encoding() == PositionEncodingUTF8 {
		if char > len(bytes) {
			return len(bytes), nil
		}
		return char, nil
	}
	var units int
	var pos int
	for pos < len(bytes) {
		r, size := utf8.DecodeRune(bytes[pos:])
		units += idx.runeUnits(r)
		pos += size
		if units >= char {
			return pos, nil
		}
	}
	return len(bytes), nil
}

// ByteToLSPChar inverts LSPCharToByte: converts a 0-based byte offset
// within line (0-based line) to an LSP character offset interpreted
// per the index's bound encoding. byteOffset is clamped to the
// line's byte length.
func (idx LineIndex) ByteToLSPChar(line, byteOffset int) int {
	bytes, err := idx.lineBytes(line)
	if err != nil {
		return 0
	}
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > len(bytes) {
		byteOffset = len(bytes)
	}
	// UTF-8: byte offset == char offset.
	if idx.Encoding() == PositionEncodingUTF8 {
		return byteOffset
	}
	var units int
	var pos int
	for pos < byteOffset {
		r, size := utf8.DecodeRune(bytes[pos:])
		units += idx.runeUnits(r)
		pos += size
	}
	return units
}

// PositionToNodeCoords converts an LSP Position (0-based line,
// 0-based UTF-16 char) to yaml.v3 coordinates (1-based line, 1-based
// UTF-8 byte column).
func (idx LineIndex) PositionToNodeCoords(pos Position) (line, col int, err error) {
	byteOff, err := idx.LSPCharToByte(pos.Line, pos.Character)
	if err != nil {
		return 0, 0, err
	}
	return pos.Line + 1, byteOff + 1, nil
}

// NodeToPosition converts a yaml.Node's start coordinates (1-based
// line, 1-based byte column) to an LSP Position (0-based line,
// 0-based UTF-16 char). A nil node returns the zero Position.
func (idx LineIndex) NodeToPosition(n *yaml.Node) Position {
	if n == nil {
		return Position{}
	}
	line := n.Line - 1
	if line < 0 {
		line = 0
	}
	col := n.Column - 1
	if col < 0 {
		col = 0
	}
	return Position{Line: line, Character: idx.ByteToLSPChar(line, col)}
}

// ScalarEnd returns the end position of the scalar value. The
// position is computed against yaml.v3's normalized Value (folded
// scalars' content folded into a single logical string; indentation
// stripped from block scalars; quoted-content unwrapped via the +=2
// adjustment for surrounding quote bytes). For block scalars (literal
// `|` or folded `>`), the returned position reflects the normalized
// content's structure, NOT the raw source file's indentation or line
// breaks. Callers that need raw-source-range semantics — typically
// for IDE hover ranges spanning multi-line authoring — must compute
// the position themselves by walking the source bytes between the
// scalar's start node and the next sibling's start.
//
// For inline scalars (the dominant case for reference targets:
// URIs, names, type values), the returned position points to the
// source file's true scalar end.
//
// Multi-line literal scalars (n.Value containing '\n') advance Line
// per newline encountered and reset Character to 0; single-line
// scalars (including folded `>` blocks whose content has no embedded
// newlines after folding) stay on the start line.
//
// Per-rune unit cost is dispatched through the index's encoding
// (UTF-8 / UTF-16 / UTF-32). Quoted-style scalars add 2 to Character
// regardless of encoding — ASCII quote characters (`"` and `'`) are
// 1 code unit in every encoding.
func (idx LineIndex) ScalarEnd(n *yaml.Node) Position {
	start := idx.NodeToPosition(n)
	if n == nil {
		return start
	}
	line := start.Line
	char := start.Character
	for _, r := range n.Value {
		if r == '\n' {
			line++
			char = 0
			continue
		}
		char += idx.runeUnits(r)
	}
	if n.Style == yaml.DoubleQuotedStyle || n.Style == yaml.SingleQuotedStyle {
		char += 2
	}
	return Position{Line: line, Character: char}
}

// Lookup returns the deepest yaml.Node containing the supplied
// Position. Returns nil with no error when the position lies outside
// any node (whitespace, comments, document margins). Returns
// ErrPositionOutOfRange when the position addresses a line outside
// the document.
func Lookup(doc *yaml.Node, idx LineIndex, pos Position) (*yaml.Node, error) {
	yamlLine, yamlCol, err := idx.PositionToNodeCoords(pos)
	if err != nil {
		return nil, err
	}
	root := doc
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	n, _, _ := descend(root, yamlLine, yamlCol, nil)
	return n, nil
}

// Classify returns the Context for the cursor's structural location.
// When no node contains the cursor, returns a Context with empty Path
// and ContextKindUnknown — never an error (the LSP convention is
// "no result" rather than "error").
func Classify(doc *yaml.Node, idx LineIndex, pos Position) (Context, error) {
	yamlLine, yamlCol, err := idx.PositionToNodeCoords(pos)
	if err != nil {
		return Context{}, err
	}
	root := doc
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	node, path, side := descend(root, yamlLine, yamlCol, nil)
	ctx := Context{Path: path, Side: side}
	if node != nil && node.Kind == yaml.ScalarNode {
		ctx.Value = node.Value
	}
	ctx.Kind = classifyContext(path, side)
	return ctx, nil
}

// descend walks the YAML subtree rooted at n looking for the deepest
// node containing the cursor (line, col) in yaml.v3 1-based coords.
// The walker tracks a breadcrumb path of mapping keys and sequence
// indices; when the cursor lands on a mapping key, the key's text is
// the final path element. When it lands on a mapping value, the
// containing key's text is appended and the walker descends into the
// value subtree. The third return value carries the structural side
// of the leaf — Key when the cursor landed on a mapping key scalar,
// Value when on a mapping value scalar or a sequence-indexed leaf,
// Unknown when no scalar matched.
func descend(n *yaml.Node, line, col int, path []string) (*yaml.Node, []string, ContextSide) {
	if n == nil {
		return nil, path, ContextSideUnknown
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			valNode := n.Content[i+1]
			if nodeContains(keyNode, line, col) {
				return keyNode, appendPath(path, keyNode.Value), ContextSideKey
			}
			if containsRecursive(valNode, line, col) {
				childPath := appendPath(path, keyNode.Value)
				// When the cursor's eventual leaf is the value scalar
				// itself, the recursive descend lands on a ScalarNode and
				// returns ContextSideUnknown; overwrite to Value for that
				// case so callers can distinguish key-side vs value-side
				// leaves on the same Path.
				node, p, side := descend(valNode, line, col, childPath)
				if side == ContextSideUnknown && valNode.Kind == yaml.ScalarNode && nodeContains(valNode, line, col) {
					side = ContextSideValue
				}
				return node, p, side
			}
		}
		return nil, path, ContextSideUnknown
	case yaml.SequenceNode:
		for i, child := range n.Content {
			if containsRecursive(child, line, col) {
				childPath := appendPath(path, strconv.Itoa(i))
				node, p, side := descend(child, line, col, childPath)
				if side == ContextSideUnknown && child.Kind == yaml.ScalarNode && nodeContains(child, line, col) {
					side = ContextSideValue
				}
				return node, p, side
			}
		}
		return nil, path, ContextSideUnknown
	case yaml.ScalarNode:
		if nodeContains(n, line, col) {
			return n, path, ContextSideUnknown
		}
		return nil, path, ContextSideUnknown
	default:
		return nil, path, ContextSideUnknown
	}
}

func appendPath(path []string, elem string) []string {
	out := make([]string, len(path)+1)
	copy(out, path)
	out[len(path)] = elem
	return out
}

// nodeContains reports whether (line, col) (1-based yaml.v3 coords)
// falls within the scalar span of n. Used for leaf decisions only;
// container coverage is computed via containsRecursive.
func nodeContains(n *yaml.Node, line, col int) bool {
	if n == nil {
		return false
	}
	if n.Kind != yaml.ScalarNode {
		return false
	}
	if line != n.Line {
		return false
	}
	startCol := n.Column
	endCol := startCol + len(n.Value)
	if n.Style == yaml.DoubleQuotedStyle || n.Style == yaml.SingleQuotedStyle {
		endCol += 2
	}
	return col >= startCol && col <= endCol
}

// containsRecursive reports whether (line, col) falls anywhere inside
// the subtree rooted at n. For scalar nodes this reduces to
// nodeContains; for mappings and sequences it recurses to find any
// child whose span covers the cursor.
func containsRecursive(n *yaml.Node, line, col int) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode {
		return nodeContains(n, line, col)
	}
	for _, child := range n.Content {
		if containsRecursive(child, line, col) {
			return true
		}
	}
	return false
}

// ClassifyPath inspects the structural breadcrumb and returns the
// matching ContextKind. The classifier strips a leading "spec" and
// then zero or more "subsystems"/"<key>" pairs greedily before
// suffix-matching the recognized shapes.
//
// The bare "features/<feat>/type" suffix requires the strip to have
// consumed at least one subsystem-pair — otherwise it would match
// a non-existent "spec.features.<feat>.type" path at the top of an
// AggregateSpec. The strip-count guard returns ContextKindUnknown
// for that shape.
//
// Exposed as a pure function so external callers — the daemon's
// reference-shape detector in particular — share one canonical
// implementation. A new ContextKind added here must also be considered
// by every caller that switches on the result.
//
// ClassifyPath does not see ContextSide; for shapes that require side
// dispatch (Manifest mapping pairs, verbose bare-string shorthand
// uses-target), classifyContext is the side-aware wrapper. Path-only
// callers consume the Path-derivable shapes only.
func ClassifyPath(path []string) ContextKind {
	return classifyContext(path, ContextSideValue)
}

// classifyContext is the internal side-aware classifier. ClassifyPath
// is the path-only public surface; Classify drives this directly with
// the side computed by descend.
func classifyContext(path []string, side ContextSide) ContextKind {
	if len(path) == 2 && path[0] == "metadata" && path[1] == "name" {
		return ContextKindMetadataName
	}
	if len(path) == 0 || path[0] != "spec" {
		return ContextKindUnknown
	}
	rest := path[1:]

	if len(rest) == 1 && rest[0] == "extends" {
		return ContextKindExtendsValue
	}
	if len(rest) == 1 && rest[0] == "implements" {
		return ContextKindRecipeImplements
	}
	if len(rest) == 1 && rest[0] == "aggregate" {
		return ContextKindReleaseAggregatePath
	}
	if len(rest) == 1 && rest[0] == "manifest" {
		return ContextKindReleaseManifestPath
	}
	if len(rest) >= 3 && rest[0] == "schema" && rest[1] == "properties" {
		return ContextKindSchemaProperty
	}

	if kind := classifyManifestMapping(rest, side); kind != ContextKindUnknown {
		return kind
	}

	stripped := 0
	for len(rest) >= 2 && rest[0] == "subsystems" {
		rest = rest[2:]
		stripped++
	}

	if len(rest) == 3 && rest[0] == "components" && rest[2] == "type" {
		return ContextKindComponentsType
	}
	if len(rest) == 5 && rest[0] == "components" && rest[2] == "features" && rest[4] == "type" {
		return ContextKindFeaturesType
	}
	if len(rest) == 3 && rest[0] == "features" && rest[2] == "type" && stripped >= 1 {
		return ContextKindFeaturesType
	}

	if kind := classifyUsesTarget(rest, side); kind != ContextKindUnknown {
		return kind
	}

	return ContextKindUnknown
}

// classifyManifestMapping recognizes the four Manifest mapping value-side
// suffixes — components/features × type/name. All four return
// ContextKindManifestRecipeReference because the cursor lands on a Recipe URI.
//
// The exact-length match is deliberate rather than a minimum: `components` and
// `features` are the whole key surface under `mappings:`, so a deeper path is
// not a mapping value at all and must not be offered recipe completions.
//
// rest is the path after the leading "spec" has been stripped. The
// match fires only when side == ContextSideValue.
func classifyManifestMapping(rest []string, side ContextSide) ContextKind {
	if side != ContextSideValue {
		return ContextKindUnknown
	}
	if len(rest) < 4 || rest[0] != "mappings" {
		return ContextKindUnknown
	}
	r := rest[1:]
	// r = [components|features, type|name, <key>]
	if len(r) == 3 && isCompOrFeat(r[0]) && isTypeOrName(r[1]) {
		return ContextKindManifestRecipeReference
	}
	return ContextKindUnknown
}

// classifyUsesTarget recognizes the three uses-target shapes after
// stripping subsystem nesting prefixes. Returns ContextKindUsesTarget
// when a match is found and the cursor is on the value side.
//
// rest is the path after the leading "spec" has been stripped.
func classifyUsesTarget(rest []string, side ContextSide) ContextKind {
	stripped := 0
	for len(rest) >= 2 && rest[0] == "subsystems" {
		rest = rest[2:]
		stripped++
	}

	// Match prefixes ending at the "uses" boundary, then the
	// per-shape suffix.
	matchUsesSuffix := func(usesIdx int) ContextKind {
		// uses[usesIdx+1] = <socket>, uses[usesIdx+2..] = shape suffix
		after := rest[usesIdx+2:]
		// Verbose alias-map: [<alias>, "target"]
		if len(after) == 2 && after[1] == "target" {
			return ContextKindUsesTarget
		}
		// Simple list: [<index>] (decimal-integer-only)
		if len(after) == 1 && isNumericSegment(after[0]) {
			return ContextKindUsesTarget
		}
		// Verbose bare-string shorthand: [<alias>] where alias is
		// non-numeric AND not "target", AND cursor is on a value.
		if len(after) == 1 && !isNumericSegment(after[0]) && after[0] != "target" && side == ContextSideValue {
			return ContextKindUsesTarget
		}
		return ContextKindUnknown
	}

	// Component-level: [components, <comp>, uses, ...]
	if len(rest) >= 4 && rest[0] == "components" && rest[2] == "uses" {
		if kind := matchUsesSuffix(2); kind != ContextKindUnknown {
			return kind
		}
	}
	// Feature-instance under Component:
	// [components, <comp>, features, <feat>, uses, ...]
	if len(rest) >= 6 && rest[0] == "components" && rest[2] == "features" && rest[4] == "uses" {
		if kind := matchUsesSuffix(4); kind != ContextKindUnknown {
			return kind
		}
	}
	// Subsystem-direct Feature: [features, <feat>, uses, ...] (stripped ≥ 1)
	if len(rest) >= 4 && rest[0] == "features" && rest[2] == "uses" && stripped >= 1 {
		if kind := matchUsesSuffix(2); kind != ContextKindUnknown {
			return kind
		}
	}
	return ContextKindUnknown
}

func isCompOrFeat(s string) bool { return s == "components" || s == "features" }
func isTypeOrName(s string) bool { return s == "type" || s == "name" }

// isNumericSegment reports whether s is a non-empty decimal-integer
// string. Used to distinguish simple-list `uses` entries (whose Path
// element is a sequence index) from verbose-shape mapping keys.
func isNumericSegment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
