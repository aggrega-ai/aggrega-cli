// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestResolveCatalogDirsNamesADirectoryOnce holds the catalog search stack to one
// entry per directory, however often and in whatever spelling the directory is
// named, while every distinct directory keeps its own entry in its own place.
//
// A directory is one directory when the file system says the two paths are one
// place: a flag naming a configured directory, a configuration listing one twice,
// a trailing separator, a link. The first naming keeps its position and its
// spelling, so precedence among the other entries does not move. Two different
// directories stay two, in order, and a directory beneath another stays its own
// entry. The case-folding row runs only where the file system running the test
// folds case — Windows and macOS by default — because elsewhere two spellings
// that differ in case are two directories, which the row would then be wrong to
// fold; there the row is skipped and says so.
func TestResolveCatalogDirsNamesADirectoryOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		config string
		// flags are the --catalog values, each relative to the root.
		flags []string
		// link names a link the row creates at <root>/<link[0]> leading to
		// <root>/<link[1]>.
		link [2]string
		// foldsCase skips the row unless the file system folds case.
		foldsCase bool
		want      []string // relative to the root, in order
	}{
		{name: "a flag naming the directory the configuration lists",
			config: "workspace:\n  catalog_dirs:\n    - variant\n", flags: []string{"variant"},
			want: []string{"variant"}},
		{name: "a flag spelling it with a trailing separator",
			config: "workspace:\n  catalog_dirs:\n    - variant\n", flags: []string{"variant" + string(filepath.Separator)},
			want: []string{"variant"}},
		{name: "a configuration listing it twice",
			config: "workspace:\n  catalog_dirs:\n    - variant\n    - ./variant\n",
			want:   []string{"variant"}},
		{name: "a flag naming it through a link keeps the flag's spelling and place",
			config: "workspace:\n  catalog_dirs:\n    - other\n    - variant\n", flags: []string{"variant-link"},
			link: [2]string{"variant-link", "variant"},
			want: []string{"variant-link", "other"}},
		{name: "two different directories stay two, in order",
			config: "workspace:\n  catalog_dirs:\n    - variant\n", flags: []string{"other"},
			want: []string{"other", "variant"}},
		{name: "a directory beneath a listed one keeps its own entry",
			config: "workspace:\n  catalog_dirs:\n    - variant\n", flags: []string{filepath.Join("variant", "sub")},
			want: []string{filepath.Join("variant", "sub"), "variant"}},
		{name: "a --catalog directory that is not there, named twice, is named once",
			config: "workspace: {}\n", flags: []string{"missing", "missing" + string(filepath.Separator)},
			want: []string{"missing"}},
		{name: "a flag naming it in another case, where the file system folds case",
			config: "workspace:\n  catalog_dirs:\n    - variant\n", flags: []string{"VARIANT"}, foldsCase: true,
			want: []string{"VARIANT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, d := range []string{"variant", filepath.Join("variant", "sub"), "other"} {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.foldsCase && !foldsCase(t, root) {
				t.Skip("this file system distinguishes case, so two spellings that differ in case are two directories here")
			}
			if tc.link[0] != "" {
				if err := os.Symlink(filepath.Join(root, tc.link[1]), filepath.Join(root, tc.link[0])); err != nil {
					t.Skipf("this file system cannot hold a symbolic link: %v", err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, ".aggrega-config.yaml"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			rw := resolveIn(t, root)

			flags := make([]string, 0, len(tc.flags))
			for _, f := range tc.flags {
				flags = append(flags, filepath.Join(root, f)+suffixOf(f))
			}
			got := rw.ResolveCatalogDirs("", flags)
			want := make([]string, 0, len(tc.want))
			for _, w := range tc.want {
				want = append(want, filepath.Join(root, w))
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("stack = %v\nwant    %v", got, want)
			}
			if named := rw.NamedCatalogDirs("", flags); !reflect.DeepEqual(named, want) {
				t.Errorf("named part = %v\nwant         %v", named, want)
			}
		})
	}
}

// suffixOf returns the trailing separator f carries, so a row spelling a
// directory with one hands the resolver that spelling rather than one cleaned by
// the join that made it absolute.
func suffixOf(f string) string {
	if strings.HasSuffix(f, string(filepath.Separator)) {
		return string(filepath.Separator)
	}
	return ""
}

// foldsCase reports whether the file system holding dir resolves a name spelled
// in another case to the same entry.
func foldsCase(t *testing.T, dir string) bool {
	t.Helper()
	lower := filepath.Join(dir, "variant")
	upper := filepath.Join(dir, "VARIANT")
	li, err := os.Stat(lower)
	if err != nil {
		t.Fatal(err)
	}
	ui, err := os.Stat(upper)
	return err == nil && os.SameFile(li, ui)
}
