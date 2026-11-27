// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
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

// The classes. A node takes the first of folded, attached, substrate,
// existing, mapped and drafted that applies to it, except that a node the
// source reads and does not create is never folded nor attached; a fold or a
// type rule carrying none of it makes it not carried, as the format locating
// no source makes a module; and a node whose drafted Component would take a
// name the catalog holds already, or a module whose drafted Component would
// take the name a resource of the import drafts, is held back.
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
	// the catalog holds and this import did not draft, or a module whose
	// drafted Component would take the name a resource of this import drafts:
	// it is neither drafted nor referenced.
	ClassHeldBack
	// ClassNotCarried is a resource no Recipe takes: a fold carrying none of
	// it or taking one value only, a type the format's type rules carry none
	// of, or a module no row maps whose source the format does not locate.
	ClassNotCarried
	// ClassAttached is a resource the format's attach rule writes as a Feature
	// attachment on another resource's instance, as [AttachRule] says: nothing
	// is drafted for it. It comes after folded in the order a node is classed.
	ClassAttached
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
	case ClassAttached:
		return "attached"
	}
	return "class " + strconv.Itoa(int(c))
}

// TypeRule is a format's rule for the resources of one type, matched
// regardless of case; a Type ending in * is the rule of every type the text
// before the * opens, such as AWS::EC2::*, and a node takes the first rule of
// the format's list matching its type. A rule of ClassSubstrate or
// ClassNotCarried classes each node of the type no fold takes; a rule of any
// other class leaves the node to be classed as any other is. Says is what the
// decision on a node of the type says beside its class and rule, and for a
// substrate or not-carried resource, what the warning naming it says.
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
	// the Component drafted for a drafted node; empty for every other class.
	Entity aggrega.EmittedEntity
	// Drafts are the Component and the Recipe drafted for a drafted node's
	// Name, held by the first node by ID of those taking it and empty on the
	// others, so the Drafts of a classification, laid out in order, draft each
	// Component once.
	Drafts []importer.CatalogDraft
	// Key is the instance key of a mapped or drafted node, [Keys] of those
	// nodes in the order Classify returns them; empty for every other class.
	Key string
	// Uses are the connections the instance of a mapped or drafted node draws
	// to other instances, in the order of their keys.
	Uses []Use
	// Comments are the comment lines written above the instance of a mapped or
	// drafted node: each held-back resource it reads, named.
	Comments []string
	// Inputs are the inputs of the Recipe drafted for a drafted node, each with
	// the pillar and the key it is read from: for a resource, those of the
	// Recipe its Drafts hold; for a module, each parameter any node drafted
	// with it sets, read from infra.overrides under its name or, where a
	// neighbour's value is delivered into it whole, off the inject pipe, less
	// each the Recipe binds itself and each a secret feeds.
	Inputs []schemadraft.Input
	// Recipe is the name of the Recipe drafted for a drafted node, empty for
	// every other node.
	Recipe string
	// Provides are what a drafted node's Recipe publishes, each attribute
	// under its provides key: for a resource its id and name, and for a
	// resource and a module alike each attribute another node references.
	Provides []schemadraft.Provide
	// Credentials are the credentials the instance of a mapped or drafted
	// node reads from other resources, in the order the source writes them.
	Credentials []CredentialRead
	// Attach are the attachments an attached node is written as, on the
	// instances taking it, in the order the Aggregate writes those instances;
	// empty for every other class.
	Attach []Attachment
}

