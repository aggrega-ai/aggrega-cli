// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package wasmtest provides shared helpers for tests that consume
// .wasm artifacts built on demand from per-language plugin sources.
// The package is internal to aggrega-cli and is intended only for
// use from `*_test.go` files; the helpers depend on the testing
// package.
//
// Two source layouts are supported:
//
//   - PDK fixtures at `aggrega-pdk/<lang>/examples/<socket>/`. The
//     artifact name follows `<socket>-<lang>.wasm` (e.g.,
//     `mutator-rust.wasm`), built by the fixture's own `build.sh`.
//   - Production plugins at `aggrega-cli/plugins/<name>/`. The
//     artifact name is `<name>.wasm` (no language suffix), built by the
//     plugin's own Cargo project.
//
// EnsureFixtureBuilt detects the layout from the artifact-name shape
// and dispatches accordingly. The cache + freshness semantics are
// identical across both layouts: artifact mtime ≥ newest source
// mtime under the source directory. Both layouts hand back a path under
// `aggrega-cli/internal/wasm/testdata/`, written only by this package and
// only by rename, so a test never reads an artifact another test process
// is in the middle of writing.
//
// Concurrency is guarded at two scopes, because they answer different
// questions. Publishing by rename keeps a reader from seeing a half-written
// destination file. A lock held for the whole duration of a build keeps two
// builders from interleaving inside the shared source directory, where the
// build scripts install dependencies and assemble intermediate output at
// fixed paths; content produced wrongly there is already wrong by the time
// any rename happens, so the rename cannot speak to it.
package wasmtest

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

