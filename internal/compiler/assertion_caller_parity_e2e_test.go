// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — caller-coverage parity for Component and Feature
// assertions.
//
// The assertion rule reads the finalised IR graph and the catalog and nothing
// else, so its inputs are complete before any artifact bytes exist. Both entry
// points into the pre-render phases must therefore reach the same verdict. They
// did not: the render entry point halted while the validation entry point — the
// one `aggrega validate --release`, the machine-readable tool surface and the
// editor daemon all reach through a single semantic-validation core — returned a
// nil error, zero diagnostics, zero warnings and a fully built graph.
//
// The silence is what makes the class dangerous: a surface that says nothing is
// indistinguishable from a surface that checked and approved.

package compiler_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

func assertionCoverageInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/assertion-coverage/release-failing.yaml",
		CatalogPaths: []string{
			"testdata/uses-edge-config/catalog/base",
			"testdata/assertion-coverage/catalog/base",
		},
	}
}

// TestAssertionCallerParity_ValidationPathReachesTheSameVerdict pins that both
// entry points reject a failing Component assertion with the same sentinel.
func TestAssertionCallerParity_ValidationPathReachesTheSameVerdict(t *testing.T) {
	t.Parallel()

	_, buildErr := pipelinetest.BuildE(t, assertionCoverageInput())
	if !errors.Is(buildErr, renderer.ErrAssertionFailed()) {
		t.Fatalf("render path: want ErrAssertionFailed; got %v", buildErr)
	}

	t4, validateErr := pipelinetest.Tier4E(t, assertionCoverageInput())
	if !errors.Is(validateErr, renderer.ErrAssertionFailed()) {
		t.Fatalf("validation path: want ErrAssertionFailed; got %v", validateErr)
	}
	if buildErr.Error() != validateErr.Error() {
		t.Errorf("the two paths must present one failure identically:\n  render:     %q\n  validation: %q",
			buildErr.Error(), validateErr.Error())
	}

	// The validation path's contract is that phases which completed are returned
	// even when a later one failed. The graph is final before assertions run, so
	// it must still be there — a caller that maps diagnostics onto the graph
	// needs it.
	if t4 == nil || t4.IRGraph == nil {
		t.Fatalf("validation path must still return the graph it built; got %+v", t4)
	}
	if t4.IRGraph.Spec.Nodes.Len() == 0 {
		t.Error("validation path returned an empty graph")
	}
}

// TestAssertionCallerParity_PassingAssertionIsSilentOnBothPaths is the
// counter-case: the gate rejects a failing assertion, not the presence of one.
// Without this, a closure that always failed would look correct.
func TestAssertionCallerParity_PassingAssertionIsSilentOnBothPaths(t *testing.T) {
	t.Parallel()
	in := pipelinetest.Input{
		ReleasePath: "testdata/assertion-coverage/release-passing.yaml",
		CatalogPaths: []string{
			"testdata/uses-edge-config/catalog/base",
			"testdata/assertion-coverage/catalog/base",
		},
	}
	if _, err := pipelinetest.BuildE(t, in); err != nil {
		t.Fatalf("render path: a satisfied assertion must build clean; got %v", err)
	}
	if _, err := pipelinetest.Tier4E(t, in); err != nil {
		t.Fatalf("validation path: a satisfied assertion must validate clean; got %v", err)
	}
}
