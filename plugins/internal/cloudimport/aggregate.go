// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
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
// Comments open it: the sources read and those not read, the notes the format
// adds to the graph, each line of one a comment line, each output of the
// source with the instance and the provides key carrying it where one does,
// the parameters the draft carries nowhere, and the substrate the source
// creates, which the draft leaves to the operator. Above an instance whose key
// is not its node's ID a comment names that ID, then its Comments. Each
// connection is written under a socket and an alias both named for its
// target's key, its comments above it and its entries delivered through
// draftyaml.Inject. Each attachment an attached node is written as on an
// instance, other than a referenced secret the Release attaches, is written
// under that instance's features at its slot: its Feature as the type, and its
// values read off the attached node through the rule's placements, as a mapped
// instance's values are, the name it pins in place of what they place under
// name.
//
// The values of a mapped instance are those its row's Values place, read off
// its node, or, for an instance a fold of its row makes of a child, off that
// child where it writes the placement's path and off the parent folded into it
// otherwise, followed by the value of that child the fold writes under values;
// then the value under values of each child folded into it; then each string
// values key its row requires that none of these writes, reading an Aggregate
// parameter declared for it with no default, which the Release supplies. A
// child's name, where a fold or a row reads it off a mapped instance's own
// node or a child, is its own name within its parent: its Leaf where the
// source composes its Name of its parent's and its own, and not written,
// warned value-not-carried, where the source composes them so its own part
// cannot be told apart. The values of a drafted instance are those its Recipe reads from
// values, read off its node. A placement's converter turns the value read into
// the one written: string writes its text, integer an integer, unit:<suffix>
// its text followed by the suffix, map:<from>=<to>,… the <to> of the <from> its
// text is, regardless of case, or nothing where it is none of them. A literal
// is written as YAML reading back as the literal, each string in it — an object's keys included — written through the
// format's Text where the import's target is f.Target, so the format's own
// Recipes read it as the literal it is; deploy-time text is written as it is
// for its own target, beneath a comment naming each parameter whose value the
// format wrote in it, and for any other target is not written and warned
// value-not-carried, as is text owing what it reads and a value a converter
// cannot convert. A value read
// from a parameter of the template is written as a reference to a parameter of
// the Aggregate, declared under spec.parameters, as params.go describes; a
// value read from a secure parameter, a reference, a credential or text that
// names a secure parameter is not written, and is warned value-not-carried
// with the key it would be written at, as is every object or list holding
// one. A secret a store holds is
// written as the source's text for it where the import's target is f.Target,
// and is not written otherwise. Every
// text read from the source is escaped through EscapeTemplateText, so it reads
// back as written once the compiler evaluates the Aggregate as a template, and
// every comment line is written through draftyaml.CommentLine.
//
// An instance whose node carries a condition is written with active reading the
// parameter the condition is, where it is one boolean parameter and no
// connection of the Aggregate reaches the instance.
//
// A parameter is carried where a node the draft writes an instance for, or
// folds or attaches into one, reads it at a path the draft carries: any
// property of a drafted node, its name included; a property a mapped
// instance's row places; the path a fold writes of a folded child; the path an
// attachment the Aggregate declares places in its values. An entry of a mapped
// instance's environment list carries no parameter: the draft delivers the
// environment from connections, and names each entry it does not deliver in
// the Release. The
// Aggregate is named as importer.DraftName names it. Two runs over one input
// give one byte sequence.
func Aggregate(in aggrega.ImportInput, f Format, g *Graph, classified []Classified) (aggrega.EmittedEntity,
	[]aggrega.Diagnostic) {
	w := newAggregateWriter(in, f, g, classified, false)

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
	var components strings.Builder
	written := false
	for _, cl := range w.out {
		if cl.Key == "" || !isInstance(cl) {
			continue
		}
		if !written {
			components.WriteString("  components:\n")
			written = true
		}
		w.instance(&components, cl)
	}
	if !written {
		components.WriteString("  components: {}\n")
	}
	b.WriteString(w.parametersBlock())
	b.WriteString(components.String())
	return aggrega.EmittedEntity{Kind: "Aggregate", Name: name, Mode: "draft", Body: b.String()}, w.diags
}

