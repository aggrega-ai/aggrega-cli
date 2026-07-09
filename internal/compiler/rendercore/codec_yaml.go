// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

	"go.yaml.in/yaml/v3"
)

// stockYAMLMediaType tags a unit the stock codec carved from YAML output.
const stockYAMLMediaType = "application/yaml"

// StockYAMLCodec is the core's default codec: the borrow every YAML-shaped
// provider uses unless it overrides with one that knows its own format. It
// carves a provider's emitted output into discrete YAML documents (the
// parser-driven multi-document split) and reassembles them surgically at
// node-span granularity — a patched scalar or sub-tree edits only its own
// byte-range, so comments, anchors (`&`/`*`), merge keys (`<<:`), and scalar
// styles in the same document survive verbatim. It never re-serialises a
// whole document: the round-trip is confined to the exact byte-spans the
// patch ops address.
//
// It implements [frp.Codec]. The indent width is carried for the rare op
// that must synthesise new YAML text (an add of a structured value), so the
// inserted text matches the renderer's configured indentation.
type StockYAMLCodec struct {
	indent int
}

// newStockYAMLCodec returns the stock codec at the configured indent width.
func newStockYAMLCodec(indent int) StockYAMLCodec {
	return StockYAMLCodec{indent: ClampYAMLIndent(indent)}
}

// NewStockYAMLCodec returns the core's stock YAML codec as a reusable
// [frp.Codec] primitive. It is the borrow default every YAML-shaped provider
// uses, and the shared building block a provider codec composes on: a provider
// that carves its own units (a templated format, for instance) delegates the
// structural YAML scaffold to this codec so the node-span surgical machinery —
// comment, anchor, merge-key, and scalar-style preservation — is single-sourced
// rather than re-implemented per provider. The indent width is clamped to the
// renderer's supported range and used only when an op must synthesise new YAML
// text.
func NewStockYAMLCodec(indent int) frp.Codec {
	return newStockYAMLCodec(indent)
}

// Segment splits one emitted file into ordered units, one per YAML document.
// Boundary detection delegates to the same parser-driven Split the renderer
// uses elsewhere, so a `---` inside a block or flow scalar is never mistaken
// for a separator. Each document's body is converted to a JSON tree-view; a
// document whose body cannot be treed becomes an opaque unit (MediaType +
// Bytes, no Tree), still handed to the guest but not a PATCH target.
//
// A file the parser rejects entirely surfaces a wrapped
// ErrPipelinePatchTargetNotStructured, mirroring Split: such a file has no
// addressable units at all.
func (c StockYAMLCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	segs, err := Split(name, body)
	if err != nil {
		return nil, err
	}
	units := make([]frp.Unit, 0, len(segs))
	for _, sg := range segs {
		u := frp.Unit{
			Token:     sg.Token,
			Bytes:     append([]byte(nil), sg.Range(body)...),
			MediaType: stockYAMLMediaType,
		}
		docBody := body[sg.bodyStart:sg.bodyEnd]
		if len(strings.TrimSpace(string(docBody))) > 0 {
			raw, cerr := YAMLBodyToJSON(docBody)
			if cerr == nil {
				u.Tree = raw
			}
			// A body that fails JSON conversion stays opaque (no Tree): it is
			// a SELECT/EMIT target, and a PATCH against it halts loudly.
		}
		units = append(units, u)
	}
	return units, nil
}