// Classify gives each node of g one class and one decision, and each resource
// g references and does not declare the class existing. A node is classed by
// the first of these that takes it:
//
//  1. folded: a node whose properties the body of a node a row maps holds in
//     place of a reference to it (Node.ReadIn), written through that node's
//     row and drawing no edge of its own; a node a fold of the row of its
//     nearest ancestor that is mapped and folds its type writes into that
//     ancestor's instance; the fold of a
//     row taking one value writes the first such node by ID, and a fold that
//     carries none of its type leaves each not carried. A parent whose row
//     makes each child of a type an instance of its own is written into each
//     such instance, and each such child is mapped as an instance of that row.
//     A node the source reads and does not create is never folded: it is
//     substrate where a type rule says so, and existing otherwise.
//  2. attached: a node the format's attach rule of its type takes and an
//     instance takes, as [AttachRule] says, written as a Feature attachment on
//     that instance. Such a node is classed once every other node is, but those
//     whose ancestor it is, so a reader's class is known; its descendants are
//     classed after it, so a fold reads its class.
//  3. substrate, or not carried: a type the format's type rules leave to the
//     operator or carry none of;
//  4. existing: a node the source reads and does not create;
//  5. mapped: the importer kit's Decide references a Component for it, through
//     its row in the table or a mapping_overrides entry of its type;
//  6. drafted: any other node, drafted from the resource as the source writes
//     it, or for a module, as a Recipe sourcing it whole; where the catalog
//     holds the Component name its draft takes, it is held back instead.
//
// A node a row does not map, and whose reader kept its body as written
// (Node.AsWritten), takes that body once it is classed, and is wired and
// drafted from it.
//
// Each mapped and drafted node is an instance of the drafted Aggregate, keyed
// by the format's [Format.Keys] in the order Classify returns them, and the
// instances are wired as the wiring rule states before any Component is
// drafted: each instance's Uses, Comments and Credentials record what it
// draws, a reference not wired is reported reference-not-wired and a
// credential read credential-flow, on the node reading it. A read of an
// attached node at the credential slot its attachment fills draws nothing: the
// attachment carries it. Each attached node's Attach records what it is
// written as, once the instances are keyed, and its attachments are reported
// resource-folded, naming the Feature of each.
//
// The nodes of one Component name — two resources of one type no row maps —
// take one Component and one Recipe, drafted once with
// [schemadraft.DraftResource] from every property any of them writes: each
// property path once, matched regardless of case under the spelling the
// first node by ID writes it in, and a path inside an object another node
// writes whole read as that object. The draft's resource takes the type and
// apiVersion of the first node by ID, its name as its identity, the properties
// the format's rules place as placement, identity and secret so, every
// property a secure parameter or a credential feeds as a secret, each property
// a neighbour's value is delivered into read off the inject pipe, and provides
// its Provides. The nodes of a module form of one Component name take one
// Component and one Recipe sourcing the module whole, drafted once with
// [schemadraft.DraftWrapped] and the format's Wrap: the module the first node
// by ID calls, located by the format's Module at the version that node states,
// each parameter any of them sets read from infra.overrides under its name,
// matched regardless of case under the first node's spelling, or off the
// inject pipe where a neighbour's value is delivered into it whole, and each
// output another node reads provided. A parameter a secret feeds — a secure
// parameter, a credential, or a value the format's secret rule names — is no
// input of that Recipe: value-not-carried warns of it on each node setting it,
// naming the module, for the author to bind it in the Recipe. A module whose
// source the format's Module does not locate is not carried, warned
// linked-source-not-read; a module whose drafted Component would take the name
// a resource of the import drafts is held back, warned component-exists, since
// one Component takes one drafted Recipe. Each node is an instance of the
// Component with a decision of its own, and its Inputs are those of the
// drafted Recipe. A node no row maps for which the format has no writer — no
// Recipe writer for a resource, no module writer or no Module for a module —
// halts source-refused, the format being misconfigured, and nothing is
// classified. Where a draft halts, its nodes are classed drafted with nothing
// drafted, and the halt is returned.
//
// A child that is an instance of its own — mapped or drafted, not an instance
// a fold of its parent's row makes — gains its parent in its DependsOn, so the
// edge to its parent is a dependency and its parent is ordered before it. A
// parent, a reference or a dependency naming a repeated node names the
// instances g.Expanded records for it, and one deploying no instance names
// none. A child folds into a parent only where its parent names one node; a
// child whose parent repeats into several instances, or into none, is an
// instance of its own, and its decision says why.
//
// Each decision's rationale opens with the class and names the rule that gave
// it, and closes with what the format's reader says of the node (Node.Says,
// and where a row maps it, Node.SaysIfMapped, the reader's ReportsIfMapped
// then reported too). Folded children are reported resource-folded; a
// not-carried resource resource-not-carried, but a module whose source the
// format does not locate
// linked-source-not-read; the substrate resources the source creates
// substrate-left-to-operator, in one warning listing each; a resource the
// catalog holds back component-exists, naming the Component and the
// mapping_overrides entry that maps the type to it, built from the Component's
// labels in the catalog view, so the import applying it references the
// Component; a module held back beside a resource component-exists, naming the
// Component, the resources drafting it and the module's address; a node
// whose row depends on a value set when the source deploys, and no
// mapping_overrides entry maps, discriminator-unresolved. A type rule's Says
// is added to the decision on each node of its type, whatever its class. The
// decision and the warning on a resource a type rule classes substrate or not
// carried name each node and parameter it reads, by the property path it reads
// it at; no message quotes a value the source writes.
//
// The table's rows are merged once: a table [Table.Rows] refuses halts
// source-refused, as do an attach rule the core cannot apply and two nodes of
// g sharing one ID, and nothing is classified. The classified nodes are
// returned in [Order] of g, followed by
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
	if refused := attachRefused(f.Attach); refused != nil {
		return nil, append([]aggrega.Diagnostic(nil), *refused)
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
	c.settleAttachments()
	c.settleOneValueFolds()
	if refused := c.settleUnmatched(); refused != nil {
		return nil, append([]aggrega.Diagnostic(nil), *refused)
	}
	c.dependOnParents()
	undeclared := c.undeclared()
	c.wire(c.ordered(undeclared))
	c.draftGroups()
	return c.result(undeclared)
}

