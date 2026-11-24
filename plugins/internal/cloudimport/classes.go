// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Class is what the core makes of a node of a source, and so what the draft
// writes for it.
type Class int

// The classes. A node takes the first of folded, substrate, existing, mapped
// and drafted that applies to it; a fold or a type rule carrying none of it
// makes it not carried, and a node whose drafted Component would take a name
// the catalog holds already is held back.
const (
	// ClassFolded is a child or extension resource a fold of another node's
	// row writes into that node's instance, or a parent a fold writes into
	// each instance its children take.
	ClassFolded Class = iota + 1
	// ClassSubstrate is a resource of a type the format's type rules leave to
	// the operator: nothing is drafted for it, and a value read from it is a
	// pointer the Release holds.
	ClassSubstrate
	// ClassExisting is a resource the source reads and does not create, or
	// references and does not declare: nothing is drafted for it, and a value
	// read from it is a pointer the Release holds.
	ClassExisting
	// ClassMapped is a resource the importer kit maps to a catalog Component:
	// an instance referencing that Component.
	ClassMapped
	// ClassDrafted is a resource no catalog Component maps: an instance of a
	// Component and a Recipe drafted from the resource as the source writes it.
	ClassDrafted
	// ClassHeldBack is a resource whose drafted Component would take a name
	// the catalog holds and this import did not draft: it is neither drafted
	// nor referenced.
	ClassHeldBack
	// ClassNotCarried is a resource no Recipe takes: a fold carrying none of
	// it or taking one value only, or a type the format's type rules carry
	// none of.
	ClassNotCarried
)

// String names the class as a decision's rationale opens with it.
func (c Class) String() string {
	switch c {
	case ClassFolded:
		return "folded"
	case ClassSubstrate:
		return "substrate"
	case ClassExisting:
		return "existing"
	case ClassMapped:
		return "mapped"
	case ClassDrafted:
		return "drafted"
	case ClassHeldBack:
		return "held back"
	case ClassNotCarried:
		return "not carried"
	}
	return "class " + strconv.Itoa(int(c))
}

// TypeRule is a format's rule for the resources of one type, matched
// regardless of case. A rule of ClassSubstrate or ClassNotCarried classes each
// node of the type no fold takes; a rule of any other class leaves the node to
// be classed as any other is. Says is what the decision on a node of the type
// says beside its class and rule, and for a substrate or not-carried resource,
// what the warning naming it says.
type TypeRule struct {
	Type  string
	Class Class
	Says  string
}

// Classified is one node of a graph with its class, its decision and what the
// draft writes for it.
type Classified struct {
	// Node is the node: one of the graph's, or for a resource the source
	// references and does not declare, a node of the referenced ID marked
	// Existing, holding no property.
	Node  *Node
	Class Class
	// Row is the row a mapped node is an instance of, read in the node's
	// form; nil for a node a mapping_overrides entry maps to a Component no
	// row of its type names. For a folded node it is the row of the instance
	// it is written into.
	Row *Row
	// Into is the ID of the node whose instance a folded node is written into;
	// for a parent written into each instance its children take, the first of
	// them by ID.
	Into string
	// Name is the Component a mapped node references, or that a drafted or
	// held-back node's draft takes.
	Name     string
	Decision aggrega.ImportDecision
	// Entity is the reference to the Component a mapped node references, or
	// the Component drafted for a drafted node; empty for every other class,
	// and for a drafted node of a module form, whose Recipe sources the module
	// whole and is drafted with the format's module writer.
	Entity aggrega.EmittedEntity
	// Drafts are the Component and the Recipe drafted for a drafted node's
	// Name, held by the first node by ID of those taking it and empty on the
	// others, so the Drafts of a classification, laid out in order, draft each
	// Component once.
	Drafts []importer.CatalogDraft
}

