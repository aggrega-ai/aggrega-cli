// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package wasmfixture builds the test .wasm artifacts whose sources live inside
// this module, and holds the discipline every fixture build in this module
// shares: the module root a fixture path is joined onto, the shared fixture
// directory, at most one build per artifact per test process, the cross-process
// build lock, the staging directory, publishing by rename and the freshness
// check. The package is internal to aggrega-cli and is intended only for use
// from `*_test.go` files and the test-support packages they call; the helpers
// depend on the testing package.
//
// [EnsureLocalGuestBuilt] reads nothing above the module root: its source, the
// fixture directory and the module root itself all resolve inside the module.
// The shared helpers read only the paths their caller hands them. The layouts
// that build from the plugin kit and ABI checkouts beside this repository are
// the wasmtest package's, which builds on this one.
//
// Every artifact lands under `aggrega-cli/internal/wasm/testdata/`, written
// only by rename, so a test never reads an artifact another test process is in
// the middle of writing.
//
// Concurrency is guarded at two scopes, because they answer different
// questions. Publishing by rename keeps a reader from seeing a half-written
// destination file. A lock held for the whole duration of a build keeps two
// builders from interleaving inside a shared source directory, where build
// scripts install dependencies and assemble intermediate output at fixed paths;
// content produced wrongly there is already wrong by the time any rename
// happens, so the rename cannot speak to it.
package wasmfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
)

// fixtureResults caches the per-fixture build outcome for the
// lifetime of the `go test` process. The cached value is either the
// built artifact's path (a string) or the skip reason (an error). A
// build run that succeeds or skips is not retried by subsequent tests
// in the same process.
var fixtureResults sync.Map

// fixtureBuildLocks holds one mutex per artifact name, so at most one
// builder per artifact runs at a time. Consulting the result cache and
// building on a miss are two steps rather than one, and acting on an
// unlocked miss is what lets every caller that missed at the same moment
// start its own build of the same output path. The lock is per artifact
// rather than one global lock so unrelated fixtures still build
// concurrently.
var fixtureBuildLocks sync.Map

