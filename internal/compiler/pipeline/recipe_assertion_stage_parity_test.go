// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestTheValidationPathReachesTheBuildsVerdictOnARecipeAssertion runs each row of
// the number-comparison fixture through both entry points into the compiler: the
// one a build renders through and the one a validation reads without rendering.
// A row whose Component assertion refuses, a row whose Recipe assertion refuses
// and a row both accept must reach the same verdict on both, and a refusal must
// read identically on both — one rule, reported one way, whichever surface asked.
//
// 🔴 PLANTED DEFECT: skip the Recipe assertion stage in RunTier4Pipeline (the
// evaluateRecipeAssertions call in pipeline.go): the "above" row, whose refusal
// is the Recipe's, validates clean and fails here.
func TestTheValidationPathReachesTheBuildsVerdictOnARecipeAssertion(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"holds", "below", "above"} {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			in := pipelinetest.Input{
				ReleasePath:  assertionNumberDir + "release-" + variant + ".yaml",
				CatalogPaths: []string{assertionNumberDir + "catalog/base"},
			}
			_, buildErr := pipelinetest.BuildE(t, in)
			t4, validateErr := pipelinetest.Tier4E(t, in)

			if buildErr == nil {
				if validateErr != nil {
					t.Fatalf("the build passed and the validation refused: %s", cerrors.CauseChain(validateErr))
				}
				return
			}
			if !errors.Is(buildErr, renderer.ErrAssertionFailed()) {
				t.Fatalf("the fixture no longer halts the build on an assertion: %s", cerrors.CauseChain(buildErr))
			}
			if !errors.Is(validateErr, renderer.ErrAssertionFailed()) || !renderer.AssertionCalledFail(validateErr) {
				t.Fatalf("the build halted on the author's assertion and the validation did not: %v", validateErr)
			}
			// Compared from the first diagnostic envelope down, which is where every
			// surface starts its rendering: the build's render step adds a frame of its
			// own above the envelope, naming the step, which no surface prints.
			var buildEnv, validateEnv *cerrors.CompilerError
			if !errors.As(buildErr, &buildEnv) || !errors.As(validateErr, &validateEnv) {
				t.Fatalf("a failure carries no diagnostic envelope:\n  build:      %v\n  validation: %v", buildErr, validateErr)
			}
			if got, want := cerrors.HeadedCauseChain(validateEnv), cerrors.HeadedCauseChain(buildEnv); got != want {
				t.Errorf("the two paths present one failure differently:\n  build:      %q\n  validation: %q", want, got)
			}
			// The phases that completed are still handed back beside the failure.
			if t4 == nil || t4.IRGraph == nil || t4.IRGraph.Spec.Nodes.Len() == 0 {
				t.Errorf("the validation path must still return the graph it built; got %+v", t4)
			}
		})
	}
}