// Classify gives each node of g one class and one decision, and each resource
// g references and does not declare the class existing. A node is classed by
// the first of these that takes it:
//
//  1. folded: a node a fold of the row of its nearest ancestor that is mapped
//     and folds its type writes into that ancestor's instance; the fold of a
//     row taking one value writes the first such node by ID, and a fold that
//     carries none of its type leaves each not carried. A parent whose row
//     makes each child of a type an instance of its own is written into each
//     such instance, and each such child is mapped as an instance of that row.
//  2. substrate, or not carried: a type the format's type rules leave to the
//     operator or carry none of;
//  3. existing: a node the source reads and does not create;
//  4. mapped: the importer kit's Decide references a Component for it, through
//     its row in the table or a mapping_overrides entry of its type;
//  5. drafted: any other node, drafted from the resource as the source writes
//     it; where the catalog holds the Component name its draft takes, it is
//     held back instead.
//
// The nodes of one Component name — two resources of one type no row maps —
// take one Component and one Recipe, drafted once with
// [schemadraft.DraftResource] from every property any of them writes: each
// property path once, matched regardless of case under the spelling the
// first node by ID writes it in, and a path inside an object another node
// writes whole read as that object. The draft's resource takes the type and
// apiVersion of the first node by ID, its name as its identity, the properties
// the format's rules place as placement, identity and secret so, every
// property a secure parameter or a credential feeds as a secret, and provides
// id and name. Each node is an instance of the Component with a decision of its
// own. A node of a module form is classed drafted and named, and nothing is
// drafted for it here: its Recipe sources the module whole and is drafted with
// the format's module writer. Where a draft halts, its nodes are classed
// drafted with nothing drafted, and the halt is returned.
//
// A child that is an instance of its own — mapped or drafted, not an instance
// a fold of its parent's row makes — gains its parent in its DependsOn, so the
// edge to its parent is a dependency and its parent is ordered before it. A
// parent, a reference or a dependency naming a repeated node names the
// instances g.Expanded records for it, and one deploying no instance names
// none. A child folds into a parent only where its parent names one node.
//
// Each decision's rationale opens with the class and names the rule that gave
// it. Folded children are reported resource-folded; a not-carried resource
// resource-not-carried; the substrate resources the source creates
// substrate-left-to-operator, in one warning listing each; a held-back
// resource component-exists, naming the Component and the mapping_overrides
// entry that maps the type to it; a node whose row depends on a value set when
// the source deploys discriminator-unresolved. The decision and the warning
// on a resource a type rule classes substrate or not carried name each node
// and parameter it reads, by the property path it reads it at; no message
// quotes a value the source writes.
//
// The table's rows are merged once: a table [Table.Rows] refuses halts
// source-refused, as do two nodes of g sharing one ID, and nothing is
// classified. The classified nodes are returned in [Order] of g, followed by
// the resources referenced and not declared, in ID order; every list is built
// in an order of IDs, so two runs over one graph give one result.
func Classify(in aggrega.ImportInput, f Format, g *Graph) ([]Classified, []aggrega.Diagnostic) {
	if g == nil {
		return nil, nil
	}
	rows, err := f.Table.Rows()
	if err != nil {
		return nil, append([]aggrega.Diagnostic(nil), tableRefused(err))
	}
	c := newClassifier(in, f, g, rows)
	if halts := c.sharedIDs(); len(halts) > 0 {
		return nil, halts
	}
	for _, n := range c.nodes {
		c.assess(n)
	}
	for _, n := range c.byDepth() {
		c.classify(n)
	}
	c.settleOneValueFolds()
	c.settleUnmatched()
	c.dependOnParents()
	undeclared := c.undeclared()
	return c.result(undeclared)
}

// classifier is one classification in progress.
type classifier struct {
	in aggrega.ImportInput
	f  Format
	g  *Graph
	// table holds the merged rows as its generated rows, so a node is
	// resolved without merging them again.
	table Table
	// nodes are g's nodes in ID order, and byID each by its ID.
	nodes []*Node
	byID  map[string]*Node
	// kids are the children of each node whose parent names it alone, in ID
	// order.
	kids map[string][]*Node
	// own is what each node is of itself, before any fold.
	own map[string]assessment
	// instances are the children a fold of their parent's row makes
	// instances of that row.
	instances map[string]instanceOf
	// ones are the nodes each fold taking one value takes, by parent and
	// type, and oneKeys their keys in the order first met.
	ones    map[string]*oneFold
	oneKeys []string
	// pending are the nodes no Component maps, drafted or held back once
	// every node is classed.
	pending map[string]bool
	out     map[string]*Classified
	// notes are the diagnostics naming one node, by its ID; diags those of a
	// draft.
	notes map[string][]aggrega.Diagnostic
	diags []aggrega.Diagnostic
}

