// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// assertPairedDistByteIdentical walks the file set in both results.
// For every path appearing in either, the function compares the two
// rendered contents byte-for-byte. First differing file fails the
// test with the path + short head/tail diff so authors can navigate
// to the divergence quickly.
//
// No path exclusion is needed — fullstackAppResult.Files contains only
// rendered output; the harness writes trace output to a separate
// root that does not surface in the result Files map.
func assertPairedDistByteIdentical(t *testing.T, explicit, annotation *fullstackAppResult) {
	t.Helper()
	all := map[string]struct{}{}
	for p := range explicit.Files {
		all[p] = struct{}{}
	}
	for p := range annotation.Files {
		all[p] = struct{}{}
	}
	paths := make([]string, 0, len(all))
	for p := range all {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		ex, exHas := explicit.Files[path]
		an, anHas := annotation.Files[path]
		switch {
		case exHas && !anHas:
			t.Fatalf("path %q present in explicit pair, missing in annotation pair", path)
		case !exHas && anHas:
			t.Fatalf("path %q present in annotation pair, missing in explicit pair", path)
		case !bytes.Equal(ex, an):
			t.Fatalf("path %q content diverges:\n--- explicit (first 400 bytes) ---\n%s\n--- annotation (first 400 bytes) ---\n%s",
				path, headBytes(ex, 400), headBytes(an, 400))
		}
	}
}

func headBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return fmt.Sprintf("%s...\n[truncated; full length=%d]", string(b[:n]), len(b))
}

// runPairedAnnotationVariant compiles the helm-synthesized variant for an
// explicit-Feature Release and an annotation-driven Release through the build-
// faithful harness, then asserts the rendered dist/ output is byte-identical.
// Each Release names its own Aggregate via spec pointers (the build derives
// it), so the pair compares an explicitly-authored Aggregate against the
// annotation-driven one that the same authoring should expand to.
func runPairedAnnotationVariant(t *testing.T, explicitRelease, annotationRelease string) {
	t.Helper()
	explicit := runFullstackAppVariant(t, variantFixture{
		name:        "helm-synthesized",
		releasePath: explicitRelease,
	})
	annotation := runFullstackAppVariant(t, variantFixture{
		name:        "helm-synthesized",
		releasePath: annotationRelease,
	})
	assertPairedDistByteIdentical(t, explicit, annotation)
}

func TestFullstackApp_AnnotationRewriteHost_Boundary(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-boundary-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-boundary-annotation.yaml",
	)
}

func TestFullstackApp_AnnotationRewriteHost_ComponentOverride(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-override-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-override-annotation.yaml",
	)
}

func TestFullstackApp_AnnotationRewriteHost_NullOptOut(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-null-opt-out-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-null-opt-out-annotation.yaml",
	)
}

func TestFullstackApp_AnnotationValuesBinding(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-values-binding.yaml",
		"testdata/fullstack-app/releases/annotation-values-binding-annotation.yaml",
	)
}

func TestFullstackApp_AnnotationRecursiveBoundary(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-recursive-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-recursive-annotation.yaml",
	)
}

// assertBoundaryReachedTheOutput proves the boundary a Release declares actually
// applied to what the pair rendered: every file lands under the boundary's
// declared directory, and each carries the boundary's own annotation.
//
// It exists because byte-identity between the two halves of a pair does not
// establish it. A boundary whose `components` keys name nothing places nothing,
// and a component nothing placed renders outside every boundary — with no
// directory, no annotation, and an empty boundary path. Two Releases in that
// state still agree with each other byte for byte, so the pair assertion passes
// over output that carries none of what the Releases declare. This asserts the
// declaration reached the artifacts, which is the part agreement cannot show.
func assertBoundaryReachedTheOutput(t *testing.T, r *fullstackAppResult, dir, annotated string) {
	t.Helper()
	if len(r.Files) == 0 {
		t.Fatalf("the variant rendered no files; there is nothing for the boundary to have reached")
	}
	paths := make([]string, 0, len(r.Files))
	for p := range r.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !strings.HasPrefix(p, dir+"/") {
			t.Fatalf("every artifact must render under the boundary's declared directory %q; %q does not (all: %v)", dir, p, paths)
		}
		if !bytes.Contains(r.Files[p], []byte(annotated)) {
			t.Fatalf("artifact %q must carry what the boundary annotates (%q); it does not:\n%s",
				p, annotated, headBytes(r.Files[p], 400))
		}
	}
}

// runPlacedAnnotationVariant is runPairedAnnotationVariant for a pair whose
// Releases declare a boundary, adding the assertion that the boundary reached
// the output on both halves.
func runPlacedAnnotationVariant(t *testing.T, explicitRelease, annotationRelease, dir, annotated string) {
	t.Helper()
	explicit := runFullstackAppVariant(t, variantFixture{
		name:        "helm-synthesized",
		releasePath: explicitRelease,
	})
	annotation := runFullstackAppVariant(t, variantFixture{
		name:        "helm-synthesized",
		releasePath: annotationRelease,
	})
	assertBoundaryReachedTheOutput(t, explicit, dir, annotated)
	assertBoundaryReachedTheOutput(t, annotation, dir, annotated)
	assertPairedDistByteIdentical(t, explicit, annotation)
}

func TestFullstackApp_AnnotationAggregateSubsystem(t *testing.T) {
	runPlacedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-aggregate-subsystem.yaml",
		"testdata/fullstack-app/releases/annotation-aggregate-subsystem-annotation.yaml",
		"dist/data", "namespace: data-zone",
	)
}

func TestFullstackApp_AnnotationAggregateComponentOverride(t *testing.T) {
	runPlacedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-aggregate-override.yaml",
		"testdata/fullstack-app/releases/annotation-aggregate-override-annotation.yaml",
		"dist/data", "namespace: data-zone",
	)
}

func TestFullstackApp_AnnotationCrossSideCompose(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-cross-side-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-cross-side-annotation.yaml",
	)
}

func TestFullstackApp_AnnotationStructuredValue(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/releases/annotation-structured-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-structured-annotation.yaml",
	)
}
