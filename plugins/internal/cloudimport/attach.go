// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// AttachVia is the instance an attach rule writes its attachment on.
type AttachVia int

// The instances an attach rule writes its attachment on.
const (
	// AttachReader writes the attachment on each instance reading the node at
	// a credential slot: an instance a row maps whose reference to the node,
	// or credential read from it, sits at a path one of the row's Secrets
	// placements reads, the slot that placement's.
	AttachReader AttachVia = iota
	// AttachParent writes the attachment at the rule's Slot of the instance of
	// the one node the node's Parent names, where a row maps that node.
	AttachParent
)

// AttachRule is a format's rule for the nodes of one type: a node it takes is
// no instance of its own, and is written as a Feature attachment on another
// node's instance instead, classed [ClassAttached].
//
// A node is taken by the first rule of its type, matched regardless of case,
// whose When holds of it, each predicate read as a row's is read; a predicate
// a value set only when the source deploys leaves undecided does not hold. A
// node the source reads and does not create is never taken, nor one a fold of
// an ancestor's row writes. A node the rule holds for that no instance takes,
// as Via says, is classed as any other node is.
type AttachRule struct {
	// Type is the vendor type of the nodes the rule takes.
	Type string
	// When are the predicates a node of Type meets for the rule to take it,
	// all of them; none takes every node of the type.
	When []Pred
	// Feature is the feature type the attachment requests, such as
	// core/random-password. Where Secret is set the kit's form of the secret
	// gives the type, and Feature is not read.
	Feature string
	// Via is the instance the attachment is written on.
	Via AttachVia
	// Slot is the slot of the parent's instance an AttachParent rule fills.
	Slot string
	// Values place the node's properties under the attachment's values keys:
	// each reads the node at From and writes the value under values at Key,
	// converted by Conv, as a row's placement does. A Secret rule reads the
	// placement keyed origin for the secret's origin instead.
	Values []Placement
	// Others, where set, is what each reader after the first takes, where
	// several instances read the node: the kit's referenced secret,
	// core/referenced-secret, attached in the reader's Release entry, its
	// origin the name the first reader's attachment pins under values.name —
	// the text the node writes as a literal where Values place name, else the
	// key the format gives the node. Where it is empty, every reader takes
	// Feature.
	Others string
	// Secret makes each attachment the kit's referenced secret, attached in
	// the Release entry of the instance taking it and declared nowhere in the
	// Aggregate, its origin the text the node writes as a literal where Values
	// place origin, and none, for the Release to name, otherwise.
	Secret bool
}

// Attachment is one attachment a node an attach rule takes is written as, on
// another node's instance.
type Attachment struct {
	// Into is the ID of the node whose instance takes the attachment, and Slot
	// the slot it fills.
	Into, Slot string
	// Feature is the feature type it requests.
	Feature string
	// Values are the rule's placements of the attached node's properties under
	// the attachment's values keys, written in the Aggregate.
	Values []Placement
	// Name, where set, is the values.name the attachment pins, written in place
	// of what Values place under name, or after what they place where they
	// place none: the origin each other reader's referenced secret names.
	Name string
	// Secret, where set, is the kit's referenced secret the attachment is,
	// attached in the Release entry of Into's instance; the Aggregate declares
	// nothing of it, and Values and Name are not read.
	Secret *importer.Secret
}

// The values keys an attach rule's placements give a meaning of their own.
const (
	// attachName is the key of the name an attachment pins.
	attachName = "name"
	// attachOrigin is the key of the origin of a Secret rule's secret.
	attachOrigin = "origin"
)

// attachedTo is where a node an attach rule takes is attached: the rule and
// the instances taking it.
type attachedTo struct {
	rule    *AttachRule
	targets []attachTarget
}

// attachTarget is an instance an attachment is written on: the ID of its node,
// the slot, and each path at which that node reads the attached node there,
// none for a parent.
type attachTarget struct {
	into, slot string
	paths      [][]string
}

// referencedSecretType is the feature type of the kit's referenced secret.
func referencedSecretType() string {
	return importer.Secret{Referenced: true}.Form("").Type
}

// attachRefused returns the halt on the first of rules the core cannot apply:
// one attaching on no instance the core knows, one naming no Feature where it
// is no Secret rule, an AttachParent rule naming no slot, and one giving each
// reader after the first anything but the kit's referenced secret. It is nil
// where the core can apply every rule.
func attachRefused(rules []AttachRule) *aggrega.Diagnostic {
	for _, r := range rules {
		var why string
		switch {
		case r.Via != AttachReader && r.Via != AttachParent:
			why = fmt.Sprintf("attaches on no instance the core knows, its Via being %d", r.Via)
		case !r.Secret && r.Feature == "":
			why = "names no Feature to attach"
		case r.Via == AttachParent && r.Slot == "":
			why = "attaches on its parent's instance at no slot"
		case r.Others != "" && r.Others != referencedSecretType():
			why = fmt.Sprintf("gives each reader after the first %s, where the core attaches the kit's referenced "+
				"secret, %s", r.Others, referencedSecretType())
		default:
			continue
		}
		d := Report(CodeSourceRefused, "the importer's attach rules cannot be read, so no resource of the source is "+
			"classed: the rule of "+r.Type+WhenText(r.When)+" "+why+"; the rules are the importer's own, so report "+
			"it to the importer's authors")
		return &d
	}
	return nil
}