// Reassemble rebuilds the file from its segmentation, splicing the patched
// units back surgically. A token absent from patched is re-joined from its
// original byte-range verbatim (via ApplyBack); a patched token has only the
// byte-spans its ops address edited, so every untouched byte — comments,
// anchors, merge keys, sibling members, scalar styles — survives.
//
// The codec edits by op kind over the document body, never a whole-document
// re-serialise: a replace edits the addressed value's span; an add inserts a
// new member at the established child indent before the mapping region's end;
// a remove deletes the member's span. A node class it cannot edit surgically
// is a hard error, not a silent whole-document fallback.
func (c StockYAMLCodec) Reassemble(name string, body []byte, segs []frp.Unit, patched []frp.PatchedUnit) ([]byte, error) {
	if len(patched) == 0 {
		return append([]byte(nil), body...), nil
	}

	// Re-derive the byte-faithful document segmentation (affix + body spans)
	// from the original bytes so the surgical body edit splices back with the
	// leading/trailing affixes preserved verbatim.
	docSegs, err := Split(name, body)
	if err != nil {
		return nil, err
	}
	byToken := make(map[string]Segment, len(docSegs))
	for _, sg := range docSegs {
		byToken[sg.Token] = sg
	}

	patchedBodies := make(map[string][]byte, len(patched))
	for _, pu := range patched {
		if len(pu.Ops) == 0 {
			continue
		}
		sg, ok := byToken[pu.Token]
		if !ok {
			return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: name},
				fmt.Sprintf("stock codec: patched token %q not found in %q", pu.Token, name), nil, nil)
		}
		docBody := body[sg.bodyStart:sg.bodyEnd]
		edited, eerr := editDocumentBody(docBody, pu.Ops, c.indent)
		if eerr != nil {
			return nil, cerrors.Wrap(eerr, cerrors.CatalogError, cerrors.SourceLocation{File: name},
				fmt.Sprintf("stock codec: surgical edit of unit %q", pu.Token))
		}
		patchedBodies[pu.Token] = edited
	}

	return ApplyBack(body, docSegs, patchedBodies), nil
}

// editDocumentBody applies ops to one document body at node-span granularity,
// editing only the byte-spans the ops address and leaving every other byte
// verbatim. Each op is planned against the current body and folded in before
// the next op is planned, so a later op resolves its node positions against
// the already-edited bytes.
func editDocumentBody(body []byte, ops []frp.JSONPatchOp, indent int) ([]byte, error) {
	for _, op := range ops {
		// Re-parse before each op so a prior op's edit (already folded into
		// body) is reflected in the node positions the next op resolves
		// against. Ops within one document are few; re-parsing keeps the
		// offset model exact rather than tracking shifts across heterogeneous
		// op kinds.
		edit, err := planEdit(body, op, indent)
		if err != nil {
			return nil, err
		}
		body = applyByteEdit(body, edit)
	}
	return body, nil
}

// byteEdit replaces body[Start:End) with Replacement. An insertion has
// Start == End.
type byteEdit struct {
	Start       int
	End         int
	Replacement []byte
}

func applyByteEdit(body []byte, e byteEdit) []byte {
	out := make([]byte, 0, len(body)-(e.End-e.Start)+len(e.Replacement))
	out = append(out, body[:e.Start]...)
	out = append(out, e.Replacement...)
	out = append(out, body[e.End:]...)
	return out
}

// planEdit resolves one RFC-6902 op against the document body's node tree and
// returns the byte edit that realises it surgically. It supports the op
// kinds whose byte-span is mechanically derivable from yaml.v3 node
// positions: replace of a scalar value, add of a new mapping member, remove
// of a mapping member. An op whose target node class is not surgically
// editable returns a hard error (no whole-document fallback).
func planEdit(body []byte, op frp.JSONPatchOp, indent int) (byteEdit, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return byteEdit{}, fmt.Errorf("parse document body: %w", err)
	}
	if len(root.Content) == 0 {
		return byteEdit{}, fmt.Errorf("empty document body has no node to edit")
	}
	doc := root.Content[0]
	offs := lineStartOffsets(string(body))
	bo := func(line, col int) int {
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

	tokens, err := parseJSONPointer(op.Path)
	if err != nil {
		return byteEdit{}, err
	}

	switch op.Op {
	case "replace":
		return planReplace(body, doc, tokens, op.Value, offs, bo, indent)
	case "add":
		return planAdd(body, doc, tokens, op.Value, offs, bo, indent)
	case "remove":
		return planRemove(body, doc, tokens, offs, bo)
	default:
		return byteEdit{}, &nonSurgicalOpError{op: op.Op, reason: "op kind not surgically supported by the stock codec"}
	}
}

// nonSurgicalOpError marks an op the stock codec cannot realise as a
// byte-span edit. It is a hard error the caller surfaces; the codec never
// falls back to a whole-document re-serialise.
type nonSurgicalOpError struct {
	op     string
	reason string
}

func (e *nonSurgicalOpError) Error() string {
	return fmt.Sprintf("stock codec: %s (op %q)", e.reason, e.op)
}

