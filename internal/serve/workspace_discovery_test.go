// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverWorkspaceRootMatchesEachMarkerType verifies that
// discoverWorkspaceRoot returns successfully when any of the five
// marker types exists in the parent chain. Each fixture isolates one
// marker so the per-tier match logic is exercised independently.
func TestDiscoverWorkspaceRootMatchesEachMarkerType(t *testing.T) {
	cases := []struct {
		name       string
		fixture    string
		wantSource markerSource
		wantConfig bool
	}{
		{"config-file", "config-only", markerConfigFile, true},
		{"aggrega-dir", "aggrega-dir", markerAggregaDir, false},
		{"aggregate-glob", "aggregate-glob", markerAggregateGlob, false},
		{"release-glob", "release-glob", markerReleaseGlob, false},
		{"manifest-glob", "manifest-glob", markerManifestGlob, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := absTestdata(t, tc.fixture)
			res, err := discoverWorkspaceRoot(dir)
			if err != nil {
				t.Fatalf("discoverWorkspaceRoot(%s): %v", dir, err)
			}
			if res.root != dir {
				t.Errorf("root: got %s, want %s", res.root, dir)
			}
			if res.source != tc.wantSource {
				t.Errorf("source: got %s, want %s", res.source, tc.wantSource)
			}
			if tc.wantConfig && res.configPath == "" {
				t.Error("configPath: expected non-empty for config-file marker")
			}
			if !tc.wantConfig && res.configPath != "" {
				t.Errorf("configPath: expected empty for non-config marker, got %q", res.configPath)
			}
		})
	}
}

// TestDiscoverWorkspaceRootPriorityTiebreak verifies that when a
// single directory contains markers from multiple tiers, the
// highest-priority marker wins. Priority order:
// .aggrega-config.yaml > .aggrega/ > aggregate*.yaml >
// release*.yaml > manifest*.yaml.
func TestDiscoverWorkspaceRootPriorityTiebreak(t *testing.T) {
	dir := absTestdata(t, "priority-tiebreak")
	res, err := discoverWorkspaceRoot(dir)
	if err != nil {
		t.Fatalf("discoverWorkspaceRoot: %v", err)
	}
	if res.source != markerConfigFile {
		t.Errorf("priority tiebreak: got %s, want %s (config file is highest priority)",
			res.source, markerConfigFile)
	}
}

// TestDiscoverWorkspaceRootGlobMatchesBothPlainAndSuffixedForms
// verifies that aggregate*.yaml matches both aggregate.yaml and
// aggregate-staging.yaml. Same for release and manifest globs.
// The fixture for each *-glob/ contains both forms; existence of EITHER
// form is sufficient to anchor.
func TestDiscoverWorkspaceRootGlobMatchesBothPlainAndSuffixedForms(t *testing.T) {
	cases := []struct {
		name       string
		fixture    string
		wantSource markerSource
	}{
		{"aggregate-glob both forms present", "aggregate-glob", markerAggregateGlob},
		{"release-glob both forms present", "release-glob", markerReleaseGlob},
		{"manifest-glob both forms present", "manifest-glob", markerManifestGlob},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := absTestdata(t, tc.fixture)
			res, err := discoverWorkspaceRoot(dir)
			if err != nil {
				t.Fatalf("discoverWorkspaceRoot: %v", err)
			}
			if res.source != tc.wantSource {
				t.Errorf("source: got %s, want %s", res.source, tc.wantSource)
			}
		})
	}
}

// TestDiscoverWorkspaceRootExcludesLockfiles covers the lockfile-
// exclusion rule: release.lock.yaml and aggregate.lock.yaml do not
// anchor a workspace on their own. The fixture contains only lockfile
// names; discovery must fall through and return os.ErrNotExist.
func TestDiscoverWorkspaceRootExcludesLockfiles(t *testing.T) {
	dir := absTestdata(t, "lockfile-exclusion")
	_, err := discoverWorkspaceRoot(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist for lockfile-only fixture, got %v", err)
	}
}

// TestDiscoverWorkspaceRootWalksUpward covers the parent-walk
// behavior: a cwd with no markers walks up to the parent's marker.
// Fixture: nested/parent/.aggrega-config.yaml, nested/parent/child/cwd
// (empty). Discovery from cwd anchors at parent.
func TestDiscoverWorkspaceRootWalksUpward(t *testing.T) {
	cwd := absTestdata(t, "nested/parent/child/cwd")
	want := absTestdata(t, "nested/parent")
	res, err := discoverWorkspaceRoot(cwd)
	if err != nil {
		t.Fatalf("discoverWorkspaceRoot: %v", err)
	}
	if res.root != want {
		t.Errorf("root: got %s, want %s", res.root, want)
	}
	if res.source != markerConfigFile {
		t.Errorf("source: got %s, want %s", res.source, markerConfigFile)
	}
}

// TestDiscoverWorkspaceRootDeepestMatchWins covers the cross-directory
// disambiguation rule: a deeper directory's marker (any tier) anchors
// before reaching a shallower directory's higher-priority marker.
// Fixture: parent/.aggrega-config.yaml + parent/child/aggregate.yaml.
// Discovery from child anchors at child (aggregate.yaml), NOT at parent
// (despite parent's higher-priority config file).
func TestDiscoverWorkspaceRootDeepestMatchWins(t *testing.T) {
	cwd := absTestdata(t, "nested-tier-precedence/parent/child")
	res, err := discoverWorkspaceRoot(cwd)
	if err != nil {
		t.Fatalf("discoverWorkspaceRoot: %v", err)
	}
	if res.root != cwd {
		t.Errorf("root: got %s, want %s (deeper match must win)", res.root, cwd)
	}
	if res.source != markerAggregateGlob {
		t.Errorf("source: got %s, want %s", res.source, markerAggregateGlob)
	}
}

// TestDiscoverWorkspaceRootReturnsNotExistOnUnreachableFilesystem
// covers the walk-to-root termination: when no marker exists anywhere
// in the parent chain (using a tmp dir that is isolated from any
// real Aggrega workspace), discovery returns os.ErrNotExist without
// looping.
func TestDiscoverWorkspaceRootReturnsNotExistOnUnreachableFilesystem(t *testing.T) {
	tmp := t.TempDir()
	// On systems where the tmp tree happens to live under a real
	// workspace, the walk might find an ancestor marker. Detect and
	// skip in that case rather than fail spuriously.
	if _, err := discoverWorkspaceRoot(tmp); err == nil {
		t.Skip("tmp directory is nested under a real Aggrega workspace; cannot exercise no-match path")
	}
	res, err := discoverWorkspaceRoot(tmp)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v (result=%+v)", err, res)
	}
}

// absTestdata returns the absolute path of a discovery fixture. The
// abs path is required because discoverWorkspaceRoot's parent-walk
// terminates on filepath.Dir returning the same value — only an
// absolute path produces a well-defined termination.
func absTestdata(t *testing.T, relPath string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "discovery", relPath))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}
