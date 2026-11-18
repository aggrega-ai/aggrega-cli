// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmtest

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/siblingrepo"
)

// SiblingPath returns the absolute path of elem inside the repository named repo
// checked out beside this one, in the directory that holds this module's
// checkout. When repo is not checked out there it skips t, naming the
// repository, where it was looked for and what the test reads from it: the
// repository is a companion this repository's own clone does not carry, so its
// absence is a partial checkout, not a defect.
//
// Only the repository's absence skips. A path there that cannot be read as a
// directory — a file in its place, a directory the test may not list — fails t.
// A repository that is present but lacks elem, or holds it in a shape the test
// cannot read, fails the test where the test reads it, so a broken companion
// checkout never reads as a pass.
func SiblingPath(t testing.TB, repo string, elem ...string) string {
	t.Helper()
	dir := requireSiblingRepo(t, ModuleRoot(t), repo, filepath.Join(elem...))
	return filepath.Join(append([]string{dir}, elem...)...)
}

// SkipWithoutSiblings skips t when any of paths reaches into a repository
// checked out beside this one that is not checked out, with the message
// [SiblingPath] gives. A path is read relative to the test's working directory,
// as the test itself reads it. A path inside this module, or one leaving the
// directory that holds this module's checkout, is not a companion repository's
// and is left alone.
//
// It is for a test handing a list of paths to a loader, where some of the paths
// are a companion repository's and some are this module's own testdata.
func SkipWithoutSiblings(t testing.TB, paths ...string) {
	t.Helper()
	moduleRoot := ModuleRoot(t)
	for _, p := range paths {
		repo, elem, ok := siblingOf(moduleRoot, p)
		if !ok {
			continue
		}
		requireSiblingRepo(t, moduleRoot, repo, elem)
	}
}

// siblingOf reports the companion repository path p reaches into, and the rest
// of p inside it, when p names a location in a directory beside moduleRoot.
func siblingOf(moduleRoot, p string) (repo, elem string, ok bool) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", "", false
	}
	rel, err := filepath.Rel(filepath.Dir(moduleRoot), abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	repo, elem, _ = strings.Cut(rel, string(filepath.Separator))
	if repo == filepath.Base(moduleRoot) {
		return "", "", false
	}
	return repo, elem, true
}

// requireSiblingRepo returns the directory of the repository repo beside
// moduleRoot for a test reading elem from it. It skips t when the repository is
// not checked out and fails t when its path is there and cannot be read.
func requireSiblingRepo(t testing.TB, moduleRoot, repo, elem string) string {
	t.Helper()
	dir, err := siblingrepo.Dir(moduleRoot, repo)
	switch {
	case siblingrepo.IsAbsent(err):
		t.Skipf("%s", siblingSkipMessage(repo, elem, err))
	case err != nil:
		t.Fatalf("%s is beside this repository at %s but cannot be read as a checkout: %v", repo, dir, err)
	}
	return dir
}

// siblingSkipMessage is the skip message for a test reading elem from the
// companion repository repo, which siblingrepo.Dir found missing with err.
func siblingSkipMessage(repo, elem string, err error) string {
	what := repo
	if elem != "" {
		what = repo + "/" + filepath.ToSlash(elem)
	}
	return fmt.Sprintf("%s is not checked out beside this repository (%v); this test reads %s, so check out %s "+
		"beside this repository to run it", repo, err, what, repo)
}
