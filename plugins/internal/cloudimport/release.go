// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Release writes the drafted Release of the import in beside aggregate, the
// Aggregate Aggregate wrote for the same classified nodes, through the kit's
// DraftRelease, and the Manifest the kit drafts where a realization is named.
//
// Its spec.parameters leaves each parameter of the Aggregate that has no
// default for the answers file. It has one entry for each instance, in the
// Aggregate's order:
//
//   - a mapped instance's infra and infra.overrides hold what its row places
//     there, read as the Aggregate reads its values — off its node, or for an
//     instance a fold of its row makes of a child, off that child where it
//     writes the path and off the folded parent otherwise — a folded child's
//     included where the row's fold writes under infra; a drafted instance's hold the values its Recipe reads
//     from infra and infra.overrides, read off its node, and the keys a
//     property the node does not write leaves empty;
//   - each value is written as the Release's text, a parameter as its value —
//     the parameters file's, else the template's literal default — beneath a
//     comment naming it, and deploy-time text beneath one naming each
//     parameter whose value it holds written in place; a pointer or
//     deploy-time text as pointers.go says: a drafted instance's
//     pointer as the format's text for it, a mapped instance's left for the
//     operator to write the literal identifier its catalog Recipe takes; what cannot be written
//     is left for the answers file where the source's value is owed, and
//     warned value-not-carried where it is not, and deploy-time text owing
//     what it reads is both;
//   - the secrets are those secrets.go attaches, and a drafted instance's
//     comment names each secret that stays a deploy-time parameter;
//   - a mapped instance names its row's Recipe as its realization on every
//     target, which the host confirms against that target's Recipes; a drafted
//     instance names its drafted Recipe, which exists on the format's own
//     target alone, where the import's target is that one;
//   - a mapped instance's comment names each property its node writes that its
//     row does not place — a module-form node's parameters its row does not
//     translate — and each property a child folded into it writes that its
//     fold does not; then each entry of its environment list the draft does
//     not deliver from a connection, the variable and its value as the source
//     writes it, or the variable alone where the value is secret, each also
//     warned value-not-carried.
func Release(in aggrega.ImportInput, f Format, g *Graph, classified []Classified,
	aggregate aggrega.EmittedEntity) ([]aggrega.EmittedEntity, []aggrega.Diagnostic) {
	analysis := newAggregateWriter(in, f, g, classified, false)
	for _, cl := range analysis.out {
		if cl.Key != "" && isInstance(cl) {
			analysis.instanceParts(cl)
		}
	}
	w := newAggregateWriter(in, f, g, classified, true)
	var draft importer.ReleaseDraft
	if g != nil {
		done := map[string]bool{}
		for _, p := range g.Params {
			key, ok := analysis.hoisted[p.Name]
			if !ok || done[p.Name] {
				continue
			}
			done[p.Name] = true
			if _, has := analysis.defaultOf(p); !has {
				draft.Parameters = append(draft.Parameters, importer.LeftItem{Path: key, Text: "the value of the " +
					"template parameter " + draftyaml.Comment(p.Name) + ", which the parameters file does not supply and " +
					"the template gives no literal default"})
			}
		}
	}
	for _, cl := range w.out {
		if cl.Key != "" && isInstance(cl) {
			draft.Entries = append(draft.Entries, w.entry(cl))
		}
	}
	return importer.DraftRelease(in, aggregate, draft), w.diags
}

