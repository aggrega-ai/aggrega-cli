// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package stepwalk

import (
	"context"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestWalk_StepsChannelIsolatedPerPipeline proves the walk isolates the
// per-pipeline `.steps` channel from any inherited or shared context. A
// context arriving with a foreign `.steps` entry (as it would after a shallow
// copy of a context an earlier pipeline populated) must not leak that entry
// into this pipeline: after the walk, `.steps` holds only this pipeline's own
// step id, and a step's `with` referencing the foreign id resolves to nothing.
func TestWalk_StepsChannelIsolatedPerPipeline(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}

	var ownOut types.OrderedMap[any]
	ownOut.Set("k", "from-mine")

	inv := newScripted()
	inv.outputs["mine"] = frp.FRPStepOutput{Outputs: ownOut}

	// The eval stub resolves the step's `with`: it reports whether the foreign
	// entry is visible in the live context at evaluation time. If isolation
	// holds, `.steps.foreign` is absent and the rendered input records "gone".
	eval := func(name, text string, data any) (string, error) {
		if !strings.Contains(text, ".steps.foreign") {
			return text, nil
		}
		tc := data.(*types.TemplateContext)
		if _, ok := tc.Steps.Get("foreign"); ok {
			return `{"foreign_visible":"yes"}`, nil
		}
		return `{"foreign_visible":"gone"}`, nil
	}

	steps := []types.PipelineStep{
		{ID: "mine", Plugin: "p", Function: "f", With: "{{ .steps.foreign }}"},
	}

	// Pre-populate the caller's context with a foreign entry and capture the
	// backing so a post-walk Set on it can be shown not to reach the walk's map.
	withCtx := &types.TemplateContext{}
	var foreign types.StepOutput
	foreign.Outputs.Set("k", "from-a-prior-pipeline")
	withCtx.Steps.Set("foreign", foreign)

	_, err := Walk(context.Background(), docs, steps, inv.invoke, withCtx, renderctx.RenderContext{}, eval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// After the walk the channel holds only this pipeline's step id.
	if withCtx.Steps.Len() != 1 {
		t.Fatalf("Steps channel = %d entries, want 1 (only this pipeline's own step)", withCtx.Steps.Len())
	}
	if _, ok := withCtx.Steps.Get("foreign"); ok {
		t.Errorf(".steps.foreign still present after walk; inherited entry was not cleared")
	}
	so, ok := withCtx.Steps.Get("mine")
	if !ok {
		t.Fatalf(".steps.mine absent after walk")
	}
	if got, _ := so.Outputs.Get("k"); got != "from-mine" {
		t.Errorf(".steps.mine.outputs.k = %q, want from-mine", got)
	}

	// The foreign reference did not resolve during the walk: the step's rendered
	// `with` recorded the channel as isolated.
	in := receivedInput(t, inv.received["mine"])
	if got, _ := in.StepInput.Get("foreign_visible"); got != "gone" {
		t.Errorf("foreign entry was visible to the step; got %v, want gone", got)
	}
}
