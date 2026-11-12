// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

// "The preview writes nothing" is the property every surface over it depends on:
// the command line's build writes the returned buffers itself, and the two
// in-memory surfaces write nothing at all. Asserting it here rather than at each
// surface is what makes it a property of the computation instead of three
// independent promises.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
)

// treeSnapshot records every entry under root by path, size and modification
// time. Size and modification time are both recorded because either alone misses
// a class: a rewrite with identical length moves only the time, and a file
// written twice within the filesystem's timestamp granularity moves only the
// size.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		snap[rel] = fmt.Sprintf("dir=%v size=%d mtime=%d", d.IsDir(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return snap
}

// diffSnapshots returns a human-readable description of every entry that was
// added, removed or modified between two snapshots, and the empty string when
// they are identical.
func diffSnapshots(before, after map[string]string) string {
	var lines []string
	for path, a := range after {
		b, existed := before[path]
		switch {
		case !existed:
			lines = append(lines, "added:    "+path)
		case a != b:
			lines = append(lines, "modified: "+path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			lines = append(lines, "removed:  "+path)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestDiffSnapshotsDetectsAPlantedFile is the calibration for the assertion
// below, and it is why that assertion is evidence rather than decoration: a
// comparison that cannot fail proves nothing about the run it brackets.
//
// It plants into a scratch directory rather than into the tracked fixture tree
// the real assertion watches, because a control that mutated the very tree under
// observation would be the defect it is meant to detect.
func TestDiffSnapshotsDetectsAPlantedFile(t *testing.T) {
	scratch := t.TempDir()
	if err := os.WriteFile(filepath.Join(scratch, "already-here.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed scratch: %v", err)
	}

	before := treeSnapshot(t, scratch)
	if d := diffSnapshots(before, treeSnapshot(t, scratch)); d != "" {
		t.Fatalf("an unchanged tree must compare equal; got:\n%s", d)
	}

	if err := os.WriteFile(filepath.Join(scratch, "planted.txt"), []byte("y"), 0o644); err != nil {
		t.Fatalf("plant: %v", err)
	}
	d := diffSnapshots(before, treeSnapshot(t, scratch))
	if !strings.Contains(d, "added:    planted.txt") {
		t.Fatalf("the comparison failed to see a planted file, so it cannot witness a write:\n%s", d)
	}
}

// TestPreviewWritesNothingToTheWorkspace runs the preview in its full
// artifact-producing posture — every gate on, the render reached, artifacts
// produced — over the tracked fixture workspace, and asserts that not one byte of
// that workspace moved.
//
// The fixture tree is deliberately the subject rather than a scratch copy: it is
// version-controlled, so a write this assertion somehow missed would still show
// up as a working-tree modification, and a write into a temporary directory
// nobody looks at afterwards is exactly the place a stray write would be lost.
//
// The render genuinely runs — the artifact count is asserted non-zero — because
// an assertion that nothing was written is trivially satisfied by a run that did
// nothing.
func TestPreviewWritesNothingToTheWorkspace(t *testing.T) {
	view, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)

	before := treeSnapshot(t, workspaceRoot)

	// The artifact-producing operation, so every gate is on. The fixture carries no
	// lockfile, so the gates that read one stand down on the inputs; everything
	// else runs, render included.
	//
	// The fixture's render requests no external input, so the seam is named as
	// absent rather than built. Naming it is the point: the constructor admits no
	// value that leaves the choice unmade.
	preview, err := PreviewForArtifact(
		context.Background(),
		NewPreviewInputs(
			NoExternalInputs(),
			view,
			releasePath,
			workspaceRoot,
			wsCfg,
			"test",
		),
		pipeline.Options{},
	)
	if err != nil {
		t.Fatalf("the preview over the fixture workspace: %v", err)
	}
	if completed := preview.PipelineOutcome().Completed(); completed == nil || len(completed.Artifacts) == 0 {
		t.Fatal("the render must actually produce artifacts, or 'it wrote nothing' says nothing")
	}

	if d := diffSnapshots(before, treeSnapshot(t, workspaceRoot)); d != "" {
		t.Fatalf("the preview wrote to the workspace; every surface over it promises it does not:\n%s", d)
	}
}
