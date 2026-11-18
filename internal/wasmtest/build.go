// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
//     plugin's own Cargo project for a Rust crate, or for a Go guest — a
//     package of this module — by the standard Go toolchain from the module
//     root, as the core-plugin build builds it.
//
// EnsureFixtureBuilt detects the layout from the artifact-name shape
// and dispatches accordingly. The cache + freshness semantics are
// identical across both layouts: artifact mtime ≥ newest source
// mtime under the source directory and every local tree it builds
// against — for a Rust crate, each crate it reaches by path; for a Go
// guest, every file of each package it imports from this module or
// from a local directory a replace directive names. A dependency taken
// at a released version is fixed by the lockfile or go.sum that pins
// it. Both layouts hand back a path under
// `aggrega-cli/internal/wasm/testdata/`, written only by rename, so a test
// never reads an artifact another test process is in the middle of
// writing.
//
// The module root, the shared fixture directory, the once-per-process
// build memo, the cross-process build lock, the staging directory,
// publishing by rename and the freshness walk are the wasmfixture
// package's, which also builds a guest whose source lives inside this
// module ([wasmfixture.EnsureLocalGuestBuilt]). What is here builds the
// production plugins and the plugin kit examples, and locates the
// repositories checked out beside this one ([SiblingPath],
// [SkipWithoutSiblings]): only a plugin kit example is read from one.
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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/siblingrepo"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmfixture"
)

