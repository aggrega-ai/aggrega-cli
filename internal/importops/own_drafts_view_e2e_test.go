// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs, through the real Import, that the catalog an import composes
// its Manifest over and reads owed values against leaves out the Components and
// Recipes its own record entry lists — its earlier drafts, written into the
// catalog folder the workspace reads — and keeps those another format's entry
// lists, which are catalog entities to it.
//
// Each case imports through the draft-set importer into the workspace's root, so
// a drafted Component or Recipe placed under catalog/ lands in the catalog folder
// the next import of the workspace reads.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// The places the cases draft the store's Component and its Recipe of arm at,
// beneath the workspace's catalog folder.
const (
	ownComponentPath = "catalog/imported/x.yaml"
	ownRecipePath    = "catalog/imported/arm/x.yaml"
)

// ownStoreDescription is how the drafted Component describes its required sku.
const ownStoreDescription = "The SKU the store runs on."

// ownStoreEntities are the store's Component and its Recipe of arm, as the
// draft-set importer drafts them into the workspace's catalog folder.
func ownStoreEntities() []abi.ScaffoldFile {
	return []abi.ScaffoldFile{
		{Path: ownComponentPath, Content: draftedStoreComponent.Content},
		{Path: ownRecipePath, Content: draftedStoreRecipe.Content},
	}
}

// ownStoreComposition is the Aggregate declaring the one instance store of
// ws/storage/x:0.1.0, and the Release and Manifest the plugin kit drafts for it on
// arm, its entry realized by x and leaving infra.sku for the Release.
func ownStoreComposition() []abi.ScaffoldFile {
	return append([]abi.ScaffoldFile{storeAggregateOf("ws/storage/x:0.1.0")},
		kitReleaseOf("arm", importer.ReleaseEntry{Key: "store", Realization: "x",
			Left: []importer.LeftItem{{Path: "infra.sku"}}})...)
}

// draftSetSource writes a draft-set source spelling out files and returns its
// path.
func draftSetSource(t *testing.T, files ...abi.ScaffoldFile) string {
	t.Helper()
	return importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"), importopstest.DraftSetBody(files...))
}

