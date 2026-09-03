// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
)

// carriedSectionFragment is one fragment declaring a single entry of the given
// section under the given name, alongside one legal resource so the assembly has
// something to carry.
//
// The entry's body is deliberately shape-agnostic. This core reads a carried
// section as a map and never asks what an entry's body means, so a body written
// per section would add a per-section detail the tests below would then have to
// keep in step with the declared lists — and a table missing a row for a newly
// declared section would skip it in silence, which is the failure the lists exist
// to prevent.
func carriedSectionFragment(section, name string) []byte {
	return []byte("Resources:\n  StoreQueue:\n    Type: AWS::SQS::Queue\n" +
		section + ":\n  " + name + ":\n    Detail: value\n")
}

// TestEveryCarriedSectionHasANameDisposition holds the two name dispositions to
// the carried sections: every carried section is either held to the logical-id
// alphabet or deliberately not, no section is in both, and neither list names a
// section that is not carried.
//
// It is the guard that makes the partition worth declaring. A section added to
// [carriedSections] without a disposition fails here rather than landing
// unchecked in silence — which is exactly how the three sections this refusal
// covers came to be unchecked in the first place.
//
// What it cannot check is whether a disposition is the RIGHT one: the template
// language is not ours, so whether a section's names lie in the logical-id
// alphabet is settled by that language's own documentation and not by anything
// here. The two lists say so in their own words, per section.
func TestEveryCarriedSectionHasANameDisposition(t *testing.T) {
	t.Parallel()
	disposition := map[string][]string{}
	for _, group := range []struct {
		name     string
		sections []string
	}{
		{"alphabet-checked", alphabetCheckedSections},
		{"alphabet-unchecked", alphabetUncheckedSections},
	} {
		for _, section := range group.sections {
			disposition[section] = append(disposition[section], group.name)
		}
	}

	for _, section := range carriedSections {
		switch got := disposition[section]; len(got) {
		case 0:
			t.Errorf("carried section %q has no name disposition: an authored name in it would reach the emitted template unchecked, and nothing would say so", section)
		case 1:
			// exactly one, which is the contract
		default:
			sort.Strings(got)
			t.Errorf("carried section %q has %d name dispositions (%v); it must have exactly one", section, len(got), got)
		}
		delete(disposition, section)
	}
	for section, groups := range disposition {
		t.Errorf("section %q is given the name disposition(s) %v but is not a carried section", section, groups)
	}
}

// TestAssembleStackRefusesACarriedNameOutsideTheAlphabet is the invocation half
// of the partition, and it is driven from [alphabetCheckedSections] rather than
// from a hand-written list of sections.
//
// That is the point rather than a convenience: a set-only assertion passes on a
// section declared checked and never wired, which is a criterion satisfied while
// nothing can run the thing. Every member here has to actually HALT, through the
// real derivation, on a name carrying a character the alphabet does not admit.
//
// Each row asserts the halt names its own section and its own character, so a row
// satisfied by any error would keep passing once some earlier gate rejected a
// different fault.
func TestAssembleStackRefusesACarriedNameOutsideTheAlphabet(t *testing.T) {
	t.Parallel()
	if len(alphabetCheckedSections) == 0 {
		t.Fatal("no carried section is held to the logical-id alphabet; the refusal is declared over nothing")
	}
	for _, section := range alphabetCheckedSections {
		t.Run(section, func(t *testing.T) {
			t.Parallel()
			_, err := AssembleStack([]NodeFragment{{
				NodeID: "payments",
				Files:  [][]byte{carriedSectionFragment(section, "payments-db")},
			}})
			if err == nil {
				t.Fatalf("expected a loud halt for a %s name outside the alphabet; got nil error — the name would have reached the emitted template unchanged", section)
			}
			msg := err.Error()
			t.Logf("halt: %s", msg)
			for _, want := range []string{
				// The component, so the author knows whose Recipe wrote it.
				`"payments"`,
				// The section, which is the word the author searches their own files for.
				section,
				// The name itself, and the character that cannot be in it.
				`"payments-db"`,
				`"-"`,
				// What may be written instead.
				"letters and digits",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the halt does not name %s: %s", want, msg)
				}
			}
		})
	}
}