// classifier is one classification in progress.
type classifier struct {
	in aggrega.ImportInput
	f  Format
	g  *Graph
	// table holds the merged rows as its generated rows, and the format's
	// image rows, so a node is resolved without merging them again.
	table Table
	// inlined holds the nodes folded into a node whose body holds their
	// properties in place of a reference to them ([classifier.readIn]).
	inlined map[string]bool
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
	// groups are the nodes drafted together, one group for each Component
	// name and form, in the order first met, and groupOf each such node's.
	groups  []*draftGroup
	groupOf map[string]*draftGroup
	// located holds the source the format's Module gives each node of a
	// module form it locates, by the node's ID.
	located map[string]ModuleSource
	// cs reads the classes of the nodes as the wiring reaches them.
	cs  classLookup
	out map[string]*Classified
	// incoming are the edges to each node, by its ID, in the order [Edges]
	// lists them.
	incoming map[string][]Edge
	// deferred holds the attach rule of each node it may attach, classed once
	// every node but those awaiting it is; waiting holds the nodes awaiting an
	// ancestor so deferred, and queued lists them in the order they are
	// classed.
	deferred map[string]*AttachRule
	waiting  map[string]bool
	queued   []*Node
	// attaching holds where each attached node is attached, by its ID; slots
	// the slots of each instance an attachment fills, by slotKey; readings the
	// reads of an attached node at a slot its attachment fills, by readingKey.
	attaching map[string]attachedTo
	slots     map[string]bool
	readings  map[string]bool
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
	c := &classifier{in: in, f: f, g: g, table: Table{Generated: rows, Images: f.Table.Images}, nodes: byID(g),
		byID: map[string]*Node{}, kids: map[string][]*Node{}, own: map[string]assessment{},
		instances: map[string]instanceOf{}, ones: map[string]*oneFold{}, pending: map[string]bool{},
		groupOf: map[string]*draftGroup{}, inlined: map[string]bool{},
		located: map[string]ModuleSource{}, out: map[string]*Classified{}, notes: map[string][]aggrega.Diagnostic{},
		incoming: map[string][]Edge{}, deferred: map[string]*AttachRule{}, waiting: map[string]bool{},
		attaching: map[string]attachedTo{}, slots: map[string]bool{}, readings: map[string]bool{}}
	for _, n := range c.nodes {
		if _, held := c.byID[n.ID]; !held {
			c.byID[n.ID] = n
		}
	}
	for _, e := range edges(c.nodes) {
		c.incoming[e.To] = append(c.incoming[e.To], e)
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
				"and the draft keys each resource by its ID: rename one of them in the source", c.nodes[i].ID)))
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
		if r.matches(typ) {
			return &c.f.Types[i]
		}
	}
	return nil
}

