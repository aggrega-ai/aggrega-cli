// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// What the three reclamation verbs print when another process holds a file they
// would remove. Windows refuses to remove a file another handle holds open, and
// each verb must then leave that file, say so, and finish the rest of its work;
// every other platform removes the file and prints what it always printed.
//
// The handles are this process's own. They meet the same refusal another
// process's would, because the refusal belongs to how a handle was opened, not to
// who holds it, and they are opened the way the standard library opens every file
// a reader reads or a lock holder locks.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// elementLine finds the element the shared class assembly enumerates for target
// and spells the line a held-open report carries for it. The class, label and
// reach come from the shared layer rather than being written here, so this
// reading cannot drift from what the enumeration calls the element.
func elementLine(t *testing.T, loc cacheops.Locations, target string) string {
	t.Helper()
	want, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat %s: %v", target, err)
	}
	classes, err := cacheops.Collect(t.Context(), loc)
	if err != nil {
		t.Fatalf("the shared class assembly refused: %v", err)
	}
	for _, cl := range classes {
		for _, el := range cl.Elements {
			if got, err := os.Stat(el.Path); err == nil && os.SameFile(got, want) {
				return "left in place " + cl.Name + ": " + el.Label + " [" + string(cl.Reach) + "], open in another process"
			}
		}
	}
	t.Fatalf("no enumerated element is %s", target)
	return ""
}

// holdOpen opens each path for reading and returns the function that closes
// them all.
func holdOpen(t *testing.T, paths ...string) func() {
	t.Helper()
	var files []*os.File
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			for _, g := range files {
				_ = g.Close()
			}
			t.Fatalf("open %s: %v", p, err)
		}
		files = append(files, f)
	}
	return func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
}

// TestCachePruneLeavesWhatAnotherProcessHasOpen prunes a workspace while three
// unreferenced elements are held open: a compiled module the attribution index
// ties to no pinned plugin, an external-input blob, and the blob inside an
// entity-cache slot. The prune must finish either way; on Windows it names the
// three as left in place and keeps the compiled module's attribution, and
// elsewhere it removes them as it removes every unreferenced element.
func TestCachePruneLeavesWhatAnotherProcessHasOpen(t *testing.T) {
	fx := newCacheFixture(t)
	modDir := filepath.Join(fx.loc.CompileDir, "wazero-1.0-amd64-linux")
	orphanKey := "wazero-1.0-amd64-linux/guestorphan0"
	orphanLeaf := writeCompileLeaf(t, modDir, "guestorphan0", 900, time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC))
	fx.loc.AttribIndexPath = filepath.Join(t.TempDir(), "compile-attribution-index.json")
	if err := attribindex.RecordGuestLeaf(fx.loc.AttribIndexPath, orphanKey, 900, digest.ContentDigest([]byte("orphan-guest"))); err != nil {
		t.Fatalf("record the orphan compiled module: %v", err)
	}
	entityBlob := filepath.Join(fx.unrefEntitySlot, "blob")
	wantLines := []string{
		elementLine(t, fx.loc, orphanLeaf),
		elementLine(t, fx.loc, fx.unrefInputBlob),
		elementLine(t, fx.loc, fx.unrefEntitySlot),
		"3 element(s) left in place because another process has them open",
	}

	closeAll := holdOpen(t, orphanLeaf, fx.unrefInputBlob, entityBlob)
	defer closeAll()

	var out bytes.Buffer
	if err := runCachePrune(t.Context(), &out, fx.loc); err != nil {
		t.Fatalf("a prune over elements another process has open failed instead of leaving them: %v", err)
	}
	s := out.String()
	t.Logf("prune output:\n%s", s)

	// What nothing held open is pruned on every platform.
	for _, p := range []string{fx.unrefSourceSlot, fx.unrefDescribe} {
		if exists(p) {
			t.Errorf("prune retained an unreferenced element nothing held open: %s", p)
		}
	}

	ix, err := attribindex.Load(fx.loc.AttribIndexPath)
	if err != nil {
		t.Fatalf("load the attribution index: %v", err)
	}
	_, stillAttributed := ix.Entry(orphanKey)

	if runtime.GOOS == "windows" {
		for _, line := range wantLines {
			if !strings.Contains(s, line) {
				t.Errorf("prune output does not carry %q", line)
			}
		}
		for _, p := range []string{orphanLeaf, fx.unrefInputBlob, entityBlob} {
			if !exists(p) {
				t.Errorf("a file another process holds open is gone: %s", p)
			}
		}
		if !stillAttributed {
			t.Error("the attribution index dropped a compiled module the prune left in place")
		}
	} else {
		if strings.Contains(s, "left in place") {
			t.Errorf("prune reported an element left in place on a platform that removes open files:\n%s", s)
		}
		for _, p := range []string{orphanLeaf, fx.unrefInputBlob, fx.unrefEntitySlot} {
			if exists(p) {
				t.Errorf("prune retained an unreferenced element: %s", p)
			}
		}
		if !strings.Contains(s, "removed compiled-module cache: "+orphanKey) {
			t.Errorf("prune did not name the removed compiled module:\n%s", s)
		}
		if stillAttributed {
			t.Error("the attribution index still holds a compiled module the prune removed")
		}
	}
}

