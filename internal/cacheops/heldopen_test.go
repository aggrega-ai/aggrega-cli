// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheops

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
)

// The tests in this file hold files open with this process's own handles. A
// handle meets the same refusal another process's would, because Windows refuses
// to remove a file according to how its open handles were opened, not according
// to who holds them; the handles here are opened exactly the way the standard
// library opens every file a reader reads or a lock holder locks.

func writeHeldOpenFixtureFile(t *testing.T, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func heldOpenFixtureExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TestRemoveAllLeavingHeldOpenLeavesOnlyTheOpenFile removes a tree in which one
// file is held open. Windows must leave exactly that file and the directories that
// hold it, count it, and remove everything else; every other platform removes the
// whole tree and reports nothing left, which is os.RemoveAll's own outcome there.
func TestRemoveAllLeavingHeldOpenLeavesOnlyTheOpenFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "subtree")
	held := filepath.Join(root, "a", "held")
	free := filepath.Join(root, "a", "free")
	deep := filepath.Join(root, "b", "c", "free")
	writeHeldOpenFixtureFile(t, held, 7)
	writeHeldOpenFixtureFile(t, free, 11)
	writeHeldOpenFixtureFile(t, deep, 13)

	reader, err := os.Open(held)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	left, err := removeAllLeavingHeldOpen(root)
	if err != nil {
		t.Fatalf("a removal over a file another process holds open failed instead of leaving it: %v", err)
	}
	if runtime.GOOS == "windows" {
		if left.Files != 1 || left.Bytes != 7 {
			t.Fatalf("left %d file(s), %d byte(s); want the one open file and its 7 bytes", left.Files, left.Bytes)
		}
		if !heldOpenFixtureExists(held) {
			t.Fatal("the file held open is gone")
		}
		for _, p := range []string{free, deep, filepath.Join(root, "b")} {
			if heldOpenFixtureExists(p) {
				t.Errorf("%s survived although nothing held it open", p)
			}
		}
	} else {
		if left.Files != 0 || left.Bytes != 0 {
			t.Fatalf("left %d file(s), %d byte(s) on a platform that removes open files", left.Files, left.Bytes)
		}
		if heldOpenFixtureExists(root) {
			t.Fatal("the tree survived a removal that nothing could refuse here")
		}
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	again, err := removeAllLeavingHeldOpen(root)
	if err != nil || again.Files != 0 {
		t.Fatalf("after the last handle closed the removal left %d file(s), err %v", again.Files, err)
	}
	if heldOpenFixtureExists(root) {
		t.Fatal("the tree survived once nothing held any of it open")
	}
}

// TestClearLeavesWhatACompileHoldsAndClearsEverythingElse runs a clear while a
// compile is in progress in another process: it holds its per-guest lock file and
// the reclamation lock file, and is reading one compiled module.
//
// On Windows those three files cannot be removed while they are held, and the
// clear must neither fail nor stop: it leaves them, counts them against the
// compiled-module cache, and goes on to clear the plugin-describe cache after it.
// Everywhere else the held files are removed with the rest, as the clear always
// did. Either way, once nothing holds them, a second clear removes what is left.
func TestClearLeavesWhatACompileHoldsAndClearsEverythingElse(t *testing.T) {
	compileDir := filepath.Join(t.TempDir(), "wasm-compile")
	describeDir := filepath.Join(t.TempDir(), "plugin-describe")
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-"+runtime.GOOS)
	read := filepath.Join(modDir, "module-being-read")
	idle := filepath.Join(modDir, "module-idle")
	writeHeldOpenFixtureFile(t, read, 100)
	writeHeldOpenFixtureFile(t, idle, 40)
	describe := filepath.Join(describeDir, "sha256-aa.json")
	writeHeldOpenFixtureFile(t, describe, 5)

	releaseCompile := filelock.Acquire(filepath.Join(compileDir, ".compile-aa.lock"))
	releaseReclaim := filelock.Acquire(filepath.Join(compileDir, ".compile-reclaim.lock"))
	reader, err := os.Open(read)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = reader.Close()
		releaseReclaim()
		releaseCompile()
	}
	defer release()

	loc := Locations{CompileDir: compileDir, DescribeDir: describeDir}
	res, err := Clear(t.Context(), loc)
	if err != nil {
		t.Fatalf("a clear during a compile failed instead of leaving what the compile holds: %v", err)
	}
	byClass := map[string]ClearedClass{}
	for _, c := range res.Cleared {
		byClass[c.Class] = c
	}
	compiled, ok := byClass[CompiledModuleClassName]
	if !ok {
		t.Fatalf("the clear did not report the compiled-module cache: %+v", res.Cleared)
	}
	if _, ok := byClass["plugin-describe cache"]; !ok {
		t.Fatalf("the clear stopped before the plugin-describe cache: %+v", res.Cleared)
	}
	if heldOpenFixtureExists(describe) || heldOpenFixtureExists(idle) {
		t.Fatal("the clear left a file nothing held open")
	}

	if runtime.GOOS == "windows" {
		if compiled.HeldOpenFiles != 3 || compiled.HeldOpenBytes != 100 {
			t.Fatalf("held open: %d file(s), %d byte(s); want the two lock files and the module being read (100 bytes)",
				compiled.HeldOpenFiles, compiled.HeldOpenBytes)
		}
		if compiled.Size != 40 {
			t.Fatalf("reclaimed %d byte(s) from the compiled-module cache, want only the idle module's 40", compiled.Size)
		}
		if !heldOpenFixtureExists(read) {
			t.Fatal("the module being read is gone")
		}
	} else {
		if compiled.HeldOpenFiles != 0 || compiled.HeldOpenBytes != 0 {
			t.Fatalf("held open: %d file(s) on a platform that removes open files", compiled.HeldOpenFiles)
		}
		if heldOpenFixtureExists(compileDir) {
			t.Fatal("the compiled-module cache survived a clear nothing could refuse here")
		}
	}

	release()
	again, err := Clear(t.Context(), loc)
	if err != nil {
		t.Fatalf("a clear after the compile finished failed: %v", err)
	}
	for _, c := range again.Cleared {
		if c.HeldOpenFiles != 0 {
			t.Errorf("%s still reported %d file(s) held open after every holder released", c.Class, c.HeldOpenFiles)
		}
	}
	if heldOpenFixtureExists(compileDir) {
		t.Fatal("the compiled-module cache survived a clear once nothing held any of it")
	}
}
