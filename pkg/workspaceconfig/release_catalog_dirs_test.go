// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseCatalogConfig is a workspace configuration giving four Releases each a
// different kind of entry, over a workspace list of its own, so every row below
// reads one resolution and differs from the others only in the Release it asks
// about.
const releaseCatalogConfig = `workspace:
  catalog_dirs:
    - ./shared
  releases:
    release-own.yaml:
      catalog_dirs:
        - ./variant
    release-adds.yaml:
      catalog_dirs_append:
        - ./extra
    release-both.yaml:
      catalog_dirs:
        - ./variant
      catalog_dirs_append:
        - ./extra
    release-off.yaml:
      catalog_dirs: []
`

// releaseWorkspace authors a workspace root carrying config verbatim, an empty
// document at each named Release file and each named directory, and resolves
// it against a frozen, empty environment.
func releaseWorkspace(t *testing.T, config string, releases, dirs []string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, workspaceconfig.ConfigFilename), config)
	for _, r := range releases {
		write(t, filepath.Join(root, r), "kind: Release\n")
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	return resolveIn(t, root)
}

// sameStack compares two stacks, treating a nil and an empty stack as the same
// statement — the resolver's answer carries no meaning in the difference.
func sameStack(got, want []string) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

// TestResolveCatalogDirsForARelease pins what one resolution answers for each
// Release, by equality over the whole stack: a Release the configuration names
// reads its own directories in place of the workspace's, one it does not name
// reads the workspace's, and a `--catalog` directory is searched ahead of either.
//
// Every row resolves through the real cascade, because an entry's absent list
// and its authored empty list are carried by nil-ness, and only a resolution
// through the loader proves the loader keeps it.
func TestResolveCatalogDirsForARelease(t *testing.T) {
	t.Parallel()
	rw := releaseWorkspace(t, releaseCatalogConfig,
		[]string{"release-own.yaml", "release-adds.yaml", "release-both.yaml", "release-off.yaml", "release-unnamed.yaml"},
		[]string{"shared", "variant", "extra", "catalog", "from-flag"})
	root := rw.Root()
	at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }

	if err := os.Symlink(at("release-own.yaml"), at("linked-release.yaml")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cases := []struct {
		name    string
		release string
		flag    []string
		want    []string
	}{
		{
			name:    "a Release the map names reads its own list, then the conventional directory",
			release: at("release-own.yaml"),
			want:    []string{at("variant"), at("catalog")},
		},
		{
			name:    "a Release the map does not name reads the workspace list",
			release: at("release-unnamed.yaml"),
			want:    []string{at("shared"), at("catalog")},
		},
		{
			name:    "a --catalog directory is searched ahead of a Release's own list",
			release: at("release-own.yaml"),
			flag:    []string{at("from-flag")},
			want:    []string{at("from-flag"), at("variant"), at("catalog")},
		},
		{
			name:    "a --catalog directory is searched ahead of the workspace list for a Release the map does not name",
			release: at("release-unnamed.yaml"),
			flag:    []string{at("from-flag")},
			want:    []string{at("from-flag"), at("shared"), at("catalog")},
		},
		{
			name:    "an entry omitting catalog_dirs keeps the workspace list and adds its own after it",
			release: at("release-adds.yaml"),
			want:    []string{at("shared"), at("extra"), at("catalog")},
		},
		{
			name:    "an entry's catalog_dirs_append follows its own list",
			release: at("release-both.yaml"),
			want:    []string{at("variant"), at("extra"), at("catalog")},
		},
		{
			name:    "an entry's empty list reads no local catalog, the conventional directory included",
			release: at("release-off.yaml"),
			want:    []string{},
		},
		{
			name:    "a --catalog directory survives a Release's off-switch",
			release: at("release-off.yaml"),
			flag:    []string{at("from-flag")},
			want:    []string{at("from-flag")},
		},
		{
			name:    "no Release asks for the workspace's own stack",
			release: "",
			want:    []string{at("shared"), at("catalog")},
		},
		{
			name:    "a Release named through a symbolic link finds the entry of the file it names",
			release: at("linked-release.yaml"),
			want:    []string{at("variant"), at("catalog")},
		},
		{
			name:    "a Release named by an uncleaned spelling finds its entry",
			release: root + string(filepath.Separator) + "." + string(filepath.Separator) + "release-own.yaml",
			want:    []string{at("variant"), at("catalog")},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := stackOf(t, rw, tc.release, tc.flag); !sameStack(got, tc.want) {
				t.Errorf("ResolveCatalogDirs(%q, %v) = %v; want %v", tc.release, tc.flag, got, tc.want)
			}
		})
	}
}

