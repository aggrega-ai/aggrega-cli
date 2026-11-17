// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of an import in a workspace that names no catalog folder: it
// reads the catalog its default registry publishes as one signed snapshot, keeps
// it, pins it, reads it again with no request, refreshes it only when asked,
// refuses it offline when the machine holds none, and refuses one that does not
// verify. A workspace that names catalog folders reads the same snapshot behind
// them (layered_catalog_e2e_test.go).
//
// Every case runs the REAL seam over a registry of its own on loopback that counts
// the requests it answers (snapshottest.Registry). The snapshot it publishes is
// the draft-composition catalog — Components example/x, example/y and example/z,
// the Recipes implementing them, and the draft-set importer with its module beside
// its entity — so an import for the compose target over it drafts the Manifest
// mapping example/x, which only a catalog read from the snapshot can supply. Each
// workspace pins the test's own key for that registry and requires a signature of
// every artifact it fetches there.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/snapshottest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// snapshotTree lays the draft-composition catalog and the draft-set importer down
// in a fresh directory, the tree a case publishes as a catalog snapshot.
func snapshotTree(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "catalog")
	importopstest.LayDraftComposerCatalog(t, filepath.Dir(dir), dir)
	return dir
}

// snapshotFixture is a registry publishing the snapshot of a tree, signed by the
// key a workspace pins: the registry, the key, and the snapshot's manifest digest.
type snapshotFixture struct {
	reg    *snapshottest.Registry
	signer *signing.KeyedSigner
	pub    []byte
	digest string
}

