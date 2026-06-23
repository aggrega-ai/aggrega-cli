// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestBuildDetail_LockfilePresent_NoPullNote pins the --detail no-pull path:
// when a lockfile already exists, build does not pull, so there is no
// resolution to report. --detail prints a one-line stderr note (rather than
// silence or a fabricated tier) and reports the lockfile pre-existed; without
// --detail the path is silent.
func TestBuildDetail_LockfilePresent_NoPullNote(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join(root, "release.yaml")
	writeFixture(t, rel, "kind: Release\n")
	// A present lockfile makes acquireLockfile take the no-pull branch; its
	// content is irrelevant (only os.Stat is consulted there).
	writeFixture(t, lockfile.PathFor(rel), "{}\n")

	var noteBuf bytes.Buffer
	pre, acqErr := acquireLockfile(nil, rel, []string{root}, workspaceconfig.Config{}, true, &noteBuf)
	if acqErr != nil {
		t.Fatalf("acquireLockfile (detail=true): %v", acqErr)
	}
	if !pre {
		t.Fatal("expected lockfilePreexisted=true when the lockfile is present")
	}
	if !strings.Contains(noteBuf.String(), "resolution detail is a pull-time surface") {
		t.Errorf("expected the no-pull note on the diagnostic sink, got %q", noteBuf.String())
	}

	var silentBuf bytes.Buffer
	if _, silentErr := acquireLockfile(nil, rel, []string{root}, workspaceconfig.Config{}, false, &silentBuf); silentErr != nil {
		t.Fatalf("acquireLockfile (detail=false): %v", silentErr)
	}
	if strings.TrimSpace(silentBuf.String()) != "" {
		t.Errorf("expected no diagnostic output without --detail, got %q", silentBuf.String())
	}
}
