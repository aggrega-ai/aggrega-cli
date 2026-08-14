// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
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

// TestEdgeConformance_BlankCompactItemIsNotJudged proves the consequence at the
// gate, in both directions: a socket declaring a required per-edge key raises
// nothing against a blank compact item, and still raises against every item
// that names a target — including when the two sit in one list.
//
// A blank item is an author leaving a list entry empty. It identifies no
// connection, so there is no connection for a per-edge contract to be measured
// against, and a required-key diagnostic locating an empty alias segment names
// something the author cannot act on.
func TestEdgeConformance_BlankCompactItemIsNotJudged(t *testing.T) {
	t.Parallel()
	cat := edgeCoverageCatalog(t)

	cases := []struct {
		name string
		// usesBlock is the `uses:` body of component `api`.
		usesBlock string
		// wantAliases are the edge aliases the gate must raise a
		// missing-required-key diagnostic against, in diagnostic order.
		wantAliases []string
	}{
		{
			name:        "a blank item on its own is judged nowhere",
			usesBlock:   "caches: [\"\"]\n",
			wantAliases: nil,
		},
		{
			name:        "a named item is still judged",
			usesBlock:   "caches: [db]\n",
			wantAliases: []string{"db"},
		},
		{
			name:        "a named item beside a blank one is judged once",
			usesBlock:   "caches: [db, \"\"]\n",
			wantAliases: []string{"db"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agg := parseAggregate(t, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
`+indent(tc.usesBlock, 8)+`    db:
      type: app/db:1.0.0
`)
			errs := guardEdgeConformance(normalized(t, agg), nil, cat, nil)
			if len(errs) != len(tc.wantAliases) {
				t.Fatalf("want %d diagnostic(s) at aliases %q; got %d: %v",
					len(tc.wantAliases), tc.wantAliases, len(errs), errs)
			}
			for i, alias := range tc.wantAliases {
				if !errors.Is(errs[i], ErrSchemaConformance()) {
					t.Fatalf("diagnostic %d must wrap the conformance sentinel; got %v", i, errs[i])
				}
				wantField := `uses["caches"][` + `"` + alias + `"]`
				if !strings.Contains(errs[i].Field, wantField) {
					t.Errorf("diagnostic %d must locate the edge at %s; got %s", i, wantField, errs[i].Field)
				}
				if !strings.Contains(errs[i].Message, "pool_size") {
					t.Errorf("diagnostic %d must name the missing key; got %s", i, errs[i].Message)
				}
			}
			// The property the whole change turns on: no diagnostic may locate an
			// edge by an empty alias segment, whatever else the gate raised.
			for _, e := range errs {
				if strings.Contains(e.Field, `[""]`) {
					t.Errorf("no diagnostic may name an empty alias segment; got %s", e.Field)
				}
			}
		})
	}
}
