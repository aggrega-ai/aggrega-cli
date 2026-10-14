// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nameStringSchema requires an object with a string "name" property.
const nameStringSchema = `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`

func orderedOf(pairs ...any) types.OrderedMap[any] {
	var m types.OrderedMap[any]
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// TestValidateStepInput_ContractHalt proves a rendered `with` input that
// violates the export's declared input schema halts with a plain,
// step-naming error wrapping ErrPipelineContractViolation, while a conforming
// input passes.
func TestValidateStepInput_ContractHalt(t *testing.T) {
	export := wasm.PluginExport{Name: "f", Input: json.RawMessage(nameStringSchema)}
	step := types.PipelineStep{ID: "configure-db", Plugin: "p", Function: "f"}

	// Violation: name is a number, not a string.
	err := validateStepInput(export, orderedOf("name", 123), step)
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Fatalf("violating input: err = %v, want ErrPipelineContractViolation", err)
	}
	if !strings.Contains(err.Error(), "configure-db") || !strings.Contains(err.Error(), "input does not satisfy the declared input contract") {
		t.Errorf("violating input: message %q must name the step and the input contract", err.Error())
	}
	t.Logf("input-halt message: %v", err)

	// Conforming input passes.
	if err := validateStepInput(export, orderedOf("name", "db"), step); err != nil {
		t.Errorf("conforming input: unexpected error %v", err)
	}
}

// TestValidateStepOutput_ContractHalt proves returned outputs that violate the
// export's declared output schema halt with a plain, step-naming error wrapping
// ErrPipelineContractViolation, while conforming outputs pass.
func TestValidateStepOutput_ContractHalt(t *testing.T) {
	export := wasm.PluginExport{Name: "f", Output: json.RawMessage(nameStringSchema)}
	step := types.PipelineStep{ID: "emit-dsn", Plugin: "p", Function: "f"}

	// Violation: required "name" is absent.
	err := validateStepOutput(export, orderedOf("other", "v"), step)
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Fatalf("violating outputs: err = %v, want ErrPipelineContractViolation", err)
	}
	if !strings.Contains(err.Error(), "emit-dsn") || !strings.Contains(err.Error(), "outputs that do not satisfy the declared output contract") {
		t.Errorf("violating outputs: message %q must name the step and the output contract", err.Error())
	}
	t.Logf("output-halt message: %v", err)

	// Conforming outputs pass.
	if err := validateStepOutput(export, orderedOf("name", "postgres://…"), step); err != nil {
		t.Errorf("conforming outputs: unexpected error %v", err)
	}
}

// TestValidateStep_NoSchemaUnchanged proves an export that declares no input or
// output schema is unchecked — any value passes, preserving free-form
// pass-through behaviour.
func TestValidateStep_NoSchemaUnchanged(t *testing.T) {
	export := wasm.PluginExport{Name: "f"} // no Input, no Output
	step := types.PipelineStep{ID: "s", Plugin: "p", Function: "f"}

	if err := validateStepInput(export, orderedOf("name", 123), step); err != nil {
		t.Errorf("no input schema: value must pass unchecked, got %v", err)
	}
	if err := validateStepOutput(export, orderedOf("anything", true), step); err != nil {
		t.Errorf("no output schema: value must pass unchecked, got %v", err)
	}
}

// TestValidateStep_ReadsTheValuesAsTheWireCarriesThem proves the two checks reach
// the same verdict on a value as it crosses the wire as on the value the walk
// holds: the step input as the walk rendered it against the same input read back
// out of its encoded payload, and the outputs a guest's answer decodes to against
// the same outputs built in place. The values cross in the binary encoding, so a
// number arrives as the JSON value it encodes and a nested object as an
// order-keeping map; neither the verdict nor the refusal's sentence may depend on
// which of the two forms the check was handed.
func TestValidateStep_ReadsTheValuesAsTheWireCarriesThem(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"port":{"type":"integer","maximum":1024},` +
		`"labels":{"type":"object","additionalProperties":{"type":"string"}}},"required":["port"]}`)
	export := wasm.PluginExport{Name: "f", Input: schema, Output: schema}
	step := types.PipelineStep{ID: "s", Plugin: "p", Function: "f"}

	var nested types.OrderedMap[any]
	nested.Set("zeta", "z")
	nested.Set("alpha", 7) // not a string: refused at /labels/alpha
	held := orderedOf("port", 8080, "labels", &nested)

	payload, err := frp.FRPStepInput{StepInput: held}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the step input: %v", err)
	}
	var in frp.FRPStepInput
	if err := in.DecodeCBORStrict(payload); err != nil {
		t.Fatalf("decode the step input: %v", err)
	}
	answer, err := frp.FRPStepOutput{Outputs: held}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the step output: %v", err)
	}
	var out frp.FRPStepOutput
	if err := out.DecodeCBORStrict(answer); err != nil {
		t.Fatalf("decode the step output: %v", err)
	}

	heldIn := validateStepInput(export, held, step)
	wireIn := validateStepInput(export, in.StepInput, step)
	if heldIn == nil || wireIn == nil {
		t.Fatalf("the input check must refuse both forms: held=%v wire=%v", heldIn, wireIn)
	}
	if heldIn.Error() != wireIn.Error() {
		t.Errorf("the input refusal depends on the form it was handed:\n held: %s\n wire: %s", heldIn, wireIn)
	}
	heldOut := validateStepOutput(export, held, step)
	wireOut := validateStepOutput(export, out.Outputs, step)
	if heldOut == nil || wireOut == nil {
		t.Fatalf("the output check must refuse both forms: held=%v wire=%v", heldOut, wireOut)
	}
	if heldOut.Error() != wireOut.Error() {
		t.Errorf("the output refusal depends on the form it was handed:\n held: %s\n wire: %s", heldOut, wireOut)
	}
}
