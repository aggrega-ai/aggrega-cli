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

// The two rules over a Recipe's input-kind surface are gated on two conditions
// between them — the capability the Recipe declares, and which of the four
// authored blocks it declares — so what they specify is a grid rather than two
// lists. Testing each condition on its own would state the grid's margins and
// leave its interior unsaid, which is exactly where the eval carve-out lives:
// one rule refuses the surface an eval Recipe must not declare, and the other
// exempts an eval Recipe from having to declare one. The tests below enumerate
// the cells.

// withSource returns r carrying a source pointer.
func withSource(r *types.Recipe) *types.Recipe {
	r.Spec.Source = "./chart"
	return r
}

// withWorkload returns r carrying an inline workload body.
func withWorkload(r *types.Recipe) *types.Recipe {
	r.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"}
	return r
}

// withArtifacts returns r carrying one artifact entry.
func withArtifacts(r *types.Recipe) *types.Recipe {
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("deploy.yaml", types.ArtifactEntry{Import: "./manifests/deploy.yaml"})
	return r
}

// withValuesMapping returns r carrying an inline values overlay.
func withValuesMapping(r *types.Recipe) *types.Recipe {
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "demo:1.0"}`}
	return r
}

// TestValidateRecipe_InputKindSurface_Grid drives both rules over the cells of
// the pair of conditions that gate them, rather than over each condition alone.
//
// Four cells carry the weight, and each fails differently if a gate is dropped.
// An eval Recipe declaring nothing must be accepted — that is the carve-out, and
// dropping it refuses a legitimate catalog. An eval Recipe declaring anything
// must be refused under the eval condition. A Recipe on any other capability
// declaring the same blocks must reach neither of those verdicts, which is what
// the capability gate on the eval rule buys. And a Recipe on any other
// capability declaring none of the three rendering blocks must be refused under
// the input-kinds condition.
//
// Each row asserts a condition identity rather than "some error", because the
// two rules run in sequence over one document: a row asserting only that
// something was refused keeps passing once a different rule starts refusing it
// first, and stops covering the rule it was written for without saying so.
func TestValidateRecipe_InputKindSurface_Grid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// recipe is the document under test, built fresh per row.
		recipe func() *types.Recipe
		// want is the condition the document must be refused under, or nil
		// where the document must be accepted.
		want error
	}{
		{
			name:   "an eval Recipe declaring no block at all is accepted",
			recipe: func() *types.Recipe { return newRecipe("eval") },
			want:   nil,
		},
		{
			name:   "an eval Recipe declaring a source is refused under the eval condition",
			recipe: func() *types.Recipe { return withSource(newRecipe("eval")) },
			want:   validator.ErrRecipeEvalProviderConstraint(),
		},
		{
			name:   "an eval Recipe declaring a workload is refused under the eval condition",
			recipe: func() *types.Recipe { return withWorkload(newRecipe("eval")) },
			want:   validator.ErrRecipeEvalProviderConstraint(),
		},
		{
			name:   "an eval Recipe declaring artifacts is refused under the eval condition",
			recipe: func() *types.Recipe { return withArtifacts(newRecipe("eval")) },
			want:   validator.ErrRecipeEvalProviderConstraint(),
		},
		{
			// The document also violates the values-mapping rule — an overlay
			// with no source — so the row pins which of the two refuses it.
			name:   "an eval Recipe declaring a values overlay is refused under the eval condition rather than the values-mapping one",
			recipe: func() *types.Recipe { return withValuesMapping(newRecipe("eval")) },
			want:   validator.ErrRecipeEvalProviderConstraint(),
		},
		{
			name:   "a Recipe on another capability declaring a source is accepted",
			recipe: func() *types.Recipe { return withSource(newRecipe("helm")) },
			want:   nil,
		},
		{
			name: "a Recipe on another capability declaring every block is accepted",
			recipe: func() *types.Recipe {
				return withValuesMapping(withArtifacts(withWorkload(withSource(newRecipe("helm")))))
			},
			want: nil,
		},
		{
			name:   "a Recipe on another capability declaring no block is refused under the input-kinds condition",
			recipe: func() *types.Recipe { return newRecipe("helm") },
			want:   validator.ErrRecipeNoInputKind(),
		},
		{
			name:   "a Recipe on another capability declaring only a workload is accepted",
			recipe: func() *types.Recipe { return withWorkload(newRecipe("helm")) },
			want:   nil,
		},
		{
			name:   "a Recipe on another capability declaring only artifacts is accepted",
			recipe: func() *types.Recipe { return withArtifacts(newRecipe("helm")) },
			want:   nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validator.ValidateRecipe(tc.recipe(), tFile)
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

// TestValidateRecipe_EvalRule_NamesTheDeclaredBlocksInAFixedOrder pins the order
// the eval refusal names the offending blocks in.
//
// The order is part of the contract rather than an artifact of how the rule is
// written: identical input produces identical output, so a Recipe declaring
// several blocks names them the same way on every run and in every reader's
// diff. Nothing else in the tree asserts it, and a rule reordered by an
// unrelated edit would change a user-visible sentence with no gate objecting.
func TestValidateRecipe_EvalRule_NamesTheDeclaredBlocksInAFixedOrder(t *testing.T) {
	t.Parallel()

	r := withValuesMapping(withArtifacts(withWorkload(withSource(newRecipe("eval")))))
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("an eval Recipe declaring every input-kind block must be refused; got nil")
	}
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint()) {
		t.Fatalf("the refusal must carry the eval-provider condition; got: %v", err)
	}

	// Asserted as the message's tail rather than as a substring anywhere in it:
	// the advice closes the diagnostic, so a suffix match also pins that nothing
	// follows the list and that no block was appended after it.
	const wantAdvice = "forbidden fields declared: source, workload, artifacts, valuesMapping"
	if msg := err.Error(); !strings.HasSuffix(msg, wantAdvice) {
		t.Errorf("the diagnostic must close by naming the blocks as %q; got: %s", wantAdvice, msg)
	}
}

// TestValidateRecipe_EvalRule_NamesOnlyTheBlocksTheDocumentDeclares asserts the
// refusal reports the occurrence rather than the rule's whole field set.
//
// A Recipe declaring one block and being told four are forbidden has to work out
// which of them it wrote, which is the difference between a diagnostic and a
// restatement of the rule.
func TestValidateRecipe_EvalRule_NamesOnlyTheBlocksTheDocumentDeclares(t *testing.T) {
	t.Parallel()

	err := validator.ValidateRecipe(withWorkload(newRecipe("eval")), tFile)
	if err == nil {
		t.Fatal("an eval Recipe declaring a workload must be refused; got nil")
	}
	// The suffix match carries both halves at once: the one block the document
	// declares is named, and nothing follows it — so a rule that reported its
	// whole field set rather than the occurrence would fail here.
	if msg := err.Error(); !strings.HasSuffix(msg, "forbidden fields declared: workload") {
		t.Errorf("the diagnostic must close by naming the one block declared; got: %s", msg)
	}
}

// TestValidateRecipe_InputKindsRule_CarriesItsRemedyAndItsRegisteredSentence
// asserts the refusal an author actually reads.
//
// The sentence is read off the condition rather than restated here, so this
// cannot pass against a second copy of the wording composed from the failure's
// fields. The remedy is asserted verbatim because it is the only part of the
// message that says what to write instead.
func TestValidateRecipe_InputKindsRule_CarriesItsRemedyAndItsRegisteredSentence(t *testing.T) {
	t.Parallel()

	err := validator.ValidateRecipe(newRecipe("helm"), tFile)
	if err == nil {
		t.Fatal("a Recipe declaring no input kind must be refused; got nil")
	}
	msg := err.Error()

	if sentence := validator.ErrRecipeNoInputKind().Error(); !strings.HasPrefix(msg, sentence+" (") {
		t.Errorf("the message must open with the registered sentence %q; got: %s", sentence, msg)
	}
	for _, want := range []string{
		"Recipe " + tName + ":" + tVer,
		"rule input-kinds",
		tFile,
		"declare one of: source (with valuesMapping), workload, artifacts",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the diagnostic must carry %q; got: %s", want, msg)
		}
	}
}

// TestValidateRecipe_EvalRule_CarriesItsRegisteredSentenceAndPosition asserts the
// same contract on the eval refusal, whose rule identifier differs.
func TestValidateRecipe_EvalRule_CarriesItsRegisteredSentenceAndPosition(t *testing.T) {
	t.Parallel()

	err := validator.ValidateRecipe(withSource(newRecipe("eval")), tFile)
	if err == nil {
		t.Fatal("an eval Recipe declaring a source must be refused; got nil")
	}
	msg := err.Error()

	if sentence := validator.ErrRecipeEvalProviderConstraint().Error(); !strings.HasPrefix(msg, sentence+" (") {
		t.Errorf("the message must open with the registered sentence %q; got: %s", sentence, msg)
	}
	for _, want := range []string{
		"Recipe " + tName + ":" + tVer,
		"rule eval",
		tFile,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the diagnostic must carry %q; got: %s", want, msg)
		}
	}
}
