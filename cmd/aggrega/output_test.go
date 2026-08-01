// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestParseFormatAcceptsMembers pins that an accepted value comes back unchanged,
// which is what lets a caller switch on a readable name instead of an ordinal.
func TestParseFormatAcceptsMembers(t *testing.T) {
	t.Parallel()

	for _, want := range []string{"table", "json", "names"} {
		got, err := parseFormat("search", want, "table", "json", "names")
		if err != nil {
			t.Errorf("parseFormat(%q): unexpected error %v", want, err)
			continue
		}
		if got != want {
			t.Errorf("parseFormat(%q) = %q; want the value unchanged", want, got)
		}
	}
}

// TestParseFormatRejectsUnknown pins the diagnostic: it names the command, the
// rejected value, and the accepted set in the order the help lists it. The set's
// order is asserted because the help text and the error have to agree, and a
// caller that reorders one without the other is the drift this checks for.
func TestParseFormatRejectsUnknown(t *testing.T) {
	t.Parallel()

	_, err := parseFormat("plugin list", "yaml", "table", "json")
	if err == nil {
		t.Fatal("expected an error for an unknown format value")
	}
	msg := err.Error()
	for _, want := range []string{"aggrega plugin list", `"yaml"`, "table, json"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected the diagnostic to contain %q; got %q", want, msg)
		}
	}
}

// TestParseFormatRejectsEmpty pins that an explicitly empty value is a rejection
// rather than a silent fall-through to the first accepted member.
func TestParseFormatRejectsEmpty(t *testing.T) {
	t.Parallel()

	if _, err := parseFormat("version", "", "text", "json"); err == nil {
		t.Error("expected an error for an empty format value")
	}
}

type seamPayload struct {
	Name  string `json:"name"`
	Nest  []int  `json:"nest"`
	Angle string `json:"angle"`
}

func seamFixture() seamPayload {
	// The angle brackets matter: encoding/json escapes them, and the two marshal
	// entry points would be a plausible place for that to differ.
	return seamPayload{Name: "helm", Nest: []int{1, 2}, Angle: "a<b>c&d"}
}

// TestWriteStructuredIndented pins the indented shape: two-space indentation and
// exactly one trailing newline, byte-identical to marshalling indented by hand.
func TestWriteStructuredIndented(t *testing.T) {
	t.Parallel()

	var got bytes.Buffer
	if err := writeStructured(&got, "search", structuredIndented, seamFixture()); err != nil {
		t.Fatalf("writeStructured: %v", err)
	}

	want, err := json.MarshalIndent(seamFixture(), "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if got.String() != string(want)+"\n" {
		t.Errorf("indented shape drifted:\n got %q\nwant %q", got.String(), string(want)+"\n")
	}
	if !strings.Contains(got.String(), "\n  \"name\"") {
		t.Errorf("expected two-space indentation; got %q", got.String())
	}
}

// TestWriteStructuredCompactMatchesStreamingEncoder pins the compact shape
// against encoding/json's streaming encoder, which is the byte shape the
// consumers of the compact surface already depend on. Asserting the equivalence
// rather than a literal keeps the guarantee meaningful as a payload changes: what
// must hold is that routing a surface through this seam does not alter its bytes.
func TestWriteStructuredCompactMatchesStreamingEncoder(t *testing.T) {
	t.Parallel()

	var got bytes.Buffer
	if err := writeStructured(&got, "version", structuredCompact, seamFixture()); err != nil {
		t.Fatalf("writeStructured: %v", err)
	}

	var want bytes.Buffer
	if err := json.NewEncoder(&want).Encode(seamFixture()); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if got.String() != want.String() {
		t.Errorf("compact shape is not byte-identical to the streaming encoder:\n got %q\nwant %q",
			got.String(), want.String())
	}
	if n := strings.Count(got.String(), "\n"); n != 1 {
		t.Errorf("expected exactly one newline, got %d in %q", n, got.String())
	}
	if strings.Contains(got.String(), `": `) {
		t.Errorf("expected no space after a key's colon; got %q", got.String())
	}
}

// TestWriteStructuredWrapsMarshalFailure pins that a marshal failure names the
// surface that produced it rather than surfacing a bare encoding/json error, so
// the diagnostic says which command's output could not be built.
func TestWriteStructuredWrapsMarshalFailure(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := writeStructured(&buf, "plugin list", structuredIndented, map[string]any{"ch": make(chan int)})
	if err == nil {
		t.Fatal("expected a marshal error for an unmarshalable payload")
	}
	if !strings.Contains(err.Error(), "aggrega plugin list") {
		t.Errorf("expected the diagnostic to name the command; got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected nothing written on a marshal failure; got %q", buf.String())
	}
}
