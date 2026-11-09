// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// catalogIndexLinksFeature is a Feature a row lays, under its own name so that
// reading it shows up by name.
func catalogIndexLinksFeature(name string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Feature\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\nspec: {}\n"
}

// TestTheCatalogIndexListsNoEntityItNamesAsNotRead drives aggrega/catalogIndex
// over a workspace whose configuration lists catalog directories laid with links,
// one layout per row, and reads back which Features the index lists from those
// directories and which links it names as not read.
//
// The property under test is the pair: an entity the answer names as behind a
// link it did not read is never also listed, and a catalog directory the
// configuration reaches through a link is read and not named. The second row is
// the contradiction this guards — a Feature linked into a catalog directory from
// outside, named as not read by the walk of the workspace's documents while the
// catalog lookup beside it read it through the same link.
func TestTheCatalogIndexListsNoEntityItNamesAsNotRead(t *testing.T) {
	const probe = "probe/linked-feature"
	rows := []struct {
		name string
		// lay builds the row's catalog directories and returns the configuration's
		// catalog_dirs entries, given the workspace root and a directory outside it.
		lay       func(t *testing.T, root, outside string) []string
		wantFound bool
		wantFiles []string
		wantDirs  []string
	}{
		{
			name: "control: a Feature in a configured catalog directory outside the workspace is listed",
			lay: func(t *testing.T, root, outside string) []string {
				plantFile(t, filepath.Join(outside, "cat", "feature.yaml"), catalogIndexLinksFeature(probe))
				return []string{filepath.Join(outside, "cat")}
			},
			wantFound: true, wantFiles: []string{}, wantDirs: []string{},
		},
		{
			name: "a Feature linked into a configured catalog directory from outside is named and not listed",
			lay: func(t *testing.T, root, outside string) []string {
				plantFile(t, filepath.Join(outside, "feature.yaml"), catalogIndexLinksFeature(probe))
				plantDir(t, filepath.Join(root, "cat", "feature"))
				plantLink(t, filepath.Join(outside, "feature.yaml"), filepath.Join(root, "cat", "feature", "linked.yaml"))
				return []string{"./cat"}
			},
			wantFound: false, wantFiles: []string{"cat/feature/linked.yaml"}, wantDirs: []string{},
		},
		{
			name: "a configured catalog directory that is a link leading out is read where it leads, and not named",
			lay: func(t *testing.T, root, outside string) []string {
				plantFile(t, filepath.Join(outside, "cat", "feature.yaml"), catalogIndexLinksFeature(probe))
				plantLink(t, filepath.Join(outside, "cat"), filepath.Join(root, "cat-link"))
				return []string{"./cat-link"}
			},
			wantFound: true, wantFiles: []string{}, wantDirs: []string{},
		},
		{
			name: "a link leading out of a configured catalog directory outside the workspace is not listed",
			lay: func(t *testing.T, root, outside string) []string {
				plantFile(t, filepath.Join(outside, "elsewhere", "feature.yaml"), catalogIndexLinksFeature(probe))
				plantDir(t, filepath.Join(outside, "cat"))
				plantLink(t, filepath.Join(outside, "elsewhere", "feature.yaml"), filepath.Join(outside, "cat", "linked.yaml"))
				return []string{filepath.Join(outside, "cat")}
			},
			wantFound: false, wantFiles: []string{}, wantDirs: []string{},
		},
		{
			name: "a directory link leading out that the configuration does not list is still named",
			lay: func(t *testing.T, root, outside string) []string {
				plantFile(t, filepath.Join(outside, "cat", "feature.yaml"), catalogIndexLinksFeature(probe))
				plantLink(t, filepath.Join(outside, "cat"), filepath.Join(root, "cat-link"))
				plantDir(t, filepath.Join(root, "listed"))
				return []string{"./listed"}
			},
			wantFound: false, wantFiles: []string{}, wantDirs: []string{"cat-link"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			dirs := row.lay(t, root, outside)
			config := "workspace:\n  catalog_dirs:\n"
			for _, dir := range dirs {
				config += fmt.Sprintf("    - %q\n", dir)
			}
			srv := linkedFilesServer(t, root, config)
			// The session lookup is the one a request naming no Release resolves
			// through, built over the configuration's directories as a session builds it.
			named, err := srv.namedCatalogDirsFor("")
			if err != nil {
				t.Fatalf("the workspace's own stack was refused: %v", err)
			}
			srv.lazyCatalog = catalog.NewLazyLookup(root, srv.wsConfig.ArtifactCacheDir, srv.wsConfig.Workspace.CatalogDirs, nil, named...)
			srv.releaseCatalogs = newReleaseCatalogs(srv.wsConfig.Workspace.CatalogDirs, named, srv.lazyCatalog)

			var got catalogIndexResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/catalogIndex", map[string]any{}), &got)
			found := slices.ContainsFunc(got.Entities, func(e CatalogEntity) bool { return e.Name == probe })
			if found != row.wantFound {
				t.Errorf("%s listed: %v, want %v (entities: %+v)", probe, found, row.wantFound, got.Entities)
			}
			if !sameInOrder(got.UnreadLinkedFiles, row.wantFiles) {
				t.Errorf("unreadLinkedFiles = %q; want %q", got.UnreadLinkedFiles, row.wantFiles)
			}
			if !sameInOrder(got.UnreadLinkedDirs, row.wantDirs) {
				t.Errorf("unreadLinkedDirs = %q; want %q", got.UnreadLinkedDirs, row.wantDirs)
			}
		})
	}
}