// TestResolveCatalogDirsForARelativeReleaseReadsTheWorkingDirectory pins that a
// relative Release is the file it names from the working directory, as a Release
// named on a command line is. It is its own test because it cannot run in
// parallel with anything that depends on the working directory.
func TestResolveCatalogDirsForARelativeReleaseReadsTheWorkingDirectory(t *testing.T) {
	rw := releaseWorkspace(t, releaseCatalogConfig,
		[]string{"release-own.yaml", "release-adds.yaml", "release-both.yaml", "release-off.yaml"}, []string{"shared", "variant"})
	t.Chdir(rw.Root())
	want := []string{filepath.Join(rw.Root(), "variant")}
	if got := stackOf(t, rw, "release-own.yaml", nil); !sameStack(got, want) {
		t.Errorf("ResolveCatalogDirs(%q, nil) = %v; want %v", "release-own.yaml", got, want)
	}
}

// TestReleaseCatalogEntryNamingNoFileIsRefusedAtLoad drives each refusal the
// entries carry at load, one fault per row, asserting both the condition and
// the whole sentence: a renamed Release, an entry naming a directory, and two
// entries naming one file. The positive control resolves the same shape with the
// file present, so the refusal cannot be satisfied by a loader refusing every
// entry.
func TestReleaseCatalogEntryNamingNoFileIsRefusedAtLoad(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		config   string
		releases []string
		dirs     []string
		want     func(root, cfg string) string
	}{
		{
			name:   "an entry naming a file that does not exist",
			config: "workspace:\n  releases:\n    release-renamed.yaml:\n      catalog_dirs:\n        - ./variant\n",
			want: func(root, cfg string) string {
				return fmt.Sprintf("%s: the workspace.releases entry %q names %s, which does not exist; "+
					"write the path of the Release file this entry is for, relative to this file, or remove the entry: %v",
					cfg, "release-renamed.yaml", filepath.Join(root, "release-renamed.yaml"), workspaceconfig.ErrConfigInvalid())
			},
		},
		{
			name:   "an entry naming a directory",
			config: "workspace:\n  releases:\n    releases:\n      catalog_dirs: []\n",
			dirs:   []string{"releases"},
			want: func(root, cfg string) string {
				return fmt.Sprintf("%s: the workspace.releases entry %q names %s, which is a directory; "+
					"each entry is keyed by one Release file: %v",
					cfg, "releases", filepath.Join(root, "releases"), workspaceconfig.ErrConfigInvalid())
			},
		},
		{
			name: "two entries naming one file",
			config: "workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs: []\n" +
				"    ./release-a.yaml:\n      catalog_dirs: []\n",
			releases: []string{"release-a.yaml"},
			want: func(root, cfg string) string {
				real, err := filepath.EvalSymlinks(filepath.Join(root, "release-a.yaml"))
				if err != nil {
					real = filepath.Join(root, "release-a.yaml")
				}
				return fmt.Sprintf("%s: the workspace.releases entries %q and %q both name %s; "+
					"keep one entry for each Release file: %v",
					cfg, "release-a.yaml", "./release-a.yaml", real, workspaceconfig.ErrConfigInvalid())
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cfg := filepath.Join(root, workspaceconfig.ConfigFilename)
			write(t, cfg, tc.config)
			for _, r := range tc.releases {
				write(t, filepath.Join(root, r), "kind: Release\n")
			}
			for _, d := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", d, err)
				}
			}
			_, err := workspaceconfig.Resolve(root, "", frozenLookup(nil))
			if err == nil {
				t.Fatal("the configuration loaded; want the entry refused")
			}
			if !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
				t.Errorf("the refusal does not match ErrConfigInvalid: %v", err)
			}
			if want := tc.want(root, cfg); err.Error() != want {
				t.Errorf("the refusal reads\n  %s\nwant\n  %s", err.Error(), want)
			}
		})
	}

	t.Run("the positive control: the file is present", func(t *testing.T) {
		t.Parallel()
		rw := releaseWorkspace(t,
			"workspace:\n  releases:\n    release-renamed.yaml:\n      catalog_dirs:\n        - ./variant\n",
			[]string{"release-renamed.yaml"}, []string{"variant"})
		want := []string{filepath.Join(rw.Root(), "variant")}
		if got := stackOf(t, rw, filepath.Join(rw.Root(), "release-renamed.yaml"), nil); !sameStack(got, want) {
			t.Errorf("ResolveCatalogDirs = %v; want %v", got, want)
		}
	})
}

