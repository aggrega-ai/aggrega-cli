// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// authoredValueExpressions indexes, per component instance name, the values
// leaves an Aggregate authored as an unquoted template expression, mapped to the
// expression text exactly as it appears in the document.
//
// An unquoted `{{ .parameters.x }}` is well-formed YAML that reads as a flow
// mapping standing in a mapping-key position rather than as a scalar. Reading it
// structurally is what lets an author leave a reference unquoted, but the
// structural read cannot preserve the key's own text, so a value the author
// wrote as an expression arrives with that text gone. Every consumer reading the
// document for structure is unaffected; a consumer that prints an authored value
// back to the reader is not, and this is what it reads instead.
//
// The index is keyed by instance name because a document that resolves at all
// has unique component names: two components sharing a name collide when the
// topology flattens, and that collision is refused before anything renders.
//
// Only a field whose value node carries a non-scalar key is recorded — the exact
// condition the structural read is lossy on. A field authored as an ordinary
// nested mapping has scalar keys throughout and is never consulted here, and a
// document that authors no unquoted expression yields an empty index without any
// value text being read at all.
type authoredValueExpressions map[string]map[string]string

// applyTo returns values with every leaf the document authored as an unquoted
// expression replaced by that expression's text, leaving every other leaf
// untouched. The receiver map is not modified: the topology node it came from is
// shared with the rest of the render, so the substitution is made on a copy.
//
// Replacing the field itself, rather than filtering what the structural read
// left behind, is what makes the rendered line match the quoted spelling: the
// leaf regains both its authored path and its authored text in one step.
func (a authoredValueExpressions) applyTo(instance string, values types.OrderedMap[any]) types.OrderedMap[any] {
	fields, ok := a[instance]
	if !ok || len(fields) == 0 {
		return values
	}
	out := values
	for path := range fields {
		out = replaceLeaf(out, strings.Split(path, "."), fields[path])
	}
	return out
}

// replaceLeaf returns values with the leaf at the dotted segment chain set to
// text, copying each mapping it descends through so no shared map is mutated. A
// chain that does not resolve leaves values unchanged.
func replaceLeaf(values types.OrderedMap[any], segments []string, text string) types.OrderedMap[any] {
	if len(segments) == 0 {
		return values
	}
	head := segments[0]
	current, ok := values.Get(head)
	if !ok {
		return values
	}
	out := types.OrderedMap[any]{}
	for k, v := range values.All() {
		out.Set(k, v)
	}
	if len(segments) == 1 {
		out.Set(head, text)
		return out
	}
	nested, ok := current.(*types.OrderedMap[any])
	if !ok || nested == nil {
		return values
	}
	replaced := replaceLeaf(*nested, segments[1:], text)
	out.Set(head, &replaced)
	return out
}

// readAuthoredValueExpressions walks an Aggregate document's own node tree and
// collects the values leaves it authored as unquoted template expressions.
//
// The walk parses a node tree and nothing more, which is the same tolerant read
// the document already passed to be rendered at all — so it cannot refuse a
// document the renderer accepted. A document it cannot parse yields an empty
// index rather than an error: this supplies display text, so having none is a
// plainer outcome than failing a render that otherwise succeeds.
func readAuthoredValueExpressions(data []byte) authoredValueExpressions {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	root := mappingValue(documentRoot(&doc), "spec")
	if root == nil {
		return nil
	}
	out := authoredValueExpressions{}
	collectComponentExpressions(root, data, out)
	return out
}

// documentRoot unwraps a document node to the node carrying its content.
func documentRoot(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return n.Content[0]
	}
	return n
}

// mappingValue returns the value node a mapping holds under a scalar key, or nil
// when the node is not a mapping or does not carry that key.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// collectComponentExpressions records the authored expressions of every
// component a scope declares, descending through nested subsystems. Both places
// a component may be declared are read: directly under the scope, and inside a
// subsystem the scope names.
func collectComponentExpressions(scope *yaml.Node, data []byte, out authoredValueExpressions) {
	if components := mappingValue(scope, "components"); components != nil && components.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(components.Content); i += 2 {
			name := components.Content[i]
			if name.Kind != yaml.ScalarNode {
				continue
			}
			values := mappingValue(components.Content[i+1], "values")
			if values == nil {
				continue
			}
			fields := map[string]string{}
			collectExpressionFields(values, "", data, fields)
			if len(fields) > 0 {
				out[name.Value] = fields
			}
		}
	}
	subsystems := mappingValue(scope, "subsystems")
	if subsystems == nil || subsystems.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(subsystems.Content); i += 2 {
		collectComponentExpressions(subsystems.Content[i+1], data, out)
	}
}

// collectExpressionFields walks a values mapping and records the dotted path of
// every field the document authored as an unquoted template expression, together
// with that expression's text.
//
// A field qualifies on the structural condition alone: its value is a mapping
// carrying a non-scalar key, which is what an unquoted expression parses as and
// what an ordinary nested mapping never produces. Recovery is additionally
// required to yield text opening as a template expression, so a genuine
// complex-keyed mapping — legal YAML that no template authored — is left to
// render as it does today rather than being reported as an expression.
func collectExpressionFields(values *yaml.Node, prefix string, data []byte, out map[string]string) {
	if values == nil || values.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(values.Content); i += 2 {
		key := values.Content[i]
		if key.Kind != yaml.ScalarNode {
			continue
		}
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}
		value := values.Content[i+1]
		if value.Kind != yaml.MappingNode {
			continue
		}
		if hasNonScalarKey(value) {
			if text, ok := expressionText(data, value); ok {
				out[path] = text
			}
			continue
		}
		collectExpressionFields(value, path, data, out)
	}
}

// hasNonScalarKey reports whether a mapping carries a key that is not a scalar —
// the shape an unquoted template expression takes, and the one shape the
// structural read cannot preserve.
func hasNonScalarKey(n *yaml.Node) bool {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Kind != yaml.ScalarNode {
			return true
		}
	}
	return false
}

// expressionText recovers the source text of a flow mapping standing where an
// author wrote a template expression, by reading the document between the node's
// opening brace and the brace that closes it.
//
// Reading to the closing brace rather than to the end of the line is what keeps
// a trailing comment out of the recovered text. The result is returned only when
// it opens as a template expression, so a mapping that is genuinely complex-keyed
// yields nothing and the caller leaves the field alone.
func expressionText(data []byte, n *yaml.Node) (string, bool) {
	start, ok := byteOffset(data, n.Line, n.Column)
	if !ok || data[start] != '{' {
		return "", false
	}
	depth := 0
	for i := start; i < len(data); i++ {
		switch data[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				text := string(data[start : i+1])
				if !strings.HasPrefix(text, "{{") {
					return "", false
				}
				return text, true
			}
		case '\n':
			// A template expression is written on one line; a flow mapping that
			// runs past the end of it is not one, so recovery stops rather than
			// reporting text spanning lines the author did not write as a value.
			return "", false
		}
	}
	return "", false
}

// byteOffset converts a node's one-based line and column into an index into the
// document bytes.
func byteOffset(data []byte, line, column int) (int, bool) {
	if line < 1 || column < 1 {
		return 0, false
	}
	offset := 0
	for l := 1; l < line; l++ {
		next := bytes.IndexByte(data[offset:], '\n')
		if next < 0 {
			return 0, false
		}
		offset += next + 1
	}
	offset += column - 1
	if offset >= len(data) {
		return 0, false
	}
	return offset, true
}
