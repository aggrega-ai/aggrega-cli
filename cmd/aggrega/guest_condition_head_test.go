// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestGuestReportedConditionReachesThePrintedHead is the consumer half of the
// guest failure transport: it names the surface that reads the code off the error
// and shows the code in the line a reader sees.
//
// It asserts against the printed head rather than against the error, because a
// code carried on an error nothing prints is exactly the shape that passes while
// changing nothing a user meets. The guest's own identifiers are supplied as
// values here rather than read from a guest core: this package is the printing
// boundary, and which conditions the guests publish is asserted where those
// conditions are declared.
//
// The head carries the code QUALIFIED by the plugin that raised it, and the
// assertion pins the exact spelling rather than merely looking for both tokens:
// the code is the final segment, after a solidus, so a reader recovers it from a
// qualifier that carries separators of its own.
func TestGuestReportedConditionReachesThePrintedHead(t *testing.T) {
	t.Parallel()

	const guestPlugin = "provider/compose-provider"

	// The identifiers are read off the condition the guest chose rather than
	// written out, so this test cannot come to assert a spelling the registration
	// does not carry. The conditions a plugin may choose are the compiler's own,
	// so there is an accessor to read them from.
	chosen := plugincondition.ErrPluginCannotExpressInput()
	guestCode, ok := cerrors.CodeOf(chosen)
	if !ok {
		t.Fatal("the chosen condition reports no descriptive code, so this test asserts nothing")
	}
	guestShort, ok := cerrors.ShortCodeOf(chosen)
	if !ok {
		t.Fatal("the chosen condition reports no short code, so the head below carries nothing to assert")
	}

	guestErr := wasm.GuestReturnedError(guestPlugin, "render_node", wasm.GuestFailure{
		Message:   "compose: node \"web\" declares container lifecycle hooks; compose has no lifecycle-hook primitive",
		Code:      guestCode,
		ShortCode: guestShort,
	})
	// The host frames the guest's failure the way the render path does: its own
	// categorised envelope, pointing at the node, over the guest's error.
	framed := cerrors.Unidentified(cerrors.TranslationError,
		cerrors.SourceLocation{Instance: "web"}, "rendering node \"web\"", nil, guestErr)

	got := formatCompilerError(framed)
	wantHead := "[" + string(cerrors.TranslationError) + " " + guestPlugin + "/" + guestShort + "]"
	if !strings.HasPrefix(got, wantHead) {
		t.Errorf("the printed head does not carry the guest's code qualified by the plugin that raised it.\n  want prefix: %s\n  got:\n%s", wantHead, got)
	}
	// An unqualified code must not be what prints. Two independently authored
	// guests can publish one code, so a bare one names a condition the reader
	// cannot resolve — which is the whole condition the qualifier removes.
	if strings.HasPrefix(got, "["+string(cerrors.TranslationError)+" "+guestShort+"]") {
		t.Errorf("the printed head carries the guest's code unqualified, so two guests publishing %s would be indistinguishable:\n%s", guestShort, got)
	}
	// The host's own generic plugin-failure code must not be what prints: it would
	// name a condition the reader cannot look up against the failure they met.
	hostShort, ok := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	if !ok {
		t.Fatal("the host's guest-error condition reports no short code, so the control below asserts nothing")
	}
	if strings.Contains(got, hostShort) {
		t.Errorf("the printed head carries the host's own code %s instead of the chosen condition's %s:\n%s", hostShort, guestShort, got)
	}
	// A code and a sentence describing two different things on one line is the
	// failure the closed set exists to remove, so the body is asserted beside the
	// head: the condition the code resolves to, then the plugin's own sentence.
	for _, want := range []string{
		chosen.Error(),
		"compose: node \"web\" declares container lifecycle hooks; compose has no lifecycle-hook primitive",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the printed diagnostic is missing %q:\n%s", want, got)
		}
	}
}

