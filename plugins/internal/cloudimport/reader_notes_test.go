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
// value its row places under infra.image, names the repository of an image
// row as that row's container product: its Component and Recipe, the image
// placed under the image row's key, or nowhere where it names none; any
// other image keeps the row.
func TestImageRowMapsAComputeNodeToItsProduct(t *testing.T) {
	row := *postgresRow()
	row.Key = toyServer
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
