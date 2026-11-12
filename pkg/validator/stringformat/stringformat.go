// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package stringformat checks a string against the named formats of the JSON
// Schema format vocabulary.
//
// A schema, or an Aggregate parameter contract, names a format with the format
// keyword. The names supported are a closed set, spelled as the JSON Schema
// specification spells them — [Names] lists them — and each checks what that
// specification defines the name to mean, by the standard the specification
// points to. The definition behind each name is stated beside its checker.
//
// Every check judges the string's syntax and nothing else. None reaches the
// network or reads the environment: a hostname is checked for the shape of a host
// name and never resolved, a URI for the shape of a URI and never fetched, a
// mailbox for the shape of an address and never contacted. Given the same name and
// the same string, a check answers the same way on every run.
//
// This package is the one home of the names and of the checks. The Aggregate
// parameter-contract validator and the schema validator every other schema goes
// through both consult it, so a format means the same thing wherever it is
// written, and a name outside the set is refused in the same words on both.
package stringformat

import (
	"fmt"
	"strings"
)

// formatChecker pairs a supported format name with the check it names. check
// returns the empty string when the value conforms and, when it does not, the
// reason in words an author can act on.
type formatChecker struct {
	name  string
	check func(string) string
}

// checkers is the closed set of supported formats, in the order they are listed
// to an author. The order is the one place the list's presentation is decided:
// [Names], [CheckName] and every schema enum held equal to [Names] read it.
var checkers = []formatChecker{
	{name: "email", check: checkEmail},
	{name: "uri", check: checkURI},
	{name: "hostname", check: checkHostname},
	{name: "ipv4", check: checkIPv4},
	{name: "ipv6", check: checkIPv6},
	{name: "uuid", check: checkUUID},
	{name: "date-time", check: checkDateTime},
	{name: "date", check: checkDate},
}

// Names returns the supported format names, in the order they are listed to an
// author. The slice is a fresh copy on every call.
func Names() []string {
	out := make([]string, 0, len(checkers))
	for _, c := range checkers {
		out = append(out, c.name)
	}
	return out
}

// Supported reports whether name is one of [Names].
func Supported(name string) bool {
	_, ok := lookup(name)
	return ok
}

// CheckName returns nil when name is one of [Names], and otherwise the one
// wording of an unsupported name: the name as written, followed by every
// supported name.
func CheckName(name string) error {
	if Supported(name) {
		return nil
	}
	return fmt.Errorf("format %q is not supported; the supported formats are %s",
		name, strings.Join(Names(), ", "))
}

// Check reports whether value conforms to the named format. It returns nil when
// it does and an error naming the value, the format and what is wrong when it does
// not. A name outside [Names] returns the error [CheckName] returns, so a caller
// that has not judged the name already still refuses it in the same words.
func Check(name, value string) error {
	c, ok := lookup(name)
	if !ok {
		return CheckName(name)
	}
	if reason := c.check(value); reason != "" {
		return fmt.Errorf("%q is not a valid %s: %s", value, name, reason)
	}
	return nil
}

func lookup(name string) (formatChecker, bool) {
	for _, c := range checkers {
		if c.name == name {
			return c, true
		}
	}
	return formatChecker{}, false
}

// Character classes shared by the grammars below. Each is ASCII-only, because
// every standard these formats point to is written over ASCII.

func isAlpha(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isHexDigit(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func isLetDig(b byte) bool { return isAlpha(b) || isDigit(b) }

// allDigits reports whether s is non-empty and made of decimal digits only.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// atoiDigits converts a string allDigits has accepted. The strings it is handed
// are at most four digits long, so no overflow is reachable.
func atoiDigits(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}
