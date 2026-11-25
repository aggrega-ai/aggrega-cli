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
// its node, or, for an instance a fold of its row makes of a child, off that
// child where it writes the placement's path and off the parent folded into it
// otherwise, followed by the value of that child the fold writes under values;
// then the value under values of each child folded into it. A child's name,
// where a fold or a row reads it off a mapped instance's own node or a child,
// is its own name within its parent: its Leaf where the source composes its
// Name of its parent's and its own, and not written, warned
// value-not-carried, where the source composes them so its own part cannot be
// told apart. The values of a drafted instance are those its Recipe reads from
// values, read off its node. A placement's converter turns the value read into
// the one written: string writes its text, integer an integer, unit:<suffix>
// its text followed by the suffix, map:<from>=<to>,… the <to> of the <from> its
// text is, regardless of case, or nothing where it is none of them. A literal
// is written as YAML reading back as the literal, each string in it — an object's keys included — written through the
// format's Text where the import's target is f.Target, so the format's own
// Recipes read it as the literal it is; deploy-time text is written as it is
// for its own target, and for any other target is not written and warned
// value-not-carried, as is a value a converter cannot convert. A value read
// from a parameter of the template is written as a reference to a parameter of
// the Aggregate, declared under spec.parameters, as params.go describes; a
// value read from a secure parameter, a reference, a credential or text that
// names a secure parameter is not written, and is warned value-not-carried with
// the key it would be written at, as is every object or list holding one. Every
// text read from the source is escaped through EscapeTemplateText, so it reads
// back as written once the compiler evaluates the Aggregate as a template, and
// every comment line is written through draftyaml.CommentLine.
//
// An instance whose node carries a condition is written with active reading the
// parameter the condition is, where it is one boolean parameter and no
// connection of the Aggregate reaches the instance.
//
// A parameter is carried where a node the draft writes an instance for, or
// folds into one, reads it at a path the draft carries: any property of a
// drafted node, its name included; a property a mapped instance's row places;
// the path a fold writes of a folded child. An entry of a mapped instance's
// environment list carries no parameter: the draft delivers the environment
// from connections, and names each entry it does not deliver in the Release. The
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
		if fold := childFold(cl.Row, n.Type); fold != nil {
			at(childRead, every, fold.From)
		}
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
	active, values := w.instanceParts(cl)
	if active != "" {
		b.WriteString("      active: " + active + "\n")
	}
	if len(values) > 0 {
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
	put := func(r read, key []string, conv Conv) {
		if r.node == nil || len(key) == 0 {
			return
		}
		site := valueSite{node: r.node.ID, instance: cl.Key, from: r.from, dest: "values." + pathText(key)}
		if !r.ok {
			if r.why != "" {
				w.notCarriedHere(site, r.why)
			}
			return
		}
		text, o := w.propText(r.prop, conv)
		if o.ok {
			tree = plant(tree, key, w.commit(text))
			return
		}
		w.pending = nil
		w.notCarriedHere(site, o.why)
	}
	n := cl.Node
	switch {
	case cl.Class == ClassMapped && cl.Row != nil:
		for _, p := range cl.Row.Values {
			put(w.readOff(cl, p.From), []string{p.Key}, p.Conv)
		}
		if fold := instancesFold(cl.Row, n.Type); fold != nil {
			put(childRead(n, fold.From), valuesKey(fold.Key), "")
		}
		for _, child := range w.out {
			if child.Class != ClassFolded || child.Into != n.ID || len(w.cs.folding[child.Node.ID]) > 0 {
				continue
			}
			if f := childFold(cl.Row, child.Node.Type); f != nil {
				put(childRead(child.Node, f.From), valuesKey(f.Key), "")
			}
		}
	case cl.Class == ClassDrafted:
		for _, in := range cl.Inputs {
			if in.Source.Pillar == schemadraft.PillarValues && in.Property != nil {
				put(nodeRead(n, in.Property.Path), []string{in.Source.Key}, "")
			}
		}
	}
	return tree
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
// does not write.
type outcome struct {
	ok          bool
	why         string
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
