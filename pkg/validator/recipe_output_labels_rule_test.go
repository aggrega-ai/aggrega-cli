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

// The two rules over a Recipe's output-label surface are gated between them on
// two conditions — which of the two blocks a key was written in, and what the
// key itself is — so what they specify is a grid rather than two lists. Testing
// each condition alone would state the grid's margins and leave its interior
// unsaid, and the interior is where both rules' gates live: one condition covers
// both blocks and must fire on each, while the other covers one block and must
// stay silent on the other. The tests below enumerate the cells.

// withLiteralLabels returns r declaring the given literal output labels, each
// under a value the rules never read.
func withLiteralLabels(r *types.Recipe, keys ...string) *types.Recipe {
	if r.Spec.OutputLabels == nil {
		r.Spec.OutputLabels = map[string]string{}
	}
	for _, key := range keys {
		r.Spec.OutputLabels[key] = "declared-in-place"
	}
	return r
}

// withPromotedLabels returns r promoting the given component label keys, in the
// order they are given.
func withPromotedLabels(r *types.Recipe, keys ...string) *types.Recipe {
	r.Spec.OutputComponentLabels = append(r.Spec.OutputComponentLabels, keys...)
	return r
}

// renderableRecipe is a Recipe that satisfies every other load-time rule, so a
// refusal from one of these tests is attributable to the output-label surface
// rather than to a rule that runs before or after it.
func renderableRecipe() *types.Recipe { return withArtifacts(newRecipe("helm")) }

// TestValidateRecipe_OutputLabelSurface_Grid drives both rules over the cells of
// the two conditions that gate them, rather than over each condition alone.
//
// Four cells carry the reserved-namespace rule, and they are what proves one
// condition covers both blocks: a migration wiring only the literal block would
// pass every cell but the promoted reserved one, and a half-covering rule that
// presents as complete is worse than an absent one, because nothing would ever
// prompt a second look at the block it silently passed.
//
// Three more carry the collision rule, and the third is the interior cell a
// per-condition test would miss: a key written in the literal block is trivially
// present in the literal block, so a collision rule without its own block gate
// would refuse every literal key it was handed.
//
// Each row asserts a condition identity rather than "some error", because the
// rules run in sequence over one document: a row asserting only that something
// was refused keeps passing once a different rule starts refusing it first, and
// stops covering the rule it was written for without saying so.
func TestValidateRecipe_OutputLabelSurface_Grid(t *testing.T) {
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
			name:   "a reserved platform root written in the literal block is refused",
			recipe: func() *types.Recipe { return withLiteralLabels(renderableRecipe(), "aggrega.ai/foo") },
			want:   validator.ErrRecipeOutputLabelReservedNamespace(),
		},
		{
			name:   "a reserved platform root promoted from a component label is refused",
			recipe: func() *types.Recipe { return withPromotedLabels(renderableRecipe(), "aggrega.ai/foo") },
			want:   validator.ErrRecipeOutputLabelReservedNamespace(),
		},
		{
			name:   "the other reserved root is refused in the literal block",
			recipe: func() *types.Recipe { return withLiteralLabels(renderableRecipe(), "app.kubernetes.io/name") },
			want:   validator.ErrRecipeOutputLabelReservedNamespace(),
		},
		{
			name:   "the other reserved root is refused in the promoted block",
			recipe: func() *types.Recipe { return withPromotedLabels(renderableRecipe(), "app.kubernetes.io/name") },
			want:   validator.ErrRecipeOutputLabelReservedNamespace(),
		},
		{
			name:   "a key outside both reserved roots is accepted in the literal block",
			recipe: func() *types.Recipe { return withLiteralLabels(renderableRecipe(), "cost-centre") },
			want:   nil,
		},
		{
			name: "a key scoped under a reserved root is accepted in the promoted block",
			recipe: func() *types.Recipe {
				return withPromotedLabels(renderableRecipe(), "component.aggrega.ai/hosting")
			},
			want: nil,
		},
		{
			name: "a promoted key also declared literally is refused",
			recipe: func() *types.Recipe {
				return withPromotedLabels(withLiteralLabels(renderableRecipe(), "hosting"), "hosting")
			},
			want: validator.ErrRecipeOutputComponentLabelCollision(),
		},
		{
			name: "a promoted key the literal block does not declare is accepted",
			recipe: func() *types.Recipe {
				return withPromotedLabels(withLiteralLabels(renderableRecipe(), "tier"), "hosting")
			},
			want: nil,
		},
		{
			name: "a literal key is not a collision with itself",
			recipe: func() *types.Recipe {
				return withLiteralLabels(renderableRecipe(), "hosting", "tier")
			},
			want: nil,
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

// TestValidateRecipe_OutputLabels_LiteralBlockIsReportedInSortedOrder pins which
// key a literal block declaring two reserved ones reports.
//
// The block is a mapping, whose iteration order the language leaves unspecified,
// so without a sort the reported key would move between runs of one binary over
// one document. Nothing else in the tree asserts the sort now that it lives on
// the presentation rather than at the rule.
func TestValidateRecipe_OutputLabels_LiteralBlockIsReportedInSortedOrder(t *testing.T) {
	t.Parallel()

	r := withLiteralLabels(renderableRecipe(), "app.kubernetes.io/z", "aggrega.ai/a")
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("a literal block declaring reserved keys must be refused; got nil")
	}
	if !strings.Contains(err.Error(), `key "aggrega.ai/a"`) {
		t.Errorf("the sorted-first key must be the one reported; got: %v", err)
	}
}

