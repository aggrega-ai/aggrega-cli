// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Unit tests for normalizeUses and buildVerboseEdge — the typed
// uses-form normalisation that converges both authoring shapes
// (string-array and verbose alias-map) onto NormalizedEdge. mkCompactUses
// and mkVerboseUses build the typed input shapes; mkInject builds typed
// inject blocks for verbose entries.
//
// Behaviour clusters:
//
//   - TestNormalizeUses_StringArray — pins the compact form: one edge
//     per array entry; OriginFeature is empty for component-authored
//     edges.
//   - TestNormalizeUses_VerboseMap — pins the verbose form: alias
//     becomes Alias, the entry's Target overrides the alias as Target,
//     the entry's Inject populates the edge's Inject block.
//   - TestNormalizeUses_OriginFeature — pins the per-edge origin-marker
//     carry-through: a non-empty originFeature parameter lands on every
//     emitted edge.
//   - TestNormalizeUses_Empty — pins the empty-input contract: zero-
//     length uses returns no edges and no error.
//   - TestNormalizeUses_DeterministicOrder — pins the OUTER socket-
//     iteration discipline as AUTHOR ORDER.
//   - TestNormalizeUses_VerboseDefaultsTargetToAlias — pins the
//     omitted-target default behaviour: when Target is empty, the
//     emitted edge's Target equals the alias.
//   - TestNormalizeUses_VerboseFormPreservesAuthorOrder — pins the
//     INNER verbose-form alias loop's author-order traversal.

package topology

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// mkCompactUses builds a types.OrderedMap[UsesSocketValue] with one
// socket in compact form, preserving the call-site argument order.
func mkCompactUses(socket string, targets ...string) types.OrderedMap[types.UsesSocketValue] {
	m := types.OrderedMap[types.UsesSocketValue]{}
	m.Set(socket, types.UsesSocketValue{Compact: targets})
	return m
}

// mkVerboseUses builds a types.OrderedMap[UsesSocketValue] with one
// socket in verbose form. Alias/entry pairs preserve the call-site
// argument order; alternation is (alias, entry, alias, entry, ...).
func mkVerboseUses(socket string, aliasEntryPairs ...any) types.OrderedMap[types.UsesSocketValue] {
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	for i := 0; i+1 < len(aliasEntryPairs); i += 2 {
		alias, _ := aliasEntryPairs[i].(string)
		entry, _ := aliasEntryPairs[i+1].(types.VerboseUsesEntry)
		verbose.Set(alias, entry)
	}
	m := types.OrderedMap[types.UsesSocketValue]{}
	m.Set(socket, types.UsesSocketValue{Verbose: verbose})
	return m
}

