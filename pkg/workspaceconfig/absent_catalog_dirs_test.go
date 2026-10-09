// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAConfiguredCatalogDirectoryWhereNothingExistsIsLeftOutAndNoted pins the one
// decision every surface reads a configured catalog directory that is not there
// through: the stack leaves it out, so it is read as holding nothing, the named
// part leaves it out with it, and the notice names it and the setting that lists
// it, once.
//
// Every row resolves one workspace through the real cascade and asks the three
// readers the same question — the stack, its named part and the notice — so a row
// fails on any one of them disagreeing with the other two. The first row is the
// positive control: the listed directory is there, empty, and the stack keeps it
// with nothing noted. The rows after it each hold one directory to one answer: the
// ones where nothing exists are left out and noted; a `--catalog` directory, a
// file, and a link leading nowhere stay, with nothing noted, because each is
// something other than a configured directory where nothing exists.
func TestAConfiguredCatalogDirectoryWhereNothingExistsIsLeftOutAndNoted(t *testing.T) {
	t.Parallel()
	const (
		workspaceWords = "`workspace.catalog_dirs` in .aggrega-config.yaml"
		releaseWords   = "`workspace.catalog_dirs`, or a Release's own `catalog_dirs` under `workspace.releases`, in .aggrega-config.yaml"
	)
	for _, tc := range []struct {
		name   string
		config string
		// make creates each of these directories, relative to the root, before the
		// workspace resolves; file creates each as a file; dangling creates each as a
		// link leading to a place that does not exist.
		make, file, dangling []string
		// release asks for that Release's stack, relative to the root; empty asks for
		// the workspace's own.
		release string
		// flags are --catalog values, relative to the root.
		flags []string
		// stack is the stack the readers answer, relative to the root, in order.
		stack []string
		// noted is the directories the notice names, relative to the root, in order,
		// and words the setting it names them under.
		noted []string
		words string
	}{
		{name: "control: a listed directory that is there, empty, is read and noted nowhere",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./listed\n", make: []string{"own", "listed"},
			stack: []string{"own", "listed"}},
		{name: "a directory the workspace's list names where nothing exists is left out and noted",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./no-such-catalog\n", make: []string{"own"},
			stack: []string{"own"}, noted: []string{"no-such-catalog"}, words: workspaceWords},
		{name: "a directory a Release's own list names where nothing exists is left out of that Release's stack and noted",
			config:  "workspace:\n  catalog_dirs:\n    - ./own\n  releases:\n    release.yaml:\n      catalog_dirs:\n        - ./own\n        - ./gone-for-release\n",
			make:    []string{"own"},
			release: "release.yaml",
			stack:   []string{"own"}, noted: []string{"gone-for-release"}, words: releaseWords},
		{name: "a directory a Release's addition names where nothing exists is left out and noted",
			config:  "workspace:\n  catalog_dirs:\n    - ./own\n  releases:\n    release.yaml:\n      catalog_dirs_append:\n        - ./gone-extra\n",
			make:    []string{"own"},
			release: "release.yaml",
			stack:   []string{"own"}, noted: []string{"gone-extra"}, words: releaseWords},
		{name: "a directory listed twice where nothing exists is noted once",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./no-such-catalog\n    - no-such-catalog\n", make: []string{"own"},
			stack: []string{"own"}, noted: []string{"no-such-catalog"}, words: workspaceWords},
		{name: "every listed directory missing is not the off-switch: the conventional directory is still read",
			config: "workspace:\n  catalog_dirs:\n    - ./no-such-catalog\n", make: []string{workspaceconfig.ConventionalCatalogDir},
			stack: []string{workspaceconfig.ConventionalCatalogDir}, noted: []string{"no-such-catalog"}, words: workspaceWords},
		{name: "a --catalog directory where nothing exists stays in the stack and is noted nowhere",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n", make: []string{"own"}, flags: []string{"no-such-flag-dir"},
			stack: []string{"no-such-flag-dir", "own"}},
		{name: "a listed directory a --catalog flag also names is the flag's entry, kept and noted nowhere",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./no-such-catalog\n", make: []string{"own"}, flags: []string{"no-such-catalog"},
			stack: []string{"no-such-catalog", "own"}},
		{name: "a listed path holding a file stays in the stack and is noted nowhere",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./a-file\n", make: []string{"own"}, file: []string{"a-file"},
			stack: []string{"own", "a-file"}},
		{name: "a listed link leading nowhere stays in the stack and is noted nowhere",
			config: "workspace:\n  catalog_dirs:\n    - ./own\n    - ./dangling\n", make: []string{"own"}, dangling: []string{"dangling"},
			stack: []string{"own", "dangling"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			for _, d := range tc.make {
				if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range tc.file {
				write(t, filepath.Join(ws, f), "not a directory\n")
			}
			for _, l := range tc.dangling {
				link(t, filepath.Join(ws, "nowhere", l), filepath.Join(ws, l))
			}
			write(t, filepath.Join(ws, "release.yaml"), "kind: Release\n")
			write(t, filepath.Join(ws, workspaceconfig.ConfigFilename), tc.config)
			rw, err := workspaceconfig.ResolveWithoutGlobal(ws, ws, func(string) string { return "" })
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			at := func(rel string) string { return filepath.Join(rw.Root(), rel) }
			release := ""
			if tc.release != "" {
				release = at(tc.release)
			}
			var flags []string
			for _, f := range tc.flags {
				flags = append(flags, at(f))
			}

			wantStack := make([]string, 0, len(tc.stack))
			for _, d := range tc.stack {
				wantStack = append(wantStack, at(d))
			}
			stack := rw.ResolveCatalogDirs(release, flags)
			if !slices.Equal(stack, wantStack) {
				t.Errorf("the stack is %v; want %v", stack, wantStack)
			}
			named := rw.NamedCatalogDirs(release, flags)
			for _, d := range named {
				if !slices.Contains(stack, d) {
					t.Errorf("the named part names %s, which the stack %v does not hold", d, stack)
				}
			}

			var notice bytes.Buffer
			rw.NoteAbsentCatalogDirs(&notice, release, flags)
			var want string
			for _, d := range tc.noted {
				want += "  catalog: " + at(d) + " is listed under " + tc.words + " and does not exist, so nothing is read from it\n"
				if slices.Contains(named, at(d)) {
					t.Errorf("the named part %v names %s, which the notice says is left out", named, at(d))
				}
			}
			if notice.String() != want {
				t.Errorf("the notice reads\n%q\nwant\n%q", notice.String(), want)
			}
		})
	}
}
