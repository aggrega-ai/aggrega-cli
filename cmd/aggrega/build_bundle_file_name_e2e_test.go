// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage of the names an ArtifactLibrary bundle may give its files,
// through the command's own pin, pull and build: a bundle file name that names no
// file of its own beneath the artifacts key it is consumed at halts the build,
// naming the file and the key, and a bundle whose names are written plainly builds
// and lands each file beneath the key.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// bundleFileNameWorkspace authors a workspace whose one helm component consumes
// the bundle Probe#/artifacts/Bundle at the artifacts key "support", with the
// bundle carrying one file under each name given, and returns what a build is
// handed. The library is written before the workspace is pinned and pulled, so
// the lockfile pins it.
func bundleFileNameWorkspace(t *testing.T, root string, names ...string) (releasePath, catalogDir string) {
	t.Helper()
	var library strings.Builder
	library.WriteString("apiVersion: aggrega.ai/v1alpha1\nkind: ArtifactLibrary\nmetadata:\n  name: Probe\n" +
		"  version: \"1.0.0\"\nspec:\n  artifacts:\n    Bundle:\n")
	for i, name := range names {
		library.WriteString("      \"" + name + "\": |\n        apiVersion: v1\n        kind: ConfigMap\n" +
			"        metadata:\n          name: probe-" + string(rune('a'+i)) + "\n")
	}
	writeFixture(t, filepath.Join(root, "catalog", "libraries", "probe.yaml"), library.String())
	return boundaryArtifactsWorkspace(t, root, "helm", []boundaryRecipe{
		{component: "app", spec: "  artifacts:\n    support:\n      $import: \"Probe#/artifacts/Bundle\"\n"},
	})
}

// TestBuild_ABundleFileThatNamesNoFileBeneathItsKeyIsRefused builds a bundle whose
// file is named "." — the consumption key itself — and one carrying "a//b" beside
// "a/b", two spellings of one file, which a build writes one over the other. Each
// halts with the bundle-file condition, naming the file and the key, and the
// remedy.
func TestBuild_ABundleFileThatNamesNoFileBeneathItsKeyIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		names   []string
		refused string
	}{
		{"a file named for the key itself", []string{"."}, "."},
		{"a doubled separator beside its single-separator spelling", []string{"a/b", "a//b"}, "a//b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			releasePath, catalogDir := bundleFileNameWorkspace(t, root, tc.names...)
			err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
			if !errors.Is(err, renderer.ErrArtifactLibraryFileEscapesKey()) {
				t.Fatalf("got %v, want the bundle-file condition", err)
			}
			chain := cerrors.CauseChain(err)
			t.Logf("the refusal as a build reports it:\n%s", chain)
			for _, want := range []string{`"` + tc.refused + `"`, `"support"`, "rename the file"} {
				if !strings.Contains(chain, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, chain)
				}
			}
		})
	}
}

// TestBuild_ABundleWhoseFileNamesAreWrittenPlainlyBuilds is the control for the
// refusals above, through the same workspace: names written plainly, one of them
// a nested layout, build and land beneath the consumption key.
func TestBuild_ABundleWhoseFileNamesAreWrittenPlainlyBuilds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir := bundleFileNameWorkspace(t, root, "a/b", "rbac/role.yaml")
	tree := builtTree(t, root, releasePath, catalogDir)
	for _, want := range []string{"/support/a/b", "/support/rbac/role.yaml"} {
		found := false
		for p := range tree {
			if strings.HasSuffix(p, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no written file ends in %s; wrote %v", want, sortedTreeKeys(tree))
		}
	}
}
