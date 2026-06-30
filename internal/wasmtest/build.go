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
//     `mutator-rust.wasm`). The fixture's `build.sh` produces the
//     artifact under `aggrega-cli/internal/wasm/testdata/`.
//   - Production plugins at `aggrega-cli/wasm-plugins/<name>/`. The
//     artifact name is `<name>.wasm` (no language suffix). The
//     plugin's own Cargo project produces the artifact under
//     `aggrega-cli/wasm-plugins/<name>/target/wasm32-unknown-unknown/release/<name_with_underscores>.wasm`.
//
// EnsureFixtureBuilt detects the layout from the artifact-name shape
// and dispatches accordingly. The cache + freshness semantics are
// identical across both layouts: artifact mtime ≥ newest source
// mtime under the source directory.
package wasmtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fixtureBuildOnce caches the per-fixture build outcome for the
// lifetime of the `go test` process. The cached value is either the
// built artifact's path (a string) or the skip reason (an error). A
// build run that succeeds or skips is not retried by subsequent tests
// in the same process.
var fixtureBuildOnce sync.Map

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
//     aggrega-cli/wasm-plugins/<name>/. Built via cargo.
//
// The cache lets multiple integration tests share a single build per
// fixture per test process, and turns the absent-toolchain case into
// one skip across the run rather than one per test.
func EnsureFixtureBuilt(t *testing.T, artifactName string) string {
	t.Helper()

	if cached, ok := fixtureBuildOnce.Load(artifactName); ok {
		switch v := cached.(type) {
		case string:
			return v
		case error:
			t.Skipf("%v", v)
		}
	}

	umbrellaRoot, err := resolveUmbrellaRoot()
	if err != nil {
		skipErr := fmt.Errorf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
		fixtureBuildOnce.Store(artifactName, skipErr)
		t.Skipf("%v", skipErr)
	}

	if socket, lang, ok := parsePDKFixtureName(artifactName); ok {
		path := buildPDKFixture(t, umbrellaRoot, artifactName, socket, lang)
		return path
	}

	if name, ok := parsePluginArtifactName(artifactName); ok {
		path := buildPluginArtifact(t, umbrellaRoot, artifactName, name)
		return path
	}

	t.Fatalf("malformed artifact name %q (want <socket>-<lang>.wasm OR <name>.wasm)", artifactName)
	return ""
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

func buildPDKFixture(t *testing.T, umbrellaRoot, artifactName, socket, lang string) string {
	testdataDir := filepath.Join(umbrellaRoot, "aggrega-cli", "internal", "wasm", "testdata")
	artifactPath := filepath.Join(testdataDir, artifactName)

	sourceDir := filepath.Join(umbrellaRoot, "aggrega-pdk", lang, "examples", socket)
	if _, err := os.Stat(sourceDir); err != nil {
		skipErr := fmt.Errorf("fixture source %s not present (the %s PDK may not have shipped yet): %v",
			sourceDir, lang, err)
		fixtureBuildOnce.Store(artifactName, skipErr)
		t.Skipf("%v", skipErr)
	}

	if isFresh(artifactPath, sourceDir) {
		fixtureBuildOnce.Store(artifactName, artifactPath)
		return artifactPath
	}

	if err := os.MkdirAll(testdataDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}

	buildScript := filepath.Join(sourceDir, "build.sh")
	cmd := exec.Command("bash", buildScript)
	cmd.Dir = sourceDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		skipErr := fmt.Errorf("fixture build failed for %s: %v\noutput:\n%s\nrun scripts/setup-workspace.sh to install the required toolchain",
			artifactName, err, string(output))
		fixtureBuildOnce.Store(artifactName, skipErr)
		t.Skipf("%v", skipErr)
	}

	if _, err := os.Stat(artifactPath); err != nil {
		skipErr := fmt.Errorf("fixture build succeeded but %s is missing afterwards (build.sh OUT_DIR may have been redirected): %v",
			artifactPath, err)
		fixtureBuildOnce.Store(artifactName, skipErr)
		t.Skipf("%v", skipErr)
	}

	fixtureBuildOnce.Store(artifactName, artifactPath)
	return artifactPath
}

