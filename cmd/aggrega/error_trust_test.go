// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestEveryTrustConditionPrintsAHead walks the trust set itself rather than a
// transcribed list of it, so a condition added to the set is covered with
// nothing to remember. Each is wrapped the way a raise site wraps it and
// rendered through the command line's own diagnostic formatter; the assertion is
// that the printed head carries both tokens — the trust category, and that
// condition's own short code.
//
// This is the assertion the item exists for: a declared category, a registered
// condition and a wired conversion all pass while nothing prints a head, so what
// is checked here is the printed bytes.
func TestEveryTrustConditionPrintsAHead(t *testing.T) {
	t.Parallel()
	conditions := trustConditions()
	if len(conditions) == 0 {
		t.Fatal("the trust set is empty, so every row below would be vacuous")
	}
	for _, condition := range conditions {
		condition := condition
		code, ok := cerrors.CodeOf(condition)
		if !ok {
			t.Errorf("a trust-set entry carries no registered code, so nothing can resolve it: %v", condition)
			continue
		}
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			short, hasShort := cerrors.ShortCodeOf(condition)
			if !hasShort {
				t.Fatalf("condition %q carries no short code, so the head would have only one token", code)
			}
			err := fmt.Errorf("an occurrence naming %s: %w", code, condition)
			got := formatCompilerError(err)
			want := "[" + string(cerrors.TrustError) + " " + short + "]"
			if !strings.HasPrefix(got, want) {
				t.Fatalf("rendered diagnostic does not open with the trust head\n want prefix: %s\n got:         %s", want, got)
			}
			if !strings.Contains(got, "an occurrence naming "+code) {
				t.Errorf("the occurrence's own text was dropped from the diagnostic; got: %s", got)
			}
		})
	}
}

// TestTrustHeadDoesNotRepeatTheMessage pins the shape the rendering-only
// envelope buys, over the ordinary single-verb wrap a raise site writes: the
// head, then the failure's flattened text exactly once. Were the envelope to
// carry the original error as its wrap chain, the chain walk beneath it would
// print that same text a second time as a "caused by:" continuation, because the
// envelope's message already holds every frame's contribution.
func TestTrustHeadDoesNotRepeatTheMessage(t *testing.T) {
	t.Parallel()
	inner := fmt.Errorf("%w: the held key is RSA, not ECDSA P-256", signing.ErrUnsupportedKey())
	err := fmt.Errorf("construct signer: %w", inner)
	got := formatCompilerError(err)
	if strings.Contains(got, "caused by") {
		t.Errorf("a trust diagnostic rendered a continuation, so its text is said twice; got:\n%s", got)
	}
	if n := strings.Count(got, "the held key is RSA"); n != 1 {
		t.Errorf("the inner fault appears %d time(s); want exactly 1\n%s", n, got)
	}
	if !strings.Contains(got, string(cerrors.TrustError)) {
		t.Errorf("the head carries no trust category; got:\n%s", got)
	}
}

// TestATwoVerbTrustWrapRendersEachConditionOnItsOwn covers the publish path's
// wrap, which names two conditions at once — the specific fault and the refusal
// beside it — with two %w verbs. Such a chain exposes Unwrap() []error, so the
// multi-error branch claims it BEFORE any conversion branch and renders each
// wrapped error as a failure in its own right, separated by the "---" rule. That
// split is the formatter's pre-existing behaviour for the shape; what is new is
// that each of the two now leads with its own trust head and its own short code,
// rather than with plain text.
func TestATwoVerbTrustWrapRendersEachConditionOnItsOwn(t *testing.T) {
	t.Parallel()
	specific := fmt.Errorf("%w: held key is *rsa.PrivateKey, want ECDSA P-256", signing.ErrUnsupportedKey())
	err := fmt.Errorf("construct signer: %w: %w", specific, signing.ErrUnsignedPushRefused())

	firstShort, ok := cerrors.ShortCodeOf(signing.ErrUnsupportedKey())
	if !ok {
		t.Fatal("the unsupported-key condition carries no short code")
	}
	secondShort, ok := cerrors.ShortCodeOf(signing.ErrUnsignedPushRefused())
	if !ok {
		t.Fatal("the unsigned-push refusal carries no short code")
	}
	if firstShort == secondShort {
		t.Fatal("the two conditions share a short code, so this test could not tell them apart")
	}
	got := formatCompilerError(err)
	if !strings.Contains(got, "\n---\n") {
		t.Fatalf("a two-verb wrap did not render as sibling failures; got:\n%s", got)
	}
	for _, want := range []string{
		"[" + string(cerrors.TrustError) + " " + firstShort + "]",
		"[" + string(cerrors.TrustError) + " " + secondShort + "]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the rendering is missing the head %s; got:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "["+string(cerrors.TrustError)+" "+firstShort+"]") {
		t.Errorf("the specific fault does not lead; got:\n%s", got)
	}
}

