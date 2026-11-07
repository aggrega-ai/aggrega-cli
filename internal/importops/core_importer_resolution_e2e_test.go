// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end coverage for which importer runs, and from where its module is read,
// when the workspace's catalog holds Plugin entities of a core importer's
// identity — a checkout of a catalog recording the importers a release published,
// whose modules sit in the registry rather than beside the entities, or a
// catalog folder holding one identity at several versions — and for what the
// import tells its reader when the registry refuses the core importer's fetch.
// Every resolution case runs the REAL seam, as the cases beside this file do, and
// seeds the process-wide carried set, so none of those is parallel.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// writeEntityAt writes entity as the plugin.yaml of dir, with no module beside
// it: the shape of a catalog checkout that records the importers a release
// published, whose modules sit in the registry rather than in the checkout.
func writeEntityAt(t *testing.T, dir string, entity []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), entity, 0o644); err != nil {
		t.Fatalf("write the entity in %s: %v", dir, err)
	}
}

// cacheAcceptedModule writes module to rw's content cache at the slot the digest
// accepted addresses — the slot a fetch of a core importer writes, and the one an
// import reads that importer from — as a fetch by an earlier import would have.
func cacheAcceptedModule(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, uri, accepted string, module []byte) {
	t.Helper()
	if err := oci.WriteCached(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), oci.PluginArtifactKind(), uri, accepted, module); err != nil {
		t.Fatalf("write %s to the content cache: %v", uri, err)
	}
}

// importLangflowSource runs Import over src through rw, reporting rather than
// writing, with a recording cascade reading the workspace's content cache, and
// returns the result, the cascade, and the refusal.
func importLangflowSource(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, src catalogFolderSource) (Result,
	*recordingCascade, error) {
	t.Helper()
	cascade := &recordingCascade{workspaceCache: true}
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    src.source,
		SourceName:    filepath.Base(src.source),
		Format:        src.format,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: cascade.resolve,
	})
	return res, cascade, err
}

// TestAnImportsRegistryRefusalNamesTheImportAsTheStepToRunAgain holds the remedy
// an import's refusal of a core importer's fetch carries, for a registry that
// asked for credentials and for one that served a module that does not hold
// together, to the step the import's reader took: the import. It names the login
// against the registry's own host before the import is run again, or the import
// run again to fetch the module afresh — and never a pull, which the reader did
// not run and which an import does not need.
//
// 🔴 PLANTED DEFECT: in carriedImporterFetchRefusal (internal/importops/acquire.go),
// append oci.RemediationFor(err, subject.Host) rather than
// oci.RemediationNaming(err, subject.Host, importRemedySteps), and both rows fail:
// the remedy sends the reader to `aggrega pull --release <release>`.
func TestAnImportsRegistryRefusalNamesTheImportAsTheStepToRunAgain(t *testing.T) {
	t.Parallel()
	subject := catalog.SignatureSubject{
		Kind:     "Plugin",
		Location: "registry.example/plugin/importer/langflow",
		Host:     "registry.example",
	}
	for _, row := range []struct {
		name   string
		cause  error
		remedy string
	}{
		{
			name:   "the registry asks for credentials",
			cause:  fmt.Errorf("GET manifest: %w", oci.ErrFetchUnauthorized()),
			remedy: "remediation: aggrega login registry.example (or docker login registry.example) before re-running the import",
		},
		{
			name:   "the module does not hold together",
			cause:  fmt.Errorf("pull: %w", oci.ErrFetchIntegrity()),
			remedy: "remediation: run the import again, which fetches the module afresh",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := carriedImporterFetchRefusal(context.Background(), acquiredSourceImporter, subject, row.cause)
			if err == nil {
				t.Fatal("the fetch failure was not refused")
			}
			if !errors.Is(err, row.cause) {
				t.Errorf("the refusal does not carry the registry's condition: %v", err)
			}
			if !strings.Contains(err.Error(), row.remedy) {
				t.Errorf("the refusal does not end with the import's remedy %q:\n%v", row.remedy, err)
			}
			if strings.Contains(err.Error(), "aggrega pull") {
				t.Errorf("the refusal sends the import's reader to a pull:\n%v", err)
			}
		})
	}
}

