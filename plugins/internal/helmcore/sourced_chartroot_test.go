// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// dirListing builds a directory source listing from root-relative entries. Each
// entry names a path and whether it is a directory, matching the shape the host
// enumerates the acquired source into.
func dirListing(root string, entries ...renderer.SourceEntry) *renderer.SourceListing {
	return &renderer.SourceListing{Root: root, RootIsDir: true, Entries: entries}
}

// TestResolveChartRootRel_NestedRealLayout pins the real OCI-chart layout: an
// extracted chart sits under a single <chartname>/ directory, so the chart root
// is one level below the source root. ResolveChartRootRel descends to it.
func TestResolveChartRootRel_NestedRealLayout(t *testing.T) {
	t.Parallel()
	listing := dirListing("/acquired/extracted",
		renderer.SourceEntry{Path: "mychart", IsDir: true},
		renderer.SourceEntry{Path: "mychart/Chart.yaml", IsDir: false},
	)
	rel, err := ResolveChartRootRel(listing)
	if err != nil {
		t.Fatalf("ResolveChartRootRel: %v", err)
	}
	if rel != "mychart" {
		t.Errorf("rel = %q; want %q", rel, "mychart")
	}
}

// TestResolveChartRootRel_FlatLayout pins the flat case: Chart.yaml directly at
// the source root resolves to the root itself (the empty relative path).
func TestResolveChartRootRel_FlatLayout(t *testing.T) {
	t.Parallel()
	listing := dirListing("/acquired/chart",
		renderer.SourceEntry{Path: "Chart.yaml", IsDir: false},
		renderer.SourceEntry{Path: "templates", IsDir: true},
		renderer.SourceEntry{Path: "templates/deployment.yaml", IsDir: false},
	)
	rel, err := ResolveChartRootRel(listing)
	if err != nil {
		t.Fatalf("ResolveChartRootRel: %v", err)
	}
	if rel != "" {
		t.Errorf("rel = %q; want %q (flat layout resolves to the root)", rel, "")
	}
}

// TestResolveChartRootRel_NoChartYAMLHaltsLoud pins the zero-match Loud Guardrail:
// a directory with no Chart.yaml anywhere halts, naming the cause.
func TestResolveChartRootRel_NoChartYAMLHaltsLoud(t *testing.T) {
	t.Parallel()
	listing := dirListing("/acquired/nochart",
		renderer.SourceEntry{Path: "subdir", IsDir: true},
		renderer.SourceEntry{Path: "subdir/README.md", IsDir: false},
	)
	_, err := ResolveChartRootRel(listing)
	if err == nil {
		t.Fatal("expected an error when no Chart.yaml is present")
	}
	if !strings.Contains(err.Error(), "no Chart.yaml") {
		t.Errorf("error should name the missing Chart.yaml; got %v", err)
	}
}

// TestResolveChartRootRel_AmbiguousHaltsLoud pins the multi-match Loud Guardrail:
// two top-level subdirectories each carrying Chart.yaml is ambiguous — halt,
// never silently pick the first.
func TestResolveChartRootRel_AmbiguousHaltsLoud(t *testing.T) {
	t.Parallel()
	listing := dirListing("/acquired/ambiguous",
		renderer.SourceEntry{Path: "alpha", IsDir: true},
		renderer.SourceEntry{Path: "alpha/Chart.yaml", IsDir: false},
		renderer.SourceEntry{Path: "beta", IsDir: true},
		renderer.SourceEntry{Path: "beta/Chart.yaml", IsDir: false},
	)
	_, err := ResolveChartRootRel(listing)
	if err == nil {
		t.Fatal("expected an ambiguity error when multiple subdirectories carry Chart.yaml")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should flag ambiguity; got %v", err)
	}
}

// TestResolveChartRootRel_ArchivePassthrough pins that a non-directory source (an
// archive) resolves to the root itself, so the guest packs the archive as is.
func TestResolveChartRootRel_ArchivePassthrough(t *testing.T) {
	t.Parallel()
	listing := &renderer.SourceListing{Root: "/acquired/chart.tgz", RootIsDir: false}
	rel, err := ResolveChartRootRel(listing)
	if err != nil {
		t.Fatalf("ResolveChartRootRel on an archive: %v", err)
	}
	if rel != "" {
		t.Errorf("rel = %q; want the empty root path for an archive", rel)
	}
}
