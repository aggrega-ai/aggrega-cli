// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPipelineStep_TypeScriptStepKeepsTheAuthoredOrder drives the TypeScript kit's
// step example through the real step invoker, twice in one pipeline. The example
// returns its `with` input as its outputs, so the first step's outputs are its
// `with` keys in the order they arrived, and the second step reads each of them
// through .steps.<id>.outputs.
//
// The `with` keys are in an order no sort produces, with an integer-like key in
// the middle, which a plain JavaScript object would move to the front. The order
// survives only if every leg keeps it: the host's encoding of the step input, the
// guest's decode of it into a Map, the guest's encoding of the Map it returns, and
// the host's decode of the answer into the steps channel. A template reading the
// channel iterates its keys alphabetically by design, so the order is read from
// the channel itself, and the later step proves each value reached a template.
func TestPipelineStep_TypeScriptStepKeepsTheAuthoredOrder(t *testing.T) {
	path := wasmtest.EnsureFixtureBuilt(t, "frp-typescript.wasm")
	lookup := pluginpath.NewResolver(
		pluginpath.ResolutionContext{Override: path}, pluginpath.PinPostureNone, pluginpath.PinMaps{}).Resolve

	var own types.OrderedMap[frp.Unit]
	// A Service, so the example patches nothing and only its outputs are under test.
	own.Set("svc.yaml", frp.Unit{Token: "svc.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Service","metadata":{"name":"web"}}`)})

	steps := []types.PipelineStep{
		{ID: "echo", Plugin: "frp-typescript.wasm", Function: "step", With: "zeta: z\n\"10\": ten\nalpha: a\n"},
		{ID: "read", Plugin: "frp-typescript.wasm", Function: "step",
			With: "first: \"{{ .steps.echo.outputs.zeta }}\"\nsecond: \"{{ index .steps.echo.outputs \"10\" }}\"\nthird: \"{{ .steps.echo.outputs.alpha }}\"\n"},
	}
	eval := func(name, text string, data any) (string, error) {
		return EvalTemplate(name, text, data, redact.Keys{})
	}
	withCtx := &types.TemplateContext{}

	if _, err := frp.Walk(context.Background(), frp.DocumentSet{Own: own}, steps, newStepInvoker(lookup),
		withCtx, renderctx.RenderContext{}, eval); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	echo, ok := withCtx.Steps.Get("echo")
	if !ok {
		t.Fatal(".steps.echo absent: the first step recorded no outputs")
	}
	if got, want := echo.Outputs.Keys(), []string{"zeta", "10", "alpha"}; !slices.Equal(got, want) {
		t.Errorf(".steps.echo.outputs is in order %v, want the authored order %v", got, want)
	}

	read, ok := withCtx.Steps.Get("read")
	if !ok {
		t.Fatal(".steps.read absent: the second step recorded no outputs")
	}
	if got, want := read.Outputs.Keys(), []string{"first", "second", "third"}; !slices.Equal(got, want) {
		t.Errorf(".steps.read.outputs is in order %v, want %v", got, want)
	}
	for key, want := range map[string]string{"first": "z", "second": "ten", "third": "a"} {
		if got, _ := read.Outputs.Get(key); got != want {
			t.Errorf("the later step read %s = %q from .steps.echo.outputs, want %q", key, got, want)
		}
	}
}
