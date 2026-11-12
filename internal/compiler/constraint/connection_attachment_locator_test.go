// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package constraint

import (
	"testing"
)

// TestConnectionAttachmentLocatorsExtendTheirEntrysLocator proves the two
// sites naming an attachment a connection carries locate it at the connection
// entry the traversals position a rule at, followed by the attachment's own
// keys — for every shape the shared fixture covers: an entry in a boundary the
// author named, one reached through a routing entry, one in the boundary
// normalization synthesizes, a component at the top level and one in a declared
// subsystem.
//
// The two are compared against the traversals' own output rather than against
// spelled-out strings, because the property is that one spelling serves both: a
// finding about an entry and a finding about an attachment it carries must name
// the same entry, and a locator assembled beside the traversal would agree with
// it today and be free to drift.
func TestConnectionAttachmentLocatorsExtendTheirEntrysLocator(t *testing.T) {
	t.Parallel()
	f := newTraversalFixture()

	t.Run("the Release's per-edge entries", func(t *testing.T) {
		t.Parallel()
		var locators []string
		capture := Check[traversalView, ReleaseEdgeConfigurationSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityFatal,
			Run: func(_ traversalView, s ReleaseEdgeConfigurationSubject) []Objection {
				locators = append(locators, s.Locator())
				return nil
			},
		}
		if _, err := RunOnReleaseEdgeConfigurations(nil, f, traversalView{}, capture); err != nil {
			t.Fatalf("the traversal must not fail over the fixture; got %v", err)
		}
		entries := f.ReleaseEdgeConfigurations()
		if len(entries) == 0 || len(locators) != len(entries) {
			t.Fatalf("the fixture yields %d entries and the traversal %d locators; both must be non-empty and equal",
				len(entries), len(locators))
		}
		for i, entry := range entries {
			if locators[i] == "" {
				t.Errorf("entry %d: the traversal named no locator, so this row proves nothing", i)
				continue
			}
			site := ConnectionAttachmentSiteInRelease(entry, "credential")
			if got, want := site.Locator(), locators[i]+".features.credential"; got != want {
				t.Errorf("entry %d: attachment locator = %q; want %q", i, got, want)
			}
			if site.SourceFile != entry.SourceFile {
				t.Errorf("entry %d: the attachment's site names file %q; want the entry's own %q",
					i, site.SourceFile, entry.SourceFile)
			}
		}
	})

	t.Run("a component's own connection entries", func(t *testing.T) {
		t.Parallel()
		var locators []string
		capture := Check[traversalView, EdgeSubject]{
			Sentinel: errRuleBroken,
			Severity: SeverityFatal,
			Run: func(_ traversalView, s EdgeSubject) []Objection {
				locators = append(locators, s.Locator())
				return nil
			},
		}
		if _, err := RunOnEdges(nil, f, traversalView{}, capture); err != nil {
			t.Fatalf("the traversal must not fail over the fixture; got %v", err)
		}
		edges := f.Edges()
		if len(edges) != len(locators) {
			t.Fatalf("the fixture yields %d connections and the traversal %d locators", len(edges), len(locators))
		}
		compared := 0
		for i, edge := range edges {
			// A connection the short form listed can carry no attachment, and its
			// locator stops at the socket, so it is not a shape this locator serves.
			if edge.SynthesizedAlias {
				continue
			}
			compared++
			site := ConnectionAttachmentSiteInAggregate(edge.Node, edge.Socket, edge.Alias, "credential")
			if got, want := site.Locator(), locators[i]+".features.credential"; got != want {
				t.Errorf("connection %d: attachment locator = %q; want %q", i, got, want)
			}
		}
		if compared == 0 {
			t.Fatal("the fixture yields no long-form connection, so this row proves nothing")
		}
	})
}

// connectionAttachmentSites is an attachment enumeration whose attachments are
// all written on connection entries, one per node, each at the site it carries.
type connectionAttachmentSites struct {
	nodes []NodeInstance
	sites map[string]AttachmentSite
}

func (c connectionAttachmentSites) Nodes() []NodeInstance { return c.nodes }

func (c connectionAttachmentSites) Attachments(node string) []string {
	if node == "plain" {
		return []string{"own"}
	}
	return []string{"gpt.credential"}
}

func (c connectionAttachmentSites) AttachmentSite(node, attachment string) (AttachmentSite, bool) {
	site, written := c.sites[node]
	if !written || attachment != "gpt.credential" {
		return AttachmentSite{}, false
	}
	return site, true
}

// TestNodeAttachmentsOnAConnectionArePositionedAtTheirEntry proves the traversal
// over the attachments a node carries positions an attachment written on a
// connection at that connection's entry, in the document and the file its site
// names, and leaves every other attachment at the node's own attachment block.
//
// Each row is one node carrying one attachment, so each locator is attributable
// to its own row; the plain node is the control holding an attachment the
// enumeration reports no site for.
func TestNodeAttachmentsOnAConnectionArePositionedAtTheirEntry(t *testing.T) {
	t.Parallel()

	developer := NodeInstance{ID: "gw", SourceFile: "aggregate.yaml", AuthoredName: "gw"}
	nested := NodeInstance{ID: "edge.gw", SourceFile: "aggregate.yaml", AuthoredName: "gw", SubsystemPath: "edge"}
	operator := NodeInstance{ID: "router", SourceFile: "aggregate.yaml", AuthoredName: "router"}
	plain := NodeInstance{ID: "plain", SourceFile: "aggregate.yaml", AuthoredName: "plain"}
	enumeration := connectionAttachmentSites{
		nodes: []NodeInstance{developer, nested, operator, plain},
		sites: map[string]AttachmentSite{
			"gw":      ConnectionAttachmentSiteInAggregate(developer, "models", "gpt", "credential"),
			"edge.gw": ConnectionAttachmentSiteInAggregate(nested, "models", "gpt", "credential"),
			"router": ConnectionAttachmentSiteInRelease(ReleaseEdgeConfigurationInstance{
				Configuration: ReleaseEdgeConfiguration{
					Document: AuthoringDocumentRelease,
					Node:     "router",
					Socket:   "models",
					Alias:    "gpt",
				},
				Site:          AuthoredSite{ScopePath: "cluster"},
				ComponentKeys: []string{"components", "router"},
				SourceFile:    "release.yaml",
			}, "credential"),
		},
	}

	type positioned struct{ file, locator string }
	var got []positioned
	capture := Check[traversalView, NodeAttachmentSubject]{
		Sentinel: errRuleBroken,
		Severity: SeverityFatal,
		Run: func(_ traversalView, s NodeAttachmentSubject) []Objection {
			got = append(got, positioned{file: s.SourceFile(), locator: s.Locator()})
			return nil
		},
	}
	if _, err := RunOnNodeAttachments(nil, enumeration, enumeration, traversalView{}, capture); err != nil {
		t.Fatalf("the traversal must not fail over the enumeration; got %v", err)
	}
	want := []positioned{
		{file: "aggregate.yaml", locator: ".spec.components.gw.uses.models.gpt.features.credential"},
		{file: "aggregate.yaml", locator: ".spec.subsystems.edge.components.gw.uses.models.gpt.features.credential"},
		{file: "release.yaml", locator: ".spec.boundaries.cluster.components.router.uses.models.gpt.features.credential"},
		{file: "aggregate.yaml", locator: ".spec.components.plain.features.own"},
	}
	if len(got) != len(want) {
		t.Fatalf("the traversal positioned %d attachment(s) %+v; want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attachment %d positioned at %+v; want %+v", i, got[i], want[i])
		}
	}
}