// assessment is what a node is of itself: its type rule, its row and the
// importer kit's decision.
type assessment struct {
	rule *TypeRule
	// row is the row the table resolves, read in the node's form; could the
	// rows a value set when the source deploys chooses between.
	row   *Row
	could []Row
	// decision, entity and mapped are the importer kit's decision.
	decision aggrega.ImportDecision
	entity   aggrega.EmittedEntity
	mapped   bool
}

// classing reports whether the rule classes the node itself.
func (a assessment) classing() bool {
	return a.rule != nil && (a.rule.Class == ClassSubstrate || a.rule.Class == ClassNotCarried)
}

// byRow reports whether the importer kit references the Component of the
// node's own row.
func (a assessment) byRow() bool {
	return a.mapped && a.row != nil && a.entity.Name == a.row.Component
}

func newClassifier(in aggrega.ImportInput, f Format, g *Graph, rows []Row) *classifier {
	c := &classifier{in: in, f: f, g: g, table: Table{Generated: rows}, nodes: byID(g), byID: map[string]*Node{},
		kids: map[string][]*Node{}, own: map[string]assessment{}, instances: map[string]instanceOf{},
		ones: map[string]*oneFold{}, pending: map[string]bool{}, out: map[string]*Classified{},
		notes: map[string][]aggrega.Diagnostic{}}
	for _, n := range c.nodes {
		if _, held := c.byID[n.ID]; !held {
			c.byID[n.ID] = n
		}
	}
	for _, n := range c.nodes {
		if p := c.parent(n); p != nil {
			c.kids[p.ID] = append(c.kids[p.ID], n)
		}
	}
	return c
}

// sharedIDs halts source-refused on each ID two nodes share.
func (c *classifier) sharedIDs() []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for i := 1; i < len(c.nodes); i++ {
		if c.nodes[i].ID == c.nodes[i-1].ID && (i == 1 || c.nodes[i-2].ID != c.nodes[i].ID) {
			out = append(out, Report(CodeSourceRefused, fmt.Sprintf("two resources of the source take the ID %s, "+
				"as a copy of a repeated resource takes the ID of the resource it repeats followed by its key: "+
				"rename one of them in the source", c.nodes[i].ID)))
		}
	}
	return out
}

// tableRefused halts on a table whose rows cannot be merged.
func tableRefused(err error) aggrega.Diagnostic {
	return Report(CodeSourceRefused, "the importer's mapping table cannot be read, so no resource of the source "+
		"is classed: "+err.Error()+"; the table is the importer's own, so report it to the importer's authors")
}

// parents returns the nodes n's parent names: the node of that ID, or the
// instances of a repeated node; none where n names no parent, names itself
// or names an ID the source does not declare.
func (c *classifier) parents(n *Node) []*Node {
	if n.Parent == "" || n.Parent == n.ID {
		return nil
	}
	if p := c.byID[n.Parent]; p != nil {
		return []*Node{p}
	}
	var out []*Node
	for _, id := range c.g.Expanded[n.Parent] {
		if p := c.byID[id]; p != nil && p != n {
			out = append(out, p)
		}
	}
	return out
}

// parent returns the one node n's parent names, nil where it names none or
// several.
func (c *classifier) parent(n *Node) *Node {
	if ps := c.parents(n); len(ps) == 1 {
		return ps[0]
	}
	return nil
}

// declared reports whether the source declares id: a node of g, or a
// repeated node Expand replaced or removed.
func (c *classifier) declared(id string) bool {
	if c.byID[id] != nil {
		return true
	}
	_, expanded := c.g.Expanded[id]
	return expanded
}

// byDepth returns the nodes in the order they are classed: each after its
// ancestors, so a fold reads its ancestors' classes, ties by ID. A node whose
// parents run in a cycle is placed by the ancestors it has before the cycle
// repeats.
func (c *classifier) byDepth() []*Node {
	depth := make(map[*Node]int, len(c.nodes))
	for _, n := range c.nodes {
		seen := map[*Node]bool{n: true}
		for p := c.parent(n); p != nil && !seen[p]; p = c.parent(p) {
			seen[p] = true
			depth[n]++
		}
	}
	out := slices.Clone(c.nodes)
	sort.SliceStable(out, func(i, j int) bool { return depth[out[i]] < depth[out[j]] })
	return out
}

// typeRule returns the format's first rule of typ, nil where it has none.
func (c *classifier) typeRule(typ string) *TypeRule {
	for i, r := range c.f.Types {
		if r.Type != "" && strings.EqualFold(r.Type, typ) {
			return &c.f.Types[i]
		}
	}
	return nil
}

