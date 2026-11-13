// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fullstackBindingRun drives both entries a build and a Release-scoped
// validation reach — the render entry with the render skipped, and the
// pre-render entry — over the fullstack fixture, with the workspace carrying the
// given provider bindings. The fixture's Recipes ask for helm, helm-chart and
// compose; the fixture catalog carries the three provider plugins serving them.
//
// It returns each entry's advisories and error, and everything the runs
// displayed. The advisory channel is read off each entry's own result, because
// the entry is what owns the log.
func fullstackBindingRun(t *testing.T, bindings []workspaceconfig.PluginRef) (buildAdvisories, validateAdvisories []abi.Diagnostic, buildErr, validateErr error, displayed string) {
	t.Helper()
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	inputs := func(display io.Writer) pipeline.Inputs {
		cfg := fixtureWorkspaceConfig()
		cfg.Plugins.Providers = bindings
		return pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     fullstackAggregatePath,
			WorkspaceConfig:   cfg,
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   fixtureCompilerVersion,
			Display:           display,
		}
	}
	// Both runs are held to the display they are handed: the binding advisory is
	// shown there, and nothing reaches the process's own standard error.
	displayed = captureDisplayOnly(t, func(display io.Writer) {
		outcome, err := pipeline.RunPipeline(context.Background(), inputs(display), wiredLookup(cat, manifest),
			cat.RecipeLookupWithManifest(manifest), cat.SubAggregateLoader(), pipeline.Options{SkipRender: true})
		buildAdvisories, buildErr = outcome.Advisories(), err
		tier4, err := pipeline.RunTier4Pipeline(context.Background(), inputs(display), wiredLookup(cat, manifest),
			cat.RecipeLookupWithManifest(manifest), cat.SubAggregateLoader())
		if tier4 != nil {
			validateAdvisories = tier4.Advisories
		}
		validateErr = err
	})
	return buildAdvisories, validateAdvisories, buildErr, validateErr, displayed
}

// entriesCoded returns the advisory entries carrying the code the given condition
// registers, read off the condition's accessor rather than spelled.
func entriesCoded(advisories []abi.Diagnostic, condition error) []abi.Diagnostic {
	code, _ := cerrors.CodeOf(condition)
	var found []abi.Diagnostic
	for _, a := range advisories {
		if a.Code == code {
			found = append(found, a)
		}
	}
	return found
}

// TestAProviderBindingNothingConsultsIsJudgedByBothEntries proves the shared
// steps a build and a Release-scoped validation both run judge every workspace
// provider binding, whether or not a Recipe asks for its capability: a binding
// naming a plugin that does not serve its capability halts both entries, and a
// capability-only binding no plugin serves reaches both entries' advisory
// channels at warning severity, carrying its condition's code, while both runs
// succeed. The controls are the legal bindings — a served capability bound to its
// plugin and a served capability bound by name alone — which produce neither.
//
// It is deliberately not parallel: it redirects the process diagnostic stream.
func TestAProviderBindingNothingConsultsIsJudgedByBothEntries(t *testing.T) {
	cases := []struct {
		name     string
		bindings []workspaceconfig.PluginRef
		halts    bool
		advises  bool
	}{
		{
			name:     "a uri binding whose plugin does not serve its capability halts",
			bindings: []workspaceconfig.PluginRef{{Capability: "hlem", URI: "provider/helm"}},
			halts:    true,
		},
		{
			name:     "a capability-only binding no plugin serves advises and the run succeeds",
			bindings: []workspaceconfig.PluginRef{{Capability: "hlem"}},
			advises:  true,
		},
		{
			name:     "control: a served capability bound to the plugin serving it is silent",
			bindings: []workspaceconfig.PluginRef{{Capability: "compose", URI: "provider/compose"}},
		},
		{
			name:     "control: a served capability bound by name alone is silent",
			bindings: []workspaceconfig.PluginRef{{Capability: "helm-chart"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buildAdv, validateAdv, buildErr, validateErr, displayed := fullstackBindingRun(t, tc.bindings)
			runs := []struct {
				entry      string
				err        error
				advisories []abi.Diagnostic
			}{
				{"build", buildErr, buildAdv},
				{"validation", validateErr, validateAdv},
			}
			for _, run := range runs {
				if tc.halts {
					if !errors.Is(run.err, catalog.ErrProviderBindingUnsatisfied()) {
						t.Errorf("%s: want ErrProviderBindingUnsatisfied, got %v\ndisplayed:\n%s", run.entry, run.err, displayed)
					}
					continue
				}
				if run.err != nil {
					t.Fatalf("%s: the run must succeed; got %v\ndisplayed:\n%s", run.entry, run.err, displayed)
				}
			}
			if tc.halts {
				return
			}
			unserved := catalog.ErrProviderBindingCapabilityUnserved()
			for _, run := range runs {
				found := entriesCoded(run.advisories, unserved)
				if !tc.advises {
					if len(found) != 0 {
						t.Errorf("%s: a legal binding was advised about: %+v", run.entry, found)
					}
					continue
				}
				if len(found) != 1 {
					t.Fatalf("%s: want one advisory carrying the unserved-capability code, got %d.\nchannel: %+v\ndisplayed:\n%s",
						run.entry, len(found), run.advisories, displayed)
				}
				if found[0].Severity != abi.SeverityWarn {
					t.Errorf("%s: advisory severity = %q; want %q", run.entry, found[0].Severity, abi.SeverityWarn)
				}
				if !strings.Contains(found[0].Message, `"hlem"`) || !strings.Contains(found[0].Message, `one edit from "helm"`) {
					t.Errorf("%s: the advisory must name the binding's capability and its near miss; got %q", run.entry, found[0].Message)
				}
			}
			sentence := unserved.Error()
			if tc.advises && strings.Count(displayed, sentence) != 2 {
				t.Errorf("the advisory must be displayed once per run; displayed:\n%s", displayed)
			}
			if !tc.advises && strings.Contains(displayed, sentence) {
				t.Errorf("a legal binding was displayed as an advisory; displayed:\n%s", displayed)
			}
		})
	}
}
