// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
func TestGuestReportedConditionReachesThePrintedHead(t *testing.T) {
	t.Parallel()

	const (
		guestCode  = "compiler-renderer-compose/unsupported-compose-lifecycle"
		guestShort = "E0241"
	)

	guestErr := wasm.GuestReturnedError("render_node", wasm.GuestFailure{
		Message:   "compose: node \"web\" declares container lifecycle hooks; compose has no lifecycle-hook primitive",
		Code:      guestCode,
		ShortCode: guestShort,
	})
	// The host frames the guest's failure the way the render path does: its own
	// categorised envelope, pointing at the node, over the guest's error.
	framed := cerrors.New(cerrors.TranslationError,
		cerrors.SourceLocation{File: "web"}, "rendering node \"web\"", nil, guestErr)

	got := formatCompilerError(framed)
	wantHead := "[" + string(cerrors.TranslationError) + " " + guestShort + "]"
	if !strings.HasPrefix(got, wantHead) {
		t.Errorf("the printed head does not carry the code the guest reported.\n  want prefix: %s\n  got:\n%s", wantHead, got)
	}
	// The host's own generic plugin-failure code must not be what prints: it would
	// name a condition the reader cannot look up against the failure they met.
	hostShort, ok := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	if !ok {
		t.Fatal("the host's guest-error condition reports no short code, so the control below asserts nothing")
	}
	if strings.Contains(got, "["+string(cerrors.TranslationError)+" "+hostShort+"]") {
		t.Errorf("the printed head carries the host's own code %s instead of the guest's %s:\n%s", hostShort, guestShort, got)
	}
}

// TestGuestFailureWithNoConditionPrintsTheHostCode is the control for the test
// above: with no code reported, the head falls back to the host's own condition
// rather than printing an empty bracket or no code at all.
//
// It is here because the two outcomes are one edit apart and only one of them is
// correct, and a test asserting only the populated case cannot tell them apart.
func TestGuestFailureWithNoConditionPrintsTheHostCode(t *testing.T) {
	t.Parallel()

	guestErr := wasm.GuestReturnedError("render_node", wasm.GuestFailure{
		Message: "compose-provider: a failure naming no registered condition",
	})
	framed := cerrors.New(cerrors.TranslationError,
		cerrors.SourceLocation{File: "web"}, "rendering node \"web\"", nil, guestErr)

	got := formatCompilerError(framed)
	hostShort, ok := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	if !ok {
		t.Fatal("the host's guest-error condition reports no short code, so the head under test cannot be built")
	}
	wantHead := "[" + string(cerrors.TranslationError) + " " + hostShort + "]"
	if !strings.HasPrefix(got, wantHead) {
		t.Errorf("with no code reported the head should carry the host's own condition.\n  want prefix: %s\n  got:\n%s", wantHead, got)
	}
}
