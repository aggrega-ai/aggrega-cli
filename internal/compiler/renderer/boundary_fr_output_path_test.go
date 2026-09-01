// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The output path a boundary-scope FeatureRecipe's files land at, proved through
// the shipped provider guests rather than through a stand-in assembler, because
// which directory a contributor's files sit under is the assembler's own answer
// and a stand-in would be free to answer differently from the guest that ships.
//
// A provider whose assembler places each contributor in its own sub-directory is
// the case worth proving: the directory name is a path segment a reader opens,
// and the per-node key the render groups a boundary-scope attachment under is a
// marker the compiler built for its own dispatch. That marker opens with an
// angle bracket and joins with a colon — characters a path refuses on some
// platforms and a URL has to escape — so a reader must never meet it, and the
// name they meet instead is the one they wrote in the boundary's features block.
//
// A provider that places its contributors flat is here as the control: its files
// never sat under a per-contributor directory at all, so a change that moved
// them has reached past the nesting position it was aimed at.

// boundaryFRChart writes a minimal chart whose one template emits a ConfigMap,
// for a provider that consumes a sourced chart rather than an artifacts map.
func boundaryFRChart(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: "+name+"\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("image: "+name+":1.0\n"), 0o644); err != nil {
		t.Fatalf("write values.yaml: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\ndata:\n  marker: \"x\"\n"), 0o644); err != nil {
		t.Fatalf("write configmap.yaml: %v", err)
	}
	return dir
}

// oneInlineArtifact builds a single-entry inline artifacts map.
func oneInlineArtifact(filename, body string) types.OrderedMap[types.ArtifactEntry] {
	m := types.OrderedMap[types.ArtifactEntry]{}
	m.Set(filename, types.ArtifactEntry{Inline: body})
	return m
}

// boundaryFROutputPathCase is one provider's render of a boundary-scope
// attachment beside a component in the same boundary and under the same
// provider, so the two share a dispatch group and the group's assembler (when it
// has one) decides where each contributor's files sit.
//
// A group holding only the attachment elects no assembler, and its files would
// then be placed flat whatever the provider claims — so the component beside it
// is what makes the nesting position reachable at all, not scenery.
type boundaryFROutputPathCase struct {
	name     string
	provider string
	// sourced selects a chart source over an artifacts map for both the
	// component's Recipe and the attachment's FeatureRecipe, because a provider
	// declares which input kinds it accepts and no one shape suits all of them.
	sourced bool
	// wantPaths is the case's whole emitted path set in path order, written out
	// rather than composed from the code under test. Pinning all of it rather than
	// the attachment's share alone is what holds the component's own position and
	// the group's assembled output to account in the same comparison.
	wantPaths []string
	// wantAttachmentDir is the directory the attachment's files sit under, or ""
	// for a provider that places its contributors flat. It is a second, redundant
	// statement of what wantPaths already implies, and deliberately so: it is the
	// one the assertion reports on, so a failure names the segment rather than a
	// path list a reader has to diff by eye.
	wantAttachmentDir string
}

func boundaryFROutputPathCases() []boundaryFROutputPathCase {
	return []boundaryFROutputPathCase{
		{
			name:     "terraform nests each contributor under its own module directory",
			provider: "terraform",
			wantPaths: []string{
				"dist/cluster/terraform/aggrega.modules.tf",
				"dist/cluster/terraform/modules/api/aggrega.provenance.tf",
				"dist/cluster/terraform/modules/api/api.tf.json",
				"dist/cluster/terraform/modules/aux/features/aux/aggrega.provenance.tf",
				"dist/cluster/terraform/modules/aux/features/aux/aux.tf.json",
			},
			wantAttachmentDir: "dist/cluster/terraform/modules/aux/",
		},
		{
			name:     "helm-chart nests each contributor under its own chart directory",
			provider: "helm-chart",
			sourced:  true,
			wantPaths: []string{
				"dist/cluster/helm-chart/Chart.yaml",
				"dist/cluster/helm-chart/charts/api/Chart.yaml",
				"dist/cluster/helm-chart/charts/api/templates/configmap.yaml",
				"dist/cluster/helm-chart/charts/api/values.yaml",
				"dist/cluster/helm-chart/charts/aux/features/aux/Chart.yaml",
				"dist/cluster/helm-chart/charts/aux/features/aux/templates/configmap.yaml",
				"dist/cluster/helm-chart/charts/aux/features/aux/values.yaml",
				"dist/cluster/helm-chart/values.yaml",
			},
			wantAttachmentDir: "dist/cluster/helm-chart/charts/aux/",
		},
		{
			// The control. This provider places contributors flat, so the
			// attachment's files sit at the boundary directory's own features
			// position and no per-contributor directory exists to name.
			name:     "helm places contributors flat, so no per-contributor directory is named",
			provider: "helm",
			wantPaths: []string{
				"dist/cluster/helm/api-config.yaml",
				"dist/cluster/helm/features/aux/aux-config.yaml",
			},
			wantAttachmentDir: "",
		},
	}
}

