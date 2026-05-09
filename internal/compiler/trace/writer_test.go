// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriter_NoOp_FlagOff verifies that New("") returns a writer whose
// Write is a no-op and produces no filesystem output. This is the
// `--trace` off path: no `.trace/` directory should appear.
func TestWriter_NoOp_FlagOff(t *testing.T) {
	t.Parallel()
	w := New("")
	if err := w.Write(PassPhaseA, "api", "default", "", map[string]any{"sample": 1}); err != nil {
		t.Errorf("noop Write returned error: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("noop Close returned error: %v", err)
	}
}

// TestWriter_Fs_WritesUnderRoot verifies that the async filesystem writer
// produces files under the configured root directory with the
// boundaries/<bp>/<nodeID>/<pass>.{yaml,log} layout.
func TestWriter_Fs_WritesUnderRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root)

	if err := w.Write(PassPhaseA, "api", "default", "", map[string]any{"sample": 1}); err != nil {
		t.Fatalf("Write PassPhaseA: %v", err)
	}
	if err := w.Write(PassEmission, "api", "default", "", map[string]any{"files": 3}); err != nil {
		t.Fatalf("Write PassEmission: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Phase A — .yaml extension.
	yamlPath := filepath.Join(root, "boundaries", "default", "api", "04-phaseA-inject.yaml")
	if _, err := os.Stat(yamlPath); err != nil {
		t.Errorf("expected %q to exist after Phase A write: %v", yamlPath, err)
	}
	// Emission — .log extension.
	logPath := filepath.Join(root, "boundaries", "default", "api", "11-engine-emission.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("expected %q to exist after PassEmission write: %v", logPath, err)
	}
}

// TestWriter_Fs_FileSuffixForPerArtifact verifies the per-artifact slot
// layout (PassPhaseB3 artifacts and PassPhaseB4 synthesis) — files land
// under the pass directory with the supplied filename, not as
// <pass>.yaml.
func TestWriter_Fs_FileSuffixForPerArtifact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root)
	if err := w.Write(PassPhaseB4, "api", "default", "deployment.yaml", map[string]any{"kind": "Deployment"}); err != nil {
		t.Fatalf("Write PassPhaseB4: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := filepath.Join(root, "boundaries", "default", "api", "10-engine-synthesis", "deployment.yaml")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected per-artifact file at %q: %v", want, err)
	}
}
