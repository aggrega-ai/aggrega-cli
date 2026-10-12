// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end coverage for an import whose importer — and the parser that
// importer calls back to — is a real Plugin entity authored in a catalog folder,
// with its built module laid down beside it.
//
// 🔴 Every case here runs the REAL seam: Import builds it through [BuildSeam]
// from the workspace's own catalog, and nothing below points that variable. The
// only thing a case supplies is the plugin-bytes resolver every caller supplies,
// and the one supplied here builds the shared cascade from nothing but the
// directories the seam hands it. So a module read from beside its entity can only
// have been found through the directories the catalog that selected it indexed —
// which is the claim — and a seam that stopped handing them over leaves the
// cascade nowhere to read the module from.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// catalogFolderSources are the two committed agent-flow fixtures and the files
// the reference importer of each one's format drafts from it under its default
// posture, in which every node is drafted. The paths are compared as a set: the
// importer's own order is the reported order, and these cases are not about it.
var catalogFolderSources = []struct {
	format string
	source string
	drafts []string
}{
	{
		format: "langflow",
		source: "../compiler/importersocket/testdata/langflow-flow.json",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-model-openaimodel.yaml",
			"components/imported-ai-tool-retrieval.yaml",
			"components/imported-component-custompythoncomponent.yaml",
		},
	},
	{
		format: "flowise",
		source: "../compiler/importersocket/testdata/flowise-flow.json",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-model-chatopenai.yaml",
			"components/imported-ai-tool-httprequest.yaml",
		},
	},
}

// cascadeResolution is one call the import made to the resolver it was handed.
type cascadeResolution struct {
	uri       string
	colocated map[string]pluginpath.ColocatedDir
	tier      pluginpath.Tier
}

// recordingCascade is a plugin-bytes resolver that builds the shared plugin
// cascade from the directories it is handed and nothing else — no workspace
// cache, no pins, no entity front — and records every call.
type recordingCascade struct {
	mu    sync.Mutex
	calls []cascadeResolution
}

func (r *recordingCascade) resolve(_ *workspaceconfig.ResolvedWorkspace, uri string,
	colocated map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
	cascade := pluginpath.NewResolver(pluginpath.ResolutionContext{ColocatedDirs: colocated},
		pluginpath.PinPostureNone, pluginpath.PinMaps{})
	resolved, err := cascade.Resolve(uri)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, cascadeResolution{uri: uri, colocated: colocated, tier: resolved.Tier})
	return resolved, err
}

func (r *recordingCascade) resolutions() []cascadeResolution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// catalogFolderImportWorkspace resolves a scratch workspace and lays the catalog
// folder down in its conventional catalog directory, with both modules beside
// their entities.
//
// The workspace is required to have resolved at the directory its configuration
// was written in, so the folder is laid inside the scratch tree and nowhere else.
func catalogFolderImportWorkspace(t *testing.T) (*workspaceconfig.ResolvedWorkspace, importopstest.CatalogFolder) {
	t.Helper()
	rw := aWorkspace(t)
	if _, err := os.Stat(filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)); err != nil {
		t.Fatalf("the workspace resolved at %s, which is not the directory its configuration was written in: %v",
			rw.Root(), err)
	}
	folder := importopstest.LayCatalogFolder(t, rw.Root(),
		filepath.Join(rw.Root(), workspaceconfig.ConventionalCatalogDir), true)
	return rw, folder
}

// catalogFolderImportWorkspaceAttaching is catalogFolderImportWorkspace whose
// configuration attaches each of the folder's importers, by its identity and
// version, with the settings given — so whichever importer a source's format
// selects is handed them.
func catalogFolderImportWorkspaceAttaching(t *testing.T, settings map[string]any) (*workspaceconfig.ResolvedWorkspace,
	importopstest.CatalogFolder) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	folder := importopstest.LayCatalogFolder(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), true)
	attachments := make([]importopstest.Attachment, 0, len(folder.Importers))
	for _, imp := range folder.Importers {
		attachments = append(attachments, importopstest.Attachment{URI: imp.Identity, Settings: settings})
	}
	importopstest.WriteConfiguration(t, wsDir, attachments...)
	rw := resolveWorkspaceAt(t, wsDir)
	if _, err := os.Stat(filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)); err != nil {
		t.Fatalf("the workspace resolved at %s, which is not the directory its configuration was written in: %v",
			rw.Root(), err)
	}
	return rw, folder
}

