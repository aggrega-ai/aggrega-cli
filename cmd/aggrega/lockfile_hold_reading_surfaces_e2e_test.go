// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The commands that read a Release without building it hold its lockfile to what
// the Release resolves to, exactly as a build does: over a lockfile that no longer
// matches, each refuses with the halt the build prints and writes nothing, and over
// a lockfile that matches each runs as it always has.
//
// Every case runs the real command tree from inside a copy of the shared workspace
// whose one Release reaches something on every axis a lockfile pins, pulled once.

// readingSurfaces are the invocations that read the Release the drift-axes fixture
// declares, each naming the Release the way a person at the workspace root types it.
var readingSurfaces = []struct {
	name string
	args []string
}{
	{"inspect", []string{"inspect", driftAxesRelease}},
	{"inspect --resolved", []string{"inspect", driftAxesRelease, "--resolved"}},
	{"inspect --ir-graph", []string{"inspect", driftAxesRelease, "--ir-graph"}},
	{"inspect --closure", []string{"inspect", driftAxesRelease, "--closure"}},
	{"validate --release", []string{"validate", "--release", driftAxesRelease}},
}

// runReadingSurface runs one invocation through the real command tree with the
// process stderr captured, and returns what the command wrote to its output stream
// and its error.
func runReadingSurface(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	captureStderr(t, func() { stdout, err = runAggrega(t, args...) })
	return stdout, err
}

// TestReadingSurfacesRefuseALockfileTheBuildRefuses pulls the fixture once, then
// runs every reading surface twice: in a copy whose lockfile matches, where each
// must succeed and the build must pass, and in a copy where one Recipe the
// lockfile pins was edited after the pull, where the build refuses with the drift
// condition and each surface must refuse with the same condition, print exactly
// the text the build's refusal prints, and write nothing to its output stream.
//
// The equality with the build's text is the assertion that matters: it is what
// says the surface answers with the build's drift comparison rather than with a
// comparison of its own that happens to raise the same condition. The matching copy is the positive
// control — without it, a surface refusing every lockfile would pass the drifted
// half — and the build over the drifted copy is what shows the edit drifted the
// lockfile at all.
func TestReadingSurfacesRefuseALockfileTheBuildRefuses(t *testing.T) {
	// Not parallel, nor are its rows: every command runs from the workspace it reads,
	// which moves the process working directory, and the command tree's pre-run sets
	// the process-wide display decision.
	pulled, _ := driftAxesWorkspace(t)
	t.Chdir(pulled)
	mustRunDriftCommand(t, "the first pull", "pull", "--release", driftAxesRelease, "--pin-sources")

	t.Run("a lockfile that matches", func(t *testing.T) {
		t.Chdir(copyPulledWorkspace(t, pulled))
		mustRunDriftCommand(t, "the build over the lockfile the pull wrote", "build", "--release", driftAxesRelease)
		for _, surface := range readingSurfaces {
			stdout, err := runReadingSurface(t, surface.args...)
			if err != nil {
				t.Errorf("%s over a lockfile that matches failed:\n%s", surface.name, formatCompilerError(err))
				continue
			}
			if strings.TrimSpace(stdout) == "" {
				t.Errorf("%s over a lockfile that matches wrote nothing to its output stream", surface.name)
			}
		}
	})

	t.Run("a lockfile a pinned Recipe has drifted from", func(t *testing.T) {
		root := copyPulledWorkspace(t, pulled)
		t.Chdir(root)
		appendToFixture(t, filepath.Join(root, "catalog", "recipe", "helm", "svc", "1.0.0", "recipe.yaml"),
			"\n# edited after the pull\n")

		buildErr := runDriftCommand(t, "build", "--release", driftAxesRelease)
		if !errors.Is(buildErr, lockfile.ErrLockfileDrift()) {
			t.Fatalf("the build after the edit must refuse with the lockfile's drift condition; got %v", buildErr)
		}
		buildShown := formatCompilerError(buildErr)
		if !strings.Contains(buildShown, `"helm/svc:1.0.0"`) {
			t.Fatalf("the build's refusal does not name the edited Recipe, so the edit drifted something else:\n%s", buildShown)
		}
		if remedy := compilerops.LockfileDriftRemedy(driftAxesRelease); !strings.Contains(buildShown, remedy) {
			t.Fatalf("the build's refusal does not carry the remedy %q:\n%s", remedy, buildShown)
		}

		for _, surface := range readingSurfaces {
			stdout, err := runReadingSurface(t, surface.args...)
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Errorf("%s over a lockfile the build refuses must refuse with the drift condition; got %v",
					surface.name, err)
				continue
			}
			if shown := formatCompilerError(err); shown != buildShown {
				t.Errorf("%s does not print the halt the build prints:\n--- %s\n%s\n--- build\n%s",
					surface.name, surface.name, shown, buildShown)
			}
			if stdout != "" {
				t.Errorf("%s wrote to its output stream before refusing:\n%s", surface.name, stdout)
			}
		}
	})
}