// assess records what n is of itself: its type rule and, where that rule
// does not class it and the source creates it, its row and the importer
// kit's decision.
func (c *classifier) assess(n *Node) {
	a := assessment{rule: c.typeRule(n.Type)}
	if !a.classing() && !n.Existing {
		row, could, ok := c.table.Resolve(n, c.g)
		if ok {
			r := row.For(n.Form)
			a.row = &r
		}
		a.could = could
		a.decision, a.entity, a.mapped = Decide(c.in, n, a.row, n.ID)
	}
	c.own[n.ID] = a
}

// classify classes n, its ancestors classed already.
func (c *classifier) classify(n *Node) {
	if inst, ok := c.instances[n.ID]; ok {
		c.instance(n, inst)
		return
	}
	if c.fold(n) {
		return
	}
	a := c.own[n.ID]
	switch {
	case a.classing():
		c.typed(n, a.rule)
	case n.Existing:
		c.set(n, &Classified{Class: ClassExisting, Decision: decisionFor(n, "existing: the source reads "+n.ID+
			" as a resource it does not create; nothing is drafted for it, and a value read from it is a "+
			"pointer the Release holds")})
	case a.mapped:
		c.unresolved(n, a)
		if !c.foldIntoInstances(n, a) {
			c.mapped(n, a)
		}
	default:
		c.unresolved(n, a)
		c.pending[n.ID] = true
		c.set(n, &Classified{Class: ClassDrafted})
	}
}

// set records n's class.
func (c *classifier) set(n *Node, cl *Classified) {
	cl.Node = n
	c.out[n.ID] = cl
}

// note records a diagnostic naming n, pointing at its source.
func (c *classifier) note(n *Node, d aggrega.Diagnostic) {
	if d.SourceRef == "" {
		d.SourceRef = n.Source
	}
	c.notes[n.ID] = append(c.notes[n.ID], d)
}

// typed classes n as its type rule does: substrate or not carried.
func (c *classifier) typed(n *Node, rule *TypeRule) {
	details := joinNonEmpty("; ", rule.Says, readsText(n))
	if rule.Class == ClassSubstrate {
		c.set(n, &Classified{Class: ClassSubstrate, Decision: decisionFor(n, joinNonEmpty("; ",
			"substrate: the format's type rule for "+n.Type+" leaves it to the operator; nothing is drafted for "+
				"it, and a value read from it is a pointer the Release holds", details))})
		return
	}
	c.set(n, &Classified{Class: ClassNotCarried, Decision: decisionFor(n, joinNonEmpty("; ",
		"not carried: the format's type rule for "+n.Type+" carries none of it", details))})
	c.note(n, Report(CodeResourceNotCarried, joinNonEmpty("; ", fmt.Sprintf("%s (%s) is not carried", n.ID, n.Type),
		details, "nothing is drafted for it, so where it is wanted, apply it to the deployed resources outside "+
			"the draft")))
}

// unresolved warns where which row maps n depends on a value set when the
// source deploys.
func (c *classifier) unresolved(n *Node, a assessment) {
	if a.row != nil || len(a.could) == 0 {
		return
	}
	var rows []string
	for _, r := range a.could {
		rows = append(rows, r.Component+" (the Recipe "+r.Recipe+")")
	}
	c.note(n, Report(CodeDiscriminatorUnresolved, fmt.Sprintf("%s (%s): which row maps it depends on a value the "+
		"source sets only when it deploys, with no default, so it is decided with no row; the rows it could take "+
		"map it to %s: give that value a default in the source, or map %s through the importer's "+
		"mapping_overrides setting", n.ID, n.Type, andList(rows), n.Type)))
}

// mapped classes n as an instance of the Component the importer kit
// references.
func (c *classifier) mapped(n *Node, a assessment) {
	d := a.decision
	d.SourceNode.ParamsSummary = summary(n)
	cl := &Classified{Class: ClassMapped, Name: a.entity.Name, Entity: a.entity}
	if a.byRow() {
		cl.Row = a.row
		d.Rationale = fmt.Sprintf("mapped: the row of %s%s (%s) maps it to %s, realized on %s by the Recipe %s",
			a.row.Key, whenText(a.row.When), origin(a.row), a.row.Component, c.f.Target, a.row.Recipe)
	} else {
		over := "no row of the table maps " + n.Type + ", and the mapping_overrides setting maps it to " + a.entity.Name
		if a.row != nil {
			over = "the mapping_overrides setting maps " + n.Type + " to " + a.entity.Name + " over the row of " +
				a.row.Key + ", which maps it to " + a.row.Component
		}
		d.Rationale = "mapped: " + over + ", so the Release entry's comment lists the properties the source " +
			"writes for the author to place: " + andList(writtenPaths(n))
	}
	d.Rationale = joinNonEmpty("; ", d.Rationale, c.says(n), a.decision.Rationale)
	cl.Decision = d
	c.set(n, cl)
}

