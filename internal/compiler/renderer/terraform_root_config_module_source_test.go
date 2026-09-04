// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// What the root configuration a boundary's Terraform output carries actually
// SAYS, proved through the shipped guest rather than through a stand-in
// assembler, because the source a module block names is the guest's own answer.
//
// The emitted path set is pinned elsewhere and is not what this file is about. A
// path set cannot see the defect these proofs exist for: this target loads only
// the files sitting directly in a directory, and treats a nested directory as a
// separate module that has to be called explicitly. So a contributor's files can
// sit at a path the output genuinely emits, in a directory the configuration
// genuinely mentions nowhere, and every path assertion still passes while the
// target reads none of that contributor's content — reporting the configuration
// valid, because a module directory holding no top-level configuration file is
// an empty module rather than an error.
//
// The discriminating proof is therefore the CONVERSE of the obvious one. That
// every module block's source resolves to a directory the output emits is worth
// asserting and is the weaker half: it fails only when a block names a directory
// that is not there. That every emitted directory holding top-level
// configuration files is NAMED by a module block is the half that fails on the
// defect, because it is the one an unreferenced contributor breaks.

// terraformModuleBlock is one `module` block read back out of an emitted root
// configuration: the name it was declared under and the source it points at.
type terraformModuleBlock struct {
	name   string
	source string
}

// terraformModuleBlockPattern reads a module block's name and its source out of
// an emitted root configuration.
//
// The emitted text is read back rather than the guest's inputs being inspected,
// because what this file is about is what the target would load — and the target
// reads the text. A parser for the whole language would be a second
// implementation of one; two captures over the two lines the guest writes
// together are enough to compare a block against the directories the same render
// emitted.
var terraformModuleBlockPattern = regexp.MustCompile(`(?m)^module "([^"]+)" \{\n  source = "([^"]+)"$`)

// terraformModuleBlocksIn reads every module block out of one emitted root
// configuration, in the order they are written.
func terraformModuleBlocksIn(body string) []terraformModuleBlock {
	matches := terraformModuleBlockPattern.FindAllStringSubmatch(body, -1)
	out := make([]terraformModuleBlock, 0, len(matches))
	for _, m := range matches {
		out = append(out, terraformModuleBlock{name: m[1], source: m[2]})
	}
	return out
}

// terraformConfigDirs is the set of emitted directories that hold at least one
// file this target would load as configuration, each relative to the root
// configuration's own directory.
//
// Only the files sitting DIRECTLY in a directory count toward that directory,
// which is the whole point: a file one level down belongs to its own directory
// and to no ancestor, exactly as the target reads it.
func terraformConfigDirs(t *testing.T, files map[string]renderer.AssembledFile, rootDir string) map[string]struct{} {
	t.Helper()
	dirs := map[string]struct{}{}
	for p := range files {
		rel, ok := strings.CutPrefix(p, rootDir+"/")
		if !ok {
			continue
		}
		cut := strings.LastIndex(rel, "/")
		if cut < 0 {
			continue
		}
		name := rel[cut+1:]
		if !strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, ".tf.json") {
			continue
		}
		dirs[rel[:cut]] = struct{}{}
	}
	return dirs
}

// sortedSetKeys is one set's members in sorted order, for a failure message a
// reader can diff.
func sortedSetKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// terraformRootConfigCase is one Terraform render carrying one attachment, at the
// scope named.
type terraformRootConfigCase struct {
	name string
	// boundaryScope selects an attachment declared on the boundary over one
	// declared on the component. Both scopes are rendered because each composes
	// its own position from its own coordinates, so neither is a working
	// reference for the other and a proof over one says nothing about the other.
	boundaryScope bool
	// wantModuleSources is every module block's source, in the order the root
	// configuration writes them, written out rather than composed from the code
	// under test.
	wantModuleSources []string
	// wantModuleNames is every module block's name, in the same order. It is a
	// separate statement because a name and a source are two things a reader
	// checks together, and a block whose name collided would be a configuration
	// this target rejects however good its sources were.
	wantModuleNames []string
}

