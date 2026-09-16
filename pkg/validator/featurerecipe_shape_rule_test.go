// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The two rules over a FeatureRecipe's authored surface are gated on two
// conditions between them — the capability the document declares, and which of
// the four authored blocks it declares — so what they specify is a grid rather
// than two lists. Testing each condition on its own would state the grid's
// margins and leave its interior unsaid, and the interior is where the pair
// overlaps: a document declaring an overlay with no source satisfies both rules'
// conditions at once, and which of the two refuses it is a contract rather than
// an accident.

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
	fr.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"}
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

// TestValidateFeatureRecipe_ShapeSurface_Grid drives both rules over the cells of
// the pair of conditions that gate them, rather than over each condition alone.
//
// The cells that carry the weight each fail differently if a gate is dropped. A
// FeatureRecipe declaring no block at all must be accepted whatever its
// capability — there is deliberately no at-least-one-input-kind rule on this
// kind, and a row asserting it is what would catch one being added. An eval
// FeatureRecipe declaring any render block must be refused under the eval
// condition. A FeatureRecipe on any other capability declaring the same blocks
// must not reach that verdict, which is what the capability gate on the eval rule
// buys. And the overlay-without-source fault must be refused under the
// values-mapping condition on every capability the eval rule does not cover.
//
// Each row asserts a condition identity rather than "some error", because the two
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
			name: "an eval FeatureRecipe declaring no block at all is accepted",
			doc:  func() *types.FeatureRecipe { return newFeatureRecipe("eval") },
			want: nil,
		},
		{
			name: "an eval FeatureRecipe declaring only an inject mapping is accepted",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithInject(newFeatureRecipe("eval")) },
			want: nil,
		},
		{
			name: "an eval FeatureRecipe declaring a source is refused under the eval condition",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithSource(newFeatureRecipe("eval")) },
			want: validator.ErrFeatureRecipeEvalProviderConstraint(),
		},
		{
			name: "an eval FeatureRecipe declaring a workload is refused under the eval condition",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithWorkload(newFeatureRecipe("eval")) },
			want: validator.ErrFeatureRecipeEvalProviderConstraint(),
		},
		{
			name: "an eval FeatureRecipe declaring artifacts is refused under the eval condition",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithArtifacts(newFeatureRecipe("eval")) },
			want: validator.ErrFeatureRecipeEvalProviderConstraint(),
		},
		{
			// The document also violates the values-mapping rule — an overlay
			// with no source — so the row pins which of the two refuses it.
			name: "an eval FeatureRecipe declaring a values overlay is refused under the eval condition rather than the values-mapping one",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithValuesMapping(newFeatureRecipe("eval")) },
			want: validator.ErrFeatureRecipeEvalProviderConstraint(),
		},
		{
			name: "an eval FeatureRecipe declaring a source alongside its overlay is still refused under the eval condition",
			doc: func() *types.FeatureRecipe {
				return featureRecipeWithValuesMapping(featureRecipeWithSource(newFeatureRecipe("eval")))
			},
			want: validator.ErrFeatureRecipeEvalProviderConstraint(),
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
			// A pinned capability declaration is a different string from the
			// bare name, so it falls outside the eval rule's exact comparison
			// and the values-mapping rule reaches it. The row records the
			// behaviour rather than endorsing it: it is what the shipped code
			// does on either side of this rule's declaration.
			name: "a FeatureRecipe pinning the eval capability is outside the eval rule's exact comparison",
			doc:  func() *types.FeatureRecipe { return featureRecipeWithValuesMapping(newFeatureRecipe("eval:^1.0.0")) },
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

// TestValidateFeatureRecipe_EvalRuleIsSettledBeforeTheValuesMappingRule pins the
// precedence between the two rules on the one document both of them object to.
//
// An eval FeatureRecipe declaring an overlay and no source satisfies both
// conditions: the overlay is a render block the eval rule forbids, and it has no
// source for the values-mapping rule to find. The eval finding is the one an
// author needs — the overlay is forbidden on that capability whatever else the
// document says, so being told to add a source would send them further from a
// document that loads.
//
// The precedence is held by the order the two rules are invoked in and by the
// caller returning the first failure, so nothing in either declaration states it
// and nothing else in the tree asserts it. This test is what turns an inverted
// invocation order into a red rather than a quietly different diagnostic. It
// asserts the condition that must NOT be reported as well as the one that must,
// because an inversion produces a refusal either way and a test checking only
// that something was refused would pass through it.
func TestValidateFeatureRecipe_EvalRuleIsSettledBeforeTheValuesMappingRule(t *testing.T) {
	t.Parallel()

	err := validator.ValidateFeatureRecipe(
		featureRecipeWithValuesMapping(newFeatureRecipe("eval")), tFile)
	if err == nil {
		t.Fatal("an eval FeatureRecipe declaring a values overlay must be refused; got nil")
	}
	if !errors.Is(err, validator.ErrFeatureRecipeEvalProviderConstraint()) {
		t.Fatalf("the refusal must carry the eval-provider condition; got: %v", err)
	}
	if errors.Is(err, validator.ErrFeatureRecipeValuesMappingRequiresSource()) {
		t.Fatalf("the values-mapping condition must not be the one reported here; got: %v", err)
	}
}

// TestValidateFeatureRecipe_EvalRuleNamesTheBlocksInThisKindsOwnOrder pins the
// order the eval refusal names the offending blocks in, on both authored kinds.
//
// The order is part of the contract rather than an artifact of how each rule is
// written: identical input produces identical output, so a document declaring
// several blocks names them the same way on every run and in every reader's diff,
// and each kind's reference entry quotes its own list.
//
// The two kinds order differently, and one test drives both because the failure
// worth catching is a unification. Someone reading the two rules side by side and
// "fixing" the apparent inconsistency would align one order to the other, and a
// test that knew only one kind's order would still pass.
func TestValidateFeatureRecipe_EvalRuleNamesTheBlocksInThisKindsOwnOrder(t *testing.T) {
	t.Parallel()

	// Asserted as the message's tail rather than as a substring anywhere in it:
	// the advice closes the diagnostic, so a suffix match also pins that nothing
	// follows the list and that no block was appended after it.
	const wantFeatureRecipe = "forbidden fields declared: source, valuesMapping, workload, artifacts"
	const wantRecipe = "forbidden fields declared: source, workload, artifacts, valuesMapping"

	frErr := validator.ValidateFeatureRecipe(
		featureRecipeWithValuesMapping(
			featureRecipeWithArtifacts(
				featureRecipeWithWorkload(featureRecipeWithSource(newFeatureRecipe("eval"))))),
		tFile)
	if frErr == nil {
		t.Fatal("an eval FeatureRecipe declaring every render block must be refused; got nil")
	}
	if msg := frErr.Error(); !strings.HasSuffix(msg, wantFeatureRecipe) {
		t.Errorf("the FeatureRecipe diagnostic must close by naming the blocks as %q; got: %s",
			wantFeatureRecipe, msg)
	}

	rErr := validator.ValidateRecipe(
		withValuesMapping(withArtifacts(withWorkload(withSource(newRecipe("eval"))))), tFile)
	if rErr == nil {
		t.Fatal("an eval Recipe declaring every input-kind block must be refused; got nil")
	}
	if msg := rErr.Error(); !strings.HasSuffix(msg, wantRecipe) {
		t.Errorf("the Recipe diagnostic must close by naming the blocks as %q; got: %s",
			wantRecipe, msg)
	}
}

// TestValidateFeatureRecipe_EvalRuleNamesOnlyTheBlocksTheDocumentDeclares asserts
// the refusal reports the occurrence rather than the rule's whole field set.
//
// A FeatureRecipe declaring one block and being told four are forbidden has to
// work out which of them it wrote, which is the difference between a diagnostic
// and a restatement of the rule.
func TestValidateFeatureRecipe_EvalRuleNamesOnlyTheBlocksTheDocumentDeclares(t *testing.T) {
	t.Parallel()

	err := validator.ValidateFeatureRecipe(featureRecipeWithWorkload(newFeatureRecipe("eval")), tFile)
	if err == nil {
		t.Fatal("an eval FeatureRecipe declaring a workload must be refused; got nil")
	}
	// The suffix match carries both halves at once: the one block the document
	// declares is named, and nothing follows it — so a rule that reported its
	// whole field set rather than the occurrence would fail here.
	if msg := err.Error(); !strings.HasSuffix(msg, "forbidden fields declared: workload") {
		t.Errorf("the diagnostic must close by naming the one block declared; got: %s", msg)
	}
}

// TestValidateFeatureRecipe_EvalRuleCarriesItsRegisteredSentenceAndPosition
// asserts the refusal an author actually reads.
//
// The sentence is taken off the condition rather than restated here, so this
// cannot pass against a second copy of the wording composed from the failure's
// fields.
func TestValidateFeatureRecipe_EvalRuleCarriesItsRegisteredSentenceAndPosition(t *testing.T) {
	t.Parallel()

	err := validator.ValidateFeatureRecipe(featureRecipeWithSource(newFeatureRecipe("eval")), tFile)
	if err == nil {
		t.Fatal("an eval FeatureRecipe declaring a source must be refused; got nil")
	}
	msg := err.Error()

	if sentence := validator.ErrFeatureRecipeEvalProviderConstraint().Error(); !strings.HasPrefix(msg, sentence+" (") {
		t.Errorf("the message must open with the registered sentence %q; got: %s", sentence, msg)
	}
	for _, want := range []string{
		"FeatureRecipe " + tName + ":" + tVer,
		"rule eval",
		tFile,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the diagnostic must carry %q; got: %s", want, msg)
		}
	}
}

// TestValidateFeatureRecipe_ValuesMappingRuleCarriesItsRemedyAndItsRegisteredSentence
// asserts the same contract on the values-mapping refusal, whose rule identifier
// differs and whose remedy is the only part of the message saying what to write
// instead.
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
