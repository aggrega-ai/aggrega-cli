// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestSharedCompilationCacheConcurrentLoads proves the process-wide shared
// compilation cache is safe to share across concurrent Loaders and leaves
// rendered output unchanged. Each goroutine mimics one served request: it
// constructs its own Loader via NewCachingLoader (the exact call every render
// path makes, all backed by the one shared cache), loads a real guest, and
// calls it. Run under `go test -race`, the test asserts
//
//	(a) no data race across the concurrently-shared cache and its Runtimes, and
//	(b) byte-identical output across every concurrent run — the cache memoizes
//	    only compiled machine code, a pure function of the guest bytes, so it
//	    cannot perturb output.
//
// Under `go test` the shared cache is in-memory, so the test stays hermetic and
// off the machine's user cache directory; the concurrency guarantee is a
// property of the wazero CompilationCache itself, identical for the disk-backed
// and in-memory variants.
func TestSharedCompilationCacheConcurrentLoads(t *testing.T) {
	// Build the fixture once, before any goroutine loads it, so the on-demand
	// build never races. Skips cleanly when the toolchain/PDK is absent.
	artifactPath := ensureFixtureBuilt(t, "policy-validator-go.wasm")

	const workers = 8
	spec := wasm.PluginSpec{Name: "policy-validator-go", Path: artifactPath}
	input := []byte(`{}`)

	// runOnce performs one request-shaped load+call against the shared cache and
	// returns the guest's output bytes. It is the body both the concurrent phase
	// and the reuse assertion run.
	runOnce := func(t *testing.T) []byte {
		t.Helper()
		loader := wasm.NewCachingLoader(nil)
		plugin, err := loader.Load(context.Background(), spec)
		if err != nil {
			t.Fatalf("loader.Load: %v", err)
		}
		defer func() { _ = plugin.Close(context.Background()) }()
		out, err := plugin.Call(context.Background(), "validate", input)
		if err != nil {
			t.Fatalf("plugin.Call validate: %v", err)
		}
		return out
	}

	outputs := make([][]byte, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			outputs[i] = runOnce(t)
		}(i)
	}
	wg.Wait()

	if t.Failed() {
		return
	}

	// Byte-identical output across every concurrent run: the shared cache
	// changed only compilation cost, never the rendered bytes.
	want := outputs[0]
	if want == nil {
		t.Fatal("worker 0 produced no output")
	}
	for i := 1; i < workers; i++ {
		if !bytes.Equal(outputs[i], want) {
			t.Fatalf("output mismatch: worker %d differs from worker 0\n  worker 0: %s\n  worker %d: %s",
				i, want, i, outputs[i])
		}
	}

	// Loader close funcs are no-ops: the process-wide shared cache must still be
	// usable after every concurrent request has "closed" its Loader. A
	// successful reuse proves no per-request path releases the shared cache.
	reused := runOnce(t)
	if !bytes.Equal(reused, want) {
		t.Fatalf("post-close reuse output differs from concurrent runs\n  want: %s\n  got:  %s", want, reused)
	}
}
