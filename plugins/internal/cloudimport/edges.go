// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Use is one connection the instance of a mapped or drafted node draws to
// another instance of the drafted Aggregate. It is written under a socket and
// an alias both named for the target's key, so an instance's connections, each
// reaching another key, never share an alias.
type Use struct {
	// Target is the key of the instance the connection reaches, which names
	// its socket and its alias.
	Target string
	// Mechanism is the inject mechanism Entries are delivered on: env, into
	// the environment of a mapped instance, or property, into a property of a
	// node drafted from the source; empty where the connection delivers
	// nothing and states a dependency alone.
	Mechanism string
	// Entries are what the connection delivers, each read from a provides key
	// of the target, in the order the source writes them.
	Entries []draftyaml.Injected
	// Comments are the comment lines written above the connection: each
	// reference to the target the draft does not wire, and why.
	Comments []string
}

// CredentialRead is a credential an instance reads from another resource. The
// draft never carries its value.
type CredentialRead struct {
	// Path is the property path of the reading node the credential sits at.
	Path []string
	// From is the ID of the resource the credential is read from, and What the
	// source's call or output reading it.
	From, What string
	// Slot is the credential slot of the instance's Recipe the property is
	// placed in, on which the Release attaches core/referenced-secret, its
	// origin left for the operator to name where the credential is kept; empty
	// where no slot of the Recipe takes it.
	Slot string
}

// The wiring of the drafted Aggregate, decided before the Components and
// Recipes of the nodes no Component maps are drafted, since a drafted Recipe
// reads each value a neighbour delivers into one of its properties off the
// inject pipe and publishes each attribute another node reads of it.
//
// Every edge of the graph is resolved to the instances it joins. A consumer is
// the instance of a mapped or drafted node, or each instance a folded node is
// written into; any other node draws nothing. A producer is reached through
// its class: a mapped or drafted node is its own instance, a folded node the
// instance it is written into, or each instance written with it where its
// row's fold makes each of its children an instance of its own; a repeated
// node reaches each instance it deploys. A substrate or existing resource is a
// pointer the Release holds and draws no connection; a held-back resource is
// named in a comment above the consumer's instance; a node no instance carries
// draws nothing. An edge from an instance to a node written into that instance
// itself draws nothing.
//
// A dependency draws a connection carrying its target alone. A reference
// draws a connection delivering the producer's provides key for the attribute
// it reads, where the consumer reads it as one whole value: into an entry of
// a mapped instance's environment list on the env mechanism, under the
// variable's name — an item of the list, or a member of an environment listed
// as an object, named by its key, the list read off an instance a fold of its
// row makes of a child where the child writes one, and off the parent folded
// into it otherwise; into a property of a node drafted from the source on the
// property mechanism, under the snake_case of the property's path. Where the
// value cannot be delivered so — composed with other text, read into a
// property a mapped instance's Recipe reads no neighbour's value at, read of
// an attribute the producer publishes under no key, read of several
// instances, or refused by the format's writer — the connection states the
// dependency alone, a comment above it says why, and reference-not-wired warns
// on the consumer. A credential draws no connection: credential-flow warns,
// and the consumer's slot is left for a core/referenced-secret the Release
// attaches. A reference or a credential reading an attached node at the slot
// its attachment fills draws nothing: the attachment carries it.

// wire decides the instance keys, what each drafted node publishes, and the
// connections, deliveries and credential reads of every instance.
func (c *classifier) wire(ordered []*Classified) {
	var instances []*Node
	for _, cl := range ordered {
		if isInstance(cl) {
			instances = append(instances, cl.Node)
		}
	}
	for i, key := range c.f.Keys(instances) {
		c.out[instances[i].ID].Key = key
	}
	c.cs = newClasses(c.g, ordered)
	c.attach(ordered)

	all := edges(c.nodes)
	c.provide(all)
	var cands []candidate
	for _, e := range all {
		cands = c.wireEdge(e, cands)
	}
	c.settleInjects(cands)
	for _, cl := range ordered {
		slices.SortStableFunc(cl.Uses, func(a, b Use) int { return strings.Compare(a.Target, b.Target) })
	}
}

