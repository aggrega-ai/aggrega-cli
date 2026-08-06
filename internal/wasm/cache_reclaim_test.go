// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeReclaimLeaf writes a synthetic compiled-module cache entry of the given
// size and compile time beneath dir, creating dir when needed, and returns its
// path. Synthetic entries are what let a cap be driven past deliberately: the
// caches this pass runs against in practice hold entries of hundreds of
// megabytes and sit far below any shipped cap, so no real cache can demonstrate
// a trim.
func writeReclaimLeaf(t *testing.T, dir, name string, size int, mtime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	body := make([]byte, size)
	for i := range body {
		body[i] = 'x'
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", p, err)
	}
	return p
}

func reclaimLeafExists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	return err == nil
}

// TestDefaultCompileCacheCapIsFiftyGibibytes pins the shipped default against an
// independently written arithmetic expression rather than against the constant's
// own expression, so a typo in the constant cannot agree with itself. The number
// is also what the configuration surface publishes as its default, so the two
// must state the same figure.
func TestDefaultCompileCacheCapIsFiftyGibibytes(t *testing.T) {
	const fiftyGiB int64 = 50 * 1024 * 1024 * 1024
	if DefaultCompileCacheCapBytes != fiftyGiB {
		t.Fatalf("default compiled-module cache cap is %d bytes, want %d (50 GiB)",
			DefaultCompileCacheCapBytes, fiftyGiB)
	}
	if DefaultCompileCacheCapBytes != 53_687_091_200 {
		t.Fatalf("default cap %d does not match the figure the configuration surface publishes (53687091200)",
			DefaultCompileCacheCapBytes)
	}
}

// TestCompileCacheCapDefaultsWithNoResolverInstalled is the guard on the case
// that covers every caller who configures nothing at all — including every test
// binary, which shares one cache directory and installs no resolver. With
// nothing armed the cap must be the shipped default, so the shared cache is
// capped by construction rather than by anyone remembering to cap it.
func TestCompileCacheCapDefaultsWithNoResolverInstalled(t *testing.T) {
	if got := compileCacheCapBytes(); got != DefaultCompileCacheCapBytes {
		t.Fatalf("with no cap resolver installed the cap is %d, want the default %d", got, DefaultCompileCacheCapBytes)
	}
}

// TestCompileCacheCapResolverIsConsumed proves an armed resolver is actually the
// value the cap reads — the difference between a setting that controls something
// and a setting that is merely accepted.
//
// The resolver it arms reports a cap ABOVE the default, deliberately. The cap is
// process-global, so between arming and restoring it applies to any cache this
// process opens, including the directory a whole test run shares; a cap larger
// than the default can only ever make a pass do less, so the window cannot
// remove anything. Restoring stores a resolver reporting the default, which is
// what an unarmed process reads.
func TestCompileCacheCapResolverIsConsumed(t *testing.T) {
	want := DefaultCompileCacheCapBytes * 2
	t.Cleanup(func() {
		SetCompileCacheCapResolver(func() int64 { return DefaultCompileCacheCapBytes })
	})

	SetCompileCacheCapResolver(func() int64 { return want })
	if got := compileCacheCapBytes(); got != want {
		t.Fatalf("armed cap resolver reported %d but the cap read %d; the setting is accepted and ignored", want, got)
	}

	// A nil resolver must not disarm an installed one, so a caller that cannot
	// resolve a value cannot accidentally uncap the cache.
	SetCompileCacheCapResolver(nil)
	if got := compileCacheCapBytes(); got != want {
		t.Fatalf("a nil resolver disarmed the installed cap: read %d, want %d", got, want)
	}
}

// TestTheProcessCompiledModuleCacheIsCappedWhereverItLives is the completeness
// guard for the cap: whichever compiled-module store this process opened, that
// store is the one the cap was applied to.
//
// It matters because there is more than one such store. A test binary shares a
// store under the system temp directory so a whole run compiles each guest at
// most once; every other process uses the one under the user cache directory. The
// cap covers both without being written twice, because it is applied at the single
// site that opens the store — so the guard asserts identity with the resolved
// location rather than a fixed path, which is the only form that holds for both.
func TestTheProcessCompiledModuleCacheIsCappedWhereverItLives(t *testing.T) {
	// Constructing the process-wide cache is idempotent, so this observes
	// whatever the process already decided rather than deciding anything.
	if sharedCompilationCache(false) == nil {
		t.Fatal("no process-wide compilation cache was constructed")
	}
	if sharedCompilationCacheDir == "" {
		t.Skip("the process-wide cache is not disk-backed here, so there is no store to cap")
	}

	applied, _ := compileCacheCapAppliedTo.Load().(string)
	if applied == "" {
		t.Fatal("an on-disk compiled-module store was opened and the size cap was never applied to it")
	}
	if applied != sharedCompilationCacheDir {
		t.Fatalf("the cap was applied to %q but this process compiles into %q; the capped store is not the used store",
			applied, sharedCompilationCacheDir)
	}

	// And that store is the one the cache-location census governs, so capping it
	// caps the store every other caller resolves too.
	resolved, ok := CompileCacheDir()
	if !ok {
		t.Fatal("CompileCacheDir did not resolve; the capped store must be the resolvable one")
	}
	if applied != resolved {
		t.Fatalf("the cap was applied to %q, which is not the resolved compiled-module cache directory %q",
			applied, resolved)
	}
}