// planReplace edits the byte-span of the scalar value the path addresses. The
// target must be a scalar (replacing a mapping or sequence value is not
// surgically supported). A scalar replacement preserves the original scalar's
// style when the new value is a scalar of the same style family; a structured
// replacement (a map or slice) is encoded as compact JSON and spliced inline
// over the scalar's span — the correct shape for a structured value that
// occupies an inline (flow) value position, and a valid-YAML superset.
func planReplace(body []byte, doc *yaml.Node, tokens []string, value any, offs []int, bo func(int, int) int, indent int) (byteEdit, error) {
	_, valNode, parent, err := navigate(doc, tokens)
	if err != nil {
		return byteEdit{}, err
	}
	if valNode.Kind != yaml.ScalarNode {
		return byteEdit{}, &nonSurgicalOpError{op: "replace", reason: "stock codec replaces scalar values only; replacing a " + NodeKindName(valNode.Kind) + " is not surgically supported"}
	}
	// A value whose parent collection is flow-style (a JSON object or array, or
	// any inline `{...}`/`[...]`) sits on a line where the scalar's terminator
	// is a delimiter, not the end of the line. The span scan must know this so
	// it stops at the delimiter instead of over-reaching into the siblings.
	flow := parent != nil && parent.Style&yaml.FlowStyle != 0
	start, end := scalarSpan(body, valNode, offs, bo, flow)
	if !isScalarValue(value) {
		// The replacement is structured (a map or slice). An inline compact-JSON
		// object/array is the correct shape for a value in an inline position;
		// splice it over the scalar's span without breaking across lines.
		repl, jerr := encodeCompactJSON(value)
		if jerr != nil {
			return byteEdit{}, jerr
		}
		return byteEdit{Start: start, End: end, Replacement: repl}, nil
	}
	repl, err := encodeScalarLike(valNode, value, indent)
	if err != nil {
		return byteEdit{}, err
	}
	return byteEdit{Start: start, End: end, Replacement: repl}, nil
}

// planAdd inserts a new mapping member. The parent (all but the last token)
// must be a mapping. For a block mapping the new member is inserted at the
// parent's established child indent, just before the parent region's end, so
// existing members, comments, anchors, and styles are untouched. For a flow
// mapping the new member is spliced in as a JSON-quoted flow member: an empty
// `{}` becomes `{"key": value}`; a non-empty flow mapping gains the member
// after its last existing value, matching the body's layout (a new indented
// line for a multi-line/pretty body, an inline `, ` separator for a single-line
// body). Adding into a sequence is not surgically supported.
func planAdd(body []byte, doc *yaml.Node, tokens []string, value any, offs []int, bo func(int, int) int, indent int) (byteEdit, error) {
	if len(tokens) == 0 {
		return byteEdit{}, &nonSurgicalOpError{op: "add", reason: "whole-document add not surgically supported"}
	}
	parentTokens := tokens[:len(tokens)-1]
	key := tokens[len(tokens)-1]

	parent := doc
	if len(parentTokens) > 0 {
		_, pn, _, err := navigate(doc, parentTokens)
		if err != nil {
			return byteEdit{}, err
		}
		parent = pn
	}
	if parent.Kind != yaml.MappingNode {
		return byteEdit{}, &nonSurgicalOpError{op: "add", reason: "stock codec adds members to mappings only; adding to a " + NodeKindName(parent.Kind) + " is not surgically supported"}
	}

	// If the key already exists this is an upsert; RFC-6902 add replaces it.
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return planReplace(body, doc, tokens, value, offs, bo, indent)
		}
	}

	if parent.Style&yaml.FlowStyle != 0 {
		indentStep := strings.Repeat(" ", ClampYAMLIndent(indent))
		if len(parent.Content) == 0 {
			// Empty flow mapping `{}`: rewrite the braces in place to carry
			// the new member, preserving the flow style. The `{` is at the
			// node's reported position; the `}` is the next non-space byte.
			start := bo(parent.Line, parent.Column)
			end := start
			for end < len(body) && body[end] != '}' {
				end++
			}
			if end < len(body) && body[end] == '}' {
				end++ // include the closing brace
			}
			member, merr := encodeFlowMember(key, value, indentStep)
			if merr != nil {
				return byteEdit{}, merr
			}
			return byteEdit{Start: start, End: end, Replacement: member}, nil
		}
		return planFlowMappingAdd(body, parent, key, value, offs, bo, indentStep)
	}

	childIndent, insertAt, err := mappingInsertPoint(body, parent, offs, bo)
	if err != nil {
		return byteEdit{}, err
	}
	line, lerr := encodeMappingMember(key, value, childIndent, indent)
	if lerr != nil {
		return byteEdit{}, lerr
	}
	return byteEdit{Start: insertAt, End: insertAt, Replacement: line}, nil
}

