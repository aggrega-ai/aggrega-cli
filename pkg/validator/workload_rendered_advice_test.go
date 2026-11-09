// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The advice a rejected Workload value carries when the rendered document
// holds it as something other than text.
//
// A Workload is a document nobody authors directly — a Recipe's template
// produces it — so a value that arrives as a number arrives that way because
// the expression that produced it was left unquoted. The tests below drive the
// real bytes entry point rather than constructing findings, because what the
// advice is derived from is the rendered document itself: a fabricated finding
// would not carry one.

const adviceOpening = "YAML read this unquoted value as a"

// renderedWorkload is the shape a compose or chart-wrap Recipe's template
// produces for a single-container workload, with the sizing quantity left as
// the caller supplies it. Everything else is held fixed so each case fails on
// its own fault and on no other.
func renderedWorkload(cpu string) string {
	return "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Workload\n" +
		"containers:\n" +
		"  main:\n" +
		"    image: redis:8.8-alpine\n" +
		"    resources:\n" +
		"      requests:\n" +
		"        cpu: " + cpu + "\n" +
		"        memory: 512Mi\n"
}

// TestWorkloadBytes_QuotedSizingQuantityPasses is the control the refusal below
// is read against. The operator's value is the same either way — a whole-core
// count — and the only difference is whether the Recipe quoted the expression
// that rendered it.
func TestWorkloadBytes_QuotedSizingQuantityPasses(t *testing.T) {
	t.Parallel()
	if err := validator.ValidateWorkloadBytes(
		[]byte(renderedWorkload(`"2"`)), "recipe.yaml", "redis", "1.0.0",
	); err != nil {
		t.Fatalf("a quoted sizing quantity must validate; got %v", err)
	}
}

// TestWorkloadBytes_UnquotedSizingQuantityIsRefusedAndSaysWhatToChange is the
// other leg: the identical value, unquoted by the Recipe, is refused — and the
// refusal names the remedy rather than only the fault.
func TestWorkloadBytes_UnquotedSizingQuantityIsRefusedAndSaysWhatToChange(t *testing.T) {
	t.Parallel()
	err := validator.ValidateWorkloadBytes(
		[]byte(renderedWorkload("2")), "recipe.yaml", "redis", "1.0.0",
	)
	if !errors.Is(err, validator.ErrWorkloadValidation()) {
		t.Fatalf("an unquoted whole-core quantity must be refused; got %v", err)
	}
	message := err.Error()
	// The verdict the schema engine reached is unchanged and still leads.
	if !strings.Contains(message, `want "string"`) {
		t.Errorf("the refusal must still name the type expected; got:\n%s", message)
	}
	// What this test exists for: the remedy, which the verdict alone never said.
	if !strings.Contains(message, adviceOpening+" number") {
		t.Errorf("the refusal must say what YAML made of the value; got:\n%s", message)
	}
	if !strings.Contains(message, "quote the expression in the Recipe") {
		t.Errorf("the refusal must name the remedy; got:\n%s", message)
	}
}

// TestWorkloadBytes_UnquotedBooleanIsRefusedAndSaysWhatToChange covers the
// other shape an unquoted expansion takes. An image tag that reads as a boolean
// is the same fault as a sizing quantity that reads as a number, so it earns
// the same advice with the type word that actually applies.
func TestWorkloadBytes_UnquotedBooleanIsRefusedAndSaysWhatToChange(t *testing.T) {
	t.Parallel()
	document := "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Workload\n" +
		"containers:\n" +
		"  main:\n" +
		"    image: true\n"
	err := validator.ValidateWorkloadBytes([]byte(document), "recipe.yaml", "redis", "1.0.0")
	if !errors.Is(err, validator.ErrWorkloadValidation()) {
		t.Fatalf("an unquoted boolean where text is required must be refused; got %v", err)
	}
	if message := err.Error(); !strings.Contains(message, adviceOpening+" boolean") {
		t.Errorf("the refusal must name the type YAML read; got:\n%s", message)
	}
}

// TestWorkloadBytes_ARefusedTextValueCarriesNoQuotingAdvice is what keeps the
// advice from being a blanket sentence on every Workload refusal. The value
// here is already text and quoting it changes nothing, so advice about quoting
// would be noise a reader has to learn to ignore.
func TestWorkloadBytes_ARefusedTextValueCarriesNoQuotingAdvice(t *testing.T) {
	t.Parallel()
	document := "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Workload\n" +
		"containers:\n" +
		"  main:\n" +
		"    image: app:1.0\n" +
		"services:\n" +
		"  default:\n" +
		"    type: NotAServiceType\n"
	err := validator.ValidateWorkloadBytes([]byte(document), "recipe.yaml", "redis", "1.0.0")
	if !errors.Is(err, validator.ErrWorkloadValidation()) {
		t.Fatalf("an unknown service type must be refused; got %v", err)
	}
	if message := err.Error(); strings.Contains(message, adviceOpening) {
		t.Errorf("a value that is already text must carry no quoting advice; got:\n%s", message)
	}
}
