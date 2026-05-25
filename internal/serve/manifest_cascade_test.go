// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseYAML returns canonical Release YAML text with the supplied
// metadata.name. The aggregate / manifest references default to
// matching-name siblings; tests that don't load downstream entities
// can ignore these.
func releaseYAML(name string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: ` + name + `
  version: "1.0.0"
spec:
  aggregate: aggregate
  manifest: manifest
`
}

// writeRelease writes a Release YAML at the supplied path under
// workspaceRoot. Creates parent dirs as needed.
func writeRelease(t *testing.T, workspaceRoot, relPath, name string) string {
	t.Helper()
	abs := filepath.Join(workspaceRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(releaseYAML(name)), 0o644); err != nil {
		t.Fatalf("write release: %v", err)
	}
	return abs
}

func TestManifestCascadeStep1AuthoringReleaseWins(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "configured.yaml", "release/configured")
	writeRelease(t, ws, "other.yaml", "release/other") // second candidate so Step 2 wouldn't win
	wsCfg := workspaceconfig.Default()
	wsCfg.Build.AuthoringRelease = "configured.yaml"

	rel, attempt := runManifestCascade(ws, wsCfg, io.Discard)
	if rel == nil {
		t.Fatalf("expected Step 1 to resolve a Release; got nil")
	}
	if attempt.ResolvedStep != 1 {
		t.Errorf("ResolvedStep = %d; want 1", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/configured" {
		t.Errorf("Release name = %q; want %q", rel.Metadata.Name, "release/configured")
	}
}

func TestManifestCascadeStep1AuthoringReleaseRelativeAndAbsolute(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	abs := writeRelease(t, ws, "abs-target.yaml", "release/abs")

	// Relative path resolution
	wsCfgRel := workspaceconfig.Default()
	wsCfgRel.Build.AuthoringRelease = "abs-target.yaml"
	rel, attempt := runManifestCascade(ws, wsCfgRel, io.Discard)
	if rel == nil || attempt.ResolvedStep != 1 {
		t.Errorf("relative path: ResolvedStep = %d, rel = %v", attempt.ResolvedStep, rel)
	}

	// Absolute path resolution
	wsCfgAbs := workspaceconfig.Default()
	wsCfgAbs.Build.AuthoringRelease = abs
	rel2, attempt2 := runManifestCascade(ws, wsCfgAbs, io.Discard)
	if rel2 == nil || attempt2.ResolvedStep != 1 {
		t.Errorf("absolute path: ResolvedStep = %d, rel = %v", attempt2.ResolvedStep, rel2)
	}
}

func TestManifestCascadeStep1AuthoringReleaseFileMissingFallsThroughToStep2(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "only.yaml", "release/only")
	wsCfg := workspaceconfig.Default()
	wsCfg.Build.AuthoringRelease = "missing.yaml"

	var stderr bytes.Buffer
	rel, attempt := runManifestCascade(ws, wsCfg, &stderr)
	if rel == nil {
		t.Fatalf("expected fall-through to Step 2; got nil")
	}
	if attempt.ResolvedStep != 2 {
		t.Errorf("ResolvedStep = %d; want 2", attempt.ResolvedStep)
	}
	if !attempt.AuthoringReleaseConfigured {
		t.Errorf("AuthoringReleaseConfigured should be true")
	}
	if attempt.AuthoringReleaseLoadError == nil {
		t.Errorf("AuthoringReleaseLoadError should be non-nil")
	}
	if !strings.Contains(stderr.String(), "step 1") {
		t.Errorf("expected stderr warning about step 1; got: %q", stderr.String())
	}
}

func TestManifestCascadeStep2SingleReleaseWins(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "releases/only.yaml", "release/single")

	rel, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel == nil {
		t.Fatalf("expected Step 2 to resolve; got nil")
	}
	if attempt.ResolvedStep != 2 {
		t.Errorf("ResolvedStep = %d; want 2", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/single" {
		t.Errorf("Release name = %q; want %q", rel.Metadata.Name, "release/single")
	}
	if attempt.ReleaseCandidatesFound != 1 {
		t.Errorf("ReleaseCandidatesFound = %d; want 1", attempt.ReleaseCandidatesFound)
	}
}

func TestManifestCascadeStep2WalkExcludesIgnoredDirs(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	// Release file under .git/ — should be ignored
	writeRelease(t, ws, ".git/buried.yaml", "release/git")
	// Release file under node_modules/ — should be ignored
	writeRelease(t, ws, "node_modules/dep/buried.yaml", "release/node")
	// Single visible Release at top-level
	writeRelease(t, ws, "visible.yaml", "release/visible")

	rel, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel == nil {
		t.Fatalf("expected the visible Release to resolve")
	}
	if attempt.ReleaseCandidatesFound != 1 {
		t.Errorf("ReleaseCandidatesFound = %d; want 1 (ignored dirs not counted)", attempt.ReleaseCandidatesFound)
	}
	if rel.Metadata.Name != "release/visible" {
		t.Errorf("Release name = %q; want %q", rel.Metadata.Name, "release/visible")
	}
}

func TestManifestCascadeStep3MarkerFileWins(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "alpha.yaml", "release/alpha")
	writeRelease(t, ws, "beta.yaml", "release/beta")
	writeRelease(t, ws, "gamma.yaml", "release/gamma")
	// Marker points at beta — but there are 3 Releases so Step 2 misses;
	// Step 3 selects beta per the marker.
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("beta.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	rel, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel == nil {
		t.Fatalf("expected Step 3 to resolve via marker")
	}
	if attempt.ResolvedStep != 3 {
		t.Errorf("ResolvedStep = %d; want 3", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/beta" {
		t.Errorf("Release name = %q; want %q", rel.Metadata.Name, "release/beta")
	}
	if !attempt.MarkerFilePresent {
		t.Errorf("MarkerFilePresent should be true")
	}
	if attempt.MarkerFileTarget != "beta.yaml" {
		t.Errorf("MarkerFileTarget = %q; want %q", attempt.MarkerFileTarget, "beta.yaml")
	}
}

func TestManifestCascadeStep3MarkerFileMissingTargetFallsThroughToStep4(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "alpha.yaml", "release/alpha")
	writeRelease(t, ws, "beta.yaml", "release/beta")
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("nonexistent.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	var stderr bytes.Buffer
	rel, attempt := runManifestCascade(ws, workspaceconfig.Default(), &stderr)
	if rel != nil {
		t.Fatalf("expected fall-through to Step 4 (nil Release); got %v", rel)
	}
	if attempt.ResolvedStep != 0 {
		t.Errorf("ResolvedStep = %d; want 0 (Step 4 fallback)", attempt.ResolvedStep)
	}
	if !attempt.MarkerFilePresent {
		t.Errorf("MarkerFilePresent should be true")
	}
	if attempt.MarkerFileLoadError == nil {
		t.Errorf("MarkerFileLoadError should be non-nil")
	}
	if !strings.Contains(stderr.String(), "step 3") {
		t.Errorf("expected stderr warning about step 3; got: %q", stderr.String())
	}
}

func TestManifestCascadeStep4FallbackNilActiveRelease(t *testing.T) {
	t.Parallel()
	ws := t.TempDir() // empty workspace

	rel, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel != nil {
		t.Fatalf("expected nil Release on empty workspace; got %v", rel)
	}
	if attempt.ResolvedStep != 0 {
		t.Errorf("ResolvedStep = %d; want 0", attempt.ResolvedStep)
	}
	if attempt.ReleaseCandidatesFound != 0 {
		t.Errorf("ReleaseCandidatesFound = %d; want 0", attempt.ReleaseCandidatesFound)
	}
	if attempt.MarkerFilePresent {
		t.Errorf("MarkerFilePresent should be false")
	}
}

func TestManifestCascadePrecedenceStep1OverStep2AndStep3(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "configured.yaml", "release/configured")
	writeRelease(t, ws, "other.yaml", "release/other")
	// Marker file pointing at yet another Release
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("other.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	wsCfg := workspaceconfig.Default()
	wsCfg.Build.AuthoringRelease = "configured.yaml"

	rel, attempt := runManifestCascade(ws, wsCfg, io.Discard)
	if attempt.ResolvedStep != 1 {
		t.Errorf("Step 1 should win over Step 2 + Step 3; ResolvedStep = %d", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/configured" {
		t.Errorf("Release name = %q; want %q (Step 1 picks)", rel.Metadata.Name, "release/configured")
	}
}

func TestManifestCascadePrecedenceStep2OverStep3(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "only.yaml", "release/only")
	// Marker points at a different Release; Step 2 should still win
	// because there's exactly one Release in the workspace
	otherPath := writeRelease(t, ws, "other-dir/elsewhere.yaml", "release/elsewhere")
	_ = otherPath
	// Wait — the second writeRelease now makes 2 candidates, breaking Step 2.
	// Remove the second to keep "exactly one" + a marker pointing elsewhere.
	if err := os.Remove(otherPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Marker points at a path that doesn't exist as a Release
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("other-dir/elsewhere.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	rel, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if attempt.ResolvedStep != 2 {
		t.Errorf("Step 2 should win over Step 3; ResolvedStep = %d", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/only" {
		t.Errorf("Release name = %q; want %q (Step 2 picks the single workspace Release)", rel.Metadata.Name, "release/only")
	}
}

func TestCascadeDiagnosticMessageOnExhaustion(t *testing.T) {
	t.Parallel()
	// Construct a Server with a Step-4 cascade attempt
	srv := &Server{
		cascadeAttempt: cascadeAttempt{
			ResolvedStep:               0,
			AuthoringReleaseConfigured: false,
			ReleaseCandidatesFound:     3,
			MarkerFilePresent:          false,
		},
	}
	msg := srv.cascadeDiagnosticMessage()
	for _, sub := range []string{
		"release not resolved",
		"no authoring_release configured",
		"3 Release file(s)",
		"no .aggrega/active-release marker",
	} {
		if !strings.Contains(msg, sub) {
			t.Errorf("diagnostic message missing %q; got: %q", sub, msg)
		}
	}
}

func TestCascadeDiagnosticMessageWithMarkerLoadError(t *testing.T) {
	t.Parallel()
	srv := &Server{
		cascadeAttempt: cascadeAttempt{
			ResolvedStep:               0,
			AuthoringReleaseConfigured: true,
			AuthoringReleaseLoadError:  os.ErrNotExist,
			ReleaseCandidatesFound:     2,
			MarkerFilePresent:          true,
			MarkerFileTarget:           "stale.yaml",
			MarkerFileLoadError:        os.ErrNotExist,
		},
	}
	msg := srv.cascadeDiagnosticMessage()
	for _, sub := range []string{
		"release not resolved",
		"authoring_release configured but failed to load",
		"2 Release file(s)",
		`marker target "stale.yaml" failed to load`,
	} {
		if !strings.Contains(msg, sub) {
			t.Errorf("diagnostic message missing %q; got: %q", sub, msg)
		}
	}
}

func TestCascadeDiagnosticMessageWhenResolved(t *testing.T) {
	t.Parallel()
	srv := &Server{
		cascadeAttempt: cascadeAttempt{ResolvedStep: 1},
	}
	msg := srv.cascadeDiagnosticMessage()
	if !strings.Contains(msg, "release resolved") {
		t.Errorf("expected 'release resolved' framing for non-zero ResolvedStep; got: %q", msg)
	}
}
