// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"testing"
)

// TestPathCoveredBy_CoversTheLeafAndEverythingBeneathIt is the specification of
// the covering rule, including the two shapes that separate it from a textual
// prefix test: a leaf arbitrarily deep inside a declared object is covered, and
// a sibling whose name merely STARTS with a declared name is not.
func TestPathCoveredBy_CoversTheLeafAndEverythingBeneathIt(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{
		"dbPassword":  true,
		"credentials": true,
	}
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"the declared leaf itself", "dbPassword", true},
		{"a child of a declared object", "credentials.password", true},
		{"arbitrarily deep inside a declared object", "credentials.a.b.c", true},
		{"the declared object itself", "credentials", true},
		{"an undeclared sibling", "appName", false},
		{"a name sharing a prefix with a declared leaf", "dbPasswordExtra", false},
		{"a nested path under a name sharing a prefix", "credentialsExtra.password", false},
		{"a grandchild of a declared object", "credentials.password.deeper", true},
		{"the empty path names no leaf", "", false},
	} {
		if got := PathCoveredBy(tc.path, declared); got != tc.want {
			t.Errorf("%s: PathCoveredBy(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
	// Positive control for the table: with nothing declared, the row that
	// carried the strongest match must flip, so no row above can be passing
	// because the walk never ran.
	if PathCoveredBy("credentials.password", nil) {
		t.Error("an empty declared set must cover nothing")
	}
}

// TestPathCoveredBy_AncestorDirectionOnly pins the direction the rule does NOT
// travel: a declaration on a CHILD does not cover its parent. A display asks
// about one leaf at a time, so covering upward would withhold an object whose
// own value the author never marked.
func TestPathCoveredBy_AncestorDirectionOnly(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{"credentials.password": true}
	if PathCoveredBy("credentials", declared) {
		t.Error("a declaration on a child must not cover its parent")
	}
	if !PathCoveredBy("credentials.password", declared) {
		t.Error("the declared leaf itself must be covered; the control did not fire")
	}
}

// TestSplitPillarCoordinate_RootSelectsThePillar pins the parse side of the
// coordinate spelling against the roots the write side uses, so the two cannot
// come apart.
func TestSplitPillarCoordinate_RootSelectsThePillar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		coordinate string
		wantPillar string
		wantPath   string
		wantSplit  bool
	}{
		{"a values coordinate", "values.dbPassword", "values", "dbPassword", true},
		{"a nested values coordinate", "values.creds.password", "values", "creds.password", true},
		{"an infra coordinate", "infra.namespace", "infra", "namespace", true},
		{"a bare pillar name names no leaf", "values", "", "", false},
		{"a pillar root with nothing under it", "values.", "", "", false},
		{"a pillar this spelling does not cover", "provides.host", "", "", false},
		{"a bare leaf name carries no root", "dbPassword", "", "", false},
		{"the empty string", "", "", "", false},
	} {
		pillar, path, ok := SplitPillarCoordinate(tc.coordinate)
		if ok != tc.wantSplit || pillar != tc.wantPillar || path != tc.wantPath {
			t.Errorf("%s: SplitPillarCoordinate(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.name, tc.coordinate, pillar, path, ok, tc.wantPillar, tc.wantPath, tc.wantSplit)
		}
	}
}

// TestPillarRoots_AreTheWriteSideOfTheParse pins the two halves of the
// coordinate against each other: a coordinate built from a root and a path
// parses back to the pillar that root names and to the same path. A root that
// gained a spelling the parser did not would fail here rather than silently on
// a display.
func TestPillarRoots_AreTheWriteSideOfTheParse(t *testing.T) {
	t.Parallel()
	const leaf = "creds.password"
	for _, tc := range []struct {
		root       string
		wantPillar string
	}{
		{PillarRootValues, "values"},
		{PillarRootInfra, "infra"},
	} {
		pillar, path, ok := SplitPillarCoordinate(tc.root + leaf)
		if !ok || pillar != tc.wantPillar || path != leaf {
			t.Errorf("round trip through %q = (%q, %q, %v), want (%q, %q, true)",
				tc.root, pillar, path, ok, tc.wantPillar, leaf)
		}
	}
}
