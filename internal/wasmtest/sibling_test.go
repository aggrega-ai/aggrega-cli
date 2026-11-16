// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSiblingOfNamesTheCompanionRepositoryAPathReaches lays a module checkout
// with a companion repository beside it and checks which paths siblingOf reads
// as reaching into a companion repository: a path into the companion does, and
// names it and the rest of the path; a path inside the module, the directory
// holding both and a path leaving that directory do not.
//
// 🔴 PLANTED DEFECT: in siblingOf (sibling.go), drop the check that the first
// component is not the module's own directory: a path inside the module reads as
// a companion repository named after the module, and the case fails on it.
func TestSiblingOfNamesTheCompanionRepositoryAPathReaches(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	cases := []struct {
		path     string
		repo     string
		elem     string
		reaching bool
	}{
		{filepath.Join(moduleRoot, "..", "hub", "catalog", "x.yaml"), "hub", filepath.Join("catalog", "x.yaml"), true},
		{filepath.Join(parent, "hub"), "hub", "", true},
		{filepath.Join(moduleRoot, "internal", "testdata"), "", "", false},
		{parent, "", "", false},
		{filepath.Join(parent, "..", "elsewhere"), "", "", false},
	}
	for _, c := range cases {
		repo, elem, ok := siblingOf(moduleRoot, c.path)
		if ok != c.reaching || repo != c.repo || elem != c.elem {
			t.Errorf("siblingOf(%s) = (%q, %q, %v), want (%q, %q, %v)", c.path, repo, elem, ok, c.repo, c.elem, c.reaching)
		}
	}
}

// TestSiblingRepoDirTellsAPresentCheckoutFromAMissingOne lays a module checkout
// beside a populated companion repository, an empty directory (what a declared
// but uninitialised checkout leaves) and no directory at all, and checks that
// only the populated one is present, and that the skip message for a missing one
// names the repository, where it was looked for and what the test reads.
//
// 🔴 PLANTED DEFECT: in siblingRepoDir (sibling.go), drop the empty-directory
// check: the empty directory reads as a present checkout, and the case fails on
// it.
func TestSiblingRepoDirTellsAPresentCheckoutFromAMissingOne(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	for _, dir := range []string{moduleRoot, filepath.Join(parent, "populated"), filepath.Join(parent, "empty")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(parent, "populated", "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("populate: %v", err)
	}

	if dir, err := siblingRepoDir(moduleRoot, "populated"); err != nil || dir != filepath.Join(parent, "populated") {
		t.Errorf("siblingRepoDir(populated) = (%s, %v), want (%s, nil)", dir, err, filepath.Join(parent, "populated"))
	}
	for _, repo := range []string{"empty", "absent"} {
		_, err := siblingRepoDir(moduleRoot, repo)
		if err == nil {
			t.Errorf("siblingRepoDir(%s) found a checkout; want it missing", repo)
			continue
		}
		msg := siblingSkipMessage(repo, filepath.Join("catalog", "x.yaml"), err)
		for _, want := range []string{repo + " is not checked out beside this repository",
			filepath.Join(parent, repo), "this test reads " + repo + "/catalog/x.yaml"} {
			if !strings.Contains(msg, want) {
				t.Errorf("skip message for %s does not say %q: %s", repo, want, msg)
			}
		}
	}
}
