// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
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
//     there, read off its node, a folded child's included where the row's fold
//     writes under infra; a drafted instance's hold the values its Recipe reads
//     from infra and infra.overrides, read off its node, and the keys a
//     property the node does not write leaves empty;
//   - each value is written as the Release's text, a parameter as its value —
//     the parameters file's, else the template's literal default — and a
//     pointer or deploy-time text as pointers.go says; what cannot be written
//     is left for the answers file where the source's value is owed, and
//     warned value-not-carried where it is not;
//   - the secrets are those secrets.go attaches, and a drafted instance's
//     comment names each secret that stays a deploy-time parameter;
//   - a mapped instance names its row's Recipe as its realization on every
//     target, which the host confirms against that target's Recipes; a drafted
//     instance names its drafted Recipe, which exists on the format's own
//     target alone, where the import's target is that one;
//   - a module-form instance's comment names each parameter the node sets that
//     its row does not translate.
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
	switch {
	case cl.Class == ClassMapped && cl.Row != nil:
		source := w.sourceOf(cl)
		if source != nil {
			for _, p := range cl.Row.Infra {
				b.put(source, p.From, []string{"infra", p.Key}, p.Conv, false)
			}
			for _, p := range cl.Row.Overrides {
				b.put(source, p.From, []string{"infra", "overrides", p.Key}, p.Conv, false)
			}
		}
		for _, child := range w.out {
			if child.Class != ClassFolded || child.Into != n.ID || len(w.cs.folding[child.Node.ID]) > 0 {
				continue
			}
			if fold := childFold(cl.Row, child.Node.Type); fold != nil && len(fold.Key) > 1 && fold.Key[0] == "infra" {
				b.put(child.Node, fold.From, fold.Key, "", false)
			}
		}
		b.e.Secrets = w.mappedSecrets(cl, source)
		b.e.Realization = cl.Row.Recipe
		if isModule(n.Form) {
			if names := untranslated(n, cl.Row); len(names) > 0 {
				b.e.Comments = append(b.e.Comments, "Module parameters the source sets and the draft does not translate: "+
					draftyaml.CommentList(names)+".")
			}
		}
	case cl.Class == ClassDrafted:
		for _, in := range cl.Inputs {
			if in.Property == nil {
				continue
			}
			switch in.Source.Pillar {
			case schemadraft.PillarInfra:
				b.put(n, in.Property.Path, []string{"infra", in.Source.Key}, "", in.Required)
			case schemadraft.PillarOverrides:
				b.put(n, in.Property.Path, []string{"infra", "overrides", in.Source.Key}, "", in.Required)
			}
		}
		b.e.Comments = append(b.e.Comments, w.draftedSecrets(cl)...)
		if w.ownTarget() {
			b.e.Realization = cl.Recipe
		}
	}
	return b.done()
}

// untranslated returns the names of the parameters the module node n sets that
// no placement of row reads, in the order the node writes them.
func untranslated(n *Node, row *Row) []string {
	paths := rowPaths(row)
	var out []string
	for _, p := range n.Body {
		covered := false
		for _, path := range paths {
			covered = covered || overlaps(path, []string{p.Name})
		}
		if !covered {
			out = append(out, p.Name)
		}
	}
	return out
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

// put writes the value of source's property at from at dest, a key path of the
// entry, converted by conv: its text, or the key left for the answers file, or
// a warning where the value cannot be written. A key the source does not write
// is left where owed is set.
func (b *entryBuilder) put(source *Node, from, dest []string, conv Conv, owed bool) {
	w := b.w
	where := strings.Join(dest, ".")
	p, ok := nodeProp(source, from)
	if !ok {
		if owed {
			b.left(dest, "the property "+pathText(from)+", which another resource drafted with it writes and "+
				source.ID+" does not")
		}
		return
	}
	site := valueSite{node: source.ID, instance: b.cl.Key, from: from, dest: where}
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

// paramComment names each parameter p reads and where its value comes from.
func (w *aggregateWriter) paramComment(p Prop) string {
	var parts []string
	seen := map[string]bool{}
	for _, l := range leaves([]Prop{p}) {
		collectParams(l.value, func(name string) {
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

// collectParams calls found with the name of each parameter v reads, in order.
func collectParams(v Value, found func(string)) {
	switch v := v.(type) {
	case Param:
		found(v.Name)
	case Compose:
		for _, part := range v.Parts {
			collectParams(part, found)
		}
	}
}