// attachRule returns the first attach rule of n's type whose When holds of n;
// nil where none does, or where the source reads n and does not create it.
func (c *classifier) attachRule(n *Node) *AttachRule {
	if n.Existing {
		return nil
	}
	rd := reader{g: c.g}
	for i, r := range c.f.Attach {
		if r.Type != "" && strings.EqualFold(r.Type, n.Type) && rd.holds(r.When, n.Body) {
			return &c.f.Attach[i]
		}
	}
	return nil
}

// holds reports whether every predicate of when holds of body.
func (rd reader) holds(when []Pred, body []Prop) bool {
	for _, p := range when {
		if rd.pred(p, body) != truthHolds {
			return false
		}
	}
	return true
}

// mayAttach reports whether an instance could take n by r, as the nodes are of
// themselves before any is classed: for AttachParent, the one node n's parent
// names, a row mapping it; for AttachReader, a node a row maps reading n at a
// credential slot of that row. A node no instance could take is classed as any
// other node is, in its turn.
func (c *classifier) mayAttach(n *Node, r *AttachRule) bool {
	if r.Via == AttachParent {
		p := c.parent(n)
		return p != nil && !p.Existing && c.own[p.ID].byRow()
	}
	for _, e := range c.incoming[n.ID] {
		from, a := c.byID[e.From], c.own[e.From]
		if e.Kind != EdgeDepends && from != nil && !from.Existing && a.byRow() && slotAt(a.row, e.Path) != "" {
			return true
		}
	}
	return false
}

// slotAt returns the credential slot of r whose Secrets placement reads path,
// the first such, empty where none does.
func slotAt(r *Row, path []string) string {
	if r == nil {
		return ""
	}
	for _, p := range r.Secrets {
		if overlaps(p.From, path) {
			return p.Key
		}
	}
	return ""
}

// waits reports whether n awaits an ancestor an attach rule defers: it is
// classed once that ancestor is, so a fold reads its ancestors' classes.
func (c *classifier) waits(n *Node) bool {
	seen := map[*Node]bool{n: true}
	for a := c.parent(n); a != nil && !seen[a]; a = c.parent(a) {
		seen[a] = true
		if c.deferred[a.ID] != nil || c.waiting[a.ID] {
			return true
		}
	}
	return false
}

// settleAttachments attaches each node an attach rule defers, in ID order,
// once every node but those awaiting one is classed, then classes the nodes
// awaiting them, deeper ones after their ancestors, and settles in turn those
// of them a rule defers. A deferred node is attached on each instance its
// rule's Via names that takes it, each slot of an instance filled by the first
// node by ID taking it; a node no instance takes is classed as any other node
// is.
func (c *classifier) settleAttachments() {
	for len(c.deferred) > 0 {
		deferred, waiting := c.deferred, c.queued
		c.deferred, c.waiting, c.queued = map[string]*AttachRule{}, map[string]bool{}, nil
		for _, n := range c.nodes {
			r := deferred[n.ID]
			if r == nil {
				continue
			}
			targets := c.targets(n, r)
			if len(targets) == 0 {
				c.classifyOwn(n)
				continue
			}
			for _, t := range targets {
				c.slots[slotKey(t.into, t.slot)] = true
				for _, p := range t.paths {
					c.readings[readingKey(t.into, p, n.ID)] = true
				}
			}
			c.set(n, &Classified{Class: ClassAttached})
			c.attaching[n.ID] = attachedTo{rule: r, targets: targets}
		}
		for _, n := range waiting {
			c.classify(n)
		}
	}
}

// targets returns the instances taking n by r, as the nodes are classed: for
// AttachParent, the parent's where a row maps it; for AttachReader, each
// instance a row maps reading n at a credential slot of that row, with the
// paths it reads n at there, in the order the edges to n are listed. A slot
// another node's attachment fills already takes none.
func (c *classifier) targets(n *Node, r *AttachRule) []attachTarget {
	if r.Via == AttachParent {
		p := c.parent(n)
		if p == nil || !c.mappedByRow(p.ID) || c.slots[slotKey(p.ID, r.Slot)] {
			return nil
		}
		return []attachTarget{{into: p.ID, slot: r.Slot}}
	}
	var out []attachTarget
	for _, e := range c.incoming[n.ID] {
		if e.Kind == EdgeDepends || !c.mappedByRow(e.From) {
			continue
		}
		slot := slotAt(c.out[e.From].Row, e.Path)
		if slot == "" || c.slots[slotKey(e.From, slot)] {
			continue
		}
		i := slices.IndexFunc(out, func(t attachTarget) bool { return t.into == e.From && t.slot == slot })
		if i < 0 {
			out = append(out, attachTarget{into: e.From, slot: slot})
			i = len(out) - 1
		}
		out[i].paths = append(out[i].paths, e.Path)
	}
	return out
}