// buildFixtureOnce returns the memoised outcome for artifactName, calling
// build at most once per artifact across concurrent callers. Callers that
// arrive while a build is in flight queue on the artifact's lock and then
// observe the stored result rather than repeating the build. A skip reason
// is cached and shared, so it is derived once per process rather than once
// per caller.
//
// A build that ends the calling goroutine (t.Fatalf, t.Skipf) stores
// nothing: a hard failure is the calling test's, not a verdict to serve to
// every later caller.
func buildFixtureOnce(artifactName string, build func() (string, error)) (string, error) {
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

// EnsureFixtureBuilt resolves a .wasm artifact from its source.
// Returns the artifact's absolute path after ensuring it exists and
// is no older than every file in the source directory. Skips the
// calling test with a clear diagnostic when the source is not present
// (the per-language PDK or the plugin source has not been authored
// yet) or when the build fails (the required toolchain is not on
// PATH; the workspace setup script installs them).
//
// Two artifact-name shapes are recognised:
//
//   - "<socket>-<lang>.wasm" → PDK example layout at
//     aggrega-pdk/<lang>/examples/<socket>/. Built via build.sh.
//   - "<name>.wasm" (single component) → production plugin layout at
//     aggrega-cli/plugins/<name>/. Built via cargo.
//
// The cache lets multiple integration tests share a single build per
// fixture per test process, and turns the absent-toolchain case into
// one skip across the run rather than one per test. Concurrent callers
// requesting the same fixture are serialised, so parallel tests queue on
// one build instead of racing several builds onto one output path.
func EnsureFixtureBuilt(t *testing.T, artifactName string) string {
	t.Helper()

	path, skip := buildFixtureOnce(artifactName, func() (string, error) {
		t.Helper()

		umbrellaRoot, err := resolveUmbrellaRoot()
		if err != nil {
			return "", fmt.Errorf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
		}

		if socket, lang, ok := parsePDKFixtureName(artifactName); ok {
			return buildPDKFixture(t, umbrellaRoot, artifactName, socket, lang)
		}

		if name, ok := parsePluginArtifactName(artifactName); ok {
			return buildPluginArtifact(t, umbrellaRoot, artifactName, name)
		}

		t.Fatalf("malformed artifact name %q (want <socket>-<lang>.wasm OR <name>.wasm)", artifactName)
		return "", nil
	})
	if skip != nil {
		t.Skipf("%v", skip)
	}
	return path
}

// EnsureStampedFixtureBuilt is [EnsureFixtureBuilt] for a PDK fixture built with
// a build-provenance version of the caller's choosing: the version the guest
// then reports about its own build through describe(). It returns the absolute
// path of an artifact distinct per version, so two stamps of one fixture never
// replace each other, and never replace the unstamped artifact every other test
// reads.
//
// It exists for a test whose subject is the version a guest reports — the
// publish door holds that version against the entity's and judges its form — and
// it is the one way a test obtains such a guest, so each such test does not grow
// a build of its own.
//
// Only the PDK example layout is supported, and the example's build script must
// honour the AGGREGA_PLUGIN_VERSION stamp. A script ignoring it builds the
// fixture's default version under the stamped name, which nothing here can
// detect without loading the guest; whatever publishes or validates the result
// reads the version it actually reports, so a test built on a stamp that did not
// take fails there rather than passing on the wrong guest. version must not be
// empty, since the unstamped build is EnsureFixtureBuilt's.
//
// Caching, freshness, the cross-process build lock and publishing by rename are
// the same as EnsureFixtureBuilt's, keyed on the stamped name.
func EnsureStampedFixtureBuilt(t *testing.T, artifactName, version string) string {
	t.Helper()
	socket, lang, ok := parsePDKFixtureName(artifactName)
	if !ok {
		t.Fatalf("stamped fixture %q is not a PDK example artifact (want <socket>-<lang>.wasm)", artifactName)
	}
	if version == "" {
		t.Fatalf("stamped fixture %q needs a version; the unstamped build is EnsureFixtureBuilt's", artifactName)
	}
	stamped := strings.TrimSuffix(artifactName, ".wasm") + "@" + version + ".wasm"

	path, skip := buildFixtureOnce(stamped, func() (string, error) {
		t.Helper()
		umbrellaRoot, err := resolveUmbrellaRoot()
		if err != nil {
			return "", fmt.Errorf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
		}
		return buildPDKFixtureAs(t, umbrellaRoot, artifactName, stamped, socket, lang, version)
	})
	if skip != nil {
		t.Skipf("%v", skip)
	}
	return path
}

// EnsureLocalGuestBuilt builds a self-contained Go guest whose source lives
// inside this module and returns the built artifact's absolute path.
//
// srcDir is an absolute path to a directory holding the guest's main.go and its
// own go.mod. It is addressed directly rather than derived from an artifact
// name, so this reaches a guest the two name-parsed layouts above cannot: one
// authored here rather than in a per-language plugin kit. artifactName is the
// name the built guest is published under in the shared fixture directory, and
// it must not collide with a name either of those layouts owns.
//
// It exists for a guest that is DELIBERATELY non-conforming — one built to
// violate the plugin contract so a host refusal can be driven and its
// diagnostic read. Such a guest belongs beside the tests that drive it rather
// than in a kit of reference examples, and it hand-writes its own wire bytes,
// because a generated codec exists precisely to make the mistakes it has to make
// unrepresentable.
//
// There is no toolchain-absent skip and there must not be one. The build is the
// standard Go toolchain targeting wasip1, which ships with every Go
// distribution, so the toolchain is present whenever this runs at all — a caller
// is executing under `go test`. A skip here would let a proof that never ran
// read as a proof that passed, which for a fixture whose whole purpose is to
// make an unprovable registration provable is the one failure mode worth
// refusing outright. Any build failure is therefore a hard test failure.
//
// The cache, lock and publish-by-rename discipline is the same as the layouts
// above: at most one builder per artifact across processes, and the destination
// only ever written by rename.
func EnsureLocalGuestBuilt(t *testing.T, srcDir, artifactName string) string {
	t.Helper()

	path, skip := buildFixtureOnce(artifactName, func() (string, error) {
		t.Helper()

		umbrellaRoot, err := resolveUmbrellaRoot()
		if err != nil {
			t.Fatalf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
		}
		if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
			t.Fatalf("local guest source missing at %s: %v (the fixture's main.go is supposed to ship; absence is a regression)", srcDir, err)
		}

		testdataDir := filepath.Join(umbrellaRoot, "aggrega-cli", "internal", "wasm", "testdata")
		artifactPath := filepath.Join(testdataDir, artifactName)
		if isFresh(artifactPath, srcDir) {
			return artifactPath, nil
		}
		if err := os.MkdirAll(testdataDir, 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}

		release := lockFixtureBuild(testdataDir, artifactName)
		defer release()

		if isFresh(artifactPath, srcDir) {
			return artifactPath, nil
		}

		stageDir, err := newStageDir(testdataDir)
		if err != nil {
			t.Fatalf("stage local guest build for %s: %v", artifactName, err)
		}
		defer os.RemoveAll(stageDir)

		staged := filepath.Join(stageDir, artifactName)
		// GOWORK=off is what makes the guest's own go.mod authoritative: the
		// umbrella workspace does not enrol these single-file guest modules, and
		// resolving them through it would either fail or bind them to whatever
		// the workspace pins rather than to what the guest declares.
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
		if err := publishArtifact(staged, artifactPath); err != nil {
			t.Fatalf("publish local guest %s: %v", artifactName, err)
		}
		return artifactPath, nil
	})
	if skip != nil {
		t.Fatalf("%v", skip)
	}
	return path
}

