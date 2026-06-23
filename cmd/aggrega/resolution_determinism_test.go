// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// distSnapshot reads every file under dir into a map keyed by the path relative
// to dir, so two builds can be compared independent of the absolute location.
func distSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		out[rel] = string(b)
		return nil
	})
	return out
}

// TestBuildIsInvocationLocationIndependent is the determinism proof (Invariant
// 8): with the workspace root pinned by a `workspace:` marker, a build from the
// project root and a build from a subdirectory produce byte-identical artifact
// CONTENT. This holds because IR SourceFile records anchor to the discovered
// root (not CWD) and `catalog_dirs` resolve against their declaring config file
// (not CWD), so neither the catalog searched nor the recorded source paths
// shift with the working directory. The relative `catalog_dirs: [catalog]` here
// also exercises declaring-file resolution end-to-end. (The dist OUTPUT location
// follows the Release's CWD-relative `directory:` and is the author's choice,
// orthogonal to IR content; the snapshot keys on the path within each dist root,
// so a differing output location does not mask or cause a content difference.)
func TestBuildIsInvocationLocationIndependent(t *testing.T) {
	root := t.TempDir()
	releasePath := authoringFixture(t, root)
	// workspace: marker pins the root from any subdir; relative catalog_dirs
	// resolves against this file's dir (root) at load.
	writeWorkspaceConfig(t, root, "auto_pull: true\nworkspace:\n  catalog_dirs:\n    - catalog")

	// Build from the project root; the dist lands at the CWD per the Release's
	// `directory:`. Snapshot content keyed within that dist root.
	t.Chdir(root)
	if err := dispatchBuild(nil, releasePath, nil, false, os.Stderr); err != nil {
		t.Fatalf("build from root: %v", err)
	}
	fromRoot := distSnapshot(t, filepath.Join(root, "dist"))
	if len(fromRoot) == 0 {
		t.Fatal("build from root produced no artifacts")
	}

	// Build the same release from a subdirectory; the marker walk resolves the
	// same root, so the artifact content must be byte-identical.
	sub := filepath.Join(root, "deep", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if err := dispatchBuild(nil, releasePath, nil, false, os.Stderr); err != nil {
		t.Fatalf("build from subdir: %v", err)
	}
	fromSub := distSnapshot(t, filepath.Join(sub, "dist"))

	if !reflect.DeepEqual(fromRoot, fromSub) {
		// Surface the first differing path for a readable failure.
		for k, v := range fromRoot {
			if fromSub[k] != v {
				t.Errorf("artifact %q differs between root-build and subdir-build (IR is not invocation-location-independent)", k)
				break
			}
		}
		if len(fromRoot) != len(fromSub) {
			t.Errorf("artifact set differs: root build wrote %d files, subdir build wrote %d", len(fromRoot), len(fromSub))
		}
	}
}

// TestLintWholeCatalogRequiresCatalogDirs pins the footgun fix: whole-catalog
// lint with no configured catalog_dirs errors clearly — naming the missing
// config — instead of falling back to scanning the workspace root (which would
// pull in unrelated trees and surface as a misleading duplicate-identity dump).
func TestLintWholeCatalogRequiresCatalogDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// A workspace marker with NO catalog_dirs.
	if err := os.WriteFile(filepath.Join(root, ".aggrega-config.yaml"), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	err = lintCatalogWideFromWorkspace(rw)
	if err == nil {
		t.Fatal("expected an error when no catalog_dirs is configured; got nil (the root-scan footgun)")
	}
	if !strings.Contains(err.Error(), "catalog_dirs") {
		t.Errorf("error should name the missing catalog_dirs config; got: %v", err)
	}
}
