// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestPipelineStepReadingCallersFacts_HaltsWhateverTheDeclaration compiles the
// pipeline-only-attachment fixture with its attachment bound to a FeatureRecipe
// whose one pipeline step reads, in its input, the render facts of the components
// connecting to the host — once under a FeatureRecipe that declares it reads its
// callers' render facts, once under one that does not.
//
// Both builds halt with the step's own condition and neither with the undeclared
// one: a step never sees the connections, so the declaration that remedy names
// would not make the read work. The finding names the reference and the field it
// was written in, so the author is sent to the step by its id.
//
// The fixture's step, plugin and host are the ones TestPipelineOnlyAttachment_PipelineRuns
// builds successfully; the two FeatureRecipes here differ from that one by the
// step's input alone (and, in the declared row, the key), so the halt is
// attributable to the read rather than to the fixture's shape.
func TestPipelineStepReadingCallersFacts_HaltsWhateverTheDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release string
	}{
		{name: "a FeatureRecipe that declares it reads its callers' render facts",
			release: "testdata/pipeline-only-attachment/release-step-reads-callers.yaml"},
		{name: "a FeatureRecipe that does not declare it",
			release: "testdata/pipeline-only-attachment/release-step-reads-callers-undeclared.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := pipelineOnlyAttachmentInput(t, tc.release)
			t.Parallel()

			_, err := pipelinetest.BuildE(t, in)
			if err == nil {
				t.Fatal("the build rendered a pipeline step that reads its callers' render facts")
			}
			findings := pipelinetest.Findings(err, renderer.ErrPipelineStepReadsCallerRenderFacts())
			if len(findings) != 1 {
				t.Fatalf("the build carries %d finding(s) of the step's condition, want 1: %v", len(findings), err)
			}
			if other := pipelinetest.Findings(err, renderer.ErrCallerRenderFactsNotDeclared()); len(other) != 0 {
				t.Errorf("the build also carries the undeclared condition, whose remedy would not make a "+
					"step's read work: %v", err)
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
