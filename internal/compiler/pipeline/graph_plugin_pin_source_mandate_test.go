// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheGraphEntryRefusesARunThatNamedNoPluginPinSource drives the one body
// every graph-producing surface reaches and proves it refuses before it resolves
// anything, rather than resolving plugins against an address nobody supplied.
//
// The refusal is asserted at the collecting entry for the same reason its sibling
// is: that is the body the refusing entry delegates to, so a guard placed on the
// refusing entry alone would pass while the render and validation paths went
// unguarded.
//
// The workspace configuration is supplied here so the run reaches THIS refusal.
// Without it the sibling refusal fires first and the test would pass while
// proving nothing about pins.
func TestTheGraphEntryRefusesARunThatNamedNoPluginPinSource(t *testing.T) {
	reached := false
	err := buildGraphCollectingFindings(
		context.Background(),
		Inputs{WorkspaceConfig: defaultWorkspaceConfigForTest()},
		resolver.CatalogLookup{},
		nil,
		nil,
		func(*GraphScope) error {
			reached = true
			return nil
		},
	)
	if err == nil {
		t.Fatal("a run naming no plugin pin source was accepted; its plugins would have resolved against an empty address")
	}
	if reached {
		t.Fatal("the continuation ran: the refusal did not precede the graph build")
	}
	if !strings.Contains(err.Error(), "no plugin pin source was supplied") {
		t.Fatalf("the refusal does not name what was omitted: %v", err)
	}
}

// TestTheGraphEntryAcceptsARunThatNamedAPinFreeSource is the other half of the
// same property, and it is what makes the refusal a DISTINCTION rather than a
// ban: a surface with genuinely nothing to pin against is a legitimate run and
// must not be refused for resembling an omission. Without this half, an entry
// that refused every run would satisfy the guard above.
//
// This is the half the two harnesses depend on. They run against no workspace at
// all, so a pin-free source is what they mean — and the whole point of making it
// a written value is that they can go on saying it.
//
// It drives a real workspace all the way through, so the assertion is that the
// graph was produced rather than merely that one particular error did not appear.
func TestTheGraphEntryAcceptsARunThatNamedAPinFreeSource(t *testing.T) {
	fixture := workspaceFixture{
		aggregatePath: slotAggregatePath,
		releasePath:   slotReleasePath,
		manifestPath:  slotManifestPath,
		catalogPath:   slotCatalogPath,
	}
	rel, err := resolver.LoadRelease(fixture.releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fixture.manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggregate, err := os.ReadFile(fixture.aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", fixture.catalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	built := false
	runErr := buildGraphCollectingFindings(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggregate,
			AggregatePath:     fixture.aggregatePath,
			ReleasePath:       fixture.releasePath,
			WorkspaceConfig:   &workspaceconfig.Config{},
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest).Recipe,
		cat.SubAggregateLoader(),
		func(gs *GraphScope) error {
			built = gs.IRGraph != nil
			return nil
		},
	)
	if runErr != nil {
		t.Fatalf("a run naming a pin-free source did not complete: %v", runErr)
	}
	if !built {
		t.Fatal("the continuation ran but no graph reached it")
	}
}

// TestTheResolveEntryRefusesARunThatNamedNoPluginPinSource holds the same
// property at the SECOND exported entry a caller can reach the plugin resolver
// through. That entry runs the annotation-transform role, which resolves a plugin
// exactly as the graph body does, so a mandate that covered only the graph entry
// would leave a caller picking this one resolving against an address nobody
// supplied — the very state the mandate exists to make unreachable.
//
// The workspace configuration is supplied so the run reaches THIS refusal rather
// than halting earlier for an unrelated reason.
func TestTheResolveEntryRefusesARunThatNamedNoPluginPinSource(t *testing.T) {
	reached := false
	err := ResolveAndValidate(
		context.Background(),
		Inputs{WorkspaceConfig: defaultWorkspaceConfigForTest()},
		resolver.CatalogLookup{},
		nil,
		func(*resolver.ResolveResult) error {
			reached = true
			return nil
		},
	)
	if err == nil {
		t.Fatal("a run naming no plugin pin source was accepted; its plugins would have resolved against an empty address")
	}
	if reached {
		t.Fatal("the continuation ran: the refusal did not precede the resolve")
	}
	if !strings.Contains(err.Error(), "no plugin pin source was supplied") {
		t.Fatalf("the refusal does not name what was omitted: %v", err)
	}
}

// TestTheResolveEntryAcceptsARunThatNamedAPinFreeSource is that entry's other
// half, and it carries the same weight there as its counterpart does at the graph
// entry: without it, an entry that refused every run would satisfy the guard
// above. It drives a real workspace so the assertion is that the resolve was
// produced rather than merely that one particular error did not appear.
func TestTheResolveEntryAcceptsARunThatNamedAPinFreeSource(t *testing.T) {
	fixture := workspaceFixture{
		aggregatePath: slotAggregatePath,
		releasePath:   slotReleasePath,
		manifestPath:  slotManifestPath,
		catalogPath:   slotCatalogPath,
	}
	rel, err := resolver.LoadRelease(fixture.releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fixture.manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggregate, err := os.ReadFile(fixture.aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", fixture.catalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	resolved := false
	runErr := ResolveAndValidate(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggregate,
			AggregatePath:     fixture.aggregatePath,
			ReleasePath:       fixture.releasePath,
			WorkspaceConfig:   &workspaceconfig.Config{},
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest).Recipe,
		func(rr *resolver.ResolveResult) error {
			resolved = rr != nil && rr.Release != nil
			return nil
		},
	)
	if runErr != nil {
		t.Fatalf("a run naming a pin-free source did not complete: %v", runErr)
	}
	if !resolved {
		t.Fatal("the continuation ran but no resolved view reached it")
	}
}
