// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestEdgeValuesOf_BlankCompactItemNamesNoEdge pins the projection directly:
// a compact-list item the author left blank names no target, so it yields no
// entry to judge, while every item that names one still yields exactly one entry
// carrying that name as its alias.
//
// Asserted on the projection rather than only through the gate because the
// projection is where the rule lives — a gate assertion alone would keep passing
// if a later reader reinstated the blank entry here and some caller happened to
// filter it downstream.
func TestEdgeValuesOf_BlankCompactItemNamesNoEdge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		compact     []string
		wantAliases []string
	}{
		{name: "a blank item on its own", compact: []string{""}, wantAliases: nil},
		{name: "only blank items", compact: []string{"", ""}, wantAliases: nil},
		{name: "a named item beside a blank one", compact: []string{"db", ""}, wantAliases: []string{"db"}},
		{name: "a blank item first", compact: []string{"", "db"}, wantAliases: []string{"db"}},
		{name: "named items only, author order kept", compact: []string{"db", "cache"}, wantAliases: []string{"db", "cache"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EdgeValuesOf(types.UsesSocketValue{Compact: tc.compact})
			var gotAliases []string
			for _, ev := range got {
				gotAliases = append(gotAliases, ev.Alias)
			}
			if len(gotAliases) != len(tc.wantAliases) {
				t.Fatalf("aliases: want %q, got %q", tc.wantAliases, gotAliases)
			}
			for i := range tc.wantAliases {
				if gotAliases[i] != tc.wantAliases[i] {
					t.Fatalf("aliases: want %q, got %q", tc.wantAliases, gotAliases)
				}
			}
		})
	}
}
