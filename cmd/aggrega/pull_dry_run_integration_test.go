// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// dryRunThreeFileWorkspace lays a workspace where ONE pull writes all three of
// the files a pull can write: the lockfile, the lock beside a Recipe into which
// the pull records that Recipe's pins, and the plugin identity record.
//
// It is the two existing fixtures composed rather than a third one authored. The
// Recipe carrying a $import artifact is what puts a lock beside it in play, and a
// linter attachment served by a registry is what puts the identity record in
// play — neither fixture reaches all three on its own, and the point of this one
// is that a reader previewing a pull sees every file it touches rather than the
// one that is easy to show.
func dryRunThreeFileWorkspace(t *testing.T, host string) (root, releasePath string, published publishedVerdictPlugin) {
	t.Helper()
	root = t.TempDir()
	releasePath = fullURICollisionFixture(t, root)
	published = publishVerdictPlugin(t, host, "linter", "dry-run-probe", "1.0.0")
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), ""+
		"workspace:\n"+
		"  catalog_dirs:\n"+
		"    - "+filepath.Join(root, "catalog")+"\n"+
		"auto_pull: false\n"+
		"pin_sources_at_pull: true\n"+
		"default_registry: \""+host+"\"\n"+
		"plugins:\n"+
		"  linter:\n"+
		"    - uri: "+published.URI+"\n")
	return root, releasePath, published
}

