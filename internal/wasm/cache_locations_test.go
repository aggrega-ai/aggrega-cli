// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// pathIsInside reports whether path lies at or beneath root, comparing both after
// cleaning so a trailing separator or an unnormalized segment does not change the
// answer. A path on a different branch yields a relative path that escapes
// upward, which is the negative case.
//
// It compares whole path segments rather than raw strings, which is what the
// guards below need: the two directories they distinguish are siblings whose names
// share a prefix, and a string-prefix test would read one as nested inside the
// other. The guard on that property is asserted directly further down.
//
// It is spelled here independently of the containment predicate the compiler
// itself uses, and the duplication is deliberate: do not "unify" it. This is the
// oracle the guards below judge the resolver's answers with, and an oracle that
// asked the product the same question it is checking would agree with the product
// wherever the product was wrong. Both halves of the comparison have to be reached
// for separately for either guard to mean anything.
func pathIsInside(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// developerCacheRootDirName is the directory a normal invocation takes beneath the
// user cache directory. It is spelled here independently of the resolver, and the
// duplication is deliberate: do not "unify" it.
//
// The guards below judge the resolver's own answer, so a guard that asked the
// resolver where a normal invocation's cache lives would compare that answer
// against itself and pass wherever the cache landed. Both halves of the comparison
// have to be reached for separately for either guard to mean anything.
//
// The staleness a literal would otherwise invite — this name renamed on one side
// only, leaving the comparison pointed at a directory nothing uses — is closed by
// the census guard's depth assertion, which forbids a resolved root from sitting
// inside a sibling directory whatever that sibling is named.
const developerCacheRootDirName = "aggrega"

// developerCacheRoot returns the directory a normal invocation's caches live
// under, derived from the standard library rather than from the resolver, and
// whether this environment resolves one at all.
func developerCacheRoot() (string, bool) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(base, developerCacheRootDirName), true
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
	// The documentation bundle has two spellings of its subtree as well: the one
	// its acquisition and every read keep it under, and the one the cache
	// inventory lists and clears.
	docsRoot, docsErr := docsbundle.CacheRoot(context.Background())
	locs = append(locs, cacheLocation{
		name:     "docsbundle.CacheRoot (documentation-bundle subtree)",
		path:     docscache.DocsBundlesRoot(docsRoot),
		resolved: docsErr == nil,
	})
	inventory := cacheops.ResolveLocations(context.Background(), "", "").DocsBundlesDir
	locs = append(locs, cacheLocation{
		name:     "cacheops.ResolveLocations (documentation-bundle subtree)",
		path:     inventory,
		resolved: inventory != "",
	})
	// The store of cli plugins' bytes is resolved from a workspace resolution rather
	// than from the process, so it is asked of one taken through the process's own
	// environment, as the command line takes it: once as the addition writes it, and
	// once as the cache inventory lists and clears it.
	rw, rwErr := workspaceconfig.Resolve(t.TempDir(), "")
	if rwErr != nil {
		t.Fatalf("resolve a folder through the process environment: %v", rwErr)
	}
	storeRoot, storeDir, storeErr := rw.MachineArtifactCache()
	locs = append(locs, cacheLocation{
		name:     "workspaceconfig.ResolvedWorkspace.MachineArtifactCache",
		path:     filepath.Join(storeRoot, storeDir),
		resolved: storeErr == nil,
	})
	listed := cacheops.ResolveLocations(context.Background(), "", "").WithCLIPluginsOf(rw).CLIPluginsDir
	locs = append(locs, cacheLocation{
		name:     "cacheops.Locations.WithCLIPluginsOf (cli-plugin store)",
		path:     listed,
		resolved: listed != "",
	})
	return locs
}