// TestACoreImporterAtAPrereleaseVersionRunsOverAReleasedEntityWithNoModuleBesideIt
// drives an offline workspace whose catalog folder holds importer/langflow at a
// released version, 1.0.0, with no module beside it — a checkout of a catalog
// recording the importers an earlier release published — while the binary is a
// development build: it carries importer/langflow at a pre-release version, held
// to the digest of that build's module, which the workspace's content cache holds
// where a fetch of it writes.
//
// The catalog's entity cannot run: its module is read from nowhere but beside it.
// So it is no candidate, and the binary's own importer runs, read through the
// cascade the import builds for a core importer, and drafts the source. An
// unversioned choice prefers a release over a pre-release only among importers
// that can run.
//
// 🔴 PLANTED DEFECT: in importerResolutionPlugins (pkg/catalog/carried_importers.go),
// keep every entity the set holds rather than leaving out the ones
// importersWithoutModule names, and this case fails: the released entity is chosen
// over the pre-release, the caller's resolver is asked for its module, and the
// import is refused as a plugin no source holds, naming the folder of the entity.
func TestACoreImporterAtAPrereleaseVersionRunsOverAReleasedEntityWithNoModuleBesideIt(t *testing.T) {
	const developmentVersion = "2.1.0-dev.3"
	src := catalogFolderSources[0]
	scratch := aWorkspaceDir(t)
	built, _ := importopstest.LayImporters(t, scratch, filepath.Join(scratch, "catalog"),
		importopstest.Importer{Name: "langflow", Version: developmentVersion, Format: "langflow"})
	own := built[0]
	if own.Version != developmentVersion {
		t.Fatalf("the development build's importer/langflow reports %q, not %s", own.Version, developmentVersion)
	}
	module := moduleAt(t, own)
	accepted := digest.ContentDigest(module)
	carry(t, embedded.PublishedPlugin{URI: "importer/langflow", Entity: entityAt(t, own), Digest: accepted})

	rw := parserOnlyWorkspace(t)
	writeEntityAt(t, filepath.Join(rw.Root(), workspaceconfig.ConventionalCatalogDir, "plugin", "importer", "langflow",
		"1.0.0"), atVersion(t, entityAt(t, own), "1.0.0"))
	cacheAcceptedModule(t, rw, "importer/langflow", accepted, module)

	res, cascade, err := importLangflowSource(t, rw, src)
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if res.Halted() {
		t.Fatalf("the importer halted: %+v", res.Diagnostics)
	}
	if importers := importersResolved(cascade); len(importers) != 0 {
		t.Errorf("the caller's resolver was asked for %+v; the binary's own importer is read only through the "+
			"cascade the import builds for it", importers)
	}
	assertTheDefaultDraft(t, res, src)
}

// TestAnImporterIsReadFromBesideItsOwnEntityWhereItsIdentityIsHeldAtSeveralVersions
// drives an offline workspace whose catalog folder holds importer/langflow at
// 1.10.0 with its module beside it, and at 1.9.0 with none — a build's own entity
// beside one a release recorded — and attaches the importer at exactly 1.10.0,
// while the binary carries importer/langflow at 1.10.0, held to a digest no module
// has.
//
// The catalog's 1.10.0 keeps its place over the carried one, since its module is
// beside it, and it is read from there: the directory the resolution is handed
// for importer/langflow is that entity's own. The 1.9.0 entity's index key sorts
// after 1.10.0's, so a directory chosen per identity rather than per entity would
// be 1.9.0's, which holds no module.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), hand the resolver
// cat.PluginColocatedDirs() rather than cat.PluginColocatedDirsFor(p), and this
// case fails: the module is looked for beside the 1.9.0 entity, and the import is
// refused as a plugin no source holds, naming 1.9.0.
func TestAnImporterIsReadFromBesideItsOwnEntityWhereItsIdentityIsHeldAtSeveralVersions(t *testing.T) {
	src := catalogFolderSources[0]
	wsDir := aWorkspaceDir(t)
	catalogDir := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
	laid, _ := importopstest.LayImporters(t, wsDir, catalogDir,
		importopstest.Importer{Name: "langflow", Version: "1.10.0", Format: "langflow"})
	runs := laid[0]
	if runs.Version != "1.10.0" {
		t.Fatalf("the workspace's importer/langflow is at %q, not 1.10.0", runs.Version)
	}
	writeEntityAt(t, filepath.Join(filepath.Dir(runs.EntityDir), "1.9.0"), atVersion(t, entityAt(t, runs), "1.9.0"))
	importopstest.WriteOfflineConfiguration(t, wsDir, importopstest.Attachment{URI: runs.Identity})
	rw := resolveWorkspaceAt(t, wsDir)
	carry(t, embedded.PublishedPlugin{URI: "importer/langflow", Entity: entityAt(t, runs), Digest: unmatchedDigest})

	res, cascade, err := importLangflowSource(t, rw, src)
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if res.Halted() {
		t.Fatalf("the importer halted: %+v", res.Diagnostics)
	}
	importers := importersResolved(cascade)
	if len(importers) != 1 || importers[0].uri != "importer/langflow" || importers[0].tier != pluginpath.TierColocated {
		t.Fatalf("the import resolved %+v; want importer/langflow read from beside its entity", importers)
	}
	if at := importers[0].colocated["importer/langflow"]; at.Dir != runs.EntityDir || at.Version != "1.10.0" {
		t.Errorf("the resolution was handed %s (version %q) as the directory beside importer/langflow; the entity "+
			"attached sits in %s at 1.10.0", at.Dir, at.Version, runs.EntityDir)
	}
	assertTheDefaultDraft(t, res, src)
}

