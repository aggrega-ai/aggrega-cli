// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// withoutGeneratedAt drops every column-0 `generated_at:` line, so two lockfile
// renderings can be compared on everything except the wall-clock provenance
// stamp.
//
// The stamp is the one field a lockfile carries that moves between two runs over
// identical input — the lockfile's own writer says so, and reuses a prior stamp
// only when the rest of the bytes are unchanged. So a comparison between what a
// report showed and what a later write produced has to set it aside, and setting
// aside exactly that one line is what keeps the comparison strict about
// everything else.
func withoutGeneratedAt(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "generated_at:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// dryRunStreams runs the pull over the workspace with the reporting flag set and
// returns what it printed to each stream.
//
// Both streams come back because the report is on one and the honesty about the
// report is on the other: a run whose external-input discovery could not finish
// says so on the diagnostic stream, and a test that read only stdout would call
// such a run clean.
func dryRunStreams(t *testing.T, root, releasePath, catalogDir string) (stdout, stderr string) {
	t.Helper()
	var out, diag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		DryRun:      true,
		DiagOut:     &diag,
		Out:         &out,
		diag:        buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("pull --dry-run: %v (stderr=%q)", err, diag.String())
	}
	return out.String(), diag.String()
}

// pullDryRun is dryRunStreams for a case that must report a complete picture: it
// fails the test where the run could not finish its external-input discovery,
// because such a report describes a lockfile no pull would write.
func pullDryRun(t *testing.T, root, releasePath, catalogDir string) string {
	t.Helper()
	stdout, stderr := dryRunStreams(t, root, releasePath, catalogDir)
	if strings.Contains(stderr, "external inputs: could not finish") {
		t.Fatalf("the reported run could not finish its external-input discovery, so its lockfile is not what a pull would write:\n%s", stderr)
	}
	return stdout
}

// pullWriting runs the same pull without the flag.
func pullWriting(t *testing.T, root, releasePath, catalogDir string) {
	t.Helper()
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("pull: %v", err)
	}
}

// TestPullDryRunNamesEveryFileItWouldWriteAndWritesNone drives both halves of
// the claim over one workspace: the reported run names the files and moves
// nothing, and the writing run that follows it moves exactly those files to
// exactly the reported content.
//
// The second half is what makes the first half mean anything. A report that
// named files nothing was going to touch would satisfy a "nothing moved"
// assertion on its own, so the writing run is driven from the same starting
// state and its result is compared against the report byte for byte.
//
// The fixture's Recipe carries a $import artifact, which is what makes the
// freeze write a lock beside that recipe — and that lock, together with the
// Release's own lockfile, puts two files in play with no registry and no
// network. The authored recipe is not one of them: no run writes it, so it is
// held to being byte-identical across both halves rather than expected to move.
func TestPullDryRunNamesEveryFileItWouldWriteAndWritesNone(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := fullURICollisionFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")
	recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
	entityLockPath := recipepin.LockPath(recipePath)
	lockfilePath := lockfile.PathFor(releasePath)

	recipeBefore := mustReadFile(t, recipePath)
	if _, err := os.Stat(entityLockPath); !os.IsNotExist(err) {
		t.Fatalf("the fixture must start unfrozen, or the report below has nothing to add; stat %s err = %v", entityLockPath, err)
	}

	// (a) The reported run.
	stdout := pullDryRun(t, root, releasePath, catalogDir)
	for _, want := range []string{"==> " + entityLockPath + " <==", "==> " + lockfilePath + " <=="} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report must name every file the pull would write; missing %q in:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, "Dry run: 2 files would be written; nothing was written") {
		t.Errorf("the summary must count the files it named; got:\n%s", stdout)
	}
	// The recipe is reached under two entity URIs in this fixture — its bare name
	// and its provider-qualified one — and both resolve to one document in one
	// directory, so they share one lock and it is named once.
	if n := strings.Count(stdout, "==> "+entityLockPath+" <=="); n != 1 {
		t.Errorf("the two URIs share one lock and it must be named once; the report names it %d times:\n%s", n, stdout)
	}

	// Nothing the report named exists on disk...
	if _, err := os.Stat(entityLockPath); !os.IsNotExist(err) {
		t.Errorf("the reported run must create no lock at %s; stat err = %v", entityLockPath, err)
	}
	if _, err := os.Stat(lockfilePath); !os.IsNotExist(err) {
		t.Errorf("the reported run must create no lockfile at %s; stat err = %v", lockfilePath, err)
	}
	// ...and the authored recipe, which the report never names, is untouched.
	if got := mustReadFile(t, recipePath); !bytes.Equal(got, recipeBefore) {
		t.Errorf("the reported run rewrote the recipe;\nbefore:\n%s\nafter:\n%s", recipeBefore, got)
	}

	// (b) The same pull without the flag, from the same starting state.
	pullWriting(t, root, releasePath, catalogDir)
	entityLockAfter := mustReadFile(t, entityLockPath)
	lockAfter := mustReadFile(t, lockfilePath)
	// The writing run must actually have moved what the report described —
	// otherwise the assertions above are satisfied by a report of nothing.
	if !strings.Contains(string(entityLockAfter), "artifactDigests") {
		t.Fatalf("the writing run must have frozen the recipe's pins into its lock:\n%s", entityLockAfter)
	}
	if got := mustReadFile(t, recipePath); !bytes.Equal(got, recipeBefore) {
		t.Errorf("the writing run rewrote the authored recipe;\nbefore:\n%s\nafter:\n%s", recipeBefore, got)
	}

	// The report is held to the write byte for byte, which is the strongest form
	// of "it describes a real effect": not merely that something moved, but that
	// what moved is what was reported.
	if !strings.Contains(stdout, string(entityLockAfter)) {
		t.Errorf("the lock the writing run produced is not what the report showed;\nwritten:\n%s\nreported:\n%s", entityLockAfter, stdout)
	}
	if !strings.Contains(withoutGeneratedAt(stdout), withoutGeneratedAt(string(lockAfter))) {
		t.Errorf("the lockfile the writing run produced is not what the report showed;\nwritten:\n%s\nreported:\n%s", lockAfter, stdout)
	}
}

