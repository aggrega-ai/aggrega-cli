// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"testing"
)

// TestCommitHash_ReturnsUnknownForNonGitDir pins the graceful-degradation
// contract: when commitHash runs against a directory that is not a git
// checkout (or when git is unavailable), it returns the literal string
// "unknown" rather than panicking or surfacing the underlying error.
//
// The report's commit-hash field is informational; an "unknown" value is
// preferable to a build failure when developers run the mutator outside
// a git checkout (CI ephemeral runners with bare source bundles, etc.).
func TestCommitHash_ReturnsUnknownForNonGitDir(t *testing.T) {
	t.Parallel()
	// A fresh tmpdir is not a git checkout.
	dir := t.TempDir()
	got := commitHash(dir)
	if got != "unknown" {
		t.Errorf("commitHash(%q) = %q; want %q (non-git dir should degrade gracefully)", dir, got, "unknown")
	}
}
