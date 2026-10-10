// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"testing"
)

// recordedSites is an enumeration of one node whose attachments are each written
// at the site the row records, and whose component type declares a slot under
// every attachment's name — so the slot axis is asked about exactly the
// attachments the attachment axis is.
type recordedSites struct {
	node    NodeInstance
	names   []string
	sites   map[string]AttachmentSite
	sockets map[string][]string
}

func (r recordedSites) Nodes() []NodeInstance { return []NodeInstance{r.node} }

func (r recordedSites) Attachments(string) []string { return r.names }

func (r recordedSites) Slots(string) []string { return r.names }

func (r recordedSites) AttachmentSockets(_, attachment string) []string { return r.sockets[attachment] }

func (r recordedSites) AttachmentSite(node, attachment string) (AttachmentSite, bool) {
	site, recorded := r.sites[attachment]
	if !recorded || node != r.node.ID {
		return AttachmentSite{}, false
	}
	return site, true
}

// positionedSubject is where a traversal positioned one subject: the file, the
// locator and the authoring document.
type positionedSubject struct{ file, locator, document string }

func positionsOf(objections []Objection) []positionedSubject {
	out := make([]positionedSubject, 0, len(objections))
	for _, o := range objections {
		out = append(out, positionedSubject{file: o.SourceFile(), locator: o.Locator(), document: o.AuthoringDocument()})
	}
	return out
}

// recordedSitesFixture is one node carrying one attachment written in each block
// that can write one, and one attachment no block was recorded for.
func recordedSitesFixture() recordedSites {
	app := NodeInstance{ID: "app", SourceFile: "aggregate.yaml", AuthoredName: "app"}
	return recordedSites{
		node:  app,
		names: []string{"own", "shared", "sized", "wide", "routed", "top", "plain"},
		sites: map[string]AttachmentSite{
			"own": AttachmentSiteInBlock(AuthoringDocumentAggregate,
				ComponentSite(app.AuthoredName, app.SubsystemPath, app.SynthesizedSubsystem), "own", "aggregate.yaml"),
			"shared": AttachmentSiteInBlock(AuthoringDocumentAggregate,
				AuthoredSite{ScopePath: "edge.inner"}, "shared", "aggregate.yaml"),
			"sized": AttachmentSiteInBlock(AuthoringDocumentRelease,
				AuthoredSite{ScopePath: "prod/eu", Component: "app"}, "sized", "release.yaml"),
			"wide": AttachmentSiteInBlock(AuthoringDocumentRelease,
				AuthoredSite{ScopePath: "prod"}, "wide", "release.yaml"),
			"routed": {
				Document:      AuthoringDocumentRelease,
				Site:          AuthoredSite{ScopePath: "prod"},
				ComponentKeys: []string{"aggregates", "data", "components", "db"},
				Name:          "routed",
				SourceFile:    "release.yaml",
			},
			"top": AttachmentSiteInBlock(AuthoringDocumentRelease,
				AuthoredSite{Component: "app"}, "top", "release.yaml"),
		},
		sockets: map[string][]string{"sized": {"db"}, "plain": {"cache"}},
	}
}

// recordedSitesWant is where each of the fixture's attachments is positioned:
// the block its site records, in that document's file, carrying that document;
// and, for the attachment with no recorded site, the node's own attachment block
// in the node's file with no document.
var recordedSitesWant = []positionedSubject{
	{file: "aggregate.yaml", locator: ".spec.components.app.features.own", document: AuthoringDocumentAggregate},
	{file: "aggregate.yaml", locator: ".spec.subsystems.edge.subsystems.inner.features.shared", document: AuthoringDocumentAggregate},
	{file: "release.yaml", locator: ".spec.boundaries.prod.boundaries.eu.components.app.features.sized", document: AuthoringDocumentRelease},
	{file: "release.yaml", locator: ".spec.boundaries.prod.features.wide", document: AuthoringDocumentRelease},
	{file: "release.yaml", locator: ".spec.boundaries.prod.aggregates.data.components.db.features.routed", document: AuthoringDocumentRelease},
	{file: "release.yaml", locator: ".spec.components.app.features.top", document: AuthoringDocumentRelease},
	{file: "aggregate.yaml", locator: ".spec.components.app.features.plain"},
}