// isInstance reports whether cl is written as an instance of the drafted
// Aggregate: a node mapped to a Component or drafted from the source.
func isInstance(cl *Classified) bool {
	return cl != nil && cl.Node != nil && (cl.Class == ClassMapped || cl.Class == ClassDrafted)
}

// classLookup reads what the drafted Aggregate writes for each node of a graph,
// from its classified nodes.
type classLookup struct {
	g *Graph
	// byID holds each classified node by its ID, the first of an ID.
	byID map[string]*Classified
	// folding holds, for each node a row's fold makes each of its children an
	// instance of its own, the IDs of those instances, in ID order.
	folding map[string][]string
}

// newClasses reads the classified nodes of g.
func newClasses(g *Graph, classified []*Classified) classLookup {
	cs := classLookup{g: g, byID: map[string]*Classified{}, folding: map[string][]string{}}
	for _, cl := range classified {
		if cl == nil || cl.Node == nil {
			continue
		}
		if _, held := cs.byID[cl.Node.ID]; !held {
			cs.byID[cl.Node.ID] = cl
		}
		if cl.Class == ClassMapped && cl.Node.Parent != "" && instancesFold(cl.Row, cl.Node.Type) != nil {
			cs.folding[cl.Node.Parent] = append(cs.folding[cl.Node.Parent], cl.Node.ID)
		}
	}
	for _, ids := range cs.folding {
		slices.Sort(ids)
	}
	return cs
}

// instancesFold returns the fold of r making each child of typ an instance of
// r, nil where r has none.
func instancesFold(r *Row, typ string) *Fold {
	if r == nil {
		return nil
	}
	for i, f := range r.Folds {
		if f.Instances && f.Type != "" && strings.EqualFold(f.Type, typ) {
			return &r.Folds[i]
		}
	}
	return nil
}

// childFolds returns the folds of r writing a child of typ into the
// instance, in the order r holds them: each fold of the type but one making
// each child an instance and one carrying none of it. The folds of one type
// are the placements of one child, each writing what the child writes at its
// From under its Key, so a child is written by every one of them; whether the
// row takes every child of the type, one or none is the first fold's of the
// type, as the classifier's fold reads it.
func childFolds(r *Row, typ string) []Fold {
	if r == nil {
		return nil
	}
	var out []Fold
	for _, f := range r.Folds {
		if !f.Instances && f.NotCarried == "" && f.Type != "" && strings.EqualFold(f.Type, typ) {
			out = append(out, f)
		}
	}
	return out
}

