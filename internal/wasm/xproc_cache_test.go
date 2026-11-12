// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm_test

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/tetratelabs/wazero"
)

// Environment keys the parent uses to drive a worker subprocess. A worker is a
// re-exec of this very test binary restricted to the worker test; it constructs
// its own disk-backed compilation cache over the shared directory, loads the
// guest, and calls it. Absent these keys the worker test is inert.
const (
	xprocRoleEnv     = "AGGREGA_XPROC_ROLE"
	xprocCacheDirEnv = "AGGREGA_XPROC_CACHE_DIR"
	xprocGuestEnv    = "AGGREGA_XPROC_GUEST"
	xprocOutMarker   = "AGGREGA_XPROC_OUT="
)

// TestCrossProcessDiskCacheWorker is the subprocess half of
// TestCrossProcessDiskCacheConcurrency. When the parent has set the worker
// environment it performs one request-shaped load+call against a disk-backed
// compilation cache rooted at the shared directory and prints the guest output
// as a base64 marker line; otherwise it is inert (nothing to do in a plain test
// run). Building a Loader with an explicit Cache bypasses the process-wide
// shared-cache selection deliberately, so the test controls the exact directory
// under contention.
func TestCrossProcessDiskCacheWorker(t *testing.T) {
	if os.Getenv(xprocRoleEnv) != "worker" {
		t.Skip("not a cross-process cache worker invocation")
	}
	dir := os.Getenv(xprocCacheDirEnv)
	guest := os.Getenv(xprocGuestEnv)
	if dir == "" || guest == "" {
		t.Fatalf("worker missing env: %s=%q %s=%q", xprocCacheDirEnv, dir, xprocGuestEnv, guest)
	}

	disk, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		t.Fatalf("worker: NewCompilationCacheWithDir(%s): %v", dir, err)
	}
	defer disk.Close(context.Background())

	loader := &wasm.Loader{Cache: disk}
	plugin, err := loader.Load(context.Background(), wasm.PluginSpec{Name: "policy-go", Path: guest})
	if err != nil {
		t.Fatalf("worker: load: %v", err)
	}
	defer func() { _ = plugin.Close(context.Background()) }()

	out, err := plugin.Call(context.Background(), "verdict_ir", []byte(`{}`))
	if err != nil {
		t.Fatalf("worker: call verdict_ir: %v", err)
	}
	// Marker line the parent parses out of the combined test output.
	t.Logf("%s%s", xprocOutMarker, base64.StdEncoding.EncodeToString(out))
}

// runWorker re-execs this test binary as a single worker against cacheDir and
// guest, returning the decoded guest output. A non-zero exit (a t.Fatalf in the
// worker — an error or a corrupt cache entry) fails the calling test with the
// captured output, so a TRANSLATION_ERROR or any load failure surfaces loudly.
func runWorker(t *testing.T, cacheDir, guest string) []byte {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrossProcessDiskCacheWorker$", "-test.v")
	cmd.Env = append(os.Environ(),
		xprocRoleEnv+"=worker",
		xprocCacheDirEnv+"="+cacheDir,
		xprocGuestEnv+"="+guest,
	)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker process failed: %v\noutput:\n%s", err, raw)
	}
	out, ok := parseWorkerOutput(string(raw))
	if !ok {
		t.Fatalf("worker produced no %s marker\noutput:\n%s", xprocOutMarker, raw)
	}
	return out
}

// evictInstalledEntries deletes the installed final cache-entry files beneath
// dir, mirroring wazero's own per-entry cache deletion. It walks into the
// versioned subdirectory wazero creates and removes only regular files that are
// not in-flight staging temps (".tmp"), never a directory, so a concurrent
// compile's staging write and rename are left untouched.
func evictInstalledEntries(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := dir + "/" + e.Name()
		if e.IsDir() {
			evictInstalledEntries(full)
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		_ = os.Remove(full)
	}
}

func parseWorkerOutput(combined string) ([]byte, bool) {
	for _, line := range strings.Split(combined, "\n") {
		idx := strings.Index(line, xprocOutMarker)
		if idx < 0 {
			continue
		}
		enc := strings.TrimSpace(line[idx+len(xprocOutMarker):])
		dec, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			continue
		}
		return dec, true
	}
	return nil, false
}

