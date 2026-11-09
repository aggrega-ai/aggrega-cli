// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	fixtureModule  = "testdata/module"
	fixtureCatalog = "testdata/catalog"
)

func fixtureProviders(t *testing.T) []provider {
	t.Helper()
	got, err := scan(fixtureModule, fixtureCatalog)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return got
}

// TestScanAssemblesBothHomesInManifestOrder covers the whole extraction: the
// manifest selects which providers are read and in what order, the overlay
// supplies the support claim, the published entity supplies the render-behavior
// claim, and a row for a non-provider socket is not a provider.
func TestScanAssemblesBothHomesInManifestOrder(t *testing.T) {
	got := fixtureProviders(t)

	wantNames := []string{"alpha", "beta", "gamma"}
	if len(got) != len(wantNames) {
		t.Fatalf("extracted %d providers, want %d: %+v", len(got), len(wantNames), got)
	}
	for i, name := range wantNames {
		if got[i].Name != name {
			t.Errorf("provider %d is %q, want %q", i, got[i].Name, name)
		}
	}

	if k := got[0].InputKinds; !k.Sourced || !k.Synthesized || !k.ArtifactsStandalone {
		t.Errorf("alpha input kinds not read from its published entity: %+v", k)
	}
	if n := len(got[0].Annotations); n != 2 {
		t.Fatalf("alpha declares %d annotations, want 2", n)
	}
	if got[0].Annotations[0].Key != "aggrega.ai/first" {
		t.Errorf("alpha's first declaration is %q, want the first authored key", got[0].Annotations[0].Key)
	}
	if want := []string{"sourced", "synthesized"}; strings.Join(got[0].Annotations[0].Scope, ",") != strings.Join(want, ",") {
		t.Errorf("alpha's first scope = %v, want %v", got[0].Annotations[0].Scope, want)
	}
}

// TestScanKeepsAShippedProviderThatDeclaresNothing is the completeness property
// the whole projection turns on. A provider whose overlay contributes no support
// claim is a provider that reads nothing, and it must still reach the page —
// driving the provider set from a listing of the declarations instead would drop
// it silently, which is indistinguishable from nobody having described it.
func TestScanKeepsAShippedProviderThatDeclaresNothing(t *testing.T) {
	got := fixtureProviders(t)

	var gamma *provider
	for i := range got {
		if got[i].Name == "gamma" {
			gamma = &got[i]
		}
	}
	if gamma == nil {
		t.Fatal("a shipped provider declaring no support claim was dropped from the extraction")
	}
	if len(gamma.Annotations) != 0 {
		t.Errorf("gamma declares %d annotations, want none", len(gamma.Annotations))
	}
	if !gamma.InputKinds.Sourced {
		t.Error("gamma's input kinds were not read; its rows would read as renders-nothing")
	}
}

