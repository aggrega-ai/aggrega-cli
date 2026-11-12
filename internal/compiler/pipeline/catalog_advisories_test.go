// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// catalogAdvisoryProbe is an advisory a catalog view raised over an entity it
// resolved, in the shape the x-aggrega-* discouragement takes.
var catalogAdvisoryProbe = renderer.Diagnostic{
	Severity: abi.SeverityWarn,
	Message: `entity "core/probe" authors x-aggrega-probe at </values/database/x-aggrega-probe> ` +
		`in schema content; recommended path is the appropriate declarative table on spec`,
	SourceRef: "core/probe",
}

// tier4WithCatalogAdvisories runs the Release-scoped entry over the advisory
// fixture as authored, handing it the probe as its catalog view's advisories,
// and returns the run's channel and display.
func tier4WithCatalogAdvisories(t *testing.T, shown bool) ([]renderer.Diagnostic, string) {
	t.Helper()
	rel, err := resolver.LoadRelease(advisoryOverrideReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(advisoryOverrideManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", advisoryCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	asked := 0
	var out *pipeline.Tier4Result
	var runErr error
	displayed := captureDisplayOnly(t, func(display io.Writer) {
		out, runErr = pipeline.RunTier4Pipeline(context.Background(), pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: readFixtureBytes(t, advisoryOverrideAggregatePath),
			AggregatePath:     advisoryOverrideAggregatePath,
			WorkspaceConfig:   fixtureWorkspaceConfig(),
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   fixtureCompilerVersion,
			Display:           display,
			CatalogAdvisories: func() []renderer.Diagnostic {
				asked++
				return []renderer.Diagnostic{catalogAdvisoryProbe}
			},
			ResolutionAdvisoriesShown: shown,
		}, wiredLookup(cat, manifest), cat.RecipeLookupWithManifest(manifest), nil)
	})
	if runErr != nil {
		t.Fatalf("the fixture as authored must compile, or the run never reached the resolution: %v", runErr)
	}
	if asked != 1 {
		t.Errorf("the run asked its catalog view for advisories %d times, want once", asked)
	}
	return out.Advisories, displayed
}

// TestTheCatalogViewsAdvisoriesAreShownAndRecordedByTheRun holds the advisories
// a catalog view raised over the entities a run reads to the run's two outlets:
// recorded on the run's one advisory channel — which a surface reading no
// terminal, an editor's diagnostics among them, reads — and shown on the run's
// display as the one warning line the shared formatter composes, each exactly
// once, and nothing on the process's own standard error.
//
// 🔴 PLANTED DEFECT: drop the drain of Inputs.CatalogAdvisories from the
// resolution (ResolveAndValidate), or write its display to the process stream, or
// record it twice — and this fails.
func TestTheCatalogViewsAdvisoriesAreShownAndRecordedByTheRun(t *testing.T) {
	advisories, displayed := tier4WithCatalogAdvisories(t, false)

	var recorded []renderer.Diagnostic
	for _, a := range advisories {
		if a.Message == catalogAdvisoryProbe.Message {
			recorded = append(recorded, a)
		}
	}
	if len(recorded) != 1 {
		t.Fatalf("the channel carries the catalog view's advisory %d times, want once.\nchannel: %+v",
			len(recorded), advisories)
	}
	if recorded[0].SourceRef != "core/probe" || recorded[0].Severity != abi.SeverityWarn {
		t.Errorf("the recorded entry is not the view's advisory as raised: %+v", recorded[0])
	}
	line := "  warning: " + catalogAdvisoryProbe.Message
	if n := strings.Count(displayed, line+"\n"); n != 1 {
		t.Errorf("the display shows the advisory's warning line %d times, want once.\nwant line: %s\ndisplayed:\n%s",
			n, line, displayed)
	}
}

// TestACatalogAdvisoryAlreadyShownIsRecordedAndNotShownAgain holds the view's
// advisories to the terms the resolution's own advisories are shown on: an
// operation that resolves one Release twice shows them at its first resolution,
// so the second records them without showing them again.
func TestACatalogAdvisoryAlreadyShownIsRecordedAndNotShownAgain(t *testing.T) {
	advisories, displayed := tier4WithCatalogAdvisories(t, true)
	found := false
	for _, a := range advisories {
		if a.Message == catalogAdvisoryProbe.Message {
			found = true
		}
	}
	if !found {
		t.Errorf("an advisory already shown was not recorded; the channel must carry it whatever the display does")
	}
	if strings.Contains(displayed, catalogAdvisoryProbe.Message) {
		t.Errorf("an advisory the operation already showed was shown again:\n%s", displayed)
	}
}
