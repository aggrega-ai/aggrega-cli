// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// A failure that names a registered condition prints that condition's short
// code in its head, whether or not anything filed it under a concern.
//
// The assertions here are against the printed bytes rather than against the
// declaration, and that is the whole point: a registered condition, an exported
// accessor and a wired chain read all pass while nothing prints a head, so what
// is checked is what the formatter produced. formatCompilerError is the reader's
// surface — the root command hands its result straight to standard error and
// adds nothing — so a string asserted here is the line a reader meets.

// headlessConditions are conditions from two different packages that belong to
// neither of the families a boundary categorises, so a chain naming one and
// carrying no envelope reaches the terminal with a code and no concern.
//
// Two rather than one, and from two packages, because a single row cannot tell a
// property of the rendering from a property of one declaration.
func headlessConditions() []error {
	return []error{
		catalog.ErrAllVersionsRetracted(),
		validator.ErrCyclicExtension(),
	}
}

// TestConditionWithNoCategoryPrintsTheShortCodeAlone renders each condition the
// way a raise site that never built an envelope leaves it, and pins both halves
// of the head: the short code is there, and no category was invented to sit
// beside it.
//
// The second half is the one worth asserting explicitly. A category answers
// which concern to look at, and a failure that crossed no categorising boundary
// has no answer to give, so a plausible value standing in for it would be a
// wrong instruction rather than a cautious one — and it would render exactly as
// convincingly as a derived one.
func TestConditionWithNoCategoryPrintsTheShortCodeAlone(t *testing.T) {
	t.Parallel()
	for _, condition := range headlessConditions() {
		condition := condition
		code, ok := cerrors.CodeOf(condition)
		if !ok {
			t.Errorf("a row carries no registered code, so the row would be vacuous: %v", condition)
			continue
		}
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			short, hasShort := cerrors.ShortCodeOf(condition)
			if !hasShort {
				t.Fatalf("condition %q carries no short code, so there would be no head to render", code)
			}
			err := fmt.Errorf("an occurrence naming %s: %w", code, condition)
			got := formatCompilerError(err)
			t.Logf("rendered as a reader meets it:\n%s", got)

			want := "[" + short + "]"
			if !strings.HasPrefix(got, want) {
				t.Fatalf("the diagnostic does not open with the condition's short code\n want prefix: %s\n got:         %s", want, got)
			}
			if n := countCategoryTokens(got); n != 0 {
				t.Errorf("the diagnostic names a category %d time(s); nothing filed this failure under a concern, so none may be printed\nrendered: %s", n, got)
			}
			if !strings.Contains(got, "an occurrence naming "+code) {
				t.Errorf("the occurrence's own text was dropped from the diagnostic; got: %s", got)
			}
		})
	}
}

// TestConditionUnderAnEnvelopeKeepsBothTokens is the other side of the row
// above, and it is what keeps that one from being read as "a condition never
// gets a category". The same condition raised through the categorised envelope
// prints both tokens, so what decides the head is the failure's own path and not
// the condition's identity.
//
// It also pins that the code-only branch did not claim a failure an earlier
// branch answers better: were it to fire first, this head would lose its
// category.
func TestConditionUnderAnEnvelopeKeepsBothTokens(t *testing.T) {
	t.Parallel()
	condition := headlessConditions()[0]
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatal("the condition carries no short code, so this row would assert nothing")
	}
	identity, isIdentity := condition.(cerrors.Identity)
	if !isIdentity {
		t.Fatalf("the condition is not an identity a raise site could name: %T", condition)
	}
	raised := cerrors.Raise(identity, cerrors.CatalogError, cerrors.SourceLocation{File: "component.yaml"},
		"an occurrence carried through an envelope", nil)
	got := formatCompilerError(raised)
	t.Logf("rendered as a reader meets it:\n%s", got)

	want := "[" + string(cerrors.CatalogError) + " " + short + "]"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("the diagnostic does not open with both tokens\n want prefix: %s\n got:         %s", want, got)
	}
}

// TestChainNamingNoConditionRendersItsOwnText pins the fallback the code-only
// branch sits above. A failure naming no registered condition has nothing for a
// head to carry, so it renders byte-identically to its own Error output — no
// brackets, nothing standing in for the absent tokens.
//
// Byte equality rather than a prefix check, because this is the assertion that
// would catch the branch above firing where it must not: a head prepended here
// would be a head invented for a failure that identifies nothing.
func TestChainNamingNoConditionRendersItsOwnText(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("a wrap: %w", stderrors.New("a failure naming no registered condition"))
	if got, want := formatCompilerError(err), err.Error(); got != want {
		t.Errorf("a chain naming no condition no longer renders as its own text\n got:  %q\n want: %q", got, want)
	}
}

// TestConditionHeadIsAbsentOnAContinuation pins the position guard. A frame
// beneath a "caused by" marker is the same failure seen from further in rather
// than a failure in its own right, so it names no code — and the code-only
// branch inherits that rule for free, because the code it renders is the one the
// position decides.
func TestConditionHeadIsAbsentOnAContinuation(t *testing.T) {
	t.Parallel()
	condition := headlessConditions()[0]
	err := fmt.Errorf("an occurrence: %w", condition)
	if got, want := formatCompilerErrorAt(err, continuingDiagnostic), err.Error(); got != want {
		t.Errorf("a continuation acquired a head\n got:  %q\n want: %q", got, want)
	}
}
