// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// toyServerNode is a toy server writing version and location, mapped by the
// toy format's row.
func toyServerNode(id string) *Node {
	return &Node{ID: id, Type: toyServer, Form: FormRaw, Source: "main.json#/" + id,
		Body: []Prop{{Name: "location", Value: text("westeurope")}, {Name: "version", Value: text("16")}}}
}

// TestTypeRuleEndingInAStarRulesEveryTypeItOpens classes each node whose type
// the text before a type rule's * opens, regardless of case, by that rule,
// and leaves a type it does not open, and every type a bare * would name, to
// be classed as any other.
func TestTypeRuleEndingInAStarRulesEveryTypeItOpens(t *testing.T) {
	f := toyFormat(nil)
	f.Types = []TypeRule{{Type: "*", Class: ClassNotCarried}, {Type: "Toy.Network/*", Class: ClassSubstrate,
		Says: "the network is the operator's"}}
	g := &Graph{Format: "toy", Nodes: []*Node{
		{ID: "net", Type: "toy.network/VirtualNetworks", Form: FormRaw},
		{ID: "peer", Type: "Toy.Networking/peers", Form: FormRaw},
	}}
	cls, diags := Classify(toyInput(t, "toy", "", nil), f, g)
	noHalt(t, diags)
	if cl := classOf(t, cls, "net"); cl.Class != ClassSubstrate || !strings.Contains(cl.Decision.Rationale,
		"the network is the operator's") {
		t.Errorf("the network is classed %s, deciding %q, want substrate by the rule of Toy.Network/*", cl.Class,
			cl.Decision.Rationale)
	}
	if cl := classOf(t, cls, "peer"); cl.Class != ClassDrafted {
		t.Errorf("a type Toy.Network/* does not open is classed %s, want drafted", cl.Class)
	}
}

// TestReaderSaysCloseTheDecision closes the decision on a node with what the
// reader says of it, and, where a row maps it, with what it says of it so and
// what it reports of it so; a node no row maps takes neither of those.
func TestReaderSaysCloseTheDecision(t *testing.T) {
	notes := func(n *Node) *Node {
		n.Says = []string{"it is read so"}
		n.SaysIfMapped = []string{"its Recipe takes it so"}
		n.ReportsIfMapped = []aggrega.Diagnostic{Report(CodeValueNotCarried, n.ID+" writes what its Recipe never "+
			"takes")}
		return n
	}
	g := &Graph{Format: "toy", Nodes: []*Node{notes(toyServerNode("db")),
		notes(&Node{ID: "other", Type: "Toy.Other/things", Form: FormRaw})}}
	cls, diags := Classify(toyInput(t, "toy", "", nil), toyFormat(nil), g)
	noHalt(t, diags)
	if got := classOf(t, cls, "db").Decision.Rationale; !strings.HasSuffix(got,
		"; it is read so; its Recipe takes it so") {
		t.Errorf("the mapped node's decision is %q, want it to close with both sayings", got)
	}
	got := classOf(t, cls, "other").Decision.Rationale
	if !strings.HasSuffix(got, "; it is read so") || strings.Contains(got, "its Recipe takes it so") {
		t.Errorf("the drafted node's decision is %q, want it to close with what the reader says alone", got)
	}
	var reported []string
	for _, d := range diags {
		if strings.Contains(d.Message, "never takes") {
			reported = append(reported, d.Message)
		}
	}
	if !slices.Equal(reported, []string{"db writes what its Recipe never takes"}) {
		t.Errorf("the reports of a mapped node are %q, want the mapped node's alone", reported)
	}
}

