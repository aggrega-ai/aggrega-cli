// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestCatalogDirRootHoldsTheConventionalDirectoryToTheWorkspace pins the one
// answer for which root a catalog directory is held to: the workspace's
// conventional directory, where nobody named it, is the workspace's own however it
// leads, and every directory somebody named — the conventional one included — is
// held to the root its own place picks: the workspace where it lies inside it, the
// directory itself, followed where it was named, where it lies outside.
//
// Two rows discriminate, one each way. The conventional directory being a link
// leading out and named by nobody: judged by where it leads, as a named directory
// is, it would become a root of its own, and a read or a write beneath it would be
// carried wherever the link points. The same directory named: held to the
// workspace, as an unnamed one is, it would refuse what the configuration or the
// command line named. Whether it was named is a question of the place it leads to,
// so the rows naming that place in its own spelling, or a directory above it,
// discriminate a comparison of places from one of spellings.
func TestCatalogDirRootHoldsTheConventionalDirectoryToTheWorkspace(t *testing.T) {
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "shared", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plain, workspaceconfig.ConventionalCatalogDir), 0o755); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	link(t, filepath.Join(outside, "shared"), filepath.Join(linked, workspaceconfig.ConventionalCatalogDir))
	link(t, filepath.Join(outside, "shared"), filepath.Join(linked, "vendored"))
	if err := os.MkdirAll(filepath.Join(linked, "local"), 0o755); err != nil {
		t.Fatal(err)
	}

	conventional := filepath.Join(linked, workspaceconfig.ConventionalCatalogDir)
	rows := []struct {
		name          string
		workspaceRoot string
		dir           string
		named         []string
		want          string
	}{
		{name: "control: the conventional directory inside", workspaceRoot: plain, dir: filepath.Join(plain, "catalog"), want: plain},
		{name: "control: the conventional directory inside, named", workspaceRoot: plain, dir: filepath.Join(plain, "catalog"),
			named: []string{filepath.Join(plain, "catalog")}, want: plain},
		{name: "the conventional directory a link leading out, named by nobody, is still the workspace's",
			workspaceRoot: linked, dir: conventional, want: linked},
		{name: "the conventional directory a link leading out, where only other directories were named, is the workspace's",
			workspaceRoot: linked, dir: conventional, named: []string{filepath.Join(linked, "local")}, want: linked},
		{name: "the conventional directory a link leading out, named, is a root of its own",
			workspaceRoot: linked, dir: conventional, named: []string{filepath.Join(linked, "local"), conventional}, want: conventional},
		{name: "the conventional directory named in another spelling of it is named",
			workspaceRoot: linked, dir: conventional, named: []string{conventional + string(filepath.Separator)}, want: conventional},
		{name: "the conventional directory is named by a directory named at the place it leads to, in that place's own spelling",
			workspaceRoot: linked, dir: conventional, named: []string{filepath.Join(outside, "shared")}, want: conventional},
		{name: "the conventional directory is named by a directory named above the place it leads to",
			workspaceRoot: linked, dir: conventional, named: []string{outside}, want: conventional},
		{name: "a directory named beneath the place the conventional directory leads to does not name it",
			workspaceRoot: linked, dir: conventional, named: []string{filepath.Join(outside, "shared", "sub")}, want: linked},
		{name: "a named directory inside", workspaceRoot: linked, dir: filepath.Join(linked, "local"), want: linked},
		{name: "a named directory spelled inside that is a link leading out is a root of its own", workspaceRoot: linked, dir: filepath.Join(linked, "vendored"), want: filepath.Join(linked, "vendored")},
		{name: "a named directory outside is a root of its own", workspaceRoot: linked, dir: filepath.Join(outside, "shared"), want: filepath.Join(outside, "shared")},
		{name: "with no workspace every directory is a root of its own", workspaceRoot: "", dir: filepath.Join(plain, "catalog"), want: filepath.Join(plain, "catalog")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := workspaceconfig.CatalogDirRoot(row.workspaceRoot, row.dir, row.named); got != row.want {
				t.Errorf("CatalogDirRoot(%s, %s, %q) = %s, want %s", row.workspaceRoot, row.dir, row.named, got, row.want)
			}
		})
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
