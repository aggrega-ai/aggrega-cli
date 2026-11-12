// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The closed-set contract, driven through the host's own boundary entry point.
//
// GuestReturnedError is what every socket calls with the failure it decoded from
// a guest's output, so a report built here crosses exactly the code a real
// guest's report crosses once the bytes have been decoded. What these assertions
// do NOT cover is the decode itself — whether a guest's wire bytes reach this
// function unaltered is the transport's subject and is asserted where the guest
// cores live, against real guests.

// conditionReport returns the report a conforming guest sends when it chooses
// the given condition: the identifiers that condition is registered under, and a
// sentence of the guest's own.
//
// The identifiers are read off the identity rather than written out, so a row
// cannot come to assert a spelling the registration does not carry — which is
// the one way a table like this can pass while describing nothing.
func conditionReport(t *testing.T, condition error, message string) GuestFailure {
	t.Helper()

	code, ok := cerrors.CodeOf(condition)
	if !ok {
		t.Fatalf("the condition reports no descriptive code, so this table describes nothing")
	}
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatalf("the condition reports no short code, so this table describes nothing")
	}
	return GuestFailure{Message: message, Code: code, ShortCode: short}
}

// TestGuestSelectableConditionsAreDeclaredAndDistinct proves the generic set a
// refusal advertises is real: every member resolves under both of its identifiers
// from this binary's own declarations, no two members share either identifier, and
// the refusal itself is not among them.
//
// The set is ADVICE rather than the admissible set — the host admits any condition
// the compiler declares — so these assertions are about what a refusal tells an
// author to write, and a member that did not resolve would be advice pointing at
// nothing.
//
// The last assertion is the load-bearing one, and what it protects is narrower
// than it looks. It does not stop a guest naming the refusal: that code resolves,
// so the host admits it, and a plugin reporting the accusation against itself is
// the same undetectable bad coding as a plugin naming any other condition its
// build did not meet — a class no check is built for. What it stops is the
// refusal's own ADVICE recommending it, which would be the host inviting exactly
// that report.
func TestGuestSelectableConditionsAreDeclaredAndDistinct(t *testing.T) {
	if len(guestSelectableConditions) == 0 {
		t.Fatal("the set is empty, so every assertion below would pass over nothing")
	}

	seenCode := map[string]bool{}
	seenShort := map[string]bool{}
	for _, condition := range guestSelectableConditions {
		report := conditionReport(t, condition, "m")

		resolved, err := haltref.Resolve(report.ShortCode)
		if err != nil {
			t.Errorf("%s does not resolve from this binary's declarations by its short code %s: %v",
				report.Code, report.ShortCode, err)
			continue
		}
		if resolved.Code != report.Code {
			t.Errorf("the short code %s resolves to %q, not to %q", report.ShortCode, resolved.Code, report.Code)
		}
		if _, err := haltref.Resolve(report.Code); err != nil {
			t.Errorf("%s does not resolve from this binary's declarations by its descriptive code: %v",
				report.Code, err)
		}
		if seenCode[report.Code] || seenShort[report.ShortCode] {
			t.Errorf("%s / %s appears in the set more than once", report.Code, report.ShortCode)
		}
		seenCode[report.Code] = true
		seenShort[report.ShortCode] = true
	}

	refusalCode, _ := cerrors.CodeOf(ErrWasmGuestConditionUndeclared())
	if seenCode[refusalCode] {
		t.Errorf("%s is in the set a refusal advertises; the refusal describes a guest's misbehaviour, "+
			"so recommending it would invite a plugin to report the accusation against itself", refusalCode)
	}
}

// TestGuestReturnedError_CarriesEveryConditionAGuestMayChoose proves the accept
// half: a report naming a member reaches a reader as that condition, under the
// identifiers the condition is registered under, with the guest's own sentence
// still in the message and the host taxonomy still matching beneath.
//
// Every member is driven rather than one of them, because membership is decided
// by a walk over the set and a walk that reached only its first entry would pass
// a single-row table exactly like this one.
func TestGuestReturnedError_CarriesEveryConditionAGuestMayChoose(t *testing.T) {
	const plugin = "provider/example-provider"

	for _, condition := range guestSelectableConditions {
		report := conditionReport(t, condition, "the occurrence's own clause")
		t.Run(report.Code, func(t *testing.T) {
			err := GuestReturnedError(plugin, "render_node", report)
			if err == nil {
				t.Fatal("a report carrying a message must yield an error")
			}
			if !errors.Is(err, condition) {
				t.Errorf("the error does not match the condition the guest chose, so nothing in Go can "+
					"classify on it: %v", err)
			}
			if !errors.Is(err, ErrWasmGuestError()) {
				t.Error("choosing a declared condition must not displace the host taxonomy")
			}
			if errors.Is(err, ErrWasmGuestConditionUndeclared()) {
				t.Error("a declared condition was refused as undeclared")
			}
			if got, ok := cerrors.CodeOf(err); !ok || got != report.Code {
				t.Errorf("the error reports descriptive code %q (found=%t); want %q", got, ok, report.Code)
			}
			if got, ok := cerrors.ShortCodeOf(err); !ok || got != report.ShortCode {
				t.Errorf("the error reports short code %q (found=%t); want %q", got, ok, report.ShortCode)
			}
			if got, ok := cerrors.RaisingPluginOf(err); !ok || got != plugin {
				t.Errorf("the error reports raising plugin %q (found=%t); want %q", got, ok, plugin)
			}
			if !strings.Contains(err.Error(), report.Message) {
				t.Errorf("the guest's own sentence is not in the message, got %q", err.Error())
			}
		})
	}
}

