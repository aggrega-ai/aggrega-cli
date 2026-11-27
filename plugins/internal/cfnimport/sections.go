// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// template is one template the import reads, with its top-level sections,
// each the long form the template reader writes, its members sorted by key.
type template struct {
	// file is the name of the source holding the template.
	file string
	// top is the template's top-level object.
	top obj
	// params, mappings, conditions, resources and outputs are the sections of
	// those names: Mappings and Conditions are held for the values that read
	// them.
	params, mappings, conditions, resources, outputs obj
}

// sections reads the sections of the template h holds.
func sections(h *handed) *template {
	t := &template{file: h.name(), top: h.top}
	t.params, _ = objectOf(h.top.get("Parameters"))
	t.mappings, _ = objectOf(h.top.get("Mappings"))
	t.conditions, _ = objectOf(h.top.get("Conditions"))
	t.resources, _ = objectOf(h.top.get("Resources"))
	t.outputs, _ = objectOf(h.top.get("Outputs"))
	return t
}

// consumed are the members of a resource the reader reads into its node's
// fields, or leaves out, rather than its body.
var consumed = []string{"Type", "Condition", "DependsOn", "Metadata"}

// nodes returns the nodes of t's resources, each keyed by its logical ID: its
// type, the name of the condition it is deployed under, the resources it
// depends on, written as one name or a list, the file and JSON pointer it is
// read from, and a body of every other member — its Properties and its
// policies — each value a property of the long form ([prop]). A node's name is
// left unset: a resource's name is one of its Properties, and the core drafts
// a node's name as a property of its own. A resource that is no object, or
// whose Type is not text, is not carried, with a resource-not-carried warning
// naming where it is written.
func (r *reader) nodes(t *template) []*cloudimport.Node {
	var out []*cloudimport.Node
	for _, e := range t.resources {
		at := importer.Pointer("Resources", e.Key)
		o, isObject := objectOf(e.Value)
		typ, typed := schemadraft.JSONText(o.get("Type"))
		if !isObject || !typed || typ == "" {
			d := cloudimport.Report(cloudimport.CodeResourceNotCarried, "the template "+t.file+" writes the resource "+
				e.Key+" at "+at+", which is no object declaring its Type as text, so it is not drafted: write it as "+
				"an object whose Type names its resource type")
			d.SourceRef = t.file + "#" + at
			r.note(d)
			continue
		}
		n := &cloudimport.Node{ID: e.Key, Type: typ, Form: cloudimport.FormRaw, Source: t.file + "#" + at}
		if raw := o.get("Condition"); raw != nil {
			n.Condition = literal(raw)
		}
		n.DependsOn = names(o.get("DependsOn"))
		for _, m := range o {
			if isConsumed(m.Key) {
				continue
			}
			p := prop(m.Value)
			p.Name = m.Key
			n.Body = append(n.Body, p)
		}
		out = append(out, n)
	}
	return out
}

// isConsumed reports whether a resource's member named key is read into its
// node's fields or left out.
func isConsumed(key string) bool {
	for _, c := range consumed {
		if c == key {
			return true
		}
	}
	return false
}

// names returns the names raw writes, as one text or a list of texts, in
// order; an item that is no text names nothing.
func names(raw json.RawMessage) []string {
	if name, ok := schemadraft.JSONText(raw); ok {
		return []string{name}
	}
	items, _ := itemsOf(raw)
	var out []string
	for _, item := range items {
		if name, ok := schemadraft.JSONText(item); ok {
			out = append(out, name)
		}
	}
	return out
}

// prop returns the JSON value raw as a property: an intrinsic function — an
// object whose one member is named Ref, Condition, or Fn:: followed by a
// function's name — as one leaf holding its long form, any other object as its
// members and an array as its items, an empty object or array as its literal,
// and any other value as its literal.
func prop(raw json.RawMessage) cloudimport.Prop {
	switch kind(raw) {
	case 0:
		return cloudimport.Prop{}
	case '{':
		o, _ := objectOf(raw)
		if len(o) == 0 || len(o) == 1 && cfntemplate.IsIntrinsicKey(o[0].Key) {
			return cloudimport.Prop{Value: literal(raw)}
		}
		var p cloudimport.Prop
		for _, e := range o {
			m := prop(e.Value)
			m.Name = e.Key
			p.Props = append(p.Props, m)
		}
		return p
	case '[':
		items, _ := itemsOf(raw)
		if len(items) == 0 {
			return cloudimport.Prop{Value: literal(raw)}
		}
		var p cloudimport.Prop
		for _, item := range items {
			p.Items = append(p.Items, prop(item))
		}
		return p
	}
	return cloudimport.Prop{Value: literal(raw)}
}

