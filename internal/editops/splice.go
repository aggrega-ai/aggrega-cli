// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package editops

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/yamlsplice"
)

// splice adds the request's entry under the named container and returns the
// resulting bytes, the container segments it had to create, and whether anything
// changed.
//
// The document is parsed only to locate: which of the container path it already
// carries, the column its entries sit at, the nesting step its own indentation
// exhibits, and the line its innermost block ends at. The entry is rendered and
// inserted at the line level, so every byte outside the inserted lines carries
// through untouched.
//
// An entry already present carrying what the request asked for reports no
// change, so a repeated edit is idempotent and rewrites nothing. One present
// carrying something else is refused: this adds entries and does not replace
// them.
func splice(data []byte, container []string, opts Options) (out []byte, created []string, changed bool, err error) {
	var root yaml.Node
	if uerr := yaml.Unmarshal(data, &root); uerr != nil {
		return nil, nil, false, fmt.Errorf("%w: %s", errFileUnparseable, opts.File)
	}

	value, verr := entryValue(opts)
	if verr != nil {
		return nil, nil, false, verr
	}

	deepest, keyLine, keyIndent, missing, derr := descend(&root, container)
	if derr != nil {
		return nil, nil, false, derr
	}

	// 🔴 A container the kind derives may be created; the NODE the request names
	// may not. The distinction is what keeps this an edit to an existing node
	// rather than a way to conjure one: a request naming a component that is not
	// there would otherwise have the whole chain written under it, producing a
	// node carrying only the entry and none of what its own schema requires. The
	// document's validation does refuse that, so nothing would reach disk either
	// way — but it refuses it as a missing required property on a node the caller
	// never meant to create, which sends the reader to fix the wrong thing.
	if firstMissing := len(container) - len(missing); len(missing) > 0 && firstMissing < nodeDepth(opts) {
		return nil, nil, false, fmt.Errorf("%w: %q is not there",
			errNodeAbsent, strings.Join(container[:firstMissing+1], "."))
	}

	// An entry already present short-circuits before anything is rendered, so
	// the idempotent case cannot depend on the rendering agreeing with itself.
	if len(missing) == 0 {
		if existing, _ := childEntry(deepest, opts.Name); existing != nil {
			same, cerr := entryMatches(deepest, opts.Name, value)
			if cerr != nil {
				return nil, nil, false, cerr
			}
			if same {
				return data, nil, false, nil
			}
			return nil, nil, false, fmt.Errorf("%w: %q under %s",
				errEntryPresentDiffers, opts.Name, strings.Join(container, "."))
		}
	}

	lines := strings.Split(string(data), "\n")
	step := measureStep(&root)

	// The block is composed inwards: the entry first, then one wrapping mapping
	// per container segment the document does not carry, so a missing chain is
	// rendered as the nested block it would be if the author had typed it.
	block := mappingNode(opts.Name, value)
	for i := len(missing) - 1; i >= 0; i-- {
		block = mappingNode(missing[i], block)
	}

	var insertAt, indent int
	switch {
	case len(missing) > 0:
		// The outermost missing key sits one step inside the deepest container
		// the document does carry.
		if deepest == nil || isEmptyNode(deepest) {
			// The key is there and carries nothing, so its entries start one
			// step inside the key's own column and the key's line is the block.
			indent = keyIndent + step
			insertAt = keyLine
		} else {
			indent = childIndentOf(deepest, keyIndent, step)
			insertAt = yamlsplice.EndOfMappingContent(lines, keyLine, deepest, indent, nil)
		}
	default:
		indent = childIndentOf(deepest, keyIndent, step)
		insertAt = yamlsplice.EndOfMappingContent(lines, keyLine, deepest, indent, nil)
	}

	// The rendered block is the entry alone once the wrapping is unwound by
	// RenderBlock, which takes the outermost key and its value.
	outerKey := opts.Name
	var outerValue *yaml.Node = value
	if len(missing) > 0 {
		outerKey = missing[0]
		outerValue = block.Content[1]
	}
	rendered, rerr := yamlsplice.RenderBlock(outerKey, outerValue, indent, step)
	if rerr != nil {
		return nil, nil, false, fmt.Errorf("render the entry for %q: %w", opts.Name, rerr)
	}

	result := spliceInsert(lines, insertAt, rendered)
	return []byte(strings.Join(result, "\n")), append([]string(nil), missing...), true, nil
}

