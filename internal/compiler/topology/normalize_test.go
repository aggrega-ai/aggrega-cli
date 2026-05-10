// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Unit tests for normalizeUses, parseVerboseEntry, and parseInjectBlock
// — the polymorphic uses-form normalisation that converges both
// authoring shapes (string-array and verbose-map) onto NormalizedEdge.
// mkUses is a small helper that assembles an OrderedMap[any]
// preserving the call-site key insertion order, mirroring how YAML
// unmarshal would build the same map from authored YAML.
//
// Behaviour clusters:
//
//   - TestNormalizeUses_StringArray — pins string-array form; one edge
//     per array entry; OriginFeature is empty for component-authored
//     edges.
//   - TestNormalizeUses_VerboseMap — pins verbose-map form: alias
//     becomes Alias, inner `target:` overrides the alias as Target,
//     inner `inject:` populates the edge's Inject block.
//   - TestNormalizeUses_OriginFeature — pins the per-edge origin-marker
//     carry-through: a non-empty originFeature parameter lands on
//     every emitted edge.
//   - TestNormalizeUses_Empty — pins the empty-input contract:
//     zero-length uses returns no edges and no error.
//   - TestNormalizeUses_InvalidArrayElement /
//     TestNormalizeUses_InvalidType — pin the two ErrInvalidUsesFormat
//     paths (non-string array element; non-array-non-map socket value).
//   - TestNormalizeUses_DeterministicOrder — pins the OUTER socket-
//     iteration discipline as AUTHOR ORDER (OrderedMap.All preserves
//     insertion order). The inner verbose-map alias loop sorts
//     alphabetically as a separate concern; this test exercises only
//     the outer iteration via string-array entries.
//   - TestNormalizeUses_VerboseSimpleString — pins the bare-string
//     verbose-entry shorthand: `{"my-db": "target-name"}` is
//     equivalent to `{"my-db": {"target": "target-name"}}`.

package topology

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// mkUses builds a types.OrderedMap[any] in the order given.
func mkUses(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// mkOM builds a *types.OrderedMap[any] in the order given. Used in tests
// to construct nested OrderedMap shapes that mirror what the gamma-aware
// JSON/YAML decode path produces in production.
func mkOM(pairs ...any) *types.OrderedMap[any] {
	m := types.NewOrderedMap[any]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func TestNormalizeUses_StringArray(t *testing.T) {
	t.Parallel()

	uses := mkUses("databases", []any{"db1", "db2"})
	edges, err := normalizeUses(uses, "api-server", "")
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

	uses := mkUses("databases", mkOM(
		"hr-db", mkOM(
			"target", "corporate-hr",
			"inject", mkOM(
				"env", mkOM("DB_URL", "host"),
			),
		),
	))
	edges, err := normalizeUses(uses, "agent", "")
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

	uses := mkUses("targets", []any{"api"})
	edges, err := normalizeUses(uses, "node-a", "core/monitoring")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].OriginFeature != "core/monitoring" {
		t.Errorf("OriginFeature = %q, want %q", edges[0].OriginFeature, "core/monitoring")
	}
}

func TestNormalizeUses_Empty(t *testing.T) {
	t.Parallel()

	edges, err := normalizeUses(types.OrderedMap[any]{}, "node", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestNormalizeUses_InvalidArrayElement(t *testing.T) {
	t.Parallel()

	uses := mkUses("databases", []any{42})
	_, err := normalizeUses(uses, "node", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidUsesFormat) {
		t.Errorf("expected ErrInvalidUsesFormat, got %v", err)
	}
}

func TestNormalizeUses_InvalidType(t *testing.T) {
	t.Parallel()

	uses := mkUses("databases", 42)
	_, err := normalizeUses(uses, "node", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidUsesFormat) {
		t.Errorf("expected ErrInvalidUsesFormat, got %v", err)
	}
}

func TestNormalizeUses_DeterministicOrder(t *testing.T) {
	t.Parallel()

	// assertion refreshed for OrderedMap author-order iteration; was alphabetical under map[string]X
	uses := mkUses(
		"zebra", []any{"z1"},
		"alpha", []any{"a1"},
		"mid", []any{"m1"},
	)

	var firstOrder []string
	for i := 0; i < 10; i++ {
		edges, err := normalizeUses(uses, "src", "")
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

func TestNormalizeUses_VerboseSimpleString(t *testing.T) {
	t.Parallel()

	uses := mkUses("databases", mkOM(
		"my-db", "target-db-name",
	))
	edges, err := normalizeUses(uses, "src", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].Target != "target-db-name" || edges[0].Alias != "my-db" {
		t.Errorf("edge: %+v", edges[0])
	}
}
