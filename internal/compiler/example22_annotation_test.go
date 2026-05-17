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
// No path exclusion is needed — example22Result.Files contains only
// rendered output; the harness writes trace output to a separate
// root that does not surface in the result Files map.
func assertPairedDistByteIdentical(t *testing.T, explicit, annotation *example22Result) {
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
	explicit := runExample22Variant(t, variantFixture{
		name:          "helm-synthesized",
		aggregatePath: explicitAggregate,
		releasePath:   explicitRelease,
		manifestPath:  manifest,
	})
	annotation := runExample22Variant(t, variantFixture{
		name:          "helm-synthesized",
		aggregatePath: annotationAggregate,
		releasePath:   annotationRelease,
		manifestPath:  manifest,
	})
	assertPairedDistByteIdentical(t, explicit, annotation)
}

func TestExample22_AnnotationRewriteHost_Boundary(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-release-side.yaml",
		"testdata/example22/releases/annotation-boundary-explicit.yaml",
		"testdata/example22/aggregates/annotation-release-side.yaml",
		"testdata/example22/releases/annotation-boundary-annotation.yaml",
		"testdata/example22/manifests/annotation-rewrite-host.yaml",
	)
}

func TestExample22_AnnotationRewriteHost_ComponentOverride(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-release-side.yaml",
		"testdata/example22/releases/annotation-override-explicit.yaml",
		"testdata/example22/aggregates/annotation-release-side.yaml",
		"testdata/example22/releases/annotation-override-annotation.yaml",
		"testdata/example22/manifests/annotation-rewrite-host.yaml",
	)
}

func TestExample22_AnnotationRewriteHost_NullOptOut(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-release-side.yaml",
		"testdata/example22/releases/annotation-null-opt-out-explicit.yaml",
		"testdata/example22/aggregates/annotation-release-side.yaml",
		"testdata/example22/releases/annotation-null-opt-out-annotation.yaml",
		"testdata/example22/manifests/annotation-rewrite-host.yaml",
	)
}

func TestExample22_AnnotationValuesBinding(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-values-binding-explicit.yaml",
		"testdata/example22/releases/annotation-values-binding.yaml",
		"testdata/example22/aggregates/annotation-values-binding-annotation.yaml",
		"testdata/example22/releases/annotation-values-binding.yaml",
		"testdata/example22/manifests/annotation-values-binding.yaml",
	)
}

func TestExample22_AnnotationRecursiveBoundary(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-recursive-boundary.yaml",
		"testdata/example22/releases/annotation-recursive-explicit.yaml",
		"testdata/example22/aggregates/annotation-recursive-boundary.yaml",
		"testdata/example22/releases/annotation-recursive-annotation.yaml",
		"testdata/example22/manifests/annotation-rewrite-host.yaml",
	)
}

func TestExample22_AnnotationAggregateSubsystem(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-aggregate-subsystem-explicit.yaml",
		"testdata/example22/releases/annotation-aggregate-subsystem.yaml",
		"testdata/example22/aggregates/annotation-aggregate-subsystem-annotation.yaml",
		"testdata/example22/releases/annotation-aggregate-subsystem.yaml",
		"testdata/example22/manifests/annotation-values-binding.yaml",
	)
}

func TestExample22_AnnotationAggregateComponentOverride(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-aggregate-override-explicit.yaml",
		"testdata/example22/releases/annotation-aggregate-override.yaml",
		"testdata/example22/aggregates/annotation-aggregate-override-annotation.yaml",
		"testdata/example22/releases/annotation-aggregate-override.yaml",
		"testdata/example22/manifests/annotation-values-binding.yaml",
	)
}

func TestExample22_AnnotationCrossSideCompose(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-cross-side-explicit.yaml",
		"testdata/example22/releases/annotation-cross-side-explicit.yaml",
		"testdata/example22/aggregates/annotation-cross-side-annotation.yaml",
		"testdata/example22/releases/annotation-cross-side-annotation.yaml",
		"testdata/example22/manifests/annotation-cross-side.yaml",
	)
}

func TestExample22_AnnotationStructuredValue(t *testing.T) {
	runPairedAnnotationVariant(t,
		"testdata/example22/aggregates/annotation-structured.yaml",
		"testdata/example22/releases/annotation-structured-explicit.yaml",
		"testdata/example22/aggregates/annotation-structured.yaml",
		"testdata/example22/releases/annotation-structured-annotation.yaml",
		"testdata/example22/manifests/annotation-structured.yaml",
	)
}