// draftPaths returns the drafted paths a result carries, sorted.
func draftPaths(res Result) []string {
	paths := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	slices.Sort(paths)
	return paths
}

// TestImportRunsAnImporterFromTheCatalogFolderItsEntitySitsIn drives Import over a
// workspace whose catalog folder holds the reference importer of each format and
// a JSON parser, each with its module beside its entity, for a Langflow and a
// Flowise source.
//
// Three claims per source, and none stands in for another. The drafted files are
// the end-to-end one: the importer of the source's format ran, and so did the
// parser it calls back to, since the drafts are built from the tree the parser
// returns. Each module being read at the beside-the-entity tier is the location
// claim: there is nowhere else this cascade could have read either from. And the
// directories every call was handed being the ones the catalog indexed — each
// entity's own directory, held to the workspace root — is what says the seam
// handed over the catalog's answer rather than one of its own, for every entity
// the folder holds alike.
//
// 🔴 PLANTED DEFECT: hand the resolver nil instead of the catalog's directories in
// buildSeam (internal/importops/seam.go, the resolvePlugin call in the load
// closure) and every row fails: the cascade has nowhere to read the importer from
// and the import is refused naming a plugin no source holds.
func TestImportRunsAnImporterFromTheCatalogFolderItsEntitySitsIn(t *testing.T) {
	t.Parallel()

	for _, src := range catalogFolderSources {
		t.Run(src.format, func(t *testing.T) {
			t.Parallel()
			rw, folder := catalogFolderImportWorkspace(t)
			source, err := os.ReadFile(src.source)
			if err != nil {
				t.Fatalf("read the committed source fixture: %v", err)
			}

			cascade := &recordingCascade{}
			res, err := Import(context.Background(), rw, Options{
				Source:        source,
				SourceName:    filepath.Base(src.source),
				Delivery:      authoredcontent.DeliveryFor(true),
				ResolvePlugin: cascade.resolve,
			})
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if res.Halted() {
				t.Fatalf("the importer halted: %+v", res.Diagnostics)
			}
			if res.Format != src.format {
				t.Errorf("the source was read as the %q format, want %q", res.Format, src.format)
			}
			if got := draftPaths(res); !slices.Equal(got, src.drafts) {
				t.Errorf("the importer drafted %v, want %v", got, src.drafts)
			}
			for _, f := range res.Files {
				if f.Path == "aggregate.yaml" && !strings.Contains(f.Content, "kind: Aggregate") {
					t.Errorf("aggregate.yaml is not an Aggregate:\n%s", f.Content)
				}
			}

			calls := cascade.resolutions()
			for _, want := range []importopstest.LaidPlugin{folder.ImporterFor(t, src.format), folder.Parser} {
				i := slices.IndexFunc(calls, func(c cascadeResolution) bool { return c.uri == want.Reference })
				if i < 0 {
					t.Fatalf("the import never resolved %s; it resolved %v", want.Reference, calls)
				}
				if calls[i].tier != pluginpath.TierColocated {
					t.Errorf("%s was read at the %q tier, want the one beside its entity", want.Reference, calls[i].tier)
				}
			}
			for _, call := range calls {
				for _, laid := range folder.Plugins() {
					at, ok := call.colocated[laid.Reference]
					if !ok {
						t.Errorf("resolving %s was handed no directory for %s; the catalog indexed %s",
							call.uri, laid.Reference, laid.EntityDir)
						continue
					}
					if at.Dir != laid.EntityDir {
						t.Errorf("resolving %s was handed %s as the directory beside %s; the entity sits in %s",
							call.uri, at.Dir, laid.Reference, laid.EntityDir)
					}
					if at.Root != rw.Root() {
						t.Errorf("resolving %s was handed %q as the root a read beside %s is held to; the catalog "+
							"directory sits in the workspace %s", call.uri, at.Root, laid.Reference, rw.Root())
					}
				}
			}
		})
	}
}