func terraformRootConfigCases() []terraformRootConfigCase {
	// Both cases write the attachment's block BEFORE the component's, and the
	// order is pinned rather than corrected here. Contributors reach the
	// assembler in identifier order, and a contributor that is not one of the
	// graph's nodes carries a dispatch identity rather than a name — one opening
	// with a character that sorts ahead of every letter. So the order is stable
	// for identical input, which is the contract the output owes, and it is not
	// an order a reader would have chosen. Reordering is not available to this
	// file's subject: the arrival order is the host's, shared by every provider
	// that assembles, and this target's own assembler documents that it emits in
	// arrival order.
	return []terraformRootConfigCase{
		{
			name:              "an attachment declared on a component is called beside its host",
			wantModuleSources: []string{"./modules/api--aux", "./modules/api"},
			wantModuleNames:   []string{"api--aux", "api"},
		},
		{
			name:              "an attachment declared on a boundary is called beside the component",
			boundaryScope:     true,
			wantModuleSources: []string{"./modules/aux", "./modules/api"},
			wantModuleNames:   []string{"aux", "api"},
		},
	}
}

// renderTerraformRootConfigCase drives RenderAll over one case and returns the
// emitted file map.
func renderTerraformRootConfigCase(t *testing.T, c terraformRootConfigCase) map[string]renderer.AssembledFile {
	t.Helper()

	const (
		boundary   = "cluster"
		component  = "api"
		frInstance = "aux"
		featType   = "core/aux:1.0.0"
	)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(component, types.IRNode{Component: "core/api", BoundaryPath: boundary})

	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set(component, types.BoundaryComponent{})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	if c.boundaryScope {
		boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
		boundaryFeatures.Set(frInstance, types.BoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
		clusterBoundary.Features = boundaryFeatures
		clusterBFProj := types.OrderedMap[types.IRBoundaryFeature]{}
		clusterBFProj.Set(frInstance, types.IRBoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
		bfProj.Set(boundary, clusterBFProj)
	}
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(boundary, clusterBoundary)

	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set(frInstance, types.IRFeatureEntry{Type: featType})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(component, apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "terraform-root-config-app"},
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
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "terraform-root-config-app"}},
			Release:   &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				component: {BoundaryPath: boundary, Directory: "dist/cluster"},
			},
		},
	}

	// Each contributor's artifact is a document this target actually accepts,
	// carrying an output under a name of its own. Both halves matter. A document
	// the target would reject makes every engine verdict over this output
	// unattributable — a clean tree fails for the fixture's own fault, and a
	// planted-defect contrast against it proves nothing. Distinct output names
	// matter because this target merges every top-level file in one directory
	// into one namespace, so two contributors sharing an output name would
	// collide for a reason that has nothing to do with where their files sit.
	componentRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "terraform",
			Implements: "core/api",
			Artifacts:  oneInlineArtifact("api.tf.json", "{\"output\":{\"api_marker\":{\"value\":\"api\"}}}\n"),
		},
	}
	frRecipe := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:    "terraform",
			Implements:    featType,
			ValuesMapping: types.ValuesMapping{Inline: `{}`},
			Artifacts:     oneInlineArtifact("aux.tf.json", "{\"output\":{\"aux_marker\":{\"value\":\"aux\"}}}\n"),
		},
	}
	if c.boundaryScope {
		frRecipe.Spec.ExecutionScope = "boundary"
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

// TestTerraformRootConfigNamesEveryContributorsOwnDirectory is the proof the
// item's subject rests on, and it is the converse assertion: every emitted
// directory holding files this target loads as configuration is named by a
// module block in the root configuration.
//
// Its failure mode is the defect itself. An attachment whose files sit in a
// directory no block names is a separate module nobody calls: the target loads
// none of it, reports success, and every path assertion over the same output
// passes. So this is what makes the emitted output's correctness assertable
// without running the target — and it does not replace running it, because a
// structural agreement between two of the compiler's own outputs is not the
// target's verdict on either.
func TestTerraformRootConfigNamesEveryContributorsOwnDirectory(t *testing.T) {
	t.Parallel()
	const rootDir = "dist/cluster/terraform"
	for _, c := range terraformRootConfigCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := renderTerraformRootConfigCase(t, c)

			root, ok := files[rootDir+"/aggrega.modules.tf"]
			if !ok {
				t.Fatalf("case %q: no root configuration emitted; got %v", c.name, sortedEmittedPaths(files))
			}
			blocks := terraformModuleBlocksIn(string(root.Content))
			named := map[string]struct{}{}
			for _, b := range blocks {
				rel, isLocal := strings.CutPrefix(b.source, "./")
				if !isLocal {
					t.Errorf("case %q: module %q source %q does not name a directory beside this configuration; this target reads a source as a local path only when it begins with ./ or ../",
						c.name, b.name, b.source)
					continue
				}
				named[rel] = struct{}{}
			}

			configDirs := terraformConfigDirs(t, files, rootDir)

			// The half that fails on the defect. A directory holding
			// configuration files that no block names is content this target
			// never loads, and nothing else in the emitted output says so.
			for _, dir := range sortedSetKeys(configDirs) {
				if _, isNamed := named[dir]; !isNamed {
					t.Errorf("case %q: %q holds files this target loads as configuration and no module block names it, so the target reads none of them; module sources are %v",
						c.name, dir, sortedSetKeys(named))
				}
			}

			// The weaker half, kept because it is a cheap failure for a source
			// naming a directory that is not there at all. It is NEVER evidence
			// that the target reads the files: a source resolving to a directory
			// whose configuration files all sit one level further down resolves,
			// loads nothing, and reports success.
			for _, dir := range sortedSetKeys(named) {
				if _, exists := configDirs[dir]; !exists {
					t.Errorf("case %q: a module block names %q, which holds no file this target loads as configuration; emitted configuration directories are %v",
						c.name, dir, sortedSetKeys(configDirs))
				}
			}
		})
	}
}

