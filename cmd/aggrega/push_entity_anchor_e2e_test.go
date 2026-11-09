// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	orasoci "oras.land/oras-go/v2/content/oci"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// anchorRecipeBody is a complete, schema-shaped Recipe whose identity is
// supplied by the caller, so two fixtures differ in the one field a consumer
// asks for an entity by.
func anchorRecipeBody(name string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: ` + name + `
  version: 1.0.0
spec:
  capability: helm
  implements: core/` + name + `
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
`
}

// TestPublishedRecipeYieldsItsEntityWhateverTheFileIsNamed drives the anchor
// through the production publish path and back out again, for an entity file
// named something other than the convention.
//
// It is written as a publish-and-pull rather than as a unit assertion on the
// unbundler because the two halves are authored in different packages and
// nothing joined them: the bundler imposes no name on the entity file, the
// consume side used to require one, and only a round trip through both shows
// what a consumer of an already-published artifact actually receives. The
// destination is a file-backed OCI store in the test's own temp directory — a
// real on-disk OCI layout where pack, tag, resolve and fetch all run — created
// and discarded with the test, so the proof mutates nothing else reads.
func TestPublishedRecipeYieldsItsEntityWhateverTheFileIsNamed(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	// Named for the entity rather than for its kind, which is what a Recipe
	// author routinely does and what the old anchor could not read back.
	recipeDir := filepath.Join(root, "recipes", "mariadb", "1.0.0")
	// The Recipe declares its chart, which is what makes it publish as an archive:
	// a file beside it that it did not declare would not travel at all.
	body := `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: mariadb
  version: 1.0.0
spec:
  capability: helm
  implements: core/mariadb
  source: ./source
  valuesMapping: |
    {}
`
	writeEntityFixture(t, filepath.Join(recipeDir, "mariadb.yaml"), body)
	writeEntityFixture(t, filepath.Join(recipeDir, "source", "Chart.yaml"), "name: mariadb\nversion: 25.0.8\n")

	store, err := orasoci.New(t.TempDir())
	if err != nil {
		t.Fatalf("orasoci.New: %v", err)
	}
	// The Recipe reads a local source, so the publication pins it first rather than
	// refusing a source whose files no lock beside it records.
	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: filepath.Join(recipeDir, "mariadb.yaml"),
		Registry:   "example.com",
		PushTarget: store,
		PinSources: boolPtr(true),
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	pulled, err := oci.Pull(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}
	if !oci.IsTarGzBundle(pulled.Bytes) {
		t.Fatalf("a Recipe must publish as an archive, or the anchor below is never reached")
	}
	entity, err := oci.EntityYAML(pulled.Bytes)
	if err != nil {
		t.Fatalf("a published Recipe must yield its entity however its file was named: %v", err)
	}
	if string(entity) != body {
		t.Errorf("the entity recovered from the published artifact is not the authored document.\n got:\n%s\nwant:\n%s", entity, body)
	}
}

// TestTwoSiblingRecipesAreRefusedAtPublicationAndTheirArtifactAtTheConsumer is
// the end-to-end form of the substitution this anchor exists to stop, from both
// ends.
//
// Two same-version Recipes authored side by side in one version directory are
// refused by this product's publication, each naming the other, and nothing
// reaches either store. The read side must still be correct for an artifact
// built from such a directory, because an artifact a client that is not this
// product published — one that packs a whole directory — carries exactly that
// shape. So the two archives are built as such a client builds them, every file
// of the directory carried, and the assertion that matters is what a consumer
// gets for the SECOND coordinate: not the first entity under the second's name,
// but a refusal naming both documents.
//
// The byte-identity check runs first and fatals rather than errors: if the two
// artifacts ever stopped being identical there would be no substitution left to
// refuse, and the refusal below would be proving something else.
func TestTwoSiblingRecipesAreRefusedAtPublicationAndTheirArtifactAtTheConsumer(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	versionDir := filepath.Join(root, "recipes", "shared", "1.0.0")
	frontendPath := filepath.Join(versionDir, "frontend.yaml")
	databasePath := filepath.Join(versionDir, "database.yaml")
	writeEntityFixture(t, frontendPath, anchorRecipeBody("frontend"))
	writeEntityFixture(t, databasePath, anchorRecipeBody("database"))

	workspace := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	for path, other := range map[string]string{frontendPath: "database.yaml", databasePath: "frontend.yaml"} {
		store, err := orasoci.New(t.TempDir())
		if err != nil {
			t.Fatalf("orasoci.New: %v", err)
		}
		err = pushRun(t.Context(), pushConfig{
			Workspace:  workspace,
			EntityPath: path,
			Registry:   "example.com",
			PushTarget: store,
		})
		if !errors.Is(err, entitypin.ErrEntitySharesItsDirectory()) {
			t.Fatalf("publishing %s beside another Recipe must be refused under the shared-directory condition; got %v", path, err)
		}
		if !strings.Contains(err.Error(), other) {
			t.Errorf("the refusal must name the other entity in the way, %s; got %v", other, err)
		}
		if _, perr := oci.Pull(context.Background(), store, "1.0.0"); perr == nil {
			t.Errorf("a refused publication of %s landed bytes in its store", path)
		}
	}

	bundle := func(entityPath string) []byte {
		t.Helper()
		data, _, err := oci.Bundle("Recipe", entityPath, []string{"database.yaml", "frontend.yaml"})
		if err != nil {
			t.Fatalf("oci.Bundle %s: %v", entityPath, err)
		}
		return data
	}
	frontendArtifact := bundle(frontendPath)
	databaseArtifact := bundle(databasePath)
	if !bytes.Equal(frontendArtifact, databaseArtifact) {
		t.Fatalf("two sibling entities in one directory no longer publish byte-identical artifacts "+
			"(%d vs %d bytes); there is no substitution left for the refusal below to be about",
			len(frontendArtifact), len(databaseArtifact))
	}
	t.Logf("two published coordinates, one artifact: %d bytes, byte-identical", len(frontendArtifact))

	_, err := oci.EntityYAML(databaseArtifact)
	if !errors.Is(err, oci.ErrBundleAmbiguousEntityDocument()) {
		t.Fatalf("pulling the second coordinate must be refused, not answered with the first entity; got %v", err)
	}
	for _, want := range []string{"frontend.yaml", "database.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q so an author sees which documents collided; got %q", want, err)
		}
	}
	t.Logf("what a consumer of the second coordinate is told: %v", err)
}
