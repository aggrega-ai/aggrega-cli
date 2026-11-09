// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage of the content pin a file imported in a Recipe's boundary
// block carries. A pull asked to pin records it in the entity.lock beside the
// Recipe, in the one artifactDigests list, under a name of its own; a build, a
// verifying pull and a pull that does not pin each meet a change to that file
// exactly as they meet a change to a file an artifacts entry imports. Every plant is
// driven beside its control — the artifacts file of the same key edited the same
// way — so the two blocks are held to one condition rather than each to a
// condition named here.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// The two files the fixture's Recipe imports under one key, providers.tf: the
// component's own, through its artifacts block, and the boundary's, through its
// boundary block — each with the edit a plant makes to it.
const (
	boundaryPinOwnBody      = "# the component's own provider file\n"
	boundaryPinOwnEdited    = "# the component's own provider file, edited\n"
	boundaryPinSharedBody   = "provider \"azurerm\" {\n  features {}\n}\n"
	boundaryPinSharedEdited = "provider \"azurerm\" {\n  features {}\n  storage_use_azuread = true\n}\n"
)

// boundaryPinWorkspace authors a terraform workspace whose one Recipe imports
// own-providers.tf under providers.tf in its artifacts block and
// shared-providers.tf under the same key in its boundary block, maps the
// component's type to the Recipe by mapping, and runs a pull asked to pin. It
// returns the Release path, the catalog directory and the Recipe's document.
func boundaryPinWorkspace(t *testing.T, root, mapping string) (releasePath, catalogDir, recipePath string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	recipeDir := filepath.Join(catalogDir, "recipes", "gateway")
	recipePath = filepath.Join(recipeDir, "recipe.yaml")
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n"+
		"metadata:\n  name: aggregate\n  version: \"1.0.0\"\nspec:\n  components:\n    gateway:\n      type: gateway\n")
	writeFixture(t, filepath.Join(root, "release.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Release\n"+
		"metadata:\n  name: boundary-pin\n  environment: testing\n  version: \"1.0.0\"\nspec:\n"+
		"  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n"+
		"  boundaries:\n    main:\n      directory: ./dist/main\n      components:\n        gateway: {}\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\n"+
		"metadata:\n  name: manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n"+
		"        gateway: "+mapping+"\n")
	writeFixture(t, filepath.Join(catalogDir, "components", "gateway.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Component\nmetadata:\n  name: gateway\n  version: \"1.0.0\"\n  labels:\n"+
		"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
	writeFixture(t, recipePath, "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: gateway\n"+
		"  version: \"1.0.0\"\nspec:\n  capability: terraform\n  implements: gateway\n"+
		"  artifacts:\n    gateway.tf.json: |\n      {\"output\":{\"gateway\":{\"value\":\"gateway\"}}}\n"+
		"    providers.tf:\n      $import: ./own-providers.tf\n"+
		"  boundary:\n    artifacts:\n      providers.tf:\n        $import: ./shared-providers.tf\n")
	writeFixture(t, filepath.Join(recipeDir, "own-providers.tf"), boundaryPinOwnBody)
	writeFixture(t, filepath.Join(recipeDir, "shared-providers.tf"), boundaryPinSharedBody)
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/terraform")

	releasePath = filepath.Join(root, "release.yaml")
	pin := true
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		PinSources:  &pin,
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("a pull asked to pin: %v", err)
	}
	return releasePath, catalogDir, recipePath
}

// boundaryPinPlant is one file a plant edits: the pin name the lock records it
// under, the file, and its bytes before and after.
type boundaryPinPlant struct {
	name, pinName, file, body, edited string
}

// boundaryPinPlants are the control — the artifacts file — and the boundary file,
// edited in turn, in the Recipe's directory.
func boundaryPinPlants(recipePath string) []boundaryPinPlant {
	dir := filepath.Dir(recipePath)
	return []boundaryPinPlant{
		{"control: the artifacts file", "providers.tf", filepath.Join(dir, "own-providers.tf"), boundaryPinOwnBody, boundaryPinOwnEdited},
		{"the boundary file", "boundary/providers.tf", filepath.Join(dir, "shared-providers.tf"), boundaryPinSharedBody, boundaryPinSharedEdited},
	}
}

// editFile writes body to path, failing the test where it cannot.
func editFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPull_ABoundaryImportIsPinnedApartFromAnArtifactsImportOfTheSameKey reads the
// lock a pull asked to pin wrote: two rows, the artifacts entry's under its key and
// the boundary entry's under boundary/ and the key, each carrying the digest of its
// own block's file. The verify and a build pass over it, and the build writes the
// boundary file beside the root configuration and the artifacts file in the module.
func TestPull_ABoundaryImportIsPinnedApartFromAnArtifactsImportOfTheSameKey(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir, recipePath := boundaryPinWorkspace(t, root, "gateway")

	lock := readLockForTest(t, recipePath)
	if len(lock.ArtifactDigests) != 2 {
		t.Fatalf("want one pin per imported file, two in all; the lock records %+v", lock.ArtifactDigests)
	}
	for _, want := range []struct{ name, body string }{
		{"providers.tf", boundaryPinOwnBody},
		{"boundary/providers.tf", boundaryPinSharedBody},
	} {
		pin, ok := recipepin.MatchArtifactPin(lock.ArtifactDigests, want.name)
		if !ok {
			t.Errorf("the lock records no pin under %q: %+v", want.name, lock.ArtifactDigests)
			continue
		}
		if pin.Digest != digest.ContentDigest([]byte(want.body)) {
			t.Errorf("the pin under %q carries %s, not the digest of its own block's file", want.name, pin.Digest)
		}
	}

	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must pass over the lock the pull just wrote: %v", err)
	}
	tree := builtTree(t, root, releasePath, catalogDir)
	rootDir := ""
	for p := range tree {
		if strings.HasSuffix(p, "/aggrega.modules.tf") {
			rootDir = strings.TrimSuffix(p, "aggrega.modules.tf")
		}
	}
	if rootDir == "" {
		t.Fatalf("no root configuration in %v", sortedTreeKeys(tree))
	}
	if got := tree[rootDir+"providers.tf"]; got != boundaryPinSharedBody {
		t.Errorf("the root's providers.tf carries %q; want the boundary file's bytes", got)
	}
	if got := tree[rootDir+"modules/gateway/providers.tf"]; got != boundaryPinOwnBody {
		t.Errorf("the module's providers.tf carries %q; want the artifacts file's bytes", got)
	}
}