// TestTwoPluginsReportingOneCodePrintDistinguishableHeads is the property the
// qualifier exists for, asserted at the surface a reader actually meets.
//
// A single-plugin assertion cannot show it: a head carrying one plugin's name
// looks correct whether or not the name came from the plugin, so only two guests
// reporting the SAME code demonstrate that the head tells them apart.
//
// The shared code is a real member of the set a plugin chooses from, which is
// what makes this the ORDINARY case rather than a collision: those conditions
// name a KIND of failure and no place, so every guest that cannot express a
// construct reports the same code, and the plugin is the only thing in the head
// that says which of a build's attached guests to open. An invented code would
// not do — the host refuses one outside the set, so both heads would carry the
// refusal's own code and the case would be asserting the refusal instead.
func TestTwoPluginsReportingOneCodePrintDistinguishableHeads(t *testing.T) {
	t.Parallel()

	const (
		first  = "provider/example-provider"
		second = "provider/another-provider"
	)

	// Read off the condition rather than written out, so this cannot come to
	// assert a spelling the registration does not carry.
	chosen := plugincondition.ErrPluginCannotExpressInput()
	sharedCode, ok := cerrors.CodeOf(chosen)
	if !ok {
		t.Fatal("the chosen condition reports no descriptive code, so neither report below names a condition")
	}
	sharedShort, ok := cerrors.ShortCodeOf(chosen)
	if !ok {
		t.Fatal("the chosen condition reports no short code, so the heads below carry nothing to assert")
	}

	head := func(plugin string) string {
		guestErr := wasm.GuestReturnedError(plugin, "render_node", wasm.GuestFailure{
			Message:   "the occurrence's own clause",
			Code:      sharedCode,
			ShortCode: sharedShort,
		})
		framed := cerrors.Unidentified(cerrors.TranslationError,
			cerrors.SourceLocation{Instance: "web"}, "rendering node \"web\"", nil, guestErr)
		return formatCompilerError(framed)
	}

	gotFirst, gotSecond := head(first), head(second)
	for plugin, got := range map[string]string{first: gotFirst, second: gotSecond} {
		want := "[" + string(cerrors.TranslationError) + " " + plugin + "/" + sharedShort + "]"
		if !strings.HasPrefix(got, want) {
			t.Errorf("the head for %s is not the qualified form.\n  want prefix: %s\n  got:\n%s", plugin, want, got)
		}
	}
	if gotFirst == gotSecond {
		t.Fatalf("two guests reporting %s printed identical diagnostics, so a reader cannot tell which plugin they came from:\n%s", sharedShort, gotFirst)
	}
	// The refusal must not be what printed. Both heads above are qualified
	// whichever condition stands, so the qualified-form assertion alone would pass
	// on a pair of refusals — and the case would then be proving the qualifier over
	// a condition no plugin chose rather than over the shared one it is about.
	refusalShort, ok := cerrors.ShortCodeOf(wasm.ErrWasmGuestConditionUndeclared())
	if !ok {
		t.Fatal("the refusal reports no short code, so the control below asserts nothing")
	}
	for plugin, got := range map[string]string{first: gotFirst, second: gotSecond} {
		if strings.Contains(got, refusalShort) {
			t.Errorf("the head for %s carries the refusal's code %s, so the shared code under test was "+
				"rejected rather than reported:\n%s", plugin, refusalShort, got)
		}
	}
}

// TestCompilerRaisedConditionPrintsAnUnqualifiedCode is the control that bounds
// the qualifier to conditions a plugin raised.
//
// Without it, a qualifier applied to every head would move every diagnostic the
// compiler prints while both assertions above stayed green — and the compiler's
// own registry refuses a duplicate code, so its conditions have nothing to be
// qualified by and nothing to disambiguate.
func TestCompilerRaisedConditionPrintsAnUnqualifiedCode(t *testing.T) {
	t.Parallel()

	// A condition the compiler raises itself, reached by ordinary Go wrapping, so
	// the chain crossed no plugin socket and reports no raising plugin. It is
	// deliberately a condition from another package rather than the guest-error
	// one: building an error around that sentinel outside its own home is what the
	// chokepoint guard beside it refuses, test files included.
	condition := resolver.ErrParameterContractViolation()
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatal("the condition under test reports no short code, so the head below carries nothing to assert")
	}
	framed := cerrors.Unidentified(cerrors.DeveloperError,
		cerrors.SourceLocation{File: "aggregate.yaml"}, "resolving the Aggregate", nil,
		fmt.Errorf("resolver: %w", condition))

	got := formatCompilerError(framed)
	wantHead := "[" + string(cerrors.DeveloperError) + " " + short + "]"
	if !strings.HasPrefix(got, wantHead) {
		t.Errorf("a condition no plugin raised must print its code unqualified.\n  want prefix: %s\n  got:\n%s", wantHead, got)
	}
	if strings.Contains(got, "/"+short) {
		t.Errorf("a condition no plugin raised printed a qualified code, so every compiler diagnostic has moved:\n%s", got)
	}
}

// TestGuestFailureWithNoConditionPrintsTheHostCode is the control for the test
// above: with no code reported, the head falls back to the host's own condition
// rather than printing an empty bracket or no code at all.
//
// It is here because the two outcomes are one edit apart and only one of them is
// correct, and a test asserting only the populated case cannot tell them apart.
//
// It carries the second half of that control too, and the half is the one a
// plausible implementation gets wrong: the failure DID cross a plugin socket and
// the host DOES know which plugin answered, yet the head must print the host's
// code unqualified — because the condition that stands is the compiler's own, and
// qualifying it would say the plugin allocated a code it did not.
func TestGuestFailureWithNoConditionPrintsTheHostCode(t *testing.T) {
	t.Parallel()

	const guestPlugin = "provider/compose-provider"

	guestErr := wasm.GuestReturnedError(guestPlugin, "render_node", wasm.GuestFailure{
		Message: "compose-provider: a failure naming no registered condition",
	})
	framed := cerrors.Unidentified(cerrors.TranslationError,
		cerrors.SourceLocation{Instance: "web"}, "rendering node \"web\"", nil, guestErr)

	got := formatCompilerError(framed)
	hostShort, ok := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	if !ok {
		t.Fatal("the host's guest-error condition reports no short code, so the head under test cannot be built")
	}
	wantHead := "[" + string(cerrors.TranslationError) + " " + hostShort + "]"
	if !strings.HasPrefix(got, wantHead) {
		t.Errorf("with no code reported the head should carry the host's own condition, unqualified.\n  want prefix: %s\n  got:\n%s", wantHead, got)
	}
	if strings.Contains(got, guestPlugin+"/"+hostShort) {
		t.Errorf("the host's own condition was qualified by %s, which claims that plugin allocated a code the compiler registered:\n%s", guestPlugin, got)
	}
}
