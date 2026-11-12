// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestACompileCacheDirectoryThatIsALinkIsWalkedWhereItLeads drives the three
// readings of the compiled-module cache — the leaves reclamation collects, the
// snapshot a cold compile's attribution diffs, and a capped reclamation itself —
// over a cache directory that is the directory itself (the control) and one that
// is a link the machine's owner placed to keep the cache elsewhere.
//
// Each reading finds the one compiled module where the link leads, keyed and
// spelled beneath the directory as named, and a reclamation down to a one-byte cap
// removes that module and leaves the link standing. Walked as the link alone, the
// cache is one leaf named "." whose path is the link, so the snapshot keys the
// wrong name and the reclamation removes the link.
func TestACompileCacheDirectoryThatIsALinkIsWalkedWhereItLeads(t *testing.T) {
	const module = "wazero-v1-amd64-linux/module"
	for _, row := range []struct {
		name   string
		linked bool
	}{
		{"control: the cache directory itself", false},
		{"the cache directory a link the machine's owner placed", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			base := t.TempDir()
			kept := filepath.Join(base, "kept")
			writeSourceFile(t, filepath.Join(kept, filepath.FromSlash(module)), strings.Repeat("m", 64))
			dir := kept
			if row.linked {
				dir = filepath.Join(base, "wasm-compile")
				sourceLink(t, kept, dir)
			}
			wantPath := filepath.Join(dir, filepath.FromSlash(module))

			leaves, err := CollectCompileCacheLeaves(dir)
			if err != nil {
				t.Fatalf("CollectCompileCacheLeaves: %v", err)
			}
			if len(leaves) != 1 || leaves[0].Label != module || leaves[0].Path != wantPath {
				t.Errorf("collected %+v; want the one module %s at %s", leaves, module, wantPath)
			}

			if snap := listCompileLeaves(dir); len(snap) != 1 || snap[module] != 64 {
				t.Errorf("the compile snapshot keys %v; want %s of 64 bytes alone", snap, module)
			}

			res, err := ReclaimCompileCache(dir, 1)
			if err != nil {
				t.Fatalf("ReclaimCompileCache: %v", err)
			}
			if len(res.Removed) != 1 || res.Removed[0].Path != wantPath {
				t.Errorf("reclaimed %+v; want the module at %s", res.Removed, wantPath)
			}
			if _, err := os.Lstat(dir); err != nil {
				t.Errorf("the reclamation removed the cache directory itself: %v", err)
			}
			if _, err := os.Stat(filepath.Join(kept, filepath.FromSlash(module))); !os.IsNotExist(err) {
				t.Errorf("the module where the cache is kept was not removed: %v", err)
			}
		})
	}
}
