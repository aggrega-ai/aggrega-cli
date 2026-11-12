// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"testing"
)

// TestCollectRenderedFiles_VerbatimBytes proves the collector writes each
// file's serialized bytes through verbatim, keyed by path — no re-serialization,
// so the on-disk bytes are exactly what the guest emitted.
func TestCollectRenderedFiles_VerbatimBytes(t *testing.T) {
	t.Parallel()
	files := []RenderedFile{
		{Path: "templates/deployment.yaml", Content: []byte("kind: Deployment\nmetadata:\n  name: api\n")},
		{Path: "Chart.yaml", Content: []byte("apiVersion: v2\nname: api\nversion: 1.4.2\n")},
	}
	got, err := collectRenderedFiles(files)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != len(files) {
		t.Fatalf("file count mismatch: got %d, want %d", len(got), len(files))
	}
	for _, f := range files {
		if string(got[f.Path]) != string(f.Content) {
			t.Errorf("path %q bytes not verbatim:\n  want: %q\n  got:  %q", f.Path, f.Content, got[f.Path])
		}
	}
}

// TestCollectRenderedFiles_DuplicatePath asserts the host refuses two files
// declaring the same path rather than silently letting the second clobber the
// first — a guest contract violation surfaces loudly.
func TestCollectRenderedFiles_DuplicatePath(t *testing.T) {
	t.Parallel()
	files := []RenderedFile{
		{Path: "values.yaml", Content: []byte("a: 1\n")},
		{Path: "values.yaml", Content: []byte("b: 2\n")},
	}
	if _, err := collectRenderedFiles(files); err == nil {
		t.Fatal("expected a duplicate-path error, got nil")
	}
}

// TestCollectRenderedFiles_Empty asserts an empty file list collects to an
// empty (non-nil) map rather than an error — a guest may emit no files.
func TestCollectRenderedFiles_Empty(t *testing.T) {
	t.Parallel()
	got, err := collectRenderedFiles(nil)
	if err != nil {
		t.Fatalf("collect empty: %v", err)
	}
	if got == nil {
		t.Fatal("expected a non-nil empty map")
	}
	if len(got) != 0 {
		t.Fatalf("expected zero files, got %d", len(got))
	}
}