// publishedSnapshot publishes the snapshot of snapshotTree as latest, signed by a
// fresh key.
func publishedSnapshot(t *testing.T) snapshotFixture {
	t.Helper()
	f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
	f.signer, f.pub = snapshottest.NewKey(t)
	f.digest = f.reg.Publish(t, snapshotTree(t), catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
	return f
}

// holdSnapshotOf puts the published catalog snapshot of the tree at tree on the
// machine of the workspace at wsDir, as the snapshot it reads: published to a
// registry of the test's own, signed by a key the workspace pins, and fetched
// through the import's own acquisition. It leaves the workspace configured
// online for that registry, so a caller that wants it configured otherwise —
// offline, say — writes its own configuration afterwards, and the workspace still
// reads the snapshot it holds. It returns the snapshot's manifest digest.
func holdSnapshotOf(t *testing.T, wsDir, tree string) string {
	t.Helper()
	f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
	f.signer, f.pub = snapshottest.NewKey(t)
	f.digest = f.reg.Publish(t, tree, catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
	rw := snapshotWorkspaceAt(t, wsDir, f, false)
	held, err := readCatalogSnapshot(context.Background(), rw, catalogSnapshotRead{})
	if err != nil {
		t.Fatalf("put the catalog snapshot of %s on the machine: %v", tree, err)
	}
	if held.digest != f.digest {
		t.Fatalf("the workspace holds the snapshot %q; want %q", held.digest, f.digest)
	}
	return f.digest
}

// snapshotWorkspaceAt writes, at wsDir, the configuration naming f's registry as
// the default one and pinning its key, offline where offline is set, and resolves
// the workspace. It names no catalog folder and lays none.
func snapshotWorkspaceAt(t *testing.T, wsDir string, f snapshotFixture, offline bool) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := f.reg.WorkspaceConfiguration(f.pub, offline)
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return resolveWorkspaceAt(t, wsDir)
}

// snapshotImportOptions are the options a case imports the draft-composition
// source with: the draft-set format named, the compose target, the delivery given,
// and an output directory of the case's own.
func snapshotImportOptions(t *testing.T, delivery authoredcontent.Delivery) Options {
	t.Helper()
	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"), importopstest.DraftComposerSource())
	return Options{
		SourcePath:    source,
		SourceName:    source,
		Format:        importopstest.DraftSetFormat,
		Target:        "compose",
		OutputDir:     t.TempDir(),
		Delivery:      delivery,
		ResolvePlugin: (&recordingCascade{}).resolve,
	}
}

// recordIn reads the record a result carries, failing the test where it carries
// none or one an import does not write.
func recordIn(t *testing.T, res Result) importRecord {
	t.Helper()
	rec, err := parseRecord([]byte(fileNamed(t, res, RecordFileName)))
	if err != nil {
		t.Fatalf("the record the import wrote does not read back: %v", err)
	}
	return rec
}

// currentSnapshot returns the digest the workspace's content cache names as the
// snapshot it reads, "" where it names none.
func currentSnapshot(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) string {
	t.Helper()
	b, err := os.ReadFile(oci.CatalogSnapshotCurrentPath(rw.Root(), rw.Config().ResolvedArtifactCacheDir()))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read the workspace's current snapshot: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// TestAnImportNamingNoCatalogFolderReadsThePublishedSnapshot imports for the
// compose target in a workspace that names no catalog folder, over a registry
// publishing a signed snapshot of the draft-composition catalog, and holds the
// import to what only that catalog supplies: the Manifest maps example/x to its
// one compose Recipe. The record beside the drafts names the snapshot by its
// manifest digest, and the workspace now reads that snapshot.
//
// 🔴 PLANTED DEFECTS:
//   - drop the readSnapshot step from Import (internal/importops/import.go): the
//     import reads no catalog and is refused, no Recipe binding to compose;
//   - leave CatalogSnapshot out of the record (recordScaffold): the pin row fails.
func TestAnImportNamingNoCatalogFolderReadsThePublishedSnapshot(t *testing.T) {
	t.Parallel()
	f := publishedSnapshot(t)
	rw := snapshotWorkspaceAt(t, aWorkspaceDir(t), f, false)

	res, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if got := fileNamed(t, res, ManifestFileName); got != importopstest.DraftedManifestComposed {
		t.Errorf("the drafted Manifest is\n%s\nwant the one the snapshot's Recipes give\n%s", got,
			importopstest.DraftedManifestComposed)
	}
	if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
		t.Errorf("the record names the catalog snapshot %q; want %q", got, f.digest)
	}
	if got := currentSnapshot(t, rw); got != f.digest {
		t.Errorf("the workspace reads the snapshot %q; want %q", got, f.digest)
	}
	if f.reg.Requests() == 0 {
		t.Error("the registry answered no request, so the snapshot cannot have been fetched from it")
	}
}

// TestAnImportReadsThePinnedSnapshotWithNoRequest imports once, which fetches the
// snapshot, and then again in three ways, counting the registry's requests across
// each: a second import into another directory, an import resolved under the
// offline posture, and a sync of the first import's drafts. None asks the registry
// anything, each drafts the same Manifest, and each pins the same snapshot.
//
// 🔴 PLANTED DEFECT: have readCatalogSnapshot always fetch the newest snapshot
// (skip pinnedSnapshot and heldCatalogSnapshot): every row's count rises, and the
// offline row is refused.
func TestAnImportReadsThePinnedSnapshotWithNoRequest(t *testing.T) {
	t.Parallel()
	f := publishedSnapshot(t)
	wsDir := aWorkspaceDir(t)
	rw := snapshotWorkspaceAt(t, wsDir, f, false)
	first := snapshotImportOptions(t, authoredcontent.DeliveryWrite)
	if _, err := Import(context.Background(), rw, first); err != nil {
		t.Fatalf("the first import was refused: %v", err)
	}

	for _, tc := range []struct {
		name string
		rw   func(t *testing.T) *workspaceconfig.ResolvedWorkspace
		opts func(t *testing.T) Options
	}{
		{
			name: "another import",
			rw:   func(*testing.T) *workspaceconfig.ResolvedWorkspace { return rw },
			opts: func(t *testing.T) Options { return snapshotImportOptions(t, authoredcontent.DeliveryReport) },
		},
		{
			name: "an import under the offline posture",
			rw:   func(t *testing.T) *workspaceconfig.ResolvedWorkspace { return snapshotWorkspaceAt(t, wsDir, f, true) },
			opts: func(t *testing.T) Options { return snapshotImportOptions(t, authoredcontent.DeliveryReport) },
		},
		{
			name: "a sync of the first import's drafts",
			rw:   func(*testing.T) *workspaceconfig.ResolvedWorkspace { return snapshotWorkspaceAt(t, wsDir, f, false) },
			opts: func(t *testing.T) Options {
				o := first
				o.Sync, o.Delivery = true, authoredcontent.DeliveryReport
				return o
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.reg.Requests()
			o := tc.opts(t)
			res, err := Import(context.Background(), tc.rw(t), o)
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if after := f.reg.Requests(); after != before {
				t.Errorf("the import asked the registry %d requests; want none", after-before)
			}
			if o.Sync {
				if len(res.Drift) != 0 {
					t.Errorf("the sync found drift %+v over drafts imported against the same snapshot", res.Drift)
				}
				return
			}
			if got := fileNamed(t, res, ManifestFileName); got != importopstest.DraftedManifestComposed {
				t.Errorf("the drafted Manifest is\n%s\nwant\n%s", got, importopstest.DraftedManifestComposed)
			}
			if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
				t.Errorf("the record names the catalog snapshot %q; want %q", got, f.digest)
			}
		})
	}
}

