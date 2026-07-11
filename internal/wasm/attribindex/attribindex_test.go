// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package attribindex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRoundTrip records a kernel leaf and a guest leaf and confirms Load reads
// back the flags, sizes, and referrers, and that a leaf absent from the index
// reads as unattributed (Entry ok false).
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)

	if err := RecordKernelLeaf(path, "wazero-dev/kernelhex", 20523, "sha256:AAAA"); err != nil {
		t.Fatalf("record kernel: %v", err)
	}
	if err := RecordGuestLeaf(path, "wazero-dev/guesthex", 13438558, "sha256:AAAA"); err != nil {
		t.Fatalf("record guest: %v", err)
	}

	ix, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	k, ok := ix.Entry("wazero-dev/kernelhex")
	if !ok {
		t.Fatalf("kernel leaf missing")
	}
	if !k.Kernel {
		t.Fatalf("kernel leaf not flagged Kernel: %+v", k)
	}
	if k.Size != 20523 {
		t.Fatalf("kernel size = %d, want 20523", k.Size)
	}
	if len(k.Referrers) != 1 || k.Referrers[0] != "aaaa" {
		t.Fatalf("kernel referrers = %+v, want [aaaa] (normalized)", k.Referrers)
	}

	g, ok := ix.Entry("wazero-dev/guesthex")
	if !ok {
		t.Fatalf("guest leaf missing")
	}
	if g.Kernel {
		t.Fatalf("guest leaf must not be Kernel: %+v", g)
	}
	if len(g.Referrers) != 1 || g.Referrers[0] != "aaaa" {
		t.Fatalf("guest referrers = %+v, want [aaaa]", g.Referrers)
	}

	if _, ok := ix.Entry("wazero-dev/never-recorded"); ok {
		t.Fatalf("an unrecorded leaf must read as unattributed (ok=false)")
	}
}

