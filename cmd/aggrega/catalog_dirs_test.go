// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
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
// directories — the Recipe in a second dir, the rest in the first — both via
// two --catalog flags and via catalog_dirs config. No network: the auto-pull
// resolves the whole closure from the local directories. This is what lets a
// project consume a shared catalog alongside its own local entities.
//
// The Recipe is the entity relocated because its resolution genuinely depends
// on the set of directories searched: with the Recipe in the second directory,
// a build handed only the first halts with the Recipe unresolved. A provider
// would prove nothing here — the binary embeds its own core provider entities
// and prefers them over a catalog entity of the same provider name, so a core
// provider resolves without any catalog directory supplying it.
func TestBuild_MultiCatalogResolvesAcrossDirs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	// Relocate the Recipe into a SECOND catalog directory, so the build can
	// only succeed if both directories are searched.
	dir1 := filepath.Join(root, "catalog")
	dir2 := filepath.Join(root, "catalog2")
	recipeSrc := filepath.Join(dir1, "recipes", "server.yaml")
	recipeDst := filepath.Join(dir2, "recipes", "server.yaml")
	data, err := os.ReadFile(recipeSrc)
	if err != nil {
		t.Fatalf("read recipe: %v", err)
	}
	writeFixture(t, recipeDst, string(data))
	if err := os.Remove(recipeSrc); err != nil {
		t.Fatalf("remove recipe from dir1: %v", err)
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
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{dir1, dir2}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("two-flag build failed (Recipe in dir2 must resolve): %v", err)
	}
	if renderedCount() == 0 {
		t.Error("two-flag build wrote no artifacts")
	}

	// (b) via catalog_dirs config, no flags.
	reset()
	writeWorkspaceConfig(t, root, "auto_pull: true\nworkspace:\n  catalog_dirs:\n    - "+dir1+"\n    - "+dir2+"\n")
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("config catalog_dirs build failed: %v", err)
	}
	if renderedCount() == 0 {
		t.Error("config-dirs build wrote no artifacts")
	}
}