// TestUpgradeRefreshesThePinnedSnapshot publishes a first snapshot, imports, then
// publishes a second one — the same catalog with one more file — as latest. An
// import without Upgrade still reads the first; one with Upgrade fetches the second
// and pins it, and every import after it reads the second; a sync of drafts the
// first import wrote reads the first, as its record names, until it is given
// Upgrade too.
//
// 🔴 PLANTED DEFECT: ignore read.upgrade in readCatalogSnapshot: the Upgrade row
// pins the first snapshot.
func TestUpgradeRefreshesThePinnedSnapshot(t *testing.T) {
	t.Parallel()
	f := publishedSnapshot(t)
	rw := snapshotWorkspaceAt(t, aWorkspaceDir(t), f, false)
	first := snapshotImportOptions(t, authoredcontent.DeliveryWrite)
	if _, err := Import(context.Background(), rw, first); err != nil {
		t.Fatalf("the first import was refused: %v", err)
	}

	newer := snapshotTree(t)
	importopstest.WriteSource(t, filepath.Join(newer, "notes.txt"), "a file the first snapshot does not carry\n")
	second := f.reg.Publish(t, newer, catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
	if second == f.digest {
		t.Fatal("the second snapshot has the first one's digest, so nothing here could tell them apart")
	}

	// pinOf imports under o and returns the snapshot the record it leaves names: the
	// record the import hands back, or, for a sync that leaves the record as it is
	// and so hands back none, the one on disk beside the drafts.
	pinOf := func(t *testing.T, o Options) string {
		t.Helper()
		res, err := Import(context.Background(), rw, o)
		if err != nil {
			t.Fatalf("the import was refused: %v", err)
		}
		for _, file := range res.Files {
			if file.Path == RecordFileName {
				return recordIn(t, res).CatalogSnapshot
			}
		}
		onDisk, err := os.ReadFile(filepath.Join(o.OutputDir, RecordFileName))
		if err != nil {
			t.Fatalf("the import handed back no record and none is beside the drafts: %v", err)
		}
		rec, err := parseRecord(onDisk)
		if err != nil {
			t.Fatalf("the record beside the drafts does not read back: %v", err)
		}
		return rec.CatalogSnapshot
	}
	report := func(t *testing.T, upgrade, sync bool) Options {
		o := snapshotImportOptions(t, authoredcontent.DeliveryReport)
		if sync {
			o = first
			o.Sync, o.Delivery = true, authoredcontent.DeliveryReport
		}
		o.Upgrade = upgrade
		return o
	}
	for _, tc := range []struct {
		name          string
		upgrade, sync bool
		want          string
	}{
		{name: "an import without upgrade", want: f.digest},
		{name: "an import with upgrade", upgrade: true, want: second},
		{name: "an import after the upgrade", want: second},
		{name: "a sync of the first import's drafts", sync: true, want: f.digest},
		{name: "a sync with upgrade", sync: true, upgrade: true, want: second},
	} {
		if got := pinOf(t, report(t, tc.upgrade, tc.sync)); got != tc.want {
			t.Errorf("%s reads the snapshot %q; want %q", tc.name, got, tc.want)
		}
	}
	if got := currentSnapshot(t, rw); got != second {
		t.Errorf("after the upgrade the workspace reads the snapshot %q; want %q", got, second)
	}
}

// TestOfflineWithNoSnapshotOnTheMachineRefusesNamingWhatToRun imports under the
// offline posture in a workspace whose machine holds no snapshot, and refreshes
// under it in one that holds one: both are refused under the offline condition,
// naming what puts a snapshot on the machine, and neither asks the registry
// anything or keeps anything.
//
// 🔴 PLANTED DEFECT: have fetchCatalogSnapshot return the egress refusal as it
// comes: the first row loses its remedy.
func TestOfflineWithNoSnapshotOnTheMachineRefusesNamingWhatToRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		held    bool
		upgrade bool
		want    []string
	}{
		{
			name: "nothing held",
			want: []string{"holds no catalog snapshot", "run the import once with the network available",
				"AGGREGA_CATALOG_DIRS"},
		},
		{
			name: "a refresh with a snapshot held", held: true, upgrade: true,
			want: []string{"refreshing the published catalog"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := publishedSnapshot(t)
			wsDir := aWorkspaceDir(t)
			if tc.held {
				online := snapshotWorkspaceAt(t, wsDir, f, false)
				if _, err := Import(context.Background(), online, snapshotImportOptions(t, authoredcontent.DeliveryReport)); err != nil {
					t.Fatalf("the online import was refused: %v", err)
				}
			}
			rw := snapshotWorkspaceAt(t, wsDir, f, true)
			before := f.reg.Requests()
			o := snapshotImportOptions(t, authoredcontent.DeliveryReport)
			o.Upgrade = tc.upgrade
			_, err := Import(context.Background(), rw, o)
			if !errors.Is(err, egress.ErrOfflineEgress()) {
				t.Fatalf("the import was not refused as offline: %v", err)
			}
			for _, part := range tc.want {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("the refusal does not carry %q: %v", part, err)
				}
			}
			if after := f.reg.Requests(); after != before {
				t.Errorf("the offline import asked the registry %d requests", after-before)
			}
			if !tc.held && currentSnapshot(t, rw) != "" {
				t.Error("the refused import left a snapshot in the content cache")
			}
		})
	}
}