// matches reports whether r is a rule of the type typ: its Type, matched
// regardless of case, or where it ends in *, the text before the * opening
// typ, matched so.
func (r TypeRule) matches(typ string) bool {
	if prefix, wild := strings.CutSuffix(r.Type, "*"); wild {
		return prefix != "" && len(typ) >= len(prefix) && strings.EqualFold(typ[:len(prefix)], prefix)
	}
	return r.Type != "" && strings.EqualFold(r.Type, typ)
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

// classify classes n, its ancestors classed already; it defers n where an
// attach rule may attach it, and where an ancestor of n is deferred.
func (c *classifier) classify(n *Node) {
	if c.waits(n) {
		c.waiting[n.ID] = true
		c.queued = append(c.queued, n)
		return
	}
	if inst, ok := c.instances[n.ID]; ok {
		c.instance(n, inst)
		return
	}
	if !n.Existing && (c.readIn(n) || c.fold(n)) {
		return
	}
	if r := c.attachRule(n); r != nil && c.mayAttach(n, r) {
		c.deferred[n.ID] = r
		return
	}
	c.classifyOwn(n)
}

// classifyOwn classes n as what it is of itself, no fold and no attach rule
// taking it.
func (c *classifier) classifyOwn(n *Node) {
	a := c.own[n.ID]
	if n.AsWritten != nil && (a.classing() || !a.byRow()) {
		n.Body = n.AsWritten
	}
	switch {
	case a.classing():
		c.typed(n, a.rule)
	case n.Existing:
		c.set(n, &Classified{Class: ClassExisting, Decision: decisionFor(n, joinNonEmpty("; ", "existing: the "+
			"source reads "+n.ID+" as a resource it does not create; nothing is drafted for it, and a value read "+
			"from it is a pointer the Release holds", c.says(n)))})
	case a.mapped:
		if !c.foldIntoInstances(n, a) {
			c.mapped(n, a)
		}
	default:
		c.unresolved(n, a)
		c.pending[n.ID] = true
		c.set(n, &Classified{Class: ClassDrafted})
	}
}

// readIn folds n into the first node of its ReadIn a row maps, whose body
// holds n's properties in place of a reference to it, and reports whether one
// does: n drafts nothing, and its decision and a resource-folded record name
// each such node.
func (c *classifier) readIn(n *Node) bool {
	var by []string
	var into string
	var row *Row
	for _, id := range n.ReadIn {
		p, a := c.byID[id], c.own[id]
		if p == nil || p.Existing || a.classing() || !a.byRow() {
			continue
		}
		by = append(by, id)
		if into == "" {
			into, row = id, a.row
		}
	}
	if into == "" {
		return false
	}
	c.inlined[n.ID] = true
	mapped := "an instance a row maps"
	if len(by) > 1 {
		mapped = "each an instance a row maps"
	}
	c.set(n, &Classified{Class: ClassFolded, Row: row, Into: into, Decision: decisionFor(n, joinNonEmpty("; ",
		"folded: read in place of the reference to it in the body of "+andList(by)+", "+mapped+", so its "+
			"properties are written through that row and nothing is drafted for it", c.says(n)))})
	c.note(n, Report(CodeResourceFolded, fmt.Sprintf("%s (%s) is read in place of the reference to it by %s, so "+
		"nothing is drafted for it", n.ID, n.Type, andList(by))))
	return true
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
			a.row.Key, WhenText(a.row.When), origin(a.row), a.row.Component, c.f.Target, a.row.Recipe)
	} else {
		over := "no row of the table maps " + n.Type + ", and the mapping_overrides setting maps it to " + a.entity.Name
		if a.row != nil {
			over = "the mapping_overrides setting maps " + n.Type + " to " + a.entity.Name + " over the row of " +
				a.row.Key + ", which maps it to " + a.row.Component
		}
		d.Rationale = "mapped: " + over + ", so the Release entry's comment lists the properties the source " +
			"writes for the author to place: " + andList(writtenPaths(n))
	}
	d.Rationale = joinNonEmpty("; ", d.Rationale, c.parentNote(n), c.says(n), a.decision.Rationale)
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

// draftGroup is the nodes no Component maps that take one Component name and
// one form, drafted once.
type draftGroup struct {
	name string
	// module is set for nodes of a module form, whose Recipe the format's
	// module writer drafts.
	module bool
	nodes  []*Node
	// props are the properties the nodes of a resource form write, as their
	// draft reads them.
	props []*writtenProp
	// provides are what the group's Recipe publishes.
	provides []schemadraft.Provide
	// injected holds the inject key of each property a neighbour's value is
	// delivered into, by its path as foldedPath writes it; injectKeys the path
	// of each such key, and injectPaths those paths in the order delivered.
	injected    map[string]string
	injectKeys  map[string][]string
	injectPaths [][]string
}