// TestCrossProcessDiskCacheConcurrency proves a single on-disk wazero
// compilation cache directory is safe to share across independent OS processes
// that concurrently compile, read, and evict the same guest entry, and that a
// warm shared cache is reused rather than recompiled. It exercises the property
// the test suite relies on when many parallel test binaries share one on-disk
// compile cache.
//
// Each worker is a separate process (a re-exec of this test binary) holding its
// own disk-backed CompilationCache over the same directory. The rounds stress
// the contended paths: a cold round pre-clears the directory so every worker
// cold-compiles and races to install the same cache entry; an evicted round runs
// a concurrent deleter that removes cache files while workers load; a warm round
// reads an already-populated cache. Every worker must succeed and emit
// byte-identical guest output — a corrupt or torn cache entry would fail a
// worker load (surfacing as a non-zero worker exit) or perturb its output.
func TestCrossProcessDiskCacheConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-process cache stress skipped in -short")
	}
	guest := ensureFixtureBuilt(t, "policy-go.wasm")
	cacheDir := t.TempDir()

	const workers = 6
	var want []byte

	spawnBatch := func(t *testing.T, evict bool) [][]byte {
		t.Helper()
		outs := make([][]byte, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func(i int) {
				defer wg.Done()
				outs[i] = runWorker(t, cacheDir, guest)
			}(i)
		}
		if evict {
			// Concurrent eviction models wazero's own single-entry cache
			// deletion: remove installed final entry files while workers load,
			// leaving the versioned directory and any in-flight staging temp
			// files intact (deleting those would break a write the harness never
			// performs). An open fd survives a concurrent unlink and a missing
			// entry is a plain cache miss (recompile), so the correct outcome is
			// no error and unchanged output — never a corrupt read.
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
						evictInstalledEntries(cacheDir)
						time.Sleep(time.Millisecond)
					}
				}
			}()
			wg.Wait()
			close(stop)
			<-done
		} else {
			wg.Wait()
		}
		return outs
	}

	assertUniform := func(t *testing.T, label string, outs [][]byte) {
		t.Helper()
		for i, o := range outs {
			if len(o) == 0 {
				t.Fatalf("%s: worker %d produced empty output", label, i)
			}
			if want == nil {
				want = o
				continue
			}
			if string(o) != string(want) {
				t.Fatalf("%s: worker %d output differs across processes\n want: %s\n  got: %s",
					label, i, want, o)
			}
		}
	}

	// Cold rounds: clear the directory so all workers cold-compile and race to
	// install the identical entry — the collision-prone write path.
	for round := 0; round < 3; round++ {
		if err := os.RemoveAll(cacheDir); err != nil {
			t.Fatalf("clear cache dir: %v", err)
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			t.Fatalf("recreate cache dir: %v", err)
		}
		assertUniform(t, "cold round", spawnBatch(t, false))
	}

	// Evicted round: concurrent deletion during load (open-during-unlink,
	// miss-then-recompile).
	assertUniform(t, "evicted round", spawnBatch(t, true))

	// Warm rounds: an already-populated cache read concurrently.
	for round := 0; round < 2; round++ {
		assertUniform(t, "warm round", spawnBatch(t, false))
	}
}

// TestCrossProcessDiskCacheReuse proves a warm shared on-disk cache is reused
// across processes rather than recompiled: a second process against a populated
// directory completes markedly faster than the cold first process and yields
// byte-identical output. wazero exposes no hit/miss counter, so wall time is the
// observable proxy for a cache hit; the ratio is logged and a conservative floor
// is asserted so the reuse is demonstrable, not merely assumed.
func TestCrossProcessDiskCacheReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-process cache reuse timing skipped in -short")
	}
	guest := ensureFixtureBuilt(t, "policy-go.wasm")
	cacheDir := t.TempDir()

	start := time.Now()
	cold := runWorker(t, cacheDir, guest)
	coldDur := time.Since(start)

	start = time.Now()
	warm := runWorker(t, cacheDir, guest)
	warmDur := time.Since(start)

	if string(cold) != string(warm) {
		t.Fatalf("warm output differs from cold\n cold: %s\n warm: %s", cold, warm)
	}
	t.Logf("cross-process compile cost: cold=%s warm=%s speedup=%.2fx", coldDur, warmDur, float64(coldDur)/float64(warmDur))

	// The cold process pays guest compilation; the warm process must not. Process
	// spawn + WASI bootstrap dominate the warm run, so assert only a conservative
	// floor rather than a tight bound.
	if warmDur >= coldDur {
		t.Fatalf("warm run (%s) not faster than cold run (%s): shared disk cache was not reused across processes", warmDur, coldDur)
	}
}