// TestTerraformRootConfigModuleBlocksArePinned pins each case's whole module
// block set — every name and every source, in emission order — so a change that
// moves one is a diff a reader sees rather than a set comparison that still
// agrees.
//
// It is a second, deliberately redundant statement of what the converse proof
// above already implies. The converse proof would keep passing if both the
// placement and the block moved together, which is exactly the change that has
// to be read rather than merely agreed with.
func TestTerraformRootConfigModuleBlocksArePinned(t *testing.T) {
	t.Parallel()
	const rootDir = "dist/cluster/terraform"
	for _, c := range terraformRootConfigCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := renderTerraformRootConfigCase(t, c)
			root, ok := files[rootDir+"/aggrega.modules.tf"]
			if !ok {
				t.Fatalf("case %q: no root configuration emitted; got %v", c.name, sortedEmittedPaths(files))
			}
			blocks := terraformModuleBlocksIn(string(root.Content))
			gotSources := make([]string, 0, len(blocks))
			gotNames := make([]string, 0, len(blocks))
			for _, b := range blocks {
				gotSources = append(gotSources, b.source)
				gotNames = append(gotNames, b.name)
			}
			if !slicesEqualStrings(gotSources, c.wantModuleSources) {
				t.Errorf("case %q: module sources = %v, want %v\nroot configuration:\n%s",
					c.name, gotSources, c.wantModuleSources, root.Content)
			}
			if !slicesEqualStrings(gotNames, c.wantModuleNames) {
				t.Errorf("case %q: module names = %v, want %v\nroot configuration:\n%s",
					c.name, gotNames, c.wantModuleNames, root.Content)
			}
		})
	}
}

// slicesEqualStrings reports element-wise equality, which is what an ordered
// pinning compares.
func slicesEqualStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
