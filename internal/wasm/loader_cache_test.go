// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"os"
	"testing"
)

// diskCacheDir returns the on-disk compile-cache directory that the loader
// would use given the current environment, and whether one resolves. The
// disk-vs-memory branch is otherwise unobservable — wazero.CompilationCache is
// opaque — so these tests assert on the cache directory's existence as the
// faithful side effect of the disk branch: NewCompilationCacheWithDir creates
// the directory, the in-memory constructor touches no filesystem.
func diskCacheDir(t *testing.T) string {
	t.Helper()
	dir, ok := wasmCompileCacheDir()
	if !ok {
		t.Fatal("wasmCompileCacheDir did not resolve; test needs a resolvable cache dir")
	}
	return dir
}

func dirExists(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(dir)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("stat %s: %v", dir, err)
	return false
}

// TestNewCompilationCacheDiskByDefault proves the default (dial off) path
// selects the persistent on-disk cache: the cache directory is created even
// though nothing else exercises it, confirming the disk branch ran.
func TestNewCompilationCacheDiskByDefault(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := diskCacheDir(t)
	if dirExists(t, dir) {
		t.Fatalf("precondition: cache dir %s already exists in fresh temp", dir)
	}

	cache := newCompilationCache(false)
	defer cache.Close(context.Background())

	if !dirExists(t, dir) {
		t.Fatalf("dial off should select the on-disk cache and create %s; it was not created", dir)
	}
}

// TestNewCompilationCacheDisableSkipsDisk proves the dial-on path selects the
// in-memory cache even when an on-disk cache directory WOULD resolve: no cache
// directory is created on disk.
func TestNewCompilationCacheDisableSkipsDisk(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := diskCacheDir(t)
	if dirExists(t, dir) {
		t.Fatalf("precondition: cache dir %s already exists in fresh temp", dir)
	}

	cache := newCompilationCache(true)
	defer cache.Close(context.Background())

	if dirExists(t, dir) {
		t.Fatalf("dial on should select the in-memory cache and touch no disk; %s was created", dir)
	}
}

// TestDisablePluginCacheContextRoundTrip covers the context dial accessor: the
// zero value is false (default disk-backed behaviour), and a true signal round
// trips.
func TestDisablePluginCacheContextRoundTrip(t *testing.T) {
	if DisablePluginCacheFromContext(context.Background()) {
		t.Fatal("no signal attached should read as false")
	}
	on := ContextWithDisablePluginCache(context.Background(), true)
	if !DisablePluginCacheFromContext(on) {
		t.Fatal("true signal did not round trip")
	}
	off := ContextWithDisablePluginCache(context.Background(), false)
	if DisablePluginCacheFromContext(off) {
		t.Fatal("false signal should read as false")
	}
}

// TestNewCachingLoaderFromContextHonorsDial ties the context dial to the loader
// constructor the render pipeline uses: with the dial set, the constructor
// selects the in-memory cache and writes no cache directory; without it, the
// on-disk cache directory is created. Asserted end-to-end through the exported
// constructor rather than the internal helper.
func TestNewCachingLoaderFromContextHonorsDial(t *testing.T) {
	t.Run("dial on skips disk", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		dir := diskCacheDir(t)
		ctx := ContextWithDisablePluginCache(context.Background(), true)

		_, closeLoader := NewCachingLoaderFromContext(ctx, nil)
		defer closeLoader()

		if dirExists(t, dir) {
			t.Fatalf("dial on: expected no on-disk cache, but %s was created", dir)
		}
	})

	t.Run("dial off uses disk", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		dir := diskCacheDir(t)

		_, closeLoader := NewCachingLoaderFromContext(context.Background(), nil)
		defer closeLoader()

		if !dirExists(t, dir) {
			t.Fatalf("dial off: expected the on-disk cache at %s, but it was not created", dir)
		}
	})
}