// TestCrossProcessReuseSurvivesCappedReclaim is the guard on the property a size
// cap could plausibly destroy: that a shared on-disk compiled-module cache is
// still reused across processes after the cap has removed entries from it.
//
// It matters because the cap and the sharing pull in opposite directions. The
// sharing is why a whole test run compiles each guest at most once instead of
// paying a cold compile per process; a cap that held the cache under its bound by
// removing what the next process needs would keep the accounting honest while
// destroying the thing the cache exists for. So a reclamation that stayed under
// cap is not sufficient — reuse has to be shown still working afterwards.
//
// It also measures the safety claim the reclamation point rests on: removing an
// entry a live process is about to read costs that process a recompile and never a
// failed read. A worker that ran into a removed entry as an error rather than a
// miss would exit non-zero and fail this test loudly. The two reclamation shapes
// are both exercised — one entry removed, and every entry removed — because they
// fail differently: a partial removal leaves a directory that is populated but
// incomplete, which is the state a naive reader would mishandle.
func TestCrossProcessReuseSurvivesCappedReclaim(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-process reuse-after-reclaim timing skipped in -short")
	}
	guest := ensureFixtureBuilt(t, "policy-go.wasm")
	cacheDir := t.TempDir()

	timeWorker := func(label string) ([]byte, time.Duration) {
		t.Helper()
		start := time.Now()
		out := runWorker(t, cacheDir, guest)
		d := time.Since(start)
		if len(out) == 0 {
			t.Fatalf("%s: worker produced empty output", label)
		}
		return out, d
	}

	cacheTotal := func(label string) (int64, []wasm.CompileCacheLeaf) {
		t.Helper()
		leaves, err := wasm.CollectCompileCacheLeaves(cacheDir)
		if err != nil {
			t.Fatalf("%s: enumerate cache: %v", label, err)
		}
		if len(leaves) == 0 {
			t.Fatalf("%s: the cache holds no entries, so this test would prove nothing about reuse", label)
		}
		var total int64
		for _, l := range leaves {
			total += l.Size
		}
		return total, leaves
	}

	want, coldDur := timeWorker("cold")
	_, warmDur := timeWorker("warm")
	if warmDur >= coldDur {
		t.Fatalf("the baseline is not established: warm (%s) was not faster than cold (%s), "+
			"so a later warm run proves nothing about reuse", warmDur, coldDur)
	}

	total, leaves := cacheTotal("populated")
	t.Logf("populated cache: %d entrie(s), %d bytes; cold=%s warm=%s", len(leaves), total, coldDur, warmDur)

	// A cap at the total is a no-op, and the cache is still reused afterwards. This
	// is the overwhelmingly common case in practice — a cache under its cap — so it
	// is the one that must cost nothing.
	noop, err := wasm.ReclaimCompileCache(cacheDir, total)
	if err != nil {
		t.Fatalf("no-op reclaim: %v", err)
	}
	if len(noop.Removed) != 0 {
		t.Fatalf("a cap at the cache total removed %d entrie(s); it must remove nothing: %+v",
			len(noop.Removed), noop.Removed)
	}
	got, afterNoopDur := timeWorker("warm after a no-op cap")
	if string(got) != string(want) {
		t.Fatalf("output changed after a no-op cap\n want: %s\n  got: %s", want, got)
	}
	if afterNoopDur >= coldDur {
		t.Fatalf("a no-op cap cost the cache its warmth: %s, not faster than cold %s", afterNoopDur, coldDur)
	}

	// Now a cap that genuinely removes something: exactly the oldest entry. The
	// next process must succeed — a removed entry is a miss that recompiles — and
	// the process after it must be warm again, which is the reuse claim.
	oldest := leaves[0]
	for _, l := range leaves[1:] {
		if l.ModTime.Before(oldest.ModTime) {
			oldest = l
		}
	}
	partial, err := wasm.ReclaimCompileCache(cacheDir, total-oldest.Size)
	if err != nil {
		t.Fatalf("partial reclaim: %v", err)
	}
	if len(partial.Removed) == 0 {
		t.Fatalf("a cap below the total removed nothing, so nothing was reclaimed to measure against")
	}
	if partial.Remaining > total-oldest.Size {
		t.Fatalf("after the partial pass the cache holds %d bytes, above the %d-byte cap",
			partial.Remaining, total-oldest.Size)
	}
	got, repopDur := timeWorker("first process after a partial reclaim")
	if string(got) != string(want) {
		t.Fatalf("output changed after a partial reclaim\n want: %s\n  got: %s", want, got)
	}
	got, warmAfterPartialDur := timeWorker("warm after a partial reclaim")
	if string(got) != string(want) {
		t.Fatalf("output changed on the warm run after a partial reclaim\n want: %s\n  got: %s", want, got)
	}
	if warmAfterPartialDur >= coldDur {
		t.Fatalf("cross-process reuse did not recover after a partial reclaim: warm %s, not faster than cold %s",
			warmAfterPartialDur, coldDur)
	}
	t.Logf("partial reclaim removed %d entrie(s), %d bytes; repopulate=%s warm-after=%s",
		len(partial.Removed), partial.Reclaimed, repopDur, warmAfterPartialDur)

	// The extreme: a cap so low every entry goes. The next process must still
	// succeed, which is the sharpest form of "a removed entry is a miss".
	full, err := wasm.ReclaimCompileCache(cacheDir, 1)
	if err != nil {
		t.Fatalf("full reclaim: %v", err)
	}
	if full.Remaining > 1 {
		t.Fatalf("a 1-byte cap left %d bytes behind", full.Remaining)
	}
	got, afterWipeDur := timeWorker("first process after every entry was removed")
	if string(got) != string(want) {
		t.Fatalf("output changed after every entry was removed\n want: %s\n  got: %s", want, got)
	}
	got, warmAfterWipeDur := timeWorker("warm after every entry was removed")
	if string(got) != string(want) {
		t.Fatalf("output changed on the warm run after every entry was removed\n want: %s\n  got: %s", want, got)
	}
	if warmAfterWipeDur >= afterWipeDur {
		t.Fatalf("the cache did not repopulate after a full reclaim: warm %s, not faster than the repopulating run %s",
			warmAfterWipeDur, afterWipeDur)
	}
	t.Logf("full reclaim removed %d entrie(s), %d bytes; repopulate=%s warm-after=%s",
		len(full.Removed), full.Reclaimed, afterWipeDur, warmAfterWipeDur)
}
