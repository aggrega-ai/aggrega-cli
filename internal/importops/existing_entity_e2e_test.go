// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs that an import never writes over a catalog entity already at
// a drafted path unasked, as it never writes over an Aggregate: a document whose
// top-level kind is Component, Feature, Recipe or FeatureRecipe is refused
// before anything is asked, written or reported, under a dry run too, and under
// a sync one the record does not list is refused as an Aggregate is.
//
// Each case imports through the real Import with the draft-set importer, which
// drafts exactly the files its source spells out.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// entityDraftPath is where the cases draft an entity, beneath the output
// directory.
const entityDraftPath = "catalog/components/drafted.yaml"

// entityDocument is a document of the given kind carrying only what a kind
// reader needs: the host reads nothing else of it.
func entityDocument(kind entitykind.Kind, note string) string {
	return "kind: " + string(kind) + "\nmetadata:\n  name: example/drafted\n  version: \"1.0.0\"\n# " + note + "\n"
}

// entityWorkspace is a workspace whose draft-set source drafts one document of
// the given kind at entityDraftPath.
func entityWorkspace(t *testing.T, kind entitykind.Kind) (rw *workspaceconfig.ResolvedWorkspace, source, outputDir string) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir)
	importopstest.LayOwingCatalog(t, wsDir, filepath.Join(wsDir, "catalog"))
	source = importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"),
		importopstest.DraftSetBody(abi.ScaffoldFile{Path: entityDraftPath, Content: entityDocument(kind, "drafted")}))
	rw = resolveWorkspaceAt(t, wsDir)
	return rw, source, filepath.Join(wsDir, "drafts")
}

// editEntity replaces the drafted file with an author's edit of it, of the given
// kind, and returns the edit's bytes.
func editEntity(t *testing.T, outputDir string, kind entitykind.Kind) []byte {
	t.Helper()
	place := filepath.Join(outputDir, filepath.FromSlash(entityDraftPath))
	edited := []byte(entityDocument(kind, "edited by an author"))
	if err := os.WriteFile(place, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	return edited
}

// assertUntouched fails unless the drafted place still holds want.
func assertUntouched(t *testing.T, outputDir string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(entityDraftPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the file at the drafted path changed:\n%s\nwant:\n%s", got, want)
	}
}

// assertExistingEntity fails unless err is an ExistingEntityError for the
// drafted path and kind.
func assertExistingEntity(t *testing.T, err error, kind entitykind.Kind) {
	t.Helper()
	var e *ExistingEntityError
	if !errors.As(err, &e) {
		t.Fatalf("the import returned %v; want an ExistingEntityError", err)
	}
	if e.Path != entityDraftPath || e.Kind != kind {
		t.Errorf("the refusal names %q as %s; want %q as %s", e.Path, e.Kind, entityDraftPath, kind)
	}
	for _, want := range []string{string(kind), entityDraftPath, "--sync", "--output-dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err.Error(), want)
		}
	}
}

func TestExistingEntityRefusesEditedComponent(t *testing.T) {
	t.Parallel()
	rw, source, outputDir := entityWorkspace(t, entitykind.Component)
	owingImport(t, rw, source, outputDir, Options{})
	edited := editEntity(t, outputDir, entitykind.Component)

	_, err := owingImportErr(rw, source, outputDir, Options{})
	assertExistingEntity(t, err, entitykind.Component)
	assertUntouched(t, outputDir, edited)
}

func TestExistingEntityRefusesEditedRecipe(t *testing.T) {
	t.Parallel()
	// The draft is a Component; what is on disk is a Recipe an author put there:
	// the refusal turns on the file on disk, and names its kind.
	rw, source, outputDir := entityWorkspace(t, entitykind.Component)
	owingImport(t, rw, source, outputDir, Options{})
	edited := editEntity(t, outputDir, entitykind.Recipe)

	_, err := owingImportErr(rw, source, outputDir, Options{})
	assertExistingEntity(t, err, entitykind.Recipe)
	assertUntouched(t, outputDir, edited)
}

func TestExistingEntityRefusesEveryCatalogKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []entitykind.Kind{entitykind.Component, entitykind.Feature, entitykind.Recipe,
		entitykind.FeatureRecipe} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			rw, source, outputDir := entityWorkspace(t, entitykind.Component)
			owingImport(t, rw, source, outputDir, Options{})
			edited := editEntity(t, outputDir, kind)

			_, err := owingImportErr(rw, source, outputDir, Options{})
			assertExistingEntity(t, err, kind)
			assertUntouched(t, outputDir, edited)
		})
	}
}

func TestExistingEntityWritesOverAnUnguardedKind(t *testing.T) {
	t.Parallel()
	rw, source, outputDir := entityWorkspace(t, entitykind.Component)
	owingImport(t, rw, source, outputDir, Options{})
	editEntity(t, outputDir, entitykind.Manifest)

	if _, err := owingImportErr(rw, source, outputDir, Options{}); err != nil {
		t.Fatalf("a Manifest at a drafted path is written over as before, but the import returned %v", err)
	}
	assertUntouched(t, outputDir, []byte(entityDocument(entitykind.Component, "drafted")))
}

func TestExistingEntityUnderDryRun(t *testing.T) {
	t.Parallel()
	rw, source, outputDir := entityWorkspace(t, entitykind.Component)
	owingImport(t, rw, source, outputDir, Options{})
	edited := editEntity(t, outputDir, entitykind.Component)

	_, err := owingImportErr(rw, source, outputDir, Options{Delivery: authoredcontent.DeliveryReport})
	assertExistingEntity(t, err, entitykind.Component)
	assertUntouched(t, outputDir, edited)
}

func TestSyncRefusesUnlistedCatalogEntity(t *testing.T) {
	t.Parallel()
	rw, source, outputDir := entityWorkspace(t, entitykind.Component)
	// An author's Component at the drafted path, and no record beside it.
	place := filepath.Join(outputDir, filepath.FromSlash(entityDraftPath))
	if err := os.MkdirAll(filepath.Dir(place), 0o755); err != nil {
		t.Fatal(err)
	}
	edited := []byte(entityDocument(entitykind.Component, "written by an author"))
	if err := os.WriteFile(place, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := owingImportErr(rw, source, outputDir, Options{Sync: true})
	var e *ImportRecordError
	if !errors.As(err, &e) {
		t.Fatalf("the sync returned %v; want an ImportRecordError", err)
	}
	if e.Path != entityDraftPath || e.Kind != entitykind.Component || e.Err != nil {
		t.Errorf("the refusal names %q as %s (err %v); want %q as Component", e.Path, e.Kind, e.Err, entityDraftPath)
	}
	if !strings.Contains(err.Error(), "Component") || !strings.Contains(err.Error(), entityDraftPath) {
		t.Errorf("the refusal %q does not name the kind and the path", err.Error())
	}
	assertUntouched(t, outputDir, edited)
}

func TestReimportOverDraftedEntityAsksForSync(t *testing.T) {
	t.Parallel()
	rw, source, outputDir := entityWorkspace(t, entitykind.Component)
	first := owingImport(t, rw, source, outputDir, Options{})
	if len(first.Written) == 0 {
		t.Fatal("the first import wrote nothing")
	}
	drafted := []byte(entityDocument(entitykind.Component, "drafted"))

	_, err := owingImportErr(rw, source, outputDir, Options{})
	assertExistingEntity(t, err, entitykind.Component)
	assertUntouched(t, outputDir, drafted)

	again := owingImport(t, rw, source, outputDir, Options{Sync: true})
	if len(again.Drift) != 0 || len(again.Written) != 0 || len(again.Removed) != 0 {
		t.Errorf("a sync over unchanged drafts drifts %+v, writes %v, removes %v; want none", again.Drift,
			again.Written, again.Removed)
	}
	assertUntouched(t, outputDir, drafted)
}
