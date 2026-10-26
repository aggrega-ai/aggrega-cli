// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// unreadParameterAdvisory is the line a resolution shows for the Aggregate's
// parameter that no reference reads, as the build, the pull and the validation
// all print it.
const unreadParameterAdvisory = `  lint: declared parameter is read by no reference in the Aggregate (Aggregate lint-once, at .spec.parameters.unused_knob, "unused_knob")`

// unreadParameterWorkspace is the workspace lintOnceWorkspace lays down, with an
// Aggregate that declares one parameter no reference reads — the advisory the
// resolution raises for it is the line this test counts.
func unreadParameterWorkspace(t *testing.T, root string) {
	t.Helper()
	lintOnceWorkspace(t, root)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: lint-once
  version: "1.0.0"
spec:
  parameters:
    unused_knob:
      type: string
      default: "x"
  components:
    app:
      type: test/svc
      features:
        secret:
          type: test/overlap
          values:
            origin: inline-value
`)
}

// TestEachCommandShowsAResolutionAdvisoryOnce drives validate, pull, build, and
// a build that fills its own lockfile, through the real command tree, and counts
// the advisory the resolution raises for a parameter no reference reads.
//
// A pull resolves the Release twice — once to draw the graph it pins and once to
// render it for the external inputs it needs — and a build that fills its own
// lockfile runs that pull. Each command is one operation, so each shows the
// advisory exactly once. The count is exact rather than "at most once", so a
// command that stopped showing the advisory fails here too.
//
// 🔴 PLANTED DEFECT: drop compilerops.WithResolutionAdvisoriesShown from the
// discovery render's options (internal/pullops/externalinputs.go) and the pull
// and the filling build each show the line twice.
func TestEachCommandShowsAResolutionAdvisoryOnce(t *testing.T) {
	// Not parallel: the advisories are read from the process stderr, and the test
	// moves the process working directory.
	cases := []struct {
		name string
		// prepare runs ahead of the command whose output is counted, with its own
		// output discarded: the build counted here reads the lockfile a pull wrote.
		prepare []string
		args    []string
	}{
		{name: "validate", args: []string{"validate", "--release", "release.yaml"}},
		{name: "pull", args: []string{"pull", "--release", "release.yaml"}},
		{name: "build", prepare: []string{"pull", "--release", "release.yaml"}, args: []string{"build", "--release", "release.yaml"}},
		{name: "build filling its own lockfile", args: []string{"build", "--release", "release.yaml", "--auto-pull"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			unreadParameterWorkspace(t, root)
			t.Chdir(root)
			if tc.prepare != nil {
				captureStderr(t, func() {
					if out, err := runAggrega(t, append([]string{"--workspace-root", root}, tc.prepare...)...); err != nil {
						t.Fatalf("%s: %v\n%s", strings.Join(tc.prepare, " "), err, out)
					}
				})
			}
			var out string
			var err error
			stderr := captureStderr(t, func() {
				out, err = runAggrega(t, append([]string{"--workspace-root", root}, tc.args...)...)
			})
			if err != nil {
				t.Fatalf("%s: %v\noutput:\n%s\nstderr:\n%s", strings.Join(tc.args, " "), err, out, stderr)
			}
			if n := strings.Count(out+"\n"+stderr, unreadParameterAdvisory+"\n"); n != 1 {
				t.Errorf("%s showed the advisory %d time(s); want once\noutput:\n%s\nstderr:\n%s",
					strings.Join(tc.args, " "), n, out, stderr)
			}
		})
	}
}
