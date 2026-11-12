// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// One build, one render — the two halves of that claim, and why each needs its
// own fixture.
//
// The first half is that a build which fills its own lockfile emits exactly what
// a resolution followed by a plain build emits. That is a claim about breadth: a
// difference between the two would show up in whichever construct the two paths
// happened to hand different inputs, so the fixture is the widest one the tree
// has rather than the smallest one that renders.
//
// The second half is that it renders ONCE. Byte identity cannot witness that —
// it holds under one render and under two — so a separate observable is needed,
// and the one that exists is a line the render step writes per render. That
// needs a fixture whose render writes such a line, which the wide fixture does
// not: its target expresses everything its Workloads declare, so it renders
// silently. Hence two tests over two fixtures, each fixture chosen for what its
// own assertion requires.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
)

// fullstackTriad copies the wide fixture's Aggregate, Manifest and Release into
// root and returns the Release path. The triad is copied rather than referenced
// in place because a resolution writes its lockfile beside the Release, and a
// run that wrote into the shared fixture directory would leave the next one
// reading a lockfile it did not create.
//
// The catalog is read in place, which is safe for this fixture in a way it would
// not be in general: every recipe it maps is synthesized, so the resolution's
// recipe freeze has no fetched source to pin and writes nothing back.
func fullstackTriad(t *testing.T, fixtureDir, root string) string {
	t.Helper()
	for _, name := range []string{"aggregate.yaml", "manifest.yaml", "release.yaml"} {
		body, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatalf("read wide fixture %s: %v", name, err)
		}
		writeFixture(t, filepath.Join(root, name), string(body))
	}
	return filepath.Join(root, "release.yaml")
}

// TestBuildAutoPullEmitsWhatPullThenBuildEmits is the parity this change owes and
// nothing else asserted: the artifacts a build that fills its own lockfile emits
// are byte-for-byte the artifacts a resolution followed by a plain build emits.
//
// # The fixture, and what it reaches
//
// The wide fixture: three components across three boundaries, one of them
// contributed by a SUB-AGGREGATE the top-level Aggregate references by type; two
// `uses` edges authored in both shapes the grammar admits, one of them carrying
// an explicit injection mapping into environment variables; a Feature attached
// at the Release with its own per-instance infra and a FeatureRecipe behind it;
// a Component that inherits through `extends`; per-boundary annotations; and
// recipes that emit artifacts beside their chart. Every one of those is a place
// the two paths could have handed their render different inputs, which is the
// whole reason the acceptance asked for breadth rather than for the smallest
// Release that renders.
//
// # Why both halves of the comparison are checked for substance first
//
// Two empty trees are byte-identical, and so are two trees from a pair of builds
// that both quietly rendered nothing. So each side is required to carry files
// from every boundary before the comparison is allowed to mean anything.
func TestBuildAutoPullEmitsWhatPullThenBuildEmits(t *testing.T) {
	t.Parallel()
	// Both helpers derive the fixture's path from the working directory, which no
	// test running beside this one moves; each leg's build writes into its own
	// root rather than into that directory.
	catalogDir := cliFullstackCatalogDir(t)
	fixtureDir := filepath.Dir(catalogDir)
	providerOverlay := seedEmbeddedProviderOverlay(t, "provider/helm")
	catalogDirs := []string{providerOverlay, catalogDir}

	// Leg A — the two-command path: resolve, then build against what was resolved.
	rootA := t.TempDir()
	releaseA := fullstackTriad(t, fixtureDir, rootA)
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: rootA}),
		ReleasePath: releaseA,
		CatalogDirs: catalogDirs,
	}); err != nil {
		t.Fatalf("leg A resolution: %v", err)
	}
	var diagA, outA bytes.Buffer
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: rootA}), releaseA, catalogDirs, false,
		buildDiagOptions{}, rootedBuildOptions(t, rootA, buildops.RunOptions{}), &diagA, &outA); err != nil {
		t.Fatalf("leg A build: %v\ndiagnostics:\n%s", err, diagA.String())
	}
	treeA := renderedTree(t, filepath.Join(rootA, "dist"))

	// Leg B — the one-command path: the build fills its own lockfile.
	rootB := t.TempDir()
	releaseB := fullstackTriad(t, fixtureDir, rootB)
	var diagB, outB bytes.Buffer
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: rootB}), releaseB, catalogDirs, false,
		buildDiagOptions{}, rootedBuildOptions(t, rootB, buildops.RunOptions{AutoPull: true}), &diagB, &outB); err != nil {
		t.Fatalf("leg B build --auto-pull: %v\ndiagnostics:\n%s", err, diagB.String())
	}
	treeB := renderedTree(t, filepath.Join(rootB, "dist"))

	// Substance before comparison: a tree missing a boundary would make the
	// equality below true of less than the fixture describes.
	for _, leg := range []struct {
		name string
		tree map[string]string
	}{{"pull then build", treeA}, {"build --auto-pull", treeB}} {
		// The Release's boundary directories are workspace-relative and land
		// beneath the output directory, so a boundary's files are keyed
		// dist/<boundary>/… within the tree read from that directory.
		for _, boundary := range []string{"frontend", "backend", "data"} {
			found := false
			for rel := range leg.tree {
				if strings.HasPrefix(filepath.ToSlash(rel), "dist/"+boundary+"/") {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s emitted nothing under the %q boundary, so the comparison below would "+
					"cover less than the fixture declares; emitted: %v", leg.name, boundary, sortedRenderedPaths(leg.tree))
			}
		}
	}

	if len(treeA) != len(treeB) {
		t.Fatalf("the two paths emitted different file counts: pull-then-build %d, build --auto-pull %d\n"+
			"pull then build: %v\nbuild --auto-pull: %v",
			len(treeA), len(treeB), sortedRenderedPaths(treeA), sortedRenderedPaths(treeB))
	}
	for rel, want := range treeA {
		got, ok := treeB[rel]
		if !ok {
			t.Errorf("build --auto-pull emitted no %s", rel)
			continue
		}
		if got != want {
			t.Errorf("build --auto-pull emitted different bytes for %s\n--- pull then build ---\n%s\n--- build --auto-pull ---\n%s",
				rel, want, got)
		}
	}
	for rel := range treeB {
		if _, ok := treeA[rel]; !ok {
			t.Errorf("build --auto-pull emitted %s, which pull-then-build did not", rel)
		}
	}
}

