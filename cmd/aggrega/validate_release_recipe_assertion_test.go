// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestValidateReleaseEvaluatesRecipeAssertions holds validate --release to the
// verdict a build reaches on a Recipe's assertion, over the fixture whose Recipe
// asserts that the Release chose infra.image.
//
// A Release leaving the value to the default fails validation with the condition
// and the sentence the build halts with — the same block, line for line, as the
// build's own error prints — and the core's run error matches the condition by
// identity, not by text. A Release choosing the value validates clean: the control
// showing that the assertion, and nothing else in the fixture, is what the
// validation refused.
//
// 🔴 PLANTED DEFECT: skip the Recipe assertion stage in RunTier4Pipeline
// (internal/compiler/pipeline/pipeline.go, the evaluateRecipeAssertions call): the
// first row validates clean and fails here; the control still passes.
//
// Not parallel: the catalog's notices are written to the process's standard error.
func TestValidateReleaseEvaluatesRecipeAssertions(t *testing.T) {
	t.Run("a Release leaving the value to the default fails as the build halts", func(t *testing.T) {
		_, catalogRoot, releasePath, _ := assertionWorkspace(t, "")
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
		ctx := context.Background()

		var diag, out bytes.Buffer
		verr := validateRelease(ctx, ws, releasePath, []string{catalogRoot}, compilerops.VerbosityDetailed, false, false, &diag, &out)
		if verr == nil {
			t.Fatalf("validate --release passed a Release whose Recipe assertion fails\ndiagnostics:\n%s\nout:\n%s", diag.String(), out.String())
		}

		// The validation's own core, over the inputs validateRelease hands it: the
		// failure is the assertion's condition, matched by identity, and it is the
		// author's own fail rather than a template the evaluation could not read.
		deps, err := resolveReleaseDependencies(ctx, ws, releasePath, []string{catalogRoot})
		if err != nil {
			t.Fatalf("resolve the Release's dependencies: %s", formatCompilerError(err))
		}
		vr := compilerops.Validate(ctx, compilerops.ValidateInputs{
			View:            deps.view,
			Closure:         deps.resolved.Closure,
			Release:         deps.resolved.Release,
			ReleasePath:     releasePath,
			Manifest:        deps.resolved.Manifest,
			AggBytes:        deps.resolved.AggBytes,
			Lockfile:        deps.lockfile,
			AggregatePath:   catalog.RelativeToWorkspace(ws.Root(), deps.resolved.AggregatePath),
			WorkspaceRoot:   ws.Root(),
			WorkspaceConfig: ws.Config(),
			CompilerVersion: provenanceCompilerVersion(),
		})
		if !errors.Is(vr.RunErr, renderer.ErrAssertionFailed()) || !renderer.AssertionCalledFail(vr.RunErr) {
			t.Fatalf("the validation's run error is not the author's failing assertion: %v", vr.RunErr)
		}

		// The build of the same Release halts on the same assertion; every line it
		// prints for that halt is printed by the validation too.
		if err := pullRun(ctx, pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: ws}); err != nil {
			t.Fatalf("prime lockfile via pull: %v", err)
		}
		var buildDiag, buildOut bytes.Buffer
		berr := dispatchBuild(ctx, ws, releasePath, []string{catalogRoot}, false, &buildDiag, &buildOut)
		if !errors.Is(berr, renderer.ErrAssertionFailed()) {
			t.Fatalf("the build did not halt on the assertion: %v", berr)
		}
		block := formatCompilerError(berr)
		for _, want := range []string{"E0033", imageChosenFailure} {
			if !strings.Contains(block, want) {
				t.Fatalf("the build's halt does not carry %q, so the fixture no longer exercises this case:\n%s", want, block)
			}
		}
		for _, line := range strings.Split(block, "\n") {
			if !strings.Contains(diag.String(), "  "+line) {
				t.Errorf("the validation does not print the build's line %q\nvalidation:\n%s\nbuild:\n%s", line, diag.String(), block)
			}
		}
	})

	t.Run("a Release choosing the value validates clean", func(t *testing.T) {
		_, catalogRoot, releasePath, _ := assertionWorkspace(t, "registry.example/server:1.0")
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
		var diag, out bytes.Buffer
		if err := validateRelease(context.Background(), ws, releasePath, []string{catalogRoot}, compilerops.VerbosityDetailed, false, false, &diag, &out); err != nil {
			t.Fatalf("validate --release refused a Release satisfying the assertion: %v\ndiagnostics:\n%s", err, diag.String())
		}
		if !strings.Contains(out.String(), "release dependency validation passed") {
			t.Errorf("the success line is missing:\n%s", out.String())
		}
	})
}