// TestGuestReturnedError_RefusesAConditionTheSocketDoesNotDeclare drives a guest
// report whose identifiers resolve to no condition this compiler declares, and
// proves all three halves of the refusal at once: the build halts under the host's
// own protocol condition, the offending code is quoted so the plugin's author can
// find it, and the plugin's own sentence survives into the message.
//
// Every row here leaves a reader holding an identifier that resolves to nothing,
// which is the property the refusal exists to prevent — not whether a plugin was
// the party that raised the condition. A report naming a condition the compiler
// DOES declare is admitted, and is driven by the admission test below.
//
// The third half is the one that would be easiest to drop and the most expensive
// to drop. A refusal that withheld the guest's message would tell a user that a
// plugin misbehaved while hiding what actually stopped their build, so the row
// asserts it for every shape of violation rather than once.
func TestGuestReturnedError_RefusesAConditionTheSocketDoesNotDeclare(t *testing.T) {
	const (
		plugin  = "provider/example-provider"
		message = "the occurrence's own clause"
	)

	// A number the retirement record holds. The row asserts it is still retired
	// before using it, so a change to that record fails this test loudly instead
	// of leaving the retired arm covered by a code that has quietly become
	// unknown — which is the same refusal by a different path and would look
	// exactly like a pass.
	const retiredShort = "E0002"
	if _, retired := cerrors.RetiredShortCode(retiredShort); !retired {
		t.Fatalf("%s is no longer retired, so the retired arm below is not being driven", retiredShort)
	}

	firstMember := conditionReport(t, guestSelectableConditions[0], message)
	secondMember := conditionReport(t, guestSelectableConditions[1], message)

	for _, tc := range []struct {
		name   string
		report GuestFailure
		quoted string
	}{
		{
			name:   "a short code no condition is registered under",
			report: GuestFailure{Message: message, Code: "example-core/a-condition-nobody-declares", ShortCode: "E9999"},
			quoted: "E9999",
		},
		{
			name:   "a retired short code, which the numbering promises never names a condition again",
			report: GuestFailure{Message: message, Code: "example-core/a-condition-nobody-declares", ShortCode: retiredShort},
			quoted: retiredShort,
		},
		{
			name: "two identifiers that name two different conditions",
			report: GuestFailure{
				Message:   message,
				Code:      secondMember.Code,
				ShortCode: firstMember.ShortCode,
			},
			quoted: firstMember.ShortCode,
		},
		{
			name: "a member's short code in a spelling the registration does not carry",
			report: GuestFailure{
				Message:   message,
				Code:      firstMember.Code,
				ShortCode: strings.ToLower(firstMember.ShortCode),
			},
			quoted: strings.ToLower(firstMember.ShortCode),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := GuestReturnedError(plugin, "render_node", tc.report)
			if err == nil {
				t.Fatal("a report naming a condition the socket does not declare must halt the build")
			}
			if !errors.Is(err, ErrWasmGuestConditionUndeclared()) {
				t.Errorf("the refusal does not classify as the socket's own protocol condition, got %v", err)
			}
			if !errors.Is(err, ErrWasmGuestError()) {
				t.Error("a refused report is still a guest failure and must classify as one")
			}
			refusalShort, _ := cerrors.ShortCodeOf(ErrWasmGuestConditionUndeclared())
			if got, ok := cerrors.ShortCodeOf(err); !ok || got != refusalShort {
				t.Errorf("the error reports short code %q (found=%t); want the refusal's %q, because the "+
					"offered code names no condition and publishing it would resolve to nothing",
					got, ok, refusalShort)
			}
			if !strings.Contains(err.Error(), tc.quoted) {
				t.Errorf("the refusal does not quote the offending code %q, so its author cannot find it; got %q",
					tc.quoted, err.Error())
			}
			if !strings.Contains(err.Error(), message) {
				t.Errorf("the plugin's own sentence did not survive the refusal, so a user is told a plugin "+
					"misbehaved and not what stopped their build; got %q", err.Error())
			}
			if got, ok := cerrors.RaisingPluginOf(err); !ok || got != plugin {
				t.Errorf("the refusal reports raising plugin %q (found=%t); want %q, without which a reader "+
					"cannot tell which of a build's guests broke the contract", got, ok, plugin)
			}
			for _, condition := range guestSelectableConditions {
				code, _ := cerrors.CodeOf(condition)
				if !strings.Contains(err.Error(), code) {
					t.Errorf("the refusal does not name %s among the conditions a plugin may choose, so it "+
						"says what is wrong and not what to write instead; got %q", code, err.Error())
				}
			}
		})
	}
}