// TestPullDryRunOverAnUnchangedWorkspaceNamesNothing is the discriminator for
// the "only the files that would move" rule. Without it, a report that always
// named every candidate file would satisfy every assertion in the test above.
func TestPullDryRunOverAnUnchangedWorkspaceNamesNothing(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := fullURICollisionFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")

	pullWriting(t, root, releasePath, catalogDir)

	stdout := pullDryRun(t, root, releasePath, catalogDir)
	if !strings.Contains(stdout, "Dry run: 0 files would be written") {
		t.Errorf("a report over a workspace a pull would not change must name no file; got:\n%s", stdout)
	}
	if strings.Contains(stdout, "==> ") {
		t.Errorf("a report that names a file over an unchanged workspace teaches a write that is not coming:\n%s", stdout)
	}
}

// TestPullDryRunSaysWhereItsLockfileIsShort pins the one case the report cannot
// describe completely, and holds it to saying so on both streams.
//
// A pull rewrites the entity.lock beside each Recipe and then renders the Release
// to find out which external inputs it consumes. A report leaves those locks
// alone, so where a recorded digest has gone stale — here, because the imported
// file was edited after the last resolution — that render is held to the stale
// digest the unwritten lock still records and stops. The reported lockfile is then
// short of a real one on its external-input axis alone.
//
// The assertion is that the shortfall is ANNOUNCED. A report that carried on
// silently would hand a reader a lockfile no pull would write, and the reader
// would have no way to tell.
func TestPullDryRunSaysWhereItsLockfileIsShort(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := fullURICollisionFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")
	importPath := filepath.Join(catalogDir, "recipes", "extra.yaml")

	pullWriting(t, root, releasePath, catalogDir)

	// Change the imported file, which leaves the digest entity.lock records for it
	// stale until the next freeze rewrites the lock.
	writeFixture(t, importPath, `apiVersion: v1
kind: ConfigMap
metadata:
  name: extra
data:
  origin: edited
`)

	stdout, stderr := dryRunStreams(t, root, releasePath, catalogDir)
	if !strings.Contains(stderr, "external inputs: could not finish") {
		t.Fatalf("this workspace is the case the report cannot describe completely; the diagnostic must say so:\n%s", stderr)
	}
	if !strings.Contains(stderr, "short of what a pull would write") {
		t.Errorf("the diagnostic must name the shortfall rather than only the render failure:\n%s", stderr)
	}
	// The false sentence a writing run's diagnostic carries must not reach a
	// reporting one: nothing was written, so nothing "were written".
	if strings.Contains(stderr, "dependency pins were written") {
		t.Errorf("a reported run wrote no pins, so its diagnostic must not say they were written:\n%s", stderr)
	}
	if !strings.Contains(stdout, "The reported lockfile is short of what a pull would write") {
		t.Errorf("the summary must carry the shortfall too, for a reader reading only stdout:\n%s", stdout)
	}
}

// TestPullDryRunIsRefusedWithVerify holds the pairing to a refusal rather than to
// a silent no-op. A verify already writes none of the files a dry run reports, so
// accepting the pair would leave the flag doing nothing while reading as though it
// had done something.
func TestPullDryRunIsRefusedWithVerify(t *testing.T) {
	cmd := newPullCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--release", "release.yaml", "--verify", "--dry-run"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--verify with --dry-run must be refused")
	}
	for _, want := range []string{"--verify", "--dry-run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name both flags; missing %q in %v", want, err)
		}
	}
}