// EnsureFixtureBytes builds the fixture (via EnsureFixtureBuilt) and returns its
// .wasm bytes. It is the byte-oriented companion for callers that load a plugin
// from in-memory content (the publishing-linter dispatcher takes resolved bytes,
// not a path), so tests feed it the same fixture without each one re-reading the
// file. Skips the test under the same conditions EnsureFixtureBuilt does.
func EnsureFixtureBytes(t *testing.T, artifactName string) []byte {
	t.Helper()
	path := EnsureFixtureBuilt(t, artifactName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read built fixture %s: %v", path, err)
	}
	return b
}

func buildPDKFixture(t *testing.T, umbrellaRoot, artifactName, socket, lang string) (string, error) {
	return buildPDKFixtureAs(t, umbrellaRoot, artifactName, artifactName, socket, lang, "")
}

// buildPDKFixtureAs builds the PDK example that produces artifactName and
// publishes the result under outName. A non-empty version is handed to the
// example's build script as its version stamp; empty builds the example's own
// default.
func buildPDKFixtureAs(t *testing.T, umbrellaRoot, artifactName, outName, socket, lang, version string) (string, error) {
	testdataDir := filepath.Join(umbrellaRoot, "aggrega-cli", "internal", "wasm", "testdata")
	artifactPath := filepath.Join(testdataDir, outName)

	sourceDir := filepath.Join(umbrellaRoot, "aggrega-pdk", lang, "examples", socket)
	if _, err := os.Stat(sourceDir); err != nil {
		return "", fmt.Errorf("fixture source %s not present (the %s PDK may not have shipped yet): %v",
			sourceDir, lang, err)
	}

	// Freshness watches the example's own source AND the per-language
	// PDK and ABI source trees the example builds against. A change to
	// a dependency moves the artifact stale even though the example's
	// own source is untouched, so a regenerated PDK or ABI forces a
	// rebuild rather than reusing a now-incompatible prebuilt fixture.
	pdkSrc, abiSrc := pdkDependencySources(umbrellaRoot, lang)
	if isFresh(artifactPath, sourceDir, pdkSrc, abiSrc) {
		return artifactPath, nil
	}

	if err := os.MkdirAll(testdataDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}

	// A fixture build is serialised across processes for its WHOLE duration,
	// not just around publishing its result. The build scripts install
	// dependencies and assemble intermediate output inside the example
	// directory itself, at fixed paths that the output-directory override does
	// not redirect. So two processes building the same fixture share those
	// paths however different their destinations, and one can read an
	// intermediate file while the other is rewriting it. That read does not
	// fail: the tools downstream accept a truncated or empty intermediate and
	// emit a complete, well-formed artifact with the wrong contents, which is
	// then loaded and believed. Serialising the whole build is what removes
	// that window; publishing by rename, below, is a narrower guarantee that
	// cannot reach content produced before the rename.
	//
	// Every test package is its own process and no in-process lock spans them,
	// so this has to be a lock on the filesystem.
	release := lockFixtureBuild(testdataDir, artifactName)
	defer release()

	// Freshness is re-checked now the lock is held. A process that queued
	// behind another builder finds that builder's finished artifact here and
	// skips the build, so contention costs it a wait rather than a rebuild and
	// only the first arrival pays the build cost.
	if isFresh(artifactPath, sourceDir, pdkSrc, abiSrc) {
		return artifactPath, nil
	}

	// A fixture build writes its artifact straight to the output path it
	// is handed, so aiming it at the shared destination would leave that
	// file readable while it is still being written. Aim it at a private
	// staging directory instead and publish the finished artifact by
	// rename.
	stageDir, err := newStageDir(testdataDir)
	if err != nil {
		t.Fatalf("stage fixture build for %s: %v", artifactName, err)
	}
	defer os.RemoveAll(stageDir)

	buildScript := filepath.Join(sourceDir, "build.sh")
	cmd := exec.Command("bash", buildScript)
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "AGGREGA_PDK_WASM_OUT="+stageDir)
	if version != "" {
		cmd.Env = append(cmd.Env, "AGGREGA_PLUGIN_VERSION="+version)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		// A non-zero exit splits two ways. If a required build tool is
		// absent from PATH, skip with a loud, greppable marker so an
		// arc-end gate can distinguish a toolchain skip from a real
		// pass. If the toolchain is present and the build still failed,
		// that is a genuine failure the gate must surface — fail loudly,
		// never skip, so a red build cannot read as green.
		if pdkToolchainAbsent(string(output)) {
			return "", fmt.Errorf("TOOLCHAIN-ABSENT: fixture build for %s skipped — a required build tool is not on PATH; run scripts/setup-workspace.sh to install the toolchain\noutput:\n%s",
				artifactName, string(output))
		}
		t.Fatalf("fixture build failed for %s (toolchain present, build broke): %v\noutput:\n%s",
			artifactName, err, string(output))
	}

	staged := filepath.Join(stageDir, artifactName)
	if _, err := os.Stat(staged); err != nil {
		return "", fmt.Errorf("fixture build for %s succeeded but produced nothing in the staging directory %s (the build script may not honour its output-directory override): %v",
			artifactName, stageDir, err)
	}

	if err := publishArtifact(staged, artifactPath); err != nil {
		t.Fatalf("publish fixture %s: %v", artifactName, err)
	}

	return artifactPath, nil
}

