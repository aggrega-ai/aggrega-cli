// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// defaultWorkspaceConfigForTest is the configuration a fixture with no workspace
// file runs against, as a pointer the inputs can carry. A fixture supplies it
// explicitly for the same reason a production surface does: the graph entry
// refuses an unsupplied configuration, and "the defaults are in force" and
// "nobody said" are different facts.
//
// It is deliberately test-only. A production helper handing out a default would
// be the escape hatch the refusal exists to close — a caller with a real
// workspace could reach for it to satisfy the compiler and get exactly the
// transform-less graph the refusal is there to stop.
func defaultWorkspaceConfigForTest() *workspaceconfig.Config {
	c := workspaceconfig.Default()
	return &c
}

// fixtureInputs is the inputs a fixture with no workspace at all runs against:
// the defaults in force, and no pin source, both stated rather than omitted.
//
// It exists because both are REQUIRED at the entry, so every fixture would
// otherwise repeat the same two assignments — and a repetition is what drifts
// when a third mandate is added. A fixture needing more fields starts from this
// value and sets them.
func fixtureInputs() Inputs {
	return Inputs{
		WorkspaceConfig: defaultWorkspaceConfigForTest(),
		PluginPins:      pluginpath.NoPins(),
	}
}

// TestTheGraphEntryRefusesARunThatSuppliedNoWorkspaceConfiguration drives the one
// body every graph-producing surface reaches and proves it refuses before it
// resolves anything, rather than building a graph with the roles that shape it
// silently skipped.
//
// The refusal is asserted at the collecting entry because that is the body the
// refusing entry delegates to; a guard placed on the refusing entry alone would
// pass while the collecting one — which the render and validation paths take —
// went unguarded.
func TestTheGraphEntryRefusesARunThatSuppliedNoWorkspaceConfiguration(t *testing.T) {
	reached := false
	err := buildGraphCollectingFindings(
		context.Background(),
		Inputs{},
		resolver.CatalogLookup{},
		renderer.RecipeLookup{},
		nil,
		func(*GraphScope) error {
			reached = true
			return nil
		},
	)
	if err == nil {
		t.Fatal("a run supplying no workspace configuration was accepted; the transform roles would have been skipped in silence")
	}
	if reached {
		t.Fatal("the continuation ran: the refusal did not precede the graph build")
	}
	if !strings.Contains(err.Error(), "no workspace configuration was supplied") {
		t.Fatalf("the refusal does not name what was omitted: %v", err)
	}
}

// TestTheGraphEntryAcceptsASuppliedButEmptyWorkspaceConfiguration is the other
// half of the same property, and it is what makes the refusal a DISTINCTION
// rather than a ban: a workspace that attaches nothing is a legitimate run and
// must not be refused for resembling an omission. Without this half, an entry
// that refused every run would satisfy the guard above.
//
// It drives a real workspace all the way through, so the assertion is that the
// graph was produced rather than merely that one particular error did not
// appear.
func TestTheGraphEntryAcceptsASuppliedButEmptyWorkspaceConfiguration(t *testing.T) {
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
	// An EMPTY configuration, not the defaults: the point is that emptiness is
	// accepted, so the value under test carries nothing at all.
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
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		func(gs *GraphScope) error {
			built = gs.IRGraph != nil
			return nil
		},
	)
	if runErr != nil {
		t.Fatalf("a supplied-but-empty configuration did not complete: %v", runErr)
	}
	if !built {
		t.Fatal("the continuation ran but no graph reached it")
	}
}
