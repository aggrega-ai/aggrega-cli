// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestWholeCatalogPasses pins which passes a whole-catalog check runs, by
// equality over every pass's Releases and directories.
//
// The rows that matter most are the ones a union would get wrong: two variants a
// workspace keeps side by side are two passes, never one over both directories;
// and the ones that keep the single-catalog answer unchanged: a workspace naming
// no Release under `workspace.releases` is one pass naming no Release, whatever
// Releases it holds.
func TestWholeCatalogPasses(t *testing.T) {
	t.Parallel()

	const variants = `workspace:
  releases:
    release-a.yaml:
      catalog_dirs:
        - ./variant-a
    release-b.yaml:
      catalog_dirs:
        - ./variant-b
    release-same.yaml:
      catalog_dirs:
        - ./variant-a
    release-off.yaml:
      catalog_dirs: []
`
	const sharedAndAppend = `workspace:
  catalog_dirs:
    - ./shared
  releases:
    release-replaces.yaml:
      catalog_dirs:
        - ./variant-a
    release-appends.yaml:
      catalog_dirs_append:
        - ./extra
`
	const noReleases = "workspace:\n  catalog_dirs:\n    - ./shared\n"
	const allOff = `workspace:
  releases:
    release-a.yaml:
      catalog_dirs: []
    release-b.yaml:
      catalog_dirs: []
`

	type pass struct {
		releases []string
		dirs     []string
	}
	cases := []struct {
		name     string
		config   string
		files    []string
		dirs     []string
		releases []string // what the caller reports the workspace holds; nil asks nothing
		flag     []string
		want     func(at func(...string) string) []pass
	}{
		{
			name:     "a workspace naming no Release is one pass over its own stack, naming no Release",
			config:   noReleases,
			files:    []string{"release-a.yaml", "release-b.yaml"},
			dirs:     []string{"shared"},
			releases: []string{"release-a.yaml", "release-b.yaml"},
			want: func(at func(...string) string) []pass {
				return []pass{{dirs: []string{at("shared")}}}
			},
		},
		{
			name:     "each Release with its own variant is a pass of its own, never one over the union",
			config:   variants,
			files:    []string{"release-a.yaml", "release-b.yaml", "release-same.yaml", "release-off.yaml"},
			dirs:     []string{"variant-a", "variant-b"},
			releases: []string{"release-a.yaml", "release-b.yaml"},
			want: func(at func(...string) string) []pass {
				return []pass{
					{releases: []string{"release-a.yaml"}, dirs: []string{at("variant-a")}},
					{releases: []string{"release-b.yaml"}, dirs: []string{at("variant-b")}},
				}
			},
		},
		{
			name:     "Releases reading identical directories share one pass that names each of them",
			config:   variants,
			files:    []string{"release-a.yaml", "release-b.yaml", "release-same.yaml", "release-off.yaml"},
			dirs:     []string{"variant-a", "variant-b"},
			releases: []string{"release-a.yaml", "release-b.yaml", "release-same.yaml"},
			want: func(at func(...string) string) []pass {
				return []pass{
					{releases: []string{"release-a.yaml", "release-same.yaml"}, dirs: []string{at("variant-a")}},
					{releases: []string{"release-b.yaml"}, dirs: []string{at("variant-b")}},
				}
			},
		},
		{
			name:     "a Release whose entry switches its catalog off is a pass of its own that reads nothing",
			config:   variants,
			files:    []string{"release-a.yaml", "release-b.yaml", "release-same.yaml", "release-off.yaml"},
			dirs:     []string{"variant-a", "variant-b"},
			releases: []string{"release-a.yaml", "release-off.yaml"},
			want: func(at func(...string) string) []pass {
				return []pass{
					{releases: []string{"release-a.yaml"}, dirs: []string{at("variant-a")}},
					{releases: []string{"release-off.yaml"}, dirs: nil},
				}
			},
		},
		{
			name:     "the --catalog directories are prepended to every pass",
			config:   variants,
			files:    []string{"release-a.yaml", "release-b.yaml", "release-same.yaml", "release-off.yaml"},
			dirs:     []string{"variant-a", "variant-b", "flagged"},
			releases: []string{"release-a.yaml", "release-b.yaml", "release-off.yaml"},
			flag:     []string{"FLAG"},
			want: func(at func(...string) string) []pass {
				return []pass{
					{releases: []string{"release-a.yaml"}, dirs: []string{at("flagged"), at("variant-a")}},
					{releases: []string{"release-b.yaml"}, dirs: []string{at("flagged"), at("variant-b")}},
					{releases: []string{"release-off.yaml"}, dirs: []string{at("flagged")}},
				}
			},
		},
		{
			name:     "an entry writing catalog_dirs replaces the workspace list; one writing only catalog_dirs_append keeps it",
			config:   sharedAndAppend,
			files:    []string{"release-replaces.yaml", "release-appends.yaml", "release-unnamed.yaml"},
			dirs:     []string{"shared", "variant-a", "extra"},
			releases: []string{"release-appends.yaml", "release-replaces.yaml", "release-unnamed.yaml"},
			want: func(at func(...string) string) []pass {
				return []pass{
					{releases: []string{"release-appends.yaml"}, dirs: []string{at("shared"), at("extra")}},
					{releases: []string{"release-replaces.yaml"}, dirs: []string{at("variant-a")}},
					{releases: []string{"release-unnamed.yaml"}, dirs: []string{at("shared")}},
				}
			},
		},
		{
			name:     "a Release spelled by its absolute path is matched by the file it names and keeps its spelling",
			config:   variants,
			files:    []string{"release-a.yaml", "release-b.yaml", "release-same.yaml", "release-off.yaml"},
			dirs:     []string{"variant-a", "variant-b"},
			releases: []string{"ABS:release-b.yaml"},
			want: func(at func(...string) string) []pass {
				return []pass{{releases: []string{at("release-b.yaml")}, dirs: []string{at("variant-b")}}}
			},
		},
		{
			name:     "a workspace naming Releases that holds none is the single workspace pass",
			config:   variants,
			files:    []string{"release-a.yaml", "release-b.yaml", "release-same.yaml", "release-off.yaml"},
			dirs:     []string{"variant-a", "variant-b"},
			releases: []string{},
			want: func(at func(...string) string) []pass {
				return []pass{{dirs: nil}}
			},
		},
		{
			name:     "every Release switched off is one pass per stack, each reading nothing",
			config:   allOff,
			files:    []string{"release-a.yaml", "release-b.yaml"},
			releases: []string{"release-a.yaml", "release-b.yaml"},
			want: func(at func(...string) string) []pass {
				return []pass{{releases: []string{"release-a.yaml", "release-b.yaml"}, dirs: nil}}
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw := releaseWorkspace(t, tc.config, tc.files, tc.dirs)
			root := rw.Root()
			at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }

			releases := make([]string, 0, len(tc.releases))
			for _, r := range tc.releases {
				if rest, ok := strings.CutPrefix(r, "ABS:"); ok {
					r = at(rest)
				}
				releases = append(releases, r)
			}
			var flag []string
			for _, f := range tc.flag {
				if f == "FLAG" {
					f = at("flagged")
				}
				flag = append(flag, f)
			}

			got := rw.WholeCatalogPasses(func() []string { return releases }, flag)
			want := tc.want(at)
			if len(got) != len(want) {
				t.Fatalf("WholeCatalogPasses returned %d pass(es) %+v; want %d %+v", len(got), got, len(want), want)
			}
			for i := range want {
				if !reflect.DeepEqual(got[i].Releases, want[i].releases) && (len(got[i].Releases) != 0 || len(want[i].releases) != 0) {
					t.Errorf("pass %d names %v; want %v", i, got[i].Releases, want[i].releases)
				}
				if !sameStack(got[i].Dirs, want[i].dirs) {
					t.Errorf("pass %d reads %v; want %v", i, got[i].Dirs, want[i].dirs)
				}
				if got[i].NamesReleases() != (len(want[i].releases) > 0) {
					t.Errorf("pass %d NamesReleases() = %v; want %v", i, got[i].NamesReleases(), len(want[i].releases) > 0)
				}
			}
		})
	}
}