// TestByteStable proves the on-disk file is byte-identical across two writes of
// identical logical state — the determinism downstream file-diffing relies on.
// The second pass re-records the same facts in a different call order and with
// duplicate referrers; sorting and dedup must land on identical bytes.
func TestByteStable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)

	if err := RecordKernelLeaf(path, "wazero/kernel", 20000, "sha256:BBBB"); err != nil {
		t.Fatalf("kernel: %v", err)
	}
	if err := RecordGuestLeaf(path, "wazero/guest-b", 900000, "sha256:BBBB"); err != nil {
		t.Fatalf("guest-b: %v", err)
	}
	if err := RecordGuestLeaf(path, "wazero/guest-a", 800000, "sha256:AAAA"); err != nil {
		t.Fatalf("guest-a: %v", err)
	}
	if err := AddKernelReferrer(path, "sha256:AAAA"); err != nil {
		t.Fatalf("kernel referrer a: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}

	// Re-record the same logical state, mixing order and repeating referrers.
	if err := AddKernelReferrer(path, "sha256:BBBB"); err != nil {
		t.Fatalf("kernel referrer b (dup): %v", err)
	}
	if err := RecordGuestLeaf(path, "wazero/guest-a", 800000, "sha256:AAAA"); err != nil {
		t.Fatalf("guest-a again: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("on-disk index not byte-identical across writes:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestGuestReferrerIdempotent confirms recording the same {leaf, guest} twice
// leaves exactly one referrer.
func TestGuestReferrerIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)
	for i := 0; i < 3; i++ {
		if err := RecordGuestLeaf(path, "wazero/guest", 500, "sha256:CAFE"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	ix, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e, ok := ix.Entry("wazero/guest")
	if !ok {
		t.Fatalf("leaf missing")
	}
	if len(e.Referrers) != 1 {
		t.Fatalf("want 1 referrer after repeated record, got %+v", e.Referrers)
	}
}

// TestKernelReferrerAccumulation proves the kernel leaf accumulates one referrer
// per distinct guest, deduplicated and sorted, and that KernelRecorded flips
// once a kernel is present.
func TestKernelReferrerAccumulation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)

	if rec, err := KernelRecorded(path); err != nil || rec {
		t.Fatalf("KernelRecorded on empty = (%v, %v), want (false, nil)", rec, err)
	}
	// No kernel yet: AddKernelReferrer is a no-op, not an error.
	if err := AddKernelReferrer(path, "sha256:AAAA"); err != nil {
		t.Fatalf("add referrer with no kernel: %v", err)
	}
	if rec, _ := KernelRecorded(path); rec {
		t.Fatalf("AddKernelReferrer must not create a kernel entry")
	}

	if err := RecordKernelLeaf(path, "wazero/kernel", 20000, "sha256:AAAA"); err != nil {
		t.Fatalf("record kernel: %v", err)
	}
	if rec, _ := KernelRecorded(path); !rec {
		t.Fatalf("KernelRecorded must be true after RecordKernelLeaf")
	}
	if err := AddKernelReferrer(path, "sha256:BBBB"); err != nil {
		t.Fatalf("add referrer b: %v", err)
	}
	if err := AddKernelReferrer(path, "sha256:BBBB"); err != nil {
		t.Fatalf("add referrer b (dup): %v", err)
	}
	if err := AddKernelReferrer(path, "sha256:CCCC"); err != nil {
		t.Fatalf("add referrer c: %v", err)
	}

	ix, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e, ok := ix.Entry("wazero/kernel")
	if !ok {
		t.Fatalf("kernel missing")
	}
	want := []string{"aaaa", "bbbb", "cccc"}
	if len(e.Referrers) != len(want) {
		t.Fatalf("kernel referrers = %+v, want %+v", e.Referrers, want)
	}
	for i := range want {
		if e.Referrers[i] != want[i] {
			t.Fatalf("kernel referrers not sorted/deduped: %+v, want %+v", e.Referrers, want)
		}
	}
}

// TestRemoveLeaves proves the reconcile primitive: removing a guest leaf drops
// its entry AND removes its digest from the shared kernel's referrer set; a
// kernel whose referrers all drop stays present until the caller removes it by
// key; and removed keys or dropped referrers absent from the index are no-ops.
func TestRemoveLeaves(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)

	// A kernel shared by two guests, plus the two guest leaves.
	if err := RecordKernelLeaf(path, "wazero/kernel", 20000, "sha256:AAAA"); err != nil {
		t.Fatalf("kernel: %v", err)
	}
	if err := AddKernelReferrer(path, "sha256:BBBB"); err != nil {
		t.Fatalf("kernel referrer b: %v", err)
	}
	if err := RecordGuestLeaf(path, "wazero/guest-a", 800000, "sha256:AAAA"); err != nil {
		t.Fatalf("guest-a: %v", err)
	}
	if err := RecordGuestLeaf(path, "wazero/guest-b", 900000, "sha256:BBBB"); err != nil {
		t.Fatalf("guest-b: %v", err)
	}

	// Remove guest-a; drop its referrer from every survivor (the kernel).
	if err := RemoveLeaves(path, []string{"wazero/guest-a"}, []string{"sha256:AAAA"}); err != nil {
		t.Fatalf("remove guest-a: %v", err)
	}
	ix, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := ix.Entry("wazero/guest-a"); ok {
		t.Errorf("guest-a entry not dropped")
	}
	if _, ok := ix.Entry("wazero/guest-b"); !ok {
		t.Errorf("guest-b entry wrongly dropped")
	}
	k, ok := ix.Entry("wazero/kernel")
	if !ok {
		t.Fatalf("kernel dropped prematurely")
	}
	if len(k.Referrers) != 1 || k.Referrers[0] != "bbbb" {
		t.Errorf("kernel referrers = %+v, want [bbbb] (aaaa dropped)", k.Referrers)
	}

	// Removing an absent leaf and dropping an absent referrer are no-ops.
	if err := RemoveLeaves(path, []string{"wazero/never"}, []string{"sha256:CCCC"}); err != nil {
		t.Fatalf("no-op remove: %v", err)
	}
	ix, _ = Load(path)
	if k, _ := ix.Entry("wazero/kernel"); len(k.Referrers) != 1 || k.Referrers[0] != "bbbb" {
		t.Errorf("no-op remove perturbed the kernel: %+v", k.Referrers)
	}
	if g, ok := ix.Entry("wazero/guest-b"); !ok || len(g.Referrers) != 1 || g.Referrers[0] != "bbbb" {
		t.Errorf("no-op remove perturbed guest-b: %+v (ok=%v)", g.Referrers, ok)
	}

	// Remove guest-b; the kernel's referrer set empties, leaving it caller-removable.
	if err := RemoveLeaves(path, []string{"wazero/guest-b"}, []string{"sha256:BBBB"}); err != nil {
		t.Fatalf("remove guest-b: %v", err)
	}
	ix, _ = Load(path)
	k, ok = ix.Entry("wazero/kernel")
	if !ok {
		t.Fatalf("kernel dropped by referrer removal (only the caller removes it)")
	}
	if len(k.Referrers) != 0 {
		t.Errorf("kernel referrers = %+v, want empty", k.Referrers)
	}

	// The caller removes the now-unreferenced kernel by key.
	if err := RemoveLeaves(path, []string{"wazero/kernel"}, nil); err != nil {
		t.Fatalf("remove kernel: %v", err)
	}
	ix, _ = Load(path)
	if len(ix.Entries()) != 0 {
		t.Errorf("index not empty after removing every leaf: %+v", ix.Entries())
	}
}

// TestLoadMissingIsEmpty confirms a first-ever run (no file) and an empty path
// both load clean rather than erroring.
func TestLoadMissingIsEmpty(t *testing.T) {
	t.Parallel()
	if ix, err := Load(filepath.Join(t.TempDir(), "nope.json")); err != nil || len(ix.Entries()) != 0 {
		t.Fatalf("load missing = (%+v, %v), want empty/nil", ix.Entries(), err)
	}
	if ix, err := Load(""); err != nil || len(ix.Entries()) != 0 {
		t.Fatalf("load empty path = (%+v, %v), want empty/nil", ix.Entries(), err)
	}
}

// TestPathForCompileDirMatchesRoot proves the write-side path (derived from the
// compile directory) and the read-side default path (derived from the cache
// root) resolve to the same file in the shipped layout, where the compile
// directory is a child of the cache root. DefaultIndexPath's test-binary branch
// is bypassed here by comparing against the explicit non-test spelling.
func TestPathForCompileDirMatchesRoot(t *testing.T) {
	t.Parallel()
	root := "/home/u/.cache/aggrega"
	compileDir := filepath.Join(root, "wasm-compile")
	if got, want := PathForCompileDir(compileDir), filepath.Join(root, indexFileName); got != want {
		t.Fatalf("PathForCompileDir = %q, want %q", got, want)
	}
}
