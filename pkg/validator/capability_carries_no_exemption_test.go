// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// No capability name changes what a Recipe or a FeatureRecipe must declare, and
// the rule is written on two layers — the kind's published JSON Schema and the
// load-time shape rules — which are read by different authors on different days.
// A layer that grew a capability carve-out would admit at one gate what the other
// refuses, and neither layer's own tests would notice. These tests drive the same
// documents through both and hold them to one verdict.
//
// `eval` is driven alongside a provider's name because a capability that renders
// nothing and only evaluates values is the most plausible place for such a
// carve-out to be written.

// capabilityRecipeDoc renders a Recipe binding capability, publishing one value,
// with whatever the body adds.
func capabilityRecipeDoc(capability, body string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: probe
  version: "1.0.0"
spec:
  capability: "` + capability + `"
  implements: "core/probe"
  provides:
    endpoint: "localhost"
` + body)
}

// capabilityFeatureRecipeDoc renders a FeatureRecipe binding capability, with
// whatever the body adds.
func capabilityFeatureRecipeDoc(capability, body string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: probe
  version: "1.0.0"
spec:
  capability: "` + capability + `"
  implements: "core/probe"
` + body)
}

const capabilityBodyArtifacts = "  artifacts:\n    note.yaml: \"hello\"\n"

// refusedBySchema reports whether the kind's published schema refused doc, and
// fails the test if the refusal carried some other condition.
func refusedBySchema(t *testing.T, kind envelope.Kind, doc []byte) bool {
	t.Helper()
	_, err := envelope.ValidateBytes(kind, doc, "probe.yaml")
	if err == nil {
		return false
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Fatalf("a schema refusal must carry %v; got: %v", envelope.ErrEntityEnvelope(), err)
	}
	return true
}

// TestNoCapabilityExemptsARecipeFromDeclaringAnInputKind holds both layers to
// one verdict for a Recipe on each capability: declaring only the values it
// publishes is refused, and the same document with one artifact entry is
// accepted — the pair differing by one block, so no other rule explains it.
func TestNoCapabilityExemptsARecipeFromDeclaringAnInputKind(t *testing.T) {
	t.Parallel()
	for _, capability := range []string{"eval", "eval:^1.0.0", "helm"} {
		t.Run(capability, func(t *testing.T) {
			t.Parallel()

			if !refusedBySchema(t, envelope.KindRecipe, capabilityRecipeDoc(capability, "")) {
				t.Errorf("the schema accepted a Recipe on %q declaring no input kind", capability)
			}
			if err := validator.ValidateRecipe(newRecipe(capability), tFile); !errors.Is(err, validator.ErrRecipeNoInputKind()) {
				t.Errorf("the shape rule did not refuse a Recipe on %q declaring no input kind under the input-kinds condition; got: %v",
					capability, err)
			}

			if refusedBySchema(t, envelope.KindRecipe, capabilityRecipeDoc(capability, capabilityBodyArtifacts)) {
				t.Errorf("the schema refused a Recipe on %q declaring an artifact entry", capability)
			}
			if err := validator.ValidateRecipe(withArtifacts(newRecipe(capability)), tFile); err != nil {
				t.Errorf("the shape rule refused a Recipe on %q declaring an artifact entry: %v", capability, err)
			}
		})
	}
}

// TestNoCapabilityForbidsAFeatureRecipeARenderBlock holds both layers to one
// verdict for a FeatureRecipe on each capability: a pure-wiring one is accepted,
// and so is one declaring an artifact entry.
func TestNoCapabilityForbidsAFeatureRecipeARenderBlock(t *testing.T) {
	t.Parallel()
	for _, capability := range []string{"eval", "eval:^1.0.0", "helm"} {
		t.Run(capability, func(t *testing.T) {
			t.Parallel()
			for _, body := range []string{"", capabilityBodyArtifacts} {
				if refusedBySchema(t, envelope.KindFeatureRecipe, capabilityFeatureRecipeDoc(capability, body)) {
					t.Errorf("the schema refused a FeatureRecipe on %q with body %q", capability, body)
				}
			}
			if err := validator.ValidateFeatureRecipe(featureRecipeWithArtifacts(newFeatureRecipe(capability)), tFile); err != nil {
				t.Errorf("the shape rules refused a FeatureRecipe on %q declaring an artifact entry: %v", capability, err)
			}
		})
	}
}
