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
  environment: testing
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api: {}
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
	writeRelease(t, ws, "other.yaml", "release/other") // second candidate so Step 3 wouldn't win
	wsCfg := workspaceconfig.Default()
	wsCfg.Build.AuthoringRelease = "configured.yaml"

	rel, _, attempt := runManifestCascade(ws, wsCfg, io.Discard)
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
	rel, _, attempt := runManifestCascade(ws, wsCfgRel, io.Discard)
	if rel == nil || attempt.ResolvedStep != 1 {
		t.Errorf("relative path: ResolvedStep = %d, rel = %v", attempt.ResolvedStep, rel)
	}

	// Absolute path resolution
	wsCfgAbs := workspaceconfig.Default()
	wsCfgAbs.Build.AuthoringRelease = abs
	rel2, _, attempt2 := runManifestCascade(ws, wsCfgAbs, io.Discard)
	if rel2 == nil || attempt2.ResolvedStep != 1 {
		t.Errorf("absolute path: ResolvedStep = %d, rel = %v", attempt2.ResolvedStep, rel2)
	}
}

func TestManifestCascadeStep1AuthoringReleaseFileMissingFallsThroughToStep3(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "only.yaml", "release/only")
	wsCfg := workspaceconfig.Default()
	wsCfg.Build.AuthoringRelease = "missing.yaml"

	var stderr bytes.Buffer
	rel, _, attempt := runManifestCascade(ws, wsCfg, &stderr)
	if rel == nil {
		t.Fatalf("expected fall-through past the absent marker to Step 3; got nil")
	}
	if attempt.ResolvedStep != 3 {
		t.Errorf("ResolvedStep = %d; want 3", attempt.ResolvedStep)
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

func TestManifestCascadeStep3SingleReleaseWins(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "releases/only.yaml", "release/single")

	rel, _, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel == nil {
		t.Fatalf("expected Step 3 to resolve; got nil")
	}
	if attempt.ResolvedStep != 3 {
		t.Errorf("ResolvedStep = %d; want 3", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/single" {
		t.Errorf("Release name = %q; want %q", rel.Metadata.Name, "release/single")
	}
	if attempt.ReleaseCandidatesFound != 1 {
		t.Errorf("ReleaseCandidatesFound = %d; want 1", attempt.ReleaseCandidatesFound)
	}
}

func TestManifestCascadeStep3WalkExcludesIgnoredDirs(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	// Release file under .git/ — should be ignored
	writeRelease(t, ws, ".git/buried.yaml", "release/git")
	// Release file under node_modules/ — should be ignored
	writeRelease(t, ws, "node_modules/dep/buried.yaml", "release/node")
	// Single visible Release at top-level
	writeRelease(t, ws, "visible.yaml", "release/visible")

	rel, _, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
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

func TestManifestCascadeStep2MarkerFileWins(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "alpha.yaml", "release/alpha")
	writeRelease(t, ws, "beta.yaml", "release/beta")
	writeRelease(t, ws, "gamma.yaml", "release/gamma")
	// Step 2 selects beta per the marker, before the walk sees the three
	// candidates it could not have chosen between anyway.
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("beta.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	rel, _, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel == nil {
		t.Fatalf("expected Step 2 to resolve via marker")
	}
	if attempt.ResolvedStep != 2 {
		t.Errorf("ResolvedStep = %d; want 2", attempt.ResolvedStep)
	}
	if attempt.ReleaseCandidatesFound != 0 {
		t.Errorf("ReleaseCandidatesFound = %d; want 0 (the marker resolves before the walk runs)", attempt.ReleaseCandidatesFound)
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

func TestManifestCascadeStep2MarkerFileMissingTargetFallsThroughToStep4(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	// Two Releases, so the walk Step 2 falls through to cannot resolve
	// either: the cascade exhausts into the Step 4 fallback.
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
	rel, _, attempt := runManifestCascade(ws, workspaceconfig.Default(), &stderr)
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
	if attempt.ReleaseCandidatesFound != 2 {
		t.Errorf("ReleaseCandidatesFound = %d; want 2 (the walk ran and found both)", attempt.ReleaseCandidatesFound)
	}
	if !strings.Contains(stderr.String(), "step 2") {
		t.Errorf("expected stderr warning about step 2; got: %q", stderr.String())
	}
}

// TestManifestCascadeStep2MarkerFileMissingTargetFallsThroughToStep3 pins the
// fall-through a stale marker now takes: a marker naming a file that will not
// load does not strand the workspace at the fallback while a single Release is
// sitting there. The walk runs after the marker fails and resolves its one
// candidate.
func TestManifestCascadeStep2MarkerFileMissingTargetFallsThroughToStep3(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "only.yaml", "release/only")
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("nonexistent.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	var stderr bytes.Buffer
	rel, _, attempt := runManifestCascade(ws, workspaceconfig.Default(), &stderr)
	if rel == nil {
		t.Fatalf("expected fall-through to Step 3; got nil")
	}
	if attempt.ResolvedStep != 3 {
		t.Errorf("ResolvedStep = %d; want 3", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/only" {
		t.Errorf("Release name = %q; want %q", rel.Metadata.Name, "release/only")
	}
	if attempt.MarkerFileLoadError == nil {
		t.Errorf("MarkerFileLoadError should be non-nil (the marker target does not exist)")
	}
	if !strings.Contains(stderr.String(), "step 2") {
		t.Errorf("expected stderr warning about step 2; got: %q", stderr.String())
	}
}

// TestManifestCascadeStep3SingleReleaseLoadFailureWarns covers the remaining
// load-failure warning: the walk's one candidate is a Release the loader
// rejects, so the cascade exhausts and says which step could not load it.
func TestManifestCascadeStep3SingleReleaseLoadFailureWarns(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	// A Release header the walk counts, over a body the loader rejects.
	broken := "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nspec: \"not-an-object\"\n"
	if err := os.WriteFile(filepath.Join(ws, "broken.yaml"), []byte(broken), 0o644); err != nil {
		t.Fatalf("write broken release: %v", err)
	}

	var stderr bytes.Buffer
	rel, _, attempt := runManifestCascade(ws, workspaceconfig.Default(), &stderr)
	if rel != nil {
		t.Fatalf("expected no Release from an unloadable candidate; got %v", rel)
	}
	if attempt.ResolvedStep != 0 {
		t.Errorf("ResolvedStep = %d; want 0", attempt.ResolvedStep)
	}
	if attempt.SingleReleaseLoadError == nil {
		t.Errorf("SingleReleaseLoadError should be non-nil")
	}
	if !strings.Contains(stderr.String(), "step 3") {
		t.Errorf("expected stderr warning about step 3; got: %q", stderr.String())
	}
}

func TestManifestCascadeStep4FallbackNilActiveRelease(t *testing.T) {
	t.Parallel()
	ws := t.TempDir() // empty workspace

	rel, _, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
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

	rel, _, attempt := runManifestCascade(ws, wsCfg, io.Discard)
	if attempt.ResolvedStep != 1 {
		t.Errorf("Step 1 should win over Step 2 + Step 3; ResolvedStep = %d", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/configured" {
		t.Errorf("Release name = %q; want %q (Step 1 picks)", rel.Metadata.Name, "release/configured")
	}
	// The attempt log still records the marker the config outranked, so the
	// record describes the workspace rather than only the winning step.
	if !attempt.MarkerFilePresent {
		t.Errorf("MarkerFilePresent should be true even though Step 1 won")
	}
	if attempt.MarkerFileTarget != "other.yaml" {
		t.Errorf("MarkerFileTarget = %q; want %q", attempt.MarkerFileTarget, "other.yaml")
	}
}

// TestManifestCascadePrecedenceStep2MarkerOverStep3SingleRelease pins the
// rule that an operator's explicit marker outranks the single-Release
// inference. The fixture is built so the two steps genuinely disagree: the
// marker names a Release in a hidden directory, which the walk excludes, so
// the walk's only candidate is a different file. The second half removes the
// marker and re-runs, which is what proves the walk really would have picked
// that other file — without it the first half could pass on a workspace where
// the marker and the walk happen to agree.
func TestManifestCascadePrecedenceStep2MarkerOverStep3SingleRelease(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRelease(t, ws, "only.yaml", "release/only")
	writeRelease(t, ws, ".hidden/marked.yaml", "release/marked")
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	markerPath := filepath.Join(markerDir, "active-release")
	if err := os.WriteFile(markerPath, []byte(".hidden/marked.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	rel, path, attempt := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel == nil {
		t.Fatalf("expected the marker's Release to resolve; got nil")
	}
	if attempt.ResolvedStep != 2 {
		t.Errorf("the marker should win over the single-Release heuristic; ResolvedStep = %d, want 2", attempt.ResolvedStep)
	}
	if rel.Metadata.Name != "release/marked" {
		t.Errorf("Release name = %q; want %q (the marker's target)", rel.Metadata.Name, "release/marked")
	}
	if want := filepath.Join(ws, ".hidden", "marked.yaml"); path != want {
		t.Errorf("resolved path = %q; want %q", path, want)
	}
	if !attempt.MarkerFilePresent || attempt.MarkerFileTarget != ".hidden/marked.yaml" {
		t.Errorf("marker record = (%v, %q); want (true, %q)", attempt.MarkerFilePresent, attempt.MarkerFileTarget, ".hidden/marked.yaml")
	}

	// Remove the marker and the walk resolves its one visible candidate —
	// which is the file the marker outranked above.
	if err := os.Remove(markerPath); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	rel2, _, attempt2 := runManifestCascade(ws, workspaceconfig.Default(), io.Discard)
	if rel2 == nil {
		t.Fatalf("expected the walk's single candidate to resolve once the marker is gone; got nil")
	}
	if attempt2.ResolvedStep != 3 {
		t.Errorf("ResolvedStep = %d; want 3 (single-Release heuristic)", attempt2.ResolvedStep)
	}
	if rel2.Metadata.Name != "release/only" {
		t.Errorf("Release name = %q; want %q (the walk's candidate)", rel2.Metadata.Name, "release/only")
	}
	if attempt2.ReleaseCandidatesFound != 1 {
		t.Errorf("ReleaseCandidatesFound = %d; want 1 (the hidden directory is excluded)", attempt2.ReleaseCandidatesFound)
	}
	if attempt2.MarkerFilePresent {
		t.Errorf("MarkerFilePresent should be false once the marker is removed")
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

// TestCascadeAttemptFullTraceReadsInExecutionOrder pins the trace's step
// labelling: an operator reading it top to bottom sees the steps in the order
// the cascade ran them, each named as well as numbered.
func TestCascadeAttemptFullTraceReadsInExecutionOrder(t *testing.T) {
	t.Parallel()
	srv := &Server{
		cascadeAttempt: cascadeAttempt{
			ResolvedStep:           0,
			ReleaseCandidatesFound: 2,
			MarkerFilePresent:      true,
			MarkerFileTarget:       "stale.yaml",
			MarkerFileLoadError:    os.ErrNotExist,
		},
	}
	trace := srv.cascadeAttemptFullTrace()
	prev := -1
	for _, want := range []string{
		"  step 1 (build.authoring_release",
		"  step 2 (.aggrega/active-release marker)",
		"  step 3 (single-Release heuristic)",
		"  step 4 (Aggregate-only fallback)",
	} {
		at := strings.Index(trace, want)
		if at < 0 {
			t.Fatalf("trace missing %q; got:\n%s", want, trace)
		}
		if at <= prev {
			t.Errorf("%q appears out of execution order; got:\n%s", want, trace)
		}
		prev = at
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
