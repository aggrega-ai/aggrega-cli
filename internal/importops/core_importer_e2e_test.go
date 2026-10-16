// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end coverage for the importers the binary carries the entities of
// without carrying their modules — the compiler's own core importers — as the
// import's catalog view serves them beside the workspace's own. Every case runs
// the REAL seam: Import builds it from the workspace's own catalog and
// configuration, and nothing below points that variable.
//
// Two kinds of case live here. The ones driven by the importers the build of this
// binary carries fail, rather than skip, where the build carries none: the claim
// is about the set a release ships, and a run that never saw it proves nothing
// about it. The ones driven by a carried set the case chose — an identity, a
// version or a digest it controls — say so, and run one at a time, because the
// set is process-wide.

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// coreImporterOf is the identity of the core importer serving each format.
var coreImporterOf = map[string]string{
	"langflow": "importer/langflow",
	"flowise":  "importer/flowise",
}

// requireCarriedCoreImporters fails the test unless the binary carries the core
// importer of every format coreImporterOf names, with a non-blank digest, and
// returns what it carries of each by identity.
//
// It fails rather than skips: a build that carries none is a build these cases
// cannot speak for, and a skip would read as a pass over a set nobody looked at.
func requireCarriedCoreImporters(t *testing.T) map[string]embedded.PublishedPlugin {
	t.Helper()
	carried := map[string]embedded.PublishedPlugin{}
	for _, p := range embedded.PublishedPlugins() {
		carried[p.URI] = p
	}
	for _, format := range slices.Sorted(maps.Keys(coreImporterOf)) {
		uri := coreImporterOf[format]
		p, ok := carried[uri]
		if !ok || strings.TrimSpace(p.Digest) == "" || len(p.Entity) == 0 {
			t.Fatalf("this binary carries no entity and digest for %s, so nothing here can show the core %s importer "+
				"being served: build the core plugins with the repository's build script, which writes what the "+
				"binary carries of each core plugin it does not embed", uri, format)
		}
	}
	return carried
}

// parserOnlyWorkspace resolves a scratch workspace whose catalog folder holds the
// JSON parser and no importer, and whose configuration attaches nothing.
func parserOnlyWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.LayJSONParser(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir))
	importopstest.WriteConfiguration(t, wsDir)
	return resolveWorkspaceAt(t, wsDir)
}

// importersResolved returns the importer references a recorded cascade was asked
// for, in order, and the call for each.
func importersResolved(cascade *recordingCascade) []cascadeResolution {
	var out []cascadeResolution
	for _, call := range cascade.resolutions() {
		if strings.HasPrefix(call.uri, "importer/") {
			out = append(out, call)
		}
	}
	return out
}

// TestAWorkspaceHoldingNoImporterResolvesTheBinarysCoreImporter drives a workspace
// whose catalog holds the JSON parser and no importer, over the importers this
// build of the binary carries, with the format named and with it recognised.
//
// The core importer of the source's format is the one resolved — the cascade is
// asked for it by its identity — and the recognition reads that importer's own
// recorded signatures, since the catalog holds no other. Its resolution is handed
// no directory beside a catalog entity, while the parser's is handed the
// catalog's. No module of it is on this machine — the cascade reads the
// workspace's content cache, which is empty — so the import is refused under the
// condition for a plugin no source holds, naming the importer.
//
// 🔴 PLANTED DEFECT: delete cat.UseCarriedImporterResolution() from buildSeam
// (internal/importops/seam.go) and every row fails — the named rows with the
// no-importer condition, the recognised rows as a source no importer recognises.
func TestAWorkspaceHoldingNoImporterResolvesTheBinarysCoreImporter(t *testing.T) {
	t.Parallel()
	requireCarriedCoreImporters(t)

	for _, src := range catalogFolderSources {
		for _, named := range []bool{true, false} {
			label := src.format + ", the format recognised"
			format := ""
			if named {
				label, format = src.format+", the format named", src.format
			}
			t.Run(label, func(t *testing.T) {
				t.Parallel()
				rw := parserOnlyWorkspace(t)
				cascade := &recordingCascade{workspaceCache: true}
				_, err := Import(context.Background(), rw, Options{
					SourcePath:    src.source,
					SourceName:    filepath.Base(src.source),
					Format:        format,
					Delivery:      authoredcontent.DeliveryFor(true),
					ResolvePlugin: cascade.resolve,
				})
				if err == nil {
					t.Fatal("the import ran, though no module of the core importer is on this machine")
				}
				if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
					t.Fatalf("the import was not refused as a plugin no source holds: %v", err)
				}
				want := coreImporterOf[src.format]
				if !strings.Contains(err.Error(), `"`+want+`"`) {
					t.Errorf("the refusal does not name %s: %v", want, err)
				}
				importers := importersResolved(cascade)
				if len(importers) != 1 || importers[0].uri != want {
					t.Fatalf("the import resolved the importers %v; want exactly %s", importers, want)
				}
				if len(importers[0].colocated) != 0 {
					t.Errorf("resolving %s was handed the directories beside catalog entities %v; a core importer "+
						"has no entity in a catalog directory", want, importers[0].colocated)
				}
				if named {
					// A named format opens nothing before the importer loads, so the
					// parser is never asked for.
					return
				}
				parser := slices.IndexFunc(cascade.resolutions(), func(c cascadeResolution) bool {
					return c.uri == "parser/"+importopstest.ParserName
				})
				if parser < 0 {
					t.Fatalf("the recognition never resolved the catalog's parser; it resolved %v", cascade.resolutions())
				}
				if len(cascade.resolutions()[parser].colocated) == 0 {
					t.Error("the parser's resolution was handed no directories, so the core importer's was not singled out")
				}
			})
		}
	}
}