// TestCacheEvictLeavesALeafAnotherProcessHasOpen trims while the oldest leaf is
// held open. On Windows the trim names it as left in place and evicts the
// next-oldest instead; elsewhere it evicts the oldest as it always does.
func TestCacheEvictLeavesALeafAnotherProcessHasOpen(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oldest := writeCompileLeaf(t, modDir, "leaf-old-1", 100, base)
	writeCompileLeaf(t, modDir, "leaf-old-2", 100, base.Add(time.Hour))
	writeCompileLeaf(t, modDir, "leaf-new", 100, base.Add(2*time.Hour))

	closeAll := holdOpen(t, oldest)
	defer closeAll()

	var out bytes.Buffer
	// Total is 300 B; a 200 B cap needs one leaf removed.
	if err := runCacheEvict(t.Context(), &out, cacheops.Locations{CompileDir: compileDir}, 200); err != nil {
		t.Fatalf("an eviction over a leaf another process has open failed instead of leaving it: %v", err)
	}
	s := out.String()
	t.Logf("evict output:\n%s", s)

	if runtime.GOOS == "windows" {
		for _, line := range []string{
			"left in place compiled-module cache: wazero-1.0-amd64-linux/leaf-old-1 [machine], open in another process",
			"evicted wazero-1.0-amd64-linux/leaf-old-2 (100 B) [machine]",
			"evicted 1 leaf(s), 100 B reclaimed; 200 B retained (cap 200 B)",
		} {
			if !strings.Contains(s, line) {
				t.Errorf("evict output does not carry %q", line)
			}
		}
		if !exists(oldest) {
			t.Error("the leaf another process holds open is gone")
		}
	} else {
		if strings.Contains(s, "left in place") {
			t.Errorf("evict reported a leaf left in place on a platform that removes open files:\n%s", s)
		}
		if !strings.Contains(s, "evicted wazero-1.0-amd64-linux/leaf-old-1 (100 B) [machine]") {
			t.Errorf("evict did not remove the oldest leaf:\n%s", s)
		}
	}
}

// TestCacheClearLeavesTheLockACompileHolds clears while a compile in another
// process holds its per-guest lock file. On Windows the clear succeeds, clears
// every class, and says what it left; elsewhere it removes the lock file with
// the rest and says nothing of it.
func TestCacheClearLeavesTheLockACompileHolds(t *testing.T) {
	fx := newCacheFixture(t)
	release := filelock.Acquire(fx.compileLock)
	defer release()

	var out bytes.Buffer
	if err := runCacheClear(t.Context(), &out, fx.loc); err != nil {
		t.Fatalf("a clear during a compile failed instead of leaving the compile's lock: %v", err)
	}
	s := out.String()
	t.Logf("clear output:\n%s", s)

	if exists(fx.compileModule) || exists(fx.loc.DescribeDir) {
		t.Errorf("clear left content nothing held open:\n%s", s)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(s, "left in place: 1 file(s) (0 B) another process has open") {
			t.Errorf("clear did not say it left the held lock file:\n%s", s)
		}
		if !exists(fx.compileLock) {
			t.Error("the lock file a compile holds is gone")
		}
	} else if strings.Contains(s, "left in place") {
		t.Errorf("clear reported a file left in place on a platform that removes open files:\n%s", s)
	}
}
