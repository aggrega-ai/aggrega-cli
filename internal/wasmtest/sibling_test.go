// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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
		if !isSiblingAbsent(err) {
			t.Errorf("siblingRepoDir(%s) = %v; want an absent checkout", repo, err)
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
// companion repository's checkout would be and checks that siblingRepoDir does
// not read it as an absent checkout, and that requireSiblingRepo fails the test
// naming the path rather than skipping it.
//
// 🔴 PLANTED DEFECT: in siblingRepoDir (sibling.go), return every listing error as
// a *siblingAbsentError: the file reads as an absent checkout, the helper skips, and
// the case fails on it.
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

	_, err := siblingRepoDir(moduleRoot, "hub")
	if !errors.Is(err, syscall.ENOTDIR) || isSiblingAbsent(err) {
		t.Fatalf("siblingRepoDir over a file = %v; want a not-a-directory error that is not an absent checkout", err)
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

// TestEmbeddedEntitiesSkipOnlyWithoutAggregaHub lays out a module holding no
// generated embedded plugin entity and checks requireEmbeddedEntities in each
// state of the aggrega-hub checkout beside it: absent skips naming aggrega-hub,
// present fails naming the generator, and a generated entity lets the test run.
//
// 🔴 PLANTED DEFECT: in requireEmbeddedEntities (sibling.go), skip whenever no
// entity is generated: the present-checkout case skips instead of failing, and
// this test fails on it.
func TestEmbeddedEntitiesSkipOnlyWithoutAggregaHub(t *testing.T) {
	t.Parallel()
	run := func(moduleRoot string) *recordingTB {
		rec := &recordingTB{TB: t}
		done := make(chan struct{})
		go func() {
			defer close(done)
			requireEmbeddedEntities(rec, moduleRoot)
		}()
		<-done
		return rec
	}
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	entityDir := filepath.Join(moduleRoot, filepath.FromSlash(embeddedEntityDir))
	if err := os.MkdirAll(entityDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", entityDir, err)
	}

	rec := run(moduleRoot)
	if !strings.Contains(rec.skipped, "aggrega-hub, whose plugin overlays they are generated from, is not checked out") || rec.failed != "" {
		t.Errorf("no entity and no aggrega-hub: skipped %q, failed %q; want a skip naming aggrega-hub", rec.skipped, rec.failed)
	}

	hub := filepath.Join(parent, "aggrega-hub")
	if err := os.MkdirAll(filepath.Join(hub, "plugin-overlays"), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", hub, err)
	}
	rec = run(moduleRoot)
	if rec.skipped != "" || !strings.Contains(rec.failed, "generate them with scripts/gen-embedded-plugin-entities.sh") {
		t.Errorf("no entity, aggrega-hub present: skipped %q, failed %q; want a failure naming the generator", rec.skipped, rec.failed)
	}

	if err := os.WriteFile(filepath.Join(entityDir, "helm.plugin.yaml"), []byte("kind: Plugin\n"), 0o644); err != nil {
		t.Fatalf("write entity: %v", err)
	}
	rec = run(moduleRoot)
	if rec.skipped != "" || rec.failed != "" {
		t.Errorf("a generated entity: skipped %q, failed %q; want the test to run", rec.skipped, rec.failed)
	}
}