// TestGuestReturnedError_AdmitsAnyConditionTheCompilerDeclares drives a report
// naming a condition the compiler declares for its OWN use and proves the host
// reports it as that condition rather than refusing it.
//
// This is the regression the admission rule exists to prevent, and it is not
// hypothetical. Shared compiler code is linked into the provider guests as well as
// into the host, so the conditions that code registers are raised INSIDE a guest
// and cross this socket as guest failures — chosen by no plugin. A host admitting
// only the generic set halted those under a protocol violation that named the
// plugin for a mistake the build's own author had made, and took a resolvable
// identity away from the reader at the same time.
//
// The condition driven here is one the host itself declares for its own use, which
// is the sharpest available form of the case: nothing about it is a plugin's to
// report, and it is still admitted, because the test is whether the code resolves.
// Its identifiers are read off the registration rather than written out, so the
// row cannot come to assert a spelling the registration does not carry.
func TestGuestReturnedError_AdmitsAnyConditionTheCompilerDeclares(t *testing.T) {
	const (
		plugin  = "provider/example-provider"
		message = "the occurrence's own clause"
	)

	compilerOwned := conditionReport(t, ErrWasmOutputShape(), message)

	err := GuestReturnedError(plugin, "render_node", compilerOwned)
	if err == nil {
		t.Fatal("a report carrying a message must yield an error")
	}
	if errors.Is(err, ErrWasmGuestConditionUndeclared()) {
		t.Fatalf("a condition the compiler declares was refused as undeclared, which accuses the "+
			"plugin for a condition it did not choose: %v", err)
	}
	if got, ok := cerrors.ShortCodeOf(err); !ok || got != compilerOwned.ShortCode {
		t.Errorf("the error reports short code %q (found=%t); want %q, without which the reader loses "+
			"a code that resolves", got, ok, compilerOwned.ShortCode)
	}
	if got, ok := cerrors.CodeOf(err); !ok || got != compilerOwned.Code {
		t.Errorf("the error reports descriptive code %q (found=%t); want %q", got, ok, compilerOwned.Code)
	}
	if !errors.Is(err, ErrWasmGuestError()) {
		t.Error("admitting a compiler-declared condition must not displace the host taxonomy")
	}
	if !errors.Is(err, ErrWasmOutputShape()) {
		t.Error("the error does not match the condition the report named, so nothing in Go can classify on it")
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("the guest's own sentence is not in the message, got %q", err.Error())
	}
	// The condition's REGISTERED sentence is what the host contributes, read from
	// the registry rather than from anything the guest sent — which is what keeps a
	// guest from putting a sentence of its own where the condition's belongs.
	registered, ok := cerrors.MessageOf(ErrWasmOutputShape())
	if !ok {
		t.Fatal("the condition carries no registered sentence, so the assertion below describes nothing")
	}
	if !strings.Contains(err.Error(), registered) {
		t.Errorf("the condition's registered sentence is missing, so the head names a condition whose "+
			"wording the line does not carry; got %q", err.Error())
	}
}

// TestGuestReturnedError_RefusalDoesNotClaimTheOfferedCode proves the refused
// code is quoted in the sentence and reported as an identifier nowhere.
//
// Asserted apart from the table above because it is the property a reasonable
// implementation gets wrong in the opposite direction: carrying the guest's code
// through onto the error reads as preserving information, and it publishes an
// identifier that resolves to nothing — which is the exact failure the closed set
// exists to remove.
func TestGuestReturnedError_RefusalDoesNotClaimTheOfferedCode(t *testing.T) {
	const offered = "E9999"

	err := GuestReturnedError("provider/example-provider", "render_node", GuestFailure{
		Message:   "the occurrence's own clause",
		Code:      "example-core/a-condition-nobody-declares",
		ShortCode: offered,
	})
	if got, _ := cerrors.ShortCodeOf(err); got == offered {
		t.Errorf("the refusal reports the offered short code %q as its own identifier; a reader quoting it "+
			"would resolve nothing", offered)
	}
	if got, _ := cerrors.CodeOf(err); got == "example-core/a-condition-nobody-declares" {
		t.Errorf("the refusal reports the offered descriptive code %q as its own identifier", got)
	}
}