// TestASnapshotThatDoesNotVerifyIsRefused fetches a snapshot that should not be
// read — its archive altered on the way, signed by a key the workspace does not
// trust, or not signed at all where a signature is required — and holds each
// refusal to its condition, with nothing kept in the content cache.
//
// 🔴 PLANTED DEFECT: skip catalog.VerifySnapshot in fetchCatalogSnapshot: the
// untrusted and unsigned rows are read.
func TestASnapshotThatDoesNotVerifyIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		publish func(t *testing.T) snapshotFixture
		refused func(error) bool
	}{
		{
			name: "an archive altered on the way",
			publish: func(t *testing.T) snapshotFixture {
				f := publishedSnapshot(t)
				f.reg.Tamper()
				return f
			},
			refused: func(err error) bool { return errors.Is(err, oci.ErrFetchIntegrity()) },
		},
		{
			name: "a signature by a key the workspace does not trust",
			publish: func(t *testing.T) snapshotFixture {
				f := publishedSnapshot(t)
				_, f.pub = snapshottest.NewKey(t)
				return f
			},
			refused: func(err error) bool {
				return errors.Is(err, signing.ErrSignatureInvalid()) || errors.Is(err, signing.ErrSignatureVerificationFailed())
			},
		},
		{
			name: "no signature where one is required",
			publish: func(t *testing.T) snapshotFixture {
				f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
				f.signer, f.pub = snapshottest.NewKey(t)
				f.digest = f.reg.Publish(t, snapshotTree(t), catalog.SnapshotLatestTag)
				return f
			},
			refused: func(err error) bool { return errors.Is(err, signing.ErrNoSignatureFound()) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := tc.publish(t)
			rw := snapshotWorkspaceAt(t, aWorkspaceDir(t), f, false)
			_, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
			if err == nil || !tc.refused(err) {
				t.Fatalf("the snapshot was not refused under its condition: %v", err)
			}
			if got := currentSnapshot(t, rw); got != "" {
				t.Errorf("the refused snapshot became the one the workspace reads: %s", got)
			}
			slot, err := oci.CatalogSnapshotSlot(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), f.digest)
			if err != nil {
				t.Fatal(err)
			}
			for _, kept := range []string{oci.SlotBlobFile, oci.SlotManifestFile} {
				if _, err := os.Stat(filepath.Join(slot, kept)); err == nil {
					t.Errorf("the refused snapshot's %s was kept in %s", kept, slot)
				}
			}
		})
	}
}

