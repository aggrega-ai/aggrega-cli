// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package deprecation

import (
	"strings"
	"testing"
)

// TestValidateAcceptsThreeSeverities proves the three known severities are
// accepted with a non-empty reason.
func TestValidateAcceptsThreeSeverities(t *testing.T) {
	for _, sev := range []Severity{SeverityWarning, SeverityError, SeverityRetracted} {
		if err := (Marker{Severity: sev, Reason: "migrate away"}).Validate(); err != nil {
			t.Errorf("severity %q should validate, got %v", sev, err)
		}
	}
}

// TestValidateRejectsUnknownSeverity proves an unknown severity is rejected.
func TestValidateRejectsUnknownSeverity(t *testing.T) {
	err := Marker{Severity: "critical", Message: "x"}.Validate()
	if err == nil {
		t.Fatal("unknown severity should be rejected")
	}
	if !strings.Contains(err.Error(), "unknown severity") {
		t.Errorf("error %q lacks the unknown-severity diagnostic", err.Error())
	}
}

// TestValidateRejectsEmptyReason proves an empty reason is rejected even for a
// known severity.
func TestValidateRejectsEmptyReason(t *testing.T) {
	if err := (Marker{Severity: SeverityWarning}).Validate(); err == nil {
		t.Fatal("empty reason should be rejected")
	}
}

// TestValidateRejectsMalformedIssuedOn proves a present-but-unparseable issuedOn
// is rejected, while a blank issuedOn is tolerated.
func TestValidateRejectsMalformedIssuedOn(t *testing.T) {
	if err := (Marker{Severity: SeverityWarning, Reason: "x", IssuedOn: "not-a-time"}).Validate(); err == nil {
		t.Fatal("malformed issuedOn should be rejected")
	}
	if err := (Marker{Severity: SeverityWarning, Reason: "x"}).Validate(); err != nil {
		t.Fatalf("blank issuedOn should be tolerated, got %v", err)
	}
	if err := (Marker{Severity: SeverityWarning, Reason: "x", IssuedOn: "2026-01-02T03:04:05Z"}).Validate(); err != nil {
		t.Fatalf("valid RFC-3339 issuedOn should pass, got %v", err)
	}
}

// TestIssuedOnTime proves the ordering key parses a valid timestamp and treats a
// blank or malformed one as absent (zero time, sorts oldest).
func TestIssuedOnTime(t *testing.T) {
	if _, ok := (Marker{}).IssuedOnTime(); ok {
		t.Error("blank issuedOn should report absent")
	}
	if _, ok := (Marker{IssuedOn: "garbage"}).IssuedOnTime(); ok {
		t.Error("malformed issuedOn should report absent")
	}
	tm, ok := (Marker{IssuedOn: "2026-01-02T03:04:05Z"}).IssuedOnTime()
	if !ok || tm.IsZero() {
		t.Errorf("valid issuedOn should parse, got ok=%v tm=%v", ok, tm)
	}
}

// TestMarshalRejectsInvalid proves Marshal refuses an invalid marker rather
// than emitting bytes for it.
func TestMarshalRejectsInvalid(t *testing.T) {
	if _, err := Marshal(Marker{Severity: "nope", Reason: "x"}); err == nil {
		t.Fatal("Marshal should reject an invalid marker")
	}
}

// TestMarshalIsDeterministicAndCanonical proves Marshal produces byte-identical
// output for identical inputs, with fixed field order and no trailing newline —
// the property that keeps the marker's manifest digest (which a signature
// commits to) stable.
func TestMarshalIsDeterministicAndCanonical(t *testing.T) {
	m := Marker{
		Severity:    SeverityError,
		Message:     "use core/api-server:2 instead",
		Reason:      "superseded by the v2 line",
		Replacement: "core/api-server:2",
		IssuedOn:    "2026-01-02T03:04:05Z",
	}
	a, err := Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b, err := Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("Marshal not deterministic: %q vs %q", a, b)
	}
	want := `{"severity":"error","message":"use core/api-server:2 instead","reason":"superseded by the v2 line","replacement":"core/api-server:2","issuedOn":"2026-01-02T03:04:05Z"}`
	if string(a) != want {
		t.Errorf("canonical form = %q, want %q", a, want)
	}
	if strings.HasSuffix(string(a), "\n") {
		t.Errorf("canonical form should carry no trailing newline: %q", a)
	}
}

// TestMarshalOmitsEmptyOptionalFields proves the optional message, replacement,
// and issuedOn fields are omitted when empty, keeping the canonical form compact
// and deterministic for a minimal marker.
func TestMarshalOmitsEmptyOptionalFields(t *testing.T) {
	a, err := Marshal(Marker{Severity: SeverityWarning, Reason: "migrate away"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"severity":"warning","reason":"migrate away"}`
	if string(a) != want {
		t.Errorf("canonical form = %q, want %q", a, want)
	}
}

// TestUnmarshalRoundTrips proves Unmarshal recovers a marshalled marker and
// rejects bytes carrying an unknown severity.
func TestUnmarshalRoundTrips(t *testing.T) {
	m := Marker{
		Severity:    SeverityRetracted,
		Message:     "withdrawn",
		Reason:      "withdrawn for a security defect",
		Replacement: "core/api-server:2",
		IssuedOn:    "2026-03-04T05:06:07Z",
	}
	data, err := Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != m {
		t.Errorf("round-trip = %+v, want %+v", got, m)
	}
	if _, err := Unmarshal([]byte(`{"severity":"bogus","reason":"x"}`)); err == nil {
		t.Error("Unmarshal should reject an unknown severity")
	}
}

// TestMeetsThreshold proves the severity ordering warning < error < retracted
// governs the build-time halt gate: a severity meets a threshold iff it is at
// least as severe, and any unknown severity or unknown threshold never meets.
func TestMeetsThreshold(t *testing.T) {
	cases := []struct {
		severity  Severity
		threshold Severity
		want      bool
	}{
		// At-threshold and above meet.
		{SeverityWarning, SeverityWarning, true},
		{SeverityError, SeverityWarning, true},
		{SeverityRetracted, SeverityWarning, true},
		{SeverityError, SeverityError, true},
		{SeverityRetracted, SeverityError, true},
		{SeverityRetracted, SeverityRetracted, true},
		// Below threshold does not meet.
		{SeverityWarning, SeverityError, false},
		{SeverityWarning, SeverityRetracted, false},
		{SeverityError, SeverityRetracted, false},
		// Unknown severity never triggers; unknown threshold never suppresses.
		{"bogus", SeverityWarning, false},
		{SeverityRetracted, "bogus", false},
		{"bogus", "bogus", false},
	}
	for _, c := range cases {
		if got := MeetsThreshold(c.severity, c.threshold); got != c.want {
			t.Errorf("MeetsThreshold(%q, %q) = %v, want %v", c.severity, c.threshold, got, c.want)
		}
	}
}