// TestAssembleStackCarriesAnUncheckedSectionNameOutsideTheAlphabet is the other
// half of the partition, and it is the assertion that keeps an exclusion honest.
//
// A declared exclusion that the code does not honour is invisible: the section
// would be listed as deliberately unchecked while the refusal quietly rejected
// its names, and the only symptom would be a build refusing input the target
// accepts. So every member of [alphabetUncheckedSections] must assemble, and the
// authored name must reach the emitted bytes verbatim — read from the emitted
// document rather than from the build's exit, because a check that halted nothing
// but rewrote the name would pass an exit-only assertion.
func TestAssembleStackCarriesAnUncheckedSectionNameOutsideTheAlphabet(t *testing.T) {
	t.Parallel()
	if len(alphabetUncheckedSections) == 0 {
		t.Fatal("no carried section is declared unchecked; this assertion covers nothing")
	}
	const authored = "store.documentation-v1"
	for _, section := range alphabetUncheckedSections {
		t.Run(section, func(t *testing.T) {
			t.Parallel()
			out, err := AssembleStack([]NodeFragment{{
				NodeID: "payments",
				Files:  [][]byte{carriedSectionFragment(section, `"`+authored+`"`)},
			}})
			if err != nil {
				t.Fatalf("the %s section is declared as one whose names are not held to the logical-id alphabet, so %q must assemble; got %v", section, authored, err)
			}
			var stack map[string]any
			if err := json.Unmarshal(out, &stack); err != nil {
				t.Fatalf("assembled stack is not valid JSON: %v", err)
			}
			carried, ok := stack[section].(map[string]any)
			if !ok {
				t.Fatalf("the assembled stack carries no %s object; got %#v\n--- stack ---\n%s", section, stack[section], out)
			}
			if _, present := carried[authored]; !present {
				t.Errorf("the carried %s section does not declare %q verbatim; got keys %v\n--- stack ---\n%s", section, authored, sortedKeys(carried), out)
			}
		})
	}
}

// TestDeclaredNamesRefusesACarriedNameOutsideTheAlphabet asserts the refusal
// reaches the declaration a node hands the host, not only the merge.
//
// This is the coverage the placement buys, and it is worth an assertion of its
// own because the declaration deliberately DISCARDS a carried section's entries:
// what reaches the host is the resource and output names alone. So a reader could
// reasonably conclude this path never sees a carried section. It does — the
// refusal raises from inside the one derivation both paths read, before any claim
// is returned for a caller to keep or discard.
func TestDeclaredNamesRefusesACarriedNameOutsideTheAlphabet(t *testing.T) {
	t.Parallel()
	for _, section := range alphabetCheckedSections {
		t.Run(section, func(t *testing.T) {
			t.Parallel()
			resourceIDs, outputIDs, err := DeclaredNames("payments", [][]byte{carriedSectionFragment(section, "payments-db")})
			if err == nil {
				t.Fatalf("expected a loud halt while the node's names were declared; got resources %v and outputs %v", resourceIDs, outputIDs)
			}
			t.Logf("halt: %s", err)
			if resourceIDs != nil || outputIDs != nil {
				t.Errorf("a refused declaration must carry no names; got resources %v and outputs %v", resourceIDs, outputIDs)
			}
			for _, want := range []string{section, `"-"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not name %q: %s", want, err)
				}
			}
		})
	}
}

// TestDeclaredNamesSourcedRefusesACarriedNameOutsideTheAlphabet asserts the
// sourced path is covered too. A sourced template's sections are read through the
// same derivation a fragment's are, so nothing about the input's origin changes
// the verdict — which is what one derivation buys and what two walks kept in step
// by eye would not.
func TestDeclaredNamesSourcedRefusesACarriedNameOutsideTheAlphabet(t *testing.T) {
	t.Parallel()
	for _, section := range alphabetCheckedSections {
		t.Run(section, func(t *testing.T) {
			t.Parallel()
			files := []SourcedFile{{Path: "template.yaml", Content: carriedSectionFragment(section, "payments_db")}}
			resourceIDs, outputIDs, err := DeclaredNamesSourced("payments", files, true)
			if err == nil {
				t.Fatalf("expected a loud halt while the sourced source's names were declared; got resources %v and outputs %v", resourceIDs, outputIDs)
			}
			t.Logf("halt: %s", err)
			for _, want := range []string{"template.yaml", section, `"_"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not name %q: %s", want, err)
				}
			}
		})
	}
}