// EnsureFixtureBuilt resolves a .wasm artifact from its source.
// Returns the artifact's absolute path after ensuring it exists and
// is no older than every file it is built from. Skips the calling test
// with a clear diagnostic when a plugin kit example is asked for and the
// aggrega-pdk checkout it is built from is not beside this repository,
// or when a build tool or target the build needs is not installed; a
// checkout that lacks the example fails the test. A production plugin reads
// nothing beside this repository. A build that fails with its toolchain
// present fails the test.
//
// Two artifact-name shapes are recognised:
//
//   - "<socket>-<lang>.wasm" → PDK example layout at
//     aggrega-pdk/<lang>/examples/<socket>/. Built via build.sh.
//   - "<name>.wasm" (single component) → production plugin layout at
//     aggrega-cli/plugins/<name>/. Built via cargo for a Rust crate, or via
//     the Go toolchain for a Go guest, stamped with coreGuestDefaultVersion.
//
// The cache lets multiple integration tests share a single build per
// fixture per test process, and turns the absent-toolchain case into
// one skip across the run rather than one per test. Concurrent callers
// requesting the same fixture are serialised, so parallel tests queue on
// one build instead of racing several builds onto one output path.
func EnsureFixtureBuilt(t *testing.T, artifactName string) string {
	t.Helper()

	path, skip := wasmfixture.BuildFixtureOnce(artifactName, func() (string, error) {
		t.Helper()

		moduleRoot := ModuleRoot(t)

		if socket, lang, ok := parsePDKFixtureName(artifactName); ok {
			siblingsDir, err := kitCheckoutDir(t, moduleRoot, lang, socket)
			if err != nil {
				return "", err
			}
			return buildPDKFixture(t, moduleRoot, siblingsDir, artifactName, socket, lang)
		}

		if name, ok := parsePluginArtifactName(artifactName); ok {
			return buildPluginArtifact(t, moduleRoot, artifactName, name)
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
// Two layouts are supported: the PDK example layout, and a Go guest of the
// production-plugin layout. A PDK example's build script must honour the
// AGGREGA_PLUGIN_VERSION stamp. A script ignoring it builds the fixture's default
// version under the stamped name, which nothing here can detect without loading
// the guest; whatever publishes or validates the result reads the version it
// actually reports, so a test built on a stamp that did not take fails there
// rather than passing on the wrong guest. A Go guest under aggrega-cli/plugins/ is
// stamped through the linker here, the way the core-plugin build stamps it. A
// Rust crate of the production-plugin layout is not supported. version must not
// be empty, since the unstamped build is EnsureFixtureBuilt's.
//
// Caching, freshness, the cross-process build lock and publishing by rename are
// the same as EnsureFixtureBuilt's, keyed on the stamped name.
func EnsureStampedFixtureBuilt(t *testing.T, artifactName, version string) string {
	t.Helper()
	socket, lang, isPDK := parsePDKFixtureName(artifactName)
	plugin, isPlugin := "", false
	if !isPDK {
		plugin, isPlugin = parsePluginArtifactName(artifactName)
	}
	if !isPDK && !isPlugin {
		t.Fatalf("stamped fixture %q is neither a PDK example artifact (<socket>-<lang>.wasm) nor a production plugin artifact (<name>.wasm)", artifactName)
	}
	if version == "" {
		t.Fatalf("stamped fixture %q needs a version; the unstamped build is EnsureFixtureBuilt's", artifactName)
	}
	stamped := strings.TrimSuffix(artifactName, ".wasm") + "@" + version + ".wasm"

	path, skip := wasmfixture.BuildFixtureOnce(stamped, func() (string, error) {
		t.Helper()
		moduleRoot := ModuleRoot(t)
		if isPDK {
			siblingsDir, err := kitCheckoutDir(t, moduleRoot, lang, socket)
			if err != nil {
				return "", err
			}
			return buildPDKFixtureAs(t, moduleRoot, siblingsDir, artifactName, stamped, socket, lang, version)
		}
		pluginDir := pluginSourceDir(moduleRoot, plugin)
		if !isGoGuest(pluginDir) {
			t.Fatalf("stamped fixture %q names no Go guest at %s; only a Go guest of the production-plugin layout takes a stamp", artifactName, pluginDir)
		}
		return buildGoPluginArtifact(t, moduleRoot, artifactName, stamped, plugin, version)
	})
	if skip != nil {
		t.Skipf("%v", skip)
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

func buildPDKFixture(t *testing.T, moduleRoot, siblingsDir, artifactName, socket, lang string) (string, error) {
	return buildPDKFixtureAs(t, moduleRoot, siblingsDir, artifactName, artifactName, socket, lang, "")
}

// buildPDKFixtureAs builds the PDK example that produces artifactName and
// publishes the result under outName. A non-empty version is handed to the
// example's build script as its version stamp; empty builds the example's own
// default.
func buildPDKFixtureAs(t *testing.T, moduleRoot, siblingsDir, artifactName, outName, socket, lang, version string) (string, error) {
	testdataDir := wasmfixture.FixtureDir(moduleRoot)
	artifactPath := filepath.Join(testdataDir, outName)

	sourceDir := filepath.Join(siblingsDir, "aggrega-pdk", lang, "examples", socket)

	// Freshness watches what the example's build reads from local
	// directories, as pdkExampleInputs lists them: the example's own source
	// and manifest, the kit it builds against by path, and the manifests and
	// lockfiles that pin the ABI codec and every other released dependency of
	// the build. Inputs that cannot be listed leave the artifact stale, so the
	// build runs and says why.
	inputs, inputsErr := pdkExampleInputs(siblingsDir, lang, sourceDir)
	fresh := func() bool { return inputsErr == nil && wasmfixture.IsFresh(artifactPath, inputs...) }
	if fresh() {
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
	release := wasmfixture.LockFixtureBuild(testdataDir, artifactName)
	defer release()

	// Freshness is re-checked now the lock is held. A process that queued
	// behind another builder finds that builder's finished artifact here and
	// skips the build, so contention costs it a wait rather than a rebuild and
	// only the first arrival pays the build cost.
	if fresh() {
		return artifactPath, nil
	}

	// A fixture build writes its artifact straight to the output path it
	// is handed, so aiming it at the shared destination would leave that
	// file readable while it is still being written. Aim it at a private
	// staging directory instead and publish the finished artifact by
	// rename.
	stageDir, err := wasmfixture.NewStageDir(testdataDir)
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
		// absent from PATH, skip with a loud, greppable marker so a gate
		// reading the test log can distinguish a toolchain skip from a real
		// pass. If the toolchain is present and the build still failed,
		// that is a genuine failure the gate must surface — fail loudly,
		// never skip, so a red build cannot read as green.
		if pdkToolchainAbsent(string(output)) {
			return "", fmt.Errorf("TOOLCHAIN-ABSENT: fixture build for %s skipped — the %s toolchain is incomplete: a required build tool is not on PATH or a required target is not installed (the build output below names it)\noutput:\n%s",
				artifactName, lang, string(output))
		}
		t.Fatalf("fixture build failed for %s (toolchain present, build broke): %v\noutput:\n%s",
			artifactName, err, string(output))
	}

	staged := filepath.Join(stageDir, artifactName)
	if _, err := os.Stat(staged); err != nil {
		return "", fmt.Errorf("fixture build for %s succeeded but produced nothing in the staging directory %s (the build script may not honour its output-directory override): %v",
			artifactName, stageDir, err)
	}

	if err := wasmfixture.PublishArtifact(staged, artifactPath); err != nil {
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

// pdkExampleInputs lists what the build of the plugin kit example at sourceDir
// reads from local directories, for lang, one of the recognised kit languages
// (rust, go, typescript) as classified by parsePDKFixtureName:
//
//   - rust: every package cargo reads from a local directory and the workspace
//     lockfile, as cargoLocalPackageDirs lists them — the example, the kit crate
//     it depends on by path, and the lockfile of the kit's workspace, which pins
//     the ABI codec;
//   - go: the example, whose go.mod and go.sum pin the ABI module, and the kit's
//     go.mod, go.sum and pkg/ tree, which its replace directive reads;
//   - typescript: the example and the kit's package.json, package-lock.json and
//     src/ tree.
func pdkExampleInputs(siblingsDir, lang, sourceDir string) ([]string, error) {
	kit := filepath.Join(siblingsDir, "aggrega-pdk", lang)
	switch lang {
	case "rust":
		return cargoLocalPackageDirs(filepath.Join(sourceDir, "Cargo.toml"))
	case "go":
		return existing(sourceDir, filepath.Join(kit, "go.mod"), filepath.Join(kit, "go.sum"), filepath.Join(kit, "pkg")), nil
	default:
		return existing(sourceDir, filepath.Join(kit, "package.json"), filepath.Join(kit, "package-lock.json"),
			filepath.Join(kit, "src")), nil
	}
}

// existing returns the paths among paths that exist, so a manifest a layout
// does not carry is not read as a changed input.
func existing(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// buildPluginArtifact handles the production-plugin layout at
// aggrega-cli/plugins/<name>/. A directory holding a Cargo.toml is a Rust crate
// built by cargo; one holding a main.go and no Cargo.toml is a Go guest, built by
// buildGoPluginArtifact at coreGuestDefaultVersion. A directory holding neither
// is a hard fail (the plugin source is supposed to exist); for a Rust crate a
// missing wasm32-unknown-unknown target is a skip with a clear remediation, and
// other cargo failures are FAIL with the cargo error output.
//
// The path returned is a published copy under the shared testdata
// directory, not cargo's own release artifact: cargo owns that path and
// rewrites it in place on every build, so a test that read it directly
// could catch it mid-write while another test process rebuilt the crate.
func buildPluginArtifact(t *testing.T, moduleRoot, artifactName, name string) (string, error) {
	pluginDir := pluginSourceDir(moduleRoot, name)
	manifestPath := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		if isGoGuest(pluginDir) {
			return buildGoPluginArtifact(t, moduleRoot, artifactName, artifactName, name, coreGuestDefaultVersion)
		}
		t.Fatalf("plugin source missing at %s: %v, and no main.go beside it (a production plugin is a Cargo crate or a Go guest; absence is a regression)", manifestPath, err)
	}

	testdataDir := wasmfixture.FixtureDir(moduleRoot)
	artifactPath := filepath.Join(testdataDir, artifactName)

	// Freshness watches every package of the crate's build that cargo reads
	// from a local directory, as cargoLocalPackageDirs lists them: the crate
	// itself, its manifest and lockfile included, and each crate it depends on
	// by path. A registry dependency, the plugin kit and the ABI codec among
	// them, is immutable at the version the lockfile pins, so a change to it
	// reaches the build only through the lockfile. A crate whose packages
	// cannot be listed is built, and the build says why it cannot be.
	// wasmfixture.IsFresh stays a generic newest-mtime walk over the dirs it is given.
	if dirs, err := cargoLocalPackageDirs(manifestPath); err == nil && wasmfixture.IsFresh(artifactPath, dirs...) {
		return artifactPath, nil
	}

	if skipErr := cargoBuildWasm(t, pluginDir, manifestPath, artifactName); skipErr != nil {
		return "", skipErr
	}

	return publishPluginArtifact(t, pluginDir, name, artifactPath), nil
}

// coreGuestDefaultVersion is the build-provenance version a Go guest of the
// production-plugin layout reports when a test names none. A Plugin entity is
// generated from a guest at the version the guest reports, and the generation
// refuses a guest reporting none, so an unstamped test build could not be laid
// down as an entity. It is the default the plugin kit's own example builds stamp,
// so an entity generated from a fixture carries the same version whichever layout
// its guest was built from.
const coreGuestDefaultVersion = "1.0.0"

// pdkVersionSymbol is the linker symbol the Go plugin kit folds into a guest's
// describe() descriptor as the version the guest reports about its own build.
const pdkVersionSymbol = "github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega.Version"

// isGoGuest reports whether pluginDir holds a Go guest: a main.go at its root.
func isGoGuest(pluginDir string) bool {
	_, err := os.Stat(filepath.Join(pluginDir, "main.go"))
	return err == nil
}

// guestInputsListing is one Go guest's guestModuleInputs, listed once.
type guestInputsListing struct {
	once   sync.Once
	inputs []string
	err    error
}

// guestInputs holds a *guestInputsListing per module root and guest, so a test
// process lists a guest's packages once however many fixtures — an unstamped
// build and its stamped ones — are built from it.
var guestInputs sync.Map

// guestModuleInputs returns what the build of the Go guest at
// aggrega-cli/plugins/<name>/ reads from local directories, as
// listGuestModuleInputs lists it, listed once per test process.
func guestModuleInputs(moduleRoot, name string) ([]string, error) {
	actual, _ := guestInputs.LoadOrStore(moduleRoot+"\x00"+name, &guestInputsListing{})
	l := actual.(*guestInputsListing)
	l.once.Do(func() { l.inputs, l.err = listGuestModuleInputs(moduleRoot, name) })
	return l.inputs, l.err
}

// guestPackagesTemplate is the go list template writing, for each package of a
// guest's build that belongs to the main module or to a module a replace
// directive points at a local directory, one line: its directory, then each Go
// file it compiles and each file it embeds, relative to that directory,
// tab-separated. A package of a module taken at a version, from the module
// cache, writes nothing: its content is fixed by the checksum go.sum records.
const guestPackagesTemplate = `{{with .Module}}{{if or .Main (and .Replace (not .Replace.Version))}}` +
	`{{$.Dir}}{{range $.GoFiles}}{{"\t"}}{{.}}{{end}}{{range $.CgoFiles}}{{"\t"}}{{.}}{{end}}` +
	`{{range $.EmbedFiles}}{{"\t"}}{{.}}{{end}}{{end}}{{end}}`

// listGuestModuleInputs lists what the build of the Go guest at
// aggrega-cli/plugins/<name>/ reads from local directories: the go.mod and go.sum
// of the module at moduleRoot, where each exists, and for every package the
// guest imports from that module or from a module a replace directive points at
// a local directory, its own package included, the package's directory — whose
// time moves when a file is added to it or removed from it — and each file the
// package compiles and embeds. The packages are those `go list -deps` reports
// for the guest under the environment its build runs in (GOWORK=off,
// GOOS=wasip1, GOARCH=wasm), as guestPackagesTemplate selects them; a package
// the guest does not import, a test file and a file another platform's build
// compiles are not among them. A module taken at a version is covered by
// go.sum, which records its checksum.
func listGuestModuleInputs(moduleRoot, name string) ([]string, error) {
	cmd := exec.Command("go", "list", "-deps", "-f", guestPackagesTemplate, "./plugins/"+name)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list the packages the guest %s imports: %v\n%s", name, err, stderr.String())
	}
	var inputs []string
	for _, file := range []string{"go.mod", "go.sum"} {
		if _, err := os.Stat(filepath.Join(moduleRoot, file)); err == nil {
			inputs = append(inputs, filepath.Join(moduleRoot, file))
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\t")
		dir := fields[0]
		if dir == "" {
			continue
		}
		inputs = append(inputs, dir)
		for _, file := range fields[1:] {
			inputs = append(inputs, filepath.Join(dir, file))
		}
	}
	return inputs, nil
}

// isFreshAgainst reports whether artifactPath exists and its time is at or after
// the time of every one of paths, each a file or a directory read as itself
// rather than walked. A path that no longer exists leaves the artifact not
// fresh, since the build it was listed for read it.
func isFreshAgainst(artifactPath string, paths []string) bool {
	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		return false
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.ModTime().After(artifactInfo.ModTime()) {
			return false
		}
	}
	return true
}

// buildGoPluginArtifact builds the Go guest at aggrega-cli/plugins/<name>/ and
// publishes it under outName in the shared testdata directory, stamped with
// version as the version it reports through describe().
//
// The guest is a package of this module rather than a module of its own, so it is
// built from the module root against the module's own go.mod — GOWORK=off, as the
// core-plugin build runs it — with that build's reproducibility flags (-trimpath,
// -buildvcs=false, an empty build id). Freshness watches the guest's own directory
// and every package it imports from a local directory, as guestModuleInputs lists
// them: a guest keeps most of what it does in packages beside it under
// plugins/internal/, so a change there rebuilds every guest importing the changed
// package and no other. The Go plugin kit and ABI are read wherever go.mod
// resolves them: at a released version through go.sum, or from a local directory
// a replace directive names, whose packages are then listed like this module's.
//
// There is no toolchain-absent skip, for the reason [wasmfixture.EnsureLocalGuestBuilt] gives:
// the standard Go toolchain targeting wasip1 is present whenever a test runs at
// all, so any build failure is a hard test failure.
func buildGoPluginArtifact(t *testing.T, moduleRoot, artifactName, outName, name, version string) (string, error) {
	t.Helper()
	pluginDir := pluginSourceDir(moduleRoot, name)
	testdataDir := wasmfixture.FixtureDir(moduleRoot)
	artifactPath := filepath.Join(testdataDir, outName)

	fresh := func() bool {
		if !wasmfixture.IsFresh(artifactPath, pluginDir) {
			return false
		}
		inputs, err := guestModuleInputs(moduleRoot, name)
		// A guest whose packages cannot be listed is built, and the build says why
		// it cannot be.
		return err == nil && isFreshAgainst(artifactPath, inputs)
	}
	if fresh() {
		return artifactPath, nil
	}
	if err := os.MkdirAll(testdataDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}

	release := wasmfixture.LockFixtureBuild(testdataDir, artifactName)
	defer release()

	if fresh() {
		return artifactPath, nil
	}

	stageDir, err := wasmfixture.NewStageDir(testdataDir)
	if err != nil {
		t.Fatalf("stage Go guest build for %s: %v", outName, err)
	}
	defer os.RemoveAll(stageDir)

	staged := filepath.Join(stageDir, outName)
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-buildmode=c-shared",
		"-ldflags", "-X "+pdkVersionSymbol+"="+version+" -buildid=",
		"-o", staged, "./plugins/"+name)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Go guest build failed for %s at %s: %v\noutput:\n%s", outName, pluginDir, err, string(output))
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("Go guest build for %s reported success but produced nothing at %s: %v", outName, staged, err)
	}
	if err := wasmfixture.PublishArtifact(staged, artifactPath); err != nil {
		t.Fatalf("publish Go guest %s: %v", outName, err)
	}
	return artifactPath, nil
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

	moduleRoot := ModuleRoot(t)
	pluginDir := pluginSourceDir(moduleRoot, name)
	manifestPath := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("plugin source missing at %s: %v (the plugin's Cargo.toml is supposed to ship; absence is a regression)", manifestPath, err)
	}

	if skipErr := cargoBuildWasm(t, pluginDir, manifestPath, name+".wasm"); skipErr != nil {
		t.Skipf("%v", skipErr)
	}

	artifactPath := filepath.Join(wasmfixture.FixtureDir(moduleRoot), name+".wasm")
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

	pluginDir := pluginSourceDir(ModuleRoot(t), name)
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

// ModuleRoot is [wasmfixture.ModuleRoot]: the root directory of this module,
// which every in-repository fixture path is joined onto.
func ModuleRoot(t testing.TB) string {
	t.Helper()
	return wasmfixture.ModuleRoot(t)
}

// pluginSourceDir is the source directory of the production plugin name.
func pluginSourceDir(moduleRoot, name string) string {
	return filepath.Join(moduleRoot, "plugins", name)
}

// kitCheckoutDir returns the directory holding the aggrega-pdk checkout beside
// this repository, which the plugin kit example for socket in lang is built
// from. When the checkout is absent it returns the skip reason naming it; when
// its path is there and cannot be read, or the checkout lacks the example, it
// fails t, since neither is a partial checkout.
func kitCheckoutDir(t *testing.T, moduleRoot, lang, socket string) (string, error) {
	t.Helper()
	kit, err := siblingrepo.Dir(moduleRoot, "aggrega-pdk")
	if siblingrepo.IsAbsent(err) {
		return "", fmt.Errorf("%s", siblingSkipMessage("aggrega-pdk", filepath.Join(lang, "examples", socket), err))
	}
	if err != nil {
		t.Fatalf("aggrega-pdk is beside this repository at %s but cannot be read as a checkout: %v", kit, err)
	}
	example := filepath.Join(kit, lang, "examples", socket)
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("plugin kit example %s is not in the aggrega-pdk checkout: %v", example, err)
	}
	return filepath.Dir(kit), nil
}

// cargoLocalPackageDirs lists the directory of every package the crate whose
// manifest is manifestPath builds from a local directory — the crate itself and
// each crate reached by a path dependency or a local patch — as cargo metadata
// reports them for the versions the crate's lockfile pins, and that lockfile,
// which sits at the root of the crate's workspace.
func cargoLocalPackageDirs(manifestPath string) ([]string, error) {
	cmd := exec.Command("cargo", "metadata", "--format-version", "1", "--locked", "--manifest-path", manifestPath)
	cmd.Dir = filepath.Dir(manifestPath)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var meta struct {
		Packages []struct {
			Source       *string `json:"source"`
			ManifestPath string  `json:"manifest_path"`
		} `json:"packages"`
		WorkspaceRoot string `json:"workspace_root"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		return nil, err
	}
	var dirs []string
	for _, p := range meta.Packages {
		if p.Source == nil {
			dirs = append(dirs, filepath.Dir(p.ManifestPath))
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("cargo metadata for %s lists no local package", manifestPath)
	}
	lock := filepath.Join(meta.WorkspaceRoot, "Cargo.lock")
	if _, err := os.Stat(lock); err != nil {
		return nil, fmt.Errorf("the lockfile of %s's workspace: %w", manifestPath, err)
	}
	return append(dirs, lock), nil
}

// publishArtifactCopy publishes a copy of src, for a source that has to stay
// where it is because another tool owns it. The copy lands in the staging
// area first and reaches dst through [wasmfixture.PublishArtifact], so the destination is
// still only ever written by rename.
func publishArtifactCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	stageDir, err := wasmfixture.NewStageDir(filepath.Dir(dst))
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

	return wasmfixture.PublishArtifact(staged, dst)
}