// parameters returns the parameters t declares, in name order, with the notes
// naming what of them the draft does not carry: each with its type, its
// description, the values it allows, its bounds and its default. A NoEcho
// parameter is secure and its default is never read, whatever any setting
// says, as the value of a NoEcho parameter is a secret and is never written:
// a note says the draft never writes it. A parameter whose type opens AWS::
// or List<AWS:: names an existing resource of the cloud, and is a pointer. An
// AllowedPattern, which the graph holds no place for, is named in a note.
func (t *template) parameters() ([]cloudimport.Parameter, []string) {
	var (
		out   []cloudimport.Parameter
		notes []string
	)
	for _, e := range t.params {
		o, _ := objectOf(e.Value)
		p := cloudimport.Parameter{Name: e.Key}
		p.Type, _ = schemadraft.JSONText(o.get("Type"))
		p.Description, _ = schemadraft.JSONText(o.get("Description"))
		if allowed, ok := itemsOf(o.get("AllowedValues")); ok {
			for _, a := range allowed {
				p.Allowed = append(p.Allowed, compacted(a))
			}
		}
		p.Min, p.Max = number(o.get("MinValue")), number(o.get("MaxValue"))
		p.MinLength, p.MaxLength = length(o.get("MinLength")), length(o.get("MaxLength"))
		p.Secure = isTrue(o.get("NoEcho"))
		p.Pointer = strings.HasPrefix(p.Type, "AWS::") || strings.HasPrefix(p.Type, "List<AWS::")
		if pattern, ok := schemadraft.JSONText(o.get("AllowedPattern")); ok {
			notes = append(notes, "The template parameter "+draftyaml.Comment(e.Key)+" allows only values matching "+
				"its AllowedPattern, "+draftyaml.Comment(pattern)+", which the draft does not carry.")
		}
		switch raw := o.get("Default"); {
		case raw == nil || isNull(raw):
		case p.Secure:
			notes = append(notes, "The template parameter "+draftyaml.Comment(e.Key)+" is NoEcho, so the draft "+
				"never writes its default.")
		default:
			p.Default = literal(raw)
		}
		out = append(out, p)
	}
	return out, notes
}

// isTrue reports whether raw is true, written as a boolean or as text.
func isTrue(raw json.RawMessage) bool {
	if s, ok := schemadraft.JSONText(raw); ok {
		return strings.EqualFold(strings.TrimSpace(s), "true")
	}
	return string(compacted(raw)) == "true"
}

// number returns the number raw holds, written as a number or as text; nil
// where it holds none.
func number(raw json.RawMessage) *json.Number {
	s := string(compacted(raw))
	if text, ok := schemadraft.JSONText(raw); ok {
		s = strings.TrimSpace(text)
	}
	if s == "" || !(s[0] == '-' || s[0] >= '0' && s[0] <= '9') || !json.Valid([]byte(s)) {
		return nil
	}
	n := json.Number(s)
	return &n
}

// length returns the length raw holds, a whole number written as a number or
// as text; nil where it holds none.
func length(raw json.RawMessage) *int {
	s := string(compacted(raw))
	if text, ok := schemadraft.JSONText(raw); ok {
		s = strings.TrimSpace(text)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// outputsRead returns the outputs t declares, in name order, each with its
// value, and the notes naming the name each is exported under, which the
// draft does not export.
func (t *template) outputsRead() ([]cloudimport.Output, []string) {
	var (
		out   []cloudimport.Output
		notes []string
	)
	for _, e := range t.outputs {
		o, _ := objectOf(e.Value)
		output := cloudimport.Output{Name: e.Key}
		if raw := o.get("Value"); raw != nil {
			output.Value = literal(raw)
		}
		out = append(out, output)
		export, _ := objectOf(o.get("Export"))
		raw := export.get("Name")
		if raw == nil {
			continue
		}
		if name, ok := schemadraft.JSONText(raw); ok {
			notes = append(notes, "The output "+draftyaml.Comment(e.Key)+" is exported as "+draftyaml.Comment(name)+
				", which the draft does not export.")
			continue
		}
		notes = append(notes, "The output "+draftyaml.Comment(e.Key)+" is exported under a name the template "+
			"computes when the stack deploys, which the draft does not export.")
	}
	return out, notes
}

// declared returns the notes naming what t declares at its top level that no
// resource carries, in order: the transforms it names, the keys of its
// Metadata and the names of its Rules.
func (t *template) declared() []string {
	var notes []string
	if transforms := transformNames(t.top.get("Transform")); len(transforms) == 1 {
		notes = append(notes, "The template declares the transform "+transforms[0]+".")
	} else if len(transforms) > 1 {
		notes = append(notes, "The template declares the transforms "+strings.Join(transforms, ", ")+".")
	}
	if raw := t.top.get("Metadata"); raw != nil && !isNull(raw) {
		o, _ := objectOf(raw)
		if len(o) == 0 {
			notes = append(notes, "The template's Metadata is not carried.")
		} else {
			notes = append(notes, "The template's Metadata, which the draft does not carry, holds "+
				draftyaml.CommentList(keys(o))+".")
		}
	}
	if rules, _ := objectOf(t.top.get("Rules")); len(rules) > 0 {
		notes = append(notes, "The template's Rules, which the draft does not carry, check its parameters when the "+
			"stack deploys: "+draftyaml.CommentList(keys(rules))+".")
	}
	return notes
}

// transformNames returns the name of each transform raw declares, written
// for a comment, in order, as [declaredTransforms] reads them; a transform
// named by no text is left out.
func transformNames(raw json.RawMessage) []string {
	var out []string
	for _, tr := range declaredTransforms(raw) {
		if tr.name != "" {
			out = append(out, draftyaml.Comment(tr.name))
		}
	}
	return out
}

// keys returns the key of each member of o, in order.
func keys(o obj) []string {
	out := make([]string, len(o))
	for i, e := range o {
		out[i] = e.Key
	}
	return out
}