// TestAFailureWithNoTrustConditionKeepsTheBareFallback is the negative control
// that makes every assertion above attributable, and it is also the guard that
// the bare fallback stays. Over an error of the same shape
// carrying no registered trust condition, the formatter prints the message alone
// with no brackets — so the heads above came from the match rather than from the
// branch firing on everything that reaches it.
func TestAFailureWithNoTrustConditionKeepsTheBareFallback(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("read config: %w", stderrors.New("no such file or directory"))
	got := formatCompilerError(err)
	if strings.Contains(got, "[") {
		t.Errorf("an uncategorised failure acquired a bracketed head; got: %s", got)
	}
	if got != err.Error() {
		t.Errorf("the fallback no longer prints the error's own text\n want: %s\n got:  %s", err.Error(), got)
	}
}

// TestAnEnvelopeAroundATrustConditionKeepsItsOwnCategory pins the branch order.
// The trust match is broader than an errors.As on one concrete type, so it has
// to run after the envelope branch: a boundary that has already chosen a
// category for its own reasons keeps it, and the trust branch fills only what
// would otherwise have fallen through.
func TestAnEnvelopeAroundATrustConditionKeepsItsOwnCategory(t *testing.T) {
	t.Parallel()
	err := cerrors.Wrap(
		fmt.Errorf("verify: %w", signing.ErrSignatureVerificationFailed()),
		cerrors.CatalogError,
		cerrors.SourceLocation{File: "catalog/recipe.yaml"},
		"catalog load",
	)
	got := formatCompilerError(err)
	if !strings.Contains(got, string(cerrors.CatalogError)) {
		t.Errorf("the envelope's own category was overwritten; got:\n%s", got)
	}
	if strings.Contains(got, string(cerrors.TrustError)) {
		t.Errorf("the trust branch fired ahead of the envelope branch; got:\n%s", got)
	}
}

// TestATrustContinuationCarriesNoShortCode holds the trust family to the
// head-only placement rule every other diagnostic follows: the condition is
// named once, where the reader meets the failure, and a frame beneath a
// "caused by" marker carries its category alone.
func TestATrustContinuationCarriesNoShortCode(t *testing.T) {
	t.Parallel()
	trust := fmt.Errorf("hold the record: %w", pluginstrust.ErrVerdictAttachmentNotConfigured())
	short, ok := cerrors.ShortCodeOf(trust)
	if !ok {
		t.Fatal("the not-configured condition carries no short code")
	}
	got := formatCompilerErrorAt(trust, continuingDiagnostic)
	if !strings.Contains(got, string(cerrors.TrustError)) {
		t.Fatalf("a continuation dropped the trust category; got:\n%s", got)
	}
	if strings.Contains(got, short) {
		t.Errorf("a continuation repeated the short code %s; got:\n%s", short, got)
	}
}

// TestTrustMatchIsIdentityBasedNotCodeBased guards the one shortcut that would
// type-check and then rot. A distinct error whose text happens to contain a
// trust condition's registered code names no condition, so it must not be
// categorised — which is what says the match reads identities rather than
// strings.
func TestTrustMatchIsIdentityBasedNotCodeBased(t *testing.T) {
	t.Parallel()
	code, ok := cerrors.CodeOf(signing.ErrNoSignatureFound())
	if !ok {
		t.Fatal("the no-signature condition carries no registered code")
	}
	err := stderrors.New("a message that merely mentions " + code + " in prose")
	if matchTrustCondition(err) {
		t.Errorf("an error naming no condition was matched by its text alone; the match is reading codes rather than identities")
	}
}
