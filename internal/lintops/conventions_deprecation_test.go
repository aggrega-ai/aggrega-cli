// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

// The release-scoped conventions pass's side of the deprecation report. This pass
// produces a REPORT, so it names every recorded marker and never halts on one; and
// because a marker is a fact about the lockfile alone, it says so even on a run
// that cannot get as far as resolving the closure.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	lintDeprecationStamp = "2026-08-01T09:30:00Z"
	lintDeprecatedURI    = "oci://reg.example/plugins/helm-renderer:1.4.0"
)

// deprecatedPinWorkspace writes a workspace whose lockfile records a withdrawal
// beside a Release path that does not exist, so the pass fails at the closure and
// the report has to have been emitted ahead of it to be observed at all.
//
// withMarker=false is the matched control: without it, a change that emitted a
// deprecation line unconditionally would leave the assertion below green.
func deprecatedPinWorkspace(t *testing.T, withMarker bool) (rw *workspaceconfig.ResolvedWorkspace, releasePath string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	releasePath = filepath.Join(root, "release.yaml")

	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: lintDeprecationStamp,
	}
	if withMarker {
		lf.Plugins = []lockfile.Plugin{{
			URI:             lintDeprecatedURI,
			ResolvedVersion: "1.4.0",
			Deprecation: &lockfile.DeprecationRecord{
				Severity: "retracted",
				Message:  "withdrawn: unsound renderer",
			},
		}}
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return rw, releasePath
}

// TestReleaseConventionsReportsARetractedPinAheadOfTheClosure proves the report is
// emitted from the lockfile alone. The fixture's Release does not exist, so the
// pass cannot succeed — and the notice still reaches the reader, which is the whole
// reason it sits ahead of the closure work rather than after it.
func TestReleaseConventionsReportsARetractedPinAheadOfTheClosure(t *testing.T) {
	rw, releasePath := deprecatedPinWorkspace(t, true)

	var diag bytes.Buffer
	err := RunRelease(t.Context(), rw, releasePath, nil, io.Discard, &diag, nil)
	if err == nil {
		t.Fatal("the fixture has no Release, so the pass cannot succeed; the assertion below would be untested")
	}

	got := diag.String()
	for _, want := range []string{
		"deprecation: " + lintDeprecatedURI,
		"is retracted",
		"withdrawn: unsound renderer",
		"as of " + lintDeprecationStamp,
		"aggrega pull",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the pass drops %q — a withdrawn dependency went unmentioned:\n%s", want, got)
		}
	}
}

// TestReleaseConventionsWithoutAMarkerReportsNoDeprecation is the matched control,
// over the identical workspace with no marker recorded.
func TestReleaseConventionsWithoutAMarkerReportsNoDeprecation(t *testing.T) {
	rw, releasePath := deprecatedPinWorkspace(t, false)

	var diag bytes.Buffer
	err := RunRelease(t.Context(), rw, releasePath, nil, io.Discard, &diag, nil)
	if err == nil {
		t.Fatal("the fixture has no Release, so the pass cannot succeed; the control would be vacuous")
	}
	if strings.Contains(diag.String(), "deprecation:") {
		t.Errorf("no recorded marker must report nothing:\n%s", diag.String())
	}
}

// TestReleaseConventionsToleratesAnAbsentLockfile holds the by-design case: this
// pass runs without a lockfile, and where there is no lockfile there are no
// recorded markers either — so the report must contribute nothing rather than
// fault.
func TestReleaseConventionsToleratesAnAbsentLockfile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	var diag bytes.Buffer
	if err := RunRelease(t.Context(), rw, filepath.Join(root, "release.yaml"), nil, io.Discard, &diag, nil); err == nil {
		t.Fatal("the fixture has no Release, so the pass cannot succeed")
	}
	if strings.Contains(diag.String(), "deprecation:") {
		t.Errorf("an absent lockfile has no recorded markers:\n%s", diag.String())
	}
}
