// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pathscope_test

import (
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// walkedEntry is one call a walk made of its function: the path, spelled
// relative to the base the test laid its tree under, and what the entry was.
type walkedEntry struct {
	path      string
	dir, link bool
}

// walkFrom walks root with pathscope.WalkDir and returns each call it made, the
// paths spelled relative to base, plus the name the root's own entry carried.
func walkFrom(t *testing.T, base, root string) ([]walkedEntry, string) {
	t.Helper()
	var got []walkedEntry
	rootName := ""
	err := pathscope.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatalf("the walk met an error at %s: %v", path, err)
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			t.Fatalf("a path the walk handed on is not beneath the laid tree: %s", path)
		}
		if path == root {
			rootName = d.Name()
		}
		got = append(got, walkedEntry{path: filepath.ToSlash(rel), dir: d.IsDir(), link: d.Type()&fs.ModeSymlink != 0})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", root, err)
	}
	return got, rootName
}

// TestWalkDirWalksARootThatIsALinkWhereItLeads drives the one walk of a tree
// beneath a root over each shape a root can take. A directory, and a link to one
// — absolute or relative — are walked alike: the same entries, in the same order,
// every path spelled beneath the root as it was passed, the root handed on as
// given with the entry of the directory it leads to, named as the root names it.
// A link beneath the root is handed on as the link it is and not entered, whether
// it leads inside or out. A root that is a link leading nowhere, or to a file, is
// walked as it stands: the one entry, the link itself.
//
// The first row is the control: a plain directory, walked as filepath.WalkDir
// walks it, which is what makes a link row's difference the link's doing.
func TestWalkDirWalksARootThatIsALinkWhereItLeads(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	mustMkdir(t, filepath.Join(realDir, "sub"))
	mustWrite(t, filepath.Join(realDir, "a.yaml"))
	mustWrite(t, filepath.Join(realDir, "sub", "b.yaml"))
	outside := filepath.Join(base, "outside")
	mustMkdir(t, outside)
	mustWrite(t, filepath.Join(outside, "c.yaml"))
	// Beneath the root: a link to a directory inside it, and one to a directory
	// outside it. Neither is entered.
	mustLink(t, filepath.Join(realDir, "sub"), filepath.Join(realDir, "inner-link"))
	mustLink(t, outside, filepath.Join(realDir, "out-link"))
	mustLink(t, realDir, filepath.Join(base, "via"))
	mustLink(t, "real", filepath.Join(base, "viarel"))
	mustLink(t, filepath.Join(base, "missing"), filepath.Join(base, "dangling"))
	mustLink(t, filepath.Join(realDir, "a.yaml"), filepath.Join(base, "filelink"))

	// The tree beneath a directory root, each path spelled beneath that root.
	beneath := func(root string) []walkedEntry {
		return []walkedEntry{
			{path: root, dir: true},
			{path: root + "/a.yaml"},
			{path: root + "/inner-link", link: true},
			{path: root + "/out-link", link: true},
			{path: root + "/sub", dir: true},
			{path: root + "/sub/b.yaml"},
		}
	}
	for _, row := range []struct {
		name     string
		root     string
		want     []walkedEntry
		wantName string
	}{
		{name: "control: a directory", root: "real", want: beneath("real"), wantName: "real"},
		{name: "a root that is a link to a directory", root: "via", want: beneath("via"), wantName: "via"},
		{name: "a root that is a relative link to a directory", root: "viarel", want: beneath("viarel"), wantName: "viarel"},
		{name: "a root that is a link leading nowhere is walked as it stands", root: "dangling",
			want: []walkedEntry{{path: "dangling", link: true}}, wantName: "dangling"},
		{name: "a root that is a link to a file is walked as it stands", root: "filelink",
			want: []walkedEntry{{path: "filelink", link: true}}, wantName: "filelink"},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := filepath.Join(base, row.root)
			got, gotName := walkFrom(t, base, root)
			if !slices.Equal(got, row.want) {
				t.Errorf("walked %+v\nwant   %+v", got, row.want)
			}
			if gotName != row.wantName {
				t.Errorf("the root's entry is named %q; want %q, the name the root is spelled with", gotName, row.wantName)
			}
		})
	}
}

// TestWalkDirStopsWhereItsFunctionSaysSo holds a walk of a root that is a link to
// filepath.WalkDir's own answers from fn: SkipDir at the root walks nothing
// beneath it, and SkipAll ends the walk; neither is returned as an error.
func TestWalkDirStopsWhereItsFunctionSaysSo(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	mustMkdir(t, filepath.Join(realDir, "sub"))
	mustWrite(t, filepath.Join(realDir, "a.yaml"))
	mustWrite(t, filepath.Join(realDir, "sub", "b.yaml"))
	via := filepath.Join(base, "via")
	mustLink(t, realDir, via)

	for _, row := range []struct {
		name string
		stop func(path string) error
		want []string
	}{
		{"SkipDir at the root walks nothing beneath it", func(path string) error {
			if path == via {
				return fs.SkipDir
			}
			return nil
		}, []string{"via"}},
		{"SkipAll at the first file ends the walk", func(path string) error {
			if filepath.Base(path) == "a.yaml" {
				return fs.SkipAll
			}
			return nil
		}, []string{"via", "via/a.yaml"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			var got []string
			err := pathscope.WalkDir(via, func(path string, _ fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(base, path)
				got = append(got, filepath.ToSlash(rel))
				return row.stop(path)
			})
			if err != nil {
				t.Fatalf("the walk returned %v; a skip is not an error", err)
			}
			if !slices.Equal(got, row.want) {
				t.Errorf("walked %v; want %v", got, row.want)
			}
		})
	}
}