// aggregateWriter writes the text of values: those of the drafted Aggregate's
// instances, where a parameter is a reference to a parameter of the Aggregate,
// and, with release set, those of the drafted Release's entries, which is no
// template and holds each parameter's value itself.
type aggregateWriter struct {
	in      aggrega.ImportInput
	f       Format
	g       *Graph
	out     []*Classified
	cs      classLookup
	diags   []aggrega.Diagnostic
	release bool
	// hoisted holds the Aggregate parameter key of each template parameter an
	// instance reads; keys are the keys taken; pending the parameters the
	// value being written reads, declared once it is written whole.
	hoisted map[string]string
	keys    map[string]bool
	pending []string
	// required holds the Aggregate parameters standing for the values the
	// instances' Components require and their sources do not give, in the order
	// declared.
	required []requiredParam
	// noted holds the secure parameters whose values were judged, and secure the
	// patterns that find each secure parameter's name in text.
	noted  map[string]bool
	secure map[string]*regexp.Regexp
	// mappedEntry is set while the Release entry of a mapped instance is
	// written, whose pointers are left for the answers file.
	mappedEntry bool
}

// newAggregateWriter returns the writer of the instances of classified.
func newAggregateWriter(in aggrega.ImportInput, f Format, g *Graph, classified []Classified,
	release bool) *aggregateWriter {
	w := &aggregateWriter{in: in, f: f, g: g, release: release, hoisted: map[string]string{}, keys: map[string]bool{},
		noted: map[string]bool{}, secure: map[string]*regexp.Regexp{}}
	for i := range classified {
		if classified[i].Node != nil {
			w.out = append(w.out, &classified[i])
		}
	}
	w.cs = newClasses(g, w.out)
	return w
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
		for _, note := range w.g.Notes {
			for _, line := range lineBreak.Split(note, -1) {
				b.WriteString(draftyaml.CommentLine("", line))
			}
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
// the order the source declares them. A parameter whose value deploy-time text
// holds written in place is carried only where that text is written.
func (w *aggregateWriter) notCarried() []string {
	if w.g == nil {
		return nil
	}
	carried := map[string]bool{}
	for _, cl := range w.out {
		for _, v := range w.carriedValues(cl) {
			collectParams(v, func(name string, inPlace bool) {
				if !inPlace || w.textWritten(v) {
					carried[name] = true
				}
			})
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

// textWritten reports whether v is deploy-time text the draft writes as it is,
// as leafText decides it: neither for another target, nor owing what it reads,
// nor naming a secure parameter.
func (w *aggregateWriter) textWritten(v Value) bool {
	o, ok := v.(Opaque)
	if !ok {
		return false
	}
	_, out := w.leafText(o, "")
	return out.ok
}

// carriedValues returns the values of cl's node the draft carries.
func (w *aggregateWriter) carriedValues(cl *Classified) []Value {
	n := cl.Node
	var out []Value
	// at adds the values n writes at paths, each read as read reads it, where
	// keep holds of the path.
	at := func(read func(*Node, []string) read, keep func([]string) bool, paths ...[]string) {
		for _, l := range leaves(n.Body) {
			for _, path := range paths {
				if keep(path) && overlaps(path, l.path) {
					out = append(out, l.value)
					break
				}
			}
		}
		for _, path := range paths {
			if r := read(n, path); keep(path) && isName(path) && r.ok {
				out = append(out, r.prop.Value)
				break
			}
		}
	}
	every := func([]string) bool { return true }
	if cl.Key != "" && isInstance(cl) && w.conditionParam(cl) != nil {
		out = append(out, n.Condition)
	}
	switch {
	case cl.Class == ClassDrafted:
		out = append(out, n.Name)
		for _, l := range leaves(n.Body) {
			out = append(out, l.value)
		}
	case cl.Class == ClassMapped && cl.Row != nil:
		paths := placedPaths(cl.Row)
		if fold := instancesFold(cl.Row, n.Type); fold != nil {
			paths = append(paths, fold.From)
		}
		at(childRead, every, paths...)
	case cl.Class == ClassFolded && len(w.cs.folding[n.ID]) > 0:
		for _, id := range w.cs.folding[n.ID] {
			inst := w.cs.byID[id]
			if inst == nil {
				continue
			}
			child := inst.Node
			at(nodeRead, func(path []string) bool { return !childWrites(child, path) }, placedPaths(inst.Row)...)
		}
	case cl.Class == ClassFolded:
		var paths [][]string
		for _, f := range childFolds(cl.Row, n.Type) {
			paths = append(paths, f.From)
		}
		at(childRead, every, paths...)
	case cl.Class == ClassAttached:
		var paths [][]string
		for _, a := range cl.Attach {
			if a.Secret != nil {
				continue
			}
			for _, p := range a.Values {
				if p.Key != attachName || a.Name == "" {
					paths = append(paths, p.From)
				}
			}
		}
		at(nodeRead, every, paths...)
	}
	return out
}

// rowPaths are the paths r reads of its node: each placement's and the
// environment list's, whose entries the draft wires or names.
func rowPaths(r *Row) [][]string {
	out := placedPaths(r)
	if r != nil && r.Env != nil {
		out = append(out, r.Env.Path)
	}
	return out
}

// placedPaths are the paths r carries of its node, each placement's. The
// environment list's entries are not among them: an entry the draft carries
// is a connection's delivery, and one it does not is named in the Release.
func placedPaths(r *Row) [][]string {
	if r == nil {
		return nil
	}
	var out [][]string
	for _, ps := range [][]Placement{r.Values, r.Infra, r.Overrides, r.Secrets} {
		for _, p := range ps {
			out = append(out, p.From)
		}
	}
	return out
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
	if w.f.InstanceComment != nil {
		if line := w.f.InstanceComment(*cl); line != "" {
			b.WriteString(draftyaml.CommentLine("    ", line))
		}
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
	active, values := w.instanceParts(cl)
	features := w.features(cl)
	if active != "" {
		b.WriteString("      active: " + active + "\n")
	}
	if len(values) > 0 {
		b.WriteString("      values:\n")
		writeTree(b, "        ", values)
	}
	if len(features) > 0 {
		b.WriteString("      features:\n")
		for _, f := range features {
			b.WriteString("        " + keyText(f.slot) + ":\n")
			b.WriteString("          type: " + f.feature + "\n")
			if len(f.values) > 0 {
				b.WriteString("          values:\n")
				writeTree(b, "            ", f.values)
			}
		}
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

// valueTree is a values key holding either text or members, and the comment
// written above it.
type valueTree struct {
	key, text, comment string
	members            []*valueTree
}

// values returns the values of cl's instance, as Aggregate states them, and
// for a mapped instance each string values key its row requires that they do
// not hold, reading the Aggregate parameter declared for it (requiredValue).
func (w *aggregateWriter) values(cl *Classified) []*valueTree {
	var tree []*valueTree
	for _, v := range w.valueReads(cl) {
		tree = w.put(tree, cl, v.r, v.key, "values."+pathText(v.key), v.conv)
	}
	if cl.Class != ClassMapped || cl.Row == nil {
		return tree
	}
	for _, req := range cl.Row.Requires {
		key, ok := strings.CutPrefix(req.Key, "values.")
		if !ok || key == "" || strings.Contains(key, ".") || req.Type != "string" ||
			slices.ContainsFunc(tree, func(v *valueTree) bool { return v.key == key }) {
			continue
		}
		tree = plant(tree, []string{key}, w.requiredValue(cl, key), "")
	}
	return tree
}

// put plants in tree, under key, the value r reads, converted by conv, as cl's
// instance writes it at dest, a comment above it naming each parameter it
// reads; a value that cannot be written is warned value-not-carried, and one r
// does not read is passed over.
func (w *aggregateWriter) put(tree []*valueTree, cl *Classified, r read, key []string, dest string,
	conv Conv) []*valueTree {
	if r.node == nil || len(key) == 0 {
		return tree
	}
	site := valueSite{node: r.node.ID, instance: cl.Key, from: r.from, dest: dest}
	if !r.ok {
		if r.why != "" {
			w.notCarriedHere(site, r.why)
		}
		return tree
	}
	text, o := w.propText(r.prop, conv)
	if o.ok {
		return plant(tree, key, w.commit(text), w.paramComment(r.prop))
	}
	w.pending = nil
	w.notCarriedHere(site, o.why)
	return tree
}

// valueRead is one value an instance's values read: what is read, the key
// under values it is written at, and its converter.
type valueRead struct {
	r    read
	key  []string
	conv Conv
}

// valueReads returns the values cl's instance reads, in the order Aggregate
// writes them: a mapped instance's row's Values, the value of the child an
// instance a fold of its row makes is, and the value of each child folded into
// it each fold of its type writes under values; a drafted instance's inputs
// read from values.
func (w *aggregateWriter) valueReads(cl *Classified) []valueRead {
	var out []valueRead
	n := cl.Node
	switch {
	case cl.Class == ClassMapped && cl.Row != nil:
		for _, p := range cl.Row.Values {
			out = append(out, valueRead{r: w.readOff(cl, p.From), key: []string{p.Key}, conv: p.Conv})
		}
		if fold := instancesFold(cl.Row, n.Type); fold != nil {
			out = append(out, valueRead{r: childRead(n, fold.From), key: valuesKey(fold.Key)})
		}
		for _, child := range w.out {
			if child.Class != ClassFolded || child.Into != n.ID || len(w.cs.folding[child.Node.ID]) > 0 {
				continue
			}
			for _, f := range childFolds(cl.Row, child.Node.Type) {
				out = append(out, valueRead{r: childRead(child.Node, f.From), key: valuesKey(f.Key)})
			}
		}
	case cl.Class == ClassDrafted:
		for _, in := range cl.Inputs {
			if in.Source.Pillar == schemadraft.PillarValues && in.Property != nil {
				out = append(out, valueRead{r: nodeRead(n, in.Property.Path), key: []string{in.Source.Key}})
			}
		}
	}
	return out
}

// feature is one attachment the Aggregate declares under an instance's
// features: its slot, its feature type and its values.
type feature struct {
	slot, feature string
	values        []*valueTree
}

// features returns the attachments the Aggregate declares on cl's instance,
// as Aggregate states them, in the order the attached nodes are written, each
// node's in its order.
func (w *aggregateWriter) features(cl *Classified) []feature {
	var out []feature
	for _, at := range w.attachedOn(cl.Node.ID) {
		if at.a.Secret != nil {
			continue
		}
		var tree []*valueTree
		named := false
		for _, p := range at.a.Values {
			if p.Key == attachName && at.a.Name != "" {
				tree, named = plant(tree, []string{p.Key}, w.stringText(at.a.Name), ""), true
				continue
			}
			tree = w.put(tree, cl, nodeRead(at.node, p.From), []string{p.Key},
				"features."+at.a.Slot+".values."+p.Key, p.Conv)
		}
		if at.a.Name != "" && !named {
			tree = plant(tree, []string{attachName}, w.stringText(at.a.Name), "")
		}
		out = append(out, feature{slot: at.a.Slot, feature: at.a.Feature, values: tree})
	}
	return out
}

// attachmentOn is an attachment written on an instance, with the node it
// attaches.
type attachmentOn struct {
	node *Node
	a    Attachment
}

// attachedOn returns the attachments written on the instance of the node id,
// in the order the attached nodes are classified, each node's in its order.
func (w *aggregateWriter) attachedOn(id string) []attachmentOn {
	var out []attachmentOn
	for _, cl := range w.out {
		if cl.Class != ClassAttached {
			continue
		}
		for _, a := range cl.Attach {
			if a.Into == id {
				out = append(out, attachmentOn{node: cl.Node, a: a})
			}
		}
	}
	return out
}

// read is what a node writes at a property path, as a placement reads it.
type read struct {
	// node is the node read and from the path read.
	node *Node
	from []string
	// prop is what node writes at from, ok reporting that it writes something
	// there; why says why nothing is read where node writes the path and the
	// value cannot be told, empty where node writes nothing there.
	prop Prop
	ok   bool
	why  string
}

// nodeRead reads what n writes at path, as nodeProp reads it.
func nodeRead(n *Node, path []string) read {
	if n == nil {
		return read{from: path}
	}
	p, ok := nodeProp(n, path)
	return read{node: n, from: path, prop: p, ok: ok}
}

// childRead reads what n writes at path, as nodeProp reads it, but its name
// as its own name within its parent: Leaf where Name is the full name the
// source composes, and nothing, with why, where the source composes the two
// so its own part cannot be told. A fold reads a child so, and so does an
// instance of a row read off its own node or a child's.
func childRead(n *Node, path []string) read {
	if n != nil && n.Composed && isName(path) {
		if _, written := propAt(n.Body, path); !written {
			if n.Leaf == nil {
				return read{node: n, from: path, why: "the source composes the name of " + n.ID + " of its " +
					"parent's name and its own, so its own name within its parent cannot be told apart"}
			}
			return read{node: n, from: path, prop: Prop{Value: n.Leaf}, ok: true}
		}
	}
	return nodeRead(n, path)
}

// childWrites reports whether the child n writes path, its name read as
// childRead reads it: the test that reads an instance a fold makes of a child
// off the child rather than off the parent folded into it.
func childWrites(n *Node, path []string) bool {
	r := childRead(n, path)
	return r.ok || r.why != ""
}

// readOff reads what the mapped instance cl's row reads at path: off the
// instance's node, its name read as childRead reads it; for an instance a fold
// of its row makes of a child, off the child where childWrites holds of path,
// and off the parent folded into it otherwise.
func (w *aggregateWriter) readOff(cl *Classified, path []string) read {
	if instancesFold(cl.Row, cl.Node.Type) == nil || childWrites(cl.Node, path) {
		return childRead(cl.Node, path)
	}
	if parent := w.foldedParent(cl); parent != nil {
		return nodeRead(parent, path)
	}
	return childRead(cl.Node, path)
}

// foldedParent returns the parent folded into cl, an instance a fold of its
// row makes of a child; nil where there is none.
func (w *aggregateWriter) foldedParent(cl *Classified) *Node {
	if instancesFold(cl.Row, cl.Node.Type) == nil {
		return nil
	}
	if parent := w.cs.byID[cl.Node.Parent]; parent != nil && parent.Class == ClassFolded {
		return parent.Node
	}
	return nil
}

// valuesKey returns the key under values a fold writes at key, nil where key
// is not under values.
func valuesKey(key []string) []string {
	if len(key) < 2 || key[0] != "values" {
		return nil
	}
	return key[1:]
}

// plant adds text under key to tree, comment above it, unless key, or a key it
// lies in or holds, is set already.
func plant(tree []*valueTree, key []string, text, comment string) []*valueTree {
	for _, t := range tree {
		if t.key != key[0] {
			continue
		}
		if len(key) == 1 || t.members == nil {
			return tree
		}
		t.members = plant(t.members, key[1:], text, comment)
		return tree
	}
	t := &valueTree{key: key[0]}
	if len(key) == 1 {
		t.text, t.comment = text, comment
	} else {
		t.members = plant(nil, key[1:], text, comment)
	}
	return append(tree, t)
}

// writeTree writes tree at indent.
func writeTree(b *strings.Builder, indent string, tree []*valueTree) {
	for _, t := range tree {
		if t.comment != "" {
			b.WriteString(draftyaml.CommentLine(indent, t.comment))
		}
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

// lineBreak is a line break YAML reads as one: a note is written one comment
// line per line it holds.
var lineBreak = regexp.MustCompile(`\r\n|\r|\n`)

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

// valueSite is where a value written is read and written, for a warning: dest
// names the key it is written at, such as values.version or infra.sku.
type valueSite struct {
	node, instance string
	from           []string
	dest           string
}

// outcome is what became of writing one value: written, or not and why. A value
// not written may be left for the Release's answers file (leave), and may also
// warrant a warning of its own (warn); the Aggregate warns of every value it
// does not write. left, where set, is what the comment above a key left for
// the answers file says in place of why: a locator the source states, which a
// comment names and a warning, quoting no value of the source, does not.
type outcome struct {
	ok          bool
	why, left   string
	leave, warn bool
}

// written is the outcome of a value written.
func written() outcome { return outcome{ok: true} }

// notCarriedHere warns that the value at site is not written, and why.
func (w *aggregateWriter) notCarriedHere(site valueSite, why string) {
	where := "in the Aggregate"
	if w.release {
		where = "in the Release"
	}
	w.diags = append(w.diags, Report(CodeValueNotCarried, fmt.Sprintf("%s writes %s, which the instance %s "+
		"takes as %s, and %s, so it is not written: write the value %s by hand", site.node, pathText(site.from),
		site.instance, site.dest, why, where)))
}

// propText returns the YAML text the value p is written as, converted by conv,
// and what became of writing it. An object or a list is written whole or not at
// all: one member not written is the whole's outcome.
func (w *aggregateWriter) propText(p Prop, conv Conv) (string, outcome) {
	switch {
	case p.Value != nil:
		return w.leafText(p.Value, conv)
	case conv != "" && (len(p.Props) > 0 || len(p.Items) > 0):
		return "", outcome{why: "the converter " + string(conv) + " converts a scalar, not an object or a list"}
	case len(p.Props) > 0:
		parts := make([]string, 0, len(p.Props))
		for _, m := range p.Props {
			text, o := w.propText(m, "")
			if !o.ok {
				return "", o
			}
			parts = append(parts, w.stringText(m.Name)+": "+text)
		}
		return "{" + strings.Join(parts, ", ") + "}", written()
	case len(p.Items) > 0:
		parts := make([]string, 0, len(p.Items))
		for _, item := range p.Items {
			text, o := w.propText(item, "")
			if !o.ok {
				return "", o
			}
			parts = append(parts, text)
		}
		return "[" + strings.Join(parts, ", ") + "]", written()
	}
	return "", outcome{why: "the source writes nothing there"}
}

// leafText returns the YAML text the value v is written as, converted by conv,
// and what became of writing it.
func (w *aggregateWriter) leafText(v Value, conv Conv) (string, outcome) {
	switch v := v.(type) {
	case Literal:
		if conv == "" {
			text, ok := w.jsonText(v.JSON)
			if !ok {
				return "", outcome{why: "it is not a value JSON writes"}
			}
			return text, written()
		}
		scalar, st := literalText(v.JSON)
		if st != stateRead {
			return "", outcome{why: "the converter " + string(conv) + " converts a scalar, and it is none"}
		}
		return w.converted(scalar, conv)
	case Opaque:
		if secure := w.secureReads(v.Text); len(secure) > 0 {
			return "", outcome{why: "it reads the secure parameter " + secure[0] + " inside text the source evaluates " +
				"when it deploys, which the draft cannot tell holds a secret", leave: true, warn: true}
		}
		if v.Target != w.in.Target {
			return "", outcome{why: fmt.Sprintf("it is text the %s target evaluates when the source deploys, and "+
				"the import drafts for %s", v.Target, targetName(w.in.Target)), leave: true}
		}
		if owed := w.owed(v); owed != "" {
			return "", outcome{why: owed, leave: true, warn: true}
		}
		if conv != "" && conv != "string" {
			return "", outcome{why: "the converter " + string(conv) + " cannot convert text evaluated when the " +
				"source deploys"}
		}
		return draftyaml.String(w.escape(v.Text)), written()
	case Stored:
		if !w.ownTarget() {
			resolves := ", which " + targetName(w.f.Target) + " resolves when the source deploys"
			return "", outcome{why: "it is a secret the " + v.Store + " store holds" + resolves + ", and the import " +
				"drafts for " + targetName(w.in.Target),
				left: "the secret the " + draftyaml.Comment(v.Store) + " store holds at " + draftyaml.Comment(v.Locator) +
					resolves + "; the import drafts for " + targetName(w.in.Target) + ", so supply the secret as that " +
					"target reads one",
				leave: true}
		}
		if conv != "" && conv != "string" {
			return "", outcome{why: "the converter " + string(conv) + " cannot convert text evaluated when the " +
				"source deploys"}
		}
		return draftyaml.String(w.escape(v.Text)), written()
	case Param:
		return w.paramText(v, conv)
	case Compose:
		return w.composeText(v, conv)
	case Ref:
		if w.release {
			return w.pointerText(v)
		}
		return "", outcome{why: "it reads an attribute of " + v.Node + ", which a connection delivers or the Release " +
			"points at, and the Aggregate's values hold neither"}
	case Credential:
		return "", outcome{why: "it is a credential of " + v.Node + ", which the draft never carries"}
	}
	return "", outcome{why: "it is no value the draft writes"}
}

// owed returns what the deploy-time text o owes once every node is classed:
// what its OwedBesideReads names where the format's Declares says the draft
// declares every resource o reads under the ID o reads it by, and what its
// Owed names otherwise — or, where the format leaves Owed empty, the core's
// own phrase naming each resource not so declared, so text reading one is
// never written. A nil Declares declares none, and a resource no node of the
// classification is, such as a repeated node's, is declared under no ID o
// reads.
func (w *aggregateWriter) owed(o Opaque) string {
	var undeclared []string
	for _, id := range o.Reads {
		cl := w.cs.byID[id]
		if w.f.Declares == nil || cl == nil || !w.f.Declares(*cl) {
			undeclared = append(undeclared, id)
		}
	}
	switch {
	case len(o.Reads) == 0:
		return o.Owed
	case len(undeclared) == 0:
		return o.OwedBesideReads
	case o.Owed != "":
		return o.Owed
	}
	return joinNonEmpty("; ", undeclaredReads(undeclared), o.OwedBesideReads)
}

// undeclaredReads says why deploy-time text reading the resources ids by their
// IDs in the source cannot be written, where its format says nothing of them.
func undeclaredReads(ids []string) string {
	if len(ids) == 1 {
		return "it reads the resource " + ids[0] + " by its ID in the source, and the draft may declare no " +
			"resource under that ID"
	}
	return "it reads the resources " + andList(ids) + " by their IDs in the source, and the draft may declare no " +
		"resource under those IDs"
}

// escape writes text read from the source for the document being written: for
// the Aggregate, escaped so it reads back once evaluated as a template; for the
// Release, which is no template, as it is.
func (w *aggregateWriter) escape(text string) string {
	if w.release {
		return text
	}
	return importer.EscapeTemplateText(text)
}

// targetName names target in a warning.
func targetName(target string) string {
	if target == "" {
		return "no target"
	}
	return "the " + target + " target"
}

// converted returns scalar converted by conv as YAML text, and what became of
// converting it.
func (w *aggregateWriter) converted(scalar string, conv Conv) (string, outcome) {
	switch {
	case conv == "string":
		return w.stringText(scalar), written()
	case conv == "integer":
		if i, err := strconv.ParseInt(strings.TrimSpace(scalar), 10, 64); err == nil {
			return strconv.FormatInt(i, 10), written()
		}
		return "", outcome{why: "the converter integer reads no integer in it"}
	case strings.HasPrefix(string(conv), "unit:"):
		return w.stringText(scalar + strings.TrimPrefix(string(conv), "unit:")), written()
	case strings.HasPrefix(string(conv), convMap):
		if to, ok := conv.mapped(scalar); ok {
			return to, written()
		}
		return "", outcome{why: "the converter " + string(conv) + " maps no value of the source's spelling there"}
	}
	return "", outcome{why: "the row names the converter " + string(conv) + ", which the core does not know"}
}

// stringText writes s, a literal string of the source, as the double-quoted
// scalar it is written as: through the format's Text where the import's target
// is the format's own, then escaped as escape says.
func (w *aggregateWriter) stringText(s string) string {
	if w.f.Text != nil && w.ownTarget() {
		s = w.f.Text(s)
	}
	return draftyaml.String(w.escape(s))
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

// ownTarget reports whether the import names a target and it is the format's
// own.
func (w *aggregateWriter) ownTarget() bool {
	return w.in.Target != "" && w.in.Target == w.f.Target
}