// TestScanRefusesAShippedProviderWithNoPublishedEntity covers the case that
// would otherwise publish a full row set claiming the provider renders nothing.
// An extraction that cannot find the published claim has lost track of the
// catalog layout, and an all-declined row set is exactly the wrong published
// answer, so it must be an error.
func TestScanRefusesAShippedProviderWithNoPublishedEntity(t *testing.T) {
	catalog := t.TempDir()
	if err := os.MkdirAll(filepath.Join(catalog, "plugin-overlays", "provider"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalog, "plugin-overlays", "provider", "alpha.yaml"),
		[]byte("apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := scan(fixtureModule, catalog)
	if err == nil {
		t.Fatal("expected an error for a shipped provider with no published entity")
	}
	if !strings.Contains(err.Error(), "no published entity") {
		t.Errorf("error does not say what went wrong: %v", err)
	}
}

// TestProviderNamesRejectsAShortRow keeps a truncated manifest from yielding an
// empty provider name, which would address a catalog path of "".
func TestProviderNamesRejectsAShortRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.tsv")
	if err := os.WriteFile(path, []byte("provider\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := providerNames(path); err == nil {
		t.Fatal("expected an error for a row with too few fields")
	}
}

// TestNodeScopedRegionProjectsEveryCell checks the rendered grid against the
// declarations it projects, cell by cell, rather than against a frozen blob: a
// golden would pass on a table whose columns were transposed.
func TestNodeScopedRegionProjectsEveryCell(t *testing.T) {
	providers := fixtureProviders(t)
	region, err := renderNodeScopedRegion(providers)
	if err != nil {
		t.Fatalf("renderNodeScopedRegion: %v", err)
	}

	columns := nodeScopedKeys(providers)
	if want := []string{"aggrega.ai/first", "aggrega.ai/second"}; strings.Join(columns, ",") != strings.Join(want, ",") {
		t.Fatalf("column set = %v, want %v (first-declaration order)", columns, want)
	}

	rows := map[string][]string{}
	for _, l := range strings.Split(region, "\n") {
		if !strings.HasPrefix(l, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(l, "| "), " | ")
		rows[cells[0]+"/"+cells[1]] = cells[2:]
	}

	for _, p := range providers {
		for _, kind := range inputKindOrder {
			cells, ok := rows["`"+p.Name+"`/"+kind.label]
			if !ok {
				t.Fatalf("no row for %s/%s in:\n%s", p.Name, kind.label, region)
			}
			if len(cells) != len(columns) {
				t.Fatalf("%s/%s has %d cells, want %d", p.Name, kind.label, len(cells), len(columns))
			}
			for i, key := range columns {
				want := cell(p, kind.claimed(p.InputKinds), kind.scope, key)
				if cells[i] != want {
					t.Errorf("%s/%s %s cell = %q, want %q", p.Name, kind.label, key, cells[i], want)
				}
			}
		}
	}
}

// TestCellDistinguishesNotAppliedFromNotRendered pins the distinction the grid
// exists to carry. "the provider renders this path and applies nothing there" and
// "the provider does not render this path at all" are different answers, and
// collapsing them would tell an author their annotation was ignored on a path
// their node could never take.
func TestCellDistinguishesNotAppliedFromNotRendered(t *testing.T) {
	p := provider{
		Name:       "alpha",
		InputKinds: types.PluginInputKinds{Sourced: true},
		Annotations: []types.WellKnownAnnotation{
			{Key: "aggrega.ai/first", Scope: []string{types.WellKnownAnnotationScopeSourced}},
		},
	}
	cases := []struct {
		name        string
		kindClaimed bool
		scope, key  string
		want        string
	}{
		{"declared on a rendered path", true, types.WellKnownAnnotationScopeSourced, "aggrega.ai/first", applied},
		{"undeclared on a rendered path", true, types.WellKnownAnnotationScopeSourced, "aggrega.ai/other", notApplied},
		{"declared but the path is not rendered", false, types.WellKnownAnnotationScopeSynthesized, "aggrega.ai/first", notRendered},
		{"undeclared and the path is not rendered", false, types.WellKnownAnnotationScopeSynthesized, "aggrega.ai/other", notRendered},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cell(p, c.kindClaimed, c.scope, c.key); got != c.want {
				t.Errorf("cell = %q, want %q", got, c.want)
			}
		})
	}
}

// TestBoundaryScopedRegionCarriesOnlyBoundaryKeys pins the partition between the
// two regions: a key belongs to exactly one of them, decided by its declared
// scope and by nothing else.
func TestBoundaryScopedRegionCarriesOnlyBoundaryKeys(t *testing.T) {
	providers := fixtureProviders(t)
	region, err := renderBoundaryScopedRegion(providers)
	if err != nil {
		t.Fatalf("renderBoundaryScopedRegion: %v", err)
	}
	if !strings.Contains(region, "aggrega.ai/edge") {
		t.Errorf("the boundary-scoped key is missing:\n%s", region)
	}
	for _, key := range nodeScopedKeys(providers) {
		if strings.Contains(region, key) {
			t.Errorf("node-scoped key %q leaked into the boundary table:\n%s", key, region)
		}
	}
}

