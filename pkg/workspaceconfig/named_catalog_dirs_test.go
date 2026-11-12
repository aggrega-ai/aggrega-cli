// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheNamedPartOfAStackSaysWhetherTheConventionalDirectoryWasNamed pins the one
// producer of the named part every catalog construction is handed: the conventional
// `catalog` directory is in a stack in every row, and in its named part exactly
// where the configuration lists it — for the workspace or for the Release — or a
// --catalog directory names it. The whole-catalog passes carry the same named part
// beside each stack, and the root the directory is held to follows from it: the
// directory is a link leading out of the workspace in every row, so it is a root of
// its own where it was named and held to the workspace where it was not.
//
// The first row is the control: nobody names the directory, so it is in the stack
// and not in the named part, and is held to the workspace.
func TestTheNamedPartOfAStackSaysWhetherTheConventionalDirectoryWasNamed(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		config string
		// flag is a --catalog directory relative to the workspace root, or empty.
		flag  string
		named bool
	}{
		{name: "control: read only because it is there", config: "workspace: {}\n"},
		{name: "listed for the workspace", config: "workspace:\n  catalog_dirs:\n    - ./catalog\n", named: true},
		{name: "listed for the Release", config: "workspace:\n  releases:\n    release.yaml:\n      catalog_dirs:\n        - ./catalog\n", named: true},
		{name: "passed with --catalog", config: "workspace: {}\n", flag: "catalog", named: true},
		{name: "another directory listed, the conventional one still appended unnamed",
			config: "workspace:\n  catalog_dirs:\n    - ./other\n"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			if err := os.MkdirAll(filepath.Join(ws, "other"), 0o755); err != nil {
				t.Fatal(err)
			}
			link(t, t.TempDir(), filepath.Join(ws, workspaceconfig.ConventionalCatalogDir))
			release := filepath.Join(ws, "release.yaml")
			for path, body := range map[string]string{release: "kind: Release\n", filepath.Join(ws, workspaceconfig.ConfigFilename): row.config} {
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			rw, err := workspaceconfig.ResolveWithoutGlobal(ws, ws, func(string) string { return "" })
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			var flags []string
			if row.flag != "" {
				flags = []string{filepath.Join(ws, row.flag)}
			}
			conventional := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)

			stack, named := stackOf(t, rw, release, flags), namedOf(t, rw, release, flags)
			if !slices.Contains(stack, conventional) {
				t.Fatalf("precondition: the stack %v does not hold %s, so the row is about nothing", stack, conventional)
			}
			if got := slices.Contains(named, conventional); got != row.named {
				t.Errorf("the named part %v holds %s: %v, want %v", named, conventional, got, row.named)
			}
			for _, dir := range named {
				if !slices.Contains(stack, dir) {
					t.Errorf("the named part names %s, which the stack %v does not hold", dir, stack)
				}
			}
			want := ws
			if row.named {
				want = conventional
			}
			if got := workspaceconfig.CatalogDirRoot(ws, conventional, named); got != want {
				t.Errorf("CatalogDirRoot over the named part = %s, want %s", got, want)
			}

			passes := passesOf(t, rw, func() []string { return []string{"release.yaml"} }, flags)
			if len(passes) != 1 {
				t.Fatalf("WholeCatalogPasses returned %d passes; want 1", len(passes))
			}
			wantPass := namedOf(t, rw, "", flags)
			if passes[0].NamesReleases() {
				wantPass = named
			}
			if !slices.Equal(passes[0].Named, wantPass) {
				t.Errorf("the pass carries named part %v; want %v, the resolver's for the same stack", passes[0].Named, wantPass)
			}
		})
	}
}
