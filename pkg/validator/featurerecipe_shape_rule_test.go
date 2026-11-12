// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The rules over a FeatureRecipe's authored surface read which of the authored
// blocks the document declares, and nothing about the capability it binds. A
// FeatureRecipe owes no rendering block at all — a pure-wiring one is valid — and
// the one shape it may not take is an overlay with no source to overlay.

// featureRecipeWithSource returns fr carrying a source pointer.
func featureRecipeWithSource(fr *types.FeatureRecipe) *types.FeatureRecipe {
	fr.Spec.Source = "./chart"
	return fr
}

// featureRecipeWithValuesMapping returns fr carrying an inline values overlay.
func featureRecipeWithValuesMapping(fr *types.FeatureRecipe) *types.FeatureRecipe {
	fr.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "demo:1.0"}`}
	return fr
}

// featureRecipeWithWorkload returns fr carrying an inline workload body.
func featureRecipeWithWorkload(fr *types.FeatureRecipe) *types.FeatureRecipe {
	fr.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1beta1\nkind: Workload"}
	return fr
}

// featureRecipeWithArtifacts returns fr carrying one artifact entry.
func featureRecipeWithArtifacts(fr *types.FeatureRecipe) *types.FeatureRecipe {
	fr.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	fr.Spec.Artifacts.Set("deploy.yaml", types.ArtifactEntry{Import: "./manifests/deploy.yaml"})
	return fr
}

// featureRecipeWithInject returns fr carrying an inject mapping.
func featureRecipeWithInject(fr *types.FeatureRecipe) *types.FeatureRecipe {
	fr.Spec.InjectMapping = `{"env": {"X": "y"}}`
	return fr
}

// TestValidateFeatureRecipe_ShapeSurface_Grid drives the shape rules over the
// blocks a FeatureRecipe may declare.
//
// A FeatureRecipe declaring no block at all must be accepted whatever its
// capability — there is deliberately no at-least-one-input-kind rule on this
// kind, and a row asserting it is what would catch one being added. Any render
// block is accepted on any capability, `eval` included: no capability name forbids
// one. And the overlay-without-source fault must be refused under the
// values-mapping condition on every capability.
//
// Each row asserts a condition identity rather than "some error", because the
// rules run in sequence over one document: a row asserting only that something
// was refused keeps passing once a different rule starts refusing it first, and
// stops covering the rule it was written for without saying so.
func TestValidateFeatureRecipe_ShapeSurface_Grid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// doc is the document under test, built fresh per row.
		doc func() *types.FeatureRecipe
		// want is the condition the document must be refused under, or nil
		// where the document must be accepted.
		want error
	}{
		{
			name: "a FeatureRecipe declaring no block at all is accepted, because this kind has no input-kind rule",
			doc:  func() *types.FeatureRecipe { return newFeatureRecipe("helm") },
			want: nil,
		},
		{
			name: "a FeatureRecipe binding the capability eval and declaring no block at all is accepted",
			doc:  func() *types.FeatureRecipe { return newFeatureRecipe("eval") },
			want: nil,
		},
		{
			name: "a FeatureRecipe binding the capability eval and declaring artifacts is accepted",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithArtifacts(newFeatureRecipe("eval")) },
			want: nil,
		},
		{
			name: "a FeatureRecipe declaring only an inject mapping is accepted",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithInject(newFeatureRecipe("helm")) },
			want: nil,
		},
		{
			name: "a FeatureRecipe on another capability declaring a source is accepted",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithSource(newFeatureRecipe("helm")) },
			want: nil,
		},
		{
			name: "a FeatureRecipe on another capability declaring only a workload is accepted",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithWorkload(newFeatureRecipe("helm")) },
			want: nil,
		},
		{
			name: "a FeatureRecipe on another capability declaring only artifacts is accepted",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithArtifacts(newFeatureRecipe("helm")) },
			want: nil,
		},
		{
			name: "a FeatureRecipe on another capability declaring every block is accepted",
			doc: func() *types.FeatureRecipe {
				return featureRecipeWithValuesMapping(
					featureRecipeWithArtifacts(
						featureRecipeWithWorkload(featureRecipeWithSource(newFeatureRecipe("helm")))))
			},
			want: nil,
		},
		{
			name: "a FeatureRecipe on another capability declaring an overlay with no source is refused under the values-mapping condition",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithValuesMapping(newFeatureRecipe("helm")) },
			want: validator.ErrFeatureRecipeValuesMappingRequiresSource(),
		},
		{
			name: "a FeatureRecipe binding the capability eval and declaring an overlay with no source is refused under the values-mapping condition",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithValuesMapping(newFeatureRecipe("eval")) },
			want: validator.ErrFeatureRecipeValuesMappingRequiresSource(),
		},
		{
			name: "a FeatureRecipe declaring no capability at all is refused under the values-mapping condition",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithValuesMapping(newFeatureRecipe("")) },
			want: validator.ErrFeatureRecipeValuesMappingRequiresSource(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validator.ValidateFeatureRecipe(tc.doc(), tFile)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("the document must be accepted; got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the document must be refused under %v; got nil", tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("the refusal must carry %v; got: %v", tc.want, err)
			}
		})
	}
}

// TestValidateFeatureRecipe_ValuesMappingRuleCarriesItsRemedyAndItsRegisteredSentence
// asserts the refusal an author actually reads: the registered sentence, the
// rule identifier, the position, and the remedy — the only part of the message
// saying what to write instead. The sentence is taken off the condition rather
// than restated here, so this cannot pass against a second copy of the wording.
func TestValidateFeatureRecipe_ValuesMappingRuleCarriesItsRemedyAndItsRegisteredSentence(t *testing.T) {
	t.Parallel()

	err := validator.ValidateFeatureRecipe(featureRecipeWithValuesMapping(newFeatureRecipe("helm")), tFile)
	if err == nil {
		t.Fatal("a FeatureRecipe declaring an overlay with no source must be refused; got nil")
	}
	msg := err.Error()

	if sentence := validator.ErrFeatureRecipeValuesMappingRequiresSource().Error(); !strings.HasPrefix(msg, sentence+" (") {
		t.Errorf("the message must open with the registered sentence %q; got: %s", sentence, msg)
	}
	for _, want := range []string{
		"FeatureRecipe " + tName + ":" + tVer,
		"rule values-mapping",
		tFile,
		"declare a source: pointer alongside valuesMapping " +
			"(or remove the valuesMapping if no overlay is intended)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the diagnostic must carry %q; got: %s", want, msg)
		}
	}
}