// TestValidateRecipe_OutputLabels_PromotedBlockIsReportedInAuthorOrder pins the
// other half: a sequence carries the author's order and it is not re-sorted.
//
// The two keys are chosen so that sorting would change the answer — the authored
// first key sorts second — so a presentation that sorted this block as it sorts
// the literal one fails here rather than silently reporting the other key.
func TestValidateRecipe_OutputLabels_PromotedBlockIsReportedInAuthorOrder(t *testing.T) {
	t.Parallel()

	r := withPromotedLabels(renderableRecipe(), "app.kubernetes.io/z", "aggrega.ai/a")
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("a promoted block declaring reserved keys must be refused; got nil")
	}
	if !strings.Contains(err.Error(), `key "app.kubernetes.io/z"`) {
		t.Errorf("the first key the author wrote must be the one reported; got: %v", err)
	}
}

// TestValidateRecipe_OutputLabels_ReservedIsSettledBeforeCollision pins the
// precedence between the two rules over the promoted block.
//
// The reserved rule is settled across the whole block before the collision rule
// is asked anything, so a Recipe breaking both always reports the reserved
// namespace — the more fundamental fault, and the one whose fix does not depend
// on the other block's contents. The fixture is the case that tells the two
// orderings apart: the colliding key is written FIRST, so a walk asking both
// questions of each key in turn would report the collision.
func TestValidateRecipe_OutputLabels_ReservedIsSettledBeforeCollision(t *testing.T) {
	t.Parallel()

	r := withPromotedLabels(
		withLiteralLabels(renderableRecipe(), "hosting"),
		"hosting", "aggrega.ai/spoofed",
	)
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("a promoted block breaking both rules must be refused; got nil")
	}
	if !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
		t.Fatalf("the reserved-namespace condition must outrank the collision one; got: %v", err)
	}
	if !strings.Contains(err.Error(), `key "aggrega.ai/spoofed"`) {
		t.Errorf("the reserved key must be the one reported; got: %v", err)
	}
}

// TestValidateRecipe_OutputLabels_LiteralBlockIsSettledBeforeThePromotedOne pins
// the precedence between the two blocks, which is the caller's ordering rather
// than either rule's.
//
// Both blocks declare a reserved key here, so the two are told apart only by
// which block the diagnostic names.
func TestValidateRecipe_OutputLabels_LiteralBlockIsSettledBeforeThePromotedOne(t *testing.T) {
	t.Parallel()

	r := withPromotedLabels(
		withLiteralLabels(renderableRecipe(), "aggrega.ai/literal"),
		"aggrega.ai/promoted",
	)
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("a Recipe declaring a reserved key in each block must be refused; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `key "aggrega.ai/literal"`) {
		t.Errorf("the literal block must be the one reported; got: %s", msg)
	}
	if !strings.Contains(msg, "rule output-labels,") {
		t.Errorf("the finding must carry the literal block's rule identifier; got: %s", msg)
	}
}

// TestValidateRecipe_OutputLabels_ReservedRuleCarriesItsSentenceBlockAndPosition
// asserts the refusal an author actually reads.
//
// The sentence is read off the condition rather than restated here, so this
// cannot pass against a second copy of the wording composed from the failure's
// fields. The block is asserted because one condition covers both, so its
// registered sentence cannot say which of the two the author typed and the
// advice is the only thing that can.
//
// The position is asserted in both halves. The file is what a reader opens; the
// locator is the block within it. A single string could not be both, so a
// regression that dropped either half would leave the other reading as correct.
func TestValidateRecipe_OutputLabels_ReservedRuleCarriesItsSentenceBlockAndPosition(t *testing.T) {
	t.Parallel()

	err := validator.ValidateRecipe(
		withPromotedLabels(renderableRecipe(), "aggrega.ai/x"), tFile)
	if err == nil {
		t.Fatal("a promoted reserved key must be refused; got nil")
	}
	msg := err.Error()

	for _, want := range []string{
		validator.ErrRecipeOutputLabelReservedNamespace().Error(),
		"Recipe " + tName + ":" + tVer,
		"rule output-component-labels",
		tFile,
		"at .spec.outputComponentLabels",
		`key "aggrega.ai/x" in outputComponentLabels uses a reserved namespace`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the diagnostic must carry %q; got: %s", want, msg)
		}
	}
}

