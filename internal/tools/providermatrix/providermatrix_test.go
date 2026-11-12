// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const fixtureRoot = "testdata/root"

// kinds builds an input-kind claim positionally, in the home type's declaration
// order, so a fixture reads as one line. It names the kinds explicitly rather than
// taking a variadic list: a new kind added to the home type must break this
// signature, which is what forces every fixture below to be re-read rather than
// silently defaulting the new kind to false.
func kinds(sourced, synthesized, artifactsStandalone bool) types.PluginInputKinds {
	return types.PluginInputKinds{
		Sourced:             sourced,
		Synthesized:         synthesized,
		ArtifactsStandalone: artifactsStandalone,
	}
}

// TestScanReadsDeclaredContractsInManifestOrder covers the whole extraction: the
// manifest selects which guests are read and in what order, the guest's own
// declaration supplies every value, and a row for a non-provider socket is not a
// provider.
func TestScanReadsDeclaredContractsInManifestOrder(t *testing.T) {
	got, err := scan(fixtureRoot)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	want := []provider{
		{Name: "alpha", PluginInputKinds: kinds(true, true, true), Omits: true},
		{Name: "beta", PluginInputKinds: kinds(true, false, false), DelimLeft: "[[", DelimRight: "]]", BoundaryArtifacts: true},
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %d providers, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("provider %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

// TestScanRefusesGuestDeclaringNoContract is the case that would otherwise
// publish a row claiming the provider accepts nothing. An extraction that finds
// no declaration has lost track of the declaration form, and a silently
// all-declined row is exactly the wrong published answer, so it must be an error.
func TestScanRefusesGuestDeclaringNoContract(t *testing.T) {
	_, err := contractOf(filepath.Join(fixtureRoot, "plugins", "nocontract-provider"))
	if err == nil {
		t.Fatal("expected an error for a guest declaring no contract")
	}
	if !strings.Contains(err.Error(), "descriptor form moved") {
		t.Errorf("error does not say what went wrong: %v", err)
	}
}

// TestScanRefusesGuestDeclaringTwoContracts covers the other ambiguity: with two
// declarations this projection cannot know which one describes the guest, so
// picking either would publish a coin flip.
func TestScanRefusesGuestDeclaringTwoContracts(t *testing.T) {
	_, err := contractOf(filepath.Join(fixtureRoot, "plugins", "double-provider"))
	if err == nil {
		t.Fatal("expected an error for a guest declaring two contracts")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error does not say what went wrong: %v", err)
	}
}

// TestContractIsReadUnderAnyImportAlias pins the alias-insensitivity the
// extraction depends on: guests import the plugin development kit under
// different names, and matching the qualified name would read one guest and
// silently skip its neighbour.
func TestContractIsReadUnderAnyImportAlias(t *testing.T) {
	alpha, err := contractOf(filepath.Join(fixtureRoot, "plugins", "alpha-provider"))
	if err != nil {
		t.Fatalf("alpha (plain import): %v", err)
	}
	beta, err := contractOf(filepath.Join(fixtureRoot, "plugins", "beta-provider"))
	if err != nil {
		t.Fatalf("beta (aliased import): %v", err)
	}
	if !alpha.Sourced || !beta.Sourced {
		t.Errorf("a declaration was not read: alpha=%+v beta=%+v", alpha, beta)
	}
}

// TestAlongsideArtifactsFollowsGatePrecedence pins the one derived column
// against the rule that produces it: the input-kind gate classifies by source,
// then workload, then artifacts, so alongside artifacts need a primary to ride
// with and the standalone claim does not enter into it.
func TestAlongsideArtifactsFollowsGatePrecedence(t *testing.T) {
	cases := []struct {
		name string
		p    provider
		want bool
	}{
		{"sourced only", provider{PluginInputKinds: kinds(true, false, false)}, true},
		{"synthesized only", provider{PluginInputKinds: kinds(false, true, false)}, true},
		{"both primaries", provider{PluginInputKinds: kinds(true, true, false)}, true},
		{"standalone artifacts but no primary", provider{PluginInputKinds: kinds(false, false, true)}, false},
		{"no input kind at all", provider{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.AcceptsAlongsideArtifacts(); got != c.want {
				t.Errorf("AcceptsAlongsideArtifacts() = %t, want %t", got, c.want)
			}
		})
	}
}

// TestRenderRegionProjectsEveryClaim checks the rendered grid against the
// declarations it projects, column by column, rather than against a frozen
// blob: a golden would pass on a table whose columns were transposed.
func TestRenderRegionProjectsEveryClaim(t *testing.T) {
	providers, err := scan(fixtureRoot)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	region, err := renderRegion(providers)
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}

	lines := strings.Split(region, "\n")
	rows := map[string][]string{}
	for _, l := range lines {
		if !strings.HasPrefix(l, "| ") || strings.HasPrefix(l, "| --- ") || strings.HasPrefix(l, "| Provider ") {
			continue
		}
		cells := strings.Split(strings.Trim(l, "| "), " | ")
		rows[cells[0]] = cells[1:]
	}

	for _, p := range providers {
		cells, ok := rows[p.Name]
		if !ok {
			t.Fatalf("no row for provider %q in:\n%s", p.Name, region)
		}
		if len(cells) != 6 {
			t.Fatalf("provider %q has %d capability cells, want 6", p.Name, len(cells))
		}
		if want := mark(p.Sourced); cells[0] != want {
			t.Errorf("%s sourced cell = %q, want %q", p.Name, cells[0], want)
		}
		if want := mark(p.Synthesized); cells[1] != want {
			t.Errorf("%s workload cell = %q, want %q", p.Name, cells[1], want)
		}
		if got := strings.HasPrefix(cells[2], yes); got != p.AcceptsAlongsideArtifacts() {
			t.Errorf("%s alongside cell = %q, want accepted=%t", p.Name, cells[2], p.AcceptsAlongsideArtifacts())
		}
		if want := mark(p.ArtifactsStandalone); cells[3] != want {
			t.Errorf("%s artifacts-only cell = %q, want %q", p.Name, cells[3], want)
		}
		if want := mark(p.Omits); cells[4] != want {
			t.Errorf("%s omits cell = %q, want %q", p.Name, cells[4], want)
		}
		if want := mark(p.BoundaryArtifacts); cells[5] != want {
			t.Errorf("%s boundary artifacts cell = %q, want %q", p.Name, cells[5], want)
		}
	}
}

// TestTheOmitsColumnIsMarkedOnlyForAGuestDeclaringTheClaim reads the claim from
// guest source end to end: alpha's declaration sets it and beta's leaves it out,
// so the grid marks alpha's cell and leaves beta's unmarked. The expected marks
// are fixed here rather than taken from the scan, because a scan that stopped
// reading the claim would report both guests as declining it, and a check built
// from that scan would agree with it.
func TestTheOmitsColumnIsMarkedOnlyForAGuestDeclaringTheClaim(t *testing.T) {
	providers, err := scan(fixtureRoot)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	region, err := renderRegion(providers)
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}
	if !strings.Contains(region, "| `omits:` |") {
		t.Fatalf("the grid carries no omits column:\n%s", region)
	}
	want := map[string]string{"alpha": yes, "beta": no}
	seen := map[string]bool{}
	for _, l := range strings.Split(region, "\n") {
		cells := strings.Split(strings.Trim(l, "| "), " | ")
		name := cells[0]
		wantMark, ok := want[name]
		if !ok {
			continue
		}
		seen[name] = true
		// The omits column is the second to last: the name, the four input-kind
		// cells, then the omits cell.
		if got := cells[5]; got != wantMark {
			t.Errorf("%s omits cell = %q, want %q: %s", name, got, wantMark, l)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("no row for %s in:\n%s", name, region)
		}
	}
}

// TestTheBoundaryArtifactsColumnIsMarkedOnlyForAGuestDeclaringTheClaim reads the
// shared-files claim from guest source end to end, crossed with the omits claim:
// beta's declaration sets it and alpha's leaves it out, the opposite of the omits
// claim, so a grid that wrote one claim into the other's column would mark both
// cells wrongly. The expected marks are fixed here rather than taken from the scan,
// for the reason the omits column's test gives.
func TestTheBoundaryArtifactsColumnIsMarkedOnlyForAGuestDeclaringTheClaim(t *testing.T) {
	providers, err := scan(fixtureRoot)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	region, err := renderRegion(providers)
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}
	if !strings.Contains(region, "| `boundary.artifacts:` |") {
		t.Fatalf("the grid carries no boundary artifacts column:\n%s", region)
	}
	want := map[string]string{"alpha": no, "beta": yes}
	seen := map[string]bool{}
	for _, l := range strings.Split(region, "\n") {
		cells := strings.Split(strings.Trim(l, "| "), " | ")
		name := cells[0]
		wantMark, ok := want[name]
		if !ok {
			continue
		}
		seen[name] = true
		if got := cells[len(cells)-1]; got != wantMark {
			t.Errorf("%s boundary artifacts cell = %q, want %q: %s", name, got, wantMark, l)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("no row for %s in:\n%s", name, region)
		}
	}
}

