// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestValueTemplateRunFailure_NamesARunOnlyWhereNothingElseNamedIt holds the
// value-template condition to the failures it names, through the evaluators the
// value surfaces call.
//
// A run that fails names it, on the Recipe-authored surfaces and on the
// Aggregate-authored self-inject surface alike, and keeps the engine's reason as
// its cause. A template that does not parse is not a run that failed and names no
// condition. A failure already naming a condition keeps that one, because the
// condition read off a chain is the first one found. A template that runs is the
// control.
func TestValueTemplateRunFailure_NamesARunOnlyWhereNothingElseNamedIt(t *testing.T) {
	t.Parallel()
	condition := ErrValueTemplateExecutionFailed()
	code, ok := cerrors.CodeOf(condition)
	if !ok || code == "" {
		t.Fatal("the value-template condition reports no code")
	}
	const absentRead = "nil pointer evaluating interface {}.tag"
	// The values pillar holds a key, so .values is a map and the read fails one
	// level down, at the key it does not hold, as an author's typo does.
	values := types.OrderedMap[any]{}
	values.Set("present", "x")

	t.Run("a Recipe value template failing while it runs names the condition", func(t *testing.T) {
		t.Parallel()
		_, err := EvalTemplate("workload[probe]", "image: {{ .values.nosuch.tag }}", ProvidesContext{Values: values}, redact.Keys{})
		if err == nil {
			t.Fatal("the template ran; want it to fail reading through an absent map")
		}
		if !errors.Is(err, condition) {
			t.Errorf("the failure does not carry the value-template condition: %s", cerrors.CauseChain(err))
		}
		if got, _ := cerrors.CodeOf(err); got != code {
			t.Errorf("the chain's first condition is %q, want %q: %s", got, code, cerrors.CauseChain(err))
		}
		if !strings.Contains(cerrors.CauseChain(err), absentRead) {
			t.Errorf("the failure no longer carries the engine's reason %q: %s", absentRead, cerrors.CauseChain(err))
		}
	})

	t.Run("an Aggregate self-inject value failing while it runs names the condition on the developer's surface", func(t *testing.T) {
		t.Parallel()
		_, err := evalSelfInjectValue("self-inject.env[PROBE]", "{{ .values.nosuch.tag }}", SelfInjectContext{Values: values}, redact.Keys{})
		if err == nil {
			t.Fatal("the template ran; want it to fail reading through an absent map")
		}
		if !errors.Is(err, condition) {
			t.Errorf("the failure does not carry the value-template condition: %s", cerrors.CauseChain(err))
		}
		var envelope *cerrors.CompilerError
		if !errors.As(err, &envelope) || envelope.Category() != cerrors.DeveloperError {
			t.Errorf("the failure is not filed on the developer's surface: %s", cerrors.CauseChain(err))
		}
	})

	t.Run("a template that does not parse names no condition", func(t *testing.T) {
		t.Parallel()
		_, err := EvalTemplate("workload[probe]", "image: {{ .values.tag ", ProvidesContext{}, redact.Keys{})
		if err == nil {
			t.Fatal("the template parsed; want an unterminated action to fail its parse")
		}
		if errors.Is(err, condition) {
			t.Errorf("a parse failure carries the condition for a template that ran: %s", cerrors.CauseChain(err))
		}
		if got, named := cerrors.CodeOf(err); named {
			t.Errorf("a parse failure names the condition %q: %s", got, cerrors.CauseChain(err))
		}
	})

	t.Run("a failure already naming a condition keeps it", func(t *testing.T) {
		t.Parallel()
		named := fmt.Errorf("a function's own fault: %w", ErrProvidesEvaluation())
		want, _ := cerrors.CodeOf(named)
		got := valueTemplateRunFailure(stageExecute, named)
		if got != named {
			t.Errorf("a named failure was rewrapped: %s", cerrors.CauseChain(got))
		}
		if first, _ := cerrors.CodeOf(got); first != want {
			t.Errorf("the chain's first condition is %q, want the one it already named, %q", first, want)
		}
	})

	t.Run("a template that runs reports nothing", func(t *testing.T) {
		t.Parallel()
		out, err := EvalTemplate("workload[probe]", "image: {{ .context.instance_id }}",
			ProvidesContext{Context: ProvidesContextBlock{InstanceID: "probe"}}, redact.Keys{})
		if err != nil {
			t.Fatalf("the control failed: %s", cerrors.CauseChain(err))
		}
		if out != "image: probe" {
			t.Errorf("the control rendered %q, want %q", out, "image: probe")
		}
	})
}
