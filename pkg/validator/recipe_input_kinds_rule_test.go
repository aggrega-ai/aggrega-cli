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

// The rule over a Recipe's input-kind surface reads which of the authored
// blocks a Recipe declares, and nothing about the capability it binds: a Recipe
// on any capability owes one rendering block. The tests below enumerate the
// blocks, and hold that no capability is exempt.

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

// TestValidateRecipe_InputKindSurface_Grid drives the input-kinds rule over the
// blocks a Recipe may declare, on a capability a provider serves and on one no
// provider serves.
//
// A Recipe declaring none of the three rendering blocks is refused under the
// input-kinds condition whatever capability it binds — `eval` included: no
// capability name exempts a Recipe from the rule. A Recipe declaring any one of
// them is accepted.
//
// Each row asserts a condition identity rather than "some error", because the
// shape rules run in sequence over one document: a row asserting only that
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
			name:   "a Recipe binding the capability eval and declaring no block is refused under the input-kinds condition",
			recipe: func() *types.Recipe { return newRecipe("eval") },
			want:   validator.ErrRecipeNoInputKind(),
		},
		{
			name:   "a Recipe binding the capability eval and declaring artifacts is accepted",
			recipe: func() *types.Recipe { return withArtifacts(newRecipe("eval")) },
			want:   nil,
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
