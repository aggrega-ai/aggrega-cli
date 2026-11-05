// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	stderrors "errors"
	"fmt"
	"testing"
)

// TestLocate_StatesAPositionAndRendersNothing pins the two halves of the
// annotation's contract, row by row: what LocatedAt reads back, and that the text
// every rendering of the chain produces is the text of the chain without it.
func TestLocate_StatesAPositionAndRendersNothing(t *testing.T) {
	inner := Wrap(stderrors.New("the cause"), CatalogError, SourceLocation{Instance: "node-a"}, "rendering node \"a\"")
	recipe := SourceLocation{File: "/w/recipe.yaml", Locator: ".spec.workload"}
	assertion := SourceLocation{File: "/w/recipe.yaml", Locator: ".spec.assertions.check"}

	for _, tc := range []struct {
		name string
		err  error
		// plain is the same chain with no annotation in it, whose text the
		// annotated chain must render byte for byte.
		plain  error
		want   SourceLocation
		wantOK bool
	}{
		{
			name:  "a chain with no annotation",
			err:   fmt.Errorf("rendering: %w", inner),
			plain: fmt.Errorf("rendering: %w", inner),
		},
		{
			name:   "an annotated cause beneath a wrap",
			err:    fmt.Errorf("rendering: %w", Locate(inner, recipe)),
			plain:  fmt.Errorf("rendering: %w", inner),
			want:   recipe,
			wantOK: true,
		},
		{
			name:   "the innermost of two annotations",
			err:    Locate(fmt.Errorf("outer: %w", Locate(inner, assertion)), recipe),
			plain:  fmt.Errorf("outer: %w", inner),
			want:   assertion,
			wantOK: true,
		},
		{
			name:  "an annotation naming no document",
			err:   Locate(inner, SourceLocation{Locator: ".spec.workload"}),
			plain: inner,
		},
		{
			name:  "an annotation down one branch of a joined failure",
			err:   stderrors.Join(Locate(inner, recipe), stderrors.New("another failure")),
			plain: stderrors.Join(inner, stderrors.New("another failure")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LocatedAt(tc.err)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("LocatedAt = %+v, %v; want %+v, %v", got, ok, tc.want, tc.wantOK)
			}
			if tc.err.Error() != tc.plain.Error() {
				t.Errorf("the annotation moved the text:\nannotated: %q\nplain:     %q", tc.err.Error(), tc.plain.Error())
			}
			if CauseChain(tc.err) != CauseChain(tc.plain) {
				t.Errorf("the annotation moved the cause chain:\nannotated: %q\nplain:     %q",
					CauseChain(tc.err), CauseChain(tc.plain))
			}
		})
	}

	t.Run("a nil error stays nil", func(t *testing.T) {
		if err := Locate(nil, recipe); err != nil {
			t.Errorf("Locate(nil) = %v; want nil", err)
		}
	})
	t.Run("the annotated failure is still matched through it", func(t *testing.T) {
		sentinel := stderrors.New("the sentinel")
		var envelope *CompilerError
		err := Locate(Wrap(sentinel, CatalogError, SourceLocation{}, "frame"), recipe)
		if !stderrors.Is(err, sentinel) || !stderrors.As(err, &envelope) {
			t.Error("errors.Is or errors.As no longer reaches the annotated chain")
		}
	})
}
