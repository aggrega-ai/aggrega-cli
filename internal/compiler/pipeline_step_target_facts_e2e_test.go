// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestPipelineStepReadingTargetsFacts_HaltsWhateverTheDeclaration is the twin of
// TestPipelineStepReadingCallersFacts_HaltsWhateverTheDeclaration for the other end
// of a connection. It compiles the pipeline-only-attachment fixture with its
// attachment bound to a FeatureRecipe whose one pipeline step reads, in its input,
// the render facts of the components its host is wired to — once under a
// FeatureRecipe that declares it reads its targets' render facts, once under one
// that does not.
//
// Both builds halt with the step's own condition and neither with the undeclared
// one: a step is handed the connections its host makes but never their targets'
// facts, so the declaration that remedy names would not make the read work. The
// finding names the reference and the field it was written in, so the author is
// sent to the step by its id.
//
// The rule reads the template as written, so the host needs no connection of its
// own for the read to be refused; the fixture's step, plugin and host are the ones
// TestPipelineOnlyAttachment_PipelineRuns builds successfully, and the two
// FeatureRecipes here differ from that one by the step's input alone (and, in the
// declared row, the key).
func TestPipelineStepReadingTargetsFacts_HaltsWhateverTheDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release string
	}{
		{name: "a FeatureRecipe that declares it reads its targets' render facts",
			release: "testdata/pipeline-only-attachment/release-step-reads-targets.yaml"},
		{name: "a FeatureRecipe that does not declare it",
			release: "testdata/pipeline-only-attachment/release-step-reads-targets-undeclared.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := pipelineOnlyAttachmentInput(t, tc.release)
			t.Parallel()

			_, err := pipelinetest.BuildE(t, in)
			if err == nil {
				t.Fatal("the build rendered a pipeline step that reads its targets' render facts")
			}
			findings := pipelinetest.Findings(err, renderer.ErrPipelineStepReadsTargetRenderFacts())
			if len(findings) != 1 {
				t.Fatalf("the build carries %d finding(s) of the step's condition, want 1: %v", len(findings), err)
			}
			for _, other := range []error{
				renderer.ErrTargetRenderFactsNotDeclared(),
				renderer.ErrCallerRenderFactsNotDeclared(),
				renderer.ErrPipelineStepReadsCallerRenderFacts(),
			} {
				if got := pipelinetest.Findings(err, other); len(got) != 0 {
					t.Errorf("the build also carries %v: %v", other, err)
				}
			}
			message := findings[0].Error()
			for _, want := range []string{".render_facts", "pipeline[inject_into_host].with"} {
				if !strings.Contains(message, want) {
					t.Errorf("the finding does not name %q: %s", want, message)
				}
			}
			t.Logf("REFUSED %s -> %s", tc.name, message)
		})
	}
}