// TestImportWritesTheDraftsAnImporterFromTheCatalogFolderProduced is the writing
// delivery over the same folder: what the importer drafted is on disk beneath the
// output directory, carrying the content the import reported, and the output
// directory holds nothing else.
func TestImportWritesTheDraftsAnImporterFromTheCatalogFolderProduced(t *testing.T) {
	t.Parallel()
	rw, _ := catalogFolderImportWorkspace(t)
	src := catalogFolderSources[0]
	source, err := os.ReadFile(src.source)
	if err != nil {
		t.Fatalf("read the committed source fixture: %v", err)
	}
	outputDir := filepath.Join(rw.Root(), "drafts")

	cascade := &recordingCascade{}
	res, err := Import(context.Background(), rw, Options{
		Source:        source,
		SourceName:    filepath.Base(src.source),
		OutputDir:     outputDir,
		ResolvePlugin: cascade.resolve,
	})
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	written := slices.Clone(res.Written)
	slices.Sort(written)
	if !slices.Equal(written, src.drafts) {
		t.Fatalf("the import names %v as written, want %v", written, src.drafts)
	}
	for _, f := range res.Files {
		got, rerr := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(f.Path)))
		if rerr != nil {
			t.Fatalf("%s is not beneath the output directory: %v", f.Path, rerr)
		}
		if string(got) != f.Content {
			t.Errorf("%s on disk differs from the content the import reported", f.Path)
		}
	}
	var onDisk []string
	if werr := filepath.WalkDir(outputDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(outputDir, path)
		onDisk = append(onDisk, filepath.ToSlash(rel))
		return rerr
	}); werr != nil {
		t.Fatalf("walk the output directory: %v", werr)
	}
	slices.Sort(onDisk)
	if !slices.Equal(onDisk, src.drafts) {
		t.Errorf("the output directory holds %v, want exactly %v", onDisk, src.drafts)
	}
}

// TestImportRefusesADraftWhosePathALinkLeadsOutOfTheWorkspace is the drafts'
// containment judged THROUGH Import, with the real seam and the real importer:
// a real symbolic link standing in the output directory, on the way to a path
// the importer drafts, leads out of the workspace — so the import is refused and
// nothing is written on either side of the link, under either delivery.
//
// The link is on the way to the drafted components rather than at the output
// directory itself, and the importer also drafts a file that does not pass
// through it, so a containment judged per file as it is written would leave that
// one on disk: nothing being written beside the link is what says every path was
// judged before the first write.
//
// 🔴 PLANTED DEFECT: delete the checkFiles call in Import
// (internal/importops/import.go) and both rows redden, each on the refusal it no
// longer meets — and the writing row, past that, has written the drafted
// components through the link into the directory outside the workspace and
// aggregate.yaml beside the link.
func TestImportRefusesADraftWhosePathALinkLeadsOutOfTheWorkspace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		dryRun bool
	}{
		{"writing the drafts", false},
		{"reporting the drafts", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw, _ := catalogFolderImportWorkspace(t)
			source, err := os.ReadFile(catalogFolderSources[0].source)
			if err != nil {
				t.Fatalf("read the committed source fixture: %v", err)
			}

			outside := t.TempDir()
			outputDir := filepath.Join(rw.Root(), "drafts")
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				t.Fatalf("create the output directory: %v", err)
			}
			if err := os.Symlink(outside, filepath.Join(outputDir, "components")); err != nil {
				t.Fatalf("lay the link: %v", err)
			}

			_, err = Import(context.Background(), rw, Options{
				Source:        source,
				SourceName:    "flow.json",
				OutputDir:     outputDir,
				Delivery:      authoredcontent.DeliveryFor(tc.dryRun),
				ResolvePlugin: (&recordingCascade{}).resolve,
			})
			if err == nil {
				t.Fatal("an import whose drafted path leads through a link out of the workspace was not refused")
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("the import was refused, but not as a path leading out of the workspace: %v", err)
			}
			if entries, rerr := os.ReadDir(outside); rerr != nil || len(entries) != 0 {
				t.Errorf("the directory the link leads to holds %v (read error %v); nothing may be written there",
					entries, rerr)
			}
			entries, rerr := os.ReadDir(outputDir)
			if rerr != nil {
				t.Fatalf("read the output directory: %v", rerr)
			}
			if len(entries) != 1 || entries[0].Name() != "components" {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("the output directory holds %v; a refused import leaves only the link in it", names)
			}
		})
	}
}