// TestABinaryCoreImporterCompetesWithAWorkspaceImporterOfAnotherName drives a
// workspace whose catalog holds an importer of another name for the Langflow
// format, over the importers this build of the binary carries. With no attachment
// naming it the import halts as ambiguous, naming both importers and loading
// neither; with one naming it, the workspace's importer runs, read from beside
// its entity, and drafts the source.
func TestABinaryCoreImporterCompetesWithAWorkspaceImporterOfAnotherName(t *testing.T) {
	t.Parallel()
	requireCarriedCoreImporters(t)
	src := catalogFolderSources[0]

	for _, attached := range []bool{false, true} {
		name := "unattached"
		if attached {
			name = "attached"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rw := attachedImportWorkspace(t, []importopstest.Importer{{Name: "import-langflow", Format: src.format}},
				func(laid map[string]importopstest.LaidPlugin) []importopstest.Attachment {
					if !attached {
						return nil
					}
					return []importopstest.Attachment{{URI: laid["import-langflow"].Identity}}
				})
			cascade := &recordingCascade{workspaceCache: true}
			res, err := Import(context.Background(), rw, Options{
				SourcePath:    src.source,
				SourceName:    filepath.Base(src.source),
				Format:        src.format,
				Delivery:      authoredcontent.DeliveryFor(true),
				ResolvePlugin: cascade.resolve,
			})
			importers := importersResolved(cascade)

			if !attached {
				if !errors.Is(err, catalog.ErrAmbiguousImporterPlugin()) {
					t.Fatalf("the import was not refused as ambiguous: %v", err)
				}
				want := `format "langflow" is advertised by importer plugins [import-langflow langflow]`
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the ambiguity halt reads %q; want it to carry %q", err.Error(), want)
				}
				if len(importers) != 0 {
					t.Errorf("an ambiguous import loaded the importers %v", importers)
				}
				return
			}
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if res.Halted() {
				t.Fatalf("the importer halted: %+v", res.Diagnostics)
			}
			if len(importers) != 1 || importers[0].uri != "importer/import-langflow" ||
				importers[0].tier != pluginpath.TierColocated {
				t.Fatalf("the import resolved %+v; want the workspace's importer read from beside its entity", importers)
			}
			if got := draftPaths(res); !slices.Equal(got, src.drafts) {
				t.Errorf("the importer drafted %v, want %v", got, src.drafts)
			}
		})
	}
}

// unmatchedDigest is a content digest no module has.
const unmatchedDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// carry makes the binary carry exactly plugins for the rest of the test.
//
// 🔴 The carried set is process-wide, so a case calling this must not be
// parallel.
func carry(t *testing.T, plugins ...embedded.PublishedPlugin) {
	t.Helper()
	t.Cleanup(embedded.SeedPublishedForTest(plugins))
}

// entityAt reads the entity the folder laid for p.
func entityAt(t *testing.T, p importopstest.LaidPlugin) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.EntityDir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("read the entity laid for %s: %v", p.Identity, err)
	}
	return b
}