// TestReclaimCompileCacheTrimsOldestFirstToCap drives a cache past its cap and
// asserts it comes back to at or below it, oldest-compiled entries removed first
// and the newest retained.
func TestReclaimCompileCacheTrimsOldestFirstToCap(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	oldest := writeReclaimLeaf(t, versioned, "leaf-oldest", 400, base)
	middle := writeReclaimLeaf(t, versioned, "leaf-middle", 400, base.Add(time.Hour))
	newest := writeReclaimLeaf(t, versioned, "leaf-newest", 400, base.Add(2*time.Hour))

	res, err := ReclaimCompileCache(dir, 500)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if res.Total != 1200 {
		t.Fatalf("total before the pass was %d, want 1200", res.Total)
	}
	if res.WithinCapBefore() {
		t.Fatal("a 1200-byte cache reported itself already within a 500-byte cap")
	}
	if res.Remaining > 500 {
		t.Fatalf("after the pass the cache holds %d bytes, above the 500-byte cap", res.Remaining)
	}
	if res.Reclaimed+res.Remaining != res.Total {
		t.Fatalf("accounting does not close: reclaimed %d + remaining %d != total %d",
			res.Reclaimed, res.Remaining, res.Total)
	}

	if reclaimLeafExists(t, oldest) {
		t.Error("the oldest entry survived a trim that had to remove something")
	}
	if reclaimLeafExists(t, middle) {
		t.Error("the second-oldest entry survived while the cache was still over cap")
	}
	if !reclaimLeafExists(t, newest) {
		t.Error("the newest entry was removed although removing the two older ones sufficed")
	}

	// Removal order is the reported order, and it is oldest-first.
	if len(res.Removed) != 2 {
		t.Fatalf("the pass reported %d removals, want 2: %+v", len(res.Removed), res.Removed)
	}
	if res.Removed[0].Label != "wazero-1.0-amd64-linux/leaf-oldest" {
		t.Errorf("first removal was %q, want the oldest entry", res.Removed[0].Label)
	}
	if res.Removed[1].Label != "wazero-1.0-amd64-linux/leaf-middle" {
		t.Errorf("second removal was %q, want the second-oldest entry", res.Removed[1].Label)
	}

	// A second pass at the same cap is a no-op: the cap is a steady state, not a
	// ratchet that keeps removing.
	again, err := ReclaimCompileCache(dir, 500)
	if err != nil {
		t.Fatalf("second reclaim: %v", err)
	}
	if !again.WithinCapBefore() || len(again.Removed) != 0 {
		t.Fatalf("a second pass at the same cap removed %d entries; it should be a no-op: %+v",
			len(again.Removed), again.Removed)
	}
}

// TestReclaimCompileCacheSpansEveryVersionedSubdirectory asserts the pass sees
// entries under every version-and-architecture subdirectory, not only the one the
// running build uses. A subdirectory left by a runtime version no longer in use is
// dead weight that only this pass can reach, and it is exactly the oldest content,
// so it must be both counted and removable.
func TestReclaimCompileCacheSpansEveryVersionedSubdirectory(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	stale := writeReclaimLeaf(t, filepath.Join(dir, "wazero-0.9-amd64-linux"), "leaf-stale", 400, base)
	live := writeReclaimLeaf(t, filepath.Join(dir, "wazero-1.0-amd64-linux"), "leaf-live", 400, base.Add(time.Hour))

	res, err := ReclaimCompileCache(dir, 400)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if res.Total != 800 {
		t.Fatalf("total was %d, want 800; a versioned subdirectory went uncounted", res.Total)
	}
	if reclaimLeafExists(t, stale) {
		t.Error("the entry under the older runtime subdirectory survived; it is the oldest content and should go first")
	}
	if !reclaimLeafExists(t, live) {
		t.Error("the newer entry was removed although removing the stale one sufficed")
	}
}

// TestReclaimCompileCacheLeavesAnInFlightCompileAlone is the guard on the one way
// a size cap could break a compile that is running right now. A compile in
// progress owns two kinds of file in the cache directory: the lock sidecar that
// serializes it, and the staging file it writes before renaming into place.
// Neither is finished cache content. If the pass counted them it would over-trim,
// and if it removed them it would break the compile writing them — so both must
// be invisible to it on both axes.
func TestReclaimCompileCacheLeavesAnInFlightCompileAlone(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	oldest := writeReclaimLeaf(t, versioned, "leaf-oldest", 400, base)
	newest := writeReclaimLeaf(t, versioned, "leaf-newest", 400, base.Add(time.Hour))
	// Both are large and old: counted, they would blow past the cap; removable,
	// they would be the first things to go.
	guestLock := writeReclaimLeaf(t, dir, ".compile-deadbeef.lock", 5000, base)
	staging := writeReclaimLeaf(t, versioned, "leaf-newest.1234.tmp", 5000, base)
	// The reclamation lock file has the same excluded shape, so a pass cannot
	// count or remove its own lock.
	reclaimLock := writeReclaimLeaf(t, dir, compileCacheReclaimLockName, 5000, base)

	res, err := ReclaimCompileCache(dir, 500)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if res.Total != 800 {
		t.Fatalf("total was %d, want 800: an in-flight compile's files were counted as cache content", res.Total)
	}
	if reclaimLeafExists(t, oldest) {
		t.Error("the oldest real entry survived")
	}
	if !reclaimLeafExists(t, newest) {
		t.Error("the newest real entry was removed although removing the older one sufficed")
	}
	for name, p := range map[string]string{
		"per-guest compile lock":  guestLock,
		"in-flight staging file":  staging,
		"reclamation lock itself": reclaimLock,
	} {
		if !reclaimLeafExists(t, p) {
			t.Errorf("the pass removed the %s at %s; that breaks a compile in progress", name, p)
		}
	}
}

