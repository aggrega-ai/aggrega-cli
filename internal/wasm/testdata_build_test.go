// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

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

// ensureFixtureBuilt resolves a fixture .wasm artifact from its source
// in the sibling aggrega-pdk submodule. Returns the artifact's
// absolute path after ensuring it exists and is no older than every
// file in the fixture source directory. Skips the calling test with a
// clear diagnostic when the source is not present (the per-language
// PDK is not yet shipped) or when the fixture build fails (the
// required toolchain is not on PATH; the workspace setup script
// installs them).
//
// The cache lets multiple integration tests share a single build per
// fixture per test process, and turns the absent-toolchain case into
// one skip across the run rather than one per test.
func ensureFixtureBuilt(t *testing.T, artifactName string) string {
	t.Helper()

	if cached, ok := fixtureBuildOnce.Load(artifactName); ok {
		switch v := cached.(type) {
		case string:
			return v
		case error:
			t.Skipf("%v", v)
		}
	}

	socket, lang, ok := parseFixtureName(artifactName)
	if !ok {
		t.Fatalf("malformed fixture name %q (want <socket>-<lang>.wasm)", artifactName)
	}

	umbrellaRoot, err := resolveUmbrellaRoot()
	if err != nil {
		skipErr := fmt.Errorf("cannot resolve umbrella root: %v; run scripts/setup-workspace.sh from the umbrella checkout", err)
		fixtureBuildOnce.Store(artifactName, skipErr)
		t.Skipf("%v", skipErr)
	}

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

// parseFixtureName splits "<socket>-<lang>.wasm" into its socket and
// lang components. Returns ok=false for any other shape; the caller
// treats that as a programmer error and Fatals.
func parseFixtureName(name string) (socket, lang string, ok bool) {
	stem := strings.TrimSuffix(name, ".wasm")
	if stem == name {
		return "", "", false
	}
	idx := strings.LastIndex(stem, "-")
	if idx <= 0 || idx == len(stem)-1 {
		return "", "", false
	}
	return stem[:idx], stem[idx+1:], true
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
// after the latest mtime among the source files under sourceDir.
// Mtime-based freshness is sufficient for development cadence: a
// fixture source edit moves the mtime forward and forces a rebuild; a
// fresh CI checkout has no artifact and forces a rebuild. The walker
// skips hidden directories so build sidecars (e.g. .git, node_modules)
// don't pin freshness to unrelated changes.
func isFresh(artifactPath, sourceDir string) bool {
	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		return false
	}
	artifactMtime := artifactInfo.ModTime()

	var newestSrc int64
	err = filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != sourceDir {
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
	return artifactMtime.UnixNano() >= newestSrc
}