// TestValidateRecipe_OutputLabels_CollisionRuleCarriesItsSentenceAndAuthoredKey
// asserts the same contract on the collision refusal, whose condition and locator
// depth both differ.
//
// The locator reaches the key here where the reserved rule's stops at the block,
// and the difference is the dialect's rather than either rule's: a reserved key
// necessarily carries the locator separator inside it, and a colliding key need
// not. Asserting the deeper form is what proves the traversal contributes the key
// as a segment at all.
func TestValidateRecipe_OutputLabels_CollisionRuleCarriesItsSentenceAndAuthoredKey(t *testing.T) {
	t.Parallel()

	r := withPromotedLabels(withLiteralLabels(renderableRecipe(), "hosting"), "hosting")
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("a key declared in both blocks must be refused; got nil")
	}
	msg := err.Error()

	for _, want := range []string{
		validator.ErrRecipeOutputComponentLabelCollision().Error(),
		"rule output-component-labels",
		tFile,
		"at .spec.outputComponentLabels.hosting",
		`key "hosting" is promoted through outputComponentLabels and also declared literally in outputLabels`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the diagnostic must carry %q; got: %s", want, msg)
		}
	}
}

// TestValidateRecipe_OutputLabels_LocatorStopsAtTheBlockForAnUnaddressableKey
// pins what the position does with a key the locator dialect cannot address.
//
// A locator is a position and not an identifier: its segments are joined by a
// separator, so a key containing that separator would produce a string reading
// equally as one key or as two. The dialect therefore names the deepest
// addressable site and stops, which here is the block. The key is not lost — it
// travels as the occurrence's advice — and that pairing is the contract this
// pins, because a reader meeting only the truncated locator would take it for a
// rule that could not name the key at all.
func TestValidateRecipe_OutputLabels_LocatorStopsAtTheBlockForAnUnaddressableKey(t *testing.T) {
	t.Parallel()

	const dotted = "example.com/team"
	r := withPromotedLabels(withLiteralLabels(renderableRecipe(), dotted), dotted)
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("a key declared in both blocks must be refused; got nil")
	}
	msg := err.Error()

	if !strings.Contains(msg, "at .spec.outputComponentLabels:") {
		t.Errorf("the locator must stop at the block for a key carrying the separator; got: %s", msg)
	}
	if !strings.Contains(msg, `key "`+dotted+`" is promoted`) {
		t.Errorf("the key must still reach the reader as the advice; got: %s", msg)
	}
}

// TestValidateRecipe_OutputLabels_RefusalThreadsThroughItsPosition asserts the
// failure a caller inspects is unchanged by carrying both halves of its position.
//
// Positioning at the file and the key together puts one more frame between the
// caller and the condition, so a caller matching by identity, or reaching for the
// structured failure, has to keep working through it.
func TestValidateRecipe_OutputLabels_RefusalThreadsThroughItsPosition(t *testing.T) {
	t.Parallel()

	err := validator.ValidateRecipe(
		withLiteralLabels(renderableRecipe(), "aggrega.ai/foo"), tFile)
	if err == nil {
		t.Fatal("a literal reserved key must be refused; got nil")
	}

	var ve *validator.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("the structured failure must stay reachable through the chain; got %T", err)
	}
	if !errors.Is(ve.ToCompilerError(), validator.ErrRecipeOutputLabelReservedNamespace()) {
		t.Error("the condition must thread through the converted compiler error")
	}
}

// TestValidateRecipe_OutputLabels_AbsentBlocksAreNotAViolation asserts the
// ordinary authoring shape, which is a Recipe declaring neither block.
//
// A rule invoked once per declared key is invoked no times here, and a rule that
// is never invoked reports nothing — which is indistinguishable from one that
// found nothing wrong. So this row is asserting the traversal's behaviour on an
// empty enumeration rather than either predicate's.
func TestValidateRecipe_OutputLabels_AbsentBlocksAreNotAViolation(t *testing.T) {
	t.Parallel()

	if err := validator.ValidateRecipe(renderableRecipe(), tFile); err != nil {
		t.Fatalf("a Recipe declaring neither label block must be accepted; got: %v", err)
	}
}
