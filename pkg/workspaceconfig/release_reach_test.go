// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// releaseReachConfig gives two Releases directories of their own, side by side,
// and a third an addition to the workspace list, so a document's place decides
// which Releases' answers hold it.
const releaseReachConfig = `workspace:
  catalog_dirs:
    - ./shared
  releases:
    release-own.yaml:
      catalog_dirs:
        - ./variant-own
    release-other.yaml:
      catalog_dirs:
        - ./variant-other
    release-adds.yaml:
      catalog_dirs_append:
        - ./extra
`

// TestReleaseReachHoldsWhatTheReleasesStackReads pins the rule an answer given in
// one Release's terms holds documents by: a document under a catalog directory
// only another stack reads is left out, and every other document is held.
//
// Every row asks one resolution about one document, so a row fails on the one
// placement it names. The rows holding a document are as load-bearing as the ones
// leaving one out: a rule that left out everything would satisfy every "not held"
// row, and one that held everything would satisfy the rest.
func TestReleaseReachHoldsWhatTheReleasesStackReads(t *testing.T) {
	t.Parallel()
	rw := releaseWorkspace(t, releaseReachConfig,
		[]string{"release-own.yaml", "release-other.yaml", "release-adds.yaml", "release-unnamed.yaml"},
		[]string{"shared", "variant-own", "variant-other", "variant-other-notes", "extra", "catalog", "loose"})
	root := rw.Root()
	at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }

	cases := []struct {
		name    string
		release string
		flag    []string
		path    string
		want    bool
	}{
		{name: "no Release holds a document under a Release's own directory", release: "", path: at("variant-other", "c.yaml"), want: true},
		{name: "a Release holds a document under its own directory", release: at("release-own.yaml"), path: at("variant-own", "c.yaml"), want: true},
		{name: "a Release leaves out a document under another Release's directory", release: at("release-own.yaml"), path: at("variant-other", "c.yaml"), want: false},
		{name: "a Release whose list replaces the workspace's leaves out the workspace list", release: at("release-own.yaml"), path: at("shared", "c.yaml"), want: false},
		{name: "a Release holds the conventional directory its stack reads", release: at("release-own.yaml"), path: at("catalog", "c.yaml"), want: true},
		{name: "a Release holds a document under no catalog directory", release: at("release-own.yaml"), path: at("loose", "a.yaml"), want: true},
		{name: "a Release holds a document at the workspace root", release: at("release-own.yaml"), path: at("release-own.yaml"), want: true},
		{name: "a Release leaves out a directory only another Release's addition reads", release: at("release-own.yaml"), path: at("extra", "x.yaml"), want: false},
		{name: "a Release keeping the workspace list holds it", release: at("release-adds.yaml"), path: at("shared", "x.yaml"), want: true},
		{name: "a Release keeping the workspace list holds its own addition", release: at("release-adds.yaml"), path: at("extra", "x.yaml"), want: true},
		{name: "a Release keeping the workspace list leaves out another Release's directory", release: at("release-adds.yaml"), path: at("variant-own", "x.yaml"), want: false},
		{name: "a Release the map does not name holds the workspace list", release: at("release-unnamed.yaml"), path: at("shared", "x.yaml"), want: true},
		{name: "a Release the map does not name leaves out every Release's own directory", release: at("release-unnamed.yaml"), path: at("variant-own", "x.yaml"), want: false},
		{name: "a --catalog directory is held even where another Release reads it", release: at("release-own.yaml"), flag: []string{at("variant-other")}, path: at("variant-other", "x.yaml"), want: true},
		{name: "a sibling whose name extends a catalog directory's is not under it", release: at("release-own.yaml"), path: at("variant-other-notes", "x.yaml"), want: true},
		{name: "a path relative to the root is held as its absolute form is", release: at("release-own.yaml"), path: filepath.Join("variant-own", "c.yaml"), want: true},
		{name: "a path relative to the root is left out as its absolute form is", release: at("release-own.yaml"), path: filepath.Join("variant-other", "c.yaml"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reach, err := rw.ReleaseReach(tc.release, tc.flag)
			if err != nil {
				t.Fatalf("ReleaseReach(%q): %v", tc.release, err)
			}
			if got := reach.Holds(tc.path); got != tc.want {
				t.Errorf("Holds(%s) for %q = %v; want %v", tc.path, tc.release, got, tc.want)
			}
		})
	}
}

