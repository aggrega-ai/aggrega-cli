// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// rootedBuildOptions returns run with the build's writes placed under root: the
// rendered artifacts at root/dist and a trace at root/.trace, which is the
// layout a build over a workspace rooted at root produces, spelled as absolute
// paths.
//
// It is the one home for building into a test's own directory. Left to its
// defaults, a build writes both beneath the workspace root it resolved, which is
// root for a test that resolved its own; naming the two directories outright
// keeps every write inside the test's own root even where a test resolves its
// workspace some other way, so no test writes into the package directory every
// test in the binary shares. A directory the caller already chose is left as it
// is.
func rootedBuildOptions(t *testing.T, root string, run buildops.RunOptions) buildops.RunOptions {
	t.Helper()
	if !filepath.IsAbs(root) {
		t.Fatalf("the build root %q is relative, so the build would still write relative to the working directory", root)
	}
	if run.OutDir == "" {
		run.OutDir = filepath.Join(root, artifactout.DefaultOutputDir)
	}
	if run.TraceDir == "" {
		run.TraceDir = filepath.Join(root, buildops.DefaultTraceDir)
	}
	return run
}

// dispatchBuildInRoot is dispatchBuild with the build's writes placed under the
// workspace root rw resolves, through rootedBuildOptions: the silent-diagnostics
// build a test drives most often, run from nowhere in particular.
func dispatchBuildInRoot(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, diagOut, out io.Writer) error {
	t.Helper()
	return dispatchBuildWithDiag(context.Background(), rw, releasePath, catalogFlagDirs, traceEnabled,
		buildDiagOptions{}, rootedBuildOptions(t, rw.Root(), buildops.RunOptions{}), diagOut, out)
}

// buildOutputEntries are the entries a build writes beneath its workspace root
// when nothing places them elsewhere: the default output directory and the
// default trace directory. A build whose resolution fell back to the package
// directory, the working directory every test shares, writes them there.
var buildOutputEntries = []string{artifactout.DefaultOutputDir, buildops.DefaultTraceDir}

// strayBuildOutput reports each build-output entry present in dir, in the order
// buildOutputEntries lists them.
func strayBuildOutput(dir string) []string {
	var present []string
	for _, name := range buildOutputEntries {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			present = append(present, name)
		}
	}
	return present
}

// guardPackageDirectory runs the suite and then fails it if a build wrote into
// the package directory, which is the working directory every test in this
// binary shares.
//
// A test that builds without placing its output, over a workspace resolved from
// that directory, writes dist and .trace there.
// For a test running beside others that is a defect whatever it asserts: two
// such builds race on one tree, and each can read the other's artifacts as its
// own. Nothing inside the test notices, because the build succeeds and the
// artifacts it looks for are there. So the suite checks the directory itself
// once every test has finished, and a stray entry turns a passing run into a
// failing one naming what was written and where.
//
// An entry already present before the suite starts is reported too: with it in
// place a new write cannot be told from the old one, so the guard would be
// blind. The entries it finds after the run are removed once reported, so the
// next run starts from a clean directory rather than inheriting the blindness.
func guardPackageDirectory(run func() int) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the package-directory guard cannot read the working directory: %v\n", err)
		return 1
	}
	if before := strayBuildOutput(dir); len(before) > 0 {
		fmt.Fprintf(os.Stderr, "the package directory %s already holds %v before any test ran; a build writing there "+
			"could not be told apart from what is already there, so remove it and run again\n", dir, before)
		return 1
	}
	code := run()
	after := strayBuildOutput(dir)
	if len(after) == 0 {
		return code
	}
	fmt.Fprintf(os.Stderr, "a test wrote %v into the package directory %s, the working directory every test shares: "+
		"build into the test's own root through rootedBuildOptions instead\n", after, dir)
	for _, name := range after {
		_ = os.RemoveAll(filepath.Join(dir, name))
	}
	return 1
}