// BuildFixtureOnce returns the memoised outcome for artifactName, calling
// build at most once per artifact across concurrent callers. Callers that
// arrive while a build is in flight queue on the artifact's lock and then
// observe the stored result rather than repeating the build. A skip reason
// is cached and shared, so it is derived once per process rather than once
// per caller.
//
// A build that ends the calling goroutine (t.Fatalf, t.Skipf) stores
// nothing: a hard failure is the calling test's, not a verdict to serve to
// every later caller.
//
// The memo is one per test process and keyed on the artifact name alone, so
// every layout that builds through it shares one name space.
func BuildFixtureOnce(artifactName string, build func() (string, error)) (string, error) {
	if path, skip, ok := loadFixtureResult(artifactName); ok {
		return path, skip
	}

	actual, _ := fixtureBuildLocks.LoadOrStore(artifactName, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// The unlocked read above is only a fast path. This one is
	// authoritative: a caller that queued behind the builder ahead of it
	// finds that builder's result here instead of rebuilding.
	if path, skip, ok := loadFixtureResult(artifactName); ok {
		return path, skip
	}

	path, err := build()
	if err != nil {
		fixtureResults.Store(artifactName, err)
		return "", err
	}
	fixtureResults.Store(artifactName, path)
	return path, nil
}

func loadFixtureResult(artifactName string) (path string, skip error, ok bool) {
	cached, found := fixtureResults.Load(artifactName)
	if !found {
		return "", nil, false
	}
	switch v := cached.(type) {
	case string:
		return v, nil, true
	case error:
		return "", v, true
	}
	return "", nil, false
}

// EnsureLocalGuestBuilt builds a self-contained Go guest whose source lives
// inside this module and returns the built artifact's absolute path.
//
// srcDir is an absolute path to a directory holding the guest's main.go and its
// own go.mod. It is addressed directly rather than derived from an artifact
// name, so this reaches a guest the name-parsed layouts of the wasmtest package
// cannot: one authored here rather than in a per-language plugin kit.
// artifactName is the name the built guest is published under in the shared
// fixture directory, and it must not collide with a name either of those
// layouts owns.
//
// It exists for a guest that is DELIBERATELY non-conforming — one built to
// violate the plugin contract so a host refusal can be driven and its
// diagnostic read. Such a guest belongs beside the tests that drive it rather
// than in a kit of reference examples, and it hand-writes its own wire bytes,
// because a generated codec exists precisely to make the mistakes it has to make
// unrepresentable. It serves equally a conforming guest built to make one
// contract claim no reference example makes, so the host's consumers of that
// claim can be driven with a real guest before any shipped plugin declares it.
//
// There is no toolchain-absent skip and there must not be one. The build is the
// standard Go toolchain targeting wasip1, which ships with every Go
// distribution, so the toolchain is present whenever this runs at all — a caller
// is executing under `go test`. A skip here would let a proof that never ran
// read as a proof that passed, which for a fixture whose whole purpose is to
// make an unprovable registration provable is the one failure mode worth
// refusing outright. Any build failure is therefore a hard test failure.
//
// The cache, lock and publish-by-rename discipline is the one every layout
// shares: at most one builder per artifact across processes, and the
// destination only ever written by rename.
func EnsureLocalGuestBuilt(t *testing.T, srcDir, artifactName string) string {
	t.Helper()

	path, skip := BuildFixtureOnce(artifactName, func() (string, error) {
		t.Helper()

		moduleRoot := ModuleRoot(t)
		if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
			t.Fatalf("local guest source missing at %s: %v (the fixture's main.go is supposed to ship; absence is a regression)", srcDir, err)
		}

		testdataDir := FixtureDir(moduleRoot)
		artifactPath := filepath.Join(testdataDir, artifactName)
		if IsFresh(artifactPath, srcDir) {
			return artifactPath, nil
		}
		if err := os.MkdirAll(testdataDir, 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}

		release := LockFixtureBuild(testdataDir, artifactName)
		defer release()

		if IsFresh(artifactPath, srcDir) {
			return artifactPath, nil
		}

		stageDir, err := NewStageDir(testdataDir)
		if err != nil {
			t.Fatalf("stage local guest build for %s: %v", artifactName, err)
		}
		defer os.RemoveAll(stageDir)

		staged := filepath.Join(stageDir, artifactName)
		// GOWORK=off is what makes the guest's own go.mod authoritative: a Go
		// workspace file in an enclosing directory does not enrol these
		// single-file guest modules, and resolving them through it would either
		// fail or bind them to whatever that workspace pins rather than to what
		// the guest declares.
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", staged, "./")
		cmd.Dir = srcDir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("local guest build failed for %s at %s: %v\noutput:\n%s",
				artifactName, srcDir, err, string(output))
		}
		if _, err := os.Stat(staged); err != nil {
			t.Fatalf("local guest build for %s reported success but produced nothing at %s: %v",
				artifactName, staged, err)
		}
		if err := PublishArtifact(staged, artifactPath); err != nil {
			t.Fatalf("publish local guest %s: %v", artifactName, err)
		}
		return artifactPath, nil
	})
	if skip != nil {
		t.Fatalf("%v", skip)
	}
	return path
}

// ModuleRoot returns the root directory of the module this package belongs to:
// the directory of the nearest Go module file at or above the test's working
// directory. Every path a fixture build reads or writes inside this repository —
// a plugin's source, the shared fixture directory, the module a Go guest is built
// from — is joined onto it, so those paths resolve inside the repository however
// it is checked out.
//
// The module file found must declare the module that holds this package. A test
// whose working directory has moved outside the module fails here rather than
// resolving its fixtures against an unrelated tree, which is why a test that
// changes its working directory resolves its fixtures first.
func ModuleRoot(t testing.TB) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("locate the working directory: %v", err)
	}
	root, err := moduleRootAbove(cwd)
	if err != nil {
		t.Fatalf("resolve this module's root: %v", err)
	}
	return root
}

