// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a Manifest mapping-table
// implements-inconsistency halts a real build, not only a unit test.

package compiler_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestManifestImplementsConsistency_HaltsOnMismatch pins the production halt
// path: a Manifest that maps core/compute/api-server to the web-ui recipe (whose
// spec.implements is core/compute/web-ui) is mapping-table-inconsistent, and the
// build-faithful pipeline — gating exactly as the CLI build/pull do — halts with
// ErrManifestImplementsMismatch before IR construction. The consistency check
// was previously declared and unit-tested but unwired; this proves it now gates
// a real build.
func TestManifestImplementsConsistency_HaltsOnMismatch(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/fullstack-app/release-implements-mismatch.yaml",
		CatalogPaths: []string{"testdata/fullstack-app/catalog/base", "testdata/fullstack-app/catalog/recipes/helm-synthesized-with-artifacts"},
	})
	if err == nil {
		t.Fatal("expected a build halt for the inconsistent Manifest mapping, got nil")
	}
	if !errors.Is(err, catalog.ErrManifestImplementsMismatch) {
		t.Fatalf("expected ErrManifestImplementsMismatch, got: %v", err)
	}
}

// TestManifestImplementsConsistency_ConsistentManifestUnaffected is the
// non-vacuous counterpart: the same catalog with the consistent mapping manifest
// builds without a consistency halt (it may proceed to other pipeline stages;
// what matters here is that ErrManifestImplementsMismatch does NOT fire).
func TestManifestImplementsConsistency_ConsistentManifestUnaffected(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/fullstack-app/release.yaml",
		CatalogPaths: []string{"testdata/fullstack-app/catalog/base", "testdata/fullstack-app/catalog/recipes/helm-synthesized-with-artifacts"},
	})
	if errors.Is(err, catalog.ErrManifestImplementsMismatch) {
		t.Fatalf("consistent Manifest must not trip the implements-consistency halt, got: %v", err)
	}
}
