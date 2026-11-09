// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// recipeWithSourceEntry is a Recipe that renders from a workload whatever its
// source entry says, so the rows below differ in the source entry and in nothing
// else: every one of them declares an input kind, and the only open question is
// what the entry selects.
func recipeWithSourceEntry(source any) *types.Recipe {
	r := newRecipe("helm")
	r.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1beta1\nkind: Workload"}
	r.Spec.Source = source
	return r
}

// TestValidateRecipe_SourceEntry_EmptyIsRefusedAtLoad is the load-time rule over
// the source entry: an entry written and left empty is refused with the
// empty-source condition, and the two modes the empty entry is ambiguous between
// both load.
//
// The admitting rows are what keep the refusing row honest. Without them, a rule
// refusing every Recipe that writes a source — or every Recipe at all — would pass
// the refusing row, and the absent entry is the mode the halt exists to tell the
// empty one apart from.
func TestValidateRecipe_SourceEntry_EmptyIsRefusedAtLoad(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		source  any
		refused bool
	}{
		{name: "an entry written and left empty", source: "", refused: true},
		{name: "no entry at all, which is sourceless mode", source: nil, refused: false},
		{name: "an entry naming a path, which is sourced mode", source: "./chart", refused: false},
		{
			name:    "an entry naming a structured source",
			source:  map[string]any{"kind": "helm-oci-chart", "url": "oci://example.test/chart"},
			refused: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validator.ValidateRecipe(recipeWithSourceEntry(tc.source), tFile)
			if !tc.refused {
				if err != nil {
					t.Fatalf("a declared authoring mode was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a Recipe writing its source entry and leaving it empty was admitted")
			}
			t.Logf("emitted failure: %v", err)
			if !errors.Is(err, validator.ErrEmptySource()) {
				t.Fatalf("expected the empty-source condition, got %v", err)
			}
			if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0251" {
				t.Errorf("the failure carries short code %q (present=%v); want E0251", code, ok)
			}
		})
	}
}

// TestValidateRecipe_SourceEntry_PositionedAtTheEntry is the finding's shape: the
// file the Recipe was loaded from AND the key inside it, the registered sentence,
// and advice naming both valid modes.
//
// The position is the load-bearing half. The fault is one key the author left
// behind, so a failure naming only the file would leave the author a sentence with
// no line to go to.
func TestValidateRecipe_SourceEntry_PositionedAtTheEntry(t *testing.T) {
	t.Parallel()

	err := validator.ValidateRecipe(recipeWithSourceEntry(""), tFile)
	if err == nil {
		t.Fatal("a Recipe writing its source entry and leaving it empty was admitted")
	}

	var compiled *cerrors.CompilerError
	if !errors.As(err, &compiled) {
		t.Fatalf("expected a categorised failure carrying a position, got %T: %v", err, err)
	}
	if got := compiled.Source(); got.File != tFile || got.Locator != ".spec.source" {
		t.Errorf("the failure is positioned at %q; want the Recipe's file %q at .spec.source", got.String(), tFile)
	}

	// The shape-rule failure every Recipe rule reports through is reachable through
	// the chain, and its message — the text beneath the position — opens with the
	// condition's registered sentence and closes with the advice.
	var shaped *validator.ValidationError
	if !errors.As(err, &shaped) {
		t.Fatalf("expected the failure to read as *validator.ValidationError through the chain, got %v", err)
	}
	msg := shaped.Error()
	if sentence := validator.ErrEmptySource().Error(); !strings.HasPrefix(msg, sentence) {
		t.Errorf("message must open with the registered sentence %q, got: %q", sentence, msg)
	}
	if !strings.Contains(msg, "Recipe "+tName+":"+tVer+", at .spec.source)") {
		t.Errorf("message must name the Recipe and the entry's position: %q", msg)
	}
	for _, want := range []string{"omit the source field", "sourceless mode", "sourced mode"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not name %q, so the two modes the entry is ambiguous between are not "+
				"both stated: %q", want, msg)
		}
	}
}

// TestValidateRecipe_SourceEntry_ReportedAheadOfTheRulesReadingSourcePresence
// holds the order: a Recipe whose only fault is an empty source entry is told about
// the entry, not about what an empty entry leaves undeclared.
//
// An empty entry declares no source, so both rules reading whether a source is
// declared would otherwise report a consequence of it — an overlay with nothing to
// overlay, or a Recipe with no input kind.
func TestValidateRecipe_SourceEntry_ReportedAheadOfTheRulesReadingSourcePresence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		recipe func() *types.Recipe
	}{
		{
			name: "beside a values overlay",
			recipe: func() *types.Recipe {
				r := newRecipe("helm")
				r.Spec.Source = ""
				r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "demo:1.0"}`}
				return r
			},
		},
		{
			name: "with no other input kind",
			recipe: func() *types.Recipe {
				r := newRecipe("helm")
				r.Spec.Source = ""
				return r
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validator.ValidateRecipe(tc.recipe(), tFile)
			if !errors.Is(err, validator.ErrEmptySource()) {
				t.Fatalf("expected the empty-source condition ahead of the rules reading source "+
					"presence, got %v", err)
			}
		})
	}
}