// says is what n's type rule says beside a class it does not give.
func (c *classifier) says(n *Node) string {
	if a := c.own[n.ID]; a.rule != nil && !a.classing() {
		return a.rule.Says
	}
	return ""
}

// settleUnmatched holds back each node no Component maps whose draft's name
// the catalog holds, and drafts the others, once for each name.
func (c *classifier) settleUnmatched() {
	var names []string
	groups := map[string][]*Node{}
	for _, n := range c.nodes {
		if !c.pending[n.ID] {
			continue
		}
		name := schemadraft.NameFor(c.in, c.f.Draft, n.Type)
		switch {
		case c.holds(name):
			c.heldBack(n, name)
		case isModule(n.Form) || c.f.Draft.Write == nil:
			c.unwritten(n, name)
		default:
			if _, ok := groups[name]; !ok {
				names = append(names, name)
			}
			groups[name] = append(groups[name], n)
		}
	}
	for _, name := range names {
		c.draft(name, groups[name])
	}
}

// why is the rule that leaves n unmatched.
func (c *classifier) why(n *Node) string {
	a := c.own[n.ID]
	switch {
	case a.row != nil:
		return fmt.Sprintf("the row of %s maps it to %s, which the importer kit does not reference: %s", a.row.Key,
			a.row.Component, a.decision.Rationale)
	case len(a.could) > 0:
		return "which row of the table maps " + n.Type + " depends on a value the source sets only when it deploys"
	}
	return "no row of the table maps " + n.Type + ", and no mapping_overrides entry maps it to a Component the " +
		"catalog holds"
}

// unwritten classes n drafted under name with no draft written here: a node
// of a module form, whose Recipe the format's module writer drafts, or one of
// a format that writes no Recipe, or one whose draft halted.
func (c *classifier) unwritten(n *Node, name string) {
	d := c.own[n.ID].decision
	d.SourceNode.ParamsSummary = summary(n)
	tier := ""
	if d.Chosen != nil {
		tier = d.Chosen.Tier
	}
	d.Chosen = &aggrega.DecisionChoice{Target: name, Mode: "draft", Tier: tier}
	module := ""
	if isModule(n.Form) {
		module = "its Recipe sources the module whole, drafted with the format's module writer"
	}
	d.Rationale = joinNonEmpty("; ", "drafted: "+c.why(n), module, c.says(n))
	c.set(n, &Classified{Class: ClassDrafted, Name: name, Decision: d})
}

// draft drafts the Component name and its Recipe once for nodes, the nodes no
// Component maps that take it, in ID order.
func (c *classifier) draft(name string, nodes []*Node) {
	w, notes := c.written(nodes)
	drafted, diags := schemadraft.DraftResource(c.in, c.f.Draft, w)
	c.diags = append(c.diags, diags...)
	if halts(diags) || len(drafted.Entities) == 0 {
		for _, n := range nodes {
			c.unwritten(n, name)
		}
		return
	}
	for i, n := range nodes {
		d := drafted.Decision
		d.SourceNode.ID, d.SourceNode.Type = n.ID, n.Type
		d.Rationale = joinNonEmpty("; ", "drafted: "+c.why(n), notes, c.says(n), drafted.Decision.Rationale)
		entity := drafted.Entities[0].Entity
		entity.SourceRef, entity.DecisionRef = n.ID, "decision:"+n.ID
		cl := &Classified{Class: ClassDrafted, Name: drafted.Name, Decision: d, Entity: entity}
		if i == 0 {
			cl.Drafts = drafted.Entities
		}
		c.set(n, cl)
	}
}

// writtenProp is one property the nodes of one draft write.
type writtenProp struct {
	path             []string
	value            json.RawMessage
	secret, identity bool
}