// mappedByRow reports whether the node id is an instance a row maps.
func (c *classifier) mappedByRow(id string) bool {
	cl := c.out[id]
	return cl != nil && cl.Class == ClassMapped && cl.Row != nil
}

// slotKey is the key of the slot of the instance of the node into.
func slotKey(into, slot string) string {
	return into + "\x00" + slot
}

// readingKey is the key of the read of the node to by the node from at path.
func readingKey(from string, path []string, to string) string {
	return from + "\x00" + strings.Join(path, "\x1f") + "\x00" + to
}

// attach writes, for each node an attach rule takes, the attachments it is
// written as, its decision, and the resource-folded record naming each. The
// instances taking it are ordered as the Aggregate writes them, the slots of
// one in the order its edges are listed. Where the rule's Others is set and
// several instances take the node, the first takes Feature, its values.name
// pinned, and each other the kit's referenced secret whose origin is that
// name; where Secret is set, each takes the kit's referenced secret.
func (c *classifier) attach(ordered []*Classified) {
	pos := map[string]int{}
	for i, cl := range ordered {
		if cl == nil || cl.Node == nil {
			continue
		}
		if _, met := pos[cl.Node.ID]; !met {
			pos[cl.Node.ID] = i
		}
	}
	for _, n := range c.nodes {
		at, ok := c.attaching[n.ID]
		if !ok {
			continue
		}
		r, targets := at.rule, slices.Clone(at.targets)
		sort.SliceStable(targets, func(i, j int) bool { return pos[targets[i].into] < pos[targets[j].into] })
		name := ""
		if !r.Secret && r.Others != "" && len(targets) > 1 {
			if name = c.literalPlaced(n, r, attachName); name == "" {
				name = c.f.Keys([]*Node{n})[0]
			}
		}
		first := c.out[targets[0].into].Key
		cl := c.out[n.ID]
		var phrases []string
		for i, t := range targets {
			a := Attachment{Into: t.into, Slot: t.slot, Feature: r.Feature}
			where := " on the instance " + c.out[t.into].Key + " at the slot " + t.slot
			switch {
			case r.Secret:
				s := c.attachedSecret(n, t.slot, c.literalPlaced(n, r, attachOrigin), "")
				a.Feature, a.Secret = s.Form(c.in.Target).Type, &s
				where += ", attached by reference in its Release entry"
			case i > 0 && r.Others != "":
				s := c.attachedSecret(n, t.slot, name, ", which the instance "+draftyaml.Comment(first)+" attaches as "+
					r.Feature)
				a.Feature, a.Secret = r.Others, &s
				where += ", attached by reference in its Release entry, its origin the name the attachment on the " +
					"instance " + first + " pins"
			default:
				a.Values, a.Name = r.Values, name
			}
			cl.Attach = append(cl.Attach, a)
			phrases = append(phrases, a.Feature+where)
		}
		cl.Decision = decisionFor(n, joinNonEmpty("; ", "attached: the format's attach rule of "+r.Type+
			WhenText(r.When)+" writes it as "+andList(phrases)+", and nothing is drafted for it", c.says(n)))
		c.note(n, Report(CodeResourceFolded, fmt.Sprintf("%s (%s) is attached as %s; nothing is drafted for it", n.ID,
			n.Type, andList(phrases))))
	}
}

// literalPlaced returns the text n writes as a literal scalar where the
// placement of r keyed key reads it, empty where r places nothing under key or
// n writes no such literal there.
func (c *classifier) literalPlaced(n *Node, r *AttachRule, key string) string {
	for _, p := range r.Values {
		if p.Key != key {
			continue
		}
		if prop, ok := nodeProp(n, p.From); ok {
			if text, st := literalText(literalJSON(prop.Value)); st == stateRead {
				return text
			}
		}
		return ""
	}
	return ""
}

// attachedSecret is the kit's referenced secret attached at slot for the node
// n, its origin origin, more completing what it is.
func (c *classifier) attachedSecret(n *Node, slot, origin, more string) importer.Secret {
	return importer.Secret{Path: "features." + slot, Variable: variableOf(n.ID), Referenced: true, Origin: origin,
		Text: "the secret " + draftyaml.Comment(n.ID) + " the source declares" + more}
}
