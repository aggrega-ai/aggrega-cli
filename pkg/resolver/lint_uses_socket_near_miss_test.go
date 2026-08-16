// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// socketCatalog returns a lookup whose one Component declares two sockets, so a
// near miss has something to be near.
func socketCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI != "core/devtool/adminer" {
				// The edge target resolves to a socket-less Component. It must
				// be FOUND, or the catalog-view consistency guard halts on it
				// and the halt-freedom assertion below would be measuring the
				// wrong thing.
				return types.Component{Metadata: types.ComponentMetadata{Name: "mariadb", Version: "1.0.0"}}, true
			}
			sockets := types.OrderedMap[types.UsesSchemaEntry]{}
			sockets.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
			sockets.Set("caches", types.UsesSchemaEntry{})
			return types.Component{
				Metadata: types.ComponentMetadata{Name: "adminer", Version: "1.0.0"},
				Spec:     types.ComponentSpec{UsesSchema: sockets},
			}, true
		},
	}
}

func aggWithSockets(t *testing.T, socketNames []string) types.NormalizedAggregate {
	t.Helper()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	for _, n := range socketNames {
		edges := types.OrderedMap[types.VerboseUsesEntry]{}
		edges.Set("db", types.VerboseUsesEntry{Target: "db"})
		uses.Set(n, types.UsesSocketValue{Verbose: edges})
	}
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/datastore/mariadb"})
	components.Set("web", types.ComponentInstance{
		Type: "core/devtool/adminer",
		Uses: uses,
	})
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Components: components},
	})
}

func TestLintUsesSocketNearMiss_TypoReported(t *testing.T) {
	// "databses" is one transposition from the declared "databases".
	got := LintUsesSocketNearMiss(aggWithSockets(t, []string{"databses"}), socketCatalog())
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 advisory; got %d: %v", len(got), got)
	}
	if !errors.Is(got[0], ErrUsesSocketNearMiss()) {
		t.Errorf("expected ErrUsesSocketNearMiss; got %v", got[0].Err)
	}
	// Both spellings must appear, or the author cannot see the pair.
	for _, want := range []string{"databses", "databases"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("expected the advisory to name %q; got %q", want, got[0].Message)
		}
	}
	// The Field locator is what places the advisory on the authoring key.
	wantField := `.spec.components["web"].uses["databses"]`
	if got[0].Field != wantField {
		t.Errorf("expected Field %q; got %q", wantField, got[0].Field)
	}
}

func TestLintUsesSocketNearMiss_DeclaredSocketSilent(t *testing.T) {
	// An edge through a declared socket is the intended case.
	if got := LintUsesSocketNearMiss(aggWithSockets(t, []string{"databases"}), socketCatalog()); len(got) != 0 {
		t.Fatalf("expected no advisory for a declared socket; got %v", got)
	}
}

func TestLintUsesSocketNearMiss_DeliberateAdHocSocketSilent(t *testing.T) {
	// The negative control that makes the lint honest rather than noisy: a
	// deliberate ad-hoc socket whose name resembles no declared socket is
	// ordinary authoring on this surface and must stay quiet.
	if got := LintUsesSocketNearMiss(aggWithSockets(t, []string{"telemetry"}), socketCatalog()); len(got) != 0 {
		t.Fatalf("expected no advisory for a distinct ad-hoc socket; got %v", got)
	}
}

func TestLintUsesSocketNearMiss_NeverHalts(t *testing.T) {
	// The advisory class must not reach the guard verdict: RunGuards is the
	// halting set, and the lint is deliberately not registered in it.
	na := aggWithSockets(t, []string{"databses"})
	if errs, err := RunGuards(nil, na, nil, socketCatalog(), nil, nil); err != nil || len(errs) != 0 {
		t.Fatalf("the near-miss lint must not halt through the guard suite; got %v (err %v)", errs, err)
	}
	if got := LintUsesSocketNearMiss(na, socketCatalog()); len(got) != 1 {
		t.Fatalf("expected the advisory from the lint producer; got %v", got)
	}
}

