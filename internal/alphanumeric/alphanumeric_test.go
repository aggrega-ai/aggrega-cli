// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package alphanumeric_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
)

// TestIsRuneAdmitsExactlyLettersAndDigits walks the alphabet against an oracle
// written out here rather than taken from the predicate under test: a
// cross-check sharing the implementation it verifies cannot see that
// implementation move.
//
// The range covers ASCII plus the Latin-1 and Latin Extended-A blocks, so the
// accented letters an author is most likely to reach for are asserted to be
// outside the alphabet rather than merely assumed to be.
func TestIsRuneAdmitsExactlyLettersAndDigits(t *testing.T) {
	oracle := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	for r := rune(0); r < 0x250; r++ {
		if got, want := alphanumeric.IsRune(r), oracle(r); got != want {
			t.Fatalf("IsRune(%q) = %v, want %v", r, got, want)
		}
	}
}

// TestCapitalizedWords pins the derivation's specification case by case. Each
// row names the property it holds, so a failure says which rule broke rather
// than only that some string differs.
func TestCapitalizedWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"a separator is a word boundary and the next letter is capitalised", "payments-db", "PaymentsDb"},
		{"every excluded character is a boundary, not only the hyphen", "payments_db", "PaymentsDb"},
		{"a dot is a boundary too", "payments.db", "PaymentsDb"},
		{"a run of separators is one boundary, not several", "a--b", "AB"},
		{"mixed separator runs collapse the same way", "payments-db.primary_2--EU/west", "PaymentsDbPrimary2EUWest"},
		{"a leading separator introduces no empty word", "-app-", "App"},
		{"a trailing separator adds nothing", "app-", "App"},
		{"a name of separators alone folds to the empty string", "---", ""},
		{"the empty string folds to itself", "", ""},

		{"a word's remaining characters are left as authored", "MY-app", "MYApp"},
		{"existing capitals inside a word are kept", "mYaPP", "MYaPP"},
		{"an already-capitalised word is unchanged", "My-App", "MyApp"},

		{"a leading digit is carried and the letter after it is NOT capitalised", "1st-app", "1stApp"},
		{"the same holds across any separator", "1st_app", "1stApp"},
		{"a digit-only word is carried as authored", "db-2", "Db2"},
		{"a word starting with a digit keeps its following letters as authored", "2-db", "2Db"},
		{"digits inside words are carried", "a1-b2", "A1B2"},

		{"whitespace is outside the alphabet, so it is a boundary", "my id", "MyId"},
		{"a tab is a boundary", "my\tid", "MyId"},
		{"a newline is a boundary", "my\nid", "MyId"},
		{"runs of whitespace collapse like any other separator run", "  my   id  ", "MyId"},

		{"a non-ASCII letter is outside the alphabet and is dropped as a boundary", "café-app", "CafApp"},
		{"a non-ASCII letter inside a word splits that word", "naïve", "NaVe"},
		{"a wholly non-ASCII word contributes nothing", "日本-app", "App"},

		{"punctuation the target refuses is a boundary", "my:id", "MyId"},
		{"so is a slash", "my/id", "MyId"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alphanumeric.CapitalizedWords(tc.in); got != tc.want {
				t.Errorf("CapitalizedWords(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCapitalizedWordsEmitsOnlyAlphabetCharacters is the property the callers
// depend on, asserted over every case above plus the alphabet walk: whatever
// goes in, what comes out carries characters the alphabet admits and nothing
// else. A caller emits the result into a template that refuses anything else,
// so this is the guarantee that makes the fold safe to emit unchecked.
func TestCapitalizedWordsEmitsOnlyAlphabetCharacters(t *testing.T) {
	for r := rune(0); r < 0x250; r++ {
		for _, in := range []string{string(r), "a" + string(r), string(r) + "b", "a" + string(r) + "b"} {
			for _, out := range alphanumeric.CapitalizedWords(in) {
				if !alphanumeric.IsRune(out) {
					t.Fatalf("CapitalizedWords(%q) emitted %q, which the alphabet does not admit", in, out)
				}
			}
		}
	}
}

// TestCapitalizedWordsIsNotInjective records the derivation's known limit as an
// executable statement rather than as a comment, because a caller reading the
// godoc has to decide whether it needs its own collision detection and the
// answer is always yes.
//
// 🔴 This asserts a COLLISION, so it fails if the fold is ever made injective.
// That is the intent: an injective replacement is a deliberate change of
// contract, and it must not slip in behind a green suite while callers still
// carry collision detection written for a lossy fold.
func TestCapitalizedWordsIsNotInjective(t *testing.T) {
	a, b := "my-id", "my_id"
	if a == b {
		t.Fatal("the two inputs must differ for this to say anything")
	}
	if got, want := alphanumeric.CapitalizedWords(a), alphanumeric.CapitalizedWords(b); got != want {
		t.Fatalf("CapitalizedWords(%q) = %q and CapitalizedWords(%q) = %q no longer collide; "+
			"the fold has become injective, so every caller's collision detection needs revisiting", a, got, b, want)
	}
}
