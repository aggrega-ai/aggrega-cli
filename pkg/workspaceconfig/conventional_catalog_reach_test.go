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

// TestTheConventionalDirectoryIsAppendedOnlyWhereNothingNamedReachesItsPlace pins
// the stack builder's answer to whether the workspace's conventional `catalog`
// directory is already read: by the place it leads to, compared with the places
// the named directories lead to — never by the spellings.
//
// The discriminating row is the second: the configuration lists a directory outside
// the workspace and `catalog` is a link to that same place. Compared by spelling,
// the two are different directories, so the conventional one was appended, held to
// the workspace as nobody's naming, and every catalog walk halted on the link whose
// place the configuration lists. Compared by place, the listed directory already
// reads it, so it is not appended and what lies there is read once.
//
// Where it IS appended, it is nobody's naming, so the root it is held to is the
// workspace's — which every row that appends it asserts, so an appended directory
// cannot slip through as named.
func TestTheConventionalDirectoryIsAppendedOnlyWhereNothingNamedReachesItsPlace(t *testing.T) {
	for _, row := range []struct {
		name string
		// lists is the configuration's catalog_dirs, given the workspace root and
		// the directory outside it.
		lists func(ws, shared string) []string
		// typed is a --catalog directory, given the same, or nil.
		typed func(ws, shared string) []string
		// leadsTo is where `catalog` leads, given the same; "" makes it a plain
		// directory inside the workspace.
		leadsTo  func(ws, shared string) string
		appended bool
	}{
		{name: "control: a link to a place nothing named is appended, and held to the workspace",
			lists:   func(ws, _ string) []string { return []string{filepath.Join(ws, "other")} },
			leadsTo: func(_, shared string) string { return shared }, appended: true},
		{name: "a link to the place a listed directory leads to, listed in another spelling, is not appended",
			lists:   func(_, shared string) []string { return []string{shared} },
			leadsTo: func(_, shared string) string { return shared }},
		{name: "a link to a place beneath a listed directory is not appended: the listed walk reads it",
			lists:   func(_, shared string) []string { return []string{shared} },
			leadsTo: func(_, shared string) string { return filepath.Join(shared, "feature") }},
		{name: "a link to a place above a listed directory is appended: it leads further than the listing reaches",
			lists:   func(_, shared string) []string { return []string{filepath.Join(shared, "feature")} },
			leadsTo: func(_, shared string) string { return shared }, appended: true},
		{name: "a link to the place a typed directory leads to is not appended",
			typed:   func(_, shared string) []string { return []string{shared} },
			leadsTo: func(_, shared string) string { return shared }},
		{name: "a plain directory beneath a listed workspace root is not appended: the whole-tree walk reads it",
			lists: func(ws, _ string) []string { return []string{ws} }},
		{name: "a link leading out beneath a listed workspace root is appended: the root does not reach where it leads",
			lists:   func(ws, _ string) []string { return []string{ws} },
			leadsTo: func(_, shared string) string { return shared }, appended: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			shared := filepath.Join(t.TempDir(), "shared")
			for _, dir := range []string{filepath.Join(shared, "feature"), filepath.Join(ws, "other")} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			conventional := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)
			if row.leadsTo == nil {
				if err := os.MkdirAll(conventional, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				link(t, row.leadsTo(ws, shared), conventional)
			}
			config := "workspace: {}\n"
			if row.lists != nil {
				config = "workspace:\n  catalog_dirs:\n"
				for _, dir := range row.lists(ws, shared) {
					config += "    - " + dir + "\n"
				}
			}
			if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			rw, err := workspaceconfig.ResolveWithoutGlobal(ws, ws, func(string) string { return "" })
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			var typed []string
			if row.typed != nil {
				typed = row.typed(ws, shared)
			}

			stack, named := stackOf(t, rw, "", typed), namedOf(t, rw, "", typed)
			if got := slices.Contains(stack, conventional); got != row.appended {
				t.Fatalf("the stack %v holds %s: %v, want %v", stack, conventional, got, row.appended)
			}
			if !row.appended {
				if !slices.Equal(stack, named) {
					t.Errorf("the stack %v is not exactly its named part %v", stack, named)
				}
				return
			}
			if got := workspaceconfig.CatalogDirRoot(ws, conventional, named); got != ws {
				t.Errorf("the appended conventional directory is held to %s, want the workspace %s", got, ws)
			}
		})
	}
}