// pdkToolchainAbsent reports whether a failed PDK fixture build failed
// because a required build tool is absent from PATH rather than because
// the plugin source does not build. Detection reads the build.sh output
// rather than probing PATH from the test process: the fixture build.sh
// scripts augment PATH with user-local toolchain directories before
// running, so a direct PATH probe here would report tools absent that
// the build would actually find. The scripts fail fast with a
// recognizable diagnostic when a tool is missing — the TypeScript
// build.sh prints "not on PATH", a shell that cannot locate go or cargo
// prints "command not found", and an absent cross-compile target prints
// "not installed". Any other non-zero exit is a genuine build failure
// the gate must surface loudly.
func pdkToolchainAbsent(output string) bool {
	for _, sig := range []string{"not on PATH", "command not found", "not installed"} {
		if strings.Contains(output, sig) {
			return true
		}
	}
	return false
}

// pdkDependencySources returns the PDK and ABI source directories a
// PDK example builds against, for the given language. The source-holding
// subdirectory differs per language: rust and typescript keep sources
// under src/, go under pkg/. lang is one of the recognised PDK languages
// (rust, go, typescript) as classified by parsePDKFixtureName.
func pdkDependencySources(umbrellaRoot, lang string) (pdkSrc, abiSrc string) {
	switch lang {
	case "go":
		pdkSrc = filepath.Join(umbrellaRoot, "aggrega-pdk", "go", "pkg")
		abiSrc = filepath.Join(umbrellaRoot, "aggrega-abi", "go", "pkg")
	case "rust", "typescript":
		pdkSrc = filepath.Join(umbrellaRoot, "aggrega-pdk", lang, "src")
		abiSrc = filepath.Join(umbrellaRoot, "aggrega-abi", lang, "src")
	}
	return pdkSrc, abiSrc
}

