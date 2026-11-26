// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the catalog an import reads in a workspace that names
// catalog folders: the build's resolution order, the workspace's folders first
// and the catalog its default registry publishes as one signed snapshot last,
// first-win by directory. A folder's entity shadows the published one of its
// identity, every other published entity stays in view — for the projection, the
// importers, the Recipes a target's Manifest is drafted from and the target's own
// check alike — and the record beside the drafts names the snapshot read. Where
// the snapshot cannot be read, a workspace with folders imports over them alone
// with one warning saying why, and a workspace with none is refused as before.
//
// Every case runs the REAL seam over a registry of its own on loopback that counts
// the requests it answers (snapshottest.Registry), as the cases beside this file
// do for a workspace naming no folder. The snapshot most cases publish is the
// draft-composition catalog with the draft-set importer beside it
// (publishedSnapshot), so an import for the compose target over it drafts the
// Manifest mapping example/x, which only that catalog's Recipes supply.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/snapshottest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// layeredLocalComponent is a Component only a workspace's catalog folder holds:
// no snapshot published here carries its identity.
const layeredLocalComponent = "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Component
metadata:
  name: example/local
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`

// shadowingComponent is example/x at the identity the draft-composition catalog
// publishes it at, labelled as a worker where the published one is a server, so
// the projection says which of the two the import read.
const shadowingComponent = "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Component
metadata:
  name: example/x
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "worker"
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`

// shadowingRecipe is the compose Recipe y-two at the identity the
// draft-composition catalog publishes it at, implementing example/z where the
// published one implements example/y: read in place of the published one, it
// leaves example/y one compose Recipe and gives example/z one.
const shadowingRecipe = "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Recipe
metadata:
  name: y-two
  version: "1.0.0"
spec:
  capability: compose
  implements: example/z
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: y-two:1.0.0
        ports:
          web: {{ optional .infra ` + "`port`" + ` 8080 }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ optional .infra ` + "`port`" + ` 8080 }}
`

// shadowedManifest is the Manifest an import for the compose target drafts over
// the draft-composition catalog published behind a folder holding
// shadowingRecipe: example/x mapped to the published x-server, example/y to the
// one published compose Recipe the folder leaves it, and example/z to the
// folder's y-two.
const shadowedManifest = "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Manifest
metadata:
  name: drafted
  version: 0.1.0
spec:
  mappings:
    components:
      type:
        example/x:1.0.0: compose/x-server
        example/y:1.0.0: compose/y-one
        example/z:1.0.0: compose/y-two
`

// foldersAloneWords are the words every warning of an import reading its
// workspace's catalog folders alone carries.
const foldersAloneWords = "reads the workspace's catalog folders alone"

// writeCatalogFile writes body at rel beneath the catalog folder dir.
func writeCatalogFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the directory for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// writeWorkspaceConfiguration writes body as the configuration of the workspace
// rooted at wsDir.
func writeWorkspaceConfiguration(t *testing.T, wsDir, body string) {
	t.Helper()
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
}

// withWorkspaceBlock replaces the empty workspace block a configuration body
// opens with by block, failing the test where the body carries none.
func withWorkspaceBlock(t *testing.T, body, block string) string {
	t.Helper()
	const empty = "workspace: {}\n"
	if strings.Count(body, empty) != 1 {
		t.Fatalf("the configuration does not carry one empty workspace block:\n%s", body)
	}
	return strings.Replace(body, empty, block, 1)
}

// folderNaming is one way a workspace names a catalog folder: resolve writes
// config, the configuration naming the case's registry, at wsDir with the folder
// named, creates the folder, and returns it and the workspace resolved.
type folderNaming struct {
	name    string
	resolve func(t *testing.T, wsDir, config string) (string, *workspaceconfig.ResolvedWorkspace)
}

// folderNamings are the three ways a workspace names a catalog folder: in its
// configuration, in the environment, and at the conventional place in its root.
var folderNamings = []folderNaming{
	{
		name: "catalog_dirs in the configuration",
		resolve: func(t *testing.T, wsDir, config string) (string, *workspaceconfig.ResolvedWorkspace) {
			dir := filepath.Join(t.TempDir(), "folder")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeWorkspaceConfiguration(t, wsDir, withWorkspaceBlock(t, config,
				"workspace:\n  catalog_dirs: ["+strconv.Quote(dir)+"]\n"))
			return dir, resolveWorkspaceAt(t, wsDir)
		},
	},
	{
		name: "AGGREGA_CATALOG_DIRS",
		resolve: func(t *testing.T, wsDir, config string) (string, *workspaceconfig.ResolvedWorkspace) {
			dir := filepath.Join(t.TempDir(), "folder")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeWorkspaceConfiguration(t, wsDir, config)
			return dir, resolveWorkspaceAtWith(t, wsDir, map[string]string{"AGGREGA_CATALOG_DIRS": dir})
		},
	},
	{
		name: "the conventional catalog folder",
		resolve: func(t *testing.T, wsDir, config string) (string, *workspaceconfig.ResolvedWorkspace) {
			dir := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeWorkspaceConfiguration(t, wsDir, config)
			return dir, resolveWorkspaceAt(t, wsDir)
		},
	},
}

// conventionalFolder is the folder naming at the conventional place.
var conventionalFolder = folderNamings[2]

// layeredView builds the real seam over rw and reads the snapshot the workspace
// holds behind its folders, returning the seam the import would read: its
// projection, its importers and its Recipes.
func layeredView(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) Seam {
	t.Helper()
	seam, err := buildSeam(rw, (&recordingCascade{}).resolve)
	if err != nil {
		t.Fatalf("build the import's seam: %v", err)
	}
	if seam.readSnapshot == nil {
		t.Fatal("the seam of a workspace naming catalog folders carries no step reading the published catalog")
	}
	over, _, err := seam.readSnapshot(context.Background(), catalogSnapshotRead{})
	if err != nil {
		t.Fatalf("read the published catalog behind the folders: %v", err)
	}
	return over
}

