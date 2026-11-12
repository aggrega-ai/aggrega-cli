// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"
)

// nodeMappingFixture is one component instance's resolution as the rule over an
// instance's mapping reads it, supplied as the values each accessor answers with.
type nodeMappingFixture struct {
	key        string
	byType     bool
	reference  string
	mapped     bool
	resolved   string
	implements string
	found      bool
	ancestry   map[string][]string
	unwalkable bool
}

func (f nodeMappingFixture) refused() (refusedNodeMapping, bool) {
	return nodeMappingRefused("main-db",
		func(string) (string, bool) { return f.key, f.byType },
		func(string) (string, bool) { return f.reference, f.mapped },
		func(string) string { return f.resolved },
		func(tier, reference string) (string, bool) { return f.implements, f.found },
		func(componentType string) ([]string, bool) {
			if f.unwalkable {
				return nil, false
			}
			return f.ancestry[componentType], true
		})
}

// TestTheInstanceMappingJudgementRefusesOnlyAnotherVersionsRecipe holds the rule
// over an instance's mapping to the one cell it raises in — an unversioned
// type-axis key whose Recipe the instance's own type version does not satisfy —
// and to passing over every neighbouring cell, each for the reason its row names.
//
// Each row varies one value from the refused baseline, so a row that passes is
// passing on the value it names and on nothing else.
//
// 🔴 PLANTED DEFECT: in nodeMappingRefused (pkg/resolver/checks_node_mapped_recipe_implements.go),
// return early on a key that names a version: the two "a key naming another
// version … to a 1.0.0 Recipe" and "… extending another base version" rows
// redden, passed over although the instance's version is not the key's.
func TestTheInstanceMappingJudgementRefusesOnlyAnotherVersionsRecipe(t *testing.T) {
	t.Parallel()
	baseline := func() nodeMappingFixture {
		return nodeMappingFixture{
			key:        "core/postgres",
			byType:     true,
			reference:  "postgres-chart:1.0.0",
			mapped:     true,
			resolved:   "core/postgres:2.0.0",
			implements: "core/postgres:1.0.0",
			found:      true,
		}
	}
	for _, tc := range []struct {
		name        string
		edit        func(*nodeMappingFixture)
		wantRefused bool
	}{
		{name: "an unversioned key sending a 2.0.0 instance to a 1.0.0 Recipe", edit: func(*nodeMappingFixture) {},
			wantRefused: true},
		{name: "a Recipe implementing the instance's version", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.implements = "core/postgres:2.0.0" }},
		{name: "a Recipe implementing a caret range the version falls in", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.implements = "core/postgres:^2.0.0" }},
		{name: "a Recipe implementing a caret range the version falls outside", wantRefused: true,
			edit: func(f *nodeMappingFixture) { f.implements = "core/postgres:^1.0.0" }},
		{name: "a Recipe implementing the unversioned type", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.implements = "core/postgres" }},
		{name: "a Recipe implementing a base the instance's version extends", wantRefused: false,
			edit: func(f *nodeMappingFixture) {
				f.key, f.resolved, f.implements = "acme/pg", "acme/pg:2.0.0", "core/postgres:2.0.0"
				f.ancestry = map[string][]string{
					"acme/pg":       {"core/postgres:2.0.0"},
					"acme/pg:2.0.0": {"core/postgres:2.0.0"},
				}
			}},
		{name: "a Recipe implementing a base at another version than the instance's extends", wantRefused: true,
			edit: func(f *nodeMappingFixture) {
				f.key, f.resolved, f.implements = "acme/pg", "acme/pg:2.0.0", "core/postgres:1.0.0"
				// The bare key's own ancestry admits the Recipe, so the rule over the
				// entry passes it; the instance's version extends another base.
				f.ancestry = map[string][]string{
					"acme/pg":       {"core/postgres:1.0.0"},
					"acme/pg:2.0.0": {"core/postgres:2.0.0"},
				}
			}},
		{name: "a key naming another version sending a 2.0.0 instance to a 1.0.0 Recipe", wantRefused: true,
			edit: func(f *nodeMappingFixture) { f.key = "core/postgres:1.0.0" }},
		{name: "a key naming the instance's version, mapped to a Recipe implementing it", wantRefused: false,
			edit: func(f *nodeMappingFixture) {
				f.key, f.reference, f.implements = "core/postgres:2.0.0", "postgres-chart-v2:1.0.0", "core/postgres:2.0.0"
			}},
		{name: "a key naming the instance's version whose Recipe the rule over the entry refuses", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.key = "core/postgres:2.0.0" }},
		{name: "a key naming another version and a Recipe implementing a caret range covering both", wantRefused: false,
			edit: func(f *nodeMappingFixture) {
				f.key, f.resolved, f.implements = "core/postgres:1.0.0", "core/postgres:1.4.0", "core/postgres:^1.0.0"
			}},
		{name: "a key naming another version reaching a type whose base the Recipe implements at every version",
			wantRefused: false,
			edit: func(f *nodeMappingFixture) {
				f.key, f.resolved, f.implements = "acme/pg:1.0.0", "acme/pg:2.0.0", "core/postgres"
				f.ancestry = map[string][]string{
					"acme/pg:1.0.0": {"core/postgres:1.0.0"},
					"acme/pg:2.0.0": {"core/postgres:2.0.0"},
				}
			}},
		{name: "a key naming another version reaching a type extending another base version", wantRefused: true,
			edit: func(f *nodeMappingFixture) {
				f.key, f.resolved, f.implements = "acme/pg:1.0.0", "acme/pg:2.0.0", "core/postgres:1.0.0"
				f.ancestry = map[string][]string{
					"acme/pg:1.0.0": {"core/postgres:1.0.0"},
					"acme/pg:2.0.0": {"core/postgres:2.0.0"},
				}
			}},
		{name: "a name-axis entry", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.key, f.byType = "", false }},
		{name: "an instance nothing maps", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.mapped = false }},
		{name: "a reference the catalog does not hold", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.found = false }},
		{name: "an entry the rule over the entry refuses on its key alone", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.implements = "core/mysql:2.0.0" }},
		{name: "a malformed pin on the Recipe's implements", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.implements = "core/postgres:1.x" }},
		{name: "a malformed pin on the instance's type", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.resolved = "core/postgres:2.x" }},
		{name: "a bare instance type the catalog holds no Component of", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.resolved = "core/postgres" }},
		{name: "an extends chain that cannot be walked", wantRefused: false,
			edit: func(f *nodeMappingFixture) { f.unwalkable = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := baseline()
			tc.edit(&f)
			got, refused := f.refused()
			if refused != tc.wantRefused {
				t.Fatalf("refused = %t, want %t (%+v)", refused, tc.wantRefused, got)
			}
			if !refused {
				return
			}
			if got.key != f.key || got.reference != f.reference || got.implements != f.implements ||
				got.request != f.resolved {
				t.Errorf("the refusal carries %+v; want the key %q, reference %q, implements %q and request %q",
					got, f.key, f.reference, f.implements, f.resolved)
			}
		})
	}
}