// TestTheAttachmentTraversalsPositionAnAttachmentAtItsRecordedBlock proves the
// traversals over a node's attachments and over the slots its component type
// declares position each subject at the block the enumeration records the
// attachment was written in — a component's own block, a subsystem at any depth,
// a Release boundary at any depth, the Release's entry for the component directly,
// through routing keys or in the boundary normalization synthesizes — carrying
// that block's document, and fall back to the node's own block with no document
// for an attachment the enumeration records nothing for.
//
// The two axes are asked one question about one attachment, so they must give
// one answer: a finding about a slot and a finding about the attachment filling
// it cannot come to name two blocks.
func TestTheAttachmentTraversalsPositionAnAttachmentAtItsRecordedBlock(t *testing.T) {
	t.Parallel()
	fixture := recordedSitesFixture()

	attachments, err := RunOnNodeAttachments(nil, fixture, fixture, traversalView{},
		raisingCheck[NodeAttachmentSubject]())
	if err != nil {
		t.Fatalf("the attachment traversal must not fail; got %v", err)
	}
	slots, err := RunOnNodeSlots(nil, fixture, fixture, traversalView{}, raisingCheck[NodeSlotSubject]())
	if err != nil {
		t.Fatalf("the slot traversal must not fail; got %v", err)
	}
	for _, axis := range []struct {
		name string
		got  []positionedSubject
	}{
		{name: "the attachment axis", got: positionsOf(attachments)},
		{name: "the slot axis", got: positionsOf(slots)},
	} {
		if len(axis.got) != len(recordedSitesWant) {
			t.Fatalf("%s positioned %d subject(s) %+v; want %d", axis.name, len(axis.got), axis.got, len(recordedSitesWant))
		}
		for i, want := range recordedSitesWant {
			if axis.got[i] != want {
				t.Errorf("%s, subject %d: positioned at %+v; want %+v", axis.name, i, axis.got[i], want)
			}
		}
	}
}

// TestTheAttachmentSocketTraversalExtendsTheAttachmentsRecordedBlock proves a
// socket an attachment wires is positioned inside the attachment's own entry at
// the block the enumeration records, in that document, so the socket and the
// attachment wiring it name one block — and inside the node's own block where
// nothing is recorded.
func TestTheAttachmentSocketTraversalExtendsTheAttachmentsRecordedBlock(t *testing.T) {
	t.Parallel()
	fixture := recordedSitesFixture()
	objections, err := RunOnNodeAttachmentSockets(nil, fixture, fixture, fixture, traversalView{},
		raisingCheck[NodeAttachmentSocketSubject]())
	if err != nil {
		t.Fatalf("the socket traversal must not fail; got %v", err)
	}
	want := []positionedSubject{
		{
			file:     "release.yaml",
			locator:  ".spec.boundaries.prod.boundaries.eu.components.app.features.sized.uses.db",
			document: AuthoringDocumentRelease,
		},
		{file: "aggregate.yaml", locator: ".spec.components.app.features.plain.uses.cache"},
	}
	got := positionsOf(objections)
	if len(got) != len(want) {
		t.Fatalf("the socket traversal positioned %d subject(s) %+v; want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("socket %d positioned at %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestAnAttachmentSitesLocatorIsTheTraversalsLocator proves the locator a site
// spells for a finding no rule raises is the one the traversal positions a rule's
// finding at, for every block shape the fixture records — so the two kinds of
// finding about one attachment cannot come to name two entries.
func TestAnAttachmentSitesLocatorIsTheTraversalsLocator(t *testing.T) {
	t.Parallel()
	fixture := recordedSitesFixture()
	for i, name := range fixture.names {
		site, recorded := fixture.sites[name]
		if !recorded {
			continue
		}
		if got, want := site.Locator(), recordedSitesWant[i].locator; got != want {
			t.Errorf("attachment %q: the site spells %q; the traversal positions it at %q", name, got, want)
		}
	}
}
