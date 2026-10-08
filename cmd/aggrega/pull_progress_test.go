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
// surface forced on over a non-terminal sink and asserts every pull step is
// named on exactly one plain line on the diagnostic sink (stderr), with no escape
// byte, while the user-facing "Pull complete" summary stays on the output sink
// (stdout). It also confirms stdout carries no progress lines, mirroring the
// build's stderr-only discipline.
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
	labels := pullProgressSpine().labels
	for label, n := range stepLineCounts(errStr, labels) {
		if n != 1 {
			t.Errorf("pull step %q is named on %d lines on stderr, want exactly 1; got %q", label, n, errStr)
		}
	}
	if strings.ContainsRune(errStr, '\r') {
		t.Errorf("forced static pull surface must not redraw in place; got %q", errStr)
	}
	if i := strings.IndexByte(errStr, 0x1b); i >= 0 {
		t.Errorf("forced static pull surface carries an escape byte at offset %d; got %q", i, errStr)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Pull complete") {
		t.Errorf("expected the pull summary on stdout; got %q", outStr)
	}
	for label, n := range stepLineCounts(outStr, labels) {
		if n != 0 {
			t.Errorf("stdout must carry no progress lines; step %q is named on %d; got %q", label, n, outStr)
		}
	}
	if strings.ContainsRune(outStr, '\r') {
		t.Errorf("stdout must carry no redraw; got %q", outStr)
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

	for label, n := range stepLineCounts(stderr.String(), pullProgressSpine().labels) {
		if n != 0 {
			t.Errorf("auto mode on a non-TTY must not print step lines; step %q is named on %d; got %q", label, n, stderr.String())
		}
	}
	if !strings.Contains(stdout.String(), "Pull complete") {
		t.Errorf("expected the pull summary on stdout; got %q", stdout.String())
	}
}