// buildPluginArtifact handles the production-plugin layout at
// aggrega-cli/plugins/<name>/. Missing Cargo.toml is a hard
// fail (the plugin source is supposed to exist); missing
// wasm32-unknown-unknown target is a skip with a clear remediation;
// other cargo failures are FAIL with the cargo error output.
//
// The path returned is a published copy under the shared testdata
// directory, not cargo's own release artifact: cargo owns that path and
// rewrites it in place on every build, so a test that read it directly
// could catch it mid-write while another test process rebuilt the crate.
func buildPluginArtifact(t *testing.T, umbrellaRoot, artifactName, name string) (string, error) {
	pluginDir := filepath.Join(umbrellaRoot, "aggrega-cli", "plugins", name)
	manifestPath := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("plugin source missing at %s: %v (the plugin's Cargo.toml is supposed to ship; absence is a regression)", manifestPath, err)
	}

	testdataDir := filepath.Join(umbrellaRoot, "aggrega-cli", "internal", "wasm", "testdata")
	artifactPath := filepath.Join(testdataDir, artifactName)

	// Freshness watches the plugin's own src/ AND the path-dependency
	// source trees the plugin builds against (the Rust PDK and the
	// generated ABI). A change to a dependency moves the artifact stale
	// even though the plugin's own src/ is untouched, so a regenerated
	// ABI forces a rebuild rather than reusing a now-incompatible
	// prebuilt artifact. buildPluginArtifact knows the plugin's deps;
	// isFresh stays a generic newest-mtime walk over the dirs it is given.
	srcDir := filepath.Join(pluginDir, "src")
	pdkSrc := filepath.Join(umbrellaRoot, "aggrega-pdk", "rust", "src")
	abiSrc := filepath.Join(umbrellaRoot, "aggrega-abi", "rust", "src")

	if isFresh(artifactPath, srcDir, pdkSrc, abiSrc) {
		return artifactPath, nil
	}

	if skipErr := cargoBuildWasm(t, pluginDir, manifestPath, artifactName); skipErr != nil {
		return "", skipErr
	}

	return publishPluginArtifact(t, pluginDir, name, artifactPath), nil
}

// publishPluginArtifact copies cargo's release artifact for the named
// plugin to artifactPath and returns artifactPath. Cargo's own output path
// is left untouched so cargo's bookkeeping stays intact.
func publishPluginArtifact(t *testing.T, pluginDir, name, artifactPath string) string {
	t.Helper()

	// Cargo's release-build artifact name uses underscores rather than
	// dashes (e.g., publishing-linter → publishing_linter.wasm).
	cargoArtifact := filepath.Join(pluginDir, "target", "wasm32-unknown-unknown", "release", strings.ReplaceAll(name, "-", "_")+".wasm")
	if _, err := os.Stat(cargoArtifact); err != nil {
		t.Fatalf("cargo build succeeded but %s is missing afterwards: %v", cargoArtifact, err)
	}

	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}

	if err := publishArtifactCopy(cargoArtifact, artifactPath); err != nil {
		t.Fatalf("publish plugin artifact %s: %v", name, err)
	}

	return artifactPath
}