// atVersion returns entity with its metadata.version set to version.
func atVersion(t *testing.T, entity []byte, version string) []byte {
	t.Helper()
	var p types.Plugin
	if err := yaml.Unmarshal(entity, &p); err != nil {
		t.Fatalf("read the entity: %v", err)
	}
	p.Metadata.Version = version
	out, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("encode the entity at %s: %v", version, err)
	}
	return out
}

// workspaceHoldingImporterLangflow resolves a scratch workspace whose catalog
// folder holds importer/langflow — the reference Langflow importer under the core
// importer's identity, at the fixture's own version, with its module beside it —
// and the JSON parser, attaching nothing, and returns it and what was laid for the
// importer.
func workspaceHoldingImporterLangflow(t *testing.T) (*workspaceconfig.ResolvedWorkspace, importopstest.LaidPlugin) {
	t.Helper()
	var laid importopstest.LaidPlugin
	rw := attachedImportWorkspace(t, []importopstest.Importer{{Name: "langflow", Format: "langflow"}},
		func(l map[string]importopstest.LaidPlugin) []importopstest.Attachment {
			laid = l["langflow"]
			return nil
		})
	if laid.Identity != "importer/langflow:"+laid.Version {
		t.Fatalf("the workspace's catalog holds %q, not importer/langflow", laid.Identity)
	}
	return rw, laid
}

// moduleAt reads the module laid beside p's entity.
func moduleAt(t *testing.T, p importopstest.LaidPlugin) []byte {
	t.Helper()
	b, err := os.ReadFile(p.Module)
	if err != nil {
		t.Fatalf("read the module laid for %s: %v", p.Identity, err)
	}
	return b
}

// TestAWorkspaceImporterAtTheIdentityAndVersionOfACoreImporterKeepsItsPlace drives
// a workspace whose catalog holds importer/langflow with its module beside it,
// while the binary is made to carry an importer of exactly that identity and
// version, held to a digest no module has.
//
// The workspace's entity keeps its place: the import runs it, reading its module
// from beside it, and drafts the source. Had the carried entity been served, or
// its digest applied to the workspace's entity, the module beside the entity would
// not have been read, or would have been refused.
//
// 🔴 PLANTED DEFECT: in CarriedImporterDigest (pkg/catalog/carried_importers.go),
// delete the check returning false where the view's own set holds the key, and
// this case fails: the workspace's importer is taken for the carried one, its
// resolution is handed no directories, and the import is refused as a plugin no
// source holds.
func TestAWorkspaceImporterAtTheIdentityAndVersionOfACoreImporterKeepsItsPlace(t *testing.T) {
	// Not parallel: it seeds the process-wide carried set.
	src := catalogFolderSources[0]
	rw, laid := workspaceHoldingImporterLangflow(t)
	carry(t, embedded.PublishedPlugin{URI: "importer/langflow", Entity: entityAt(t, laid), Digest: unmatchedDigest})

	cascade := &recordingCascade{workspaceCache: true}
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    src.source,
		SourceName:    filepath.Base(src.source),
		Format:        src.format,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: cascade.resolve,
	})
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if res.Halted() {
		t.Fatalf("the importer halted: %+v", res.Diagnostics)
	}
	importers := importersResolved(cascade)
	if len(importers) != 1 || importers[0].uri != "importer/langflow" || importers[0].tier != pluginpath.TierColocated {
		t.Fatalf("the import resolved %+v; want the workspace's importer/langflow read from beside its entity", importers)
	}
	if got := draftPaths(res); !slices.Equal(got, src.drafts) {
		t.Errorf("the importer drafted %v, want %v", got, src.drafts)
	}
}

