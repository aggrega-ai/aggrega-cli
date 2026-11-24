// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Aggregate writes the drafted Aggregate of the import in, of a source in the
// format f read into g, from the nodes Classify returns for it, in their order:
// one instance for each mapped or drafted node, under its Key, of the
// Component it is named for, with its values and the connections it draws.
//
// Comments open it: the sources read and those not read, each output of the
// source with the instance and the provides key carrying it where one does,
// the parameters the draft carries nowhere, and the substrate the source
// creates, which the draft leaves to the operator. Above an instance whose key
// is not its node's ID a comment names that ID, then its Comments. Each
// connection is written under a socket and an alias both named for its
// target's key, its comments above it and its entries delivered through
// draftyaml.Inject.
//
// The values of a mapped instance are those its row's Values place, read off
// its node, or, for an instance a fold of its row makes of a child, off the
// parent folded into it, followed by the value of that child the fold writes
// under values; then the value under values of each child folded into it. The
// values of a drafted instance are those its Recipe reads from values, read
// off its node. A placement's converter turns the value read into the one
// written: string writes its text, integer an integer, unit:<suffix> its text
// followed by the suffix. A literal is written as YAML reading back as the
// literal, each string in it — an object's keys included — written through the
// format's Text where the import's target is f.Target, so the format's own
// Recipes read it as the literal it is; deploy-time text is written as it is
// for its own target, and for any other target is not written and warned
// value-not-carried, as is a value a converter cannot convert. A value read
// from a parameter, a reference or a credential is not written. Every text read
// from the source is escaped through EscapeTemplateText, so it reads back as
// written once the compiler evaluates the Aggregate as a template, and every
// comment line is written through draftyaml.CommentLine.
//
// A parameter is carried where a node the draft writes an instance for, or
// folds into one, reads it at a path the draft carries: any property of a
// drafted node, its name included; a property a mapped instance's row places
// or lists its environment at; the path a fold writes of a folded child. The
// Aggregate is named as importer.DraftName names it. Two runs over one input
// give one byte sequence.
func Aggregate(in aggrega.ImportInput, f Format, g *Graph, classified []Classified) (aggrega.EmittedEntity,
	[]aggrega.Diagnostic) {
	w := &aggregateWriter{in: in, f: f, g: g}
	for i := range classified {
		if classified[i].Node != nil {
			w.out = append(w.out, &classified[i])
		}
	}
	w.cs = newClasses(g, w.out)

	format := in.Format
	if format == "" {
		format = f.Name
	}
	name := importer.DraftName(in)
	var b strings.Builder
	w.opening(&b)
	b.WriteString("apiVersion: " + apiversion.APIVersion + "\n")
	b.WriteString("kind: Aggregate\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  version: 0.1.0\n")
	b.WriteString("  description: " + importer.DraftDescription(format) + "\n")
	b.WriteString("spec:\n")
	written := false
	for _, cl := range w.out {
		if cl.Key == "" || !isInstance(cl) {
			continue
		}
		if !written {
			b.WriteString("  components:\n")
			written = true
		}
		w.instance(&b, cl)
	}
	if !written {
		b.WriteString("  components: {}\n")
	}
	return aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft", Body: b.String()}, w.diags
}

// aggregateWriter is one drafted Aggregate being written.
type aggregateWriter struct {
	in    aggrega.ImportInput
	f     Format
	g     *Graph
	out   []*Classified
	cs    classLookup
	diags []aggrega.Diagnostic
}

// opening writes the comments opening the Aggregate.
func (w *aggregateWriter) opening(b *strings.Builder) {
	if w.g != nil {
		if len(w.g.Read) > 0 {
			b.WriteString(draftyaml.CommentLine("", "Sources read: "+draftyaml.CommentList(w.g.Read)+"."))
		}
		if len(w.g.NotRead) > 0 {
			b.WriteString(draftyaml.CommentLine("", "Not read: "+draftyaml.CommentList(w.g.NotRead)+"."))
		}
		if len(w.g.Outputs) > 0 {
			b.WriteString(draftyaml.CommentLine("", "Outputs of the source:"))
			for _, o := range w.g.Outputs {
				b.WriteString(draftyaml.CommentLine("", "  - "+draftyaml.Comment(o.Name)+": "+w.publishedBy(o.Value)))
			}
		}
	}
	if names := w.notCarried(); len(names) > 0 {
		b.WriteString(draftyaml.CommentLine("", "Parameters the draft carries nowhere: "+draftyaml.CommentList(names)+
			"."))
	}
	var substrate []string
	for _, cl := range w.out {
		if cl.Class == ClassSubstrate && !cl.Node.Existing {
			substrate = append(substrate, draftyaml.Comment(cl.Node.ID)+" ("+draftyaml.Comment(cl.Node.Type)+")")
		}
	}
	if len(substrate) > 0 {
		b.WriteString(draftyaml.CommentLine("", "Substrate the source creates, left to the operator: "+
			strings.Join(substrate, ", ")+"."))
	}
}