// foldedAt names the keys the folds of r write a child of typ under, in the
// order r holds them, each once; empty where they write it under none.
func foldedAt(r *Row, typ string) string {
	var keys []string
	for _, f := range childFolds(r, typ) {
		if key := pathText(f.Key); key != "" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return andList(keys)
}

// into returns the IDs of the instances the folded node cl is written into:
// each instance its children take where its row's fold makes them instances of
// their own, and the one instance it is folded into otherwise.
func (cs classLookup) into(cl *Classified) []string {
	if ids := cs.folding[cl.Node.ID]; len(ids) > 0 {
		return ids
	}
	if cl.Into != "" {
		return []string{cl.Into}
	}
	return nil
}

// reach is what an ID a reference or a dependency names reaches in the drafted
// Aggregate.
type reach struct {
	// instances are the IDs of the instances it reaches.
	instances []string
	// child is set where it reaches a child folded into its parent's
	// instance, whose own attributes that instance publishes none of.
	child bool
	// held are the held-back resources it reaches.
	held []*Classified
	// none are the IDs it reaches that no instance carries.
	none []string
	// pointer is set where it reaches a substrate or existing resource.
	pointer bool
}

// reach returns what id reaches.
func (cs classLookup) reach(id string) reach {
	var r reach
	cs.reachInto(&r, id, true)
	return r
}

// reachInto adds what id reaches to r; expand reads a repeated node's
// instances.
func (cs classLookup) reachInto(r *reach, id string, expand bool) {
	cl := cs.byID[id]
	if cl == nil {
		ids, repeated := cs.expanded(id)
		switch {
		case repeated && !expand:
		case repeated && len(ids) == 0:
			r.none = append(r.none, id)
		case repeated:
			for _, inst := range ids {
				cs.reachInto(r, inst, false)
			}
		default:
			r.pointer = true
		}
		return
	}
	switch cl.Class {
	case ClassMapped, ClassDrafted:
		r.add(id)
	case ClassFolded:
		ids := cs.into(cl)
		if len(cs.folding[id]) == 0 {
			r.child = true
		}
		if len(ids) == 0 {
			r.none = append(r.none, id)
		}
		for _, inst := range ids {
			r.add(inst)
		}
	case ClassSubstrate, ClassExisting:
		r.pointer = true
	case ClassHeldBack:
		r.held = append(r.held, cl)
	default:
		r.none = append(r.none, id)
	}
}

// expanded returns the IDs of the instances the repeated node id deploys, and
// whether id is one.
func (cs classLookup) expanded(id string) ([]string, bool) {
	if cs.g == nil {
		return nil, false
	}
	ids, ok := cs.g.Expanded[id]
	return ids, ok
}

func (r *reach) add(id string) {
	if !slices.Contains(r.instances, id) {
		r.instances = append(r.instances, id)
	}
}

// providesKey returns the provides key under which cl's instance publishes the
// attribute attr of its resource, an empty attr naming its identifier: a key of
// its row's Provides for a mapped instance, of its own Provides for a drafted
// one; false where it publishes none.
func providesKey(cl *Classified, attr []string) (string, bool) {
	switch {
	case cl == nil:
	case cl.Class == ClassMapped && cl.Row != nil:
		for _, p := range cl.Row.Provides {
			if samePath(p.From, attr) {
				return p.Key, true
			}
		}
	case cl.Class == ClassDrafted:
		return providedKey(cl.Provides, attr)
	}
	return "", false
}

// providedKey returns the key of provides publishing attr, an empty attr
// naming the identifier.
func providedKey(provides []schemadraft.Provide, attr []string) (string, bool) {
	for _, p := range provides {
		if len(attr) == 0 && p.Identifier || len(attr) > 0 && samePath(p.Attribute, attr) {
			return p.Key, true
		}
	}
	return "", false
}

// provide gives each group of drafted nodes what its Recipe publishes: for a
// resource drafted from the source what the format's DraftProvides lists —
// where the format gives no list, its identifier as id and its name as name — then
// each attribute another node references of any node of the group, under the
// snake_case of the attribute's path, numbered where another attribute takes
// that key; for a module, each output another node references. Each node of a
// group publishes the group's keys.
func (c *classifier) provide(all []Edge) {
	for _, grp := range c.groups {
		if !grp.module {
			grp.provides = c.draftProvides()
		}
	}
	for _, e := range all {
		if e.Kind != EdgeRef {
			continue
		}
		targets := []string{e.To}
		if ids, repeated := c.cs.expanded(e.To); repeated && c.byID[e.To] == nil {
			targets = ids
		}
		for _, to := range targets {
			if grp := c.groupOf[to]; grp != nil && to != e.From {
				grp.provide(e.Attr)
			}
		}
	}
	for _, grp := range c.groups {
		for _, n := range grp.nodes {
			c.out[n.ID].Provides = grp.provides
		}
	}
}

// draftProvides returns, in a list of its own, what a Recipe drafted for a
// resource from the source publishes before the attributes its neighbours
// read: the format's DraftProvides, else the identifier as id and the
// attribute name as name.
func (c *classifier) draftProvides() []schemadraft.Provide {
	if c.f.DraftProvides == nil {
		return []schemadraft.Provide{
			{Key: "id", Attribute: []string{"id"}, Identifier: true},
			{Key: "name", Attribute: []string{"name"}},
		}
	}
	out := make([]schemadraft.Provide, len(c.f.DraftProvides))
	for i, p := range c.f.DraftProvides {
		p.Attribute = slices.Clone(p.Attribute)
		out[i] = p
	}
	return out
}

// provide publishes attr of the group's resource where it publishes it under
// no key yet. An attribute with an empty segment, or one whose path gives no
// key, is not published, nor a module's identifier.
func (grp *draftGroup) provide(attr []string) {
	if _, ok := providedKey(grp.provides, attr); ok || len(attr) == 0 || slices.Contains(attr, "") {
		return
	}
	base := schemadraft.SnakeKey(strings.Join(attr, "."))
	if base == "" {
		return
	}
	key := base
	for i := 2; slices.ContainsFunc(grp.provides, func(p schemadraft.Provide) bool { return p.Key == key }); i++ {
		key = base + "_" + strconv.Itoa(i)
	}
	grp.provides = append(grp.provides, schemadraft.Provide{Key: key, Attribute: slices.Clone(attr)})
}

// candidate is a reference a drafted node reads into one of its properties as
// one whole value, of an attribute its producer publishes: delivered on the
// property mechanism where every node of its group reads one there.
type candidate struct {
	node *Node
	// pk is the ID of the producer's instance and from its provides key.
	pk, from string
	path     []string
	read     reading
}

// reading is what a reference the draft does not wire reads, as a comment and a
// warning name it.
type reading struct {
	consumer, producer, variable string
	attr, path                   []string
	composed                     bool
}

// text says what r reads, each name read from the source written through q.
func (r reading) text(q func(string) string) string {
	what := "the identifier of " + q(r.producer)
	if len(r.attr) > 0 {
		what = q(r.producer) + "'s " + q(pathText(r.attr))
	}
	out := q(r.consumer) + " reads " + what + " at " + q(pathText(r.path))
	if r.variable != "" {
		out = q(r.consumer) + " sets the variable " + q(r.variable) + " at " + q(pathText(r.path)) + " from " + what
	}
	if r.composed {
		out += " composed with other text"
	}
	return out
}

// asIs writes a name into a warning as it is.
func asIs(s string) string { return s }

// wireEdge wires e into each instance consuming it, and returns cands with
// each reference a drafted node may read on the property mechanism added. A
// read of an attached node at the slot its attachment fills draws nothing.
func (c *classifier) wireEdge(e Edge, cands []candidate) []candidate {
	n := c.byID[e.From]
	cl := c.out[e.From]
	if n == nil || cl == nil || c.inlined[e.From] ||
		e.Kind != EdgeDepends && c.readings[readingKey(e.From, e.Path, e.To)] {
		return cands
	}
	consumers, direct := []string{e.From}, true
	switch {
	case cl.Class == ClassFolded:
		consumers, direct = c.cs.into(cl), false
	case !isInstance(cl):
		return cands
	}
	r := c.cs.reach(e.To)
	for _, ci := range consumers {
		if slices.Contains(r.instances, ci) {
			continue
		}
		switch e.Kind {
		case EdgeDepends:
			for _, pk := range r.instances {
				c.useOf(ci, pk)
			}
			for _, h := range r.held {
				c.heldComment(ci, h)
			}
		case EdgeCredential:
			c.credential(n, e, ci, direct)
		case EdgeRef:
			cands = c.reference(n, e, ci, direct, r, cands)
		}
	}
	return cands
}

// reference wires the reference e of n into the instance ci, or reports why it
// does not, and returns cands with it added where ci is a drafted node that may
// read it on the property mechanism.
func (c *classifier) reference(n *Node, e Edge, ci string, direct bool, r reach, cands []candidate) []candidate {
	value := valueAt(n.Body, e.Path)
	_, composed := value.(Compose)
	read := reading{consumer: n.ID, producer: e.To, attr: e.Attr, path: e.Path, composed: composed}
	consumer := c.out[ci]
	env, named := -1, false
	if (direct || c.envOfParent(consumer, n)) && consumer.Class == ClassMapped && consumer.Row != nil {
		env = envItem(consumer.Row.Env, e.Path)
		if env >= 0 {
			read.variable, named = envName(n.Body, consumer.Row.Env, e.Path)
		}
	}
	for _, h := range r.held {
		c.heldComment(ci, h)
		c.notWired(n, ci, nil, read, func(q func(string) string) string {
			return q(h.Node.ID) + " is held back, as the warning naming it says"
		})
	}
	if len(r.instances) == 0 {
		if len(r.none) > 0 {
			c.notWired(n, ci, nil, read, func(q func(string) string) string {
				return "the draft carries " + q(e.To) + " in no instance"
			})
		}
		if r.pointer && env >= 0 {
			c.notWired(n, ci, nil, read, func(q func(string) string) string {
				return q(e.To) + " is a resource the draft leaves to the operator, which the Release holds a pointer " +
					"to, and the Recipe of " + q(consumer.Name) + " takes the environment only through inject, so no " +
					"Release key holds the pointer"
			})
		}
		return cands
	}
	if len(r.instances) > 1 {
		c.notWired(n, ci, r.instances, read, func(q func(string) string) string {
			return q(e.To) + " deploys as " + andList(quoted(r.instances, q)) + ", and an inject delivers the value of one"
		})
		return cands
	}
	pk := r.instances[0]
	key, provided := providesKey(c.out[pk], e.Attr)
	provided = provided && !r.child
	unprovided := func(q func(string) string) string { return q(pk) + " publishes no provides key for it" }
	var why func(q func(string) string) string
	switch {
	case !direct && env < 0:
		why = func(q func(string) string) string {
			return q(n.ID) + " is written into the instance of " + q(ci) + ", whose Recipe reads no neighbour's value there"
		}
	case composed:
		why = func(func(string) string) string { return "an inject delivers a provides key whole" }
	case consumer.Class == ClassMapped && env >= 0 && !named:
		why = func(func(string) string) string { return "the variable's name is not written as text" }
	case consumer.Class == ClassMapped && env >= 0 && !provided:
		why = unprovided
	case consumer.Class == ClassMapped && env >= 0:
		c.deliver(ci, pk, draftyaml.MechanismEnv, draftyaml.Injected{In: read.variable, From: key})
		return cands
	case consumer.Class == ClassMapped:
		why = func(q func(string) string) string {
			return "the Recipe of " + q(consumer.Name) + " reads no neighbour's value there"
		}
	case !provided:
		why = unprovided
	case value == nil:
		why = func(func(string) string) string { return "the source writes no value there to deliver one into" }
	default:
		return append(cands, candidate{node: n, pk: pk, from: key, path: e.Path, read: read})
	}
	c.notWired(n, ci, []string{pk}, read, why)
	return cands
}

// envOfParent reports whether the instance consumer reads its environment
// list off n: n is the parent folded into consumer, an instance a fold of its
// row makes of a child, and the child writes no environment list, so the
// row's list is read off the parent, as its placements are.
func (c *classifier) envOfParent(consumer *Classified, n *Node) bool {
	if consumer == nil || consumer.Class != ClassMapped || consumer.Row == nil || consumer.Row.Env == nil ||
		instancesFold(consumer.Row, consumer.Node.Type) == nil || consumer.Node.Parent != n.ID {
		return false
	}
	_, written := propAt(consumer.Node.Body, consumer.Row.Env.Path)
	return !written
}

// quoted returns names each written through q.
func quoted(names []string, q func(string) string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = q(name)
	}
	return out
}

