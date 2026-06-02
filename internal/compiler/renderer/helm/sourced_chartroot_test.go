// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart/loader"
)

const minimalChartYAML = "apiVersion: v2\nname: mychart\nversion: 0.1.0\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveChartRoot_NestedRealLayout pins the real OCI-chart layout: an
// extracted chart sits under a single <chartname>/ directory, so the extracted
// directory is one level above Chart.yaml. resolveChartRoot descends to it.
//
// Non-vacuous: loader.Load on the extracted directory itself FAILS (the bug this
// fixes), while loader.Load on the resolved root SUCCEEDS — so the test fails if
// the resolution is removed.
func TestResolveChartRoot_NestedRealLayout(t *testing.T) {
	t.Parallel()
	extracted := filepath.Join(t.TempDir(), "extracted")
	writeFile(t, filepath.Join(extracted, "mychart", "Chart.yaml"), minimalChartYAML)

	// The bug: loading the extracted dir directly fails (Chart.yaml is nested).
	if _, err := loader.Load(extracted); err == nil {
		t.Fatal("precondition: loader.Load on the extracted dir must FAIL for this test to be non-vacuous (Chart.yaml is under <chartname>/)")
	}

	root, err := resolveChartRoot(extracted)
	if err != nil {
		t.Fatalf("resolveChartRoot: %v", err)
	}
	if root != filepath.Join(extracted, "mychart") {
		t.Errorf("root = %q; want %q", root, filepath.Join(extracted, "mychart"))
	}
	if _, err := loader.Load(root); err != nil {
		t.Errorf("loader.Load on the resolved root should succeed; got %v", err)
	}
}

// TestResolveChartRoot_FlatLayout pins the flat case: Chart.yaml directly at the
// given path returns that path unchanged (local-path sources, and archives that
// extract flat).
func TestResolveChartRoot_FlatLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Chart.yaml"), minimalChartYAML)

	root, err := resolveChartRoot(dir)
	if err != nil {
		t.Fatalf("resolveChartRoot: %v", err)
	}
	if root != dir {
		t.Errorf("root = %q; want %q (flat layout returns the path unchanged)", root, dir)
	}
}

// TestResolveChartRoot_NoChartYAMLHaltsLoud pins the zero-match Loud Guardrail:
// an extracted directory with no Chart.yaml anywhere halts, naming the cause.
func TestResolveChartRoot_NoChartYAMLHaltsLoud(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "subdir", "README.md"), "not a chart\n")

	_, err := resolveChartRoot(dir)
	if err == nil {
		t.Fatal("expected an error when no Chart.yaml is present")
	}
	if !strings.Contains(err.Error(), "no Chart.yaml") {
		t.Errorf("error should name the missing Chart.yaml; got %v", err)
	}
}

// TestResolveChartRoot_AmbiguousHaltsLoud pins the multi-match Loud Guardrail:
// two top-level subdirectories each carrying Chart.yaml is ambiguous — halt,
// never silently pick the first.
func TestResolveChartRoot_AmbiguousHaltsLoud(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "Chart.yaml"), minimalChartYAML)
	writeFile(t, filepath.Join(dir, "beta", "Chart.yaml"), minimalChartYAML)

	_, err := resolveChartRoot(dir)
	if err == nil {
		t.Fatal("expected an ambiguity error when multiple subdirectories carry Chart.yaml")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should flag ambiguity; got %v", err)
	}
}

// TestResolveChartRoot_ArchivePassthrough pins that a file (an archive path) is
// returned unchanged — loader.Load handles .tgz directly.
func TestResolveChartRoot_ArchivePassthrough(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "chart.tgz")
	writeFile(t, f, "fake archive bytes")

	root, err := resolveChartRoot(f)
	if err != nil {
		t.Fatalf("resolveChartRoot on a file: %v", err)
	}
	if root != f {
		t.Errorf("root = %q; want the archive path %q unchanged", root, f)
	}
}