// settleUnmatched holds back each node no Component maps whose draft's name
// the catalog holds, classes not carried each module whose source the format
// does not locate, and groups the others by name and form, each group to be
// drafted once its instances are wired; a module whose name a resource takes
// is held back. It returns a halt where the format has no writer for a node it
// would draft.
func (c *classifier) settleUnmatched() *aggrega.Diagnostic {
	var unwritable []string
	for _, n := range c.nodes {
		if !c.pending[n.ID] {
			continue
		}
		name := schemadraft.NameFor(c.in, c.f.Draft, n.Type)
		held, holds := c.held(name)
		switch {
		case holds:
			c.heldBack(n, held)
		case isModule(n.Form) && (c.f.Wrap == nil || c.f.Module == nil), !isModule(n.Form) && c.f.Draft.Write == nil:
			unwritable = append(unwritable, n.ID+" ("+n.Type+")")
		case isModule(n.Form):
			c.groupModule(n, name)
		default:
			c.group(n, name)
		}
	}
	if len(unwritable) > 0 {
		d := Report(CodeSourceRefused, "the importer's format has no writer drafting "+andList(unwritable)+
			", which no row maps: a format drafting a resource from its source needs a Recipe writer, and one "+
			"keeping a module whole a module writer and a locator of the module; the format is the importer's "+
			"own, so report it to the importer's authors")
		return &d
	}
	c.holdBackShared()
	for _, grp := range c.groups {
		if !grp.module {
			grp.props = c.union(grp.nodes)
		}
	}
	return nil
}

// group adds n, drafted as the Component name, to the group of its name and
// form, and names it.
func (c *classifier) group(n *Node, name string) {
	module := isModule(n.Form)
	i := slices.IndexFunc(c.groups, func(g *draftGroup) bool { return g.name == name && g.module == module })
	if i < 0 {
		c.groups = append(c.groups, &draftGroup{name: name, module: module})
		i = len(c.groups) - 1
	}
	c.groups[i].nodes = append(c.groups[i].nodes, n)
	c.groupOf[n.ID] = c.groups[i]
	c.out[n.ID].Name = name
}

// draftGroups drafts the Component and the Recipe of each group once its
// instances are wired: from the resources of a resource group, and for a
// module group, sourcing its module whole.
func (c *classifier) draftGroups() {
	for _, grp := range c.groups {
		if grp.module {
			c.draftModule(grp)
			continue
		}
		c.draft(grp)
	}
}

// ordered returns the classified nodes in the order Classify returns them:
// g's nodes in Order, then undeclared.
func (c *classifier) ordered(undeclared []*Node) []*Classified {
	var out []*Classified
	for _, n := range append(Order(c.g), undeclared...) {
		out = append(out, c.out[n.ID])
	}
	return out
}

// parentNote says why n, a child whose parent repeats into several instances
// or into none, folds into no instance of it; empty for any other node.
func (c *classifier) parentNote(n *Node) string {
	ids, repeated := c.g.Expanded[n.Parent]
	if n.Parent == "" || n.Parent == n.ID || c.byID[n.Parent] != nil || !repeated || len(ids) == 1 {
		return ""
	}
	if len(ids) == 0 {
		return "its parent " + n.Parent + " deploys no instance, so it folds into none and is an instance of its own"
	}
	return fmt.Sprintf("its parent %s repeats as %s, so it folds into no one of them and is an instance of its "+
		"own, depending on each", n.Parent, andList(ids))
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

// unwritten classes n, whose draft halted, drafted under name with no draft
// written.
func (c *classifier) unwritten(n *Node, name string) {
	d := c.own[n.ID].decision
	d.SourceNode.ParamsSummary = summary(n)
	tier := ""
	if d.Chosen != nil {
		tier = d.Chosen.Tier
	}
	d.Chosen = &aggrega.DecisionChoice{Target: name, Mode: "draft", Tier: tier}
	d.Rationale = joinNonEmpty("; ", "drafted: "+c.why(n), c.parentNote(n), c.says(n))
	cl := c.drafted(n)
	cl.Name, cl.Decision = name, d
}

// drafted returns the classified node n, drafted, keeping what the wiring
// recorded of its instance.
func (c *classifier) drafted(n *Node) *Classified {
	cl := c.out[n.ID]
	if cl == nil {
		cl = &Classified{}
		c.set(n, cl)
	}
	cl.Class = ClassDrafted
	return cl
}

// draft drafts the Component and the Recipe of grp once, for its nodes, the
// nodes of a resource form no Component maps that take its name, in ID order.
func (c *classifier) draft(grp *draftGroup) {
	w, notes := c.written(grp)
	drafted, diags := schemadraft.DraftResource(c.in, c.f.Draft, w)
	c.adopt(grp, drafted, diags, notes)
}

// writtenProp is one property the nodes of one draft write.
type writtenProp struct {
	path             []string
	value            json.RawMessage
	secret, identity bool
}

// union returns every property any of nodes writes, as Classify states: the
// properties the Component and the Recipe of nodes are drafted from. A list
// every value in which, at any depth, is a scalar listedScalar admits — a
// list of scalars, or of objects such as a resource's tags — is one property
// at the list's path, typed from what it holds: its items and their members
// are no properties of their own.
func (c *classifier) union(nodes []*Node) []*writtenProp {
	var props []*writtenProp
	at := map[string]*writtenProp{}
	spelled := map[string]string{}
	add := func(path []string, raw json.RawMessage, secret, identity bool) {
		path = spelling(spelled, path)
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
			raw, secret := c.typedValue(n.Name)
			add([]string{"name"}, raw, secret, true)
		}
		for _, l := range leavesWith(n.Body, c.listedScalar) {
			raw, secret := c.typedLeaf(l)
			add(l.path, raw, secret, false)
		}
	}
	return outermost(props)
}

