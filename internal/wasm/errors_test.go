// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestGuestReturnedError proves the guest-returned-error taxonomy: an empty
// message is the success case (nil), a non-empty message classifies as
// ErrWasmGuestError and names the export and the reason, and the guest
// structured-error sentinel is distinct from the crash-path sentinels so the
// three guest-failure transports converge on one classifiable taxonomy without
// collapsing into each other.
func TestGuestReturnedError(t *testing.T) {
	if err := GuestReturnedError("provider/example-provider", "render_node", GuestFailure{}); err != nil {
		t.Fatalf("an empty message is the success case and must yield nil, got %v", err)
	}

	err := GuestReturnedError("provider/example-provider", "render_node", GuestFailure{Message: "template evaluation failed"})
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
// identifiers of the condition a guest chose reach the error the host builds, and
// that the host taxonomy still classifies underneath them.
//
// Both halves are asserted because either alone would pass on a wrong
// implementation: reporting the codes by REPLACING the host condition carries them
// and silently stops matching the taxonomy every caller classifies with, and
// keeping the host condition without presenting the codes matches and reports the
// wrong condition.
//
// The sentence is asserted in both directions beside them, because an
// implementation can get the identifiers right and still print the host's
// wording — leaving a diagnostic whose code resolves to one sentence and whose
// text says another.
func TestGuestReturnedError_ReportedConditionStandsOverTheHostCondition(t *testing.T) {
	chosen := conditionReport(t, plugincondition.ErrPluginCannotExpressInput(), "the occurrence's own clause")

	err := GuestReturnedError("provider/example-provider", "render_node", chosen)
	if got, ok := cerrors.CodeOf(err); !ok || got != chosen.Code {
		t.Errorf("the error reports descriptive code %q (found=%t); want the chosen condition's %q", got, ok, chosen.Code)
	}
	if got, ok := cerrors.ShortCodeOf(err); !ok || got != chosen.ShortCode {
		t.Errorf("the error reports short code %q (found=%t); want the chosen condition's %q", got, ok, chosen.ShortCode)
	}
	if !errors.Is(err, ErrWasmGuestError()) {
		t.Error("presenting the guest's identifiers must not displace the host taxonomy")
	}
	// The sentence beside the code is the CHOSEN condition's, because the head a
	// reader meets carries the chosen condition's code: a line whose code resolves
	// to one sentence and whose text says another describes two conditions at once,
	// and the reader looking the code up is told something the line did not say.
	// It is read off the identity rather than written out here, so the wording
	// still has exactly one home.
	if !strings.Contains(err.Error(), plugincondition.ErrPluginCannotExpressInput().Error()) {
		t.Errorf("the error no longer carries the chosen condition's registered sentence, so the code in "+
			"the head and the sentence beside it name different conditions, got %q", err.Error())
	}
	// And the host's own sentence is NOT what prints, which is the other half of
	// the same rule. Asserted rather than left implied: an implementation keeping
	// the host's wording would satisfy every assertion above — the identifiers are
	// read off the chosen condition and the taxonomy still matches beneath — while
	// printing the one sentence the head's code does not resolve to.
	if strings.Contains(err.Error(), ErrWasmGuestError().Error()) {
		t.Errorf("the error carries the host condition's registered sentence, which the head's short code "+
			"does not resolve to, got %q", err.Error())
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
			err := GuestReturnedError("provider/example-provider", "render_node", tc.f)
			code, _ := cerrors.CodeOf(err)
			short, _ := cerrors.ShortCodeOf(err)
			if code != hostCode || short != hostShort {
				t.Errorf("a half report was attached: the error reports %q / %q; the host's own condition "+
					"(%q / %q) should stand", code, short, hostCode, hostShort)
			}
			// The qualifier rides on the condition, so a half report must carry no
			// plugin either: the condition that stands is the host's own, and
			// qualifying the compiler's condition by a plugin would say the plugin
			// allocated a code it did not.
			if plugin, ok := cerrors.RaisingPluginOf(err); ok {
				t.Errorf("a half report reports raising plugin %q; the host's own condition stands and carries none", plugin)
			}
		})
	}
}

// TestGuestReturnedError_TwoPluginsOneCodeStayDistinguishable proves the property
// the qualifier exists for: two guests reporting the SAME code are told apart by
// the plugin the host invoked.
//
// Two guests reporting one code is the ordinary case rather than a collision, and
// that is what makes the qualifier load-bearing. The conditions a plugin chooses
// from name a KIND of failure and no place, so every guest that cannot express a
// construct reports the same code; the plugin is the only thing in the diagnostic
// that says which of a build's attached guests to open.
//
// It is asserted at this layer as well as at the printing boundary because the two
// can fail independently — the read can lose the plugin here, and the composition
// can drop it there — and a reader meeting two identical heads has no way back to
// which plugin they held.
func TestGuestReturnedError_TwoPluginsOneCodeStayDistinguishable(t *testing.T) {
	const (
		first  = "provider/example-provider"
		second = "provider/another-provider"
	)

	failure := conditionReport(t, plugincondition.ErrPluginCannotExpressInput(), "the occurrence's own clause")
	sharedCode, sharedShort := failure.Code, failure.ShortCode
	errFirst := GuestReturnedError(first, "render_node", failure)
	errSecond := GuestReturnedError(second, "render_node", failure)

	gotFirst, ok := cerrors.RaisingPluginOf(errFirst)
	if !ok || gotFirst != first {
		t.Errorf("the first error reports raising plugin %q (found=%t); want %q", gotFirst, ok, first)
	}
	gotSecond, ok := cerrors.RaisingPluginOf(errSecond)
	if !ok || gotSecond != second {
		t.Errorf("the second error reports raising plugin %q (found=%t); want %q", gotSecond, ok, second)
	}
	if gotFirst == gotSecond {
		t.Fatal("two guests reporting one code are indistinguishable, which is the whole condition the qualifier removes")
	}

	// The codes themselves stay exactly the chosen condition's. A machine consumer
	// matches the code the condition is registered under; the qualifier is a third
	// read beside it, never folded into it.
	for _, err := range []error{errFirst, errSecond} {
		if got, ok := cerrors.ShortCodeOf(err); !ok || got != sharedShort {
			t.Errorf("the short code reports %q (found=%t); want the unqualified %q", got, ok, sharedShort)
		}
		if got, ok := cerrors.CodeOf(err); !ok || got != sharedCode {
			t.Errorf("the descriptive code reports %q (found=%t); want the unqualified %q", got, ok, sharedCode)
		}
	}
}