// TestAttachLaterReaderAdmittingAnotherFeatureIsDecidedHalting decides a
// reader after the first, taking the kit's referenced secret, whose Recipe
// admits only another feature type at its slot, as halting there, naming the
// fix; a Recipe admitting any type, or the referenced secret itself, adds
// nothing.
func TestAttachLaterReaderAdmittingAnotherFeatureIsDecidedHalting(t *testing.T) {
	const halts = "the Recipe toy-postgres admits only core/random-password at the slot admin_password, where the " +
		"instance takes core/referenced-secret, so the build halts at that slot: give the instance a " +
		"core/random-password of its own"
	for _, c := range []struct {
		case_    string
		admitted func(recipe, slot string) (string, bool)
		says     bool
	}{
		{"admits one other", func(recipe, slot string) (string, bool) {
			return "core/random-password", recipe == "toy-postgres" && slot == "admin_password"
		}, true},
		{"admits any", func(string, string) (string, bool) { return "", false }, false},
		{"admits the referenced secret", func(string, string) (string, bool) {
			return "core/referenced-secret", true
		}, false},
	} {
		t.Run(c.case_, func(t *testing.T) {
			rule := generatedSecret()
			rule.Admitted = c.admitted
			cls, _ := attached(t, "cloudformation", []AttachRule{rule}, func() []*Node {
				return []*Node{toyReader("dbA", readsSecret("dbSecret"), "dbB"), toyReader("dbB", readsSecret("dbSecret")),
					toySecret("dbSecret", "orders-db", true)}
			})
			if got := strings.Contains(classOf(t, cls, "dbA").Decision.Rationale, halts); got != c.says {
				t.Errorf("the later reader's decision says the build halts: %v, want %v: %q", got, c.says,
					classOf(t, cls, "dbA").Decision.Rationale)
			}
			if strings.Contains(classOf(t, cls, "dbB").Decision.Rationale, "halts") {
				t.Errorf("the first reader's decision says the build halts: %q",
					classOf(t, cls, "dbB").Decision.Rationale)
			}
		})
	}
}

// TestInfraKeyWrittenWithDotsIsAPath writes a value an infra placement keyed
// storage.size places as the member size of infra.storage.
func TestInfraKeyWrittenWithDotsIsAPath(t *testing.T) {
	f := toyFormat(nil)
	row := f.Table.Generated[0]
	row.Infra = append(slices.Clone(row.Infra), Placement{From: []string{"size"}, Key: "storage.size",
		Conv: "unit:Gi"})
	f.Table.Generated = []Row{row}
	n := toyServerNode("db")
	n.Body = append(n.Body, Prop{Name: "size", Value: literal("20")})
	_, _, release, _, _ := released(t, toyInput(t, "toy", "", nil), f, &Graph{Format: "toy", Nodes: []*Node{n}})
	if want := "        storage:\n          size: \"20Gi\"\n"; !strings.Contains(release, want) {
		t.Errorf("the Release does not write\n%s\nin\n%s", want, release)
	}
}

// TestImageRowMapsAComputeNodeToItsProduct resolves a node whose image, the
// value its row, of a server or a worker, places under infra.image, names the
// repository of an image row as that row's container product: its Component
// and Recipe, the image placed under the image row's key, or nowhere where it
// names none; any other image keeps the row, and so does a row of a task.
func TestImageRowMapsAComputeNodeToItsProduct(t *testing.T) {
	row := *postgresRow()
	row.Key = toyServer
	row.Traits = map[string]string{"component.aggrega.ai/type": "server"}
	row.Infra = []Placement{{From: []string{"location"}, Key: "location"}, {From: []string{"image"}, Key: "image"}}
	node := func(image string) *Node {
		n := toyServerNode("app")
		n.Body = append(n.Body, Prop{Name: "image", Value: text(image)})
		return n
	}
	for _, c := range []struct {
		case_, image, imageKey, component, recipe string
		infra                                     []string
	}{
		{"its image key", "ghcr.io/acme/board:1.2.0", "image", "core/devtool/acme-board", "acme-board-ecs",
			[]string{"location", "image"}},
		{"no image key", "ghcr.io/acme/board@sha256:" + strings.Repeat("a", 64), "", "core/devtool/acme-board",
			"acme-board-ecs", []string{"location"}},
		{"another image", "ghcr.io/acme/other:1.0.0", "image", postgres, "postgres-flexible-server",
			[]string{"location", "image"}},
	} {
		t.Run(c.case_, func(t *testing.T) {
			table := Table{Generated: []Row{row}, Images: []ImageRow{{Repository: "ghcr.io/acme/board",
				Component: "core/devtool/acme-board", Recipe: "acme-board-ecs", ImageKey: c.imageKey}}}
			got, _, ok := table.Resolve(node(c.image), nil)
			if !ok || got.Component != c.component || got.Recipe != c.recipe {
				t.Fatalf("the node resolves to %s by %s (%v), want %s by %s", got.Component, got.Recipe, ok,
					c.component, c.recipe)
			}
			task := row
			task.Traits = map[string]string{"component.aggrega.ai/type": "task"}
			table.Generated = []Row{task}
			if got, _, _ := table.Resolve(node(c.image), nil); got.Recipe != row.Recipe {
				t.Errorf("a task's row resolves to %s by %s, want its own Recipe", got.Component, got.Recipe)
			}
			var keys []string
			for _, p := range got.Infra {
				keys = append(keys, p.Key)
			}
			if !slices.Equal(keys, c.infra) {
				t.Errorf("the row places infra %q, want %q", keys, c.infra)
			}
		})
	}
}

