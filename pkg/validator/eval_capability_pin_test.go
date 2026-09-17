// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// The evaluation carve-out is encoded on two layers — the kind's published JSON
// Schema, and the load-time shape rules — and the two are read by different
// authors on different days. Neither layer can see the other, so a document is
// only correctly handled when both reach the same verdict on it; a layer that
// drifts refuses a legitimate document at one gate and admits a forbidden one at
// the other, and nothing in either layer's own tests notices.
//
// The schema half has no other coverage. Nothing else in this repository drives
// a document against the branch condition below, so these are the assertions
// that hold that layer to what it means. They run documents through
// envelope.ValidateBytes — the gate catalog loading and the single-entity check
// both call — rather than against a hand-built validator, so the layer under
// test is the one a user reaches.

// pinCapabilityForm is one authored spelling of a capability, paired with
// whether the evaluation carve-out covers it.
//
// The five forms are the ones that separate the candidate encodings of "is this
// the evaluation capability". A prefix test admits `evaluate`; a split that
// takes the FIRST colon reads `eval:a:b` as the evaluation capability while the
// shape rules, which take the last, do not. Both mistakes are invisible on the
// two forms an author usually writes, which is why the table carries all five.
type pinCapabilityForm struct {
	capability string
	carvedOut  bool
	why        string
}

func evalCapabilityForms() []pinCapabilityForm {
	return []pinCapabilityForm{
		{"eval", true, "the bare capability"},
		{"eval:^1.0.0", true, "a pin selects a plugin version and is not part of the capability's name"},
		{"eval:a:b", false, "the name is everything before the LAST colon, so this names the capability eval:a"},
		{"evaluate", false, "a longer capability that merely begins with the same letters"},
		{"evaluate:1.0.0", false, "the same longer capability, pinned"},
	}
}

// recipeDoc renders a Recipe carrying capability and whatever the body adds.
func recipeDoc(capability, body string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: pin-probe
  version: "1.0.0"
spec:
  capability: "` + capability + `"
  implements: "core/probe"
  provides:
    endpoint: "localhost"
` + body)
}

// featureRecipeDoc renders a FeatureRecipe carrying capability and whatever the
// body adds.
func featureRecipeDoc(capability, body string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: pin-probe
  version: "1.0.0"
spec:
  capability: "` + capability + `"
  implements: "core/probe"
` + body)
}

// The rendering fields the carve-out forbids, in the authored form each takes.
const (
	bodyNone      = ""
	bodyArtifacts = "  artifacts:\n    note.yaml: \"hello\"\n"
	bodySource    = "  source: ./chart\n"
	bodyWorkload  = "  workload: |\n    apiVersion: aggrega.ai/v1alpha1\n    kind: Workload\n"
	bodyMapping   = "  valuesMapping: |\n    key: value\n"
)

// schemaRefused reports whether the kind's published schema refused doc, and
// fails the test if the refusal carried some other condition.
//
// Every envelope refusal carries one condition, so the assertion here says the
// schema refused rather than which clause of it did. What pins the clause is the
// positive control each caller pairs with the refusal: the same document with
// the one offending field removed, which must be accepted. A refusal and an
// acceptance differing by a single field cannot both be explained by an
// unrelated schema rule.
func schemaRefused(t *testing.T, kind envelope.Kind, doc []byte) bool {
	t.Helper()
	_, err := envelope.ValidateBytes(kind, doc, "pin-probe.yaml")
	if err == nil {
		return false
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Fatalf("a schema refusal must carry %v; got: %v", envelope.ErrEntityEnvelope(), err)
	}
	return true
}

// TestEvalCarveOutReadsTheBareCapabilityOnBothLayers drives every capability
// form through both layers and asserts the four verdicts agree.
//
// Each form is read four ways: the schema's forbidding branch (a rendering field
// declared), the schema's exempting branch (no input kind declared at all), and
// the two shape rules that say the same two things. A row that agrees with
// itself on three of the four and disagrees on the fourth is the drift this
// pair of layers exists to make impossible, and it is the thing no single-layer
// test can see.
func TestEvalCarveOutReadsTheBareCapabilityOnBothLayers(t *testing.T) {
	t.Parallel()

	for _, form := range evalCapabilityForms() {
		form := form
		t.Run(form.capability, func(t *testing.T) {
			t.Parallel()

			// The projection the shape rules read the capability through.
			if got := types.BareCapability(form.capability); (got == "eval") != form.carvedOut {
				t.Fatalf("BareCapability(%q) = %q, which makes the carve-out %v; want %v (%s)",
					form.capability, got, got == "eval", form.carvedOut, form.why)
			}

			// Layer 1, forbidding half: a rendering field is declared.
			schemaForbids := schemaRefused(t, envelope.KindRecipe, recipeDoc(form.capability, bodyArtifacts))
			if schemaForbids != form.carvedOut {
				t.Errorf("the schema refused a Recipe on %q declaring artifacts: %v; want %v (%s)",
					form.capability, schemaForbids, form.carvedOut, form.why)
			}

			// Layer 2, forbidding half: the same document, the same question.
			ruleErr := validator.ValidateRecipe(withArtifacts(newRecipe(form.capability)), tFile)
			ruleForbids := errors.Is(ruleErr, validator.ErrRecipeEvalProviderConstraint())
			if ruleForbids != form.carvedOut {
				t.Errorf("the shape rule refused a Recipe on %q declaring artifacts under the eval condition: %v; want %v (got: %v)",
					form.capability, ruleForbids, form.carvedOut, ruleErr)
			}

			// Layer 1, exempting half: no input kind at all.
			schemaExempts := !schemaRefused(t, envelope.KindRecipe, recipeDoc(form.capability, bodyNone))
			if schemaExempts != form.carvedOut {
				t.Errorf("the schema accepted a Recipe on %q declaring no input kind: %v; want %v (%s)",
					form.capability, schemaExempts, form.carvedOut, form.why)
			}

			// Layer 2, exempting half: the same document, the same question.
			exemptErr := validator.ValidateRecipe(newRecipe(form.capability), tFile)
			ruleExempts := !errors.Is(exemptErr, validator.ErrRecipeNoInputKind())
			if ruleExempts != form.carvedOut {
				t.Errorf("the shape rule exempted a Recipe on %q from the input-kinds condition: %v; want %v (got: %v)",
					form.capability, ruleExempts, form.carvedOut, exemptErr)
			}
		})
	}
}