// TestOnlyTheTerraformGuestClaimsBoundaryArtifacts reads the shipped guests'
// own declarations and holds the shared-files claim to the one provider whose
// target reads a file placed beside its assembled output as part of it. A second
// guest making the claim is a decision about that provider's target, so it fails
// here rather than reaching the published grid unreviewed.
func TestOnlyTheTerraformGuestClaimsBoundaryArtifacts(t *testing.T) {
	providers, err := scan(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("scan the shipped guests: %v", err)
	}
	if len(providers) == 0 {
		t.Fatal("the scan read no shipped guest, so this check certifies nothing")
	}
	var claiming []string
	for _, p := range providers {
		if p.BoundaryArtifacts {
			claiming = append(claiming, p.Name)
		}
	}
	if len(claiming) != 1 || claiming[0] != "terraform" {
		t.Errorf("guests claiming boundary artifacts = %v; want exactly [terraform]", claiming)
	}
}

// TestRenderRegionNamesADeclaredDelimiterPair pins the one cell that carries
// more than a mark. The pair is what an author types in an imported artifact
// file, so losing it from the cell would drop the only actionable detail the
// declaration carries for that provider.
func TestRenderRegionNamesADeclaredDelimiterPair(t *testing.T) {
	region, err := renderRegion([]provider{
		{Name: "beta", PluginInputKinds: kinds(true, false, false), DelimLeft: "[[", DelimRight: "]]"},
		{Name: "alpha", PluginInputKinds: kinds(true, false, false)},
	})
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}
	if !strings.Contains(region, "`[[` `]]`") {
		t.Errorf("declared delimiter pair missing from the grid:\n%s", region)
	}
	// A provider taking the default pair must not acquire a phantom one.
	for _, l := range strings.Split(region, "\n") {
		if strings.HasPrefix(l, "| alpha ") && strings.Contains(l, "delimiters") {
			t.Errorf("a provider declaring no delimiter pair grew one: %s", l)
		}
	}
}