// TestTestBinaryCannotResolveTheDeveloperCache is the guard on the one
// irreversible case on this surface. The cache-maintenance command family resolves
// its compiled-module directory through CompileCacheDir, and its clear verb
// removes that directory outright. Within a test binary that resolution must
// therefore never land inside the directory a normal invocation owns: those bytes
// belong to a developer's own builds, nothing a test holds can reconstruct them,
// and a single call would delete them.
//
// The criterion is that directory and not the whole user cache directory, and the
// difference is what lets a test binary keep its caches somewhere only their owner
// can write. A criterion drawn at the user cache directory would forbid the one
// naturally-private place a cache can live and push it into a shared location every
// local account may write to — which, for a store of machine code the runtime maps
// executable, is a far worse exposure than the deletion this guard exists to
// prevent. Drawn at the directory a normal invocation owns, the deletion risk is
// still covered in full, because every removal the maintenance surface performs is
// bounded to the root the running process resolved and a sibling is not reachable
// from it.
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
	devRoot, devOK := developerCacheRoot()
	if !devOK {
		t.Skip("no user cache directory resolves in this environment, so there is no developer cache to protect")
	}
	dir, ok := wasm.CompileCacheDir()
	if !ok {
		t.Fatal("CompileCacheDir did not resolve; this guard needs a resolved path to judge")
	}
	if pathIsInside(dir, devRoot) {
		t.Fatalf("within a test binary the compiled-module cache directory must not resolve inside "+
			"the directory a normal invocation owns.\n  CompileCacheDir() = %s\n"+
			"  directory a normal invocation owns = %s\n"+
			"The cache-maintenance clear verb removes this directory outright, so this resolution "+
			"leaves a developer's own build cache one call away from irreversible deletion.",
			dir, devRoot)
	}
}

// TestEveryCacheLocationStaysInsideOneRootUnderTest is the completeness guard on
// the cache-location decision: within a test binary EVERY resolvable cache path
// must lie inside the one cache root, the directory that root is named for must
// sit directly beneath the user cache directory (the root being that directory
// itself, or on Windows the cache directory inside it), and no path may lie
// inside the directory a normal invocation owns.
//
// The three claims are separate and each catches something the others do not.
//
// "Inside one root" is what makes the decision single: a path that resolves
// somewhere else is a second site deciding for itself, which is how one cache came
// to be written in one place and read from another.
//
// "Directly beneath the user cache directory" fixes the root's depth at exactly one
// level, which is the claim that does not go stale. It rejects a root nested inside
// any sibling directory — including one whose name nothing in this file mentions —
// so a later rename on the resolver's side cannot quietly leave the containment
// check below pointed at a directory nothing uses. It also rejects a root placed
// outside the user's own cache tree, which is where a store of machine code the
// runtime maps executable must never live: a directory a second local account can
// write to is code execution inside whatever process next reads it.
//
// "Not inside the directory a normal invocation owns" is the irreversible case: the
// cache-maintenance surface removes a resolved directory outright, and those bytes
// belong to a developer's own builds and cannot be reconstructed from anything a
// test holds. Being a sibling rather than an ancestor or a descendant is what makes
// that unreachable in both directions, since every removal is bounded to the root
// the running process resolved.
//
// The assertions are framed against the resolved root rather than against a literal
// path wherever that is possible, so they hold whatever that root is named and
// cannot pass by agreeing with a stale copy of the name.
func TestEveryCacheLocationStaysInsideOneRootUnderTest(t *testing.T) {
	userCache, userCacheErr := os.UserCacheDir()
	if userCacheErr != nil {
		t.Skipf("no user cache directory resolves in this environment (%v), so no persistent cache "+
			"root exists for this guard to judge", userCacheErr)
	}
	root, ok := wasm.AggregaCacheRoot()
	if !ok {
		t.Fatal("a user cache directory resolves but no cache root did; every cache location is decided relative to it")
	}

	// A census that resolved nothing would certify nothing, so its own yield is
	// asserted before any of its results are trusted.
	locs := everyCacheLocation(t)
	if len(locs) == 0 {
		t.Fatal("the cache-location census yielded no locations, so this guard proved nothing")
	}

	// The directory the root is named for sits directly beneath the user cache
	// directory. On Windows the root is a directory named cache inside it, because
	// the user cache directory there holds an application's local data of every
	// kind rather than its caches alone; elsewhere the named directory is itself
	// the root. Spelled here from the platform rather than asked of the resolver,
	// for the reason developerCacheRootDirName is.
	owner := filepath.Clean(root)
	if runtime.GOOS == "windows" {
		if base := filepath.Base(owner); base != "cache" {
			t.Errorf("on Windows the cache root must be a directory named cache inside the directory it is "+
				"named for.\n  cache root = %s\n  its last element = %s", root, base)
		}
		owner = filepath.Dir(owner)
	}
	if parent := filepath.Dir(owner); parent != filepath.Clean(userCache) {
		t.Errorf("the directory the cache root is named for must sit directly beneath the user cache "+
			"directory, one level down.\n"+
			"  cache root = %s\n  the directory it is named for = %s\n  its parent = %s\n  user cache directory = %s\n"+
			"A root deeper than that is nested inside some other directory beneath the user cache "+
			"directory, which is how a test run reaches bytes it does not own; a root outside the "+
			"user cache tree is a store of executable machine code somewhere another local account "+
			"may be able to write.",
			root, owner, parent, userCache)
	}

	devRoot, devOK := developerCacheRoot()

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
			if devOK && pathIsInside(loc.path, devRoot) {
				t.Errorf("%s resolved inside the directory a normal invocation owns, within a test "+
					"binary, which puts a developer's own cache within reach of the cache-maintenance "+
					"surface.\n  resolved = %s\n  directory a normal invocation owns = %s",
					loc.name, loc.path, devRoot)
			}
		})
	}
}