// TestWholeCatalogPassesAsksNothingOfAWorkspaceNamingNoRelease pins that the
// Release list is not asked for where it cannot change the answer. A caller
// enumerates a workspace's Releases by walking it, so asking anyway would make
// every whole-catalog check in a single-catalog workspace pay for a walk whose
// result is thrown away. The positive control asks the same question of a
// workspace that does name a Release, where the list is needed.
func TestWholeCatalogPassesAsksNothingOfAWorkspaceNamingNoRelease(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		config  string
		wantAsk bool
	}{
		{name: "no workspace.releases", config: "workspace:\n  catalog_dirs:\n    - ./shared\n", wantAsk: false},
		{name: "workspace.releases names one", config: "workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs:\n        - ./shared\n", wantAsk: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw := releaseWorkspace(t, tc.config, []string{"release-a.yaml"}, []string{"shared"})
			asked := false
			rw.WholeCatalogPasses(func() []string { asked = true; return []string{"release-a.yaml"} }, nil)
			if asked != tc.wantAsk {
				t.Errorf("the Release list was asked for = %v; want %v", asked, tc.wantAsk)
			}
		})
	}
}

// TestAnyCatalogPassReads pins the predicate both whole-catalog refusals key on:
// a run refuses only when no pass reads a directory, so one pass with nothing to
// read beside one that reads something is a run, not a refusal.
func TestAnyCatalogPassReads(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		passes []workspaceconfig.CatalogPass
		want   bool
	}{
		{name: "no pass", passes: nil, want: false},
		{name: "one pass reading nothing", passes: []workspaceconfig.CatalogPass{{}}, want: false},
		{name: "every pass reading nothing", passes: []workspaceconfig.CatalogPass{{Releases: []string{"a"}}, {Releases: []string{"b"}, Dirs: []string{}}}, want: false},
		{name: "one pass reading something beside one reading nothing", passes: []workspaceconfig.CatalogPass{{Releases: []string{"a"}}, {Releases: []string{"b"}, Dirs: []string{"/d"}}}, want: true},
	}
	for _, tc := range cases {
		if got := workspaceconfig.AnyCatalogPassReads(tc.passes); got != tc.want {
			t.Errorf("%s: AnyCatalogPassReads = %v; want %v", tc.name, got, tc.want)
		}
	}
}

