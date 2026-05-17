// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"bytes"
	"fmt"
	"sort"
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

// runPairedAnnotationVariant runs the helm-synthesized pipeline for an
// explicit-Feature pair and an annotation-driven pair and asserts the
// rendered dist/ output is byte-identical.
func runPairedAnnotationVariant(
	t *testing.T,
	explicitAggregate, explicitRelease,
	annotationAggregate, annotationRelease,
	manifest string,
) {
	t.Helper()
	explicit := runFullstackAppVariant(t, variantFixture{
		name:          "helm-synthesized",
		aggregatePath: explicitAggregate,
		releasePath:   explicitRelease,
		manifestPath:  manifest,
	})
	annotation := runFullstackAppVariant(t, variantFixture{
		name:          "helm-synthesized",
		aggregatePath: annotationAggregate,
		releasePath:   annotationRelease,
		manifestPath:  manifest,
	})
	assertPairedDistByteIdentical(t, explicit, annotation)
}

func TestFullstackApp_AnnotationRewriteHost_Boundary(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-release-side.yaml",
		"testdata/fullstack-app/releases/annotation-boundary-explicit.yaml",
		"testdata/fullstack-app/aggregates/annotation-release-side.yaml",
		"testdata/fullstack-app/releases/annotation-boundary-annotation.yaml",
		"testdata/fullstack-app/manifests/annotation-rewrite-host.yaml",
	)
}

func TestFullstackApp_AnnotationRewriteHost_ComponentOverride(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-release-side.yaml",
		"testdata/fullstack-app/releases/annotation-override-explicit.yaml",
		"testdata/fullstack-app/aggregates/annotation-release-side.yaml",
		"testdata/fullstack-app/releases/annotation-override-annotation.yaml",
		"testdata/fullstack-app/manifests/annotation-rewrite-host.yaml",
	)
}

func TestFullstackApp_AnnotationRewriteHost_NullOptOut(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-release-side.yaml",
		"testdata/fullstack-app/releases/annotation-null-opt-out-explicit.yaml",
		"testdata/fullstack-app/aggregates/annotation-release-side.yaml",
		"testdata/fullstack-app/releases/annotation-null-opt-out-annotation.yaml",
		"testdata/fullstack-app/manifests/annotation-rewrite-host.yaml",
	)
}

func TestFullstackApp_AnnotationValuesBinding(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-values-binding-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-values-binding.yaml",
		"testdata/fullstack-app/aggregates/annotation-values-binding-annotation.yaml",
		"testdata/fullstack-app/releases/annotation-values-binding.yaml",
		"testdata/fullstack-app/manifests/annotation-values-binding.yaml",
	)
}

func TestFullstackApp_AnnotationRecursiveBoundary(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-recursive-boundary.yaml",
		"testdata/fullstack-app/releases/annotation-recursive-explicit.yaml",
		"testdata/fullstack-app/aggregates/annotation-recursive-boundary.yaml",
		"testdata/fullstack-app/releases/annotation-recursive-annotation.yaml",
		"testdata/fullstack-app/manifests/annotation-rewrite-host.yaml",
	)
}

func TestFullstackApp_AnnotationAggregateSubsystem(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-aggregate-subsystem-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-aggregate-subsystem.yaml",
		"testdata/fullstack-app/aggregates/annotation-aggregate-subsystem-annotation.yaml",
		"testdata/fullstack-app/releases/annotation-aggregate-subsystem.yaml",
		"testdata/fullstack-app/manifests/annotation-values-binding.yaml",
	)
}

func TestFullstackApp_AnnotationAggregateComponentOverride(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-aggregate-override-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-aggregate-override.yaml",
		"testdata/fullstack-app/aggregates/annotation-aggregate-override-annotation.yaml",
		"testdata/fullstack-app/releases/annotation-aggregate-override.yaml",
		"testdata/fullstack-app/manifests/annotation-values-binding.yaml",
	)
}

func TestFullstackApp_AnnotationCrossSideCompose(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-cross-side-explicit.yaml",
		"testdata/fullstack-app/releases/annotation-cross-side-explicit.yaml",
		"testdata/fullstack-app/aggregates/annotation-cross-side-annotation.yaml",
		"testdata/fullstack-app/releases/annotation-cross-side-annotation.yaml",
		"testdata/fullstack-app/manifests/annotation-cross-side.yaml",
	)
}

func TestFullstackApp_AnnotationStructuredValue(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/fullstack-app/aggregates/annotation-structured.yaml",
		"testdata/fullstack-app/releases/annotation-structured-explicit.yaml",
		"testdata/fullstack-app/aggregates/annotation-structured.yaml",
		"testdata/fullstack-app/releases/annotation-structured-annotation.yaml",
		"testdata/fullstack-app/manifests/annotation-structured.yaml",
	)
}