// TestReclaimCompileCacheUncappedRemovesNothing asserts the documented escape
// hatch: a cap of zero means uncapped, so the total is still reported and nothing
// is removed. Someone who deliberately wants an unbounded cache must get one.
func TestReclaimCompileCacheUncappedRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := writeReclaimLeaf(t, versioned, "leaf-a", 400, base)
	b := writeReclaimLeaf(t, versioned, "leaf-b", 400, base.Add(time.Hour))

	res, err := ReclaimCompileCache(dir, 0)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if res.Total != 800 || res.Remaining != 800 {
		t.Fatalf("uncapped pass reported total %d remaining %d, want 800/800", res.Total, res.Remaining)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("an uncapped pass removed %d entries: %+v", len(res.Removed), res.Removed)
	}
	if !res.WithinCapBefore() {
		t.Error("an uncapped pass should report the cache as within cap")
	}
	if !reclaimLeafExists(t, a) || !reclaimLeafExists(t, b) {
		t.Error("an uncapped pass removed an entry")
	}
}

// TestReclaimCompileCacheOnAbsentDirectory asserts a cache that was never
// populated is an empty answer rather than an error, so the startup pass on a
// first-ever build is silent.
func TestReclaimCompileCacheOnAbsentDirectory(t *testing.T) {
	res, err := ReclaimCompileCache(filepath.Join(t.TempDir(), "never-created"), 500)
	if err != nil {
		t.Fatalf("reclaim over an absent directory: %v", err)
	}
	if res.Total != 0 || len(res.Removed) != 0 {
		t.Fatalf("an absent cache reported total %d and %d removals", res.Total, len(res.Removed))
	}
	if _, err := ReclaimCompileCache("", 500); err == nil {
		t.Error("reclaim with no directory should be a loud error")
	}
}

// TestReclaimCompileCacheStartupPassHoldsTheCap exercises the automatic pass
// itself — lock acquisition included — against a caller-chosen directory and cap,
// which is the composition the process startup path runs.
func TestReclaimCompileCacheStartupPassHoldsTheCap(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oldest := writeReclaimLeaf(t, versioned, "leaf-oldest", 400, base)
	newest := writeReclaimLeaf(t, versioned, "leaf-newest", 400, base.Add(time.Hour))

	reclaimCompileCacheUnderLock(dir, 500)

	if reclaimLeafExists(t, oldest) {
		t.Error("the startup pass left the cache over cap: the oldest entry survived")
	}
	if !reclaimLeafExists(t, newest) {
		t.Error("the startup pass removed the newest entry unnecessarily")
	}

	// Re-running it is a no-op, and an uncapped run leaves the cache alone.
	reclaimCompileCacheUnderLock(dir, 0)
	if !reclaimLeafExists(t, newest) {
		t.Error("an uncapped startup pass removed an entry")
	}
	reclaimCompileCacheUnderLock("", 500)
}

// TestCompileCacheReclaimLockIsExclusiveAndNonBlocking asserts the two properties
// the reclamation lock is chosen for: a second holder does not get it, and does
// not wait for it. Not waiting is what keeps a burst of processes starting at once
// from serialising behind a trim none of them needs to perform — the one already
// holding the lock is doing that job.
func TestCompileCacheReclaimLockIsExclusiveAndNonBlocking(t *testing.T) {
	dir := t.TempDir()
	release, ok := flockCompileCacheReclaim(dir)
	if !ok {
		t.Skip("this platform has no advisory file locking, so reclamation runs unlocked by design")
	}

	done := make(chan bool, 1)
	go func() {
		second, got := flockCompileCacheReclaim(dir)
		if got {
			second()
		}
		done <- got
	}()

	select {
	case got := <-done:
		if got {
			t.Error("a second holder took the reclamation lock while the first held it")
		}
	case <-time.After(5 * time.Second):
		t.Error("a second acquisition blocked instead of reporting failure; a process must skip a trim, never wait for one")
	}

	release()

	// Once released the lock is available again, so a later process still trims.
	after, ok := flockCompileCacheReclaim(dir)
	if !ok {
		t.Fatal("the reclamation lock was not reacquirable after release")
	}
	after()
}
