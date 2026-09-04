// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
)

// resourceFragment is one fragment declaring a single resource under the given
// logical id, written as the authored YAML rather than built as a value tree so
// each row below exercises the same decode the product path takes.
func resourceFragment(id string) []byte {
	return []byte("Resources:\n  " + id + ":\n    Type: AWS::SQS::Queue\n")
}

// TestAssembleStackRefusesAResourceLogicalIDOutsideTheAlphabet asserts the
// assembly halts on an authored resource logical id carrying a character a
// CloudFormation logical id does not admit, and that the halt names what the
// author has to look at: the component, the id, and the character.
//
// One fault per row, and each row asserts the halt names its OWN character. A
// row satisfied by any error would keep passing once some earlier gate rejected
// a different fault, which is how a negative fixture silently stops covering its
// subject.
func TestAssembleStackRefusesAResourceLogicalIDOutsideTheAlphabet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		id       string
		wantChar string
	}{
		{name: "hyphen", id: "payments-db", wantChar: `"-"`},
		{name: "underscore", id: "payments_db", wantChar: `"_"`},
		{name: "dot", id: "shop.payments", wantChar: `"."`},
		{name: "colon", id: "shop:payments", wantChar: `":"`},
		{name: "space", id: `"payments db"`, wantChar: `" "`},
		{name: "outside ascii", id: "payments" + "\u00e9", wantChar: `"é"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AssembleStack([]NodeFragment{{
				NodeID: "payments",
				Files:  [][]byte{resourceFragment(tc.id)},
			}})
			if err == nil {
				t.Fatal("expected a loud halt for a resource logical id outside the alphabet; got nil error — the id would have reached the emitted template unchanged")
			}
			msg := err.Error()
			t.Logf("halt: %s", msg)
			for _, want := range []string{`"payments"`, tc.wantChar, "letters and digits"} {
				if !strings.Contains(msg, want) {
					t.Errorf("halt does not name %s: %s", want, msg)
				}
			}
		})
	}
}

// TestAssembleStackRefusesAResourceNamedByTheEmptyString asserts the case with
// no character to name at all. An empty logical id addresses nothing, so the
// alphabet clause has nothing to report and the refusal says what is wrong
// instead of naming a character that is not there.
func TestAssembleStackRefusesAResourceNamedByTheEmptyString(t *testing.T) {
	_, err := AssembleStack([]NodeFragment{{
		NodeID: "payments",
		Files:  [][]byte{[]byte("Resources:\n  \"\":\n    Type: AWS::SQS::Queue\n")},
	}})
	if err == nil {
		t.Fatal("expected a loud halt for a resource named by the empty string; got nil error")
	}
	msg := err.Error()
	t.Logf("halt: %s", msg)
	for _, want := range []string{`"payments"`, "empty string"} {
		if !strings.Contains(msg, want) {
			t.Errorf("halt does not name %q: %s", want, msg)
		}
	}
}

// TestDeclaredNamesRefusesAResourceLogicalIDOutsideTheAlphabet asserts the
// refusal reaches the declaration a node hands the host, not only the merge.
//
// This is the coverage the placement buys and the reason it does not sit at the
// write into the assembled map: a node's names are declared while its fragments
// are read, which is earlier than any assembly and is the only place the sourced
// path is reached at all. A refusal at the merge would hand the host an id no
// template can hold and say nothing.
func TestDeclaredNamesRefusesAResourceLogicalIDOutsideTheAlphabet(t *testing.T) {
	resourceIDs, outputIDs, err := DeclaredNames("payments", [][]byte{resourceFragment("payments-db")})
	if err == nil {
		t.Fatalf("expected a loud halt while the node's names were declared; got resources %v and outputs %v", resourceIDs, outputIDs)
	}
	t.Logf("halt: %s", err)
	if resourceIDs != nil || outputIDs != nil {
		t.Errorf("a refused declaration must carry no names; got resources %v and outputs %v", resourceIDs, outputIDs)
	}
	if !strings.Contains(err.Error(), `"-"`) {
		t.Errorf("halt does not name the character: %s", err)
	}
}

// TestDeclaredNamesSourcedRefusesAResourceLogicalIDOutsideTheAlphabet asserts
// the sourced path is covered too. A sourced template's resources are read
// through the same derivation a fragment's are, so nothing about the input's
// origin changes the verdict — which is what one derivation buys and what two
// walks kept in step by eye would not.
func TestDeclaredNamesSourcedRefusesAResourceLogicalIDOutsideTheAlphabet(t *testing.T) {
	files := []SourcedFile{{Path: "template.yaml", Content: resourceFragment("payments_db")}}
	resourceIDs, outputIDs, err := DeclaredNamesSourced("payments", files, true)
	if err == nil {
		t.Fatalf("expected a loud halt while the sourced source's names were declared; got resources %v and outputs %v", resourceIDs, outputIDs)
	}
	t.Logf("halt: %s", err)
	for _, want := range []string{"template.yaml", `"_"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt does not name %q: %s", want, err)
		}
	}
}