// entry returns the Release entry of the instance cl.
func (w *aggregateWriter) entry(cl *Classified) importer.ReleaseEntry {
	b := &entryBuilder{w: w, cl: cl, e: importer.ReleaseEntry{Key: cl.Key}}
	n := cl.Node
	w.mappedEntry = cl.Class == ClassMapped
	defer func() { w.mappedEntry = false }()
	switch {
	case cl.Class == ClassMapped && cl.Row != nil:
		for _, p := range cl.Row.Infra {
			b.place(w.readOff(cl, p.From), []string{"infra", p.Key}, p.Conv, false)
		}
		for _, p := range cl.Row.Overrides {
			b.place(w.readOff(cl, p.From), []string{"infra", "overrides", p.Key}, p.Conv, false)
		}
		for _, child := range w.out {
			if child.Class != ClassFolded || child.Into != n.ID || len(w.cs.folding[child.Node.ID]) > 0 {
				continue
			}
			if fold := childFold(cl.Row, child.Node.Type); fold != nil && len(fold.Key) > 1 && fold.Key[0] == "infra" {
				b.place(childRead(child.Node, fold.From), fold.Key, "", false)
			}
		}
		b.e.Secrets = w.mappedSecrets(cl)
		b.e.Realization = cl.Row.Recipe
		b.e.Comments = append(b.e.Comments, w.unplacedComments(cl)...)
		b.e.Comments = append(b.e.Comments, w.unwiredEnv(cl)...)
	case cl.Class == ClassMapped:
		// A type the mapping_overrides setting maps has no row: nothing is placed,
		// every property the node writes is named for the author to place, and the
		// host maps the realization.
		b.e.Comments = append(b.e.Comments, w.unplacedComments(cl)...)
		b.e.Comments = append(b.e.Comments, w.unwiredEnv(cl)...)
	case cl.Class == ClassDrafted:
		for _, in := range cl.Inputs {
			if in.Property == nil {
				continue
			}
			switch in.Source.Pillar {
			case schemadraft.PillarInfra:
				b.place(nodeRead(n, in.Property.Path), []string{"infra", in.Source.Key}, "", in.Required)
			case schemadraft.PillarOverrides:
				b.place(nodeRead(n, in.Property.Path), []string{"infra", "overrides", in.Source.Key}, "", in.Required)
			}
		}
		b.e.Comments = append(b.e.Comments, w.draftedSecrets(cl)...)
		if w.ownTarget() {
			b.e.Realization = cl.Recipe
		}
	}
	return b.done()
}

// unplaced returns the paths body writes that no path of paths places, in the
// order body writes them, each at the outermost member or item nothing under
// which is placed: a member a path names or lies inside is placed, one a path
// lies under is read member by member, and one holding a literal a path lies
// inside is placed.
func unplaced(body []Prop, paths [][]string) [][]string {
	var out [][]string
	for _, p := range body {
		out = unplacedIn(out, p, []string{p.Name}, paths)
	}
	return out
}

func unplacedIn(out [][]string, p Prop, at []string, paths [][]string) [][]string {
	under := false
	for _, path := range paths {
		switch {
		case len(path) <= len(at):
			if samePath(path, at[:len(path)]) {
				return out
			}
		case samePath(path[:len(at)], at):
			under = true
		}
	}
	switch {
	case !under:
		return append(out, at)
	case p.Value != nil:
		return out
	}
	for _, m := range p.Props {
		out = unplacedIn(out, m, extend(at, m.Name), paths)
	}
	for i, item := range p.Items {
		out = unplacedIn(out, item, extend(at, strconv.Itoa(i)), paths)
	}
	return out
}

// unplacedComments are the comment lines of the mapped instance cl naming what
// the draft does not place: the properties its node writes that its row
// neither places nor fixes by a predicate of its When testing it equals a
// value, which the row's Recipe writes, or for a node of a module form the
// parameters its row does not translate; and for each child folded into it,
// the properties the child writes that its fold does not.
func (w *aggregateWriter) unplacedComments(cl *Classified) []string {
	var out []string
	n := cl.Node
	placed := rowPaths(cl.Row)
	if cl.Row != nil {
		for _, p := range cl.Row.When {
			if p.Op == OpEquals {
				placed = append(placed, p.Path)
			}
		}
	}
	if paths := unplaced(n.Body, placed); len(paths) > 0 {
		lead := "Properties the source writes and the draft does not place: "
		if isModule(n.Form) {
			lead = "Module parameters the source sets and the draft does not translate: "
		}
		out = append(out, lead+draftyaml.CommentList(pathTexts(paths))+".")
	}
	for _, child := range w.out {
		if child.Class != ClassFolded || child.Into != n.ID || len(w.cs.folding[child.Node.ID]) > 0 {
			continue
		}
		fold := childFold(cl.Row, child.Node.Type)
		if fold == nil {
			continue
		}
		if paths := unplaced(child.Node.Body, [][]string{fold.From}); len(paths) > 0 {
			out = append(out, "Properties "+draftyaml.Comment(child.Node.ID)+", folded into this instance, writes "+
				"and the draft does not place: "+draftyaml.CommentList(pathTexts(paths))+".")
		}
	}
	return out
}

