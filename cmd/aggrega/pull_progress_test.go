// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// TestPull_ProgressSurface_ForcedStatic drives a real pull with the progress
// surface forced on over a non-terminal sink and asserts the pull phases appear
// as plain static lines on the diagnostic sink (stderr), while the user-facing
// "Pull complete" summary stays on the output sink (stdout). It also confirms
// stdout carries no progress lines, mirroring the build's stderr-only discipline.
func TestPull_ProgressSurface_ForcedStatic(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return false }

	root := t.TempDir()
	releasePath := authoringFixture(t, root)

	var stdout, stderr bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		diag:        buildDiagOptions{progress: true},
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("pullRun: %v (stderr=%q)", err, stderr.String())
	}

	errStr := stderr.String()
	for _, label := range []string{"loading catalog...", "resolving dependencies...", "writing lockfile..."} {
		if !strings.Contains(errStr, label) {
			t.Errorf("expected pull phase line %q on stderr; got %q", label, errStr)
		}
	}
	if strings.ContainsRune(errStr, '\r') {
		t.Errorf("forced static pull surface must not redraw in place; got %q", errStr)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Pull complete") {
		t.Errorf("expected the pull summary on stdout; got %q", outStr)
	}
	if strings.Contains(outStr, "...") || strings.ContainsRune(outStr, '\r') {
		t.Errorf("stdout must carry no progress lines; got %q", outStr)
	}
}

// TestPull_ProgressSurface_AutoNonTTYSilent confirms the auto default: with no
// --progress and a non-terminal sink, pull emits no progress surface on stderr,
// only its diagnostic notices (none here) and the stdout summary.
func TestPull_ProgressSurface_AutoNonTTYSilent(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return false }

	root := t.TempDir()
	releasePath := authoringFixture(t, root)

	var stdout, stderr bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("pullRun: %v (stderr=%q)", err, stderr.String())
	}

	if strings.Contains(stderr.String(), "loading catalog...") {
		t.Errorf("auto mode on a non-TTY must not print phase lines; got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Pull complete") {
		t.Errorf("expected the pull summary on stdout; got %q", stdout.String())
	}
}
