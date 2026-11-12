// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A file a Recipe imports inside its boundary block is pinned in the lock beside the
// Recipe as an artifacts import is, under its own name, and a build compares it
// against that pin exactly as it compares an artifacts import. Each cell is driven at
// the render entry the build runs, with the pins carried on the input as the build
// carries them, through the shipped terraform guest.

// boundaryPinDocument is the file the fixture's Recipe is declared in, the key the
// render reads its recorded pins under.
const boundaryPinDocument = "/ws/catalog/recipe/terraform/api-recipe/1.0.0/recipe.yaml"

// boundaryPinFixture is one cell's inputs: the bytes the Recipe's own artifacts entry
// and its boundary entry import, both under the key providers.tf, and the pins the
// lock records.
type boundaryPinFixture struct {
	ownBody, sharedBody string
	pins                []types.ArtifactDigest
}

// renderBoundaryPin renders one terraform component whose Recipe imports one file
// under providers.tf in its artifacts block and another under the same key in its
// boundary block, with the pins given recorded beside the Recipe's document. It
// returns the render's result or its refusal, and the path the boundary entry
// imports.
func renderBoundaryPin(t *testing.T, f boundaryPinFixture) (*renderer.RenderResult, string, error) {
	t.Helper()
	dir := t.TempDir()
	ownPath := filepath.Join(dir, "own-providers.tf")
	sharedPath := filepath.Join(dir, "shared-providers.tf")
	for p, body := range map[string]string{ownPath: f.ownBody, sharedPath: f.sharedBody} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	artifacts := oneInlineArtifact("api.tf.json", "{\"output\":{\"api_marker\":{\"value\":\"api\"}}}\n")
	artifacts.Set("providers.tf", types.ArtifactEntry{Import: ownPath})
	shared := types.OrderedMap[types.ArtifactEntry]{}
	shared.Set("providers.tf", types.ArtifactEntry{Import: sharedPath})
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "terraform",
			Implements: "core/api",
			Artifacts:  artifacts,
			Boundary:   types.RecipeSpecBoundary{Artifacts: shared},
		},
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	cluster := types.Boundary{
		Directory:  "dist/cluster",
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	cluster.Components.Set("api", types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("cluster", cluster)
	pins := recipepin.Lookup{}
	if len(f.pins) > 0 {
		pins.Record(boundaryPinDocument, recipepin.Pins{ArtifactDigests: f.pins})
	}
	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "boundary-pin-app"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: types.OrderedMap[json.RawMessage]{},
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
				Provenance:    types.IRProvenance{CompilerVersion: "test"},
			},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "boundary-pin-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "cluster", Directory: "dist/cluster"}},
		},
		RecordedPins: pins,
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			return recipe, componentType == "core/api"
		},
		RecipeDocument: func(*types.Recipe) (string, bool) { return boundaryPinDocument, true },
	}
	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	return result, sharedPath, err
}

const (
	boundaryPinOwnBody     = "# the component's own provider file\n"
	boundaryPinSharedBody  = "provider \"azurerm\" {\n  features {}\n}\n"
	boundaryPinDriftedBody = "provider \"azurerm\" {\n  features {}\n  storage_use_azuread = true\n}\n"
)

// TestABoundaryImportMatchingItsPinRenders is the admitting direction, with both
// keys pinned at once: the artifacts entry under its key and the boundary entry under
// its own pin name, each at its own file's digest. A comparison reading the boundary
// entry's pin under the bare key would meet the artifacts file's digest here and
// refuse, so this cell is also what shows the two pins are read apart.
func TestABoundaryImportMatchingItsPinRenders(t *testing.T) {
	t.Parallel()
	result, _, err := renderBoundaryPin(t, boundaryPinFixture{
		ownBody: boundaryPinOwnBody, sharedBody: boundaryPinSharedBody,
		pins: []types.ArtifactDigest{
			{Name: recipepin.BoundaryArtifactPinName("providers.tf"), Digest: digest.ContentDigest([]byte(boundaryPinSharedBody))},
			{Name: "providers.tf", Digest: digest.ContentDigest([]byte(boundaryPinOwnBody))},
		},
	})
	if err != nil {
		t.Fatalf("a boundary import still matching its pin must render: %v", err)
	}
	shared, ok := result.Files[boundaryArtifactsRootDir+"/providers.tf"]
	if !ok {
		t.Fatalf("the boundary file was not written beside the root configuration; emitted %v", sortedEmittedPaths(result.Files))
	}
	if string(shared.Content) != boundaryPinSharedBody {
		t.Errorf("the boundary file carries %q; want the imported bytes %q", shared.Content, boundaryPinSharedBody)
	}
}

// TestABoundaryImportThatNoLongerMatchesItsPinIsRefused is the raising direction: the
// boundary entry's file changed after its pin was recorded, and the build refuses
// under the condition an artifacts import's drift raises, naming the pin as the lock
// records it, the file, both digests, and positioned at the boundary block's entry.
func TestABoundaryImportThatNoLongerMatchesItsPinIsRefused(t *testing.T) {
	t.Parallel()
	recorded := digest.ContentDigest([]byte(boundaryPinSharedBody))
	_, sharedPath, err := renderBoundaryPin(t, boundaryPinFixture{
		ownBody: boundaryPinOwnBody, sharedBody: boundaryPinDriftedBody,
		pins: []types.ArtifactDigest{
			{Name: recipepin.BoundaryArtifactPinName("providers.tf"), Digest: recorded},
			{Name: "providers.tf", Digest: digest.ContentDigest([]byte(boundaryPinOwnBody))},
		},
	})
	if err == nil {
		t.Fatal("a boundary import that no longer matches its pin rendered; want a halt")
	}
	if !errors.Is(err, renderer.ErrArtifactContentMismatch()) {
		t.Fatalf("the halt does not carry the content-mismatch condition an artifacts import's drift raises: %+v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		renderer.ErrArtifactContentMismatch().Error(),
		`"boundary/providers.tf"`,
		`imported file "` + sharedPath + `"`,
		`the recorded pin is "` + recorded + `"`,
		`its content digests to "` + digest.ContentDigest([]byte(boundaryPinDriftedBody)) + `"`,
		constraint.BoundaryArtifactEntryLocator("providers.tf"),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not carry %q.\nfull halt: %s", want, msg)
		}
	}
}

// TestABoundaryImportIsNotComparedAgainstAnArtifactsPinOfTheSameKey holds the cell
// where only the artifacts entry is pinned: the lock records providers.tf at the
// artifacts file's digest and nothing under the boundary pin name. The boundary
// file's bytes differ from that digest, and the build renders, because the boundary
// entry has no pin recorded to compare against — it is an unpinned import, exactly
// as an artifacts import with no recorded pin is.
func TestABoundaryImportIsNotComparedAgainstAnArtifactsPinOfTheSameKey(t *testing.T) {
	t.Parallel()
	if _, _, err := renderBoundaryPin(t, boundaryPinFixture{
		ownBody: boundaryPinOwnBody, sharedBody: boundaryPinSharedBody,
		pins: []types.ArtifactDigest{{Name: "providers.tf", Digest: digest.ContentDigest([]byte(boundaryPinOwnBody))}},
	}); err != nil {
		t.Fatalf("a boundary import was compared against the artifacts pin of the same key: %v", err)
	}
}
