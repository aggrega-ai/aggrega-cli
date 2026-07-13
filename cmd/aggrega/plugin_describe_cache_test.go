// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestDescribeCacheRoundTrip proves the digest-keyed describe cache serves a
// stored descriptor on a later read (the "described once, re-read on subsequent
// runs" contract for configured plugins) and that a distinct digest is a cache
// miss (a changed resolved artifact invalidates the entry because a new digest
// is a new key).
func TestDescribeCacheRoundTrip(t *testing.T) {
	// The describe cache is a persistent, content-addressed store, so the test
	// keys on bytes made unique to this run — a never-before-written digest — so
	// the "fresh digest is a miss" assertion holds independent of any entry an
	// earlier run left in the shared cache directory. A different guest yields a
	// different digest.
	nonce := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	digest := describeCacheDigest([]byte("guest-bytes-v1-" + nonce))
	other := describeCacheDigest([]byte("guest-bytes-v2-" + nonce))
	if digest == other {
		t.Fatal("distinct guest bytes produced the same digest")
	}

	if _, ok := readDescribeCache(digest); ok {
		t.Fatalf("cache unexpectedly warm for a fresh digest %q", digest)
	}

	want := wasm.PluginDescriptor{
		ABIVersion:          wasm.ABIVersionV1Alpha1,
		PluginType:          wasm.PluginTypeProvider,
		CompatibleProviders: []string{"terraform"},
		Exports:             []wasm.PluginExport{{Name: "render_node"}},
	}
	writeDescribeCache(digest, want)

	got, ok := readDescribeCache(digest)
	if !ok {
		t.Fatal("cache miss after write")
	}
	if got.ABIVersion != want.ABIVersion || got.PluginType != want.PluginType ||
		len(got.CompatibleProviders) != 1 || got.CompatibleProviders[0] != "terraform" ||
		len(got.Exports) != 1 || got.Exports[0].Name != "render_node" {
		t.Errorf("cached descriptor mismatch: got %+v want %+v", got, want)
	}

	// A different digest — a changed resolved artifact — must not read the
	// stored entry.
	if _, ok := readDescribeCache(other); ok {
		t.Errorf("digest %q unexpectedly hit the entry stored under %q", other, digest)
	}
}