// projectedComponent returns the projection's entry for name, failing the test
// where it holds none.
func projectedComponent(t *testing.T, projection *abi.CatalogProjection, name string) abi.CatalogComponent {
	t.Helper()
	if projection != nil {
		for _, c := range projection.Components {
			if c.Name == name {
				return c
			}
		}
	}
	t.Fatalf("the projection holds no %s", name)
	return abi.CatalogComponent{}
}

// warningsCarrying returns the message of each warning among diags carrying
// words.
func warningsCarrying(diags []abi.Diagnostic, words string) []string {
	var out []string
	for _, d := range diags {
		if d.Severity == abi.SeverityWarn && strings.Contains(d.Message, words) {
			out = append(out, d.Message)
		}
	}
	return out
}

// closedRegistryHost returns a loopback host and port nothing listens on: a
// registry that cannot be reached.
func closedRegistryHost(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	return host
}

// TestLayeredCatalogShowsPublishedBehindFolders imports for the compose target in
// a workspace that names a catalog folder — in its configuration, in
// AGGREGA_CATALOG_DIRS, or at the conventional place in its root — holding one
// Component of its own, example/local, over a registry publishing the
// draft-composition catalog with the draft-set importer: the import reads the
// published catalog behind the folder. The draft-set importer runs from it, the
// target is bound by its Recipes and the Manifest maps example/x to the one its
// compose Recipe gives; the record names the snapshot; the warnings are the
// composition's alone; and the seam's view holds the folder's Component beside the
// published ones.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), leave readSnapshot
// unset where the stack names a folder: every row is refused, no Recipe binding to
// compose.
func TestLayeredCatalogShowsPublishedBehindFolders(t *testing.T) {
	t.Parallel()
	for _, naming := range folderNamings {
		t.Run(naming.name, func(t *testing.T) {
			t.Parallel()
			f := publishedSnapshot(t)
			folder, rw := naming.resolve(t, aWorkspaceDir(t), f.reg.WorkspaceConfiguration(f.pub, false))
			writeCatalogFile(t, folder, "components/local.yaml", layeredLocalComponent)

			res, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if f.reg.Requests() == 0 {
				t.Error("the registry answered no request, so the published catalog cannot have been read")
			}
			if got := fileNamed(t, res, ManifestFileName); got != importopstest.DraftedManifestComposed {
				t.Errorf("the drafted Manifest is\n%s\nwant the one the published Recipes give\n%s", got,
					importopstest.DraftedManifestComposed)
			}
			if !slices.Equal(res.Diagnostics, wantComposedWarnings) {
				t.Errorf("the import reported %+v\nwant the composition's warnings alone %+v", res.Diagnostics,
					wantComposedWarnings)
			}
			if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
				t.Errorf("the record names the catalog snapshot %q; want %q", got, f.digest)
			}

			view := layeredView(t, rw)
			var names []string
			for _, c := range view.Catalog.Components {
				names = append(names, c.Name)
			}
			if want := []string{"example/local", "example/x", "example/y", "example/z"}; !slices.Equal(names, want) {
				t.Errorf("the projection holds %v; want the folder's Component beside the published ones %v", names, want)
			}
			if !slices.ContainsFunc(view.Importers, func(p types.Plugin) bool {
				return slices.Contains(catalog.ImporterFormats(p), importopstest.DraftSetFormat)
			}) {
				t.Error("the view's importers hold none advertising the draft-set format the snapshot publishes")
			}
			if got := view.Recipes.RecipeCapabilities(); !slices.Contains(got, "compose") || !slices.Contains(got, "helm") {
				t.Errorf("the view's Recipes bind to %v; want the published compose and helm Recipes", got)
			}
		})
	}
}

// TestLayeredCatalogFolderShadowsPublished imports for the compose target in a
// workspace whose catalog folder holds example/x and the compose Recipe y-two at
// the identities the published draft-composition catalog holds them at — the
// Component labelled a worker, the Recipe implementing example/z: the folder's
// copies are read and the published ones are not, and every other published
// entity stays in view. So the Manifest maps example/x to the published
// x-server, example/y to the one compose Recipe left implementing it and example/z
// to the folder's y-two, nothing is left unmapped, and the projection's example/x
// is the folder's worker.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), put the snapshot's
// folder first in the stack it loads: the published copies win, the Manifest is
// the unshadowed one and the projection's example/x is a server.
func TestLayeredCatalogFolderShadowsPublished(t *testing.T) {
	t.Parallel()
	f := publishedSnapshot(t)
	folder, rw := conventionalFolder.resolve(t, aWorkspaceDir(t), f.reg.WorkspaceConfiguration(f.pub, false))
	writeCatalogFile(t, folder, "components/x.yaml", shadowingComponent)
	writeCatalogFile(t, folder, "recipes/compose/y-two.yaml", shadowingRecipe)

	res, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if got := fileNamed(t, res, ManifestFileName); got != shadowedManifest {
		t.Errorf("the drafted Manifest is\n%s\nwant the one the folder's Recipe and the published ones give\n%s", got,
			shadowedManifest)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("the import reported %+v; want nothing, every type mapped", res.Diagnostics)
	}
	if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
		t.Errorf("the record names the catalog snapshot %q; want %q", got, f.digest)
	}
	if got := projectedComponent(t, layeredView(t, rw).Catalog, "example/x").Labels["component.aggrega.ai/type"]; got != "worker" {
		t.Errorf("the projection's example/x is a %q; want the folder's worker", got)
	}
}

