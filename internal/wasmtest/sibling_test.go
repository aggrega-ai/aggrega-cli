// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/siblingrepo"
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

// TestSiblingSkipMessageNamesWhatIsMissing lays a module checkout beside an empty
// directory (what a declared but uninitialised checkout leaves) and no directory
// at all, and checks that the skip message for a missing companion repository
// names the repository, where it was looked for and what the test reads.
func TestSiblingSkipMessageNamesWhatIsMissing(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	for _, dir := range []string{moduleRoot, filepath.Join(parent, "empty")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, repo := range []string{"empty", "absent"} {
		_, err := siblingrepo.Dir(moduleRoot, repo)
		if !siblingrepo.IsAbsent(err) {
			t.Errorf("siblingrepo.Dir(%s) = %v; want an absent checkout", repo, err)
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

// recordingTB is a testing.TB recording whether a helper skipped or failed it,
// and ending the goroutine it runs on as testing.T does.
type recordingTB struct {
	testing.TB
	skipped, failed string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Skipf(format string, args ...any) {
	r.skipped = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// TestASiblingPathThatIsAFileFailsRatherThanSkips lays a regular file where a
// companion repository's checkout would be and checks that requireSiblingRepo fails the test
// naming the path rather than skipping it.
//
// 🔴 PLANTED DEFECT: in requireSiblingRepo (sibling.go), skip on every listing
// error: the file reads as an absent checkout, the helper skips, and the case fails
// on it.
func TestASiblingPathThatIsAFileFailsRatherThanSkips(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", moduleRoot, err)
	}
	file := filepath.Join(parent, "hub")
	if err := os.WriteFile(file, []byte("not a checkout"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}

	rec := &recordingTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		requireSiblingRepo(rec, moduleRoot, "hub", "catalog")
	}()
	<-done
	if rec.skipped != "" {
		t.Errorf("a file at the sibling path skipped the test: %s", rec.skipped)
	}
	if !strings.Contains(rec.failed, "hub is beside this repository at "+file+" but cannot be read as a checkout") {
		t.Errorf("a file at the sibling path failed with %q; want it named as a checkout that cannot be read", rec.failed)
	}
}