// readInGraph is a server whose body holds the properties of the toy thing
// part in place of its reference to it, the thing reading the server's
// network, and the node of type typ standing for the server where it is not
// one the toy row maps.
func readInGraph(typ string) *Graph {
	written := []Prop{{Name: "location", Value: text("westeurope")}, {Name: "part", Value: Ref{Node: "part"}}}
	app := &Node{ID: "app", Type: typ, Form: FormRaw, Source: "main.json#/app", AsWritten: written,
		Body: []Prop{{Name: "location", Value: text("westeurope")}, {Name: "version", Value: text("16")},
			{Name: "part", Props: []Prop{{Name: "size", Value: literal("3")}, {Name: "net", Value: Ref{Node: "net"}}}}}}
	part := &Node{ID: "part", Type: "Toy.Parts/parts", Form: FormRaw, Source: "main.json#/part", ReadIn: []string{"app"},
		Body: []Prop{{Name: "size", Value: literal("3")}, {Name: "net", Value: Ref{Node: "net"}}}}
	net := &Node{ID: "net", Type: toyNetwork, Form: FormRaw, Source: "main.json#/net"}
	return &Graph{Format: "toy", Nodes: []*Node{app, net, part}}
}

// TestNodeReadInAMappedNodeIsFoldedAndDrawsNoEdge folds a node whose
// properties the body of a node a row maps holds in place of a reference to
// it into that node, reported resource-folded, drafting nothing and drawing
// no edge of its own; the mapped node keeps the body its row reads.
func TestNodeReadInAMappedNodeIsFoldedAndDrawsNoEdge(t *testing.T) {
	g := readInGraph(toyServer)
	cls, diags := Classify(toyInput(t, "toy", "", nil), toyFormat(nil), g)
	noHalt(t, diags)
	part := classOf(t, cls, "part")
	if part.Class != ClassFolded || part.Into != "app" || !strings.HasPrefix(part.Decision.Rationale,
		"folded: read in place of the reference to it in the body of app") {
		t.Fatalf("the part is classed %s into %q, deciding %q", part.Class, part.Into, part.Decision.Rationale)
	}
	if n := len(onlyCode(t, diags, CodeResourceFolded, 1)); n != 1 {
		t.Errorf("the part is reported resource-folded %d times", n)
	}
	if app := classOf(t, cls, "app"); app.Class != ClassMapped || len(app.Node.Body) != 3 {
		t.Errorf("the server is classed %s with the body %+v, want mapped, keeping the body its row reads",
			app.Class, app.Node.Body)
	}
	for _, d := range diags {
		if strings.Contains(d.Message, "part reads") {
			t.Errorf("the folded part draws an edge: %s", d.Message)
		}
	}
}

// TestNodeReadInADraftedNodeIsANodeOfItsOwn keeps a node whose properties the
// body of a node no row maps holds in place of a reference to it a node of its
// own, drafted from the source, and drafts the node reading it from its body
// as written, reading the node by reference.
func TestNodeReadInADraftedNodeIsANodeOfItsOwn(t *testing.T) {
	g := readInGraph("Toy.Apps/apps")
	cls, aggregate, _, _, _ := released(t, toyInput(t, "toy", "", nil), toyFormat(nil), g)
	if part := classOf(t, cls, "part"); part.Class != ClassDrafted {
		t.Errorf("the part is classed %s, want drafted, a node of its own", part.Class)
	}
	app := classOf(t, cls, "app")
	if app.Class != ClassDrafted || len(app.Node.Body) != 2 || app.Node.Body[1].Props != nil {
		t.Errorf("the app is classed %s with the body %+v, want drafted from its body as written", app.Class,
			app.Node.Body)
	}
	if block := instanceBlock(t, aggregate, "app"); !strings.Contains(block, "target: part") {
		t.Errorf("the app does not read the part by reference:\n%s", block)
	}
}