// listedScalar reports whether v, a value inside a list, is a scalar the list
// a draft reads whole holds: a literal string, number or boolean, the value
// of a parameter of a scalar type that is not secure, deploy-time text naming
// no secure parameter, a stored secret's reference, or a composition of
// these. A reference to another resource, a credential read from one and a
// value a secure parameter feeds are none: a connection delivers the first,
// and the draft keeps each other at its own path, its secret apart from the
// list's other values.
func (c *classifier) listedScalar(v Value) bool {
	switch v := v.(type) {
	case Literal:
		_, st := literalText(v.JSON)
		return st == stateRead
	case Param:
		p := c.param(v.Name)
		if p == nil || p.Secure {
			return false
		}
		_, st := literalText(zeroOf(*p))
		return st == stateRead
	case Opaque:
		return len(secureNamed(c.g, nil, v.Text)) == 0
	case Stored:
		return true
	case Compose:
		return !slices.ContainsFunc(v.Parts, func(part Value) bool { return !c.listedScalar(part) })
	}
	return false
}

// typedLeaf returns the JSON a drafted property is typed from for the leaf l,
// and whether l is a secret, as typedValue does; for a list read whole, as
// typedProp reads it.
func (c *classifier) typedLeaf(l leafAt) (json.RawMessage, bool) {
	if l.list == nil {
		return c.typedValue(l.value)
	}
	return c.typedProp(Prop{Items: l.list})
}

// typedProp returns the JSON a drafted property is typed from for p, read
// whole — its value's as typedValue types it, the object of its members' or
// the list of its items', null for a value typed from nothing — and whether a
// value in it is a secret.
func (c *classifier) typedProp(p Prop) (json.RawMessage, bool) {
	if p.Value != nil || len(p.Props) == 0 && len(p.Items) == 0 {
		raw, secret := c.typedValue(p.Value)
		if len(bytes.TrimSpace(raw)) == 0 {
			raw = json.RawMessage("null")
		}
		return raw, secret
	}
	var b bytes.Buffer
	secret := false
	start, end := byte('['), byte(']')
	parts := p.Items
	if len(p.Props) > 0 {
		start, end, parts = '{', '}', p.Props
	}
	b.WriteByte(start)
	for i, part := range parts {
		if i > 0 {
			b.WriteByte(',')
		}
		if start == '{' {
			b.Write(importer.String(part.Name))
			b.WriteByte(':')
		}
		raw, s := c.typedProp(part)
		secret = secret || s
		b.Write(raw)
	}
	b.WriteByte(end)
	return json.RawMessage(b.Bytes()), secret
}

