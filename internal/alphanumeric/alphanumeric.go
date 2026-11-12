// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package alphanumeric

import "strings"

// IsRune reports whether the alphabet admits one character. The alphabet admits
// ASCII letters and digits and nothing else — no separator, no punctuation, no
// character outside ASCII.
//
// It is the one statement of the alphabet. [CapitalizedWords] reads it rather
// than restating it, so a caller asking whether a character is admitted and a
// caller folding a string into the alphabet cannot come to disagree about what
// the alphabet is.
func IsRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// CapitalizedWords reads s as words separated by runs of anything the alphabet
// does not admit ([IsRune]), capitalises each word's first letter, and joins
// them. Every character of the result lies inside the alphabet.
//
// A word's remaining characters are left as authored, so a name that already
// carries capitals keeps them, and two inputs differing in more than their
// separators stay distinguishable.
//
// The derivation drops every separator, so it is not injective: two inputs
// differing only in where their separators sit reach one result, and by the time
// this returns the authored input is gone. A caller that cannot tolerate two
// inputs arriving at one name is therefore the one that has to detect it, while
// it still holds both inputs — this function cannot, and no arrangement of it
// could.
func CapitalizedWords(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	atWordStart := true
	for _, r := range s {
		if IsRune(r) {
			if atWordStart && r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			atWordStart = false
		} else {
			atWordStart = true
		}
	}
	return b.String()
}
