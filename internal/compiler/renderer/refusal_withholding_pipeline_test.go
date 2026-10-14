// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// What a pipeline step's refusal may say when the step's `with` template read a
// value an author declared sensitive: the step's declared input contract refusing
// what the `with` rendered, and the rendered `with` not reading as a map. Each
// is driven through the executor the production path runs, with only the invoker
// canned, and each withheld row sits beside a row that must be byte-identical to
// the same refusal with nothing declared.

// stepInputContract is a declared input contract whose refusal quotes the value
// it refused: a pattern the library reports with the value it did not match.
var stepInputContract = json.RawMessage(`{"type":"object","properties":{"origin":{"type":"string","pattern":"^ref:"}}}`)

// contractInvoker runs the real input-contract check over whatever the step's
// `with` rendered, and never reaches a guest: every row it serves refuses there.
func contractInvoker() frp.StepInvoker {
	return func(_ context.Context, step types.PipelineStep, input types.OrderedMap[any], _ []byte) ([]byte, frp.OutputsCheck, error) {
		if err := validateStepInput(wasm.PluginExport{Input: stepInputContract}, input, step); err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("the row's input satisfied the contract, so the row proves nothing")
	}
}

// pipelineWithheldContext carries the declared value beside one nobody declared.
func pipelineWithheldContext() *types.TemplateContext {
	values := types.OrderedMap[any]{}
	values.Set("admin_password", withheldNodeSecret)
	values.Set("admin_note", withheldUndeclared)
	return &types.TemplateContext{Values: values}
}

// pipelineRefusal runs one step whose `with` is with through the executor and
// returns the whole text a surface can render of its refusal.
func pipelineRefusal(t *testing.T, with string, keys redact.Keys) (string, error) {
	t.Helper()
	steps := []types.PipelineStep{{ID: "guard", Plugin: "canned", Function: "step", With: with}}
	_, err := runNodePipelineWithInvoker(context.Background(), steps,
		ownDeployment(), nil, false, "helm", "",
		renderctx.RenderContext{}, pipelineWithheldContext(), keys,
		newStockYAMLCodec(2), newStockYAMLCodec(2), contractInvoker(), "node")
	if err == nil {
		t.Fatalf("the step must refuse; the row proves nothing otherwise")
	}
	return err.Error() + "\n" + cerrors.CauseChain(err), err
}

func TestPipelineStepRefusal_WithholdsADeclaredValueTheWithRendered(t *testing.T) {
	t.Parallel()
	declared := redact.Keys{Values: map[string]bool{"admin_password": true}}
	cases := []struct {
		name string
		with string
		// quoted is the value the refusal quotes with nothing declared — the
		// control proving the row's value reaches the text at all.
		quoted string
		// want is text the withheld refusal must carry; empty where it must be
		// byte-identical to the refusal with nothing declared.
		want      []string
		condition error
	}{
		{
			name:      "the input contract refusing the declared value",
			with:      "origin: {{ .values.admin_password }}",
			quoted:    withheldNodeSecret,
			want:      []string{"at '/origin': pattern" + stepInputWithheldNotice},
			condition: frp.ErrPipelineContractViolation(),
		},
		{
			name:      "the input contract refusing a value nobody declared",
			with:      "origin: {{ .values.admin_note }}",
			quoted:    withheldUndeclared,
			condition: frp.ErrPipelineContractViolation(),
		},
		{
			name:      "the rendered with repeating a key it rendered from the declared value",
			with:      "{{ .values.admin_password }}: a\n{{ .values.admin_password }}: b",
			quoted:    withheldNodeSecret,
			want:      []string{"parse rendered `with` as a map: " + decodeFailureWithheld},
			condition: frp.ErrPipelineStepFailed(),
		},
		{
			name:      "the rendered with repeating a key it rendered from a value nobody declared",
			with:      "{{ .values.admin_note }}: a\n{{ .values.admin_note }}: b",
			quoted:    withheldUndeclared,
			condition: frp.ErrPipelineStepFailed(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			plain, _ := pipelineRefusal(t, c.with, redact.Keys{})
			if !strings.Contains(plain, c.quoted) {
				t.Fatalf("control: the refusal with nothing declared does not quote %q, so the row proves nothing:\n%s", c.quoted, plain)
			}
			got, err := pipelineRefusal(t, c.with, declared)
			if !errors.Is(err, c.condition) {
				t.Errorf("the refusal lost its condition %v:\n%s", c.condition, got)
			}
			if c.want == nil {
				if got != plain {
					t.Errorf("a refusal quoting no declared value must be byte-identical:\nwithheld: %s\nplain:    %s", got, plain)
				}
				return
			}
			if strings.Contains(got, withheldNodeSecret) {
				t.Errorf("the refusal carries the declared value:\n%s", got)
			}
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal must carry %q:\n%s", want, got)
				}
			}
		})
	}
}

// TestStepInputRefused_ReadsAsTheRefusalAlwaysHas pins the input contract
// refusal, where nothing is withheld, to the sentence and the conditions it has
// always carried, so giving it a type of its own moved no byte of it.
func TestStepInputRefused_ReadsAsTheRefusalAlwaysHas(t *testing.T) {
	t.Parallel()
	cause := wasm.ValidateValueAgainstSchema(stepInputContract, map[string]any{"origin": "not-a-reference"})
	if cause == nil {
		t.Fatal("the contract must refuse the value")
	}
	refused := &stepInputRefused{stepID: "guard", cause: cause}
	always := fmt.Errorf("pipeline step %q `with` input does not satisfy the declared input contract: %w: %w",
		"guard", cause, frp.ErrPipelineContractViolation())
	if refused.Error() != always.Error() {
		t.Errorf("the refusal's sentence moved:\n got: %s\nwant: %s", refused.Error(), always.Error())
	}
	if !errors.Is(refused, frp.ErrPipelineContractViolation()) || !errors.Is(refused, cause) {
		t.Errorf("the refusal must unwrap to the contract violation and to the validator's failure: %v", refused)
	}
}