// planRemove deletes a mapping member's full line-span (its key line through
// the byte just before the next sibling or the parent region end). Removing a
// sequence element or a member of a flow mapping is not surgically supported.
func planRemove(body []byte, doc *yaml.Node, tokens []string, offs []int, bo func(int, int) int) (byteEdit, error) {
	if len(tokens) == 0 {
		return byteEdit{}, &nonSurgicalOpError{op: "remove", reason: "whole-document remove not surgically supported"}
	}
	parentTokens := tokens[:len(tokens)-1]
	key := tokens[len(tokens)-1]
	parent := doc
	if len(parentTokens) > 0 {
		_, pn, _, err := navigate(doc, parentTokens)
		if err != nil {
			return byteEdit{}, err
		}
		parent = pn
	}
	if parent.Kind != yaml.MappingNode || parent.Style&yaml.FlowStyle != 0 {
		return byteEdit{}, &nonSurgicalOpError{op: "remove", reason: "stock codec removes block-mapping members only"}
	}
	idx := -1
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			idx = i
			break
		}
	}
	if idx < 0 {
		return byteEdit{}, fmt.Errorf("cannot remove %q: member does not exist", key)
	}
	keyNode := parent.Content[idx]
	start := bo(keyNode.Line, 1) // remove from the start of the key's line
	// End at the next sibling key's line start, or the parent region end.
	var end int
	if idx+2 < len(parent.Content) {
		end = bo(parent.Content[idx+2].Line, 1)
	} else {
		end = parentRegionEnd(body, parent, offs, bo)
	}
	if end < start {
		end = start
	}
	return byteEdit{Start: start, End: end, Replacement: nil}, nil
}

// navigate walks tokens from the document root, returning the addressed
// value node (and, for a mapping member, its key node). It descends through
// block mappings and sequences; descending through an opaque/alias node, or
// addressing an absent member/index, is an error.
func navigate(doc *yaml.Node, tokens []string) (key *yaml.Node, val *yaml.Node, parent *yaml.Node, err error) {
	cur := doc
	for i, t := range tokens {
		switch cur.Kind {
		case yaml.MappingNode:
			found := false
			for j := 0; j+1 < len(cur.Content); j += 2 {
				if cur.Content[j].Value == t {
					key = cur.Content[j]
					parent = cur
					cur = cur.Content[j+1]
					found = true
					break
				}
			}
			if !found {
				return nil, nil, nil, fmt.Errorf("path segment %q not found", t)
			}
		case yaml.SequenceNode:
			n, cerr := strconv.Atoi(t)
			if cerr != nil || n < 0 || n >= len(cur.Content) {
				return nil, nil, nil, fmt.Errorf("array index %q out of range or invalid", t)
			}
			key = nil
			parent = cur
			cur = cur.Content[n]
		default:
			return nil, nil, nil, &nonSurgicalOpError{op: "navigate", reason: fmt.Sprintf("cannot descend through a %s at segment %q", NodeKindName(cur.Kind), t)}
		}
		_ = i
	}
	return key, cur, parent, nil
}

// scalarSpan returns the [start,end) byte range of a scalar node's source
// text. The start is the node's reported position; the end is derived by
// scanning forward from the start to the end of the value token.
//
// In block context the value runs to the line's trailing comment or the end
// of the line, so an inline `# comment` after the value is preserved and a
// plain scalar may legitimately contain a comma. In flow context — a value
// inside an inline `{...}`/`[...]` collection, which is how JSON-shaped bodies
// parse — the value's real terminator is the first unquoted `,`, `}`, or `]`
// on the line, so the scan stops there rather than running to end-of-line.
// Delimiters inside a quoted string are ignored in both contexts.
func scalarSpan(body []byte, n *yaml.Node, offs []int, bo func(int, int) int, flow bool) (int, int) {
	start := bo(n.Line, n.Column)
	// Determine the end of the value on its line. For a plain/quoted scalar
	// the value occupies from start to either a trailing comment (" #") or
	// the end of the line. For a quoted scalar the closing quote is included
	// because the scan stops at the line terminator or an unquoted comment.
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
			// An unquoted '#' preceded by whitespace starts a line comment.
			if !inSingle && !inDouble && lineEnd > start && (body[lineEnd-1] == ' ' || body[lineEnd-1] == '\t') {
				goto trim
			}
		case ',', '}', ']':
			// A flow collection's members are delimited by these; the first one
			// seen outside a quoted string ends the value. In block context the
			// characters are ordinary scalar content and the scan continues.
			if flow && !inSingle && !inDouble {
				goto trim
			}
		}
		lineEnd++
	}
