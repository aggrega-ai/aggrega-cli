// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// instanceOf is a child a fold of its parent's row makes an instance of that
// row, with the importer kit's decision on it.
type instanceOf struct {
	parent   *Node
	row      *Row
	fold     Fold
	decision aggrega.ImportDecision
	entity   aggrega.EmittedEntity
}

// oneFold is a fold taking one value, of one parent, and the nodes it takes.
type oneFold struct {
	parent *Node
	row    *Row
	fold   Fold
	nodes  []*Node
}

// fold folds n into the instance of its nearest ancestor that is mapped by a
// row folding n's type, and reports whether one does. A fold making each
// child an instance of its own is not one that folds the child: the parent is
// folded into the child instead.
func (c *classifier) fold(n *Node) bool {
	seen := map[*Node]bool{n: true}
	for a := c.parent(n); a != nil && !seen[a]; a = c.parent(a) {
		seen[a] = true
		at := c.out[a.ID]
		if at == nil || at.Class != ClassMapped || at.Row == nil {
			continue
		}
		for _, f := range at.Row.Folds {
			if f.Instances || f.Type == "" || !strings.EqualFold(f.Type, n.Type) {
				continue
			}
			switch {
			case f.NotCarried != "":
				c.notCarriedByFold(n, a, at.Row, f)
			case f.One:
				c.takeOne(n, a, at.Row, f)
			default:
				c.folded(n, a, at.Row, f)
			}
			return true
		}
	}
	return false
}

// folded writes n into a's instance at the key of each fold of row writing
// its type, f the first.
func (c *classifier) folded(n, a *Node, row *Row, f Fold) {
	at, by := foldedAt(row, n.Type), "the fold"
	if len(childFolds(row, n.Type)) > 1 {
		by = "the folds"
	}
	c.set(n, &Classified{Class: ClassFolded, Row: row, Into: a.ID, Decision: decisionFor(n, joinNonEmpty("; ",
		fmt.Sprintf("folded: written into the instance of %s at %s, by %s of %s in the row of %s (%s)", a.ID, at,
			by, f.Type, row.Key, row.Component), c.says(n)))})
	c.note(n, Report(CodeResourceFolded, fmt.Sprintf("%s (%s) is folded into the instance of %s, written at %s",
		n.ID, n.Type, a.ID, at)))
}

// notCarriedByFold leaves n not carried, by a fold of a's row that carries
// none of it.
func (c *classifier) notCarriedByFold(n, a *Node, row *Row, f Fold) {
	c.set(n, &Classified{Class: ClassNotCarried, Row: row, Decision: decisionFor(n, joinNonEmpty("; ",
		fmt.Sprintf("not carried: the fold of %s in the row of %s (%s) carries none of it: %s", f.Type, row.Key,
			row.Component, f.NotCarried), c.says(n)))})
	c.note(n, Report(CodeResourceNotCarried, fmt.Sprintf("%s (%s) is not carried: the Recipe of %s the instance "+
		"of %s takes carries none of it, since %s; nothing is drafted for it, so where it is wanted, set it on the "+
		"deployed resource outside the draft", n.ID, n.Type, row.Component, a.ID, f.NotCarried)))
}

// takeOne records n as taken by a fold of a's row taking one value, which
// settleOneValueFolds settles once every node is classed.
func (c *classifier) takeOne(n, a *Node, row *Row, f Fold) {
	key := a.ID + "\x00" + strings.ToLower(f.Type)
	one := c.ones[key]
	if one == nil {
		one = &oneFold{parent: a, row: row, fold: f}
		c.ones[key] = one
		c.oneKeys = append(c.oneKeys, key)
	}
	one.nodes = append(one.nodes, n)
	c.set(n, &Classified{Class: ClassFolded, Row: row, Into: a.ID})
}

// settleOneValueFolds folds the first node by ID each fold taking one value
// takes, and leaves each other not carried, naming the one written.
func (c *classifier) settleOneValueFolds() {
	for _, key := range c.oneKeys {
		one := c.ones[key]
		slices.SortStableFunc(one.nodes, func(x, y *Node) int { return strings.Compare(x.ID, y.ID) })
		first := one.nodes[0]
		c.folded(first, one.parent, one.row, one.fold)
		at := foldedAt(one.row, first.Type)
		for _, n := range one.nodes[1:] {
			c.set(n, &Classified{Class: ClassNotCarried, Row: one.row, Decision: decisionFor(n, joinNonEmpty("; ",
				fmt.Sprintf("not carried: the fold of %s in the row of %s takes one value, the first by ID, %s, "+
					"written into the instance of %s at %s", one.fold.Type, one.row.Key, first.ID, one.parent.ID,
					at), c.says(n)))})
			c.note(n, Report(CodeResourceNotCarried, fmt.Sprintf("%s (%s) is not carried: the instance of %s takes "+
				"one %s at %s, the first by ID, %s; where %s is wanted, write it as an instance of its own in the "+
				"Aggregate", n.ID, n.Type, one.parent.ID, one.fold.Type, at, first.ID, n.ID)))
		}
	}
}

// foldIntoInstances folds p, mapped by its own row, into each instance its
// row's folds make of its children, and reports whether any does. A child is
// such an instance where its type is that of a fold of the row making each
// child an instance, the source creates it, no type rule classes it, and the
// importer kit references the row's Component for it.
func (c *classifier) foldIntoInstances(p *Node, a assessment) bool {
	if !a.byRow() {
		return false
	}
	var made []*Node
	for _, kid := range c.kids[p.ID] {
		if kid.Existing || c.own[kid.ID].classing() {
			continue
		}
		for _, f := range a.row.Folds {
			if !f.Instances || f.Type == "" || !strings.EqualFold(f.Type, kid.Type) {
				continue
			}
			decision, entity, mapped := Decide(c.in, kid, a.row, kid.ID)
			if mapped && entity.Name == a.row.Component {
				c.instances[kid.ID] = instanceOf{parent: p, row: a.row, fold: f, decision: decision, entity: entity}
				made = append(made, kid)
			}
			break
		}
	}
	if len(made) == 0 {
		return false
	}
	ids := make([]string, len(made))
	for i, kid := range made {
		ids[i] = kid.ID
	}
	own := "each instance's Recipe creates its own " + p.Type
	c.set(p, &Classified{Class: ClassFolded, Row: a.row, Into: ids[0], Decision: decisionFor(p, joinNonEmpty("; ",
		fmt.Sprintf("folded: written into each instance its children of %s take, %s, by the row of %s (%s): %s",
			made[0].Type, andList(ids), a.row.Key, a.row.Component, own), c.says(p)))})
	c.note(p, Report(CodeResourceFolded, fmt.Sprintf("%s (%s) is folded into each instance its children of %s "+
		"take, %s: %s, where the source shares one", p.ID, p.Type, made[0].Type, andList(ids), own)))
	return true
}

// instance classes n as an instance of its parent's row, folding the parent.
func (c *classifier) instance(n *Node, inst instanceOf) {
	d := inst.decision
	d.SourceNode.ParamsSummary = summary(n)
	d.Rationale = joinNonEmpty("; ", fmt.Sprintf("mapped: an instance of the row of %s (%s), one for each %s of %s, "+
		"folding %s: each instance's Recipe creates its own %s", inst.row.Key, inst.row.Component, inst.fold.Type,
		inst.parent.ID, inst.parent.ID, inst.parent.Type), c.says(n), inst.decision.Rationale)
	c.set(n, &Classified{Class: ClassMapped, Row: inst.row, Name: inst.entity.Name, Entity: inst.entity, Decision: d})
}