// TestLayeredCatalogOfflineWithFoldersWarns imports for the compose target in a
// workspace whose conventional catalog folder holds the draft-composition catalog
// and the draft-set importer, where the published catalog cannot be read: the
// workspace is offline and this machine holds no snapshot, it names no default
// registry, its registry publishes none, or its registry cannot be reached. Each
// imports over the folder alone — the Manifest the folder's Recipes give — with
// one warning, ahead of the composition's, saying why and what reads the
// published catalog; the record names no snapshot; and an offline workspace asks
// its registry nothing.
//
// 🔴 PLANTED DEFECT: in fetchCatalogSnapshot (internal/importops/snapshot.go),
// refuse an offline import whatever the workspace names: the offline row is
// refused.
func TestLayeredCatalogOfflineWithFoldersWarns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		publish   bool
		config    func(t *testing.T, f snapshotFixture) string
		asksNoOne bool
		want      []string
	}{
		{
			name: "offline with no snapshot on the machine", publish: true, asksNoOne: true,
			config: func(t *testing.T, f snapshotFixture) string { return f.reg.WorkspaceConfiguration(f.pub, true) },
			want: []string{"holds no catalog snapshot for the workspace",
				"the offline posture is set by `offline: true` in", "run the import once with the network available"},
		},
		{
			name: "no default registry", publish: true, asksNoOne: true,
			config: func(*testing.T, snapshotFixture) string { return "workspace: {}\ndefault_registry: \"\"\n" },
			want:   []string{"names no default registry", "set default_registry"},
		},
		{
			name:   "a registry publishing none",
			config: func(t *testing.T, f snapshotFixture) string { return f.reg.WorkspaceConfiguration(f.pub, false) },
			want:   []string{"publishes no catalog snapshot"},
		},
		{
			name: "a registry that cannot be reached",
			config: func(t *testing.T, _ snapshotFixture) string {
				return "workspace: {}\ndefault_registry: " + strconv.Quote(closedRegistryHost(t)) + "\n"
			},
			want: []string{"could not be reached", "run the import again with the network available"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
			f.signer, f.pub = snapshottest.NewKey(t)
			if tc.publish {
				f.digest = f.reg.Publish(t, snapshotTree(t), catalog.SnapshotLatestTag)
				f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
			}
			wsDir := aWorkspaceDir(t)
			folder, rw := conventionalFolder.resolve(t, wsDir, tc.config(t, f))
			importopstest.LayDraftComposerCatalog(t, wsDir, folder)

			before := f.reg.Requests()
			res, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
			if err != nil {
				t.Fatalf("the import over the folder was refused: %v", err)
			}
			if got := fileNamed(t, res, ManifestFileName); got != importopstest.DraftedManifestComposed {
				t.Errorf("the drafted Manifest is\n%s\nwant the one the folder's Recipes give\n%s", got,
					importopstest.DraftedManifestComposed)
			}
			warned := warningsCarrying(res.Diagnostics, foldersAloneWords)
			if len(warned) != 1 {
				t.Fatalf("the import reported %d warnings of reading the folders alone; want one: %+v", len(warned),
					res.Diagnostics)
			}
			for _, part := range tc.want {
				if !strings.Contains(warned[0], part) {
					t.Errorf("the warning does not carry %q: %s", part, warned[0])
				}
			}
			if len(res.Diagnostics) < 1 || res.Diagnostics[0].Message != warned[0] ||
				!slices.Equal(res.Diagnostics[1:], wantComposedWarnings) {
				t.Errorf("the import reported %+v; want the warning followed by the composition's %+v",
					res.Diagnostics, wantComposedWarnings)
			}
			if got := recordIn(t, res).CatalogSnapshot; got != "" {
				t.Errorf("the record names the catalog snapshot %q, though none was read", got)
			}
			if after := f.reg.Requests(); tc.asksNoOne && after != before {
				t.Errorf("the import asked the registry %d requests", after-before)
			}
		})
	}
}

// TestLayeredCatalogOfflineWithoutFoldersRefused imports in a workspace that names
// no catalog folder where the published catalog cannot be read — offline with no
// snapshot on the machine, no default registry, a registry that cannot be reached
// — and holds each refusal to its condition, naming what to run: with no folder,
// there is no catalog to import over.
func TestLayeredCatalogOfflineWithoutFoldersRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		config  func(t *testing.T, f snapshotFixture) string
		offline bool
		want    []string
	}{
		{
			name: "offline with no snapshot on the machine", offline: true,
			config: func(t *testing.T, f snapshotFixture) string { return f.reg.WorkspaceConfiguration(f.pub, true) },
			want:   []string{"holds no catalog snapshot", "run the import once with the network available"},
		},
		{
			name:   "no default registry",
			config: func(*testing.T, snapshotFixture) string { return "workspace: {}\ndefault_registry: \"\"\n" },
			want:   []string{"names no catalog folder and no default registry", "set default_registry"},
		},
		{
			name: "a registry that cannot be reached",
			config: func(t *testing.T, _ snapshotFixture) string {
				return "workspace: {}\ndefault_registry: " + strconv.Quote(closedRegistryHost(t)) + "\n"
			},
			want: []string{"could not be reached", "run the import again with the network available"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := publishedSnapshot(t)
			wsDir := aWorkspaceDir(t)
			writeWorkspaceConfiguration(t, wsDir, tc.config(t, f))
			rw := resolveWorkspaceAt(t, wsDir)
			before := f.reg.Requests()
			_, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
			if err == nil {
				t.Fatal("the import with no folder and no readable published catalog was not refused")
			}
			if tc.offline && !errors.Is(err, egress.ErrOfflineEgress()) {
				t.Errorf("the import was not refused as offline: %v", err)
			}
			for _, part := range tc.want {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("the refusal does not carry %q: %v", part, err)
				}
			}
			if after := f.reg.Requests(); after != before {
				t.Errorf("the import asked the published catalog's registry %d requests", after-before)
			}
		})
	}
}

