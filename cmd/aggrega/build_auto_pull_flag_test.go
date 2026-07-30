// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// entityURIs returns the sorted entity URIs recorded in the lockfile at path.
func entityURIs(t *testing.T, lockfilePath string) []string {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		t.Fatalf("LoadLockfile %s: %v", lockfilePath, err)
	}
	var uris []string
	for _, e := range lf.Entities {
		uris = append(uris, e.URI)
	}
	return uris
}

// TestBuildAutoPullFlagFillsLockfileWhenConfigOff proves the --auto-pull flag
// runs the pull step even when workspace auto_pull is off (the read-only
// default): a first flagged build over a workspace with no lockfile resolves the
// closure and writes the lockfile (adds the missing pins), and a second flagged
// build honors and preserves those pins unchanged.
func TestBuildAutoPullFlagFillsLockfileWhenConfigOff(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	// Default posture: workspace auto_pull is off. The flag alone must enable the
	// pull step.
	writeWorkspaceConfig(t, root, "")
	catalogDir := filepath.Join(root, "catalog")
	lockfilePath := filepath.Join(root, "release.lock")

	if _, err := os.Stat(lockfilePath); !os.IsNotExist(err) {
		t.Fatalf("precondition: expected no lockfile before build, stat err = %v", err)
	}

	var diagBuf, outBuf bytes.Buffer
	if err := dispatchBuildWithDiag(nil, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildRunOptions{autoPull: true}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --auto-pull (config off, no lockfile): %v", err)
	}
	if _, err := os.Stat(lockfilePath); err != nil {
		t.Fatalf("--auto-pull must fill the lockfile; stat err = %v", err)
	}
	added := entityURIs(t, lockfilePath)
	if len(added) == 0 {
		t.Fatalf("expected the pull step to add entity pins; got none")
	}
	foundServer := false
	for _, u := range added {
		if u == "server" {
			foundServer = true
		}
	}
	if !foundServer {
		t.Errorf("expected the server entity pin to be added; got %v", added)
	}

	// Second flagged build honors the now-present pins and preserves them.
	if err := dispatchBuildWithDiag(nil, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildRunOptions{autoPull: true}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("second build --auto-pull: %v", err)
	}
	preserved := entityURIs(t, lockfilePath)
	if strings.Join(preserved, ",") != strings.Join(added, ",") {
		t.Errorf("second --auto-pull must preserve existing pins; before=%v after=%v", added, preserved)
	}
}

// TestBuildUpgradeWithoutActivePullImpliesAutoPull proves --upgrade with no
// active pull step (neither --auto-pull nor workspace auto_pull) no longer
// errors: requesting --upgrade turns the pull step on and runs a full-refresh
// pull before the build, filling the lockfile in-process.
func TestBuildUpgradeWithoutActivePullImpliesAutoPull(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	// Default posture: workspace auto_pull is off. --upgrade alone must enable
	// the pull step rather than erroring.
	writeWorkspaceConfig(t, root, "")
	catalogDir := filepath.Join(root, "catalog")
	lockfilePath := filepath.Join(root, "release.lock")

	if _, err := os.Stat(lockfilePath); !os.IsNotExist(err) {
		t.Fatalf("precondition: expected no lockfile before build, stat err = %v", err)
	}

	var diagBuf, outBuf bytes.Buffer
	if err := dispatchBuildWithDiag(nil, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildRunOptions{upgrade: true}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --upgrade (no --auto-pull) should imply auto-pull, not error: %v", err)
	}

	// The implied full-refresh pull filled the lockfile with catalog entity pins.
	if _, err := os.Stat(lockfilePath); err != nil {
		t.Fatalf("--upgrade must imply auto-pull and fill the lockfile; stat err = %v", err)
	}
	if len(entityURIs(t, lockfilePath)) == 0 {
		t.Errorf("implied full-refresh pull must add catalog entity pins; got none")
	}
}

// injectExternalInput appends a synthetic external-input pin to the lockfile at
// path and writes it back. External-input pins are render-time pins the catalog
// closure does not produce, so they are the clean discriminator between an
// incremental fill (which carries them forward) and a full refresh (which
// discards the prior lockfile before rebuilding, dropping them).
func injectExternalInput(t *testing.T, lockfilePath string) string {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		t.Fatalf("LoadLockfile %s: %v", lockfilePath, err)
	}
	url := "https://example.test/prior-input.txt"
	lf.ExternalInputs = append(lf.ExternalInputs, lockfile.ExternalInput{
		RequiredBy: []string{"api"},
		Sha256:     strings.Repeat("a", 64),
		Size:       3,
		URL:        url,
	})
	if err := lockfile.WriteLockfile(lockfilePath, lf); err != nil {
		t.Fatalf("WriteLockfile %s: %v", lockfilePath, err)
	}
	return url
}

func lockfileHasExternalInput(t *testing.T, lockfilePath, url string) bool {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		t.Fatalf("LoadLockfile %s: %v", lockfilePath, err)
	}
	for _, x := range lf.ExternalInputs {
		if x.URL == url {
			return true
		}
	}
	return false
}

// TestBuildAutoPullIncrementalPreservesExternalInputs proves that --auto-pull
// without --upgrade is an incremental fill: it honors the existing lockfile
// and carries forward the render-time external-input pins recorded there.
func TestBuildAutoPullIncrementalPreservesExternalInputs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "")
	catalogDir := filepath.Join(root, "catalog")
	lockfilePath := filepath.Join(root, "release.lock")

	// Establish a lockfile out-of-band (the sole writer), then inject a prior
	// external-input pin the incremental fill must preserve.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}
	url := injectExternalInput(t, lockfilePath)

	var diagBuf, outBuf bytes.Buffer
	if err := dispatchBuildWithDiag(nil, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildRunOptions{autoPull: true}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --auto-pull (incremental): %v", err)
	}
	if !lockfileHasExternalInput(t, lockfilePath, url) {
		t.Errorf("incremental --auto-pull must carry forward the prior external-input pin %q", url)
	}
}

// TestBuildAutoPullUpgradeDiscardsExternalInputs proves that --auto-pull
// --upgrade is a full refresh: the pull step discards the existing pin set, so a
// prior render-time external-input pin is dropped rather than carried forward.
// Contrasts with the incremental case above and confirms --upgrade is threaded
// through to the pull step's full-refresh path.
func TestBuildAutoPullUpgradeDiscardsExternalInputs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "")
	catalogDir := filepath.Join(root, "catalog")
	lockfilePath := filepath.Join(root, "release.lock")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}
	url := injectExternalInput(t, lockfilePath)

	var diagBuf, outBuf bytes.Buffer
	if err := dispatchBuildWithDiag(nil, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildRunOptions{autoPull: true, upgrade: true}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --auto-pull --upgrade: %v", err)
	}
	if lockfileHasExternalInput(t, lockfilePath, url) {
		t.Errorf("--upgrade must discard the prior lockfile; external-input pin %q should not survive", url)
	}
	// The refresh still produces a valid lockfile with catalog entity pins.
	if len(entityURIs(t, lockfilePath)) == 0 {
		t.Errorf("--upgrade must rebuild the lockfile with catalog entity pins")
	}
}