// pathTexts writes each of paths as text.
func pathTexts(paths [][]string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = pathText(p)
	}
	return out
}

// unwiredEnv returns the comment line naming each entry of the mapped
// instance cl's environment list the draft does not deliver, and warns
// value-not-carried of each, once: an entry whose value reads no other
// resource, which no connection delivers. An entry reading another resource is
// a connection's, reported where it is wired or why it is not. A secret value
// — a secure parameter's, a credential's, or text naming a secure parameter —
// is never written: its variable alone is named.
func (w *aggregateWriter) unwiredEnv(cl *Classified) []string {
	if cl.Row == nil {
		return nil
	}
	env := cl.Row.Env
	if env == nil {
		return nil
	}
	r := w.readOff(cl, env.Path)
	if !r.ok {
		return nil
	}
	var named []string
	for i, item := range r.prop.Items {
		variable := "the entry " + strconv.Itoa(i)
		if p, ok := child(item.Props, nil, env.Name); ok {
			if text, st := literalText(literalJSON(p.Value)); st == stateRead && text != "" {
				variable = text
			}
		}
		value, _ := child(item.Props, nil, env.Value)
		v := value.Value
		if len(refsOf(v)) > 0 || holdsCredential(v) {
			continue
		}
		text, secret := w.sourceText(v)
		entry := draftyaml.Comment(variable)
		switch {
		case secret:
			entry += ", a secret value the draft never writes"
		case text == "":
			entry += ", whose value the draft does not read"
		default:
			entry += " = " + draftyaml.Comment(text)
		}
		named = append(named, entry)
		w.diags = append(w.diags, Report(CodeValueNotCarried, fmt.Sprintf("%s sets the variable %s in the "+
			"environment of the instance %s, and the draft delivers an instance's environment from its "+
			"connections alone, so the variable is not carried: the instance's Release entry names it, for the "+
			"author to set where the instance is configured", r.node.ID, variable, cl.Key)))
	}
	if len(named) == 0 {
		return nil
	}
	return []string{"Environment variables the source sets and no connection delivers: " +
		strings.Join(named, "; ") + "."}
}

// literalJSON is the JSON of a literal v, nil for any other value.
func literalJSON(v Value) []byte {
	if l, ok := v.(Literal); ok {
		return l.JSON
	}
	return nil
}

// holdsCredential reports whether v is, or composes, a credential.
func holdsCredential(v Value) bool {
	switch v := v.(type) {
	case Credential:
		return true
	case Compose:
		for _, part := range v.Parts {
			if holdsCredential(part) {
				return true
			}
		}
	}
	return false
}

// sourceText writes v as the source writes it, for a comment: a literal
// scalar as its text and any other literal as its JSON, a parameter as the
// template parameter it names in braces, text the source evaluates when it
// deploys as written, and a composition as the text of its parts. secret is
// set where v is a secret value, whose text is not written.
func (w *aggregateWriter) sourceText(v Value) (text string, secret bool) {
	switch v := v.(type) {
	case nil:
		return "", false
	case Literal:
		if s, st := literalText(v.JSON); st == stateRead {
			return s, false
		}
		return string(bytes.TrimSpace(v.JSON)), false
	case Param:
		p := w.param(v.Name)
		return "{the template parameter " + v.Name + "}", p != nil && p.Secure
	case Opaque:
		return v.Text, len(w.secureReads(v.Text)) > 0
	case Compose:
		var b strings.Builder
		for _, part := range v.Parts {
			if l, ok := part.(Literal); ok {
				if s, st := literalText(l.JSON); st == stateRead {
					b.WriteString(s)
					continue
				}
			}
			t, s := w.sourceText(part)
			if s {
				return "", true
			}
			b.WriteString(t)
		}
		return b.String(), false
	}
	return "", false
}

