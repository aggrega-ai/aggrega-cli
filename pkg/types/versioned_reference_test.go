// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"strings"
	"testing"
)

// TestParseVersionPin pins the pin grammar the one parser states: which pins are
// judged, which are well formed, which are malformed, and that the split it
// reports is the split SplitVersionedURI states. A malformed row asserts the
// condition through its accessor, never through its code.
func TestParseVersionPin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		reference  string
		wantBase   string
		wantPin    string
		wantHasPin bool
		wantCaret  bool
		wantExact  bool
		wantRange  bool
		malformed  bool
	}{
		{"unversioned carries nothing to judge", "core/api-server", "core/api-server", "", false, false, false, false, false},
		{"exact strict semantic version", "core/api-server:1.0.0", "core/api-server", "1.0.0", true, false, true, false, false},
		{"exact prerelease and build metadata", "core/api-server:1.0.0-rc.1+build.5", "core/api-server", "1.0.0-rc.1+build.5", true, false, true, false, false},
		{"caret range", "core/api-server:^1.0.0", "core/api-server", "^1.0.0", true, true, false, true, false},
		{"caret with a wildcard minor parses as a range", "core/api-server:^1.x", "core/api-server", "^1.x", true, true, false, true, false},
		{"registry-qualified exact", "hub.aggrega.ai/component/core/api-server:1.0.0", "hub.aggrega.ai/component/core/api-server", "1.0.0", true, false, true, false, false},
		{"registry host with a port and a pin", "localhost:5000/core/api-server:^1.0.0", "localhost:5000/core/api-server", "^1.0.0", true, true, false, true, false},
		// The port colon is the last colon here, so the split reads the rest of the
		// reference as its version. A pin never carries a "/", so that segment is
		// not judged — the guard that keeps a port from being a malformed pin.
		{"registry host with a port and no pin is not judged", "localhost:5000/core/api-server", "localhost", "5000/core/api-server", true, false, false, false, false},
		{"caret whose range does not parse", "core/api-server:^garbage", "core/api-server", "^garbage", true, true, false, false, true},
		{"exact wildcard is not a semantic version", "core/api-server:1.x", "core/api-server", "1.x", true, false, false, false, true},
		{"exact with two segments is not a strict semantic version", "core/api-server:1.0", "core/api-server", "1.0", true, false, false, false, true},
		{"exact with a leading v is not a strict semantic version", "core/api-server:v1.0.0", "core/api-server", "v1.0.0", true, false, false, false, true},
		{"exact comparison operator is not a pin form", "core/api-server:>=1.0.0", "core/api-server", ">=1.0.0", true, false, false, false, true},
		{"trailing colon is an empty exact pin", "core/api-server:", "core/api-server", "", true, false, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := ParseVersionPin(tc.reference)

			wantBase, wantPin, wantHasPin := SplitVersionedURI(tc.reference)
			if wantBase != tc.wantBase || wantPin != tc.wantPin || wantHasPin != tc.wantHasPin {
				t.Fatalf("row disagrees with SplitVersionedURI(%q) = (%q, %q, %v); correct the row",
					tc.reference, wantBase, wantPin, wantHasPin)
			}
			if p.Base() != tc.wantBase {
				t.Errorf("Base() = %q, want %q", p.Base(), tc.wantBase)
			}
			if pin, hasPin := p.Pin(); pin != tc.wantPin || hasPin != tc.wantHasPin {
				t.Errorf("Pin() = (%q, %v), want (%q, %v)", pin, hasPin, tc.wantPin, tc.wantHasPin)
			}
			if p.IsCaret() != tc.wantCaret {
				t.Errorf("IsCaret() = %v, want %v", p.IsCaret(), tc.wantCaret)
			}
			if (p.Version() != nil) != tc.wantExact {
				t.Errorf("Version() = %v, want a parsed version: %v", p.Version(), tc.wantExact)
			}
			if (p.Range() != nil) != tc.wantRange {
				t.Errorf("Range() = %v, want a parsed range: %v", p.Range(), tc.wantRange)
			}

			fault := p.Fault()
			pe, malformed := p.Malformed()
			if malformed != tc.malformed || (fault != nil) != tc.malformed {
				t.Fatalf("Fault() = %v, Malformed() = %v; want malformed %v", fault, malformed, tc.malformed)
			}
			if !tc.malformed {
				if pe != nil {
					t.Errorf("Malformed() returned %v for a well-formed pin", pe)
				}
				return
			}
			if !errors.Is(fault, ErrMalformedVersionPin()) {
				t.Errorf("Fault() = %v, want it to match ErrMalformedVersionPin()", fault)
			}
			if pe.Reference != tc.reference || pe.Pin != tc.wantPin {
				t.Errorf("fault carries (%q, %q), want (%q, %q)", pe.Reference, pe.Pin, tc.reference, tc.wantPin)
			}
		})
	}
}

// TestVersionPinErrorNamesTheReferenceThePinAndTheForms asserts what the
// refusal tells the author: the reference, the pin, and the three forms a
// reference is written in, opening with the condition's own sentence.
func TestVersionPinErrorNamesTheReferenceThePinAndTheForms(t *testing.T) {
	t.Parallel()

	pe, malformed := ParseVersionPin("core/api-server:^garbage").Malformed()
	if !malformed {
		t.Fatal("a caret pin whose range does not parse was not reported as malformed")
	}
	msg := pe.At("referenced from aggregate.spec.components.api.type").Error()
	sentence := ErrMalformedVersionPin().Error()
	if !strings.HasPrefix(msg, sentence) {
		t.Errorf("message %q does not open with the condition's sentence %q", msg, sentence)
	}
	for _, want := range []string{
		`reference "core/api-server:^garbage"`,
		`pin "^garbage"`,
		"referenced from aggregate.spec.components.api.type",
		"<name>",
		"<name>:<version>",
		"<name>:^<version>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not carry %q", msg, want)
		}
	}
	if pe.Where != "" {
		t.Errorf("At mutated the parser's own fault: Where = %q", pe.Where)
	}
}