// publishedBy says which instance publishes an output of value v, and under
// which provides key: one instance, publishing the attribute v reads.
func (w *aggregateWriter) publishedBy(v Value) string {
	if ref, ok := v.(Ref); ok {
		r := w.cs.reach(ref.Node)
		if len(r.instances) == 1 && !r.child {
			cl := w.cs.byID[r.instances[0]]
			if key, ok := providesKey(cl, ref.Attr); ok {
				return "carried by the instance " + draftyaml.Comment(cl.Key) + ", provides key " +
					draftyaml.Comment(key)
			}
		}
	}
	return "carried by no instance"
}

// notCarried returns the names of the parameters the draft carries nowhere, in
// the order the source declares them.
func (w *aggregateWriter) notCarried() []string {
	if w.g == nil {
		return nil
	}
	carried := map[string]bool{}
	for _, cl := range w.out {
		for _, v := range w.carriedValues(cl) {
			paramsOf(v, carried)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range w.g.Params {
		if p.Name != "" && !carried[p.Name] && !seen[p.Name] {
			seen[p.Name] = true
			out = append(out, p.Name)
		}
	}
	return out
}

// carriedValues returns the values of cl's node the draft carries.
func (w *aggregateWriter) carriedValues(cl *Classified) []Value {
	n := cl.Node
	var out []Value
	at := func(paths ...[]string) {
		for _, l := range leaves(n.Body) {
			for _, path := range paths {
				if overlaps(path, l.path) {
					out = append(out, l.value)
					break
				}
			}
		}
		for _, path := range paths {
			if isName(path) && n.Name != nil {
				out = append(out, n.Name)
				break
			}
		}
	}
	switch {
	case cl.Class == ClassDrafted:
		out = append(out, n.Name)
		for _, l := range leaves(n.Body) {
			out = append(out, l.value)
		}
	case cl.Class == ClassMapped && cl.Row != nil:
		if fold := instancesFold(cl.Row, n.Type); fold != nil {
			at(fold.From)
		} else {
			at(rowPaths(cl.Row)...)
		}
	case cl.Class == ClassFolded && len(w.cs.folding[n.ID]) > 0:
		inst := w.cs.byID[w.cs.folding[n.ID][0]]
		if inst != nil {
			at(rowPaths(inst.Row)...)
		}
	case cl.Class == ClassFolded:
		if fold := childFold(cl.Row, n.Type); fold != nil {
			at(fold.From)
		}
	}
	return out
}

// rowPaths are the paths r carries of its node: each placement's and the
// environment list's.
func rowPaths(r *Row) [][]string {
	if r == nil {
		return nil
	}
	var out [][]string
	for _, ps := range [][]Placement{r.Values, r.Infra, r.Overrides, r.Secrets} {
		for _, p := range ps {
			out = append(out, p.From)
		}
	}
	if r.Env != nil {
		out = append(out, r.Env.Path)
	}
	return out
}

// paramsOf records in names each parameter v reads.
func paramsOf(v Value, names map[string]bool) {
	switch v := v.(type) {
	case Param:
		names[v.Name] = true
	case Compose:
		for _, part := range v.Parts {
			paramsOf(part, names)
		}
	}
}

// isName reports whether path names a node's name, which a reader keeps
// beside its body.
func isName(path []string) bool {
	return len(path) == 1 && strings.EqualFold(path[0], "name")
}

// instance writes the instance of cl.
func (w *aggregateWriter) instance(b *strings.Builder, cl *Classified) {
	if cl.Key != cl.Node.ID {
		b.WriteString(draftyaml.CommentLine("    ", "The resource "+draftyaml.Comment(cl.Node.ID)+"."))
	}
	for _, comment := range cl.Comments {
		b.WriteString(draftyaml.CommentLine("    ", comment))
	}
	b.WriteString("    " + cl.Key + ":\n")
	typ := cl.Name
	if typ == "" {
		typ = draftyaml.String("")
	}
	b.WriteString("      type: " + typ + "\n")
	if values := w.values(cl); len(values) > 0 {
		b.WriteString("      values:\n")
		writeTree(b, "        ", values)
	}
	if len(cl.Uses) > 0 {
		b.WriteString("      uses:\n")
		for _, u := range cl.Uses {
			for _, comment := range u.Comments {
				b.WriteString(draftyaml.CommentLine("        ", comment))
			}
			b.WriteString("        " + u.Target + ":\n")
			b.WriteString("          " + u.Target + ":\n")
			b.WriteString("            target: " + u.Target + "\n")
			b.WriteString(draftyaml.Inject("            ", u.Mechanism, u.Entries))
		}
	}
}

// valueTree is a values key holding either text or members.
type valueTree struct {
	key     string
	text    string
	members []*valueTree
}

// values returns the values of cl's instance, as Aggregate states them.
func (w *aggregateWriter) values(cl *Classified) []*valueTree {
	var tree []*valueTree
	put := func(n *Node, from, key []string, conv Conv) {
		if n == nil || len(key) == 0 {
			return
		}
		p, ok := nodeProp(n, from)
		if !ok {
			return
		}
		site := valueSite{node: n.ID, instance: cl.Key, from: from, key: key}
		if text, ok := w.propText(p, conv, site); ok {
			tree = plant(tree, key, text)
		}
	}
	n := cl.Node
	switch {
	case cl.Class == ClassMapped && cl.Row != nil:
		source := n
		fold := instancesFold(cl.Row, n.Type)
		if fold != nil {
			source = nil
			if parent := w.cs.byID[n.Parent]; parent != nil && parent.Class == ClassFolded {
				source = parent.Node
			}
		}
		for _, p := range cl.Row.Values {
			put(source, p.From, []string{p.Key}, p.Conv)
		}
		if fold != nil {
			put(n, fold.From, valuesKey(fold.Key), "")
		}
		for _, child := range w.out {
			if child.Class != ClassFolded || child.Into != n.ID || len(w.cs.folding[child.Node.ID]) > 0 {
				continue
			}
			if f := childFold(cl.Row, child.Node.Type); f != nil {
				put(child.Node, f.From, valuesKey(f.Key), "")
			}
		}
	case cl.Class == ClassDrafted:
		for _, in := range cl.Inputs {
			if in.Source.Pillar == schemadraft.PillarValues && in.Property != nil {
				put(n, in.Property.Path, []string{in.Source.Key}, "")
			}
		}
	}
	return tree
}

// valuesKey returns the key under values a fold writes at key, nil where key
// is not under values.
func valuesKey(key []string) []string {
	if len(key) < 2 || key[0] != "values" {
		return nil
	}
	return key[1:]
}

// plant adds text under key to tree, unless key, or a key it lies in or
// holds, is set already.
func plant(tree []*valueTree, key []string, text string) []*valueTree {
	for _, t := range tree {
		if t.key != key[0] {
			continue
		}
		if len(key) == 1 || t.members == nil {
			return tree
		}
		t.members = plant(t.members, key[1:], text)
		return tree
	}
	t := &valueTree{key: key[0]}
	if len(key) == 1 {
		t.text = text
	} else {
		t.members = plant(nil, key[1:], text)
	}
	return append(tree, t)
}

// writeTree writes tree at indent.
func writeTree(b *strings.Builder, indent string, tree []*valueTree) {
	for _, t := range tree {
		if t.members == nil {
			b.WriteString(indent + keyText(t.key) + ": " + t.text + "\n")
			continue
		}
		b.WriteString(indent + keyText(t.key) + ":\n")
		writeTree(b, indent+"  ", t.members)
	}
}

// plainKey is a key written as it is.
var plainKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// keyText writes a key of the values, quoted where it is not plain.
func keyText(key string) string {
	if plainKey.MatchString(key) {
		return key
	}
	return draftyaml.String(importer.EscapeTemplateText(key))
}

// nodeProp returns what n writes at path: its name at the path name, else the
// member of its body there, read inside a literal where the path runs into
// one.
func nodeProp(n *Node, path []string) (Prop, bool) {
	if isName(path) && n.Name != nil {
		if _, ok := propAt(n.Body, path); !ok {
			return Prop{Value: n.Name}, true
		}
	}
	p := Prop{Props: n.Body}
	for i, seg := range path {
		if p.Value != nil {
			l, ok := p.Value.(Literal)
			if !ok {
				return Prop{}, false
			}
			inner, ok := descend(l.JSON, path[i:])
			if !ok {
				return Prop{}, false
			}
			return Prop{Value: Literal{JSON: inner}}, true
		}
		next, ok := child(p.Props, p.Items, seg)
		if !ok {
			return Prop{}, false
		}
		p = next
	}
	return p, len(path) > 0
}

// valueSite is where a value written is read and written, for a warning.
type valueSite struct {
	node, instance string
	from, key      []string
}

// notCarriedHere warns that the value at site is not written, and why.
func (w *aggregateWriter) notCarriedHere(site valueSite, why string) {
	w.diags = append(w.diags, Report(CodeValueNotCarried, fmt.Sprintf("%s writes %s, which the instance %s "+
		"takes as values.%s, and %s, so it is not written: write the value in the Aggregate by hand", site.node,
		pathText(site.from), site.instance, pathText(site.key), why)))
}

// propText returns the YAML text the value p is written as, converted by conv,
// and whether it is written.
func (w *aggregateWriter) propText(p Prop, conv Conv, site valueSite) (string, bool) {
	switch {
	case p.Value != nil:
		return w.leafText(p.Value, conv, site)
	case conv != "" && (len(p.Props) > 0 || len(p.Items) > 0):
		w.notCarriedHere(site, "the converter "+string(conv)+" converts a scalar, not an object or a list")
		return "", false
	case len(p.Props) > 0:
		parts := make([]string, 0, len(p.Props))
		for _, m := range p.Props {
			text, ok := w.propText(m, "", site)
			if !ok {
				return "", false
			}
			parts = append(parts, w.stringText(m.Name)+": "+text)
		}
		return "{" + strings.Join(parts, ", ") + "}", true
	case len(p.Items) > 0:
		parts := make([]string, 0, len(p.Items))
		for _, item := range p.Items {
			text, ok := w.propText(item, "", site)
			if !ok {
				return "", false
			}
			parts = append(parts, text)
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	}
	return "", false
}

// leafText returns the YAML text the value v is written as, converted by
// conv, and whether it is written.
func (w *aggregateWriter) leafText(v Value, conv Conv, site valueSite) (string, bool) {
	switch v := v.(type) {
	case Literal:
		if conv == "" {
			text, ok := w.jsonText(v.JSON)
			if !ok {
				w.notCarriedHere(site, "it is not a value JSON writes")
			}
			return text, ok
		}
		scalar, st := literalText(v.JSON)
		if st != stateRead {
			w.notCarriedHere(site, "the converter "+string(conv)+" converts a scalar, and it is none")
			return "", false
		}
		return w.converted(scalar, conv, site)
	case Opaque:
		if v.Target != w.in.Target {
			w.notCarriedHere(site, fmt.Sprintf("it is text the %s target evaluates when the source deploys, and "+
				"the import drafts for %s", v.Target, targetName(w.in.Target)))
			return "", false
		}
		if conv != "" && conv != "string" {
			w.notCarriedHere(site, "the converter "+string(conv)+" cannot convert text evaluated when the source "+
				"deploys")
			return "", false
		}
		return draftyaml.String(importer.EscapeTemplateText(v.Text)), true
	}
	return "", false
}

// targetName names target in a warning.
func targetName(target string) string {
	if target == "" {
		return "no target"
	}
	return "the " + target + " target"
}

// converted returns scalar converted by conv as YAML text, and whether it
// converts.
func (w *aggregateWriter) converted(scalar string, conv Conv, site valueSite) (string, bool) {
	switch {
	case conv == "string":
		return w.stringText(scalar), true
	case conv == "integer":
		if i, err := strconv.ParseInt(strings.TrimSpace(scalar), 10, 64); err == nil {
			return strconv.FormatInt(i, 10), true
		}
		w.notCarriedHere(site, "the converter integer reads no integer in it")
		return "", false
	case strings.HasPrefix(string(conv), "unit:"):
		return w.stringText(scalar + strings.TrimPrefix(string(conv), "unit:")), true
	}
	w.notCarriedHere(site, "the row names the converter "+string(conv)+", which the core does not know")
	return "", false
}

// stringText writes s, a literal string of the source, as the double-quoted
// scalar it is written as: through the format's Text where the import's target
// is the format's own, then escaped so it reads back once evaluated.
func (w *aggregateWriter) stringText(s string) string {
	if w.f.Text != nil && w.in.Target == w.f.Target {
		s = w.f.Text(s)
	}
	return draftyaml.String(importer.EscapeTemplateText(s))
}

// jsonText writes the JSON raw as YAML flow text reading back as it: each
// string, an object's keys included, written as stringText writes it, each
// number, boolean and null as JSON writes it, the members of an object in the
// order the source writes them.
func (w *aggregateWriter) jsonText(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !json.Valid(raw) {
		return "", false
	}
	switch raw[0] {
	case '{':
		var parts []string
		for _, e := range importer.ObjectEntries(raw) {
			text, ok := w.jsonText(e.Value)
			if !ok {
				return "", false
			}
			parts = append(parts, w.stringText(e.Key)+": "+text)
		}
		return "{" + strings.Join(parts, ", ") + "}", true
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return "", false
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := w.jsonText(item)
			if !ok {
				return "", false
			}
			parts = append(parts, text)
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
		return w.stringText(s), true
	}
	return string(raw), true
}