// settleInjects delivers on the property mechanism each reference a drafted
// node reads as one whole value into a property its draft carries, where every
// node of its group reads a neighbour's value there, so the drafted Recipe can
// read it off the inject pipe for each of its instances, and the format's
// writer can write it there; each other is not wired. Each property delivered
// is recorded on its group under its inject key, which no two properties of a
// group share.
func (c *classifier) settleInjects(cands []candidate) {
	for _, grp := range c.groups {
		var mine []candidate
		for _, cand := range cands {
			if c.groupOf[cand.node.ID] == grp {
				mine = append(mine, cand)
			}
		}
		for _, cand := range mine {
			path, why := c.injectable(grp, mine, cand)
			if why != nil {
				c.notWired(cand.node, cand.node.ID, []string{cand.pk}, cand.read, why)
				continue
			}
			grp.inject(path)
			c.deliver(cand.node.ID, cand.pk, schemadraft.MechanismProperty,
				draftyaml.Injected{In: schemadraft.InjectKey(path), From: cand.from})
		}
	}
	for _, grp := range c.groups {
		if !grp.module {
			continue
		}
		for _, n := range grp.nodes {
			c.out[n.ID].Inputs = grp.injectInputs()
		}
	}
}

// injectable returns the path of the property of grp's draft cand is delivered
// into, or why it is not delivered there.
func (c *classifier) injectable(grp *draftGroup, mine []candidate, cand candidate) ([]string, func(func(string) string) string) {
	path := cand.path
	if grp.module {
		if why := wholeParameter(path); why != nil {
			return nil, why
		}
	} else {
		i := slices.IndexFunc(grp.props, func(p *writtenProp) bool { return samePath(p.path, cand.path) })
		if i < 0 {
			return nil, func(func(string) string) string {
				return "the drafted Recipe writes the object holding it whole, from infra.overrides"
			}
		}
		path = grp.props[i].path
		t := &schemadraft.Type{Name: grp.nodes[0].Type, APIVersion: grp.nodes[0].APIVersion}
		prop := &schemadraft.Property{Name: path[len(path)-1], Path: path, Written: true}
		if why := c.f.Draft.Rules.WhyNotInjected(t, prop); why != "" {
			return nil, func(func(string) string) string { return why }
		}
	}
	for _, other := range grp.nodes {
		if slices.ContainsFunc(mine, func(o candidate) bool { return o.node == other && samePath(o.path, path) }) {
			continue
		}
		writes := " writes that property itself"
		if valueAt(other.Body, path) == nil && !holds(other.Body, path) {
			writes = " does not write that property"
		}
		return nil, func(q func(string) string) string {
			return q(other.ID) + ", another instance of the same drafted Component," + writes +
				", so the Recipe reads it from infra.overrides"
		}
	}
	key := schemadraft.InjectKey(path)
	if taken, ok := grp.injectKeys[key]; ok && !samePath(taken, path) {
		return nil, func(q func(string) string) string {
			return "another property it reads a neighbour's value at, " + q(pathText(taken)) +
				", takes the inject key " + q(key)
		}
	}
	return path, nil
}