trim:
	// Trim trailing whitespace between the value and a comment / EOL so the
	// comment's leading spaces are preserved as part of the untouched suffix.
	end := lineEnd
	for end > start && (body[end-1] == ' ' || body[end-1] == '\t') {
		end--
	}
	return start, end
}

// mappingInsertPoint returns the child indent (column-1 spaces) of a block
// mapping's members and the byte offset at which a new member line should be
// inserted (just before the mapping region's end). An empty block mapping
// (no members) is not surgically extensible by the stock codec — its indent
// is undetermined from source — so it is a hard error.
func mappingInsertPoint(body []byte, m *yaml.Node, offs []int, bo func(int, int) int) (childIndent string, insertAt int, err error) {
	if len(m.Content) == 0 {
		return "", 0, &nonSurgicalOpError{op: "add", reason: "adding a first member to an empty block mapping is not surgically supported (no source indent to match)"}
	}
	firstKey := m.Content[0]
	childIndent = strings.Repeat(" ", firstKey.Column-1)
	insertAt = parentRegionEnd(body, m, offs, bo)
	return childIndent, insertAt, nil
}

// parentRegionEnd returns the byte offset at which a block mapping's region
// ends: the start of the line following its last member's full span. The last
// member's value may itself be a nested mapping/sequence/scalar, so the scan
// walks to the deepest last descendant's line and advances past it and any
// trailing lines that belong to that member's block.
func parentRegionEnd(body []byte, m *yaml.Node, offs []int, bo func(int, int) int) int {
	if len(m.Content) == 0 {
		return bo(m.Line, m.Column)
	}
	lastVal := m.Content[len(m.Content)-1]
	deepestLine := deepestLastLine(lastVal)
	// Advance to the start of the line after deepestLine.
	off := bo(deepestLine+1, 1)
	// If deepestLine is the final line with no newline, off clamps to len.
	if off > len(body) {
		off = len(body)
	}
	return off
}

// deepestLastLine returns the largest source line number reachable from n by
// always descending into the last child. yaml.v3 reports a node's start line
// only, so the deepest last descendant's line bounds the node's block.
func deepestLastLine(n *yaml.Node) int {
	line := n.Line
	cur := n
	for len(cur.Content) > 0 {
		cur = cur.Content[len(cur.Content)-1]
		if cur.Line > line {
			line = cur.Line
		}
	}
	return line
}

// encodeScalarLike encodes a scalar replacement value, preserving the original
// scalar node's style when the new value is a string and the original was a
// quoted scalar, so a `'single'` stays single-quoted and a `"double"` stays
// double-quoted. Non-string scalars encode in their natural YAML form. A
// structured replacement (a map or slice) never reaches here — planReplace
// encodes it as inline JSON before calling — so the non-scalar branch is a
// defensive guard, not a reachable path for map/slice values.
func encodeScalarLike(orig *yaml.Node, value any, indent int) ([]byte, error) {
	repl := &yaml.Node{Kind: yaml.ScalarNode}
	if s, ok := value.(string); ok {
		repl.Value = s
		repl.Tag = "!!str"
		repl.Style = orig.Style // preserve plain/single/double/literal/folded
	} else {
		// Encode the value through a round-trip node so numbers/bools/null
		// reach their canonical YAML form.
		var tmp yaml.Node
		b, err := yaml.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(b, &tmp); err != nil {
			return nil, err
		}
		if len(tmp.Content) == 0 || tmp.Content[0].Kind != yaml.ScalarNode {
			return nil, &nonSurgicalOpError{op: "replace", reason: "replacing a scalar with a non-scalar value is not surgically supported"}
		}
		repl = tmp.Content[0]
	}
	out, err := yaml.Marshal(repl)
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(string(out), "\n")), nil
}

