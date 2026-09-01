// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestGuestReturnedError proves the guest-returned-error taxonomy: an empty
// message is the success case (nil), a non-empty message classifies as
// ErrWasmGuestError and names the export and the reason, and the guest
// structured-error sentinel is distinct from the crash-path sentinels so the
// three guest-failure transports converge on one classifiable taxonomy without
// collapsing into each other.
func TestGuestReturnedError(t *testing.T) {
	if err := GuestReturnedError("render_node", GuestFailure{}); err != nil {
		t.Fatalf("an empty message is the success case and must yield nil, got %v", err)
	}

	err := GuestReturnedError("render_node", GuestFailure{Message: "template evaluation failed"})
	if err == nil {
		t.Fatal("a non-empty message must yield an error")
	}
	if !errors.Is(err, ErrWasmGuestError()) {
		t.Errorf("a guest-returned error must classify as ErrWasmGuestError, got %v", err)
	}
	if !strings.Contains(err.Error(), "render_node") || !strings.Contains(err.Error(), "template evaluation failed") {
		t.Errorf("the error must name the export and the reason, got %q", err.Error())
	}
	if errors.Is(err, ErrWasmGuestPanic()) || errors.Is(err, ErrWasmPluginExited()) {
		t.Error("a guest structured error must not classify as a crash or a proc_exit")
	}
}

// TestGuestReturnedError_ReportedConditionStandsOverTheHostCondition proves the
// identifiers a guest reports reach the error the host builds, and that the host
// taxonomy still classifies underneath them.
//
// Both halves are asserted because either alone would pass on a wrong
// implementation: reporting the codes by REPLACING the host condition carries them
// and silently stops matching the taxonomy every caller classifies with, and
// keeping the host condition without presenting the codes matches and reports the
// wrong condition.
func TestGuestReturnedError_ReportedConditionStandsOverTheHostCondition(t *testing.T) {
	const (
		guestCode  = "example-provider-core/a-condition-the-guest-publishes"
		guestShort = "E0000"
	)

	err := GuestReturnedError("render_node", GuestFailure{
		Message:   "the occurrence's own clause",
		Code:      guestCode,
		ShortCode: guestShort,
	})
	if got, ok := cerrors.CodeOf(err); !ok || got != guestCode {
		t.Errorf("the error reports descriptive code %q (found=%t); want the guest's %q", got, ok, guestCode)
	}
	if got, ok := cerrors.ShortCodeOf(err); !ok || got != guestShort {
		t.Errorf("the error reports short code %q (found=%t); want the guest's %q", got, ok, guestShort)
	}
	if !errors.Is(err, ErrWasmGuestError()) {
		t.Error("presenting the guest's identifiers must not displace the host taxonomy")
	}
	// The wording of "a guest reported an error" has one home, so this type
	// contributes identifiers and never a second copy of that sentence.
	if !strings.Contains(err.Error(), ErrWasmGuestError().Error()) {
		t.Errorf("the error no longer carries the host condition's registered sentence, got %q", err.Error())
	}
}

// TestGuestReturnedError_HalfReportNamesNoCondition proves the both-or-neither
// pairing rule in both of its half shapes: a report carrying one identifier and
// not the other names no condition, so the host's own stands.
//
// Left unasserted, a half report would put one accessor answering for the guest's
// condition and the other for the host's on one error, with every gate green while
// a reader resolved two different conditions from one diagnostic.
func TestGuestReturnedError_HalfReportNamesNoCondition(t *testing.T) {
	hostCode, _ := cerrors.CodeOf(ErrWasmGuestError())
	hostShort, _ := cerrors.ShortCodeOf(ErrWasmGuestError())

	for _, tc := range []struct {
		name string
		f    GuestFailure
	}{
		{"a descriptive code with no short code", GuestFailure{Message: "m", Code: "x/y"}},
		{"a short code with no descriptive code", GuestFailure{Message: "m", ShortCode: "E0000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := GuestReturnedError("render_node", tc.f)
			code, _ := cerrors.CodeOf(err)
			short, _ := cerrors.ShortCodeOf(err)
			if code != hostCode || short != hostShort {
				t.Errorf("a half report was attached: the error reports %q / %q; the host's own condition "+
					"(%q / %q) should stand", code, short, hostCode, hostShort)
			}
		})
	}
}