// TestReleaseCatalogEntriesComposeAcrossTheCascade pins that an entry composes
// one Release at a time across configuration files, exactly as the workspace's
// own two keys do: the nearer file's list for a Release replaces the farther
// file's, every file's appended directories are added in cascade order, and a
// Release only the farther file names keeps that file's entry rather than being
// dropped by the nearer file writing entries of its own.
func TestReleaseCatalogEntriesComposeAcrossTheCascade(t *testing.T) {
	t.Parallel()
	global := t.TempDir()
	root := t.TempDir()
	write(t, filepath.Join(root, "release-a.yaml"), "kind: Release\n")
	write(t, filepath.Join(root, "release-b.yaml"), "kind: Release\n")
	// Every directory either file lists is there, because a configured directory
	// where nothing exists is refused, and this test is about how the entries
	// compose rather than about what is on disk.
	for _, dir := range []string{
		filepath.Join(global, "far-variant"), filepath.Join(global, "far-extra"), filepath.Join(global, "far-b"),
		filepath.Join(root, "near-variant"), filepath.Join(root, "near-extra"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	globalCfg := filepath.Join(global, "config.yaml")
	write(t, globalCfg, fmt.Sprintf("workspace:\n  releases:\n"+
		"    %s:\n      catalog_dirs:\n        - %s\n      catalog_dirs_append:\n        - %s\n"+
		"    %s:\n      catalog_dirs:\n        - %s\n",
		filepath.Join(root, "release-a.yaml"), filepath.Join(global, "far-variant"), filepath.Join(global, "far-extra"),
		filepath.Join(root, "release-b.yaml"), filepath.Join(global, "far-b")))
	write(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs:\n        - ./near-variant\n"+
			"      catalog_dirs_append:\n        - ./near-extra\n")

	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{"AGGREGA_CONFIG": globalCfg}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, tc := range []struct {
		release string
		want    []string
	}{
		{
			release: filepath.Join(root, "release-a.yaml"),
			want: []string{
				filepath.Join(rw.Root(), "near-variant"),
				filepath.Join(global, "far-extra"),
				filepath.Join(rw.Root(), "near-extra"),
			},
		},
		{
			release: filepath.Join(root, "release-b.yaml"),
			want:    []string{filepath.Join(global, "far-b")},
		},
	} {
		if got := stackOf(t, rw, tc.release, nil); !sameStack(got, tc.want) {
			t.Errorf("ResolveCatalogDirs(%q, nil) = %v; want %v", tc.release, got, tc.want)
		}
	}
}

// TestLocalCatalogAdviceNoteNamesTheListTheReleaseReads pins that the note on a
// failed lookup tells the reader about the list that applied: the Release's own
// entry where the configuration gives it one, the workspace list otherwise, and
// nothing where the list that applied is the reader's empty one.
func TestLocalCatalogAdviceNoteNamesTheListTheReleaseReads(t *testing.T) {
	t.Parallel()
	rw := releaseWorkspace(t, releaseCatalogConfig,
		[]string{"release-own.yaml", "release-adds.yaml", "release-both.yaml", "release-off.yaml", "release-unnamed.yaml"}, nil)
	at := func(name string) string { return filepath.Join(rw.Root(), name) }

	own := rw.LocalCatalogAdviceNote(at("release-own.yaml"))
	if !strings.Contains(own, "`release-own.yaml`") || !strings.Contains(own, workspaceconfig.ReleaseCatalogsSetting) {
		t.Errorf("the note for a Release with its own list %q does not name the Release and %s", own, workspaceconfig.ReleaseCatalogsSetting)
	}
	if strings.Contains(own, workspaceconfig.CatalogDirsSetting) {
		t.Errorf("the note for a Release with its own list %q names %s, which that Release does not read", own, workspaceconfig.CatalogDirsSetting)
	}

	adds := rw.LocalCatalogAdviceNote(at("release-adds.yaml"))
	if !strings.Contains(adds, workspaceconfig.CatalogDirsSetting) || !strings.Contains(adds, workspaceconfig.ReleaseCatalogsSetting) {
		t.Errorf("the note for a Release keeping the workspace list %q does not name both lists it reads", adds)
	}

	if got := rw.LocalCatalogAdviceNote(at("release-off.yaml")); got != "" {
		t.Errorf("a Release whose own list is empty said %q; it declared no local catalog", got)
	}

	unnamed := rw.LocalCatalogAdviceNote(at("release-unnamed.yaml"))
	if want := rw.LocalCatalogAdviceNote(""); unnamed != want {
		t.Errorf("a Release the map does not name is told %q; want the workspace's own note %q", unnamed, want)
	}
}