// cargoBuildWasm runs `cargo build --release --target
// wasm32-unknown-unknown` for the plugin at pluginDir. It returns a
// non-nil error ONLY for the skippable case — the wasm32 target or
// toolchain is absent — which the caller turns into a clean test skip.
// A genuine compile failure is a hard t.Fatalf here: the source does
// not compile, which the gate must surface as a FAIL, never a skip.
func cargoBuildWasm(t *testing.T, pluginDir, manifestPath, artifactName string) error {
	t.Helper()
	cmd := exec.Command("cargo", "build", "--release", "--target", "wasm32-unknown-unknown", "--manifest-path", manifestPath)
	cmd.Dir = pluginDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(output)
		if strings.Contains(outStr, "wasm32-unknown-unknown") && strings.Contains(outStr, "not installed") {
			return fmt.Errorf("wasm32-unknown-unknown target not installed; run `rustup target add wasm32-unknown-unknown`\ncargo output:\n%s", outStr)
		}
		t.Fatalf("cargo build failed for %s: %v\noutput:\n%s", artifactName, err, outStr)
	}
	return nil
}

// ForcePluginBuild compiles a production plugin's crate unconditionally,
// bypassing the mtime freshness cache, and returns the published artifact
// path. It exists for the build-gate test: a non-compiling crate must
// FAIL the gate even if a stale prebuilt artifact would otherwise look
// fresh. The skip/fatal contract matches buildPluginArtifact — a missing
// Cargo.toml or post-build artifact is a hard fail; an absent wasm32
// target/toolchain is a clean skip; a compile error is a FAIL.
//
// name is the plugin directory name under aggrega-cli/plugins/
// (e.g. "publishing-linter").
func ForcePluginBuild(t *testing.T, name string) string {
	t.Helper()

	umbrellaRoot, err := resolveUmbrellaRoot()
	if err != nil {
		t.Skipf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
	}

	pluginDir := filepath.Join(umbrellaRoot, "aggrega-cli", "plugins", name)
	manifestPath := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("plugin source missing at %s: %v (the plugin's Cargo.toml is supposed to ship; absence is a regression)", manifestPath, err)
	}

	if skipErr := cargoBuildWasm(t, pluginDir, manifestPath, name+".wasm"); skipErr != nil {
		t.Skipf("%v", skipErr)
	}

	artifactPath := filepath.Join(umbrellaRoot, "aggrega-cli", "internal", "wasm", "testdata", name+".wasm")
	return publishPluginArtifact(t, pluginDir, name, artifactPath)
}

// ForcePluginTest runs the plugin crate's Rust unit/integration tests
// (`cargo test`, host target) unconditionally and FAILs the calling test
// on any non-zero exit. cargo test builds for the host target — the Rust
// tests call the rule classifiers directly rather than through wasm — so
// the wasm32 target is not required here; a cargo present on PATH implies
// the host target is present, so any failure is a genuine test failure,
// not a toolchain skip.
//
// name is the plugin directory name under aggrega-cli/plugins/.
func ForcePluginTest(t *testing.T, name string) {
	t.Helper()

	umbrellaRoot, err := resolveUmbrellaRoot()
	if err != nil {
		t.Skipf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
	}

	pluginDir := filepath.Join(umbrellaRoot, "aggrega-cli", "plugins", name)
	manifestPath := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("plugin source missing at %s: %v (the plugin's Cargo.toml is supposed to ship; absence is a regression)", manifestPath, err)
	}

	cmd := exec.Command("cargo", "test", "--manifest-path", manifestPath)
	cmd.Dir = pluginDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cargo test failed for %s: %v\noutput:\n%s", name, err, string(output))
	}
}