// TestBuild_ABoundaryImportChangedSinceItWasPinnedIsRefusedAsAnArtifactsImportIs
// edits each file in turn where the Manifest names the Recipe by the reference the
// Component also bears. The Recipe has a lockfile row of its own all the same — an
// entity is identified by its kind and its reference together — so the edit moves
// that row, and a build and a verifying pull halt on it exactly as they do where the
// Manifest names the Recipe by its full URI (the test below); a pull that does not
// pin names the file as a stale pin. The boundary file meets exactly what the
// artifacts file meets, and a restored file builds again.
func TestBuild_ABoundaryImportChangedSinceItWasPinnedIsRefusedAsAnArtifactsImportIs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir, recipePath := boundaryPinWorkspace(t, root, "gateway")
	lockPath := recipepin.LockPath(recipePath)
	lockfilePath := lockfile.PathFor(releasePath)
	if err := buildOver(t, root, releasePath, catalogDir); err != nil {
		t.Fatalf("a build must pass over the lock the pull just wrote, or the refusals below prove nothing: %v", err)
	}
	recorded := entityRow(t, releasePath, "gateway")

	for _, plant := range boundaryPinPlants(recipePath) {
		t.Run(plant.name, func(t *testing.T) {
			lockBefore, lockfileBefore := mustReadFile(t, lockPath), mustReadFile(t, lockfilePath)
			editFile(t, plant.file, plant.edited)
			assertRowRefusal(t, verifyPull(t, root, releasePath, catalogDir, nil), buildOver(t, root, releasePath, catalogDir),
				"gateway", recorded, rowDigestAt(t, recipePath), renderer.ErrArtifactContentMismatch())
			assertHoldingPullNames(t, pullHolding(t, root, releasePath, catalogDir, nil), lockPath, lockfilePath,
				lockBefore, lockfileBefore, "imported file "+plant.pinName+":")

			editFile(t, plant.file, plant.body)
			if err := buildOver(t, root, releasePath, catalogDir); err != nil {
				t.Fatalf("a build must pass again once the file is restored: %v", err)
			}
		})
	}
}

// TestBuild_ABoundaryImportChangedSinceThePullMovesTheRecipesLockfileRow holds the
// other cell: the Manifest names the Recipe by its full URI, so the lockfile records
// a row of the Recipe's own covering every file it imports. Editing either file
// moves that row, and a build and a verifying pull halt on the lockfile before any
// single file is compared — the boundary file exactly as the artifacts file.
func TestBuild_ABoundaryImportChangedSinceThePullMovesTheRecipesLockfileRow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const recipeURI = "terraform/gateway:1.0.0"
	releasePath, catalogDir, recipePath := boundaryPinWorkspace(t, root, recipeURI)
	recorded := entityRow(t, releasePath, recipeURI)
	if got := rowDigestAt(t, recipePath); got != recorded {
		t.Fatalf("the lockfile records %s for %s, not the row %s the Recipe digests to", recorded, recipeURI, got)
	}
	if err := buildOver(t, root, releasePath, catalogDir); err != nil {
		t.Fatalf("a build must pass over the lock the pull just wrote, or the refusals below prove nothing: %v", err)
	}

	for _, plant := range boundaryPinPlants(recipePath) {
		t.Run(plant.name, func(t *testing.T) {
			editFile(t, plant.file, plant.edited)
			assertRowRefusal(t, verifyPull(t, root, releasePath, catalogDir, nil), buildOver(t, root, releasePath, catalogDir),
				recipeURI, recorded, rowDigestAt(t, recipePath), renderer.ErrArtifactContentMismatch())
			editFile(t, plant.file, plant.body)
			if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
				t.Fatalf("verify must pass again once the file is restored: %v", err)
			}
		})
	}
}