// written returns the resource the Component and the Recipe of nodes are
// drafted from — every property any of them writes, as Classify states — and
// what the decision on each says of drafting them together.
func (c *classifier) written(nodes []*Node) (schemadraft.WrittenResource, string) {
	first := nodes[0]
	w := schemadraft.WrittenResource{ID: first.ID, Type: first.Type, APIVersion: first.APIVersion}
	var props []*writtenProp
	at := map[string]*writtenProp{}
	spelled := map[string]string{}
	add := func(path []string, v Value, identity bool) {
		path = spelling(spelled, path)
		raw, secret := c.typedValue(v)
		if p := at[foldedPath(path)]; p != nil {
			p.secret, p.identity = p.secret || secret, p.identity || identity
			return
		}
		p := &writtenProp{path: path, value: raw, secret: secret, identity: identity}
		at[foldedPath(path)] = p
		props = append(props, p)
	}
	for _, n := range nodes {
		if n.Name != nil {
			add([]string{"name"}, n.Name, true)
		}
		for _, l := range leaves(n.Body) {
			add(l.path, l.value, false)
		}
	}
	props = outermost(props)

	plain := schemadraft.WrittenResource{Type: w.Type, APIVersion: w.APIVersion}
	for _, p := range props {
		plain.Properties = append(plain.Properties, schemadraft.WrittenProperty{Path: p.path, Value: p.value})
	}
	typ, _ := schemadraft.FromWritten(plain)
	rules := c.f.Draft.Rules
	for _, p := range props {
		wp := schemadraft.WrittenProperty{Path: p.path, Value: p.value, Secret: p.secret, Identity: p.identity}
		if prop := propertyAt(typ, p.path); prop != nil {
			wp.Placement = rules.Placement != nil && rules.Placement(typ, prop)
			wp.Identity = wp.Identity || rules.Identity != nil && rules.Identity(typ, prop)
			wp.Secret = wp.Secret || rules.Secret != nil && rules.Secret(typ, prop)
		}
		if wp.Secret {
			wp.Value = nil
		}
		w.Properties = append(w.Properties, wp)
	}
	w.Provides = []schemadraft.Provide{
		{Key: "id", Attribute: []string{"id"}, Identifier: true},
		{Key: "name", Attribute: []string{"name"}},
	}
	return w, together(nodes)
}

// together says, for nodes drafted as one Component, that the draft carries
// every property any of them writes, and which apiVersion its Recipe deploys
// where they write several.
func together(nodes []*Node) string {
	if len(nodes) < 2 {
		return ""
	}
	ids := make([]string, len(nodes))
	var others []string
	for i, n := range nodes {
		ids[i] = n.ID
		if i > 0 && n.APIVersion != nodes[0].APIVersion {
			others = append(others, n.ID+" writes "+n.APIVersion)
		}
	}
	text := fmt.Sprintf("the Component and its Recipe are drafted once for %s, the resources of %s, from every "+
		"property any of them writes, so each instance's Release entry sets its own", andList(ids), nodes[0].Type)
	if len(others) > 0 {
		text += fmt.Sprintf("; the Recipe deploys the apiVersion %s writes, %s, where %s", nodes[0].ID,
			nodes[0].APIVersion, andList(others))
	}
	return text
}

// typedValue returns the JSON a drafted property is typed from for v, and
// whether v is a secret: a secure parameter's value, a credential, or a
// composition holding either. A parameter is typed by its declared type, a
// composition is text, a reference is typed as text, and deploy-time text and
// a property with no value are of any kind.
func (c *classifier) typedValue(v Value) (json.RawMessage, bool) {
	switch v := v.(type) {
	case Literal:
		return v.JSON, false
	case Param:
		p := c.param(v.Name)
		if p == nil {
			return nil, false
		}
		return zeroOf(*p), p.Secure
	case Credential:
		return nil, true
	case Compose:
		secret := false
		for _, part := range v.Parts {
			if _, s := c.typedValue(part); s {
				secret = true
			}
		}
		return json.RawMessage(`""`), secret
	case Ref:
		return nil, false
	}
	return json.RawMessage("null"), false
}

// param returns the parameter of g named name, nil where g declares none.
func (c *classifier) param(name string) *Parameter {
	for i := range c.g.Params {
		if c.g.Params[i].Name == name {
			return &c.g.Params[i]
		}
	}
	return nil
}