func TestNormalizeUses_StringArray(t *testing.T) {
	t.Parallel()

	uses := mkCompactUses("databases", "db1", "db2")
	edges, err := normalizeUses(uses, "api-server", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	if edges[0].Source != "api-server" || edges[0].Target != "db1" || edges[0].Socket != "databases" {
		t.Errorf("edge 0: %+v", edges[0])
	}
	if edges[1].Target != "db2" {
		t.Errorf("edge 1 target: %q", edges[1].Target)
	}
	if edges[0].OriginFeature != "" {
		t.Error("expected empty OriginFeature for component uses")
	}
}

func TestNormalizeUses_VerboseMap(t *testing.T) {
	t.Parallel()

	inject := &types.InjectBlock{Env: map[string]any{"DB_URL": "host"}}
	uses := mkVerboseUses("databases", "hr-db", types.VerboseUsesEntry{Target: "corporate-hr", Inject: inject})

	edges, err := normalizeUses(uses, "agent", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	e := edges[0]
	if e.Source != "agent" || e.Target != "corporate-hr" || e.Socket != "databases" || e.Alias != "hr-db" {
		t.Errorf("edge: %+v", e)
	}
	if e.Inject == nil || e.Inject.Env == nil {
		t.Error("expected inject.env to be set")
	}
}

func TestNormalizeUses_OriginFeature(t *testing.T) {
	t.Parallel()

	uses := mkCompactUses("targets", "api")
	edges, err := normalizeUses(uses, "node-a", "core/monitoring", "mon-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].OriginFeature != "core/monitoring" {
		t.Errorf("OriginFeature = %q, want %q", edges[0].OriginFeature, "core/monitoring")
	}
	// The edge carries the firing attachment's instance name (not only the
	// type), so a boundary-scope FeatureRecipe's provides can be located by
	// instance — disambiguating two attachments of the same type on a boundary.
	if edges[0].OriginFeatureInstance != "mon-1" {
		t.Errorf("OriginFeatureInstance = %q, want %q", edges[0].OriginFeatureInstance, "mon-1")
	}
}

func TestNormalizeUses_Empty(t *testing.T) {
	t.Parallel()

	edges, err := normalizeUses(types.OrderedMap[types.UsesSocketValue]{}, "node", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestNormalizeUses_DeterministicOrder(t *testing.T) {
	t.Parallel()

	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("zebra", types.UsesSocketValue{Compact: []string{"z1"}})
	uses.Set("alpha", types.UsesSocketValue{Compact: []string{"a1"}})
	uses.Set("mid", types.UsesSocketValue{Compact: []string{"m1"}})

	var firstOrder []string
	for i := 0; i < 10; i++ {
		edges, err := normalizeUses(uses, "src", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sockets := make([]string, len(edges))
		for j, e := range edges {
			sockets[j] = e.Socket
		}
		if i == 0 {
			firstOrder = sockets
		} else {
			for j, s := range sockets {
				if s != firstOrder[j] {
					t.Errorf("run %d: order changed at %d: %q vs %q", i, j, s, firstOrder[j])
				}
			}
		}
	}
	// Verify author insertion order: zebra, alpha, mid (as declared)
	if firstOrder[0] != "zebra" || firstOrder[1] != "alpha" || firstOrder[2] != "mid" {
		t.Errorf("not in author order: %v", firstOrder)
	}
}

func TestNormalizeUses_VerboseDefaultsTargetToAlias(t *testing.T) {
	t.Parallel()

	uses := mkVerboseUses("databases", "my-db", types.VerboseUsesEntry{})

	edges, err := normalizeUses(uses, "src", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].Target != "my-db" || edges[0].Alias != "my-db" {
		t.Errorf("edge: %+v (target should default to alias when empty)", edges[0])
	}
}

// TestNormalizeUses_VerboseFormPreservesAuthorOrder exercises the
// verbose-form alias loop with multiple aliases authored in non-
// alphabetical order. Pins the author-order traversal contract: the
// inner loop iterates the OrderedMap via .All(), so the emitted edge
// order matches the author's authoring sequence (z, a, m), not
// alphabetical.
func TestNormalizeUses_VerboseFormPreservesAuthorOrder(t *testing.T) {
	t.Parallel()

	uses := mkVerboseUses("databases",
		"z-alias", types.VerboseUsesEntry{Target: "z-target"},
		"a-alias", types.VerboseUsesEntry{Target: "a-target"},
		"m-alias", types.VerboseUsesEntry{Target: "m-target"},
	)

	edges, err := normalizeUses(uses, "src", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(edges))
	}
	wantAliases := []string{"z-alias", "a-alias", "m-alias"}
	for i, want := range wantAliases {
		if edges[i].Alias != want {
			t.Errorf("edge[%d].Alias = %q, want %q (author order, not alphabetical)", i, edges[i].Alias, want)
		}
	}
	wantTargets := []string{"z-target", "a-target", "m-target"}
	for i, want := range wantTargets {
		if edges[i].Target != want {
			t.Errorf("edge[%d].Target = %q, want %q", i, edges[i].Target, want)
		}
	}
}
