// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"testing"
)

// TestConnectionAttachmentLocatorsExtendTheirEntrysLocator proves the two
// locators naming an attachment a connection carries are the locator of the
// connection entry the traversals position a rule at, followed by the
// attachment's own keys — for every shape the shared fixture covers: an entry in
// a boundary the author named, one reached through a routing entry, one in the
// boundary normalization synthesizes, a component at the top level and one in a
// declared subsystem.
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
			if got, want := ReleaseConnectionAttachmentLocator(entry, "credential"),
				locators[i]+".features.credential"; got != want {
				t.Errorf("entry %d: attachment locator = %q; want %q", i, got, want)
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
			if got, want := ConnectionAttachmentLocator(edge.Node, edge.Socket, edge.Alias, "credential"),
				locators[i]+".features.credential"; got != want {
				t.Errorf("connection %d: attachment locator = %q; want %q", i, got, want)
			}
		}
		if compared == 0 {
			t.Fatal("the fixture yields no long-form connection, so this row proves nothing")
		}
	})
}