// TestLayeredCatalogEmptyCatalogDirsReadsNoFolder imports in a workspace whose
// configuration writes catalog_dirs as an empty list while its conventional
// catalog folder exists: the list says the workspace has no local catalog, so the
// folder is not read. Online, the import reads the published catalog alone — the
// unshadowed Manifest and a projection whose example/x is the published server,
// though the folder holds shadowing copies; offline with no snapshot on the
// machine, it is refused as a workspace naming no folder is, though the folder
// holds a catalog the import could have read.
func TestLayeredCatalogEmptyCatalogDirsReadsNoFolder(t *testing.T) {
	t.Parallel()
	const noLocalCatalog = "workspace:\n  catalog_dirs: []\n"

	t.Run("online", func(t *testing.T) {
		t.Parallel()
		f := publishedSnapshot(t)
		wsDir := aWorkspaceDir(t)
		writeWorkspaceConfiguration(t, wsDir, withWorkspaceBlock(t, f.reg.WorkspaceConfiguration(f.pub, false),
			noLocalCatalog))
		folder := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
		writeCatalogFile(t, folder, "components/x.yaml", shadowingComponent)
		writeCatalogFile(t, folder, "recipes/compose/y-two.yaml", shadowingRecipe)
		rw := resolveWorkspaceAt(t, wsDir)

		res, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
		if err != nil {
			t.Fatalf("the import was refused: %v", err)
		}
		if got := fileNamed(t, res, ManifestFileName); got != importopstest.DraftedManifestComposed {
			t.Errorf("the drafted Manifest is\n%s\nwant the published catalog's alone\n%s", got,
				importopstest.DraftedManifestComposed)
		}
		if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
			t.Errorf("the record names the catalog snapshot %q; want %q", got, f.digest)
		}
		seam, err := buildSeam(rw, (&recordingCascade{}).resolve)
		if err != nil {
			t.Fatalf("build the import's seam: %v", err)
		}
		over, _, err := seam.readSnapshot(context.Background(), catalogSnapshotRead{})
		if err != nil {
			t.Fatalf("read the published catalog: %v", err)
		}
		if got := projectedComponent(t, over.Catalog, "example/x").Labels["component.aggrega.ai/type"]; got != "server" {
			t.Errorf("the projection's example/x is a %q; want the published server", got)
		}
	})

	t.Run("offline with no snapshot on the machine", func(t *testing.T) {
		t.Parallel()
		f := publishedSnapshot(t)
		wsDir := aWorkspaceDir(t)
		writeWorkspaceConfiguration(t, wsDir, withWorkspaceBlock(t, f.reg.WorkspaceConfiguration(f.pub, true),
			noLocalCatalog))
		importopstest.LayDraftComposerCatalog(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir))
		rw := resolveWorkspaceAt(t, wsDir)

		_, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
		if !errors.Is(err, egress.ErrOfflineEgress()) || !strings.Contains(err.Error(), "holds no catalog snapshot") {
			t.Fatalf("the import was not refused as an offline workspace naming no folder: %v", err)
		}
	})
}