// rewriteDraft replaces old with replacement in the file at path beneath dir, as an
// author editing a draft does, failing the test where the file does not hold old.
func rewriteDraft(t *testing.T, dir, path, old, replacement string) {
	t.Helper()
	place := filepath.Join(dir, filepath.FromSlash(path))
	body := readFile(t, place)
	if !strings.Contains(body, old) {
		t.Fatalf("%s does not hold %q:\n%s", path, old, body)
	}
	if err := os.WriteFile(place, []byte(strings.Replace(body, old, replacement, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// storeMappedAndSkuOwed fails the test where res does not compose the store to
// arm/x with nothing reported beside the owed values, or does not owe the
// Release's sku described as description.
func storeMappedAndSkuOwed(t *testing.T, run string, res Result, manifest, description string) {
	t.Helper()
	if !strings.Contains(manifest, "store: arm/x") {
		t.Errorf("%s: the drafted Manifest does not map store to arm/x:\n%s", run, manifest)
	}
	if got := diagnosticsBesideTheOwed(t, res); len(got) != 0 {
		t.Errorf("%s: the import reported %+v; want nothing beside the owed values", run, got)
	}
	i := slices.IndexFunc(res.Owed, func(o Owed) bool {
		return o.File == "release.yaml" && o.Key() == "spec.components.store.infra.sku"
	})
	if i < 0 {
		t.Fatalf("%s: the drafts owe %v; want the Release's sku among them", run, owedKeys(res.Owed))
	}
	if got := res.Owed[i].Question(); got != description {
		t.Errorf("%s: the owed sku is described as %q; want %q", run, got, description)
	}
}

// TestOwnRecordedDraftsLeaveTheView imports, for arm, the store's Component and
// its Recipe into the workspace's catalog folder beside an Aggregate, a Release
// and a Manifest, and an author then edits both drafts in place: the Recipe now
// implements the catalog's example/x, and the Component describes sku otherwise.
// A sync of the same source composes over the drafts rather than over what its
// record lists: the Manifest still maps store to arm/x with nothing reported, the
// sku owed is described as the drafted Component describes it, the sync drafts
// what the import drafted — nothing drifts, nothing is written — and it removes no
// file, the Component the Aggregate references included.
//
// The control imports the same source into a directory of its own, whose record
// lists none of the edited files: to it they are catalog entities of the drafts'
// identities, which the composition and the owed reading consult before the
// drafts, so the mapping is dropped and sku is described as the edit says.
//
// 🔴 PLANTED DEFECT: hand composeDraftSet and answerDrafts seam.Recipes as the
// seam was built rather than the view leaving out the import's own drafts
// (internal/importops/import.go): the sync drafts a changed Manifest, reports the
// mapping dropped, and describes sku as the edit says, and the own-drafts rows
// redden.
func TestOwnRecordedDraftsLeaveTheView(t *testing.T) {
	t.Parallel()
	rw := draftedCatalogWorkspace(t)
	root := rw.Root()
	source := draftSetSource(t, append(ownStoreEntities(), ownStoreComposition()...)...)

	first := composedImport(t, rw, source, "arm", root, authoredcontent.DeliveryWrite)
	storeMappedAndSkuOwed(t, "the import", first, fileNamed(t, first, ManifestFileName), ownStoreDescription)
	manifest := readFile(t, filepath.Join(root, ManifestFileName))
	record := readFile(t, filepath.Join(root, RecordFileName))

	const edited = "An SKU an author describes by hand."
	rewriteDraft(t, root, ownRecipePath, "implements: ws/storage/x:0.1.0", "implements: example/x")
	rewriteDraft(t, root, ownComponentPath, ownStoreDescription, edited)

	t.Run("its own drafts", func(t *testing.T) {
		res, err := syncedImport(rw, source, "arm", root, authoredcontent.DeliveryWrite)
		if err != nil {
			t.Fatalf("the sync was refused: %v", err)
		}
		if len(res.Drift) != 0 || len(res.Files) != 0 || len(res.Removed) != 0 {
			t.Errorf("the sync drifts %+v, writes %v and removes %v; want it to draft what the import drafted",
				res.Drift, draftPaths(res), res.Removed)
		}
		storeMappedAndSkuOwed(t, "the sync", res, readFile(t, filepath.Join(root, ManifestFileName)),
			ownStoreDescription)
		if got := readFile(t, filepath.Join(root, ManifestFileName)); got != manifest {
			t.Errorf("the Manifest after the sync is\n%s\nwant it as the import drafted it\n%s", got, manifest)
		}
		if got := readFile(t, filepath.Join(root, RecordFileName)); got != record {
			t.Errorf("the record after the sync is\n%s\nwant it as the import wrote it\n%s", got, record)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ownComponentPath))); err != nil {
			t.Errorf("the Component the Aggregate references is gone after the sync: %v", err)
		}
	})

	t.Run("files no record entry lists", func(t *testing.T) {
		res := composedImport(t, rw, source, "arm", filepath.Join(t.TempDir(), "drafts"),
			authoredcontent.DeliveryReport)
		if got := fileNamed(t, res, ManifestFileName); strings.Contains(got, "store: arm/x") {
			t.Errorf("the Manifest maps store to arm/x over a catalog Recipe arm/x implementing example/x:\n%s", got)
		}
		i := slices.IndexFunc(res.Owed, func(o Owed) bool { return o.Key() == "spec.components.store.infra.sku" })
		if i < 0 || res.Owed[i].Question() != edited {
			t.Errorf("the drafts owe %+v; want sku described as the catalog's Component describes it", res.Owed)
		}
	})
}

// TestAnotherFormatsDraftsStayInView drafts the store's Component and its Recipe
// into the workspace's catalog folder under a record entry another format owns,
// then imports and syncs, for arm, an Aggregate, a Release and a Manifest whose
// store is of that Component: both runs map store to arm/x through the other
// format's Recipe with nothing reported, owe the sku that format's Component
// describes, and the sync drafts what the import drafted.
//
// 🔴 PLANTED DEFECT: have Import (internal/importops/import.go) hand listedDrafts
// the files every entry of the record lists, not this import's own alone: the
// other format's Component leaves the sync's view, sku is no longer owed, and the
// sync's owed row reddens.
func TestAnotherFormatsDraftsStayInView(t *testing.T) {
	t.Parallel()
	rw := draftedCatalogWorkspace(t)
	root := rw.Root()
	composedImport(t, rw, draftSetSource(t, ownStoreEntities()...), "", root, authoredcontent.DeliveryWrite)
	ownedBy(t, root, importopstest.DraftSetFormat, "another-format")

	source := draftSetSource(t, ownStoreComposition()...)
	first := composedImport(t, rw, source, "arm", root, authoredcontent.DeliveryWrite)
	storeMappedAndSkuOwed(t, "the import", first, fileNamed(t, first, ManifestFileName), ownStoreDescription)
	if rec, _ := recordBeside(t, root); !slices.Equal(recordFormats(rec),
		[]string{"another-format", importopstest.DraftSetFormat}) {
		t.Fatalf("the record holds the entries %v; want another format's and the draft-set import's",
			recordFormats(rec))
	}

	res, err := syncedImport(rw, source, "arm", root, authoredcontent.DeliveryWrite)
	if err != nil {
		t.Fatalf("the sync was refused: %v", err)
	}
	if len(res.Drift) != 0 || len(res.Files) != 0 || len(res.Removed) != 0 {
		t.Errorf("the sync drifts %+v, writes %v and removes %v; want it to draft what the import drafted",
			res.Drift, draftPaths(res), res.Removed)
	}
	storeMappedAndSkuOwed(t, "the sync", res, readFile(t, filepath.Join(root, ManifestFileName)), ownStoreDescription)
}

// ownedBy rewrites the record beside the drafts beneath dir so the entry of the
// format from is the entry of the format to, as though an import of to had
// drafted what it lists.
func ownedBy(t *testing.T, dir, from, to string) {
	t.Helper()
	rec, _ := recordBeside(t, dir)
	e := entryIn(t, rec, from)
	e.Format = to
	rec = rec.without(from).with(e)
	if err := os.WriteFile(filepath.Join(dir, RecordFileName), []byte(recordBytes(rec)), 0o644); err != nil {
		t.Fatal(err)
	}
	if back, _ := recordBeside(t, dir); !slices.Equal(recordFormats(back), []string{to}) {
		t.Fatalf("the rewritten record holds the entries %v; want %s alone", recordFormats(back), to)
	}
}