// TestACoreImporterRunsInPlaceOfAnEntityOfItsVersionWithNoModuleBesideIt drives an
// offline workspace whose catalog folder holds importer/langflow at exactly the
// version the binary carries, with no module beside it — a checkout of a catalog
// recording the importers this very release published — and the JSON parser.
//
// That entity cannot run, so the carried importer is served in its place, held to
// the digest the binary carries, unattached and attached at that version alike.
// Where the content cache holds the module the binary accepts, the import runs it,
// through the cascade the import builds for a core importer; where it holds none,
// the import is refused as the core importer the machine does not hold, naming
// how to fetch or place it, rather than as a module missing from beside the
// catalog's entity.
//
// 🔴 PLANTED DEFECT: in catalogImporterRuns (pkg/catalog/carried_importers.go),
// report every entity the set holds as running, whatever importersWithoutModule
// names, and every row fails: the catalog's entity keeps its place, its module is
// looked for beside it, and the import is refused naming that folder.
func TestACoreImporterRunsInPlaceOfAnEntityOfItsVersionWithNoModuleBesideIt(t *testing.T) {
	src := catalogFolderSources[0]

	for _, row := range []struct {
		name     string
		attached bool
		cached   bool
	}{
		{name: "unattached, the module cached", cached: true},
		{name: "attached at its version, the module cached", attached: true, cached: true},
		{name: "unattached, nothing cached"},
	} {
		t.Run(row.name, func(t *testing.T) {
			rw, laid := workspaceHoldingImporterLangflow(t)
			module := moduleAt(t, laid)
			accepted := digest.ContentDigest(module)
			carry(t, embedded.PublishedPlugin{URI: "importer/langflow", Entity: entityAt(t, laid), Digest: accepted})
			if err := os.Remove(laid.Module); err != nil {
				t.Fatalf("take the module from beside the workspace's entity: %v", err)
			}
			if row.attached {
				importopstest.WriteOfflineConfiguration(t, rw.Root(), importopstest.Attachment{URI: laid.Identity})
				rw = resolveWorkspaceAt(t, rw.Root())
			}
			if row.cached {
				cacheAcceptedModule(t, rw, "importer/langflow", accepted, module)
			}

			res, cascade, err := importLangflowSource(t, rw, src)
			if importers := importersResolved(cascade); len(importers) != 0 {
				t.Errorf("the caller's resolver was asked for %+v; the carried importer's module is read only "+
					"through the cascade the import builds for it", importers)
			}
			if !row.cached {
				if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
					t.Fatalf("the import was not refused as a plugin no source holds: %v", err)
				}
				want := fmt.Sprintf("core importer %q at version %s is not on this machine", "importer/langflow",
					laid.Version)
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name the core importer; want %q in: %v", want, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if res.Halted() {
				t.Fatalf("the importer halted: %+v", res.Diagnostics)
			}
			assertTheDefaultDraft(t, res, src)
		})
	}
}