// descend walks the container path and reports the deepest node the document
// carries, that node's key line and column, and the segments still missing.
//
// It reads structure and never meaning: no segment is interpreted, no key's name
// consulted for what may go inside it. A path running through a scalar or a
// sequence, or through a collection written in flow style, is refused rather than
// guessed at — a flow collection because adding a block entry to one would
// rewrite bytes the author wrote, which is an edit rather than an insertion.
func descend(root *yaml.Node, container []string) (node *yaml.Node, keyLine, keyIndent int, missing []string, err error) {
	n := root
	if n != nil && n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil, 0, 0, nil, fmt.Errorf("%w: the document carries nothing", errNodeAbsent)
		}
		n = n.Content[0]
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, 0, 0, nil, errContainerNotAMapping
	}
	// The document root has no key of its own. Its own line and column stand in,
	// which is what makes the arithmetic below uniform across the root and every
	// nested container.
	keyLine, keyIndent = n.Line, n.Column-1

	for i, seg := range container {
		if isEmptyNode(n) {
			return n, keyLine, keyIndent, container[i:], nil
		}
		if n.Style&yaml.FlowStyle != 0 {
			return nil, 0, 0, nil, fmt.Errorf("%w: at %q", errContainerFlowStyle, strings.Join(container[:i], "."))
		}
		if n.Kind != yaml.MappingNode {
			return nil, 0, 0, nil, fmt.Errorf("%w: at %q", errContainerNotAMapping, strings.Join(container[:i], "."))
		}
		key, val := childEntry(n, seg)
		if val == nil {
			return n, keyLine, keyIndent, container[i:], nil
		}
		keyLine, keyIndent = key.Line, key.Column-1
		n = val
	}

	if n != nil && !isEmptyNode(n) {
		if n.Style&yaml.FlowStyle != 0 {
			return nil, 0, 0, nil, fmt.Errorf("%w: at %q", errContainerFlowStyle, strings.Join(container, "."))
		}
		if n.Kind != yaml.MappingNode {
			return nil, 0, 0, nil, fmt.Errorf("%w: at %q", errContainerNotAMapping, strings.Join(container, "."))
		}
	}
	return n, keyLine, keyIndent, nil, nil
}

// entryMatches reports whether the entry already under key carries what the
// request would write.
//
// The comparison is on the decoded values rather than on the bytes, so an entry
// an author spelled differently — quoted where this would not quote, or with its
// keys in another order — still counts as present. A byte comparison would
// report such an entry as differing and refuse an edit that had nothing to do.
func entryMatches(parent *yaml.Node, key string, want *yaml.Node) (bool, error) {
	_, existing := childEntry(parent, key)
	if existing == nil {
		return false, nil
	}
	var have, target any
	if err := existing.Decode(&have); err != nil {
		return false, nil
	}
	if err := want.Decode(&target); err != nil {
		return false, fmt.Errorf("compare the entry for %q: %w", key, err)
	}
	return equalAny(have, target), nil
}

// equalAny compares two decoded YAML values structurally.
//
// It is written out rather than delegated to reflect.DeepEqual because the two
// sides are decoded from different shapes — one from the document's own bytes,
// one from a node tree built here — and a mapping decodes to a map whose key
// type depends on which. Comparing them as decoded generic values keeps the
// question "is this entry already what we would write" answerable without
// depending on that.
func equalAny(a, b any) bool {
	switch at := a.(type) {
	case map[string]any:
		bt, ok := b.(map[string]any)
		if !ok || len(at) != len(bt) {
			return false
		}
		for k, av := range at {
			bv, present := bt[k]
			if !present || !equalAny(av, bv) {
				return false
			}
		}
		return true
	case []any:
		bt, ok := b.([]any)
		if !ok || len(at) != len(bt) {
			return false
		}
		for i := range at {
			if !equalAny(at[i], bt[i]) {
				return false
			}
		}
		return true
	default:
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
}

// childIndentOf reports the column a container's entries start at: the column
// the document's own first child sits at where there is one, and one step inside
// the container's key otherwise.
func childIndentOf(n *yaml.Node, keyIndent, step int) int {
	if n != nil && n.Kind == yaml.MappingNode && len(n.Content) > 0 {
		return n.Content[0].Column - 1
	}
	return keyIndent + step
}

// measureStep reports the nesting step the document uses.
//
// The step is read off the pairs the document already carries: a key whose value
// is a block collection declares, by where that collection starts, how much
// further this author indents one level. The most frequent positive step wins,
// and the smallest of the equally frequent breaks a tie, so the answer is a
// function of the document rather than of the order a walk reached it in.
//
// A step of zero is not counted. YAML lets a block sequence's dashes sit at
// their key's own column, so a document in that style would otherwise report
// that one nesting level costs nothing — and an entry written at a zero step
// would not parse back as a child.
//
// A document exhibiting no nesting at all reports the conventional step, because
// the alternative — reporting zero — collapses the arithmetic every caller
// performs with it.
func measureStep(root *yaml.Node) int {
	const conventional = 2
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
				if s := val.Column - key.Column; s > 0 {
					counts[s]++
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(root)

	best, bestCount := 0, 0
	for s, c := range counts {
		if c > bestCount || (c == bestCount && s < best) {
			best, bestCount = s, c
		}
	}
	if bestCount == 0 {
		return conventional
	}
	return best
}

// isEmptyNode reports a node the document carries no value at: a missing node,
// the zero node a document with no content parses to, or an explicit null.
//
// The explicit null matters as much as the missing node: a key written with
// nothing after it parses to a null scalar, and a caller adding an entry under
// that key is addressing a container that is declared and empty rather than one
// that is absent.
func isEmptyNode(n *yaml.Node) bool {
	if n == nil || n.Kind == 0 {
		return true
	}
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// childEntry returns the key and value nodes for key within a mapping node, or
// two nils when the node is not a mapping or carries no such key.
func childEntry(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// spliceInsert returns lines with block inserted at the 0-indexed position at.
func spliceInsert(lines []string, at int, block []string) []string {
	if at < 0 {
		at = 0
	}
	if at > len(lines) {
		at = len(lines)
	}
	result := make([]string, 0, len(lines)+len(block))
	result = append(result, lines[:at]...)
	result = append(result, block...)
	result = append(result, lines[at:]...)
	return result
}
