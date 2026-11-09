// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestAModuleFindingIsPublishedOnTheModuleDocument drives the daemon's
// aggrega/validate over the Aggregate a Release names and over the document of a
// module that Aggregate imports, and pins where a finding about what the module
// declares is published: on the module's document, on the authored key there, and
// on the importing Aggregate not at all.
//
// The fault is a module component's attachment wiring a socket the attached
// Feature declares none of. Its locator is a path inside the module's document,
// so placed on the importing Aggregate it would land on whichever key there
// shares its spelling — the misplacement this pins against — while the module's
// own answer places it on the socket the author wrote.
func TestAModuleFindingIsPublishedOnTheModuleDocument(t *testing.T) {
	t.Parallel()
	const detail = "has no usesSchema"
	ws := moduleFindingWorkspace(t)
	srv := newTestServer(t, ws, nil, nil)
	dirs := []string{filepath.Join(ws, "catalog-edge"), filepath.Join(ws, "catalog-cov")}
	srv.wsConfig.Workspace.CatalogDirs = dirs
	srv.lazyCatalog = catalog.NewLazyLookup(ws, "", dirs, nil)

	module := filepath.Join(ws, "catalog-cov", "aggregates", "mod-contrib-component.yaml")
	content, diags := validateDocumentAgainstRelease(t, srv, module)
	finding := findDiagnostic(t, diags, detail)
	if finding.Severity != "error" {
		t.Errorf("the finding on the module is published at %q; want error", finding.Severity)
	}
	if want := fixtureKeyLine(t, content, "bogus-module-socket:"); finding.Line != want {
		t.Errorf("the finding on the module is at line %d; want the socket's key at line %d (diagnostics: %v)",
			finding.Line, want, diagMessages(diags))
	}

	for _, document := range []string{"aggregate.yaml", "release.yaml"} {
		_, other := validateDocumentAgainstRelease(t, srv, filepath.Join(ws, document))
		for _, d := range other {
			if containsSubstring(d.Message, detail) {
				t.Errorf("%s carries a finding written in the module's document: %q at %d:%d",
					document, d.Message, d.Line, d.Column)
			}
		}
	}
}

// moduleFindingWorkspace lays the committed sub-aggregate coverage fixture out as
// a workspace: its two catalogs, its Manifest, the Aggregate importing the module
// whose component wires an undeclared attachment socket, and a Release placing the
// module's components that names the Aggregate and the Manifest by path.
func moduleFindingWorkspace(t *testing.T) string {
	t.Helper()
	const fixtures = "../compiler/testdata"
	ws := t.TempDir()
	for from, to := range map[string]string{
		filepath.Join(fixtures, "subagg-coverage", "catalog", "base"):  filepath.Join(ws, "catalog-cov"),
		filepath.Join(fixtures, "uses-edge-config", "catalog", "base"): filepath.Join(ws, "catalog-edge"),
	} {
		if err := os.CopyFS(to, os.DirFS(from)); err != nil {
			t.Fatalf("copy the committed catalog %s: %v", from, err)
		}
	}
	for from, to := range map[string]string{
		filepath.Join(fixtures, "subagg-coverage", "manifest.yaml"):                 filepath.Join(ws, "manifest.yaml"),
		filepath.Join(fixtures, "subagg-coverage", "aggregate-contrib-module.yaml"): filepath.Join(ws, "aggregate.yaml"),
	} {
		body, err := os.ReadFile(from)
		if err != nil {
			t.Fatalf("read the committed fixture %s: %v", from, err)
		}
		if err := os.WriteFile(to, body, 0o600); err != nil {
			t.Fatalf("write %s: %v", to, err)
		}
	}
	release := "apiVersion: aggrega.ai/v1beta1\nkind: Release\n" +
		"metadata:\n  name: cov-release-contrib-module\n  environment: local\n  version: \"1.0.0\"\n" +
		"spec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n  boundaries:\n" +
		"    cluster:\n      directory: ./dist/cluster\n      components:\n" +
		"        data.consumer: {}\n        data.store: {}\n"
	if err := os.WriteFile(filepath.Join(ws, "release.yaml"), []byte(release), 0o600); err != nil {
		t.Fatalf("write the release: %v", err)
	}
	return ws
}