// wholeParameter says why a neighbour's value read at path is not delivered
// into a module's parameter, nil where it is: a module's parameter is set
// whole, so a value is delivered into one only where the parameter is the
// value, at a path of one segment that names it.
func wholeParameter(path []string) func(func(string) string) string {
	switch {
	case len(path) == 0 || path[0] == "":
		return func(func(string) string) string {
			return "it is read at a member with no name, which sets no parameter of the module"
		}
	case len(path) > 1:
		return func(q func(string) string) string {
			return "the drafted Recipe sets the module's parameter " + q(path[0]) + " whole, from infra.overrides, " +
				"and delivers no neighbour's value inside it"
		}
	}
	return nil
}

// inject records the property at path as one a neighbour's value is delivered
// into.
func (grp *draftGroup) inject(path []string) {
	if grp.injected == nil {
		grp.injected, grp.injectKeys = map[string]string{}, map[string][]string{}
	}
	place := foldedPath(path)
	if _, done := grp.injected[place]; done {
		return
	}
	key := schemadraft.InjectKey(path)
	grp.injected[place] = key
	grp.injectKeys[key] = path
	grp.injectPaths = append(grp.injectPaths, path)
}

// injectInputs returns the inputs a module group's Recipe reads off the inject
// pipe: one for each property a neighbour's value is delivered into.
func (grp *draftGroup) injectInputs() []schemadraft.Input {
	var out []schemadraft.Input
	for _, path := range grp.injectPaths {
		p := &schemadraft.Property{Name: path[len(path)-1], Path: slices.Clone(path),
			Kinds: []schemadraft.Kind{schemadraft.KindString}, Required: true, Written: true}
		out = append(out, schemadraft.Input{Property: p, Required: true, Source: schemadraft.InputSource{
			Pillar: schemadraft.PillarInject, Key: schemadraft.InjectKey(path), Mechanism: schemadraft.MechanismProperty}})
	}
	return out
}