// parsePDKFixtureName matches "<socket>-<lang>.wasm" with at least
// one "-" in the stem. Returns ok=false when the stem has no "-".
//
// Soft contract: the lang suffix is matched against a fixed allowlist
// (rust, go, typescript) — the three PDK languages aggrega-pdk/ ships
// per the project's PDK-floor framing. A production-plugin artifact
// name that happens to end in "-rust"/"-go"/"-typescript" would be
// misclassified as a PDK fixture; the cargo-build path expected by
// the production-plugin layout would never fire. The allowlist is
// kept fixed because the PDK-floor contract is fixed; new languages
// require a coordinated PDK addition. If a future production plugin
// needs a name that collides with the allowlist, rename the plugin.
func parsePDKFixtureName(name string) (socket, lang string, ok bool) {
	stem := strings.TrimSuffix(name, ".wasm")
	if stem == name {
		return "", "", false
	}
	idx := strings.LastIndex(stem, "-")
	if idx <= 0 || idx == len(stem)-1 {
		return "", "", false
	}
	// PDK layout requires a recognised language suffix; everything
	// else falls through to the production-plugin layout. The known
	// PDK languages mirror the aggrega-pdk/ subdirectories.
	candidate := stem[idx+1:]
	switch candidate {
	case "rust", "go", "typescript":
		return stem[:idx], candidate, true
	default:
		return "", "", false
	}
}

// parsePluginArtifactName matches "<name>.wasm" where <name> is the
// plugin's directory name under aggrega-cli/plugins/. The name
// may contain dashes (e.g., "publishing-linter"); the .wasm suffix
// is stripped and the remainder is returned verbatim.
func parsePluginArtifactName(name string) (pluginName string, ok bool) {
	stem := strings.TrimSuffix(name, ".wasm")
	if stem == name || stem == "" {
		return "", false
	}
	return stem, true
}

// resolveUmbrellaRoot walks up from the test's working directory until
// it finds a .gitmodules file — the umbrella root. Returns an error
// when no .gitmodules is found above the working directory (the test
// is running outside an umbrella checkout).
func resolveUmbrellaRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .gitmodules found above %s", cwd)
		}
		dir = parent
	}
}

// newStageDir creates a private directory for one build to write into,
// nested inside destDir. Nesting is what makes the staging usable: a
// finished artifact is published by renaming it to its final name, and a
// rename is only atomic within a single filesystem, so the staging area has
// to sit on the same one as the destination. The caller removes the
// directory when the build is done with it.
func newStageDir(destDir string) (string, error) {
	root := filepath.Join(destDir, stageDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "build-")
}

// stageDirName is the staging area's fixed name so it can be ignored by
// version control in one entry rather than by pattern.
const stageDirName = ".stage"

// lockFixtureBuild takes the cross-process lock for one artifact's build and
// returns the release function the caller defers.
//
// The lock is per artifact rather than global, so unrelated fixtures still build
// concurrently and only builders of the same fixture take turns. Keying on the
// artifact name is keying on the shared directory it is built from: each
// artifact name resolves to exactly one source directory under both supported
// layouts, so two builds contend precisely when they would write the same
// intermediate paths.
//
// The lock file sits in the staging area, which version control already ignores,
// so holding a lock cannot leave the working tree dirty.
func lockFixtureBuild(destDir, artifactName string) func() {
	return filelock.Acquire(filepath.Join(destDir, stageDirName, artifactName+".lock"))
}

// publishArtifact moves a finished artifact from a staging path to the
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
func publishArtifact(staged, dst string) error {
	return os.Rename(staged, dst)
}

// publishArtifactCopy publishes a copy of src, for a source that has to stay
// where it is because another tool owns it. The copy lands in the staging
// area first and reaches dst through publishArtifact, so the destination is
// still only ever written by rename.
func publishArtifactCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	stageDir, err := newStageDir(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)

	staged := filepath.Join(stageDir, filepath.Base(dst))
	out, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	return publishArtifact(staged, dst)
}

// isFresh returns true when artifactPath exists and its mtime is at or
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
func isFresh(artifactPath string, sourceDirs ...string) bool {
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
