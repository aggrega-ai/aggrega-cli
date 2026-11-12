// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"bytes"
	"strings"

	"go.yaml.in/yaml/v3"
)

// valuesMappingOutput is the document a Recipe's or FeatureRecipe's values mapping
// renders, read as the values the provider is handed.
//
// A value the mapping writes under a tag of its author's own — `!GetAtt Queue.Arn`
// written bare — is read as the text written, tag included, rather than as the value
// with its tag dropped. The compiler gives such a tag no meaning: it is the target's
// notation, which only the provider reads, and the provider can read it only if it
// arrives. Read the ordinary way, the decoder would keep `Queue.Arn` and lose the
// tag without a word, so the value would reach the provider as text naming nothing,
// while the same reference written inside quotes reaches it whole. Kept as written,
// the two spellings hand the provider the same text.
//
// Only a tag of the author's own is kept: one opening with a single `!` and naming
// something. YAML's own tags (`!!str`, `!!int`, …) and the bare `!` that forces a
// string are the decoder's, and decode as they always have.
type valuesMappingOutput map[string]any

// UnmarshalYAML reads the mapping's document with every value carrying a tag of
// its author's own replaced by the text written.
func (m *valuesMappingOutput) UnmarshalYAML(n *yaml.Node) error {
	keepAuthoredTagsAsText(n)
	var out map[string]any
	if err := n.Decode(&out); err != nil {
		return err
	}
	*m = out
	return nil
}

// keepAuthoredTagsAsText rewrites, in place, every node of the tree carrying a tag of
// its author's own into a string scalar holding the node as written — the tag and
// its value, a sequence or mapping value in flow form on one line — and descends
// into every other node. An alias is left to the decoder, which follows it to the
// anchored node this walk has already reached.
func keepAuthoredTagsAsText(n *yaml.Node) {
	if n == nil {
		return
	}
	if authoredTag(n.Tag) {
		text := authoredText(n)
		*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: text, Line: n.Line, Column: n.Column}
		return
	}
	if n.Kind == yaml.AliasNode {
		return
	}
	for _, c := range n.Content {
		keepAuthoredTagsAsText(c)
	}
}

// authoredTag reports whether a node's tag is one its author wrote rather than one
// YAML defines: a single `!` followed by a name.
func authoredTag(tag string) bool {
	return strings.HasPrefix(tag, "!") && !strings.HasPrefix(tag, "!!") && len(tag) > 1
}

// authoredText renders one tagged node back to the text it was written as, on one
// line: the encoder quotes a scalar wherever its plain form would read differently,
// and writes a sequence or mapping in flow form.
func authoredText(n *yaml.Node) string {
	c := flowCopy(n)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(c); err != nil {
		return n.Tag + " " + n.Value
	}
	_ = enc.Close()
	return strings.TrimSuffix(buf.String(), "\n")
}

// flowCopy copies a node tree with every sequence and mapping in flow style and
// every comment and anchor name dropped, so it encodes on one line as the value
// alone.
func flowCopy(n *yaml.Node) *yaml.Node {
	c := *n
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	c.Anchor = ""
	if c.Kind == yaml.SequenceNode || c.Kind == yaml.MappingNode {
		c.Style |= yaml.FlowStyle
	}
	if len(n.Content) > 0 {
		c.Content = make([]*yaml.Node, len(n.Content))
		for i, child := range n.Content {
			c.Content[i] = flowCopy(child)
		}
	}
	return &c
}