// renderBoundaryFROutputPathCase drives RenderAll over one case and returns the
// emitted file map.
func renderBoundaryFROutputPathCase(t *testing.T, c boundaryFROutputPathCase) map[string]renderer.AssembledFile {
	t.Helper()

	const (
		boundary   = "cluster"
		component  = "api"
		frInstance = "aux"
		featType   = "core/aux:1.0.0"
	)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(component, types.IRNode{Component: "core/api", BoundaryPath: boundary})

	boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
	boundaryFeatures.Set(frInstance, types.BoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   boundaryFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set(component, types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(boundary, clusterBoundary)

	clusterBFProj := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBFProj.Set(frInstance, types.IRBoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set(boundary, clusterBFProj)

	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set(frInstance, types.IRFeatureEntry{Type: featType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(component, apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "boundary-fr-path-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{component: []string{frInstance}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "boundary-fr-path-app"}},
			Release:   &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				component: {BoundaryPath: boundary, Directory: "dist/cluster"},
			},
		},
	}

	componentRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: c.provider, Implements: "core/api"},
	}
	frRecipe := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     c.provider,
			Implements:     featType,
			ExecutionScope: "boundary",
			ValuesMapping:  types.ValuesMapping{Inline: `{}`},
		},
	}
	switch {
	case c.sourced:
		componentRecipe.Spec.Source = boundaryFRChart(t, "api")
		frRecipe.Spec.Source = boundaryFRChart(t, "aux")
	case c.provider == "helm":
		componentRecipe.Spec.Artifacts = oneInlineArtifact("api-config.yaml",
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: api\ndata:\n  marker: \"x\"\n")
		frRecipe.Spec.Artifacts = oneInlineArtifact("aux-config.yaml",
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: aux\ndata:\n  marker: \"x\"\n")
	default:
		componentRecipe.Spec.Artifacts = oneInlineArtifact("api.tf.json", "{\"output\":{}}\n")
		frRecipe.Spec.Artifacts = oneInlineArtifact("aux.tf.json", "{\"output\":{}}\n")
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/api" {
				return componentRecipe, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if strings.HasPrefix(featureType, "core/aux") {
				return frRecipe, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("case %q: RenderAll: %v", c.name, err)
	}
	return result.Files
}

// sortedEmittedPaths is the emitted file map's key set in path order.
func sortedEmittedPaths(files map[string]renderer.AssembledFile) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestBoundaryScopeFROutputPathCorpusReachesBothNestingProviders is the corpus's
// own anti-needle. The proofs below are worthless over a corpus of flat
// providers, because a flat provider never places a contributor under a
// per-contributor directory and so could never have carried the marker. It also
// runs outside the parallel subtests below, which is deliberate: a count
// accumulated inside them would be read before they finished and would report
// zero however good the corpus was.
func TestBoundaryScopeFROutputPathCorpusReachesBothNestingProviders(t *testing.T) {
	t.Parallel()

	nesting := map[string]bool{}
	flat := 0
	for _, c := range boundaryFROutputPathCases() {
		if c.wantAttachmentDir == "" {
			flat++
			continue
		}
		nesting[c.provider] = true
		// A pinned directory has to actually be the parent of the pinned
		// attachment paths, or the redundant statement is not a cross-check.
		covered := 0
		for _, p := range c.wantPaths {
			if strings.HasPrefix(p, c.wantAttachmentDir) {
				covered++
			}
		}
		if covered == 0 {
			t.Errorf("case %q pins the attachment directory %q, which no pinned path sits under",
				c.name, c.wantAttachmentDir)
		}
	}
	for _, want := range []string{"terraform", "helm-chart"} {
		if !nesting[want] {
			t.Errorf("the corpus reaches no nesting case for %q; the marker only ever appeared at a "+
				"nesting position, so a corpus without it would pass with the defect intact", want)
		}
	}
	if flat == 0 {
		t.Error("the corpus carries no flat provider, so nothing holds the untouched position to account")
	}
}

// TestBoundaryScopeFROutputPathNamesTheAttachmentInstance is the emitted-path
// proof. Each case pins its whole path set, so the assertion fails on a
// directory named anything other than the instance the boundary's features block
// declares — including the per-node dispatch key, which is what the nesting
// position carried before.
func TestBoundaryScopeFROutputPathNamesTheAttachmentInstance(t *testing.T) {
	t.Parallel()

	for _, c := range boundaryFROutputPathCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := sortedEmittedPaths(renderBoundaryFROutputPathCase(t, c))
			if !slices.Equal(got, c.wantPaths) {
				t.Fatalf("emitted paths\n got: %v\nwant: %v", got, c.wantPaths)
			}
			if c.wantAttachmentDir == "" {
				return
			}
			// The attachment's files sit under a directory named for the instance,
			// reported on its own so a failure names the segment.
			for _, p := range got {
				if !strings.Contains(p, "/features/aux/") {
					continue
				}
				if !strings.HasPrefix(p, c.wantAttachmentDir) {
					t.Errorf("attachment output %q does not sit under %q", p, c.wantAttachmentDir)
				}
			}
		})
	}
}

// TestBoundaryScopeFROutputPathCarriesNoDispatchMarker is the character-level
// assertion, stated apart from the pinned paths because it is what makes a
// pinned path re-derivable rather than merely agreed: no emitted path may carry
// a character the dispatch marker opens or joins with, whatever the pinned set
// happens to say.
//
// The four characters are the ones a path refuses on some platforms and a URL
// has to escape, and the rendered tree lands in cross-platform repositories that
// are cloned on all of them.
func TestBoundaryScopeFROutputPathCarriesNoDispatchMarker(t *testing.T) {
	t.Parallel()

	for _, c := range boundaryFROutputPathCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := renderBoundaryFROutputPathCase(t, c)
			if len(files) == 0 {
				t.Fatal("the render emitted nothing, so an absence claim over it is vacuous")
			}
			for p := range files {
				for _, bad := range []string{"<", ">", ":", "@"} {
					if strings.Contains(p, bad) {
						t.Errorf("emitted path %q carries %q, which a path refuses on some platforms and a "+
							"URL must escape", p, bad)
					}
				}
			}
		})
	}
}