// encodeMappingMember encodes a "key: value\n" line for a new block-mapping
// member at the given child indent. A scalar value lands inline; a structured
// value is encoded as an indented block beneath the key.
func encodeMappingMember(key string, value any, childIndent string, indent int) ([]byte, error) {
	keyText := yamlScalar(key)
	switch v := value.(type) {
	case string, bool, int, int64, float64, nil:
		valNode := &yaml.Node{Kind: yaml.ScalarNode}
		b, err := yaml.Marshal(value)
		if err != nil {
			return nil, err
		}
		var tmp yaml.Node
		if err := yaml.Unmarshal(b, &tmp); err == nil && len(tmp.Content) > 0 {
			valNode = tmp.Content[0]
		} else {
			valNode.Value = fmt.Sprintf("%v", v)
		}
		vb, err := yaml.Marshal(valNode)
		if err != nil {
			return nil, err
		}
		valText := strings.TrimRight(string(vb), "\n")
		return []byte(childIndent + keyText + ": " + valText + "\n"), nil
	default:
		// Structured value: encode as a nested block and indent every line to
		// childIndent + one indent step under the key.
		b, err := MarshalYAMLIndent(value, indent)
		if err != nil {
			return nil, err
		}
		step := strings.Repeat(" ", ClampYAMLIndent(indent))
		var sb strings.Builder
		sb.WriteString(childIndent + keyText + ":\n")
		for _, ln := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			sb.WriteString(childIndent + step + ln + "\n")
		}
		return []byte(sb.String()), nil
	}
}

// encodeFlowMember encodes a single-member flow mapping of the form
// `{"key": value}` for an empty-flow-mapping add. The member is a JSON-quoted
// key and a JSON-encoded
// value, so the result is valid JSON as well as valid YAML — a flow mapping
// carved from a JSON body stays JSON after the add. The value is compact
// (single-line), which is the right shape for a one-member inline mapping.
func encodeFlowMember(key string, value any, indentStep string) ([]byte, error) {
	member, err := encodeJSONFlowMember(key, value, "", indentStep)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(member)+2)
	out = append(out, '{')
	out = append(out, member...)
	out = append(out, '}')
	return out, nil
}

// planFlowMappingAdd inserts a new member into a non-empty flow (inline)
// mapping. It locates the mapping's opening `{` from the reported node
// position, brace-matches forward to the matching `}` (tracking quote state and
// nested-collection depth so a delimiter inside a string or nested collection
// is not mistaken for the close), and splices the new member after the last
// existing member's value. The insertion is pure (Start == End): every existing
// byte — sibling members, the closing brace, surrounding whitespace — survives
// verbatim. Layout is preserved: a multi-line (pretty) body gets the member on
// its own line at the members' child indent; a single-line body gets it inline
// after a `, ` separator. The result stays valid JSON and a valid YAML flow
// mapping.
func planFlowMappingAdd(body []byte, parent *yaml.Node, key string, value any, offs []int, bo func(int, int) int, indentStep string) (byteEdit, error) {
	open := bo(parent.Line, parent.Column)
	// The reported position is the `{`; tolerate leading whitespace before it.
	for open < len(body) && body[open] != '{' && isFlowSpace(body[open]) {
		open++
	}
	if open >= len(body) || body[open] != '{' {
		return byteEdit{}, &nonSurgicalOpError{op: "add", reason: "flow mapping opening brace not found at the reported node position"}
	}
	closeAt, err := matchFlowClose(body, open)
	if err != nil {
		return byteEdit{}, err
	}
	firstKey := parent.Content[0]
	firstKeyOff := bo(firstKey.Line, firstKey.Column)
	// Multi-line layout: a newline sits between the `{` and the first key.
	multiline := firstKeyOff > open+1 && bytes.IndexByte(body[open+1:firstKeyOff], '\n') >= 0
	// End of the last existing member's value: scan back from `}` over trailing
	// whitespace and newlines to the last non-whitespace byte.
	lastValueEnd := closeAt
	for lastValueEnd > open+1 && isFlowSpace(body[lastValueEnd-1]) {
		lastValueEnd--
	}
	var repl []byte
	if multiline {
		childIndent := strings.Repeat(" ", firstKey.Column-1)
		frag, ferr := encodeJSONFlowMember(key, value, childIndent, indentStep)
		if ferr != nil {
			return byteEdit{}, ferr
		}
		repl = append(repl, ",\n"...)
		repl = append(repl, childIndent...)
		repl = append(repl, frag...)
	} else {
		frag, ferr := encodeJSONFlowMember(key, value, "", indentStep)
		if ferr != nil {
			return byteEdit{}, ferr
		}
		repl = append(repl, ", "...)
		repl = append(repl, frag...)
	}
	return byteEdit{Start: lastValueEnd, End: lastValueEnd, Replacement: repl}, nil
}

