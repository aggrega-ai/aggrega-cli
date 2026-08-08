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

func inputPayload(t *testing.T, si types.OrderedMap[any]) []byte {
	t.Helper()
	b, err := json.Marshal(frp.FRPStepInput{StepInput: si})
	if err != nil {
		t.Fatalf("marshal FRPStepInput: %v", err)
	}
	return b
}

func outputPayload(t *testing.T, outs types.OrderedMap[any]) []byte {
	t.Helper()
	b, err := json.Marshal(frp.FRPStepOutput{Outputs: outs})
	if err != nil {
		t.Fatalf("marshal FRPStepOutput: %v", err)
	}
	return b
}

// TestValidateStepInput_ContractHalt proves a rendered `with` input that
// violates the export's declared input schema halts with a plain,
// step-naming error wrapping ErrPipelineContractViolation, while a conforming
// input passes.
func TestValidateStepInput_ContractHalt(t *testing.T) {
	export := wasm.PluginExport{Name: "f", Input: json.RawMessage(nameStringSchema)}
	step := types.PipelineStep{ID: "configure-db", Plugin: "p", Function: "f"}

	// Violation: name is a number, not a string.
	err := validateStepInput(export, inputPayload(t, orderedOf("name", 123)), step)
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Fatalf("violating input: err = %v, want ErrPipelineContractViolation", err)
	}
	if !strings.Contains(err.Error(), "configure-db") || !strings.Contains(err.Error(), "input does not satisfy the declared input contract") {
		t.Errorf("violating input: message %q must name the step and the input contract", err.Error())
	}
	t.Logf("input-halt message: %v", err)

	// Conforming input passes.
	if err := validateStepInput(export, inputPayload(t, orderedOf("name", "db")), step); err != nil {
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
	err := validateStepOutput(export, outputPayload(t, orderedOf("other", "v")), step)
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Fatalf("violating outputs: err = %v, want ErrPipelineContractViolation", err)
	}
	if !strings.Contains(err.Error(), "emit-dsn") || !strings.Contains(err.Error(), "outputs that do not satisfy the declared output contract") {
		t.Errorf("violating outputs: message %q must name the step and the output contract", err.Error())
	}
	t.Logf("output-halt message: %v", err)

	// Conforming outputs pass.
	if err := validateStepOutput(export, outputPayload(t, orderedOf("name", "postgres://…")), step); err != nil {
		t.Errorf("conforming outputs: unexpected error %v", err)
	}
}

// TestValidateStep_NoSchemaUnchanged proves an export that declares no input or
// output schema is unchecked — any value passes, preserving free-form
// pass-through behaviour.
func TestValidateStep_NoSchemaUnchanged(t *testing.T) {
	export := wasm.PluginExport{Name: "f"} // no Input, no Output
	step := types.PipelineStep{ID: "s", Plugin: "p", Function: "f"}

	if err := validateStepInput(export, inputPayload(t, orderedOf("name", 123)), step); err != nil {
		t.Errorf("no input schema: value must pass unchecked, got %v", err)
	}
	if err := validateStepOutput(export, outputPayload(t, orderedOf("anything", true)), step); err != nil {
		t.Errorf("no output schema: value must pass unchecked, got %v", err)
	}
}