// written returns the resource the Component and the Recipe of grp are
// drafted from — every property any of its nodes writes, each a neighbour's
// value is delivered into read off the inject pipe, and the group's provides —
// and what the decision on each node says of drafting them together.
func (c *classifier) written(grp *draftGroup) (schemadraft.WrittenResource, string) {
	nodes, props := grp.nodes, grp.props
	first := nodes[0]
	w := schemadraft.WrittenResource{ID: first.ID, Type: first.Type, APIVersion: first.APIVersion}
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
		if key := grp.injected[foldedPath(p.path)]; key != "" {
			wp.Inject, wp.Secret = key, false
		}
		if wp.Secret || wp.Inject != "" {
			wp.Value = nil
		}
		w.Properties = append(w.Properties, wp)
	}
	w.Provides = slices.Clone(grp.provides)
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
// composition is text, a reference is typed as text, a secret a store holds
// is the text referring to it, and deploy-time text and a property with no
// value are of any kind.
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
	case Stored:
		return json.RawMessage(`""`), false
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
// each node's decision closing with what the reader says of it (Node.Says,
// and where a row maps it Node.SaysIfMapped), and the diagnostics: those
// naming one node, in ID order, each followed where a row maps the node by
// what the reader reports of it so (Node.ReportsIfMapped), then those of the
// drafts, then the substrate the source creates.
func (c *classifier) result(undeclared []*Node) ([]Classified, []aggrega.Diagnostic) {
	var out []Classified
	for _, n := range append(Order(c.g), undeclared...) {
		cl := *c.out[n.ID]
		says := n.Says
		if mappedByRow(&cl) {
			says = append(slices.Clone(says), n.SaysIfMapped...)
		}
		if len(says) > 0 {
			cl.Decision.Rationale = joinNonEmpty("; ", append([]string{cl.Decision.Rationale}, says...)...)
		}
		out = append(out, cl)
	}
	var diags []aggrega.Diagnostic
	for _, n := range c.nodes {
		diags = append(diags, c.notes[n.ID]...)
		if mappedByRow(c.out[n.ID]) {
			diags = append(diags, n.ReportsIfMapped...)
		}
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
// value, nil for an object or array the source writes with nothing in it; or,
// read by leavesWith, a list read whole, its items under list.
type leafAt struct {
	path  []string
	value Value
	list  []Prop
}

// leaves returns the leaves of body in its order: each member's and each
// item's leaves at their paths, an item's segment its index in decimal.
func leaves(body []Prop) []leafAt {
	return leavesWith(body, nil)
}

// leavesWith returns the leaves of body as leaves does, but for each list
// every value in which, at any depth, is one whole holds, which is one leaf
// at the list's path holding its items; a nil whole reads no list whole.
func leavesWith(body []Prop, whole func(Value) bool) []leafAt {
	var out []leafAt
	for _, p := range body {
		out = leavesOf(out, p, []string{p.Name}, whole)
	}
	return out
}

func leavesOf(out []leafAt, p Prop, path []string, whole func(Value) bool) []leafAt {
	switch {
	case p.Value != nil:
		return append(out, leafAt{path: path, value: p.Value})
	case len(p.Props) > 0:
		for _, m := range p.Props {
			out = leavesOf(out, m, extend(path, m.Name), whole)
		}
	case len(p.Items) > 0:
		if wholeList(p.Items, whole) {
			return append(out, leafAt{path: path, list: p.Items})
		}
		for i, item := range p.Items {
			out = leavesOf(out, item, extend(path, strconv.Itoa(i)), whole)
		}
	default:
		out = append(out, leafAt{path: path})
	}
	return out
}

// wholeList reports whether whole holds of every value items hold, at any
// depth: each item a value whole holds, or an object or a list every member
// and item of which is one, none empty.
func wholeList(items []Prop, whole func(Value) bool) bool {
	if whole == nil {
		return false
	}
	for _, item := range items {
		if !wholeProp(item, whole) {
			return false
		}
	}
	return true
}

// wholeProp reports whether whole holds of every value p holds, as wholeList
// reads an item.
func wholeProp(p Prop, whole func(Value) bool) bool {
	switch {
	case p.Value != nil:
		return len(p.Props) == 0 && len(p.Items) == 0 && whole(p.Value)
	case len(p.Props) > 0:
		for _, m := range p.Props {
			if !wholeProp(m, whole) {
				return false
			}
		}
		return true
	case len(p.Items) > 0:
		return wholeList(p.Items, whole)
	}
	return false
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

// mappedByRow reports whether cl is a node a row maps.
func mappedByRow(cl *Classified) bool {
	return cl != nil && cl.Class == ClassMapped && cl.Row != nil
}