// TestEvalCarveOutReadsTheBareCapabilityOnBothLayers_FeatureRecipe is the same
// agreement over the other kind the carve-out reaches.
//
// A FeatureRecipe carries only the forbidding half — it has no
// at-least-one-input-kind rule, deliberately, because a pure-wiring one is
// valid — so the exempting rows above have no counterpart here. The two kinds
// are asserted separately rather than through one table because they are two
// published schemas and two rule declarations: a repair applied to one and not
// the other is exactly the shape that would pass a shared table driven off one
// kind.
func TestEvalCarveOutReadsTheBareCapabilityOnBothLayers_FeatureRecipe(t *testing.T) {
	t.Parallel()

	for _, form := range evalCapabilityForms() {
		form := form
		t.Run(form.capability, func(t *testing.T) {
			t.Parallel()

			schemaForbids := schemaRefused(t, envelope.KindFeatureRecipe, featureRecipeDoc(form.capability, bodyArtifacts))
			if schemaForbids != form.carvedOut {
				t.Errorf("the schema refused a FeatureRecipe on %q declaring artifacts: %v; want %v (%s)",
					form.capability, schemaForbids, form.carvedOut, form.why)
			}

			ruleErr := validator.ValidateFeatureRecipe(featureRecipeWithArtifacts(newFeatureRecipe(form.capability)), tFile)
			ruleForbids := errors.Is(ruleErr, validator.ErrFeatureRecipeEvalProviderConstraint())
			if ruleForbids != form.carvedOut {
				t.Errorf("the shape rule refused a FeatureRecipe on %q declaring artifacts under the eval condition: %v; want %v (got: %v)",
					form.capability, ruleForbids, form.carvedOut, ruleErr)
			}

			// A FeatureRecipe declaring nothing is valid on every capability, so
			// this row asserts the carve-out never turned into a requirement.
			if schemaRefused(t, envelope.KindFeatureRecipe, featureRecipeDoc(form.capability, bodyNone)) {
				t.Errorf("a pure-wiring FeatureRecipe on %q must be accepted", form.capability)
			}
		})
	}
}

// TestEvalCarveOutSchemaRefusesEveryRenderingFieldOnAPinnedCapability is the
// per-field negative corpus for the published schemas.
//
// Each row declares exactly one forbidden field on a version-pinned evaluation
// capability and must be refused; its positive control is the same document with
// that field removed, which must be accepted. The pair is what makes the refusal
// attributable: the documents differ by one field, so no other schema rule
// accounts for the difference.
//
// The capability is pinned on every row deliberately. The unpinned forms are
// covered by the agreement tables above; what these rows hold is the half a
// reader is most likely to assume works because the bare form does.
func TestEvalCarveOutSchemaRefusesEveryRenderingFieldOnAPinnedCapability(t *testing.T) {
	t.Parallel()

	const pinned = "eval:^1.0.0"

	for _, kind := range []struct {
		name  string
		kind  envelope.Kind
		build func(capability, body string) []byte
	}{
		{"Recipe", envelope.KindRecipe, recipeDoc},
		{"FeatureRecipe", envelope.KindFeatureRecipe, featureRecipeDoc},
	} {
		kind := kind
		for _, field := range []struct {
			name string
			body string
		}{
			{"source", bodySource},
			{"valuesMapping", bodyMapping},
			{"workload", bodyWorkload},
			{"artifacts", bodyArtifacts},
		} {
			field := field
			t.Run(kind.name+"/"+field.name, func(t *testing.T) {
				t.Parallel()

				if !schemaRefused(t, kind.kind, kind.build(pinned, field.body)) {
					t.Fatalf("a %s on %q declaring %s must be refused by its schema",
						kind.name, pinned, field.name)
				}
				if schemaRefused(t, kind.kind, kind.build(pinned, bodyNone)) {
					t.Fatalf("the control for %s/%s must be accepted: the same document without the %s block",
						kind.name, field.name, field.name)
				}
			})
		}
	}
}
