// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// What an import reads in a workspace that names no catalog folder, compared
// across every surface that carries the capability, each driven as a spawned
// process of the built binary over its own wire: the command line, the
// assistant's import tool and the import command's tool, and the editor's
// session on aggrega/import.
//
// The workspace names a registry on loopback as its default one, pins the test's
// key for it, and holds no catalog folder. The registry publishes a signed catalog
// snapshot of the draft-composition catalog and the draft-set importer, which this
// test fetches once, in process, through the import operation itself; every
// surface then runs offline, so each can only have read the snapshot the workspace
// already holds — and the registry's request count shows none of them asked it
// anything.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/snapshottest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// snapshotParityFixture is a registry publishing a signed snapshot, and the
// public key a workspace pins to trust it.
type snapshotParityFixture struct {
	reg    *snapshottest.Registry
	pub    []byte
	digest string
}

// publishParitySnapshot publishes, signed, the snapshot of the draft-composition
// catalog with the draft-set importer beside it.
func publishParitySnapshot(t *testing.T) snapshotParityFixture {
	t.Helper()
	tree := filepath.Join(t.TempDir(), "catalog")
	importopstest.LayDraftComposerCatalog(t, filepath.Dir(tree), tree)
	f := snapshotParityFixture{reg: snapshottest.NewRegistry(t)}
	signer, pub := snapshottest.NewKey(t)
	f.pub = pub
	f.digest = f.reg.Publish(t, tree, catalog.SnapshotLatestTag)
	f.reg.Sign(t, catalog.SnapshotLatestTag, signer)
	return f
}

// layHeldSnapshotImport builds a row's workspace — its configuration naming the
// registry and pinning its key, the draft-set source at its root, no catalog
// folder — and fetches the snapshot into its content cache through the import
// operation, in process and online, under a reporting delivery that writes no
// draft. It returns the workspace root.
func layHeldSnapshotImport(t *testing.T, f snapshotParityFixture) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "ws")
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), f.reg.WorkspaceConfiguration(f.pub, false))
	writeFixture(t, filepath.Join(root, targetSourceName), importopstest.DraftComposerSource())
	rw, err := workspaceconfig.Resolve(root, "", func(k string) string {
		if k == "AGGREGA_OFFLINE" || k == workspaceconfig.EnvWorkspaceRoot {
			return ""
		}
		return os.Getenv(k)
	})
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	source := filepath.Join(root, targetSourceName)
	if _, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    source,
		SourceName:    source,
		Format:        importopstest.DraftSetFormat,
		Delivery:      authoredcontent.DeliveryReport,
		ResolvePlugin: importPluginBytes,
	}); err != nil {
		t.Fatalf("fetch the catalog snapshot into the workspace: %v", err)
	}
	return root
}

// TestEverySurfaceImportsAgainstTheHeldSnapshot imports the draft-set source for
// the compose target through every surface that carries a target, each offline in
// a workspace that names no catalog folder and holds the snapshot, and holds every
// one to the same outcome: the Manifest drafted from the snapshot's Recipes, the
// record naming the snapshot, and no request to the registry.
//
// 🔴 PLANTED DEFECTS:
//   - have the session's import build its own seam over the workspace's folders
//     only: the session row is refused, no Recipe binding to compose;
//   - leave the snapshot out of the record (internal/importops/record.go,
//     recordScaffold): every row's record check fails.
func TestEverySurfaceImportsAgainstTheHeldSnapshot(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	f := publishParitySnapshot(t)
	for _, row := range targetRows {
		t.Run(row.name, func(t *testing.T) {
			root := layHeldSnapshotImport(t, f)
			before := f.reg.Requests()
			if out, ok := row.run(t, bin, root, "compose"); !ok {
				t.Fatalf("the import for the compose target was refused: %s", out)
			}
			if after := f.reg.Requests(); after != before {
				t.Errorf("the surface asked the registry %d requests, though the snapshot is held", after-before)
			}
			manifest, err := os.ReadFile(filepath.Join(root, "manifest.yaml"))
			if err != nil {
				t.Fatalf("the drafted Manifest is not at the workspace root: %v", err)
			}
			if string(manifest) != importopstest.DraftedManifestComposed {
				t.Errorf("the drafted Manifest is\n%s\nwant the one the snapshot's Recipes give\n%s", manifest,
					importopstest.DraftedManifestComposed)
			}
			record, err := os.ReadFile(filepath.Join(root, importops.RecordFileName))
			if err != nil {
				t.Fatalf("no record beside the drafts: %v", err)
			}
			if !strings.Contains(string(record), `"catalogSnapshot": "`+f.digest+`"`) {
				t.Errorf("the record does not name the catalog snapshot %s:\n%s", f.digest, record)
			}
		})
	}
}

// TestEverySurfaceThatCarriesUpgradeRefusesItOffline asks for the newest
// snapshot through every surface that carries the refresh — the command line's
// --upgrade, the import tool's upgrade, the import command's tool's generated
// upgrade and the session's upgrade on aggrega/import — offline, in a workspace
// that holds one: each is refused under the offline condition, and the registry
// is asked nothing.
//
// 🔴 PLANTED DEFECTS: have the import tool, or the session, hand the import no
// Upgrade (the importops.Options literal in internal/mcp/tool_import.go or
// internal/serve/import.go): that row reads the held snapshot and succeeds.
func TestEverySurfaceThatCarriesUpgradeRefusesItOffline(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	f := publishParitySnapshot(t)
	for _, row := range []targetRow{
		{
			name: "the command line's --upgrade",
			run: func(t *testing.T, bin, root, target string) (string, bool) {
				return runImportCommand(t, bin, root, "import", targetSourceName, "--format",
					importopstest.DraftSetFormat, "--target", target, "--upgrade")
			},
		},
		{
			name: "the import tool's upgrade",
			run: func(t *testing.T, bin, root, target string) (string, bool) {
				return callImportTool(t, bin, root, root, "import", map[string]any{
					"source": targetSourceName, "format": importopstest.DraftSetFormat, "target": target, "upgrade": true})
			},
		},
		{
			name: "the import command's tool, its generated upgrade argument",
			run: func(t *testing.T, bin, root, target string) (string, bool) {
				return callImportTool(t, bin, root, root, "cli-import", map[string]any{
					"args": []string{targetSourceName}, "format": importopstest.DraftSetFormat, "target": target,
					"upgrade": true})
			},
		},
		{
			name: "the session's upgrade on aggrega/import",
			run: func(t *testing.T, bin, root, target string) (string, bool) {
				return sessionImport(t, bin, root, map[string]any{
					"source": targetSourceName, "outputDir": ".", "format": importopstest.DraftSetFormat, "target": target,
					"upgrade": true})
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := layHeldSnapshotImport(t, f)
			before := f.reg.Requests()
			out, ok := row.run(t, bin, root, "compose")
			if ok {
				t.Fatalf("the refresh ran offline: %s", out)
			}
			if !strings.Contains(out, "E0125") || !strings.Contains(out, "refreshing the published catalog") {
				t.Errorf("the refusal is not the offline condition naming the refresh:\n%s", out)
			}
			if after := f.reg.Requests(); after != before {
				t.Errorf("the refused refresh asked the registry %d requests", after-before)
			}
		})
	}
}