func TestLintUsesSocketNearMiss_ShortNamesNotPaired(t *testing.T) {
	// Below the minimum length nearly every name is within one edit of every
	// other, so pairing them would produce noise rather than signal.
	sockets := types.OrderedMap[types.UsesSchemaEntry]{}
	sockets.Set("dbs", types.UsesSchemaEntry{})
	cat := CatalogLookup{Component: func(u string) (types.Component, bool) {
		return types.Component{Spec: types.ComponentSpec{UsesSchema: sockets}}, true
	}}
	if got := LintUsesSocketNearMiss(aggWithSockets(t, []string{"dbz"}), cat); len(got) != 0 {
		t.Fatalf("expected no advisory for sub-threshold names; got %v", got)
	}
}

func TestLintUsesSocketNearMiss_NoSocketsNoAdvisory(t *testing.T) {
	// A type that declares no sockets has no near miss to report; every socket
	// an author names on it is ad-hoc by construction.
	cat := CatalogLookup{Component: func(u string) (types.Component, bool) {
		return types.Component{Metadata: types.ComponentMetadata{Name: "bare"}}, true
	}}
	if got := LintUsesSocketNearMiss(aggWithSockets(t, []string{"databses"}), cat); len(got) != 0 {
		t.Fatalf("expected no advisory when the type declares no sockets; got %v", got)
	}
}

func TestLintUsesSocketNearMiss_CatalogLessSilent(t *testing.T) {
	if got := LintUsesSocketNearMiss(aggWithSockets(t, []string{"databses"}), CatalogLookup{}); got != nil {
		t.Fatalf("expected nil on a catalog-less path; got %v", got)
	}
}

func TestLintUsesSocketNearMiss_Deterministic(t *testing.T) {
	// Byte-identical advisory sequence across runs of identical input. Two
	// near misses against two different declared sockets, authored in an order
	// the walk must not preserve.
	na := aggWithSockets(t, []string{"databses", "cahces"})
	first := LintUsesSocketNearMiss(na, socketCatalog())
	if len(first) != 2 {
		t.Fatalf("expected 2 advisories; got %d: %v", len(first), first)
	}
	// The walk sorts socket names, so "cahces" precedes "databses" regardless of
	// the author's order.
	if !strings.Contains(first[0].Field, "cahces") || !strings.Contains(first[1].Field, "databses") {
		t.Fatalf("expected sorted socket order; got %q then %q", first[0].Field, first[1].Field)
	}
	for i := 0; i < 8; i++ {
		again := LintUsesSocketNearMiss(na, socketCatalog())
		if len(again) != len(first) {
			t.Fatalf("run %d: length %d != %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j].Error() != first[j].Error() {
				t.Fatalf("run %d, advisory %d: %q != %q", i, j, again[j].Error(), first[j].Error())
			}
		}
	}
}

func TestLintUsesSocketNearMiss_SubsystemComponentWalked(t *testing.T) {
	// A typo is the same mistake wherever the component sits, and the locator
	// must name the real authoring site rather than a flattened one.
	uses := types.OrderedMap[types.UsesSocketValue]{}
	edges := types.OrderedMap[types.VerboseUsesEntry]{}
	edges.Set("db", types.VerboseUsesEntry{Target: "db"})
	uses.Set("databses", types.UsesSocketValue{Verbose: edges})

	inner := types.OrderedMap[types.ComponentInstance]{}
	inner.Set("web", types.ComponentInstance{Type: "core/devtool/adminer", Uses: uses})
	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("edge", types.Subsystem{Components: inner})

	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Subsystems: subs},
	})
	got := LintUsesSocketNearMiss(na, socketCatalog())
	if len(got) != 1 {
		t.Fatalf("expected 1 advisory from the subsystem component; got %d: %v", len(got), got)
	}
	wantField := `.spec.subsystems.edge.components["web"].uses["databses"]`
	if got[0].Field != wantField {
		t.Errorf("expected Field %q; got %q", wantField, got[0].Field)
	}
}
