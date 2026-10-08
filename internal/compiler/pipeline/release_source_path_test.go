// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The committed workspace whose Release — a file not named release.yaml — writes
// on a boundary an annotation the Manifest binds to a Feature, so the graph
// carries an attachment whose provenance is traced back to that Release.
const (
	boundaryAnnotationWorkspace = "../../../internal/compiler/testdata/fullstack-app"
	boundaryAnnotationRelease   = "releases/annotation-boundary-annotation.yaml"
	boundaryAnnotationAggregate = "aggregates/annotation-release-side.yaml"
	boundaryAnnotationManifest  = "manifests/annotation-rewrite-host.yaml"
	boundaryAnnotationCatalog   = "catalog"
)

// TestTheReleaseIsNamedByOneRuleWhicheverFormTheCallerHoldsItIn proves the run
// names the Release by the path its caller supplied rendered against the
// workspace root — in the resolve result every later reading takes it from, and
// in the source the graph traces a Release-written annotation to — and that a run
// given no path names no file rather than inventing one.
//
// The rows move the form the caller holds the path in and nothing else: the same
// Release, compiled the same way, handed over as an absolute path with the root,
// as the same absolute path with no root, and not at all. The first is what the
// surfaces holding a resolved path supply, and its expectation is the
// workspace-relative name, which is what keeps the graph byte-identical across
// machines; the second shows the rendering is against the root and nothing else;
// the third that the source then carries its key path alone.
func TestTheReleaseIsNamedByOneRuleWhicheverFormTheCallerHoldsItIn(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(boundaryAnnotationWorkspace)
	if err != nil {
		t.Fatalf("absolute workspace root: %v", err)
	}
	absolute := filepath.Join(root, boundaryAnnotationRelease)

	for _, row := range []struct {
		name          string
		releasePath   string
		workspaceRoot string
		// want is the name the run must give the Release; empty means none.
		want string
	}{
		{
			name:          "an absolute path, with the workspace root",
			releasePath:   absolute,
			workspaceRoot: root,
			want:          boundaryAnnotationRelease,
		},
		{
			name:        "the same absolute path, with no workspace root",
			releasePath: absolute,
			want:        absolute,
		},
		{
			name: "no path at all",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := compileBoundaryAnnotation(t, row.releasePath, row.workspaceRoot)

			if got := out.ResolveResult.ReleasePath; got != row.want {
				t.Errorf("the resolve result names the Release %q; want %q", got, row.want)
			}

			sources := releaseAnnotationSources(out)
			if len(sources) == 0 {
				t.Fatal("the graph traces no attachment back to an annotation the Release wrote, " +
					"so nothing below can fail")
			}
			for _, source := range sources {
				switch {
				case row.want != "" && !strings.HasPrefix(source, row.want+":boundaries."):
					t.Errorf("an annotation the Release wrote is traced to %q; want it under %q",
						source, row.want+":")
				case row.want == "" && (!strings.HasPrefix(source, "boundaries.") || strings.Contains(source, ".yaml:")):
					t.Errorf("with no path supplied an annotation is traced to %q; want its key path alone, "+
						"with no file name before it", source)
				}
			}
		})
	}
}

// compileBoundaryAnnotation runs the pre-render pipeline over the committed
// boundary-annotation workspace, handing it the Release under the given label and
// root, and returns what the run produced.
func compileBoundaryAnnotation(t *testing.T, releasePath, workspaceRoot string) *stepsOneToFourOutput {
	t.Helper()
	at := func(rel string) string { return filepath.Join(boundaryAnnotationWorkspace, rel) }

	rel, err := resolver.LoadRelease(at(boundaryAnnotationRelease))
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(at(boundaryAnnotationManifest))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", at(boundaryAnnotationCatalog))
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	var captured *stepsOneToFourOutput
	runErr := runStepsOneToFour(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: bytesOf(t, at(boundaryAnnotationAggregate)),
			AggregatePath:     boundaryAnnotationAggregate,
			ReleasePath:       releasePath,
			WorkspaceRoot:     workspaceRoot,
			WorkspaceConfig:   defaultWorkspaceConfigForTest(),
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		func(out *stepsOneToFourOutput) error {
			captured = out
			return nil
		},
	)
	if runErr != nil {
		t.Fatalf("pre-render pipeline: %v", runErr)
	}
	if captured == nil || captured.ResolveResult == nil || captured.IRGraph == nil {
		t.Fatal("pipeline completed without handing back a resolve result and a graph")
	}
	return captured
}

// releaseAnnotationSources collects every source the graph traces an attachment
// leaf to that an annotation written in the Release contributed.
func releaseAnnotationSources(out *stepsOneToFourOutput) []string {
	var sources []string
	for _, entries := range out.IRGraph.Spec.FeatureValuesMapping.All() {
		for _, entry := range entries.All() {
			for _, provenance := range entry.ValuesProvenance.All() {
				if provenance.Side == "release" && provenance.AnnotationSource != "" {
					sources = append(sources, provenance.AnnotationSource)
				}
			}
			for _, provenance := range entry.InfraProvenance.All() {
				if provenance.Side == "release" && provenance.AnnotationSource != "" {
					sources = append(sources, provenance.AnnotationSource)
				}
			}
		}
	}
	return sources
}
