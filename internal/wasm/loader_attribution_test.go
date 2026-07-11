// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/tetratelabs/wazero"
)

// normHex mirrors attribindex's normalize spelling so the test compares referrer
// digests in the same prefix-free lowercase-hex form the index stores.
func normHex(rawDigest string) string {
	return strings.ToLower(strings.TrimPrefix(rawDigest, "sha256:"))
}

// TestColdCompileAttributionObservation drives the real wazero compiled-module
// emission against a fresh on-disk cache and asserts the cold-compile
// observation records leaf attribution exactly as the emission shape dictates:
//
//   - the first cold compile emits two leaves (the tiny shared runtime kernel
//     plus the guest) and records the kernel (Kernel:true, small) seeded with
//     the first guest's digest, plus the guest's own leaf;
//   - a second distinct guest emits one leaf (its own; the kernel is reused) and
//     records that leaf plus joins the kernel's referrer set — the kernel now
//     names both guests;
//   - a warm reload of the first guest emits zero leaves, is not re-observed
//     (the process memo short-circuits it), and leaves the index untouched.
//
// It exercises the REAL emission, re-confirming the 2-then-1-then-0 pattern the
// attribution logic depends on, and fails loudly if that pattern does not hold.
func TestColdCompileAttributionObservation(t *testing.T) {
	// The observation memo and snapshot counter are process-wide; reset them so
	// this test observes cold compiles regardless of what ran before it.
	attribObserved.Clear()
	atomic.StoreInt64(&attribSnapshotCount, 0)

	const guest1 = "mutator-go.wasm"
	const guest2 = "mutator-rust.wasm"
	bytes1 := mustReadFixture(t, guest1)
	bytes2 := mustReadFixture(t, guest2)
	dg1 := normHex(digest.ContentDigest(bytes1))
	dg2 := normHex(digest.ContentDigest(bytes2))
	if dg1 == dg2 {
		t.Fatalf("fixtures must be distinct guests")
	}

	tmp := t.TempDir()
	cache, err := wazero.NewCompilationCacheWithDir(tmp)
	if err != nil {
		t.Fatalf("NewCompilationCacheWithDir: %v", err)
	}
	defer cache.Close(context.Background())
	loader := &Loader{Cache: cache, compileDir: tmp}
	idxPath := attribindex.PathForCompileDir(tmp)

	load := func(name, path string) {
		p, err := loader.Load(context.Background(), PluginSpec{Name: name, Path: path})
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		_ = p.Close(context.Background())
	}
	fixturePath := func(name string) string { return filepath.Join("testdata", name) }

	// --- first cold compile: two leaves, kernel + guest1 ---
	load(guest1, fixturePath(guest1))
	if got := atomic.LoadInt64(&attribSnapshotCount); got != 1 {
		t.Fatalf("after first cold compile, snapshot count = %d, want 1", got)
	}
	ix, err := attribindex.Load(idxPath)
	if err != nil {
		t.Fatalf("load index after guest1: %v", err)
	}
	entries := ix.Entries()
	if len(entries) != 2 {
		t.Fatalf("first cold compile must record two leaves (kernel + guest); got %d: %+v", len(entries), entries)
	}
	kernel, guest1Leaf := splitKernelGuest(t, entries)
	if !kernel.Kernel {
		t.Fatalf("kernel leaf not flagged Kernel: %+v", kernel)
	}
	if kernel.Size >= guest1Leaf.Size {
		t.Fatalf("kernel (%d B) must be smaller than the guest leaf (%d B)", kernel.Size, guest1Leaf.Size)
	}
	if kernel.Size > 512*1024 {
		t.Fatalf("kernel leaf %d B exceeds the size guard ceiling", kernel.Size)
	}
	assertReferrers(t, "kernel after guest1", kernel.Referrers, dg1)
	assertReferrers(t, "guest1 leaf", guest1Leaf.Referrers, dg1)

	// --- second distinct guest: one new leaf, joins the kernel referrer set ---
	load(guest2, fixturePath(guest2))
	if got := atomic.LoadInt64(&attribSnapshotCount); got != 2 {
		t.Fatalf("after second cold compile, snapshot count = %d, want 2", got)
	}
	ix, err = attribindex.Load(idxPath)
	if err != nil {
		t.Fatalf("load index after guest2: %v", err)
	}
	entries = ix.Entries()
	if len(entries) != 3 {
		t.Fatalf("after a second distinct guest, want three leaves (kernel + two guests); got %d: %+v", len(entries), entries)
	}
	kernel2, _ := findKernel(t, entries)
	assertReferrers(t, "kernel after guest2", kernel2.Referrers, dg1, dg2)
	if !hasGuestLeaf(entries, dg2) {
		t.Fatalf("guest2's own leaf was not recorded with its digest %s: %+v", dg2, entries)
	}

	// --- warm reload of guest1: zero leaves, not re-observed, index unchanged ---
	before, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read index before warm reload: %v", err)
	}
	load(guest1, fixturePath(guest1))
	if got := atomic.LoadInt64(&attribSnapshotCount); got != 2 {
		t.Fatalf("a warm reload must NOT re-observe; snapshot count = %d, want 2", got)
	}
	after, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read index after warm reload: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("warm reload mutated the attribution index:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// mustReadFixture reads a prebuilt guest fixture, failing loudly (never skipping)
// when it is absent, so an attribution assertion never silently degrades.
func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s must be present for this test: %v", name, err)
	}
	return b
}

// splitKernelGuest returns the single kernel entry and the single non-kernel
// entry from a two-entry set, failing if the shape is not one-kernel-one-guest.
func splitKernelGuest(t *testing.T, entries []attribindex.Entry) (kernel, guest attribindex.Entry) {
	t.Helper()
	var ks, gs []attribindex.Entry
	for _, e := range entries {
		if e.Kernel {
			ks = append(ks, e)
		} else {
			gs = append(gs, e)
		}
	}
	if len(ks) != 1 || len(gs) != 1 {
		t.Fatalf("want exactly one kernel and one guest leaf, got kernels=%d guests=%d: %+v", len(ks), len(gs), entries)
	}
	return ks[0], gs[0]
}

func findKernel(t *testing.T, entries []attribindex.Entry) (attribindex.Entry, bool) {
	t.Helper()
	for _, e := range entries {
		if e.Kernel {
			return e, true
		}
	}
	t.Fatalf("no kernel leaf recorded: %+v", entries)
	return attribindex.Entry{}, false
}

func hasGuestLeaf(entries []attribindex.Entry, normDigest string) bool {
	for _, e := range entries {
		if e.Kernel {
			continue
		}
		for _, r := range e.Referrers {
			if r == normDigest {
				return true
			}
		}
	}
	return false
}

func assertReferrers(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s referrers = %+v, want %+v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s referrers = %+v, want %+v (sorted)", what, got, want)
		}
	}
}
