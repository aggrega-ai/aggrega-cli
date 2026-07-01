// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package deprecation

import (
	"strings"
	"testing"
)

// TestValidateAcceptsThreeSeverities proves the three known severities are
// accepted with a non-empty message.
func TestValidateAcceptsThreeSeverities(t *testing.T) {
	for _, sev := range []Severity{SeverityWarning, SeverityError, SeverityRetracted} {
		if err := (Marker{Severity: sev, Message: "migrate away"}).Validate(); err != nil {
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

// TestValidateRejectsEmptyMessage proves an empty message is rejected even for
// a known severity.
func TestValidateRejectsEmptyMessage(t *testing.T) {
	if err := (Marker{Severity: SeverityWarning}).Validate(); err == nil {
		t.Fatal("empty message should be rejected")
	}
}

// TestMarshalRejectsInvalid proves Marshal refuses an invalid marker rather
// than emitting bytes for it.
func TestMarshalRejectsInvalid(t *testing.T) {
	if _, err := Marshal(Marker{Severity: "nope", Message: "x"}); err == nil {
		t.Fatal("Marshal should reject an invalid marker")
	}
}

// TestMarshalIsDeterministicAndCanonical proves Marshal produces byte-identical
// output for identical inputs, with fixed field order and no trailing newline —
// the property that keeps the marker's manifest digest (which a signature
// commits to) stable.
func TestMarshalIsDeterministicAndCanonical(t *testing.T) {
	m := Marker{Severity: SeverityError, Message: "use core/api-server:2 instead"}
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
	want := `{"severity":"error","message":"use core/api-server:2 instead"}`
	if string(a) != want {
		t.Errorf("canonical form = %q, want %q", a, want)
	}
	if strings.HasSuffix(string(a), "\n") {
		t.Errorf("canonical form should carry no trailing newline: %q", a)
	}
}

// TestUnmarshalRoundTrips proves Unmarshal recovers a marshalled marker and
// rejects bytes carrying an unknown severity.
func TestUnmarshalRoundTrips(t *testing.T) {
	m := Marker{Severity: SeverityRetracted, Message: "withdrawn"}
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
	if _, err := Unmarshal([]byte(`{"severity":"bogus","message":"x"}`)); err == nil {
		t.Error("Unmarshal should reject an unknown severity")
	}
}