// entryBuilder is one Release entry being written.
type entryBuilder struct {
	w  *aggregateWriter
	cl *Classified
	e  importer.ReleaseEntry
	// dotted are the lines of the values whose key holds a dot, which no key path
	// names, written as the entry's own lines.
	dotted []string
}

// place writes the value r reads at dest, a key path of the entry, converted
// by conv: its text, or the key left for the answers file, or a warning where
// the value cannot be written. A key the node read does not write is left
// where owed is set.
func (b *entryBuilder) place(r read, dest []string, conv Conv, owed bool) {
	w := b.w
	if r.node == nil {
		return
	}
	where := strings.Join(dest, ".")
	site := valueSite{node: r.node.ID, instance: b.cl.Key, from: r.from, dest: where}
	if !r.ok {
		switch {
		case r.why != "":
			w.notCarriedHere(site, r.why)
		case owed:
			b.left(dest, "the property "+pathText(r.from)+", which another resource drafted with it writes and "+
				r.node.ID+" does not")
		}
		return
	}
	p := r.prop
	text, o := w.propText(p, conv)
	if o.ok {
		b.value(dest, text, w.paramComment(p))
		return
	}
	if o.warn || !o.leave {
		w.notCarriedHere(site, o.why)
	}
	if o.leave {
		why := o.why
		if o.warn {
			why = "the source's value, which the draft does not write"
		}
		b.left(dest, why)
	}
}

// left leaves the key at dest for the answers file, why saying what is owed.
func (b *entryBuilder) left(dest []string, why string) {
	if plainPath(dest) {
		b.e.Left = append(b.e.Left, importer.LeftItem{Path: strings.Join(dest, "."), Text: why})
		return
	}
	b.e.Left = append(b.e.Left, importer.LeftItem{Text: strings.Join(dest, ".") + ", " + why})
}

// value writes text at dest, a comment above it where one is given.
func (b *entryBuilder) value(dest []string, text, comment string) {
	if plainPath(dest) {
		b.e.Values = append(b.e.Values, importer.ReleaseValue{Path: strings.Join(dest, "."), Value: text,
			Comment: comment})
		return
	}
	if len(b.dotted) == 0 {
		b.dotted = append(b.dotted, "infra:", "  overrides:")
	}
	if comment != "" {
		b.dotted = append(b.dotted, "    # "+comment)
	}
	b.dotted = append(b.dotted, "    "+keyText(dest[len(dest)-1])+": "+text)
}

// plainPath reports whether no segment of path holds a dot, so the path names
// the key it spells.
func plainPath(path []string) bool {
	for _, seg := range path {
		if strings.Contains(seg, ".") {
			return false
		}
	}
	return true
}

// done returns the entry.
func (b *entryBuilder) done() importer.ReleaseEntry {
	b.e.Lines = append(b.e.Lines, b.dotted...)
	return b.e
}

// paramComment names each parameter whose value the text of p holds and where
// that value comes from: in the Release each parameter p reads; in the
// Aggregate, which reads its own parameters, each written in place in
// deploy-time text.
func (w *aggregateWriter) paramComment(p Prop) string {
	var parts []string
	seen := map[string]bool{}
	for _, l := range leaves([]Prop{p}) {
		collectParams(l.value, func(name string, inPlace bool) {
			if !w.release && !inPlace {
				return
			}
			if declared := w.param(name); declared != nil && !seen[name] {
				seen[name] = true
				parts = append(parts, fmt.Sprintf("the template parameter %s, %s", draftyaml.Comment(name),
					paramSource(declared)))
			}
		})
	}
	if len(parts) == 0 {
		return ""
	}
	return "From " + strings.Join(parts, "; ") + "."
}

// collectParams calls found with the name of each parameter v reads, in order,
// and whether deploy-time text holds its value written in place.
func collectParams(v Value, found func(name string, inPlace bool)) {
	switch v := v.(type) {
	case Param:
		found(v.Name, false)
	case Opaque:
		for _, name := range v.Params {
			found(name, true)
		}
	case Compose:
		for _, part := range v.Parts {
			collectParams(part, found)
		}
	}
}