// TestLayeredCatalogPinsSnapshotInRecord holds an import in a workspace naming a
// catalog folder to the snapshot rule a workspace naming none follows: a first
// import fetches the newest snapshot, pins it in its record and makes it the one
// the workspace reads; once a second is published, an import into another
// directory reads the first with no request, so does one beside the first record
// and a sync of the first import's drafts, which finds no drift; an import with
// Upgrade reads the second and pins it, and the workspace reads it from then on;
// and an import beside the first record still reads the first, as its record names.
//
// 🔴 PLANTED DEFECT: in Import (internal/importops/import.go), record no snapshot
// for a workspace naming folders (pass "" to recordScaffold): the first row fails.
func TestLayeredCatalogPinsSnapshotInRecord(t *testing.T) {
	t.Parallel()
	f := publishedSnapshot(t)
	folder, rw := conventionalFolder.resolve(t, aWorkspaceDir(t), f.reg.WorkspaceConfiguration(f.pub, false))
	writeCatalogFile(t, folder, "components/local.yaml", layeredLocalComponent)

	first := snapshotImportOptions(t, authoredcontent.DeliveryWrite)
	res, err := Import(context.Background(), rw, first)
	if err != nil {
		t.Fatalf("the first import was refused: %v", err)
	}
	if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
		t.Fatalf("the first import's record names the catalog snapshot %q; want %q", got, f.digest)
	}
	if got := currentSnapshot(t, rw); got != f.digest {
		t.Fatalf("the workspace reads the snapshot %q; want %q", got, f.digest)
	}
	record, err := os.ReadFile(filepath.Join(first.OutputDir, RecordFileName))
	if err != nil {
		t.Fatalf("the first import wrote no record: %v", err)
	}

	newer := snapshotTree(t)
	importopstest.WriteSource(t, filepath.Join(newer, "notes.txt"), "a file the first snapshot does not carry\n")
	second := f.reg.Publish(t, newer, catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
	if second == f.digest {
		t.Fatal("the second snapshot has the first one's digest, so nothing here could tell them apart")
	}

	besideTheRecord := func(t *testing.T) Options {
		o := snapshotImportOptions(t, authoredcontent.DeliveryReport)
		if err := os.WriteFile(filepath.Join(o.OutputDir, RecordFileName), record, 0o644); err != nil {
			t.Fatal(err)
		}
		return o
	}
	// pinOf imports under o and returns the snapshot the record it leaves names —
	// the one it hands back, or, for a sync that hands back none, the one beside
	// the drafts — and the requests the registry answered for it.
	pinOf := func(t *testing.T, o Options) (string, []string) {
		t.Helper()
		before := f.reg.Requests()
		res, err := Import(context.Background(), rw, o)
		if err != nil {
			t.Fatalf("the import was refused: %v", err)
		}
		if o.Sync && len(res.Drift) != 0 {
			t.Errorf("the sync found drift %+v over drafts imported against the same snapshot", res.Drift)
		}
		asked := f.reg.Asked(before)
		for _, file := range res.Files {
			if file.Path == RecordFileName {
				return recordIn(t, res).CatalogSnapshot, asked
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
		return rec.sole().CatalogSnapshot, asked
	}

	for _, tc := range []struct {
		name      string
		opts      func(t *testing.T) Options
		want      string
		asksNoOne bool
	}{
		{
			name: "an import into another directory", want: f.digest, asksNoOne: true,
			opts: func(t *testing.T) Options { return snapshotImportOptions(t, authoredcontent.DeliveryReport) },
		},
		{name: "an import beside the first record", want: f.digest, asksNoOne: true, opts: besideTheRecord},
		{
			name: "a sync of the first import's drafts", want: f.digest, asksNoOne: true,
			opts: func(*testing.T) Options {
				o := first
				o.Sync, o.Delivery = true, authoredcontent.DeliveryReport
				return o
			},
		},
		{
			name: "an import with upgrade", want: second,
			opts: func(t *testing.T) Options {
				o := snapshotImportOptions(t, authoredcontent.DeliveryReport)
				o.Upgrade = true
				return o
			},
		},
		{
			name: "an import into another directory after the upgrade", want: second, asksNoOne: true,
			opts: func(t *testing.T) Options { return snapshotImportOptions(t, authoredcontent.DeliveryReport) },
		},
		{name: "an import beside the first record after the upgrade", want: f.digest, asksNoOne: true,
			opts: besideTheRecord},
	} {
		got, asked := pinOf(t, tc.opts(t))
		if got != tc.want {
			t.Errorf("%s reads the snapshot %q; want %q", tc.name, got, tc.want)
		}
		if tc.asksNoOne && len(asked) != 0 {
			t.Errorf("%s asked the registry %v; want nothing, the snapshot being held", tc.name, asked)
		}
	}
	if got := currentSnapshot(t, rw); got != second {
		t.Errorf("after the upgrade the workspace reads the snapshot %q; want %q", got, second)
	}
}

// composeTargetRecipes are the hub's compose Recipes implementing the Components
// an import of the authored Compose application references, by their path
// beneath platformHub.
var composeTargetRecipes = []string{
	"recipe/compose/postgres/1.0.0/recipe.yaml",
	"recipe/compose/redis/1.0.0/recipe.yaml",
	"recipe/compose/server/1.0.0/recipe.yaml",
}

// draftedQueueComponent is a Component a draft of a catalog entity writes into
// the workspace's catalog folder: one no Compose service is matched against.
const draftedQueueComponent = "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Component
metadata:
  name: example/queue
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "resource"
spec:
  schema:
    infra:
      retention_days: { type: integer, default: 4 }
`

// TestComposeSyncKeepsTargetRecipesAfterCatalogFolderAppears imports the authored
// Compose application for the compose target in a workspace naming no catalog
// folder, over a registry publishing a catalog that holds the reference Compose
// importer, the hub's Components a Compose service is matched against and their
// compose Recipes: the Manifest maps them. Then a catalog folder appears at the
// workspace's root, holding a Component a draft of a catalog entity wrote, and
// the first import is synced: the published Recipes stay in view behind the
// folder, so the recorded target is still bound and the sync finds the drafts in
// step with their source — no drift, nothing written or removed.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), leave readSnapshot
// unset where the stack names a folder: the sync is refused as a recorded target no
// Recipe binds to.
func TestComposeSyncKeepsTargetRecipesAfterCatalogFolderAppears(t *testing.T) {
	t.Parallel()
	tree := filepath.Join(t.TempDir(), "catalog")
	laid, _ := importopstest.LayImporters(t, filepath.Dir(tree), tree,
		importopstest.Importer{Name: "import-compose", Format: "compose"})
	layHubEntities(t, tree, append(slices.Clone(composeHubEntities), composeTargetRecipes...))
	f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
	f.signer, f.pub = snapshottest.NewKey(t)
	f.digest = f.reg.Publish(t, tree, catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)

	wsDir := aWorkspaceDir(t)
	// The binary's own Compose importer advertises the format too, so the
	// published one is attached by its identity.
	writeWorkspaceConfiguration(t, wsDir, f.reg.WorkspaceConfiguration(f.pub, false)+
		"plugins:\n  importer:\n    - uri: "+strconv.Quote(laid[0].Identity)+"\n")
	rw := resolveWorkspaceAt(t, wsDir)

	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "compose.yaml"),
		string(readComposeFixture(t, "authored/compose.yaml")))
	app := Options{
		SourcePath:    source,
		SourceName:    "compose.yaml",
		Target:        "compose",
		OutputDir:     filepath.Join(wsDir, "app"),
		Delivery:      authoredcontent.DeliveryWrite,
		ResolvePlugin: (&recordingCascade{}).resolve,
	}
	first, err := Import(context.Background(), rw, app)
	if err != nil {
		t.Fatalf("the app import was refused: %v", err)
	}
	if first.Format != "compose" || first.Halted() {
		t.Fatalf("the source was read as %q, halted %v: %+v", first.Format, first.Halted(), first.Diagnostics)
	}
	manifest := fileNamed(t, first, ManifestFileName)
	for _, want := range []string{": compose/postgres\n", ": compose/redis\n", ": compose/server\n"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the drafted Manifest does not map %q from the published Recipes:\n%s", want, manifest)
		}
	}
	if got := recordIn(t, first).CatalogSnapshot; got != f.digest {
		t.Fatalf("the app import's record names the catalog snapshot %q; want %q", got, f.digest)
	}

	writeCatalogFile(t, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir),
		"component/example/queue/1.0.0/component.yaml", draftedQueueComponent)
	rw = resolveWorkspaceAt(t, wsDir)

	sync := app
	sync.Target, sync.Sync = "", true
	res, err := Import(context.Background(), rw, sync)
	if err != nil {
		t.Fatalf("the sync after a catalog folder appeared was refused: %v", err)
	}
	if len(res.Drift) != 0 || len(res.Removed) != 0 || len(res.Written) != 0 {
		t.Errorf("the sync found drift %+v, removed %v and wrote %v; want the drafts in step with their source",
			res.Drift, res.Removed, res.Written)
	}
}

// TestLayeredCatalogCoreImporterStaysCarried drives a workspace whose catalog
// folder holds the JSON parser, over a registry publishing a catalog that holds a
// Plugin entity of importer/langflow with no module beside it — at the version
// the binary carries the core importer at, and at a later one — while the binary
// carries importer/langflow, held to the digest of a module the workspace's
// content cache holds.
//
// The published entity cannot run: nothing is beside it. So in the layered view
// it is no candidate, as it is in a folder, and the carried importer runs, read
// through the cascade the import builds for a core importer — the caller's
// resolver is asked for no importer — and drafts the source; the record names
// the snapshot, which says the published catalog was in the view.
//
// 🔴 PLANTED DEFECTS:
//   - in catalogImporterRuns (pkg/catalog/carried_importers.go), report every
//     entity the set holds as running: the published entity at the carried
//     importer's version keeps its place, and that row is refused, no importer
//     advertising the format;
//   - in importerResolutionPlugins (same file), keep every entity the set holds
//     rather than leaving out the ones importersWithoutModule names: the published
//     entity at the later version is chosen as the highest, and that row is
//     refused, its module looked for beside it.
func TestLayeredCatalogCoreImporterStaysCarried(t *testing.T) {
	src := catalogFolderSources[0]
	for _, row := range []struct {
		name    string
		another string
	}{
		{name: "published at the carried importer's version"},
		{name: "published at a later version", another: "9.0.0"},
	} {
		t.Run(row.name, func(t *testing.T) {
			scratch := t.TempDir()
			built, _ := importopstest.LayImporters(t, scratch, filepath.Join(scratch, "catalog"),
				importopstest.Importer{Name: "langflow", Format: "langflow"})
			own := built[0]
			version := own.Version
			if row.another != "" {
				if row.another == own.Version {
					t.Fatalf("the carried importer is at %s, so that version is not another one", own.Version)
				}
				version = row.another
			}
			module := moduleAt(t, own)
			accepted := digest.ContentDigest(module)
			carry(t, embedded.PublishedPlugin{URI: "importer/langflow", Entity: entityAt(t, own), Digest: accepted})

			tree := filepath.Join(t.TempDir(), "catalog")
			writeEntityAt(t, filepath.Join(tree, "plugin", "importer", "langflow", version),
				atVersion(t, entityAt(t, own), version))
			f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
			f.signer, f.pub = snapshottest.NewKey(t)
			f.digest = f.reg.Publish(t, tree, catalog.SnapshotLatestTag)
			f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)

			wsDir := aWorkspaceDir(t)
			importopstest.LayJSONParser(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir))
			writeWorkspaceConfiguration(t, wsDir, f.reg.WorkspaceConfiguration(f.pub, false))
			rw := resolveWorkspaceAt(t, wsDir)
			cacheAcceptedModule(t, rw, "importer/langflow", accepted, module)

			res, cascade, err := importLangflowSource(t, rw, src)
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if res.Halted() {
				t.Fatalf("the importer halted: %+v", res.Diagnostics)
			}
			if importers := importersResolved(cascade); len(importers) != 0 {
				t.Errorf("the caller's resolver was asked for %+v; the carried importer's module is read only "+
					"through the cascade the import builds for it", importers)
			}
			assertTheDefaultDraft(t, res, src)
			if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
				t.Errorf("the record names the catalog snapshot %q; want %q", got, f.digest)
			}
		})
	}
}

// pinOnDisk returns the catalog snapshot the record beside the drafts in
// outputDir names.
func pinOnDisk(t *testing.T, outputDir string) string {
	t.Helper()
	onDisk, err := os.ReadFile(filepath.Join(outputDir, RecordFileName))
	if err != nil {
		t.Fatalf("read the record beside the drafts: %v", err)
	}
	rec, err := parseRecord(onDisk)
	if err != nil {
		t.Fatalf("the record beside the drafts does not read back: %v", err)
	}
	return rec.sole().CatalogSnapshot
}

// TestLayeredCatalogFoldersAloneKeepsThePin imports in a workspace whose catalog
// folder holds the draft-composition catalog and the draft-set importer, over a
// registry publishing the same catalog, so the drafts read alike with the
// published catalog behind the folder and without it. A first import pins the
// snapshot; a newer one is then published and the content cache loses the pinned
// one. Where the pinned snapshot cannot be read — the workspace offline, or its
// registry out of reach — a sync of the first import's drafts is refused, naming
// the pin and running the sync where the registry is reachable, and writes
// nothing: its drafts were judged against that catalog, which the folder alone
// may not draft alike. The offline refusal carries the offline condition. An
// import beside its record that is not a sync reads the folder alone, warning
// once that it does, naming the pin, and writes the pin back unchanged in the
// record it hands back. Online again, a sync reads that pin, asked for by its
// digest, never the newest.
//
// 🔴 PLANTED DEFECT: in readCatalogSnapshot (internal/importops/snapshot.go),
// keep no pin for an import over the folders alone: the import beside the record
// records no snapshot, and every row fails.
func TestLayeredCatalogFoldersAloneKeepsThePin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		config  func(t *testing.T, f snapshotFixture) string
		offline bool
	}{
		{
			name:    "offline",
			config:  func(t *testing.T, f snapshotFixture) string { return f.reg.WorkspaceConfiguration(f.pub, true) },
			offline: true,
		},
		{
			name: "a registry that cannot be reached",
			config: func(t *testing.T, _ snapshotFixture) string {
				return "workspace: {}\ndefault_registry: " + strconv.Quote(closedRegistryHost(t)) + "\n"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := publishedSnapshot(t)
			wsDir := aWorkspaceDir(t)
			online := f.reg.WorkspaceConfiguration(f.pub, false)
			folder, rw := conventionalFolder.resolve(t, wsDir, online)
			importopstest.LayDraftComposerCatalog(t, wsDir, folder)
			first := snapshotImportOptions(t, authoredcontent.DeliveryWrite)
			if _, err := Import(context.Background(), rw, first); err != nil {
				t.Fatalf("the first import was refused: %v", err)
			}
			if got := pinOnDisk(t, first.OutputDir); got != f.digest {
				t.Fatalf("the first import pinned %q; want %q", got, f.digest)
			}
			record, err := os.ReadFile(filepath.Join(first.OutputDir, RecordFileName))
			if err != nil {
				t.Fatal(err)
			}

			newer := snapshotTree(t)
			importopstest.WriteSource(t, filepath.Join(newer, "notes.txt"), "a file the first snapshot does not carry\n")
			second := f.reg.Publish(t, newer, catalog.SnapshotLatestTag)
			f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
			if second == f.digest {
				t.Fatal("the second snapshot has the first one's digest, so nothing here could tell them apart")
			}
			if err := os.RemoveAll(oci.CatalogSnapshotsRoot(rw.Root(), rw.Config().ResolvedArtifactCacheDir())); err != nil {
				t.Fatal(err)
			}

			writeWorkspaceConfiguration(t, wsDir, tc.config(t, f))
			alone := resolveWorkspaceAt(t, wsDir)
			sync := first
			sync.Sync = true
			drafts := treeBytes(t, first.OutputDir)
			_, err = Import(context.Background(), alone, sync)
			if err == nil {
				t.Fatalf("a sync whose pinned snapshot cannot be read went on over the folder alone")
			}
			if tc.offline && !errors.Is(err, egress.ErrOfflineEgress()) {
				t.Errorf("the sync was not refused as offline: %v", err)
			}
			for _, want := range []string{f.digest, "run the sync"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if !reflect.DeepEqual(treeBytes(t, first.OutputDir), drafts) {
				t.Errorf("the refused sync changed the drafts or the record")
			}

			beside := snapshotImportOptions(t, authoredcontent.DeliveryReport)
			if err := os.WriteFile(filepath.Join(beside.OutputDir, RecordFileName), record, 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := Import(context.Background(), alone, beside)
			if err != nil {
				t.Fatalf("the import beside the record over the folder alone was refused: %v", err)
			}
			warned := warningsCarrying(res.Diagnostics, foldersAloneWords)
			if len(warned) != 1 || !strings.Contains(warned[0], f.digest) {
				t.Errorf("the import beside the record warned %q; want one warning of reading the folder alone, "+
					"naming the pin %s", warned, f.digest)
			}
			if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
				t.Errorf("the import beside the record over the folder alone records %q; want the pin %q", got,
					f.digest)
			}

			writeWorkspaceConfiguration(t, wsDir, online)
			rw = resolveWorkspaceAt(t, wsDir)
			before := f.reg.Requests()
			resync := first
			resync.Sync, resync.Delivery = true, authoredcontent.DeliveryReport
			res, err = Import(context.Background(), rw, resync)
			if err != nil {
				t.Fatalf("the sync online was refused: %v", err)
			}
			if warned := warningsCarrying(res.Diagnostics, foldersAloneWords); len(warned) != 0 {
				t.Errorf("the sync online read the folder alone: %q", warned)
			}
			asked := f.reg.Asked(before)
			if !slices.Contains(asked, "GET "+f.reg.ManifestPath(f.digest)) ||
				slices.Contains(asked, "GET "+f.reg.ManifestPath(catalog.SnapshotLatestTag)) {
				t.Errorf("the sync online asked %v; want the pinned manifest by digest and never latest", asked)
			}
			if got := pinOnDisk(t, first.OutputDir); got != f.digest {
				t.Errorf("after the sync online the record names %q; want the pin %q", got, f.digest)
			}
		})
	}
}

// publishedParserJSON is the standard catalog's JSON parser entity, read from the
// sibling hub checkout: the Plugin entity a published catalog holds for the json
// syntax, under the binary's own coordinate.
func publishedParserJSON(t *testing.T) []byte {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(platformHub(t), "plugin", "parser", "json", "*", "plugin.yaml"))
	if err != nil || len(found) == 0 {
		t.Fatalf("the hub checkout holds no plugin/parser/json entity (%v)", err)
	}
	slices.Sort(found)
	body, err := os.ReadFile(found[len(found)-1])
	if err != nil {
		t.Fatalf("read %s: %v", found[len(found)-1], err)
	}
	return body
}

// TestLayeredCatalogFolderParserServesBeforePublished imports a Langflow export
// in a workspace whose catalog folder holds the reference importers, attached,
// and the JSON parser under a name of its own, over a registry publishing a
// catalog that holds the standard catalog's parser/json: two parsers of two names
// advertise the json syntax across the two layers, which a view reading them as
// one finds ambiguous (the control). The folder's parser serves the syntax, so
// the published one is no candidate for it: the import runs, reading the
// folder's parser from beside its entity and never parser/json, and the record
// names the snapshot, which says the published catalog was in the view.
//
// 🔴 PLANTED DEFECT: in seamOver (internal/importops/seam.go), hand the parser
// socket the whole view rather than foldersFirstParsers: the import is refused as
// a syntax more than one parser advertises.
func TestLayeredCatalogFolderParserServesBeforePublished(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()
	src := catalogFolderSources[0]
	tree := filepath.Join(t.TempDir(), "catalog")
	writeCatalogFile(t, tree, "plugin/parser/json/published/plugin.yaml", string(publishedParserJSON(t)))
	f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
	f.signer, f.pub = snapshottest.NewKey(t)
	f.digest = f.reg.Publish(t, tree, catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)

	wsDir := aWorkspaceDir(t)
	folder := importopstest.LayCatalogFolder(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), true)
	config := f.reg.WorkspaceConfiguration(f.pub, false) + "plugins:\n  importer:\n"
	for _, a := range folder.Attachments(nil) {
		config += "    - uri: " + strconv.Quote(a.URI) + "\n"
	}
	writeWorkspaceConfiguration(t, wsDir, config)
	rw := resolveWorkspaceAt(t, wsDir)

	control, err := catalog.LoadAllWithNamedDirs(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), nil, folder.Dir, tree)
	if err != nil {
		t.Fatalf("load the folder and the published catalog as one view: %v", err)
	}
	if _, err := control.ParserPluginForFormat("json"); !errors.Is(err, catalog.ErrAmbiguousParserPlugin()) {
		t.Fatalf("the folder and the published catalog read as one view resolve json without ambiguity (%v), so "+
			"this case measures nothing", err)
	}

	cascade := &recordingCascade{}
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    src.source,
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
	assertTheDefaultDraft(t, res, src)
	calls := cascade.resolutions()
	i := slices.IndexFunc(calls, func(c cascadeResolution) bool { return c.uri == folder.Parser.Reference })
	if i < 0 || calls[i].tier != pluginpath.TierColocated {
		t.Errorf("the folder's parser %s was not read from beside its entity; the import resolved %+v",
			folder.Parser.Reference, calls)
	}
	if slices.ContainsFunc(calls, func(c cascadeResolution) bool { return c.uri == "parser/json" }) {
		t.Errorf("the import resolved the published parser/json: %+v", calls)
	}
	if got := recordIn(t, res).CatalogSnapshot; got != f.digest {
		t.Errorf("the record names the catalog snapshot %q; want %q", got, f.digest)
	}
}

// TestLayeredCatalogFolderImporterServesBeforePublished imports for the compose
// target in a workspace whose catalog folder holds the draft-composition catalog
// and the draft-set importer, over a registry publishing a catalog that holds
// another importer advertising the draft-set format, published-draft-set, with no
// module beside it: two importers of two names advertise the format across the
// two layers, which a view reading them as one finds ambiguous (the control).
//
// With no attachment the folder's importer serves the format, so the published
// one is no candidate for it, and the import drafts with the folder's. With an
// attachment naming the published importer, the attachment chooses it wherever it
// sits: the import loads it, and is refused for its module, which nothing holds —
// the refusal naming it is what says it was the one chosen.
//
// 🔴 PLANTED DEFECTS:
//   - in seamOver (internal/importops/seam.go), hand the import the whole view
//     rather than foldersFirstImporters: the first row is refused as a format
//     more than one importer advertises;
//   - in foldersFirstImporters, return the folders' resolution whatever the
//     attachments name: the second row drafts with the folder's importer.
func TestLayeredCatalogFolderImporterServesBeforePublished(t *testing.T) {
	t.Parallel()
	tree := filepath.Join(t.TempDir(), "catalog")
	published := importopstest.WriteImporterEntity(t, filepath.Join(tree, "plugin"), "published-draft-set",
		[]string{importopstest.DraftSetFormat}, nil)

	for _, tc := range []struct {
		name        string
		attachments string
	}{
		{name: "no attachment"},
		{name: "an attachment naming the published importer",
			attachments: "plugins:\n  importer:\n    - uri: " + strconv.Quote(published.Identity) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := snapshotFixture{reg: snapshottest.NewRegistry(t)}
			f.signer, f.pub = snapshottest.NewKey(t)
			f.digest = f.reg.Publish(t, tree, catalog.SnapshotLatestTag)
			f.reg.Sign(t, catalog.SnapshotLatestTag, f.signer)
			folder, rw := conventionalFolder.resolve(t, aWorkspaceDir(t),
				f.reg.WorkspaceConfiguration(f.pub, false)+tc.attachments)
			importopstest.LayDraftComposerCatalog(t, rw.Root(), folder)

			control, err := catalog.LoadAllWithNamedDirs(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), nil, folder,
				tree)
			if err != nil {
				t.Fatalf("load the folder and the published catalog as one view: %v", err)
			}
			if _, _, err := control.ImporterPluginForFormat(importopstest.DraftSetFormat, nil); !errors.Is(err,
				catalog.ErrAmbiguousImporterPlugin()) {
				t.Fatalf("the folder and the published catalog read as one view resolve the draft-set format "+
					"without ambiguity (%v), so this case measures nothing", err)
			}

			res, err := Import(context.Background(), rw, snapshotImportOptions(t, authoredcontent.DeliveryReport))
			if tc.attachments != "" {
				coordinate, _, _ := strings.Cut(published.Identity, ":")
				if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) || !strings.Contains(err.Error(),
					coordinate) {
					t.Fatalf("the import was not refused for the attached published importer's module: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
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