// TestResourceLogicalIDRefusalNamesEveryIDAndEachCharacterOnce asserts one build
// tells the author the whole picture: every id of the fragment that carries such
// a character is named, and each character of an id is named once however many
// times it was written.
func TestResourceLogicalIDRefusalNamesEveryIDAndEachCharacterOnce(t *testing.T) {
	fragment := []byte("Resources:\n" +
		"  a-b-c:\n    Type: AWS::SQS::Queue\n" +
		"  d_e:\n    Type: AWS::S3::Bucket\n" +
		"  Legal1:\n    Type: AWS::SNS::Topic\n")
	_, err := AssembleStack([]NodeFragment{{NodeID: "payments", Files: [][]byte{fragment}}})
	if err == nil {
		t.Fatal("expected a loud halt; got nil error")
	}
	msg := err.Error()
	t.Logf("halt: %s", msg)
	for _, want := range []string{`"a-b-c"`, `"d_e"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("halt does not name %s: %s", want, msg)
		}
	}
	// The legal id is not reported: the refusal names what the author has to
	// change, and naming a name that is already fine would send them to edit it.
	if strings.Contains(msg, `"Legal1"`) {
		t.Errorf("halt names the legal id, which the author has no reason to edit: %s", msg)
	}
	// `a-b-c` carries one hyphen twice. Two mentions would read as two different
	// things to fix.
	if got := strings.Count(msg, `"-"`); got != 1 {
		t.Errorf("the hyphen is named %d times, want once: %s", got, msg)
	}
}

// TestResourceLogicalIDRefusalIsSilentForALegalID asserts the refusal does not
// over-fire. Every id here lies inside the alphabet, so the assembly proceeds and
// the emitted template carries the id exactly as authored.
func TestResourceLogicalIDRefusalIsSilentForALegalID(t *testing.T) {
	for _, id := range []string{
		"PaymentsDb",
		"paymentsdb",
		"PAYMENTSDB",
		"Db2Primary",
		"9",
		"A",
	} {
		t.Run(id, func(t *testing.T) {
			out, err := AssembleStack([]NodeFragment{{
				NodeID: "payments",
				Files:  [][]byte{resourceFragment(id)},
			}})
			if err != nil {
				t.Fatalf("a resource logical id of letters and digits must assemble; got %v", err)
			}
			if !strings.Contains(string(out), `"`+id+`"`) {
				t.Errorf("the assembled stack does not carry the authored id %q verbatim:\n%s", id, out)
			}
		})
	}
}

// TestResourceLogicalIDRefusalReturnsAnErrorRatherThanPanicking asserts the
// standing property for invalid input: it comes back as an error up the chain,
// never as a panic. The Go test runner does not fail a recovered panic on its
// own, and this core runs inside a WebAssembly guest where a panic reaches the
// host as a trap rather than as anything an author can read — so the assertion
// is made here rather than left to whatever the caller happens to observe.
func TestResourceLogicalIDRefusalReturnsAnErrorRatherThanPanicking(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("reading a resource logical id outside the alphabet panicked: %v", r)
		}
	}()
	for _, id := range []string{"a-b", "", "\u00e9", "--", "_"} {
		if err := resourceLogicalIDRefusal("payments", []string{id}); err == nil {
			t.Errorf("id %q lies outside the alphabet and was not refused", id)
		}
	}
	if err := resourceLogicalIDRefusal("payments", nil); err != nil {
		t.Errorf("a contributor that authored no resource has nothing to refuse; got %v", err)
	}
}

// TestTheLogicalIDAlphabetHasOneHome asserts every side of the logical-id
// alphabet reads one statement of it, character by character: each refusal of a
// verbatim-read name admits exactly that alphabet, and the Output-name
// derivation carries exactly that alphabet through less the single character it
// reserves to open an escape with.
//
// That subtraction is the only asymmetry, and it is stated here rather than
// looked up, so a derivation that started reserving a second character — or
// stopped reserving this one — reddens rather than passing quietly.
//
// All of them read one predicate today, so this is the guard that keeps them
// reading it: a later edit re-stating the character class inside any one of them
// compiles, passes every other test, and leaves two copies that agree until one
// of them moves. Nothing else in the package would fail on that, because there is
// no comparison between them for anything to fail on — which is exactly what this
// supplies. Every side is asserted in one loop over one oracle, so widening the
// alphabet reddens all of them at once rather than one at a time.
//
// The carried-section side is asserted once per section held to the alphabet,
// read from the declared list, so a section added there is covered without anyone
// remembering to list it here.
//
// The oracle is deliberately written out here rather than taken from the
// predicate under test: a cross-check that shares the implementation it verifies
// cannot see it move.
func TestTheLogicalIDAlphabetHasOneHome(t *testing.T) {
	oracle := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	for r := rune(0); r < 0x250; r++ {
		want := oracle(r)
		if got := alphanumeric.IsRune(r); got != want {
			t.Fatalf("the alphabet admits %q = %v, want %v", r, got, want)
		}
		// The Output-name derivation's side of the same rule. It carries a
		// character through exactly when the alphabet admits it, with one
		// character subtracted: the one it reserves to open an escape with, which
		// the alphabet admits and the derivation may not carry. Stating the
		// subtraction here rather than reading the derivation's own predicate is
		// what makes this a cross-check — a leg that read the predicate under test
		// would agree with it whatever it said.
		carried := isOutputLogicalIDRune(r)
		if carried != (want && r != outputLogicalIDOpener) {
			t.Fatalf("the Output-name derivation %s %q, but the alphabet says %v and the reserved character is %q",
				map[bool]string{true: "carries", false: "escapes"}[carried], r, want, outputLogicalIDOpener)
		}
		// The resource refusal's side: it objects to exactly the characters the
		// alphabet does not admit.
		refused := resourceLogicalIDRefusal("payments", []string{"A" + string(r)}) != nil
		if refused == want {
			t.Fatalf("the resource refusal %s %q, but the alphabet says %v",
				map[bool]string{true: "refuses", false: "admits"}[refused], r, want)
		}
		// And the carried sections' side, per section held to the alphabet.
		for _, section := range alphabetCheckedSections {
			refusedInSection := carriedSectionNameRefusal("payments", section, []string{"A" + string(r)}) != nil
			if refusedInSection == want {
				t.Fatalf("the %s name refusal %s %q, but the alphabet says %v", section,
					map[bool]string{true: "refuses", false: "admits"}[refusedInSection], r, want)
			}
		}
	}
}
