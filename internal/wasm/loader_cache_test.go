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

// TestCompilationCacheAtSelectsDisk proves the on-disk branch is real: given a
// directory, the disk-backed cache is built and the directory is created, and the
// returned directory is the one that was asked for.
//
// The disk-vs-memory branch is otherwise unobservable — wazero.CompilationCache is
// opaque — so the directory's existence is the faithful side effect to assert:
// NewCompilationCacheWithDir creates it, the in-memory constructor touches no
// filesystem.
//
// Sanctioned exception, and the reason it must stay one: this test's PURPOSE is
// the on-disk selection, which every other path in a test binary masks — the
// process-wide cache is a singleton constructed once, and its directory is the
// one shared cache root, which no test may start empty or safely populate. The
// test therefore passes its OWN throwaway directory to the primitive. Do not
// "unify" this to route through sharedCompilationCache; that would blind the disk
// coverage in every test binary.
func TestCompilationCacheAtSelectsDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), compileCacheDirName)
	if dirExists(t, dir) {
		t.Fatalf("precondition: cache dir %s already exists in a fresh temp dir", dir)
	}

	cache, got := compilationCacheAt(dir)
	defer cache.Close(context.Background())

	if got != dir {
		t.Fatalf("compilationCacheAt should report the directory it backed the cache with; want %s, got %q", dir, got)
	}
	if !dirExists(t, dir) {
		t.Fatalf("the on-disk branch should create %s; it was not created", dir)
	}
}

// TestSelectCompilationCacheDisableSkipsDisk proves the disablePersistent-true
// path selects the in-memory cache: it reports no backing directory at all, so no
// cache directory is resolved and none can be created.
//
// Asserting on the reported directory rather than on some directory's absence is
// what makes this independent of where caches live: an in-memory cache has no
// directory to name, which is a property of the branch itself.
func TestSelectCompilationCacheDisableSkipsDisk(t *testing.T) {
	cache, dir := selectCompilationCacheWithDir(true)
	defer cache.Close(context.Background())

	if dir != "" {
		t.Fatalf("disablePersistent true should select the in-memory cache, which backs onto no directory; got %q", dir)
	}
}

// TestSelectCompilationCacheDiskByDefault proves the default (disablePersistent
// false) path selects the persistent on-disk cache and backs it with the one
// resolved compile-cache directory — not a second, separately-derived path.
func TestSelectCompilationCacheDiskByDefault(t *testing.T) {
	want, ok := CompileCacheDir()
	if !ok {
		t.Fatal("CompileCacheDir did not resolve; this test needs a resolved compile-cache directory")
	}

	cache, dir := selectCompilationCacheWithDir(false)
	defer cache.Close(context.Background())

	if dir != want {
		t.Fatalf("disablePersistent false should back the cache with the resolved compile-cache directory; want %s, got %q", want, dir)
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
// process-wide cache, disk-backed at the one resolved compile-cache directory.
// Sharing one directory across every test process lets each guest compile once
// per run and every later process reuse the machine code, eliminating the
// cold-recompile storm. It exercises the disk-defaulting input (disablePersistent
// false) and the disable input (true) and proves both resolve to that same
// singleton.
//
// Where that directory lives is asserted by the cache-location census, not here;
// this test owns the singleton identity and the disk backing. A regression that
// reverted to a per-process cache would break the identity assertion; one that
// moved the location would fail the census.
func TestTestModeSharesOneDiskCache(t *testing.T) {
	dir, ok := CompileCacheDir()
	if !ok {
		t.Fatal("CompileCacheDir did not resolve; this test needs a resolved compile-cache directory")
	}

	// disablePersistent false is the production disk-default and true is the
	// disable dial; under go test both must hand back the SAME shared singleton
	// (one cache per process).
	c1 := sharedCompilationCache(false)
	c2 := sharedCompilationCache(true)

	// The test-mode cache is disk-backed at the resolved compile-cache directory,
	// which the wazero cache constructor creates on construction; by now the
	// process-wide singleton exists, so the directory must be present.
	if !dirExists(t, dir) {
		t.Fatalf("under go test the shared compile cache must be disk-backed at %s; the directory was not created", dir)
	}
	if sharedCompilationCacheDir != dir {
		t.Fatalf("the recorded shared cache directory must be the resolved compile-cache directory, "+
			"so the cross-process compile lock places its lock files where the cache actually is; want %s, got %q",
			dir, sharedCompilationCacheDir)
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