// TestCarriedSectionNameRefusalNamesEveryNameAndEachCharacterOnce asserts one
// build tells the author the whole picture: every name of the section that
// carries such a character is named, each character of a name once however many
// times it was written, and a legal name in the same section is not named at all
// — naming one would send the author to edit a name that is already fine.
func TestCarriedSectionNameRefusalNamesEveryNameAndEachCharacterOnce(t *testing.T) {
	t.Parallel()
	fragment := []byte("Resources:\n  StoreQueue:\n    Type: AWS::SQS::Queue\n" +
		sectionParameters + ":\n" +
		"  a-b-c:\n    Type: String\n" +
		"  d_e:\n    Type: String\n" +
		"  Legal1:\n    Type: String\n")
	_, err := AssembleStack([]NodeFragment{{NodeID: "payments", Files: [][]byte{fragment}}})
	if err == nil {
		t.Fatal("expected a loud halt; got nil error")
	}
	msg := err.Error()
	t.Logf("halt: %s", msg)
	for _, want := range []string{`"a-b-c"`, `"d_e"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not name %s: %s", want, msg)
		}
	}
	if strings.Contains(msg, `"Legal1"`) {
		t.Errorf("the halt names the legal name, which the author has no reason to edit: %s", msg)
	}
	// `a-b-c` carries one hyphen twice. Two mentions would read as two different
	// things to fix.
	if got := strings.Count(msg, `"-"`); got != 1 {
		t.Errorf("the hyphen is named %d times, want once: %s", got, msg)
	}
}

// TestCarriedSectionNameRefusalReadsOnlyTheSectionsOwnKeys is the over-fire
// assertion that matters most, and it pins a bound rather than an absence.
//
// A name in one of these sections is the logical id; the keys NESTED inside an
// entry's body are not. A mapping's second-level keys are map keys an author
// chooses freely — a region name such as us-east-1 is the ordinary case — and a
// parameter's or a condition's body is keyed by attribute and intrinsic names.
// Descending into a body would refuse all of that, which is legal input, so the
// refusal reads one level and this is what holds it there.
func TestCarriedSectionNameRefusalReadsOnlyTheSectionsOwnKeys(t *testing.T) {
	t.Parallel()
	fragment := []byte(`
Resources:
  StoreQueue:
    Type: AWS::SQS::Queue
Mappings:
  RegionCidr:
    us-east-1:
      Primary: 10.0.0.0/16
    eu-west-2:
      Primary: 10.1.0.0/16
Parameters:
  DbInstanceClass:
    Type: String
    Default: db.t3.micro
Conditions:
  IsPrimaryRegion:
    Fn::Equals: [{Ref: "AWS::Region"}, us-east-1]
`)
	out, err := AssembleStack([]NodeFragment{{NodeID: "network", Files: [][]byte{fragment}}})
	if err != nil {
		t.Fatalf("a hyphenated key inside a mapping's body, and a dotted default value, are ordinary input the target accepts; got %v", err)
	}
	var stack map[string]any
	if err := json.Unmarshal(out, &stack); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}
	mappings, ok := stack[sectionMappings].(map[string]any)
	if !ok {
		t.Fatalf("Mappings is not an object; got %#v", stack[sectionMappings])
	}
	region, ok := mappings["RegionCidr"].(map[string]any)
	if !ok {
		t.Fatalf("RegionCidr is not an object; got %#v", mappings["RegionCidr"])
	}
	for _, want := range []string{"us-east-1", "eu-west-2"} {
		if _, present := region[want]; !present {
			t.Errorf("the carried mapping lost its second-level key %q; got %v\n--- stack ---\n%s", want, sortedKeys(region), out)
		}
	}
}

// TestCarriedSectionNameRefusalIsSilentForALegalName asserts the refusal does not
// over-fire on the section's own keys either: every name here lies inside the
// alphabet, so the assembly proceeds and the emitted section carries the name
// exactly as authored.
func TestCarriedSectionNameRefusalIsSilentForALegalName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"DbInstanceClass",
		"dbinstanceclass",
		"DBINSTANCECLASS",
		"Db2Class",
		"9",
		"A",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, section := range alphabetCheckedSections {
				out, err := AssembleStack([]NodeFragment{{
					NodeID: "payments",
					Files:  [][]byte{carriedSectionFragment(section, name)},
				}})
				if err != nil {
					t.Fatalf("a %s name of letters and digits must assemble; got %v", section, err)
				}
				if !strings.Contains(string(out), `"`+name+`"`) {
					t.Errorf("the assembled stack does not carry the authored %s name %q verbatim:\n%s", section, name, out)
				}
			}
		})
	}
}

// TestCarriedSectionNameRefusalReturnsAnErrorRatherThanPanicking asserts the
// standing property for invalid input: it comes back as an error up the chain,
// never as a panic. The Go test runner does not fail a recovered panic on its
// own, and this core runs inside a WebAssembly guest where a panic reaches the
// host as a trap rather than as anything an author can read.
func TestCarriedSectionNameRefusalReturnsAnErrorRatherThanPanicking(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("reading a carried section name outside the alphabet panicked: %v", r)
		}
	}()
	for _, name := range []string{"a-b", "", "\u00e9", "--", "_", "a.b"} {
		if err := carriedSectionNameRefusal("payments", sectionParameters, []string{name}); err == nil {
			t.Errorf("name %q lies outside the alphabet and was not refused", name)
		}
	}
	if err := carriedSectionNameRefusal("payments", sectionParameters, nil); err != nil {
		t.Errorf("a contributor that authored no entry in the section has nothing to refuse; got %v", err)
	}
}

// TestCarriedSectionNameRefusalNamesTheEmptyNameWithoutNamingACharacter asserts
// the case with no character to report. An empty name addresses nothing, so the
// alphabet clause has nothing to name and the refusal says what is wrong instead.
func TestCarriedSectionNameRefusalNamesTheEmptyNameWithoutNamingACharacter(t *testing.T) {
	t.Parallel()
	err := carriedSectionNameRefusal("payments", sectionParameters, []string{""})
	if err == nil {
		t.Fatal("expected a loud refusal for an entry named by the empty string; got nil error")
	}
	msg := err.Error()
	t.Logf("refusal: %s", msg)
	for _, want := range []string{`"payments"`, sectionParameters, "empty string"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q: %s", want, msg)
		}
	}
}

// TestTheTwoVerbatimRefusalsShareOneClauseDerivation asserts what the two
// refusals hold in common is one derivation and not two copies of it.
//
// The clause list is the shared part — the empty-name case, the
// distinct-characters case, once per character, in first-appearance order — and
// the only thing that varies is the noun a clause calls the thing a name
// addresses. Comparing the two refusals' clauses on one input is what would fail
// if either grew its own copy: a second copy would agree on the day it was
// written and drift afterwards, and nothing else in this package compares them.
//
// It is deliberately NOT a comparison of the whole message. The remedies differ
// on purpose — the resource remedy offers a Recipe the option of authoring a
// logical id instead of passing a name through, which would be wrong about a
// parameter — so requiring the messages to match would require the wrong thing.
func TestTheTwoVerbatimRefusalsShareOneClauseDerivation(t *testing.T) {
	t.Parallel()
	names := []string{"a-b-c", "d_e", "Legal1", "", "sh op.x"}
	resource := alphabetClauses("resource", names)
	entry := alphabetClauses("entry", names)
	if len(resource) != len(entry) {
		t.Fatalf("the two refusals report %d and %d clauses for one input; the clause derivation is not shared\nresource: %v\nentry:    %v",
			len(resource), len(entry), resource, entry)
	}
	for i := range resource {
		want := strings.Replace(resource[i], "one resource is named", "one entry is named", 1)
		if entry[i] != want {
			t.Errorf("clause %d differs beyond the subject noun:\nresource: %s\nentry:    %s", i, resource[i], entry[i])
		}
	}
	// And the derivation is actually reporting something, so the comparison above
	// is not two empty lists agreeing.
	if len(resource) != 4 {
		t.Errorf("the clause derivation reported %d clauses for %d names, want 4 — one legal name is not reported: %v", len(resource), len(names), resource)
	}
}

// TestEveryAlphabetCheckedSectionIsCarried is the containment leg the partition
// test cannot express on its own: a section held to the alphabet has to be one
// the assembly actually carries, because a check over a section nothing reads is
// a check that never runs.
func TestEveryAlphabetCheckedSectionIsCarried(t *testing.T) {
	t.Parallel()
	for _, section := range alphabetCheckedSections {
		if !slices.Contains(carriedSections, section) {
			t.Errorf("section %q is held to the logical-id alphabet but is not carried, so nothing reads its names and the check never runs", section)
		}
	}
}