// TestCatalogPassHeadingAndSettingWords pins the two sentences every surface
// reads for a per-Release report, by equality, since a wording drifting on one
// surface and not another is what these two functions exist to prevent. The
// single-catalog settings clause is pinned to the bytes a workspace naming no
// Release has always been told.
func TestCatalogPassHeadingAndSettingWords(t *testing.T) {
	t.Parallel()
	headings := []struct {
		releases, dirs []string
		want           string
	}{
		{[]string{"release-a.yaml"}, []string{"catalog-a"}, "Release release-a.yaml reads catalog-a"},
		{[]string{"a.yaml", "b.yaml"}, []string{"x", "y"}, "Releases a.yaml, b.yaml read x, y"},
		{[]string{"release-off.yaml"}, nil, "Release release-off.yaml reads no catalog directory, so there is nothing to check for it"},
		{[]string{"a.yaml", "b.yaml"}, []string{}, "Releases a.yaml, b.yaml read no catalog directory, so there is nothing to check for them"},
	}
	for _, h := range headings {
		if got := workspaceconfig.CatalogPassHeading(h.releases, h.dirs); got != h.want {
			t.Errorf("CatalogPassHeading(%v, %v) = %q; want %q", h.releases, h.dirs, got, h.want)
		}
	}
	if got, want := workspaceconfig.CatalogDirsSettingWords(false), "`workspace.catalog_dirs` in .aggrega-config.yaml"; got != want {
		t.Errorf("CatalogDirsSettingWords(false) = %q; want %q", got, want)
	}
	if got, want := workspaceconfig.CatalogDirsSettingWords(true),
		"`workspace.catalog_dirs`, or a Release's own `catalog_dirs` under `workspace.releases`, in .aggrega-config.yaml"; got != want {
		t.Errorf("CatalogDirsSettingWords(true) = %q; want %q", got, want)
	}
}