// TestReleaseReachRefusesAReleaseThatNamesNoFile pins the two refusals a named
// Release is held to, with a positive control: a Release file that is there is
// answered, so the refusals cannot be satisfied by refusing everything.
func TestReleaseReachRefusesAReleaseThatNamesNoFile(t *testing.T) {
	t.Parallel()
	// Every Release the configuration names is written, because a configuration
	// whose entry names a file that is not there is refused when it loads. The
	// workspace then loads, so a row's refusal can only come from the Release that
	// row asks about.
	rw := releaseWorkspace(t, releaseReachConfig,
		[]string{"release-own.yaml", "release-other.yaml", "release-adds.yaml"},
		[]string{"variant-own"})
	root := rw.Root()

	cases := []struct {
		name    string
		release string
		// fault is the phrase the refusal must carry, or empty where none is wanted.
		fault string
	}{
		{name: "an existing Release file is answered", release: filepath.Join(root, "release-own.yaml"), fault: ""},
		{name: "a file that is not there is refused", release: filepath.Join(root, "release-gone.yaml"), fault: "does not exist"},
		{name: "a directory is refused", release: filepath.Join(root, "variant-own"), fault: "is a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := rw.ReleaseReach(tc.release, nil)
			if tc.fault == "" {
				if err != nil {
					t.Fatalf("ReleaseReach(%q) refused an existing Release: %v", tc.release, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ReleaseReach(%q) answered; want a refusal carrying %q", tc.release, tc.fault)
			}
			if !strings.Contains(err.Error(), tc.fault) {
				t.Errorf("ReleaseReach(%q) = %v; want it to carry %q", tc.release, err, tc.fault)
			}
			// The Release is named the way the configuration names it, relative to
			// the root, so the refusal names no location the reader did not write.
			if strings.Contains(err.Error(), root) {
				t.Errorf("the refusal names the workspace root's location: %v", err)
			}
		})
	}
}

// TestCatalogDirsOfEveryStack pins the whole set, by equality and in order: the
// workspace's own stack, then each Release the configuration names, each asked of
// the resolver with the same flag directories.
func TestCatalogDirsOfEveryStack(t *testing.T) {
	t.Parallel()
	rw := releaseWorkspace(t, releaseReachConfig,
		[]string{"release-own.yaml", "release-other.yaml", "release-adds.yaml"},
		[]string{"shared", "variant-own", "variant-other", "extra", "catalog", "from-flag"})
	root := rw.Root()
	at := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }

	cases := []struct {
		name string
		flag []string
		want []string
	}{
		{
			name: "every stack, in the order the configuration names the Releases",
			want: []string{
				at("shared"), at("catalog"),
				at("variant-own"), at("catalog"),
				at("variant-other"), at("catalog"),
				at("shared"), at("extra"), at("catalog"),
			},
		},
		{
			name: "a --catalog directory leads every stack",
			flag: []string{at("from-flag")},
			want: []string{
				at("from-flag"), at("shared"), at("catalog"),
				at("from-flag"), at("variant-own"), at("catalog"),
				at("from-flag"), at("variant-other"), at("catalog"),
				at("from-flag"), at("shared"), at("extra"), at("catalog"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rw.CatalogDirsOfEveryStack(tc.flag); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CatalogDirsOfEveryStack(%v)\n got: %v\nwant: %v", tc.flag, got, tc.want)
			}
		})
	}
}