// zeroOf is the JSON a value of p's declared type is typed from: its literal
// default where its type names none of the source's scalar, object or array
// types.
func zeroOf(p Parameter) json.RawMessage {
	switch strings.ToLower(p.Type) {
	case "string", "securestring":
		return json.RawMessage(`""`)
	case "int", "integer", "number":
		return json.RawMessage("0")
	case "bool", "boolean":
		return json.RawMessage("false")
	case "object", "secureobject":
		return json.RawMessage("{}")
	case "array":
		return json.RawMessage("[]")
	}
	if l, ok := p.Default.(Literal); ok {
		return l.JSON
	}
	return nil
}

// spelling returns path with each segment spelled as the first path met
// through that place spelled it, recording the spelling of each place it
// meets first.
func spelling(spelled map[string]string, path []string) []string {
	out := make([]string, len(path))
	for i, seg := range path {
		place := foldedPath(path[:i+1])
		if first, ok := spelled[place]; ok {
			seg = first
		} else {
			spelled[place] = seg
		}
		out[i] = seg
	}
	return out
}

// foldedPath is a property path as one text, regardless of case.
func foldedPath(path []string) string {
	return strings.ToLower(strings.Join(path, "\x1f"))
}

// outermost returns props without each property inside an object another of
// them is, that object marked secret where a property inside it is.
func outermost(props []*writtenProp) []*writtenProp {
	var out []*writtenProp
	for _, p := range props {
		var outer *writtenProp
		for _, o := range props {
			if len(o.path) < len(p.path) && slices.EqualFunc(o.path, p.path[:len(o.path)], strings.EqualFold) &&
				(outer == nil || len(o.path) < len(outer.path)) {
				outer = o
			}
		}
		if outer == nil {
			out = append(out, p)
			continue
		}
		outer.secret = outer.secret || p.secret
	}
	return out
}

// propertyAt returns the property of t at path, nil where t has none.
func propertyAt(t *schemadraft.Type, path []string) *schemadraft.Property {
	if t == nil {
		return nil
	}
	for _, p := range t.Properties {
		if slices.Equal(p.Path, path) {
			return p
		}
	}
	return nil
}

// dependOnParents makes each child that is an instance of its own depend on
// the nodes its parent names.
func (c *classifier) dependOnParents() {
	for _, n := range c.nodes {
		cl := c.out[n.ID]
		if _, inst := c.instances[n.ID]; inst || cl == nil || (cl.Class != ClassMapped && cl.Class != ClassDrafted) {
			continue
		}
		var add []string
		for _, p := range c.parents(n) {
			if !slices.Contains(n.DependsOn, p.ID) && !slices.Contains(add, p.ID) {
				add = append(add, p.ID)
			}
		}
		if len(add) > 0 {
			n.DependsOn = append(slices.Clone(n.DependsOn), add...)
		}
	}
}

// undeclared returns a node, marked Existing, for each ID a reference, a
// credential, a dependency or a parent names that the source does not
// declare, in ID order, each classed existing.
func (c *classifier) undeclared() []*Node {
	referrers := map[string][]string{}
	var ids []string
	refer := func(to, from string) {
		if to == "" || c.declared(to) || slices.Contains(referrers[to], from) {
			return
		}
		if _, met := referrers[to]; !met {
			ids = append(ids, to)
		}
		referrers[to] = append(referrers[to], from)
	}
	for _, e := range edges(c.nodes) {
		refer(e.To, e.From)
	}
	for _, n := range c.nodes {
		if n.Parent != n.ID {
			refer(n.Parent, n.ID)
		}
	}
	slices.Sort(ids)
	out := make([]*Node, 0, len(ids))
	for _, id := range ids {
		from := referrers[id]
		slices.Sort(from)
		n := &Node{ID: id, Existing: true}
		if referrer := c.byID[from[0]]; referrer != nil {
			n.Source = referrer.Source
		}
		c.set(n, &Classified{Class: ClassExisting, Decision: decisionFor(n, fmt.Sprintf("existing: %s references "+
			"%s, which the source does not declare; nothing is drafted for it, and a value read from it is a "+
			"pointer the Release holds", andList(from), id))})
		out = append(out, n)
	}
	return out
}

// result returns the classified nodes in g's order followed by undeclared,
// and the diagnostics: those naming one node, in ID order, then those of the
// drafts, then the substrate the source creates.
func (c *classifier) result(undeclared []*Node) ([]Classified, []aggrega.Diagnostic) {
	var out []Classified
	for _, n := range append(Order(c.g), undeclared...) {
		out = append(out, *c.out[n.ID])
	}
	var diags []aggrega.Diagnostic
	for _, n := range c.nodes {
		diags = append(diags, c.notes[n.ID]...)
	}
	diags = append(diags, c.diags...)
	if d := c.substrate(); d != nil {
		diags = append(diags, *d)
	}
	return out, diags
}