// readOrAbsent returns a file's bytes, or nil where there is no file — so a
// before-and-after comparison covers a file that was never created as well as
// one that was left alone.
func readOrAbsent(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestIntegration_PullDryRunCoversEveryFileAPullWrites is the whole claim over
// the workspace that exercises all three files at once: with the flag, the
// command names each of them and moves none; without it, each of them moves to
// exactly the content that was reported.
//
// Three files rather than one is the point of the test. A preview naming only
// the lockfile would teach a reader that the lockfile is all a pull touches,
// which is the belief this flag exists to correct — so the assertion is per
// file, and the entity lock and the identity record are named as explicitly as
// the lockfile is. The Recipe itself is held to being untouched by both runs,
// because a pull records what it derives beside an authored file and never in it.
func TestIntegration_PullDryRunCoversEveryFileAPullWrites(t *testing.T) {
	host := startRegistry(t, nil)
	root, releasePath, published := dryRunThreeFileWorkspace(t, host)
	t.Chdir(root)
	catalogDir := filepath.Join(root, "catalog")

	recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
	entityLockPath := recipepin.LockPath(recipePath)
	lockfilePath := lockfile.PathFor(releasePath)
	recordPath := pluginstrust.PathFor(releasePath)

	recipeBefore := readOrAbsent(t, recipePath)
	entityLockBefore := readOrAbsent(t, entityLockPath)
	lockBefore := readOrAbsent(t, lockfilePath)
	recordBefore := readOrAbsent(t, recordPath)
	if lockBefore != nil || recordBefore != nil || entityLockBefore != nil {
		t.Fatalf("precondition: none of the lockfile, the entity lock and the identity record exists before the first pull")
	}
	if recipeBefore == nil {
		t.Fatalf("precondition: the fixture must author the recipe the entity lock sits beside")
	}

	// (a) The reported run: every file named, nothing moved.
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
	stdout := out.String()
	if strings.Contains(diag.String(), "external inputs: could not finish") {
		t.Fatalf("the reported run could not finish its external-input discovery, so its lockfile is not what a pull would write:\n%s", diag.String())
	}
	for _, path := range []string{entityLockPath, lockfilePath, recordPath} {
		if !strings.Contains(stdout, "==> "+path+" <==") {
			t.Errorf("the report must name %s; it named:\n%s", path, stdout)
		}
	}
	if strings.Contains(stdout, "==> "+recipePath+" <==") {
		t.Errorf("the report named the authored recipe, which no pull writes:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Dry run: 3 files would be written; nothing was written") {
		t.Errorf("the summary must count all three files; got:\n%s", stdout)
	}
	// Every line a pull says about the identity record states a change the file
	// has undergone — created, recorded, adopted, dropped — and a reported run
	// leaves it exactly as it was, so it says none of them. The record's own label
	// is asserted rather than any one line, because a report that announced a
	// change it did not make would contradict this flag's whole promise whichever
	// line it chose.
	if strings.Contains(diag.String(), "plugin trust:") {
		t.Errorf("a reported run moved no identity record, so it must announce nothing about one:\n%s", diag.String())
	}

	if got := readOrAbsent(t, recipePath); !bytes.Equal(got, recipeBefore) {
		t.Errorf("the reported run rewrote the recipe;\nbefore:\n%s\nafter:\n%s", recipeBefore, got)
	}
	if got := readOrAbsent(t, entityLockPath); got != nil {
		t.Errorf("the reported run created an entity lock:\n%s", got)
	}
	if got := readOrAbsent(t, lockfilePath); got != nil {
		t.Errorf("the reported run created a lockfile:\n%s", got)
	}
	if got := readOrAbsent(t, recordPath); got != nil {
		t.Errorf("the reported run created an identity record:\n%s", got)
	}

	// (b) The same pull without the flag, from the same starting state.
	var writeOut, writeDiag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		DiagOut:     &writeDiag,
		Out:         &writeOut,
		diag:        buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("pull: %v (stderr=%q)", err, writeDiag.String())
	}

	recipeAfter := readOrAbsent(t, recipePath)
	entityLockAfter := readOrAbsent(t, entityLockPath)
	lockAfter := readOrAbsent(t, lockfilePath)
	recordAfter := readOrAbsent(t, recordPath)
	if !bytes.Equal(recipeAfter, recipeBefore) {
		t.Errorf("the writing run rewrote the authored recipe;\nbefore:\n%s\nafter:\n%s", recipeBefore, recipeAfter)
	}
	if entityLockAfter == nil {
		t.Fatal("the writing run wrote no entity lock, so the report described nothing for it")
	}
	if lockAfter == nil {
		t.Fatal("the writing run wrote no lockfile, so the report described nothing for it")
	}
	if recordAfter == nil {
		t.Fatal("the writing run wrote no identity record, so the report described nothing for it")
	}
	if _, held := verdicttest.TrustRecordAt(t, releasePath).Find(published.Coordinate); !held {
		t.Fatalf("the writing run must record the acquired plugin %s", published.Coordinate)
	}
	// The other side of the silence above, driven through the same command: the
	// run that WROTE the record says so, naming the file it created and the
	// identity it now holds. Asserted here rather than only in a unit test because
	// this is the one place both runs are driven from the real entry point over
	// the same workspace — which is what makes the reported run's silence
	// attributable to the flag rather than to an announcement nothing ever emits.
	for _, want := range []string{
		"plugin trust: created",
		"plugin trust: " + published.Coordinate + ": recorded for the first time",
	} {
		if !strings.Contains(writeDiag.String(), want) {
			t.Errorf("the writing run must announce %q; it said:\n%s", want, writeDiag.String())
		}
	}

	// Each reported file is held to the bytes the writing run produced. The
	// lockfile is compared with its wall-clock provenance stamp set aside, which
	// is the one field two runs over identical input are expected to differ on.
	if !strings.Contains(stdout, string(entityLockAfter)) {
		t.Errorf("the entity lock the writing run produced is not what the report showed;\nwritten:\n%s\nreported:\n%s", entityLockAfter, stdout)
	}
	if !strings.Contains(stdout, string(recordAfter)) {
		t.Errorf("the identity record the writing run produced is not what the report showed;\nwritten:\n%s\nreported:\n%s", recordAfter, stdout)
	}
	if !strings.Contains(withoutGeneratedAt(stdout), withoutGeneratedAt(string(lockAfter))) {
		t.Errorf("the lockfile the writing run produced is not what the report showed;\nwritten:\n%s\nreported:\n%s", lockAfter, stdout)
	}
}