// TestAHeldSnapshotThatNoLongerMatchesItsDigestIsRefused fetches a snapshot,
// alters the archive kept in the content cache, and imports again: the import is
// refused as the integrity condition, naming the refresh that repairs it, and the
// registry is asked nothing.
//
// 🔴 PLANTED DEFECT: skip the content check in heldCatalogSnapshot: the import
// reads the altered archive.
func TestAHeldSnapshotThatNoLongerMatchesItsDigestIsRefused(t *testing.T) {
	t.Parallel()
	f := publishedSnapshot(t)
	rw := snapshotWorkspaceAt(t, aWorkspaceDir(t), f, false)
	if _, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport)); err != nil {
		t.Fatalf("the first import was refused: %v", err)
	}
	slot, err := oci.CatalogSnapshotSlot(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), f.digest)
	if err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(slot, oci.SlotBlobFile)
	held, err := os.ReadFile(blob)
	if err != nil {
		t.Fatalf("read the kept archive: %v", err)
	}
	held[len(held)-1] ^= 0xff
	if err := os.WriteFile(blob, held, 0o644); err != nil {
		t.Fatal(err)
	}
	before := f.reg.Requests()
	_, err = Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("the altered snapshot was not refused as the integrity condition: %v", err)
	}
	if !strings.Contains(err.Error(), "--upgrade") {
		t.Errorf("the refusal does not name the refresh that repairs it: %v", err)
	}
	if after := f.reg.Requests(); after != before {
		t.Errorf("the import asked the registry %d requests", after-before)
	}
}

// TestARegistryPublishingNoSnapshotLeavesTheImportWithNoCatalog imports for the
// compose target in a workspace naming no catalog folder whose registry publishes
// no snapshot: the import reads no catalog, so the target is refused as one no
// Recipe binds to, and the refusal says the registry publishes no snapshot.
func TestARegistryPublishingNoSnapshotLeavesTheImportWithNoCatalog(t *testing.T) {
	t.Parallel()
	f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
	f.signer, f.pub = snapshottest.NewKey(t)
	rw := snapshotWorkspaceAt(t, aWorkspaceDir(t), f, false)
	_, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
	if !errors.Is(err, ErrImportTargetUnbound()) {
		t.Fatalf("the import was not refused as a target no Recipe binds to: %v", err)
	}
	if !strings.Contains(err.Error(), "publishes no catalog snapshot") {
		t.Errorf("the refusal does not say the registry publishes no snapshot: %v", err)
	}
	if got := currentSnapshot(t, rw); got != "" {
		t.Errorf("the workspace reads the snapshot %q, though none is published", got)
	}
}

// TestASnapshotThePublishersScriptPushedIsImported publishes the snapshot of the
// draft-composition catalog through the catalog's own publisher script — a real
// run against a registry of the test's own, with no credential — signs it with the
// test's key, and imports for the compose target in a workspace naming no catalog
// folder: the import reads the manifest and archive the script wrote, drafts the
// Manifest the snapshot's Recipes give, and pins the digest the script printed.
// Where the catalog's repository is not checked out beside this module the test
// fails naming the script.
//
// 🔴 PLANTED DEFECT: change MEDIA_TYPE in the script: the import refuses the
// manifest for want of a layer of the snapshot's media type.
func TestASnapshotThePublishersScriptPushedIsImported(t *testing.T) {
	t.Parallel()
	f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
	f.signer, f.pub = snapshottest.NewKey(t)
	published := f.reg.PublishThroughScript(t, snapshotTree(t))
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
	f.digest = published.Digest
	rw := snapshotWorkspaceAt(t, aWorkspaceDir(t), f, false)

	res, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
	if err != nil {
		t.Fatalf("the import of the snapshot the script pushed was refused: %v", err)
	}
	if got := fileNamed(t, res, ManifestFileName); got != importopstest.DraftedManifestComposed {
		t.Errorf("the drafted Manifest is\n%s\nwant the one the snapshot's Recipes give\n%s", got,
			importopstest.DraftedManifestComposed)
	}
	if got := recordIn(t, res).CatalogSnapshot; got != published.Digest {
		t.Errorf("the record names the catalog snapshot %q; want the one the script pushed, %q", got, published.Digest)
	}
}

