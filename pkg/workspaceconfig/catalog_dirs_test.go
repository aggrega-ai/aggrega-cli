// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveIn resolves a workspace rooted at dir against a frozen, empty
// environment, so neither the machine's global config nor an exported
// AGGREGA_CATALOG_DIRS reaches the resolution under test.
func resolveIn(t *testing.T, dir string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, err := workspaceconfig.Resolve(dir, "", frozenLookup(nil))
	if err != nil {
		t.Fatalf("Resolve(%s): %v", dir, err)
	}
	return rw
}

// TestResolveCatalogDirsAuthoredShapes pins the single home for catalog-dir
// precedence across the four shapes an author can write, each resolved through
// the real cascade rather than by handing the resolver a Config directly —
// because the distinction between an ABSENT `catalog_dirs` and one authored as
// an empty list is carried by the merged slice being nil or non-nil-and-empty,
// and only a resolution through the cascade proves the merge preserves it.
//
// The shapes and what each resolves to, with a `catalog` directory present at
// the workspace root in every row:
//
//   - no config file at all, and an absent key       -> the conventional directory alone
//   - an authored list                               -> the list, then the conventional directory
//   - an authored EMPTY list                         -> nothing at all (the explicit off-switch)
//   - a --catalog flag                               -> the flag dir, then the rest
func TestResolveCatalogDirsAuthoredShapes(t *testing.T) {
	t.Parallel()
	ab := func(p string) string { a, _ := filepath.Abs(p); return a }
	cases := []struct {
		name string
		// config is the `.aggrega-config.yaml` body; "" writes no file at all.
		config string
		// makeConventional creates <root>/catalog before resolving.
		makeConventional bool
		flag             []string
		// want is built from the root, which is only known at run time.
		want func(root string) []string
	}{
		{
			name:             "no config file at all -> the conventional directory",
			config:           "",
			makeConventional: true,
			want:             func(root string) []string { return []string{filepath.Join(root, "catalog")} },
		},
		{
			name:             "absent key -> the conventional directory",
			config:           "workspace: {}\n",
			makeConventional: true,
			want:             func(root string) []string { return []string{filepath.Join(root, "catalog")} },
		},
		{
			name:             "absent key, no conventional directory -> empty",
			config:           "workspace: {}\n",
			makeConventional: false,
			want:             func(string) []string { return []string{} },
		},
		{
			name:             "authored list -> the list, then the conventional directory",
			config:           "workspace:\n  catalog_dirs:\n    - ./vendored\n",
			makeConventional: true,
			want: func(root string) []string {
				return []string{filepath.Join(root, "vendored"), filepath.Join(root, "catalog")}
			},
		},
		{
			name:             "authored list naming the conventional directory -> it appears once",
			config:           "workspace:\n  catalog_dirs:\n    - ./catalog\n    - ./vendored\n",
			makeConventional: true,
			want: func(root string) []string {
				return []string{filepath.Join(root, "catalog"), filepath.Join(root, "vendored")}
			},
		},
		{
			name:             "an ancestor already in the stack covers the conventional directory",
			config:           "workspace:\n  catalog_dirs:\n    - .\n",
			makeConventional: true,
			want:             func(root string) []string { return []string{root} },
		},
		{
			name:             "authored EMPTY list -> nothing local, conventional directory suppressed",
			config:           "workspace:\n  catalog_dirs: []\n",
			makeConventional: true,
			want:             func(string) []string { return []string{} },
		},
		{
			name:             "flag only -> the flag dir (absolute), then the conventional directory",
			config:           "workspace: {}\n",
			makeConventional: true,
			flag:             []string{"./from-flag"},
			want: func(root string) []string {
				return []string{ab("./from-flag"), filepath.Join(root, "catalog")}
			},
		},
		{
			name:             "flag prepends an authored list, conventional directory last",
			config:           "workspace:\n  catalog_dirs:\n    - ./vendored\n",
			makeConventional: true,
			flag:             []string{"./from-flag"},
			want: func(root string) []string {
				return []string{ab("./from-flag"), filepath.Join(root, "vendored"), filepath.Join(root, "catalog")}
			},
		},
		{
			name:             "flag survives the off-switch, which suppresses only the conventional directory",
			config:           "workspace:\n  catalog_dirs: []\n",
			makeConventional: true,
			flag:             []string{"./from-flag"},
			want:             func(string) []string { return []string{ab("./from-flag")} },
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.config != "" {
				write(t, filepath.Join(dir, workspaceconfig.ConfigFilename), tc.config)
			}
			if tc.makeConventional {
				if err := os.MkdirAll(filepath.Join(dir, "catalog"), 0o755); err != nil {
					t.Fatalf("mkdir catalog: %v", err)
				}
			}
			if err := os.MkdirAll(filepath.Join(dir, "vendored"), 0o755); err != nil {
				t.Fatalf("mkdir vendored: %v", err)
			}

			rw := resolveIn(t, dir)
			// The expected stacks are built from the root the resolver reports,
			// never from the temporary directory, so a root discovery that ever
			// stops somewhere else fails on the stack rather than on a path
			// spelling.
			got := rw.ResolveCatalogDirs(tc.flag)
			want := tc.want(rw.Root())
			if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
				t.Errorf("ResolveCatalogDirs(%v) = %v; want %v", tc.flag, got, want)
			}
		})
	}
}

// TestResolveCatalogDirsSkipsAConventionalPathThatIsNotADirectory: a `catalog`
// FILE at the workspace root is not a catalog directory, so it contributes
// nothing — handing the loader a path it cannot walk would fail a workspace
// that simply happens to carry a file of that name.
func TestResolveCatalogDirsSkipsAConventionalPathThatIsNotADirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	write(t, filepath.Join(root, workspaceconfig.ConventionalCatalogDir), "not a directory\n")

	if got := resolveIn(t, root).ResolveCatalogDirs(nil); len(got) != 0 {
		t.Errorf("ResolveCatalogDirs(nil) = %v; want an empty stack (a file is not a catalog directory)", got)
	}
}

// TestConfiguredCatalogDirsDistinguishAbsentFromAuthoredEmpty pins the carrier
// of the absent-versus-off-switch distinction at the level the resolver reads
// it: the merged slice is nil when no layer wrote the key and non-nil when a
// layer wrote it as an empty list. It is a separate assertion from the stack
// above because the two failure modes read identically from a resolved stack —
// both produce no local catalog for a workspace with no conventional directory
// — and a merge change that collapsed the two would leave the off-switch rows
// above green on every workspace that has none.
func TestConfiguredCatalogDirsDistinguishAbsentFromAuthoredEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		config  string
		wantNil bool
	}{
		{"absent key", "workspace: {}\n", true},
		{"no config file", "", true},
		{"authored empty list", "workspace:\n  catalog_dirs: []\n", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.config != "" {
				write(t, filepath.Join(root, workspaceconfig.ConfigFilename), tc.config)
			}
			got := resolveIn(t, root).Config().Workspace.CatalogDirs
			if len(got) != 0 {
				t.Fatalf("CatalogDirs = %v; want an empty list in every row", got)
			}
			if (got == nil) != tc.wantNil {
				t.Errorf("CatalogDirs == nil: got %v; want %v — the nil-ness is what separates an absent key from the authored off-switch", got == nil, tc.wantNil)
			}
		})
	}
}
