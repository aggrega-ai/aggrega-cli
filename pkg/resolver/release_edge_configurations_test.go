// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheReadingOfTheDocumentsNamesEachReleaseEntryWhereItWasWritten proves the
// Release's per-edge axis the reading of the two documents offers names every
// entry at the site the operator wrote it — the top-level shape for an entry the
// boundary normalization synthesized holds, the boundary for one a boundary
// names, and the routing keys for one a routing entry names — and carries the
// Release's file when the caller supplied one and none when it did not.
//
// The routed row's locator is the one the composition's own rules over the same
// surface are positioned at, so a finding raised while the documents are read and
// one raised once they are flattened name one entry alike: both read the one fold.
//
// The rule is declared here and raises once per entry at the entry itself, so
// every locator below is the axis's own site and nothing a shipped rule adds.
func TestTheReadingOfTheDocumentsNamesEachReleaseEntryWhereItWasWritten(t *testing.T) {
	t.Parallel()

	atTheEntry := constraint.Check[constraint.NormalizedView, constraint.ReleaseEdgeConfigurationSubject]{
		Sentinel: ErrSchemaConformance(),
		Severity: constraint.SeverityFatal,
		Run: func(
			_ constraint.NormalizedView,
			s constraint.ReleaseEdgeConfigurationSubject,
		) []constraint.Objection {
			return []constraint.Objection{s.Raise(ErrSchemaConformance())}
		},
	}

	agg := parseAggregate(t, "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: demo\n"+
		"spec:\n  components:\n    api:\n      type: app/svc:1.0.0\n    db:\n      type: app/db:1.0.0\n")
	const head = "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: demo-prod\nspec:\n"

	for _, row := range []struct {
		name     string
		release  string
		source   string
		locators []string
	}{
		{
			name: "an entry written at the document's top level",
			release: head + "  components:\n    api:\n      uses:\n        databases:\n" +
				"          main:\n            target: db\n",
			source:   "release.yaml",
			locators: []string{".spec.components.api.uses.databases.main"},
		},
		{
			name: "an entry a boundary names",
			release: head + "  boundaries:\n    prod:\n      components:\n        api:\n          uses:\n" +
				"            databases:\n              main:\n                target: db\n" +
				"              spare: {}\n",
			source: "releases/prod.yaml",
			locators: []string{
				".spec.boundaries.prod.components.api.uses.databases.main",
				".spec.boundaries.prod.components.api.uses.databases.spare",
			},
		},
		{
			name: "an entry a routing entry names",
			release: head + "  boundaries:\n    prod:\n      aggregates:\n        mod:\n          components:\n" +
				"            gw:\n              uses:\n                models:\n                  gpt: {}\n",
			source:   "release.yaml",
			locators: []string{".spec.boundaries.prod.aggregates.mod.components.gw.uses.models.gpt"},
		},
		{
			name: "a caller holding no file for the Release",
			release: head + "  boundaries:\n    prod:\n      components:\n        api:\n          uses:\n" +
				"            databases:\n              main:\n                target: db\n",
			locators: []string{".spec.boundaries.prod.components.api.uses.databases.main"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			release := types.NormalizeRelease(parseRelease(t, row.release)).Release()
			view := newNormalizedView(normalized(t, agg), release, CatalogLookup{}, nil)
			objections, err := constraint.RunOnReleaseEdgeConfigurations[constraint.NormalizedView](
				nil, newCoordinateSet(view).withReleaseSource(row.source), view, atTheEntry)
			if err != nil {
				t.Fatalf("the per-edge walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, row.locators) {
				t.Errorf("the axis named %v; want %v", got, row.locators)
			}
			for _, o := range objections {
				if o.SourceFile() != row.source {
					t.Errorf("the entry at %s carries file %q; want %q", o.Locator(), o.SourceFile(), row.source)
				}
			}
		})
	}

	// A presentation holding no Release offers no entry at all, rather than an
	// axis read from a document nobody supplied.
	view := newNormalizedView(normalized(t, agg), nil, CatalogLookup{}, nil)
	if entries := newCoordinateSet(view).withReleaseSource("release.yaml").ReleaseEdgeConfigurations(); len(entries) != 0 {
		t.Errorf("a presentation holding no Release enumerated %d entries; want none", len(entries))
	}
}