// moduleRootAbove is [ModuleRoot] from an explicit starting directory.
func moduleRootAbove(start string) (string, error) {
	root, modulePath, err := projectionparity.ModuleRoot(start)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(ownImportPath, modulePath+"/") {
		return "", fmt.Errorf("the module file nearest %s declares module %s, which does not hold %s",
			start, modulePath, ownImportPath)
	}
	return root, nil
}

// ownImportPath is this package's import path, read from a type it declares
// rather than spelled, so the check in moduleRootAbove follows the module if it
// is renamed.
var ownImportPath = reflect.TypeOf(moduleAnchor{}).PkgPath()

// moduleAnchor carries this package's import path for ownImportPath.
type moduleAnchor struct{}

// FixtureDir is the shared fixture directory every layout publishes into.
func FixtureDir(moduleRoot string) string {
	return filepath.Join(moduleRoot, "internal", "wasm", "testdata")
}

// NewStageDir creates a private directory for one build to write into,
// nested inside destDir. Nesting is what makes the staging usable: a
// finished artifact is published by renaming it to its final name, and a
// rename is only atomic within a single filesystem, so the staging area has
// to sit on the same one as the destination. The caller removes the
// directory when the build is done with it.
func NewStageDir(destDir string) (string, error) {
	root := filepath.Join(destDir, StageDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "build-")
}

// StageDirName is the staging area's fixed name so it can be ignored by
// version control in one entry rather than by pattern.
const StageDirName = ".stage"

// LockFixtureBuild takes the cross-process lock for one artifact's build and
// returns the release function the caller defers.
//
// The lock is per artifact rather than global, so unrelated fixtures still build
// concurrently and only builders of the same fixture take turns. Keying on the
// artifact name is keying on the shared directory it is built from: each
// artifact name resolves to exactly one source directory under every supported
// layout, so two builds contend precisely when they would write the same
// intermediate paths.
//
// The lock file sits in the staging area, which version control already ignores,
// so holding a lock cannot leave the working tree dirty.
func LockFixtureBuild(destDir, artifactName string) func() {
	return filelock.Acquire(filepath.Join(destDir, StageDirName, artifactName+".lock"))
}

// PublishArtifact moves a finished artifact from a staging path to the
// shared path tests read it from.
//
// A rename swaps the destination name in a single step, so a reader in any
// process opens either the previous artifact or the new one and never a
// half-written file — and a reader that already has the old file open keeps
// reading it intact.
//
// This covers the destination only. It says nothing about whether the bytes
// being published are the right ones, because they were produced before the
// rename; the build lock is what covers that.
func PublishArtifact(staged, dst string) error {
	return os.Rename(staged, dst)
}

// IsFresh returns true when artifactPath exists and its mtime is at or
// after the latest mtime among the source files under every sourceDir.
// Mtime-based freshness is sufficient for development cadence: a
// source edit moves the mtime forward and forces a rebuild; a fresh
// CI checkout has no artifact and forces a rebuild. The walker skips
// hidden directories so build sidecars (e.g. .git, node_modules) and
// cargo's own target/ dir don't pin freshness to unrelated changes.
//
// Callers pass every directory whose contents the artifact is built
// from — the plugin's own source plus any path-dependency source trees
// — so a change anywhere in the dependency closure forces a rebuild.
func IsFresh(artifactPath string, sourceDirs ...string) bool {
	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		return false
	}
	artifactMtime := artifactInfo.ModTime()

	var newestSrc int64
	for _, sourceDir := range sourceDirs {
		err = filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && path != sourceDir {
					return filepath.SkipDir
				}
				if d.Name() == "target" {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if t := info.ModTime().UnixNano(); t > newestSrc {
				newestSrc = t
			}
			return nil
		})
		if err != nil {
			return false
		}
	}
	return artifactMtime.UnixNano() >= newestSrc
}
