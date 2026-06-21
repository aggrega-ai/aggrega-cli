// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func cfgWithCatalogDirs(dirs ...string) workspaceconfig.Config {
	c := workspaceconfig.Default()
	c.Workspace.CatalogDirs = dirs
	return c
}

// TestResolveCatalogDirs pins the single home for catalog-dir precedence: flag
// dirs prepend to config dirs (first-win), with the fallback applied only when
// neither is set.
func TestResolveCatalogDirs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		flag     []string
		cfg      workspaceconfig.Config
		fallback string
		want     []string
	}{
		{"neither set -> fallback", nil, workspaceconfig.Default(), ".", []string{"."}},
		{"neither set -> wsRoot fallback", nil, workspaceconfig.Default(), "/ws", []string{"/ws"}},
		{"flag only", []string{"./a"}, workspaceconfig.Default(), ".", []string{"./a"}},
		{"config only", nil, cfgWithCatalogDirs("./std"), ".", []string{"./std"}},
		{"flag prepends config (first-win order)", []string{"./local"}, cfgWithCatalogDirs("./std"), ".", []string{"./local", "./std"}},
		{"multiple flags keep order, then config", []string{"./a", "./b"}, cfgWithCatalogDirs("./std1", "./std2"), ".", []string{"./a", "./b", "./std1", "./std2"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveCatalogDirs(tc.flag, tc.cfg, tc.fallback)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveCatalogDirs(%v, cfg=%v, %q) = %v; want %v", tc.flag, tc.cfg.Workspace.CatalogDirs, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestReportCatalogShadows pins the observable-shadow surface: a multi-dir load
// with shadows reports winner-vs-shadowed; an empty shadow set prints nothing.
func TestReportCatalogShadows(t *testing.T) {
	t.Parallel()

	withShadow := &catalog.Catalog{Shadows: []catalog.ShadowedEntity{{
		Kind: "Component", Identity: "server:1.0.0",
		WinnerDir: "./local", WinnerPath: "./local/server.yaml",
		ShadowedDir: "./std", ShadowedPath: "./std/server.yaml",
	}}}
	out := captureStderr(func() { reportCatalogShadows(withShadow) })
	for _, want := range []string{"Component", "server:1.0.0", "./std", "./local"} {
		if !strings.Contains(out, want) {
			t.Errorf("shadow report missing %q; got %q", want, out)
		}
	}

	if silent := captureStderr(func() { reportCatalogShadows(&catalog.Catalog{}) }); silent != "" {
		t.Errorf("no-shadow load must print nothing; got %q", silent)
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
	if err := dispatchBuild(nil, releasePath, []string{dir1, dir2}, false); err != nil {
		t.Fatalf("two-flag build failed (provider in dir2 must resolve): %v", err)
	}
	if renderedCount() == 0 {
		t.Error("two-flag build wrote no artifacts")
	}

	// (b) via catalog_dirs config, no flags.
	reset()
	writeWorkspaceConfig(t, root, "auto_pull: true\nworkspace:\n  catalog_dirs:\n    - "+dir1+"\n    - "+dir2+"\n")
	if err := dispatchBuild(nil, releasePath, nil, false); err != nil {
		t.Fatalf("config catalog_dirs build failed: %v", err)
	}
	if renderedCount() == 0 {
		t.Error("config-dirs build wrote no artifacts")
	}
}