// TestTheRecordBesideTheDraftsPinsTheSnapshotEveryImportReads holds an import to
// the snapshot the record beside its drafts names, whether or not it is a sync:
// a first import pins the first snapshot; a second one is published and the
// workspace moved to it with Upgrade; then an import into a directory holding the
// first import's record reads the FIRST snapshot, not the one the workspace reads;
// after the content cache is cleared the same import fetches that pinned snapshot
// by its digest and never asks for latest; and Upgrade still moves it to the newest
// and pins that.
//
// 🔴 PLANTED DEFECT: read the record only under a sync (the pinning block in
// Import): the first row pins the second snapshot, and the second asks for latest.
func TestTheRecordBesideTheDraftsPinsTheSnapshotEveryImportReads(t *testing.T) {
	t.Parallel()
	f := publishedSnapshot(t)
	rw := snapshotWorkspaceAt(t, aWorkspaceDir(t), f, false)
	first := snapshotImportOptions(t, authoredcontent.DeliveryWrite)
	if _, err := Import(context.Background(), rw, first); err != nil {
		t.Fatalf("the first import was refused: %v", err)
	}
	record, err := os.ReadFile(filepath.Join(first.OutputDir, RecordFileName))
	if err != nil {
		t.Fatalf("the first import wrote no record: %v", err)
	}

	newer := snapshotTree(t)
	importopstest.WriteSource(t, filepath.Join(newer, "notes.txt"), "a file the first snapshot does not carry\n")
	second := f.reg.Publish(t, newer, catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
	moved := snapshotImportOptions(t, authoredcontent.DeliveryReport)
	moved.Upgrade = true
	if _, err := Import(context.Background(), rw, moved); err != nil {
		t.Fatalf("the upgrade was refused: %v", err)
	}
	if got := currentSnapshot(t, rw); got != second {
		t.Fatalf("the workspace reads the snapshot %q after the upgrade; want %q", got, second)
	}

	// besideTheRecord is an import whose output directory holds the first import's
	// record and nothing else, so it drafts without meeting an Aggregate already
	// there and reads the record's pin.
	besideTheRecord := func(t *testing.T, upgrade bool) Options {
		o := snapshotImportOptions(t, authoredcontent.DeliveryReport)
		if err := os.WriteFile(filepath.Join(o.OutputDir, RecordFileName), record, 0o644); err != nil {
			t.Fatal(err)
		}
		o.Upgrade = upgrade
		return o
	}
	pinOf := func(t *testing.T, o Options) (string, []string) {
		t.Helper()
		before := f.reg.Requests()
		res, err := Import(context.Background(), rw, o)
		if err != nil {
			t.Fatalf("the import was refused: %v", err)
		}
		return recordIn(t, res).CatalogSnapshot, f.reg.Asked(before)
	}

	if got, asked := pinOf(t, besideTheRecord(t, false)); got != f.digest || len(asked) != 0 {
		t.Errorf("an import beside the first record read %q asking %v; want the record's %q, held, asking nothing",
			got, asked, f.digest)
	}

	if err := os.RemoveAll(oci.CatalogSnapshotsRoot(rw.Root(), rw.Config().ResolvedArtifactCacheDir())); err != nil {
		t.Fatal(err)
	}
	got, asked := pinOf(t, besideTheRecord(t, false))
	if got != f.digest {
		t.Errorf("after the cache was cleared, an import beside the first record read %q; want its pin %q", got, f.digest)
	}
	if !slices.Contains(asked, "GET "+f.reg.ManifestPath(f.digest)) ||
		slices.Contains(asked, "GET "+f.reg.ManifestPath(catalog.SnapshotLatestTag)) {
		t.Errorf("after the cache was cleared the import asked %v; want the pinned manifest by digest and never latest",
			asked)
	}

	got, asked = pinOf(t, besideTheRecord(t, true))
	if got != second || !slices.Contains(asked, "GET "+f.reg.ManifestPath(catalog.SnapshotLatestTag)) {
		t.Errorf("an upgrade beside the first record read %q asking %v; want the newest, %q, asked for as latest",
			got, asked, second)
	}
}