// credential records the credential e of n read into the instance ci, and
// warns credential-flow: the value is never carried, and the slot of a mapped
// instance's Recipe its property is placed in is left for a
// core/referenced-secret the Release attaches.
func (c *classifier) credential(n *Node, e Edge, ci string, direct bool) {
	consumer := c.out[ci]
	read := CredentialRead{Path: slices.Clone(e.Path), From: e.To, What: credentialWhat(valueAt(n.Body, e.Path), e.To)}
	if direct && consumer.Class == ClassMapped && consumer.Row != nil {
		for _, p := range consumer.Row.Secrets {
			if overlaps(p.From, e.Path) {
				read.Slot = p.Key
				break
			}
		}
	}
	consumer.Credentials = append(consumer.Credentials, read)

	what := "a credential of " + e.To
	if read.What != "" {
		what += " (" + read.What + ")"
	}
	var carried string
	switch {
	case read.Slot != "":
		carried = fmt.Sprintf("the Release attaches %s on the slot %s of the instance %s, its origin left for the "+
			"operator to name where the credential is kept", importer.Secret{Referenced: true}.Form(c.in.Target).Type,
			read.Slot, consumer.Key)
	case !direct:
		carried = fmt.Sprintf("%s is written into the instance of %s, whose Recipe takes it in no slot: supply it "+
			"where that instance is configured", n.ID, ci)
	case consumer.Class == ClassDrafted && isModule(consumer.Node.Form) && len(e.Path) > 0:
		carried = "the Recipe drafted for " + n.ID + " leaves out the module's parameter " + e.Path[0] + ", which " +
			"a secret feeds, for the author to bind in that Recipe"
	case consumer.Class == ClassDrafted:
		carried = "the Recipe drafted for " + n.ID + " reads the property as a secret, never as a value the " +
			"draft carries"
	default:
		carried = fmt.Sprintf("the Recipe of %s takes it in no slot: supply it where the instance %s is configured",
			consumer.Name, consumer.Key)
	}
	c.note(n, Report(CodeCredentialFlow, fmt.Sprintf("%s reads %s at %s, and the draft never carries a "+
		"credential's value: %s", n.ID, what, pathText(e.Path), carried)))
}