// TestALinkedIgnoreFileLeadingOutIsNotRead drives aggrega/catalogIndex over a
// workspace whose .gitignore leaves out one of its documents, laid as a plain file
// in the control row and as a link to a file outside the workspace in the other.
//
// The ignore file outside the workspace must decide nothing: its pattern is not
// applied, so the document it names is listed. The control shows the pattern
// applies where the file is the workspace's own, which is what makes the second
// row's listing the link's doing.
func TestALinkedIgnoreFileLeadingOutIsNotRead(t *testing.T) {
	for _, row := range []struct {
		name      string
		lay       func(t *testing.T, root, outside string)
		wantFound bool
	}{
		{
			name: "control: the workspace's own .gitignore leaves the document out",
			lay: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(root, ".gitignore"), "ignored.yaml\n")
			},
			wantFound: false,
		},
		{
			name: "a .gitignore that is a link leading out is not read",
			lay: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(outside, "gitignore"), "ignored.yaml\n")
				plantLink(t, filepath.Join(outside, "gitignore"), filepath.Join(root, ".gitignore"))
			},
			wantFound: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			plantFile(t, filepath.Join(root, "ignored.yaml"), catalogIndexLinksFeature("probe/ignored"))
			row.lay(t, root, outside)
			srv := linkedFilesServer(t, root, "workspace: {}\n")
			var got catalogIndexResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/catalogIndex", map[string]any{}), &got)
			found := slices.ContainsFunc(got.Entities, func(e CatalogEntity) bool { return e.Name == "probe/ignored" })
			if found != row.wantFound {
				t.Errorf("probe/ignored listed: %v, want %v", found, row.wantFound)
			}
		})
	}
}

// TestTheReleaseWalkReadsWhatTheConfigurationReachesThroughALink drives
// aggrega/releaseCandidates over a workspace holding a directory link leading out,
// listed in the configuration as a catalog directory in one row and listed nowhere
// in the other, and reads back the links named.
//
// The Release walk and the entity walk ask one judgement of the configuration, so
// a catalog directory the configuration reaches through a link is named by
// neither, and the same link the configuration does not reach is named by both.
// The judgement compares places, so a listing of the place the link leads to, in
// that place's own spelling, reaches the link as a listing of the link does, and a
// listed directory reaches a link to a directory beneath it, which its own walk
// reads; a link leading above a listed directory reaches further than the listing
// and is named.
func TestTheReleaseWalkReadsWhatTheConfigurationReachesThroughALink(t *testing.T) {
	for _, row := range []struct {
		name   string
		config string
		// lists, where set, replaces config with a listing of this directory
		// beneath the place outside the workspace, in that place's own spelling.
		lists string
		// leadsTo is the directory beneath the place outside the link leads to.
		leadsTo  string
		wantDirs []string
	}{
		{name: "a link the configuration lists as a catalog directory is not named", config: "workspace:\n  catalog_dirs:\n    - ./cat-link\n", leadsTo: "cat", wantDirs: []string{}},
		{name: "the same link listed nowhere is named", config: "workspace: {}\n", leadsTo: "cat", wantDirs: []string{"cat-link"}},
		{name: "the place the link leads to listed in its own spelling: the link is not named", lists: "cat", leadsTo: "cat", wantDirs: []string{}},
		{name: "a link to a directory beneath a listed one is not named", lists: "cat", leadsTo: filepath.Join("cat", "sub"), wantDirs: []string{}},
		{name: "a link to a directory above a listed one is named", lists: filepath.Join("cat", "sub"), leadsTo: "cat", wantDirs: []string{"cat-link"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			plantFile(t, filepath.Join(outside, "cat", "feature.yaml"), catalogIndexLinksFeature("probe/linked-feature"))
			plantFile(t, filepath.Join(outside, "cat", "sub", "feature.yaml"), catalogIndexLinksFeature("probe/beneath-feature"))
			plantLink(t, filepath.Join(outside, row.leadsTo), filepath.Join(root, "cat-link"))
			writeRelease(t, root, "release.yaml", "release/inside")
			config := row.config
			if row.lists != "" {
				config = "workspace:\n  catalog_dirs:\n    - " + filepath.Join(outside, row.lists) + "\n"
			}
			srv := linkedFilesServer(t, root, config)
			var got struct {
				UnreadLinkedDirs []string `json:"unreadLinkedDirs"`
			}
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/releaseCandidates", map[string]any{}), &got)
			if !sameInOrder(got.UnreadLinkedDirs, row.wantDirs) {
				t.Errorf("unreadLinkedDirs = %q; want %q", got.UnreadLinkedDirs, row.wantDirs)
			}
		})
	}
}