// TestRenderRegionStatesItsOwnScope pins the note that keeps the grid honest
// about what it covers. Without it the grid reads as the complete set of
// providers, which it is not.
func TestRenderRegionStatesItsOwnScope(t *testing.T) {
	region, err := renderRegion([]provider{{Name: "alpha", PluginInputKinds: kinds(true, false, false)}})
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}
	if !strings.Contains(region, "distributed with the compiler") {
		t.Errorf("the grid does not state its own scope:\n%s", region)
	}
}

// TestRenderRegionIsDeterministic guards the byte-identity the drift gate rests
// on: the gate regenerates into a copy and diffs, so a render that varied
// between runs would report drift against itself.
func TestRenderRegionIsDeterministic(t *testing.T) {
	providers, err := scan(fixtureRoot)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	first, err := renderRegion(providers)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := renderRegion(providers)
		if err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
		if again != first {
			t.Fatalf("render %d differs from the first:\n%q\n%q", i, again, first)
		}
	}
}

// TestRenderRegionRefusesACellBreakingTableSeparator covers the one input that
// would render a structurally broken table rather than a wrong one.
func TestRenderRegionRefusesACellBreakingTableSeparator(t *testing.T) {
	if _, err := renderRegion([]provider{{Name: "a|b", PluginInputKinds: kinds(true, false, false)}}); err == nil {
		t.Fatal("expected an error for a provider name containing a table separator")
	}
}

// TestProviderRowsRejectsAShortRow keeps a truncated manifest from yielding a
// guest path of "", which would read the module root as a guest directory.
func TestProviderRowsRejectsAShortRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.tsv")
	if err := os.WriteFile(path, []byte("provider\talpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := providerRows(path); err == nil {
		t.Fatal("expected an error for a row with too few fields")
	}
}
