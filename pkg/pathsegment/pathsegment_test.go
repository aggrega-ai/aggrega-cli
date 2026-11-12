// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pathsegment_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// contentHex is a content digest's hex, the shape every cached digest carries.
const contentHex = "dc0bcb5e0f6c0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293"

// encodedTable is every value the encoder is held to: the shapes a cache was
// named after when a name carrying a colon was refused, every character Windows
// refuses in a name, control characters, every device name Windows reserves in
// two letter cases with and without an extension, a value ending in a full stop
// and in a space, values differing only in letter case, the empty value, the two
// dot names, and a value that is not valid UTF-8.
func encodedTable() []string {
	values := []string{
		"hub.aggrega.ai/plugin/cli/moo:0.2.4",
		"sha256:" + contentHex,
		"core/devtool/adminer:1.0.0",
		"localhost:5000",
		"1.0.0", "1.0.0-rc.1+build.5", "v1.2.3",
		"a<b", "a>b", "a:b", `a"b`, "a/b", `a\b`, "a|b", "a?b", "a*b",
		"a\x00b", "a\x01b", "a\x1fb", "a\x7fb",
		"a.", "a ", "a. ", ".", "..", "...", "",
		"Foo", "foo", "API", "api", "Api", "Sha256:" + contentHex,
		"a\xffb", "é", "é",
	}
	for _, device := range []string{
		"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
		"COM0", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT0", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
		"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³",
	} {
		lower := strings.ToLower(device)
		values = append(values, device, lower, device+".txt", lower+".tar.gz", lower+" .txt")
	}
	return values
}

// TestEncodeYieldsAValidNameForEveryValue holds every encoded value to the one
// validity predicate, alone and with a fixed extension appended, and asserts each
// reads back to the value it was written for.
func TestEncodeYieldsAValidNameForEveryValue(t *testing.T) {
	for _, value := range encodedTable() {
		name := pathsegment.Encode(value)
		if !pathsegment.Valid(name) {
			t.Errorf("Encode(%q) = %q, which a platform refuses", value, name)
		}
		for _, extension := range []string{".json", ".bundle", ".updated"} {
			if !pathsegment.Valid(name + extension) {
				t.Errorf("Encode(%q)+%q = %q, which a platform refuses", value, extension, name+extension)
			}
		}
		got, ok := pathsegment.Decode(name)
		if !ok || got != value {
			t.Errorf("Decode(%q) = (%q, %v), want (%q, true)", name, got, ok, value)
		}
	}
}

// TestEncodeIsInjectiveUnderCaseFolding asserts no two values in the table reach
// names that a file system comparing names without regard to letter case would
// take for one — which is stronger than asserting the names differ, because two
// names differing only in case are one file on such a system.
func TestEncodeIsInjectiveUnderCaseFolding(t *testing.T) {
	values := append(encodedTable(), namesegmenttest.Corpus('+')...)
	seen := map[string]string{}
	for _, value := range values {
		folded := strings.ToLower(pathsegment.Encode(value))
		if prior, clash := seen[folded]; clash && prior != value {
			t.Fatalf("%q and %q reach names equal under case folding: %q", prior, value, folded)
		}
		seen[folded] = value
	}
	if len(seen) < len(encodedTable()) {
		t.Fatalf("%d distinct folded names from a table of %d values", len(seen), len(encodedTable()))
	}
}

// TestEncodeSpellsTheShapesThatBrokeReadably pins the spelling of the values a
// cache was named after when a colon reached a directory name, and of values
// every platform accepts as written, so the readable part of a name is held as
// well as its validity.
func TestEncodeSpellsTheShapesThatBrokeReadably(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"hub.aggrega.ai/plugin/cli/moo:0.2.4", "hub.aggrega.ai+zsplugin+zscli+zsmoo+zx00003a0.2.4"},
		{"sha256:" + contentHex, "sha256+zx00003a" + contentHex},
		{"core/devtool/adminer:1.0.0", "core+zsdevtool+zsadminer+zx00003a1.0.0"},
		{"localhost:5000", "localhost+zx00003a5000"},
		{"1.0.0", "1.0.0"},
		{"api-server_v2", "api-server_v2"},
		{"1.0.0-rc.1+build.5", "1.0.0-rc.1+build.5"},
		{"Foo", "+zx000046oo"},
		{"con", "+zx000063on"},
		{"nul.txt", "+zx00006eul.txt"},
		{"a.", "a+zd"},
		{"", "+ze"},
	} {
		if got := pathsegment.Encode(tc.value); got != tc.want {
			t.Errorf("Encode(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestValidRefusesEveryNameAPlatformRefuses is the predicate's own table: each
// refused row carries one fault, and the accepted rows are the calibration that
// keeps a predicate refusing everything from passing.
func TestValidRefusesEveryNameAPlatformRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"api-server", true},
		{"hub.aggrega.ai+zsplugin+zscli+zsmoo+zx00003a0.2.4", true},
		{"README.md", true},
		{"a b", true},
		{".hidden", true},
		{"console", true},
		{"com10", true},
		{"lpt", true},
		{"nulls.txt", true},
		{"", false},
		{".", false},
		{"..", false},
		{"moo:0.2.4", false},
		{"sha256:" + contentHex, false},
		{"a<b", false},
		{"a>b", false},
		{`a"b`, false},
		{"a/b", false},
		{`a\b`, false},
		{"a|b", false},
		{"a?b", false},
		{"a*b", false},
		{"a\x00b", false},
		{"a\x1fb", false},
		{"a.", false},
		{"a ", false},
		{"CON", false},
		{"con", false},
		{"Nul.txt", false},
		{"aux.tar.gz", false},
		{"com1", false},
		{"LPT9", false},
		{"COM¹", false},
		{"conin$", false},
		{"prn .txt", false},
		{"a\xffb", false},
	} {
		if got := pathsegment.Valid(tc.name); got != tc.valid {
			t.Errorf("Valid(%q) = %v, want %v", tc.name, got, tc.valid)
		}
	}
}

// TestDecodeRefusesANameEncodeDidNotWrite asserts a reader of a directory listing
// can tell a name this program wrote from one written some other way: a
// percent-escaped reference, an uppercase letter, a reserved name and a final
// full stop as written, and an escape the spelling does not define are each
// refused, beside a name Encode wrote, which is accepted.
func TestDecodeRefusesANameEncodeDidNotWrite(t *testing.T) {
	for _, name := range []string{
		"hub.aggrega.ai%2Fplugin%2Fcli%2Fmoo:0.2.4",
		"Foo",
		"con",
		"a.",
		"a+zq",
	} {
		if value, ok := pathsegment.Decode(name); ok {
			t.Errorf("Decode(%q) = %q, reported as a name Encode wrote", name, value)
		}
	}
	if value, ok := pathsegment.Decode("core+zsdevtool+zsadminer+zx00003a1.0.0"); !ok || value != "core/devtool/adminer:1.0.0" {
		t.Errorf("Decode of an encoded reference = (%q, %v), want (%q, true)", value, ok, "core/devtool/adminer:1.0.0")
	}
}