// credentialWhat is what reads the credential of the node to in v: the What of
// v, or of the first of its parts reading one of to.
func credentialWhat(v Value, to string) string {
	switch v := v.(type) {
	case Credential:
		return v.What
	case Compose:
		for _, part := range v.Parts {
			if cr, ok := part.(Credential); ok && cr.Node == to {
				return cr.What
			}
		}
	}
	return ""
}

// overlaps reports whether one of two paths begins with the other, segments
// compared regardless of case.
func overlaps(a, b []string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	return samePath(a, b[:len(a)])
}

// notWired reports the reference read as not wired into the instance ci, why
// saying why: a comment above each connection to pks, drawn for the dependency
// alone, and a reference-not-wired warning on n.
func (c *classifier) notWired(n *Node, ci string, pks []string, read reading, why func(func(string) string) string) {
	comment := "Not wired: " + read.text(draftyaml.Comment) + "; " + why(draftyaml.Comment) + "."
	var keys []string
	for _, pk := range pks {
		u := c.useOf(ci, pk)
		if !slices.Contains(u.Comments, comment) {
			u.Comments = append(u.Comments, comment)
		}
		keys = append(keys, c.out[pk].Key)
	}
	msg := read.text(asIs) + ", and the drafted Aggregate does not wire it: " + why(asIs)
	if len(keys) > 0 {
		msg += fmt.Sprintf("; the instance %s is connected to %s for the dependency alone, a comment above the "+
			"connection saying why", c.out[ci].Key, andList(keys))
	}
	c.note(n, Report(CodeReferenceNotWired, msg+"; write the value where the instance is configured"))
}