// TestBoundaryScopedRegionRefusesAnEmptyTable covers the extraction failure that
// would otherwise publish a claim about the product. An empty table reads as "no
// key is resolved per boundary", which is a statement, not an absence.
func TestBoundaryScopedRegionRefusesAnEmptyTable(t *testing.T) {
	_, err := renderBoundaryScopedRegion([]provider{{Name: "alpha"}})
	if err == nil {
		t.Fatal("expected an error when no provider declares a boundary-scoped key")
	}
	if !strings.Contains(err.Error(), "boundary-scoped") {
		t.Errorf("error does not say what went wrong: %v", err)
	}
}

// TestRegionsStateTheirOwnScope pins the note that keeps the grid honest about
// what it covers. Without it the grid reads as the complete set of providers,
// which it is not.
func TestRegionsStateTheirOwnScope(t *testing.T) {
	region, err := renderNodeScopedRegion(fixtureProviders(t))
	if err != nil {
		t.Fatalf("renderNodeScopedRegion: %v", err)
	}
	if !strings.Contains(region, "distributed with the compiler") {
		t.Errorf("the grid does not state its own scope:\n%s", region)
	}
}

// TestRegionsAreDeterministic guards the byte-identity a drift comparison rests
// on: regenerating into a copy and diffing would report drift against itself if
// a render varied between runs. Both regions are covered because each walks a
// different collection.
func TestRegionsAreDeterministic(t *testing.T) {
	providers := fixtureProviders(t)
	renders := []struct {
		name string
		fn   func([]provider) (string, error)
	}{
		{"node-scoped", renderNodeScopedRegion},
		{"boundary-scoped", renderBoundaryScopedRegion},
	}
	for _, r := range renders {
		t.Run(r.name, func(t *testing.T) {
			first, err := r.fn(providers)
			if err != nil {
				t.Fatalf("first render: %v", err)
			}
			for i := 0; i < 8; i++ {
				again, err := r.fn(providers)
				if err != nil {
					t.Fatalf("render %d: %v", i, err)
				}
				if again != first {
					t.Fatalf("render %d differs from the first:\n%q\n%q", i, again, first)
				}
			}
		})
	}
}

// TestRegionsRefuseACellBreakingTableSeparator covers the one input that would
// render a structurally broken table rather than a wrong one.
func TestRegionsRefuseACellBreakingTableSeparator(t *testing.T) {
	if _, err := renderNodeScopedRegion([]provider{{Name: "a|b"}}); err == nil {
		t.Fatal("expected an error for a provider name containing a table separator")
	}
	bad := []provider{{
		Name:        "alpha",
		Annotations: []types.WellKnownAnnotation{{Key: "aggrega.ai/x", Purpose: "a | b", Scope: []string{types.WellKnownAnnotationScopeBoundary}}},
	}}
	if _, err := renderBoundaryScopedRegion(bad); err == nil {
		t.Fatal("expected an error for a purpose containing a table separator")
	}
}

// TestScanRefusesAShippedDeclarationWithNoScope is the completeness rung the
// generator carries for the shipped set. A scope is optional on the authored
// surface, so a key without one is a legal weaker claim — but it contributes no
// cell here, so an unscoped key in what we ship would shrink the published
// matrix with nothing saying so.
func TestScanRefusesAShippedDeclarationWithNoScope(t *testing.T) {
	catalog := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		overlay := filepath.Join(catalog, "plugin-overlays", "provider")
		entity := filepath.Join(catalog, "aggrega-catalog", "plugin", "provider", name, "1.0.0")
		for _, d := range []string{overlay, entity} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		body := "apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: " + name + "\n"
		if name == "alpha" {
			body += "spec:\n  provider:\n    wellKnownAnnotations:\n      - key: aggrega.ai/unscoped\n        purpose: No scope declared.\n"
		}
		if err := os.WriteFile(filepath.Join(overlay, name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(entity, "plugin.yaml"),
			[]byte("apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: "+name+"\nspec:\n  provider:\n    contract:\n      inputKinds:\n        sourced: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := scan(fixtureModule, catalog)
	if err == nil {
		t.Fatal("expected an error for a shipped declaration carrying no scope")
	}
	if !strings.Contains(err.Error(), "no scope") {
		t.Errorf("error does not say what went wrong: %v", err)
	}
}