// isFlowSpace reports whether b is whitespace that may pad a flow collection's
// braces, separators, or members.
func isFlowSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// matchFlowClose returns the byte offset of the flow-collection close delimiter
// matching the open delimiter at body[open]. It tracks single- and double-quote
// state (so a delimiter inside a string is ignored, honoring backslash escapes
// inside double quotes) and nested `{`/`[` depth (so only the delimiter that
// balances the opener is returned). An unterminated collection is a hard error.
func matchFlowClose(body []byte, open int) (int, error) {
	depth := 0
	inSingle, inDouble := false, false
	for i := open; i < len(body); i++ {
		ch := body[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				i++ // skip the escaped byte
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, &nonSurgicalOpError{op: "add", reason: "unterminated flow collection; matching close delimiter not found"}
}

// encodeJSONFlowMember encodes a `"key": value` member for a flow (inline,
// JSON-shaped) mapping. The key is a JSON-quoted string and the value is
// JSON-encoded with HTML escaping disabled. A non-empty childIndent
// pretty-prints a structured value using childIndent as each nested line's
// leading indent (the value nests beneath the member's own line in a multi-line
// body); an empty childIndent yields a compact, single-line value. The result
// is valid JSON and a valid YAML flow member; Go sorts map keys, so it is
// byte-identical across runs of identical input.
func encodeJSONFlowMember(key string, value any, childIndent, indentStep string) ([]byte, error) {
	keyText, err := encodeCompactJSON(key)
	if err != nil {
		return nil, err
	}
	var valText []byte
	if childIndent != "" {
		valText, err = jsonEncodeFragment(value, childIndent, indentStep)
	} else {
		valText, err = encodeCompactJSON(value)
	}
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(keyText)+2+len(valText))
	out = append(out, keyText...)
	out = append(out, ':', ' ')
	out = append(out, valText...)
	return out, nil
}

// encodeCompactJSON encodes v as compact (single-line) JSON with HTML escaping
// disabled.
func encodeCompactJSON(v any) ([]byte, error) {
	return jsonEncodeFragment(v, "", "")
}

// jsonEncodeFragment encodes v as JSON with HTML escaping disabled — so `<`,
// `>`, and `&` reach their literal form rather than a `\u` escape — and trims
// the trailing newline the encoder appends. A non-empty prefix or indent
// pretty-prints with prefix as each line's leading indent; otherwise the output
// is compact. Go sorts map keys, so the encoding is deterministic for identical
// input.
func jsonEncodeFragment(v any, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if prefix != "" || indent != "" {
		enc.SetIndent(prefix, indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// isScalarValue reports whether an op value encodes as a single YAML/JSON
// scalar rather than a structured mapping or sequence. It draws the same line
// the member encoders use, so scalar-vs-structured is decided once.
func isScalarValue(value any) bool {
	switch value.(type) {
	case string, bool, int, int64, float64, nil:
		return true
	default:
		return false
	}
}

// parseJSONPointer decodes an RFC-6901 JSON Pointer into its reference
// tokens, unescaping ~1 to "/" and ~0 to "~". An empty pointer yields no
// tokens (the whole document). A pointer that does not start with "/" (and is
// non-empty) is malformed.
func parseJSONPointer(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("invalid JSON pointer %q: must be empty or start with /", p)
	}
	parts := strings.Split(p[1:], "/")
	out := make([]string, len(parts))
	for i, raw := range parts {
		raw = strings.ReplaceAll(raw, "~1", "/")
		raw = strings.ReplaceAll(raw, "~0", "~")
		out[i] = raw
	}
	return out, nil
}
