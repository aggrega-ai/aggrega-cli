// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"slices"
	"testing"
)

// TestAnObjectionCarriesTheAuthoringDocumentItsSiteRecorded proves the three
// traversals whose sites can sit in either authoring document hand the document a
// site recorded to every objection a rule raises there, and hand none where the
// site recorded none.
//
// The document is what a finding is later filed under, so a traversal dropping it
// would file a fault the operator wrote on the Release as the developer's, while
// the finding's file and locator — the only things a positioning test reads —
// stayed exactly right. That is why the property is asserted on its own.
//
// Each row reuses an enumeration other tests already position, so the documents
// below line up with sites whose files and locators are pinned elsewhere: the
// Release-written entry is the one positioned in the Release's file, and the
// entries naming a component's own block record no document.
func TestAnObjectionCarriesTheAuthoringDocumentItsSiteRecorded(t *testing.T) {
	t.Parallel()

	documentsOf := func(objections []Objection) []string {
		out := make([]string, 0, len(objections))
		for _, o := range objections {
			out = append(out, o.AuthoringDocument())
		}
		return out
	}

	t.Run("an attachment, by the connection entry it was written on", func(t *testing.T) {
		t.Parallel()
		developer := NodeInstance{ID: "gw", SourceFile: "aggregate.yaml", AuthoredName: "gw"}
		operator := NodeInstance{ID: "router", SourceFile: "aggregate.yaml", AuthoredName: "router"}
		plain := NodeInstance{ID: "plain", SourceFile: "aggregate.yaml", AuthoredName: "plain"}
		enumeration := connectionAttachmentSites{
			nodes: []NodeInstance{developer, operator, plain},
			sites: map[string]ConnectionAttachmentSite{
				"gw": ConnectionAttachmentSiteInAggregate(developer, "models", "gpt", "credential"),
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
		objections, err := RunOnNodeAttachments(nil, enumeration, enumeration, traversalView{},
			raisingCheck[NodeAttachmentSubject]())
		if err != nil {
			t.Fatalf("the traversal must not fail over the enumeration; got %v", err)
		}
		want := []string{AuthoringDocumentAggregate, AuthoringDocumentRelease, ""}
		if got := documentsOf(objections); !slices.Equal(got, want) {
			t.Errorf("the objections carry the documents %q; want %q — the developer's entry, the "+
				"operator's entry, and an attachment of the component's own block, which records none",
				got, want)
		}
	})

	t.Run("a connection, by the surface that drew it", func(t *testing.T) {
		t.Parallel()
		objections, err := RunOnOriginatedConnections(nil, newTraversalFixture(), traversalView{},
			raisingCheck[OriginatedConnectionSubject]())
		if err != nil {
			t.Fatalf("the traversal must not fail over the fixture; got %v", err)
		}
		// The fixture's connections in its own order: a component's own, two an
		// attachment declared in the Aggregate drew, one the Release drew, and a
		// component's own again.
		want := []string{"", AuthoringDocumentAggregate, AuthoringDocumentAggregate, AuthoringDocumentRelease, ""}
		if got := documentsOf(objections); !slices.Equal(got, want) {
			t.Errorf("the objections carry the documents %q; want %q", got, want)
		}
	})

	t.Run("an edge, by the surface that drew it", func(t *testing.T) {
		t.Parallel()
		f := traversalFixture{
			nodes: []NodeInstance{webNode},
			edges: []EdgeInstance{
				{Node: webNode, Socket: "zebra", Alias: "primary"},
				{
					Node: webNode, Socket: "zebra", Alias: "spare",
					Origin: ConnectionOrigin{
						Kind:       OriginRelease,
						Site:       AuthoredSite{ScopePath: "prod", Component: "web"},
						Document:   AuthoringDocumentRelease,
						SourceFile: "release.yaml",
					},
				},
			},
		}
		objections, err := RunOnEdges(nil, f, traversalView{}, raisingCheck[EdgeSubject]())
		if err != nil {
			t.Fatalf("the traversal must not fail over the fixture; got %v", err)
		}
		want := []string{"", AuthoringDocumentRelease}
		if got := documentsOf(objections); !slices.Equal(got, want) {
			t.Errorf("the objections carry the documents %q; want %q — an edge whose origin recorded "+
				"nothing, and one the Release's edge surface drew", got, want)
		}
		if got := objections[1].SourceFile(); got != "release.yaml" {
			t.Errorf("the Release-drawn edge is positioned in %q; want the Release's own file", got)
		}
	})
}

// TestAuthoringDocumentKindNamesBothDocumentsAndNothingElse pins the one
// correspondence between the authoring-document coordinate and the entity kind a
// category is decided by: each of the two documents has its kind, and a value
// outside the pair is reported as uncovered rather than given one.
func TestAuthoringDocumentKindNamesBothDocumentsAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		document string
		want     string
		named    bool
	}{
		{document: AuthoringDocumentAggregate, want: "Aggregate", named: true},
		{document: AuthoringDocumentRelease, want: "Release", named: true},
		{document: "", named: false},
		{document: "manifest", named: false},
	} {
		kind, named := AuthoringDocumentKind(row.document)
		if named != row.named || string(kind) != row.want {
			t.Errorf("AuthoringDocumentKind(%q) = (%q, %v); want (%q, %v)",
				row.document, kind, named, row.want, row.named)
		}
	}
}