// TestTheMachineWideCacheRootFollowsTheStandardLibraryOnTheHost holds the root a
// normal invocation keeps its machine-wide caches under to the per-user cache
// directory the standard library reports for this process, asked here
// independently for the reason developerCacheRoot asks it.
//
// The location rule restates the standard library's per-user cache rule so it
// can be read for any platform and from any environment; on the host and with
// the process's own environment it must land exactly where the standard library
// says — the directory named aggrega beneath it, and on Windows the cache
// directory inside that — or fail to resolve exactly where the standard library
// fails. A drift between the two is what would move the caches a Linux or macOS
// machine already has.
func TestTheMachineWideCacheRootFollowsTheStandardLibraryOnTheHost(t *testing.T) {
	userCache, userCacheErr := os.UserCacheDir()
	root, err := machinedirs.CacheRoot(machinedirs.Host(), os.Getenv)
	if (err == nil) != (userCacheErr == nil) {
		t.Fatalf("the cache root resolves as %q, %v on %s; the standard library's per-user cache directory resolves as %q, %v",
			root, err, machinedirs.Host(), userCache, userCacheErr)
	}
	if err != nil {
		return
	}
	want := filepath.Join(userCache, developerCacheRootDirName)
	if runtime.GOOS == "windows" {
		want = filepath.Join(want, "cache")
	}
	if filepath.Clean(root) != filepath.Clean(want) {
		t.Errorf("the machine-wide cache root is %s; beneath the standard library's per-user cache directory it is %s", root, want)
	}
}

// TestCachePathContainmentComparesWholeSegments is the guard on the comparison the
// two guards above are built from. Each asks whether a resolved cache location lies
// inside the directory a normal invocation owns, and a test binary's root is a
// sibling of that directory whose name extends it — so a containment test written as
// a raw string prefix would report the sibling as nested and refuse a location that
// is correct.
//
// The truncated-name case is asserted explicitly rather than left implied, because
// it is the one a prefix comparison gets wrong while every other case in the table
// still passes: a sibling reads as contained, and nothing else in the suite would
// say so.
func TestCachePathContainmentComparesWholeSegments(t *testing.T) {
	// A fabricated tree, not this machine's, so the table states the shape of the
	// comparison rather than depending on where the suite happens to run.
	const base = "/home/dev/.cache"
	developerRoot := filepath.Join(base, developerCacheRootDirName)
	siblingRoot := developerRoot + "-test"

	for _, tc := range []struct {
		name   string
		path   string
		inside bool
	}{
		{"a sibling whose name extends the root is not inside it", siblingRoot, false},
		{"a location beneath that sibling is not inside the root", filepath.Join(siblingRoot, "wasm-compile"), false},
		{"the root is inside itself", developerRoot, true},
		{"a location beneath the root is inside it", filepath.Join(developerRoot, "wasm-compile"), true},
		{"the shared parent is not inside the root", base, false},
		{"a same-named directory on another branch is not inside the root", filepath.Join(base, "elsewhere", developerCacheRootDirName), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathIsInside(tc.path, developerRoot); got != tc.inside {
				t.Errorf("pathIsInside(%q, %q) = %v, want %v; the containment test must compare whole "+
					"path segments, not string prefixes", tc.path, developerRoot, got, tc.inside)
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
