// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
)

// pathIsInside reports whether path lies at or beneath root, comparing both after
// cleaning so a trailing separator or an unnormalized segment does not change the
// answer. A path on a different branch yields a relative path that escapes
// upward, which is the negative case.
func pathIsInside(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cacheLocation is one resolvable cache path, named as a caller would ask for it.
type cacheLocation struct {
	// name is the accessor a caller reaches for, so a failure names the site to fix.
	name string
	// path is what that accessor resolved to, or "" when it reported that nothing
	// resolves.
	path string
	// resolved is the accessor's own report of whether a path exists to act on.
	resolved bool
}

// everyCacheLocation resolves every cache path this module can address. It is the
// census the guards below are taken over, so a cache added later is covered by
// adding one entry here rather than by writing a second test.
//
// The loader's own compile directory is not listed: it is recorded internally when
// the process-wide cache is constructed, so it is asserted from inside the package
// (see the shared-cache identity guard) where that value is visible. Every entry
// here is a path a caller resolves for itself.
func everyCacheLocation(t *testing.T) []cacheLocation {
	t.Helper()

	root, rootOK := wasm.AggregaCacheRoot()
	compileDir, compileOK := wasm.CompileCacheDir()
	describeDir, describeOK := wasm.PluginDescribeCacheDir()
	provPath, provOK := provindex.DefaultIndexPath()

	locs := []cacheLocation{
		{name: "wasm.AggregaCacheRoot", path: root, resolved: rootOK},
		{name: "wasm.CompileCacheDir", path: compileDir, resolved: compileOK},
		{name: "wasm.PluginDescribeCacheDir", path: describeDir, resolved: describeOK},
		{name: "provindex.DefaultIndexPath", path: provPath, resolved: provOK},
	}
	// The attribution index takes its root as a parameter rather than resolving
	// one, so its two spellings are both census entries: the read side asks for the
	// path under the resolved root, and the write side derives it from the compile
	// directory the loader was given.
	if rootOK {
		locs = append(locs, cacheLocation{
			name:     "attribindex.DefaultIndexPath",
			path:     attribindex.DefaultIndexPath(root),
			resolved: true,
		})
	}
	if compileOK {
		locs = append(locs, cacheLocation{
			name:     "attribindex.PathForCompileDir",
			path:     attribindex.PathForCompileDir(compileDir),
			resolved: true,
		})
	}
	return locs
}

// TestTestBinaryCannotResolveTheDeveloperCache is the guard on the one
// irreversible case on this surface. The cache-maintenance command family resolves
// its compiled-module directory through CompileCacheDir, and its clear verb
// removes that directory outright. Within a test binary that resolution must
// therefore never land inside the user cache directory: those bytes belong to a
// developer's own builds, nothing a test holds can reconstruct them, and a single
// call would delete them.
//
// It names the compiled-module directory specifically, where the census below
// covers every location. The narrower guard is kept because this is the one
// location whose loss is irreversible, so a failure here means something different
// from a failure there, and the message a maintainer reads should say so.
//
// This file asks the standard library where the user cache directory is, and it is
// the one place outside the resolver that may. Do not "unify" it by routing the
// question through the resolver: the resolver's answer is precisely what is under
// test here, so comparing it against itself would turn both guards in this file
// into tautologies that pass no matter where the cache lands. The independence is
// the point — this side of the comparison has to be reached for separately.
func TestTestBinaryCannotResolveTheDeveloperCache(t *testing.T) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache directory resolves in this environment: %v", err)
	}
	dir, ok := wasm.CompileCacheDir()
	if !ok {
		t.Fatal("CompileCacheDir did not resolve; this guard needs a resolved path to judge")
	}
	if pathIsInside(dir, userCache) {
		t.Fatalf("within a test binary the compiled-module cache directory must not resolve inside "+
			"the user cache directory.\n  CompileCacheDir() = %s\n  user cache directory = %s\n"+
			"The cache-maintenance clear verb removes this directory outright, so this resolution "+
			"leaves a developer's own build cache one call away from irreversible deletion.",
			dir, userCache)
	}
}

// TestEveryCacheLocationStaysInsideOneRootUnderTest is the completeness guard on
// the cache-location decision: within a test binary EVERY resolvable cache path
// must lie inside the one cache root, and that root must lie outside the user
// cache directory.
//
// The two halves are separate claims and both matter. "Inside one root" is what
// makes the decision single: a path that resolves somewhere else is a second site
// deciding for itself, which is how one cache came to be written in one place and
// read from another. "Outside the user cache directory" is the irreversible case:
// the cache-maintenance surface can remove a resolved directory outright, and the
// bytes under the user cache directory belong to a developer's own builds and
// cannot be reconstructed from anything a test holds.
//
// The assertions are framed against the resolved root rather than against a
// literal path, so they hold whatever that root is named and cannot pass by
// agreeing with a stale copy of the name.
func TestEveryCacheLocationStaysInsideOneRootUnderTest(t *testing.T) {
	root, ok := wasm.AggregaCacheRoot()
	if !ok {
		t.Fatal("no cache root resolved inside a test binary; every cache location is decided relative to it")
	}

	// A census that resolved nothing would certify nothing, so its own yield is
	// asserted before any of its results are trusted.
	locs := everyCacheLocation(t)
	if len(locs) == 0 {
		t.Fatal("the cache-location census yielded no locations, so this guard proved nothing")
	}

	userCache, userCacheErr := os.UserCacheDir()

	for _, loc := range locs {
		t.Run(loc.name, func(t *testing.T) {
			if !loc.resolved {
				t.Fatalf("%s reported no resolvable path inside a test binary; every cache location "+
					"is resolved from the one cache root, which did resolve", loc.name)
			}
			if !pathIsInside(loc.path, root) {
				t.Errorf("%s resolved outside the cache root, so it is deciding its own location "+
					"rather than deriving it.\n  resolved = %s\n  cache root = %s",
					loc.name, loc.path, root)
			}
			if userCacheErr == nil && pathIsInside(loc.path, userCache) {
				t.Errorf("%s resolved inside the user cache directory within a test binary, which puts a "+
					"developer's own cache within reach of the cache-maintenance surface.\n"+
					"  resolved = %s\n  user cache directory = %s",
					loc.name, loc.path, userCache)
			}
		})
	}
}

// TestAttributionIndexReadAndWriteSidesMeet is the guard on the specific failure
// the census above generalises: the compile-attribution index has two spellings —
// one asked for under the cache root, one derived from the compile directory — and
// they must name the same file. When they did not, every write landed where no
// read would look, so the index silently held nothing and no error was raised
// anywhere.
func TestAttributionIndexReadAndWriteSidesMeet(t *testing.T) {
	root, ok := wasm.AggregaCacheRoot()
	if !ok {
		t.Fatal("no cache root resolved; the read side is resolved relative to it")
	}
	compileDir, ok := wasm.CompileCacheDir()
	if !ok {
		t.Fatal("no compile-cache directory resolved; the write side is derived from it")
	}

	readSide := attribindex.DefaultIndexPath(root)
	writeSide := attribindex.PathForCompileDir(compileDir)

	if readSide != writeSide {
		t.Fatalf("the compile-attribution index must be read from and written to one path; "+
			"a disagreement makes every write invisible to every read, with nothing reporting it.\n"+
			"  read side  (DefaultIndexPath under the cache root) = %s\n"+
			"  write side (PathForCompileDir of the compile dir)  = %s",
			readSide, writeSide)
	}
}
