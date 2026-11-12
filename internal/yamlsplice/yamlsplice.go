// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package yamlsplice carries the low-level line mechanics for a surgical,
// byte-faithful edit of a single key-and-value block inside a YAML document:
// locating a block's line range under a parent mapping, rendering a
// {key: value} block at a chosen indent, and finding the insertion point at
// the end of a mapping's content. The document bytes are parsed to a node tree
// only to LOCATE lines and columns; the block is rendered and spliced at the
// line level so every other byte of the file — indentation, comments, folded
// and literal scalars, blank lines, and the trailing-newline shape — carries
// through verbatim.
//
// The package holds only the shared mechanics. The add/replace/remove
// orchestration, blank-line-separator conventions, and empty-block handling
// differ per surface and stay with each caller, which composes these
// primitives into its own splice.
package yamlsplice

import (
	"bytes"
	"strings"

	"go.yaml.in/yaml/v3"
)

// indentOf counts the leading spaces of a line. YAML forbids tabs for
// indentation, so a space count is the block's indent depth.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// BlockRange returns the line range [start, end) 0-indexed for the block whose
// key sits on 1-indexed line keyLine at keyIndent spaces of indentation. The
// block runs from the key line through the last following line indented deeper
// than keyIndent; interleaved blank lines (as occur inside a literal or folded
// scalar) are spanned, but blank lines trailing the last deeper-indented line
// are excluded so a following separator is not swallowed.
func BlockRange(lines []string, keyLine, keyIndent int) (int, int) {
	start := keyLine - 1
	lastContent := start
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if indentOf(lines[i]) > keyIndent {
			lastContent = i
			continue
		}
		break
	}
	return start, lastContent + 1
}

// RenderBlock serializes a single key and its value as YAML lines indented to
// sit under a parent: the key at keyIndent spaces and its value at the file's
// own indent step. The rendering is deterministic — a yaml.Encoder emits the
// {key: value} subtree, and each line is shifted right by keyIndent spaces.
func RenderBlock(key string, value any, keyIndent, step int) ([]string, error) {
	var valNode yaml.Node
	if err := valNode.Encode(value); err != nil {
		return nil, err
	}
	m := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&valNode,
		},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(step)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	prefix := strings.Repeat(" ", keyIndent)
	raw := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	out := make([]string, len(raw))
	for i, line := range raw {
		if line == "" {
			out[i] = ""
			continue
		}
		out[i] = prefix + line
	}
	return out, nil
}

// EndOfMappingContent returns the line index (0-indexed, exclusive) immediately
// after the last content line of mapping's children — the insertion point for a
// newly added child block. A child key for which skip returns true is not
// counted (skip may be nil to count every child). When mapping has no counted
// child it falls back to the line just after mappingKeyLine (the 1-indexed line
// of the mapping's own key).
func EndOfMappingContent(lines []string, mappingKeyLine int, mapping *yaml.Node, childIndent int, skip func(string) bool) int {
	end := 0
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i].Value
		if skip != nil && skip(key) {
			continue
		}
		_, e := BlockRange(lines, mapping.Content[i].Line, childIndent)
		if e > end {
			end = e
		}
	}
	if end == 0 {
		end = mappingKeyLine
	}
	return end
}
