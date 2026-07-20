// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestReportCatalogShadows pins the observable-shadow surface: a multi-dir load
// with shadows reports winner-vs-shadowed; an empty shadow set prints nothing.
func TestReportCatalogShadows(t *testing.T) {
	t.Parallel()

	withShadow := &catalog.Catalog{Shadows: []catalog.ShadowedEntity{{
		Kind: "Component", Identity: "server:1.0.0",
		WinnerDir: "./local", WinnerPath: "./local/server.yaml",
		ShadowedDir: "./std", ShadowedPath: "./std/server.yaml",
	}}}
	var buf bytes.Buffer
	reportCatalogShadows(withShadow, &buf)
	out := buf.String()
	for _, want := range []string{"Component", "server:1.0.0", "./std", "./local"} {
		if !strings.Contains(out, want) {
			t.Errorf("shadow report missing %q; got %q", want, out)
		}
	}

	var silentBuf bytes.Buffer
	reportCatalogShadows(&catalog.Catalog{}, &silentBuf)
	if silentBuf.String() != "" {
		t.Errorf("no-shadow load must print nothing; got %q", silentBuf.String())
	}
}

// TestBuild_MultiCatalogResolvesAcrossDirs proves multi-directory catalog
// consumption: ONE offline build resolves entities split across two catalog
// directories — the provider in a second dir, the rest in the first — both via
// two --catalog flags and via catalog_dirs config. No network: the auto-pull
// resolves the whole closure from the local directories. This is what lets a
// project consume a shared catalog alongside its own local entities.
func TestBuild_MultiCatalogResolvesAcrossDirs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	// Relocate the provider into a SECOND catalog directory, so the build can
	// only succeed if both directories are searched.
	dir1 := filepath.Join(root, "catalog")
	dir2 := filepath.Join(root, "catalog2")
	provSrc := filepath.Join(dir1, "providers", "helm.yaml")
	provDst := filepath.Join(dir2, "providers", "helm.yaml")
	data, err := os.ReadFile(provSrc)
	if err != nil {
		t.Fatalf("read provider: %v", err)
	}
	writeFixture(t, provDst, string(data))
	if err := os.Remove(provSrc); err != nil {
		t.Fatalf("remove provider from dir1: %v", err)
	}

	reset := func() {
		_ = os.Remove(filepath.Join(root, "release.lock"))
		_ = os.RemoveAll(filepath.Join(root, "dist"))
	}
	renderedCount := func() int {
		n := 0
		_ = filepath.WalkDir(filepath.Join(root, "dist"), func(_ string, d os.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				n++
			}
			return nil
		})
		return n
	}

	// (a) via two --catalog flags.
	writeWorkspaceConfig(t, root, "auto_pull: true\n")
	reset()
	if err := dispatchBuild(nil, releasePath, []string{dir1, dir2}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("two-flag build failed (provider in dir2 must resolve): %v", err)
	}
	if renderedCount() == 0 {
		t.Error("two-flag build wrote no artifacts")
	}

	// (b) via catalog_dirs config, no flags.
	reset()
	writeWorkspaceConfig(t, root, "auto_pull: true\nworkspace:\n  catalog_dirs:\n    - "+dir1+"\n    - "+dir2+"\n")
	if err := dispatchBuild(nil, releasePath, nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("config catalog_dirs build failed: %v", err)
	}
	if renderedCount() == 0 {
		t.Error("config-dirs build wrote no artifacts")
	}
}