// sortedRenderedPaths returns a rendered tree's paths in a stable order, for a
// failure message a reader can diff by eye.
func sortedRenderedPaths(tree map[string]string) []string {
	out := make([]string, 0, len(tree))
	for rel := range tree {
		out = append(out, rel)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// countOccurrences returns how many times needle appears in s, non-overlapping.
func countOccurrences(s, needle string) int {
	if needle == "" {
		return 0
	}
	return strings.Count(s, needle)
}

// TestBuildAutoPullRendersTheReleaseOnce is the assertion byte identity cannot
// make. Byte identity holds whether the Release was rendered once or twice, so
// something has to count the renders, and what counts them is a line the render
// step writes once per render: a warning naming a field the target cannot
// express. The fixture authors such a field deliberately, so each render of it
// produces exactly one line, and the number of lines IS the number of renders.
//
// The first leg is the calibration and it is not decoration: a plain build
// renders once by construction, so it fixes what one render's worth of output
// looks like on this fixture. Without it a count of one on the auto-pulling leg
// could as easily mean the fixture stopped warning at all.
//
// The third leg is the flag that had no way to reach the fill's render before
// the fill's render became the build's. It is asserted here rather than in a
// test of its own because it is the same count on the same fixture, read under
// one more flag — and because a suppression that reached only one of two renders
// is exactly the shape this fixture is able to see.
func TestBuildAutoPullRendersTheReleaseOnce(t *testing.T) {
	// Not parallel: the surface under assertion is the process stderr, and the
	// test moves the process working directory.
	work := t.TempDir()
	t.Chdir(work)
	releaseFile, catalogRoot, _ := composeUnsupportedFieldWorkspace(t, work)
	marker := unsupportedFieldMarker(t, "compose", "replicas")

	// Leg 1 — calibration. A plain build over a primed lockfile renders once, so
	// this establishes what one render's worth of warning looks like.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: work}), ReleasePath: releaseFile, CatalogDirs: []string{catalogRoot}}); err != nil {
		t.Fatalf("prime lockfile: %v", err)
	}
	var plainErr error
	var plainCobra string
	plainStderr := captureStderr(t, func() {
		plainCobra, plainErr = runAggrega(t, "build", "--release", releaseFile, "--catalog", catalogRoot)
	})
	if plainErr != nil {
		t.Fatalf("plain build failed: %v\ncobra output:\n%s\nstderr:\n%s", plainErr, plainCobra, plainStderr)
	}
	if got := countOccurrences(plainStderr, marker); got != 1 {
		t.Fatalf("a plain build renders once, so its warning must appear once; got %d\nstderr:\n%s", got, plainStderr)
	}

	// Leg 2 — the assertion. An auto-pulling build resolves and renders in one
	// command; the warning appearing twice would mean the Release was rendered
	// twice, which is the defect this whole change removes.
	var autoErr error
	var autoCobra string
	autoStderr := captureStderr(t, func() {
		autoCobra, autoErr = runAggrega(t, "build", "--release", releaseFile, "--catalog", catalogRoot, "--auto-pull")
	})
	if autoErr != nil {
		t.Fatalf("build --auto-pull failed: %v\ncobra output:\n%s\nstderr:\n%s", autoErr, autoCobra, autoStderr)
	}
	if got := countOccurrences(autoStderr, marker); got != 1 {
		t.Errorf("build --auto-pull rendered the Release %d time(s); it must render it exactly once.\n"+
			"The warning below is written once per render, so a count above one is a second render.\nmarker: %s\nstderr:\n%s",
			got, marker, autoStderr)
	}
	// The count means nothing if the run did not render: a build that halted
	// before the render would also print one warning or none.
	if _, err := os.Stat(filepath.Join(work, "dist")); err != nil {
		t.Fatalf("build --auto-pull left no rendered output, so its warning count says nothing about rendering: %v", err)
	}

	// Leg 3 — the display filter now reaches the render that happens. With two
	// renders the flag could only ever silence the one it was handed, leaving the
	// other's warning on the reader's terminal.
	var quietErr error
	var quietCobra string
	quietStderr := captureStderr(t, func() {
		quietCobra, quietErr = runAggrega(t, "build",
			"--release", releaseFile, "--catalog", catalogRoot, "--auto-pull", "--"+flagNoUnsupportedFieldWarnings)
	})
	if quietErr != nil {
		t.Fatalf("build --auto-pull --%s failed: %v\ncobra output:\n%s\nstderr:\n%s",
			flagNoUnsupportedFieldWarnings, quietErr, quietCobra, quietStderr)
	}
	if got := countOccurrences(quietStderr, marker); got != 0 {
		t.Errorf("--%s did not reach the render an auto-pulling build performs: the class surfaced %d time(s).\nmarker: %s\nstderr:\n%s",
			flagNoUnsupportedFieldWarnings, got, marker, quietStderr)
	}
}