// substrate warns of the substrate resources the source creates, which the
// draft leaves to the operator, in one warning listing each.
func (c *classifier) substrate() *aggrega.Diagnostic {
	var entries []string
	for _, n := range c.nodes {
		if c.out[n.ID].Class != ClassSubstrate || n.Existing {
			continue
		}
		entry := n.ID + " (" + n.Type + ")"
		if says := c.own[n.ID].rule.Says; says != "" {
			entry += ", of which the format says " + says
		}
		if reads := readsList(n); len(reads) > 0 {
			entry += ", reading " + andList(reads)
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil
	}
	d := Report(CodeSubstrateLeftToOperator, "the source creates substrate the draft leaves to the operator and "+
		"drafts nothing for: "+strings.Join(entries, "; ")+"; provide each where the Release deploys, and fill "+
		"the pointers the Release leaves for them")
	return &d
}

// decisionFor is the decision on n with rationale, naming no candidate and
// choosing none.
func decisionFor(n *Node, rationale string) aggrega.ImportDecision {
	return aggrega.ImportDecision{
		SourceNode: aggrega.DecisionSourceNode{ID: n.ID, Type: n.Type, ParamsSummary: summary(n)},
		Rationale:  rationale,
	}
}

// summary names the properties n writes, for its decision.
func summary(n *Node) string {
	paths := writtenPaths(n)
	if len(paths) == 0 {
		return "no property"
	}
	return "properties: " + strings.Join(paths, ", ")
}

// origin names where r comes from, for a rationale.
func origin(r *Row) string {
	if r.Origin == "" {
		return "the format's table"
	}
	return r.Origin
}

// leafAt is one leaf of a node's properties: the path it sits at and its
// value, nil for an object or array the source writes with nothing in it.
type leafAt struct {
	path  []string
	value Value
}

// leaves returns the leaves of body in its order: each member's and each
// item's leaves at their paths, an item's segment its index in decimal.
func leaves(body []Prop) []leafAt {
	var out []leafAt
	for _, p := range body {
		out = leavesOf(out, p, []string{p.Name})
	}
	return out
}

func leavesOf(out []leafAt, p Prop, path []string) []leafAt {
	switch {
	case p.Value != nil:
		return append(out, leafAt{path: path, value: p.Value})
	case len(p.Props) > 0:
		for _, m := range p.Props {
			out = leavesOf(out, m, extend(path, m.Name))
		}
	case len(p.Items) > 0:
		for i, item := range p.Items {
			out = leavesOf(out, item, extend(path, strconv.Itoa(i)))
		}
	default:
		out = append(out, leafAt{path: path})
	}
	return out
}

// readsList names each node and parameter n's properties read, with the
// property path each is read at.
func readsList(n *Node) []string {
	var out []string
	for _, l := range leaves(n.Body) {
		out = appendReads(out, l.value, pathText(l.path))
	}
	return out
}

func appendReads(out []string, v Value, at string) []string {
	switch v := v.(type) {
	case Ref:
		what := v.Node
		if len(v.Attr) > 0 {
			what += "'s " + pathText(v.Attr)
		}
		return append(out, what+" at "+at)
	case Credential:
		return append(out, "a credential of "+v.Node+" at "+at)
	case Param:
		return append(out, "the parameter "+v.Name+" at "+at)
	case Compose:
		for _, part := range v.Parts {
			out = appendReads(out, part, at)
		}
	}
	return out
}

// readsText says what n reads, empty where it reads nothing.
func readsText(n *Node) string {
	if reads := readsList(n); len(reads) > 0 {
		return "it reads " + andList(reads)
	}
	return ""
}

// andList joins items as a list in prose: a, b and c.
func andList(items []string) string {
	switch len(items) {
	case 0:
		return "none"
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// joinNonEmpty joins the parts that are not empty with sep.
func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

// halts reports whether diags hold a halt.
func halts(diags []aggrega.Diagnostic) bool {
	return slices.ContainsFunc(diags, func(d aggrega.Diagnostic) bool { return d.Severity == aggrega.SeverityHalt })
}