// heldComment names the held-back resource h above the instance ci, and why
// it is held back: the catalog holds the Component its draft takes, or, where
// the catalog does not, a resource of this import drafts that Component.
func (c *classifier) heldComment(ci string, h *Classified) {
	why := "the catalog holds the Component " + draftyaml.Comment(h.Name) + " its draft takes."
	if _, inCatalog := c.held(h.Name); !inCatalog {
		why = "the Component " + draftyaml.Comment(h.Name) + " its draft takes is the one this import drafts from a " +
			"resource."
	}
	comment := "Not connected to " + draftyaml.Comment(h.Node.ID) + ", held back: " + why
	if cl := c.out[ci]; !slices.Contains(cl.Comments, comment) {
		cl.Comments = append(cl.Comments, comment)
	}
}

// useOf returns the connection of the instance ci to the instance pk, drawn
// where it is not yet. The pointer is valid until the next connection ci
// draws.
func (c *classifier) useOf(ci, pk string) *Use {
	cl := c.out[ci]
	target := c.out[pk].Key
	i := slices.IndexFunc(cl.Uses, func(u Use) bool { return u.Target == target })
	if i < 0 {
		cl.Uses = append(cl.Uses, Use{Target: target})
		i = len(cl.Uses) - 1
	}
	return &cl.Uses[i]
}

// deliver delivers entry on mechanism through the connection of ci to pk,
// once.
func (c *classifier) deliver(ci, pk, mechanism string, entry draftyaml.Injected) {
	u := c.useOf(ci, pk)
	if u.Mechanism == "" {
		u.Mechanism = mechanism
	}
	if u.Mechanism == mechanism && !slices.Contains(u.Entries, entry) {
		u.Entries = append(u.Entries, entry)
	}
}

// envItem returns the index of the item of env whose value sits at path, -1
// where path is no item's value; for an environment listed as an object, 0
// where path is a member's value.
func envItem(env *EnvList, path []string) int {
	if env != nil && env.listsObject() {
		if len(path) != len(env.Path)+1 || !samePath(env.Path, path[:len(env.Path)]) {
			return -1
		}
		return 0
	}
	if env == nil || len(path) != len(env.Path)+2 || !samePath(env.Path, path[:len(env.Path)]) ||
		!strings.EqualFold(path[len(path)-1], env.Value) {
		return -1
	}
	i, err := strconv.Atoi(path[len(env.Path)])
	if err != nil || i < 0 || strconv.Itoa(i) != path[len(env.Path)] {
		return -1
	}
	return i
}

// envName returns the name of the variable whose value sits at path in body's
// environment list env, and whether it is written as text: for an
// environment listed as an object, the member's key.
func envName(body []Prop, env *EnvList, path []string) (string, bool) {
	if env.listsObject() {
		name := path[len(path)-1]
		return name, name != ""
	}
	item, ok := propAt(body, path[:len(path)-1])
	if !ok {
		return "", false
	}
	name, ok := child(item.Props, nil, env.Name)
	if !ok {
		return "", false
	}
	l, ok := name.Value.(Literal)
	if !ok {
		return "", false
	}
	text, st := literalText(l.JSON)
	if st != stateRead || text == "" {
		return "", false
	}
	return text, true
}

// valueAt returns the value body writes at path, nil where it writes none or an
// object or array there.
func valueAt(body []Prop, path []string) Value {
	if p, ok := propAt(body, path); ok {
		return p.Value
	}
	return nil
}

// holds reports whether body writes an object or array at path.
func holds(body []Prop, path []string) bool {
	p, ok := propAt(body, path)
	return ok && (len(p.Props) > 0 || len(p.Items) > 0)
}

// propAt returns the member or item body writes at path, each segment a
// member's name, matched as a predicate matches it, or an item's index; false
// where body writes none there.
func propAt(body []Prop, path []string) (Prop, bool) {
	if len(path) == 0 {
		return Prop{}, false
	}
	p := Prop{Props: body}
	for _, seg := range path {
		next, ok := child(p.Props, p.Items, seg)
		if !ok {
			return Prop{}, false
		}
		p = next
	}
	return p, true
}