// buildPluginArtifact handles the production-plugin layout at
// aggrega-cli/wasm-plugins/<name>/. Missing Cargo.toml is a hard
// fail (the plugin source is supposed to exist); missing
// wasm32-unknown-unknown target is a skip with a clear remediation;
// other cargo failures are FAIL with the cargo error output. Cargo's
// release-build artifact name uses underscores rather than dashes
// (e.g., publishing-linter → publishing_linter.wasm).
func buildPluginArtifact(t *testing.T, umbrellaRoot, artifactName, name string) string {
	pluginDir := filepath.Join(umbrellaRoot, "aggrega-cli", "wasm-plugins", name)
	manifestPath := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("plugin source missing at %s: %v (the plugin's Cargo.toml is supposed to ship; absence is a regression)", manifestPath, err)
	}

	cargoArtifact := filepath.Join(pluginDir, "target", "wasm32-unknown-unknown", "release", strings.ReplaceAll(name, "-", "_")+".wasm")

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

	if isFresh(cargoArtifact, srcDir, pdkSrc, abiSrc) {
		fixtureBuildOnce.Store(artifactName, cargoArtifact)
		return cargoArtifact
	}

	if skipErr := cargoBuildWasm(t, pluginDir, manifestPath, artifactName); skipErr != nil {
		fixtureBuildOnce.Store(artifactName, skipErr)
		t.Skipf("%v", skipErr)
	}

	if _, err := os.Stat(cargoArtifact); err != nil {
		t.Fatalf("cargo build succeeded but %s is missing afterwards: %v", cargoArtifact, err)
	}

	fixtureBuildOnce.Store(artifactName, cargoArtifact)
	return cargoArtifact
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
// bypassing the mtime freshness cache, and returns the built artifact
// path. It exists for the build-gate test: a non-compiling crate must
// FAIL the gate even if a stale prebuilt artifact would otherwise look
// fresh. The skip/fatal contract matches buildPluginArtifact — a missing
// Cargo.toml or post-build artifact is a hard fail; an absent wasm32
// target/toolchain is a clean skip; a compile error is a FAIL.
//
// name is the plugin directory name under aggrega-cli/wasm-plugins/
// (e.g. "publishing-linter").
func ForcePluginBuild(t *testing.T, name string) string {
	t.Helper()

	umbrellaRoot, err := resolveUmbrellaRoot()
	if err != nil {
		t.Skipf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
	}

	pluginDir := filepath.Join(umbrellaRoot, "aggrega-cli", "wasm-plugins", name)
	manifestPath := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("plugin source missing at %s: %v (the plugin's Cargo.toml is supposed to ship; absence is a regression)", manifestPath, err)
	}

	cargoArtifact := filepath.Join(pluginDir, "target", "wasm32-unknown-unknown", "release", strings.ReplaceAll(name, "-", "_")+".wasm")

	if skipErr := cargoBuildWasm(t, pluginDir, manifestPath, name+".wasm"); skipErr != nil {
		t.Skipf("%v", skipErr)
	}

	if _, err := os.Stat(cargoArtifact); err != nil {
		t.Fatalf("cargo build succeeded but %s is missing afterwards: %v", cargoArtifact, err)
	}
	return cargoArtifact
}

// ForcePluginTest runs the plugin crate's Rust unit/integration tests
// (`cargo test`, host target) unconditionally and FAILs the calling test
// on any non-zero exit. cargo test builds for the host target — the Rust
// tests call the rule classifiers directly rather than through wasm — so
// the wasm32 target is not required here; a cargo present on PATH implies
// the host target is present, so any failure is a genuine test failure,
// not a toolchain skip.
//
// name is the plugin directory name under aggrega-cli/wasm-plugins/.
func ForcePluginTest(t *testing.T, name string) {
	t.Helper()

	umbrellaRoot, err := resolveUmbrellaRoot()
	if err != nil {
		t.Skipf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
	}

	pluginDir := filepath.Join(umbrellaRoot, "aggrega-cli", "wasm-plugins", name)
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
// plugin's directory name under aggrega-cli/wasm-plugins/. The name
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
