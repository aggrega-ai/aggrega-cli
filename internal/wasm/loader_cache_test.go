// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// TestSelectCompilationCacheDiskByDefault proves the default (disablePersistent
// false) path selects the persistent on-disk cache: the cache directory is
// created even though nothing else exercises it, confirming the disk branch ran.
//
// Sanctioned single-source exception: this test's PURPOSE is the on-disk
// selection, which sharedCompilationCache's test-mode gate masks by handing
// every test build the in-memory singleton. It therefore calls
// selectCompilationCache — the disk-vs-memory primitive — directly to reach the
// disk branch. Do NOT "unify" this to route through sharedCompilationCache; that
// would blind the disk coverage in every test binary.
func TestSelectCompilationCacheDiskByDefault(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := diskCacheDir(t)
	if dirExists(t, dir) {
		t.Fatalf("precondition: cache dir %s already exists in fresh temp", dir)
	}

	cache := selectCompilationCache(false)
	defer cache.Close(context.Background())

	if !dirExists(t, dir) {
		t.Fatalf("disablePersistent false should select the on-disk cache and create %s; it was not created", dir)
	}
}

// TestSelectCompilationCacheDisableSkipsDisk proves the disablePersistent-true
// path selects the in-memory cache even when an on-disk cache directory WOULD
// resolve: no cache directory is created on disk.
//
// Sanctioned single-source exception, as in TestSelectCompilationCacheDiskByDefault:
// it reaches the disk-vs-memory primitive directly, past the test-mode gate.
func TestSelectCompilationCacheDisableSkipsDisk(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := diskCacheDir(t)
	if dirExists(t, dir) {
		t.Fatalf("precondition: cache dir %s already exists in fresh temp", dir)
	}

	cache := selectCompilationCache(true)
	defer cache.Close(context.Background())

	if dirExists(t, dir) {
		t.Fatalf("disablePersistent true should select the in-memory cache and touch no disk; %s was created", dir)
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

// TestTestModeSharesOneDiskCache is the enforcement guard for the decision
// site: within a test binary every cache request MUST resolve to ONE
// process-wide cache, backed by the shared test compile-cache directory rather
// than the developer's real user cache directory. Sharing one directory across
// every test process lets each guest compile once per suite run and every later
// process reuse the machine code, eliminating the cold-recompile storm; keeping
// it off the user cache directory leaves a developer's real build cache
// untouched. It exercises the disk-defaulting input (disablePersistent false)
// and the disable input (true) and proves both resolve to that same singleton.
//
// A regression that pointed the test-mode cache at the user cache directory
// would create diskCacheDir here and fail this test; one that reverted to a
// per-process cache would break the singleton identity assertion.
func TestTestModeSharesOneDiskCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := diskCacheDir(t)
	if dirExists(t, dir) {
		t.Fatalf("precondition: cache dir %s already exists in fresh temp", dir)
	}

	// disablePersistent false is the production disk-default and true is the
	// disable dial; under go test both must hand back the SAME shared singleton
	// (one cache per process).
	c1 := sharedCompilationCache(false)
	c2 := sharedCompilationCache(true)

	if dirExists(t, dir) {
		t.Fatalf("under go test the compile cache must stay off the user cache directory; on-disk cache dir %s was created", dir)
	}
	// The test-mode cache is disk-backed at the shared test directory, which the
	// wazero cache constructor creates on construction; by now the process-wide
	// singleton exists, so the directory must be present.
	if !dirExists(t, testCompilationCacheDir()) {
		t.Fatalf("under go test the shared compile cache must be disk-backed at %s; the directory was not created", testCompilationCacheDir())
	}
	if c1 != c2 {
		t.Fatal("under go test every build must share ONE cache instance; got two distinct caches")
	}

	// The exported constructors every production path uses must route through the
	// same singleton — the plain constructor and the context-dial constructor
	// alike.
	loader := NewCachingLoader(nil)
	if loader.Cache != c1 {
		t.Fatal("NewCachingLoader must hand out the shared singleton")
	}
	fromCtx := NewCachingLoaderFromContext(context.Background(), nil)
	if fromCtx.Cache != c1 {
		t.Fatal("NewCachingLoaderFromContext must hand out the shared singleton")
	}
	// Even with the disable-dial set, it is the same process-wide singleton.
	dialOn := NewCachingLoaderFromContext(ContextWithDisablePluginCache(context.Background(), true), nil)
	if dialOn.Cache != c1 {
		t.Fatal("NewCachingLoaderFromContext with the dial set must still share the one process cache")
	}
}

// TestNoBareLoaderInProductionCode is the structural guard that no production
// (non-test) source constructs a Loader directly. A bare &Loader{} carries no
// compilation cache, so its guest loads compile in isolation — the exact
// per-loader recompilation and, on the disk-backed path, cross-process
// cache-directory contention the process-wide shared cache exists to remove.
// Every production loader must be built through NewCachingLoader or
// NewCachingLoaderFromContext, whose Cache field is the one shared singleton.
//
// The sole sanctioned direct constructions are the two literals inside those
// constructors in loader.go. Test files are exempt: a unit test may deliberately
// exercise a cache-less loader (it touches no cache directory and so cannot
// cause the contention this guard protects against).
func TestNoBareLoaderInProductionCode(t *testing.T) {
	root := moduleRoot(t)
	loaderGo := filepath.Join(root, "internal", "wasm", "loader.go")

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "vendor", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if path == loaderGo {
			return nil // the sanctioned constructor site
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)
		if strings.Contains(text, "&wasm.Loader{") {
			offenders = append(offenders, path+": &wasm.Loader{")
		}
		if strings.Contains(filepath.ToSlash(path), "/internal/wasm/") && strings.Contains(text, "&Loader{") {
			offenders = append(offenders, path+": &Loader{")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module tree: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("production code must build Loaders through wasm.NewCachingLoader / "+
			"NewCachingLoaderFromContext so every loader shares the process-wide "+
			"compilation cache; found bare loader construction(s):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// moduleRoot walks up from the test's working directory to the directory holding
// go.mod, the module root the production-code guard scans.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the working directory")
		}
		dir = parent
	}
}
