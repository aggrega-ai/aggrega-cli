// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// captureStderr redirects os.Stderr for the duration of fn and returns what fn
// wrote. Teardown runs in a defer so a panic in fn still restores os.Stderr,
// unblocks the reader goroutine, and drains the buffer.
func captureStderr(fn func()) (out string) {
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stderr = w

	done := make(chan struct{})
	var captured strings.Builder
	go func() {
		buf := make([]byte, 1024)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				captured.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		close(done)
	}()

	defer func() {
		os.Stderr = orig
		w.Close()
		<-done
		r.Close()
		out = captured.String()
	}()

	fn()
	return
}

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

	var pre bool
	var acqErr error
	noteOut := captureStderr(func() {
		pre, acqErr = acquireLockfile(nil, rel, []string{root}, workspaceconfig.Config{}, true)
	})
	if acqErr != nil {
		t.Fatalf("acquireLockfile (detail=true): %v", acqErr)
	}
	if !pre {
		t.Fatal("expected lockfilePreexisted=true when the lockfile is present")
	}
	if !strings.Contains(noteOut, "resolution detail is a pull-time surface") {
		t.Errorf("expected the no-pull note on stderr, got %q", noteOut)
	}

	var silentErr error
	silentOut := captureStderr(func() {
		_, silentErr = acquireLockfile(nil, rel, []string{root}, workspaceconfig.Config{}, false)
	})
	if silentErr != nil {
		t.Fatalf("acquireLockfile (detail=false): %v", silentErr)
	}
	if strings.TrimSpace(silentOut) != "" {
		t.Errorf("expected no stderr output without --detail, got %q", silentOut)
	}
}