// TestACoreImporterAtAHigherVersionIsNeverReadFromBesideAWorkspaceEntity drives a
// workspace whose catalog holds importer/langflow at 1.0.0 with its module beside
// it, while the binary is made to carry importer/langflow at 9.0.0, held to the
// digest of that very module.
//
// The higher version is chosen, and it is the carried one, so its resolution is
// handed no directory beside a catalog entity: the module beside the workspace's
// entity belongs to that entity, and no other tier holds one, so the import is
// refused as a plugin no source holds. The carried digest is the one module that
// is there, so a resolution that did read from beside the workspace's entity
// would pass the digest and run — which is what this case refuses to pass on.
//
// 🔴 PLANTED DEFECT: in loadCarriedImporter (internal/importops/seam.go), hand
// the resolution the view's directories rather than none, and this case fails:
// the import runs the workspace's module under the carried entity.
func TestACoreImporterAtAHigherVersionIsNeverReadFromBesideAWorkspaceEntity(t *testing.T) {
	// Not parallel: it seeds the process-wide carried set.
	src := catalogFolderSources[0]
	rw, laid := workspaceHoldingImporterLangflow(t)
	if laid.Version != "1.0.0" {
		t.Fatalf("the workspace's importer/langflow is at %q, not below the carried 9.0.0", laid.Version)
	}
	carry(t, embedded.PublishedPlugin{
		URI:    "importer/langflow",
		Entity: atVersion(t, entityAt(t, laid), "9.0.0"),
		Digest: digest.ContentDigest(moduleAt(t, laid)),
	})

	cascade := &recordingCascade{workspaceCache: true}
	_, err := Import(context.Background(), rw, Options{
		SourcePath:    src.source,
		SourceName:    filepath.Base(src.source),
		Format:        src.format,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: cascade.resolve,
	})
	if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
		t.Fatalf("the import was not refused as a plugin no source holds: %v", err)
	}
	importers := importersResolved(cascade)
	if len(importers) != 1 || importers[0].uri != "importer/langflow" {
		t.Fatalf("the import resolved %+v; want exactly importer/langflow", importers)
	}
	if len(importers[0].colocated) != 0 {
		t.Errorf("the carried importer's resolution was handed the directories beside catalog entities %v",
			importers[0].colocated)
	}
}

// TestACoreImporterRunsOnlyFromTheModuleTheBinaryAccepts drives a workspace whose
// catalog holds the JSON parser and no importer, while the binary is made to carry
// importer/langflow — an entity generated from the reference Langflow importer —
// held to that importer's own digest, and a module for importer/langflow is put on
// this machine through the tier a module compiled into the binary is read from.
//
// Where the module is the one the digest names, the core importer runs and drafts
// the source, loaded under its own entity. Where it is another module — here the
// Flowise importer — it is refused before it runs, under the condition for bytes
// that are not the ones named, naming what the digest was held to.
//
// 🔴 PLANTED DEFECT: delete the HoldToDigest call in loadCarriedImporter
// (internal/importops/seam.go), and the second row fails: the other module runs.
func TestACoreImporterRunsOnlyFromTheModuleTheBinaryAccepts(t *testing.T) {
	// Not parallel: it seeds the process-wide carried set and the modules the
	// binary is read as carrying.
	src := catalogFolderSources[0]
	scratch := aWorkspaceDir(t)
	reference, _ := importopstest.LayImporters(t, scratch, filepath.Join(scratch, "catalog"),
		importopstest.Importer{Name: "langflow", Format: "langflow"},
		importopstest.Importer{Name: "flowise", Format: "flowise"})
	langflow, flowise := reference[0], reference[1]
	carry(t, embedded.PublishedPlugin{
		URI:    "importer/langflow",
		Entity: entityAt(t, langflow),
		Digest: digest.ContentDigest(moduleAt(t, langflow)),
	})

	for _, row := range []struct {
		name   string
		module []byte
		runs   bool
	}{
		{name: "the module the binary accepts", module: moduleAt(t, langflow), runs: true},
		{name: "another module", module: moduleAt(t, flowise)},
	} {
		t.Run(row.name, func(t *testing.T) {
			restore := embedded.SeedForTest("importer/langflow", row.module)
			defer restore()
			rw := parserOnlyWorkspace(t)
			cascade := &recordingCascade{workspaceCache: true}
			res, err := Import(context.Background(), rw, Options{
				SourcePath:    src.source,
				SourceName:    filepath.Base(src.source),
				Format:        src.format,
				Delivery:      authoredcontent.DeliveryFor(true),
				ResolvePlugin: cascade.resolve,
			})
			importers := importersResolved(cascade)
			if len(importers) != 1 || importers[0].uri != "importer/langflow" || importers[0].tier != pluginpath.TierEmbed {
				t.Fatalf("the import resolved %+v; want importer/langflow read from the binary's own tier", importers)
			}
			if !row.runs {
				if !errors.Is(err, oci.ErrFetchIntegrity()) {
					t.Fatalf("a module that is not the one the binary accepts was not refused as such: %v", err)
				}
				if !strings.Contains(err.Error(), carriedModuleIs) {
					t.Errorf("the refusal does not name what the module was held to: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if res.Halted() {
				t.Fatalf("the importer halted: %+v", res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, src.drafts) {
				t.Errorf("the importer drafted %v, want %v", got, src.drafts)
			}
		})
	}
}
