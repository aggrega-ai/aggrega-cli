// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfntemplate

import (
	"fmt"
	"strings"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	"github.com/awslabs/goformation/v7/intrinsics"
	"go.yaml.in/yaml/v3"
)

// intrinsicsMu serializes every call into the intrinsics package. Its YAML
// processing registers its tag handlers into a package-level map on every call and
// reads that map while decoding, so two concurrent calls race on it and the runtime
// aborts the process. [Normalize] is the package's only call into it, so holding
// the lock there makes every caller safe, whatever goroutine it runs on.
var intrinsicsMu sync.Mutex

// maxCopiedNodes bounds the nodes the reader copies when it writes an alias out in
// full. An alias chain can name content exponentially larger than the text; past
// the bound the remaining aliases stay as written and the YAML reader's own
// refusal of excessive aliasing speaks.
const maxCopiedNodes = 100000

// Normalize reads one template, YAML with short forms or JSON, as CloudFormation
// reads it and returns sorted-key long-form JSON with a trailing newline. No
// intrinsic is evaluated: a Ref or GetAtt survives as its long-form pointer object,
// so the conversion is deterministic across runs.
//
// A quoted "!Ref X" stays a string. An unquoted date stays the text the template
// wrote. An alias to a short form reads as that short form, and !Condition,
// !Transform and !GetStackOutput read as {"Condition": …}, {"Fn::Transform": …}
// and {"Fn::GetStackOutput": …}. An anchor aliased inside its own content is
// refused, naming the anchor.
//
// A template that needs none of those rewrites reaches the intrinsics reader as
// the bytes it was given.
func Normalize(content []byte) ([]byte, error) {
	intrinsicsMu.Lock()
	defer intrinsicsMu.Unlock()
	prepared, err := prepare(content)
	if err != nil {
		return nil, err
	}
	out, err := intrinsics.ProcessYAML(prepared, &intrinsics.ProcessorOptions{NoProcess: true})
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// prepare returns content with the rewrites the intrinsics reader does not make
// applied to its node tree, or content itself when none applies. Bytes that do not
// parse come back unchanged, so the parse failure surfaces from the intrinsics
// reader.
func prepare(content []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return content, nil
	}
	if anchor, found := anchorcycle.Find(&root); found {
		return nil, fmt.Errorf("invalid YAML template: %s", anchorcycle.Explain(anchor))
	}
	p := &pass{tagged: map[*yaml.Node]bool{}}
	p.rewrite(&root)
	if !p.changed {
		return content, nil
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return content, nil
	}
	return out, nil
}

// pass is one walk over a parsed template that rewrites what the intrinsics reader
// reads differently from CloudFormation. It follows alias links only to learn
// whether an alias target carries a short form, never to walk it, and the caller
// has established that no anchor is aliased inside its own content.
type pass struct {
	changed bool
	copied  int
	// tagged memoizes whether a node's content holds a short form.
	tagged map[*yaml.Node]bool
}

// rewrite applies the rewrites to the subtree under n: an unquoted timestamp
// becomes a quoted string, an alias whose target holds a short form is replaced by
// a copy of the target, and a !Condition, !Transform or !GetStackOutput node
// becomes the long-form mapping.
func (p *pass) rewrite(n *yaml.Node) {
	if n.Kind == yaml.AliasNode {
		return
	}
	for i, c := range n.Content {
		if c.Kind == yaml.AliasNode && c.Alias != nil && p.holdsShortForm(c.Alias) && p.copied < maxCopiedNodes {
			n.Content[i] = p.copyOf(c.Alias)
		}
	}
	if n.Kind == yaml.ScalarNode && n.Style == 0 && n.ShortTag() == "!!timestamp" {
		n.Tag = "!!str"
		n.Style = yaml.DoubleQuotedStyle
		p.changed = true
	}
	for _, c := range n.Content {
		p.rewrite(c)
	}
	p.expandLongFormTag(n)
}

// longFormTags are the short forms the intrinsics reader leaves as plain text or as
// the bare argument, each with the long-form key CloudFormation reads it as.
var longFormTags = map[string]string{
	"!Condition":      "Condition",
	"!Transform":      "Fn::Transform",
	"!GetStackOutput": "Fn::GetStackOutput",
}

// expandLongFormTag turns n, when it carries one of [longFormTags], into a mapping
// holding the long-form key and n's content, in place.
func (p *pass) expandLongFormTag(n *yaml.Node) {
	key, ok := longFormTags[n.Tag]
	if !ok {
		return
	}
	arg := *n
	arg.Anchor = ""
	switch arg.Kind {
	case yaml.SequenceNode:
		arg.Tag = "!!seq"
	case yaml.MappingNode:
		arg.Tag = "!!map"
	default:
		arg.Tag = "!!str"
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, Line: n.Line, Column: n.Column}
	*n = yaml.Node{
		Kind: yaml.MappingNode, Tag: "!!map", Anchor: n.Anchor,
		Line: n.Line, Column: n.Column, Content: []*yaml.Node{keyNode, &arg},
	}
	p.changed = true
}

// holdsShortForm reports whether n or anything it contains, through aliases, is
// tagged with a short form: one the intrinsics reader reads by tag, or one of
// [longFormTags].
func (p *pass) holdsShortForm(n *yaml.Node) bool {
	if v, done := p.tagged[n]; done {
		return v
	}
	held := false
	if strings.HasPrefix(n.Tag, "!") && !strings.HasPrefix(n.Tag, "!!") {
		_, long := longFormTags[n.Tag]
		held = long || IsShortFormTag(n.Tag[1:])
	}
	if !held && n.Alias != nil {
		held = p.holdsShortForm(n.Alias)
	}
	for _, c := range n.Content {
		if !held {
			held = p.holdsShortForm(c)
		}
	}
	p.tagged[n] = held
	return held
}

// copyOf returns a copy of n and of everything it contains, without anchors, and
// counts the nodes it copies.
func (p *pass) copyOf(n *yaml.Node) *yaml.Node {
	p.copied++
	p.changed = true
	out := *n
	out.Anchor = ""
	out.Content = make([]*yaml.Node, len(n.Content))
	for i, c := range n.Content {
		if p.copied >= maxCopiedNodes {
			out.Content[i] = c
			continue
		}
		out.Content[i] = p.copyOf(c)
	}
	return &out
}
