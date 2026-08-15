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

// TestWriteStructuredMatchesIndentedMarshal pins the seam's output against
// marshalling indented by hand: the seam adds exactly one trailing newline and
// nothing else. Asserting the equivalence rather than a literal keeps the
// guarantee meaningful as a payload changes — what must hold is that routing a
// surface through this seam does not alter its bytes.
//
// This is deliberately not the only shape assertion. It compares against the same
// encoder the implementation calls, so it moves with that encoder and cannot see a
// change that moves both; TestWriteStructuredEmitsIndentedMarks asserts the marks
// literally and is blind in the opposite direction. The pair is an intentional
// cross-check: collapsing either into the other removes the angle it covers.
func TestWriteStructuredMatchesIndentedMarshal(t *testing.T) {
	t.Parallel()

	var got bytes.Buffer
	if err := writeStructured(&got, "search", seamFixture()); err != nil {
		t.Fatalf("writeStructured: %v", err)
	}

	want, err := json.MarshalIndent(seamFixture(), "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if got.String() != string(want)+"\n" {
		t.Errorf("indented shape drifted:\n got %q\nwant %q", got.String(), string(want)+"\n")
	}
	// Keeps the equivalence above from passing vacuously: if the reference call
	// stopped indenting, both sides would move together and still match.
	if !strings.Contains(got.String(), "\n  \"name\"") {
		t.Errorf("expected two-space indentation; got %q", got.String())
	}
}

// TestWriteStructuredEmitsIndentedMarks pins the shape by its literal marks: a
// space after a key's colon, two-space indentation deepening by two per level,
// more than one line, and exactly one trailing newline. It also asserts the
// unspaced spelling is absent, so a change that re-flowed a surface back onto a
// single line fails here rather than reaching a consumer.
//
// Asserting the marks directly, rather than against an encoder, is what makes this
// independent of TestWriteStructuredMatchesIndentedMarshal: that test compares the
// seam against the same encoder the seam calls, so a change moving both would
// satisfy it. The separation is deliberate — one test covering both angles would
// have to share the encoder, and would lose this one.
func TestWriteStructuredEmitsIndentedMarks(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := writeStructured(&buf, "version", seamFixture()); err != nil {
		t.Fatalf("writeStructured: %v", err)
	}
	got := buf.String()

	for _, tc := range []struct {
		name string
		want string
	}{
		{"a space follows a key's colon", `"name": "helm"`},
		{"a top-level key is indented two spaces", "\n  \"name\": "},
		{"a nested element is indented four spaces", "\n    1,"},
	} {
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: expected %q in the payload; got %q", tc.name, tc.want, got)
		}
	}

	if strings.Contains(got, `"name":"helm"`) {
		t.Errorf("expected no unspaced key/value form; got %q", got)
	}
	if n := strings.Count(got, "\n"); n < 2 {
		t.Errorf("expected the payload to span several lines; got %d newline(s) in %q", n, got)
	}
	if !strings.HasSuffix(got, "}\n") || strings.HasSuffix(got, "}\n\n") {
		t.Errorf("expected the payload to end with a brace and exactly one newline; got %q", got)
	}
}

// TestWriteStructuredWrapsMarshalFailure pins that a marshal failure names the
// surface that produced it rather than surfacing a bare encoding/json error, so
// the diagnostic says which command's output could not be built.
func TestWriteStructuredWrapsMarshalFailure(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := writeStructured(&buf, "plugin list", map[string]any{"ch": make(chan int)})
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
