// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestReclaimCompileCacheLeavesAnEntryAnotherProcessHasOpen holds the oldest
// entry open, the way the runtime holds an entry while it reads it, and drives a
// trim that must remove one entry to reach the cap.
//
// Each platform's outcome is its own, and neither is a failure. Unix removes an
// open file and lets its holder keep reading, so the oldest entry goes exactly as
// it would unopened. Windows refuses to remove a file another handle holds open,
// so there the pass must leave the oldest entry, report it as held open, and take
// the next-oldest instead, which still brings the cache to its cap. Once the
// handle closes, a later pass reclaims what was left.
//
// The handle is this process's own. It meets the same refusal another process's
// would, because the refusal belongs to how the handle was opened, not to who
// holds it.
func TestReclaimCompileCacheLeavesAnEntryAnotherProcessHasOpen(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "wazero-1.0-amd64-"+runtime.GOOS)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	oldest := writeReclaimLeaf(t, versioned, "leaf-oldest", 400, base)
	middle := writeReclaimLeaf(t, versioned, "leaf-middle", 400, base.Add(time.Hour))
	newest := writeReclaimLeaf(t, versioned, "leaf-newest", 400, base.Add(2*time.Hour))
	prefix := "wazero-1.0-amd64-" + runtime.GOOS + "/"

	reader, err := os.Open(oldest)
	if err != nil {
		t.Fatalf("open the oldest entry: %v", err)
	}
	defer reader.Close()

	res, err := ReclaimCompileCache(dir, 800)
	if err != nil {
		t.Fatalf("a trim over an entry another process has open failed instead of leaving it: %v", err)
	}
	if res.Remaining > 800 {
		t.Fatalf("the cache holds %d bytes after the pass, above the 800-byte cap", res.Remaining)
	}
	if res.Reclaimed+res.Remaining != res.Total {
		t.Fatalf("accounting does not close: reclaimed %d + remaining %d != total %d",
			res.Reclaimed, res.Remaining, res.Total)
	}

	labels := func(leaves []CompileCacheLeaf) []string {
		var out []string
		for _, l := range leaves {
			out = append(out, l.Label)
		}
		return out
	}
	if runtime.GOOS == "windows" {
		if got := labels(res.HeldOpen); len(got) != 1 || got[0] != prefix+"leaf-oldest" {
			t.Fatalf("held open = %v, want only the oldest entry, which another handle has open", got)
		}
		if got := labels(res.Removed); len(got) != 1 || got[0] != prefix+"leaf-middle" {
			t.Fatalf("removed = %v, want the next-oldest entry in place of the one held open", got)
		}
		if !reclaimLeafExists(t, oldest) || reclaimLeafExists(t, middle) || !reclaimLeafExists(t, newest) {
			t.Fatal("on disk the oldest and newest entries should remain and the next-oldest be gone")
		}
	} else {
		if len(res.HeldOpen) != 0 {
			t.Fatalf("held open = %v, but this platform removes an open file", labels(res.HeldOpen))
		}
		if got := labels(res.Removed); len(got) != 1 || got[0] != prefix+"leaf-oldest" {
			t.Fatalf("removed = %v, want the oldest entry, open or not", got)
		}
		if reclaimLeafExists(t, oldest) || !reclaimLeafExists(t, middle) || !reclaimLeafExists(t, newest) {
			t.Fatal("on disk the oldest entry should be gone and the two newer ones remain")
		}
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	again, err := ReclaimCompileCache(dir, 1)
	if err != nil {
		t.Fatalf("a pass after the handle closed failed: %v", err)
	}
	if len(again.HeldOpen) != 0 {
		t.Fatalf("an entry was still reported held open after its last handle closed: %v", labels(again.HeldOpen))
	}
	if again.Remaining != 0 {
		t.Fatalf("a pass to a one-byte cap left %d bytes behind once nothing held an entry open", again.Remaining)
	}
}
