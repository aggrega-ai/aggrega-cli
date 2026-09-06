// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package haltref_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"

	// The registry is populated by the packages that declare conditions, so a
	// test that resolves one has to link at least one of them. This package
	// declares two and publishes both through accessors, which is what lets the
	// cases below read a real code off a real condition instead of quoting a
	// number they would then have to keep in step.
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// fixturePage is a page in the shape the generator writes: curated prose, a
// generated region pairing short codes with entry headings, and the entries the
// region names.
//
// The entries deliberately sit at two levels, with one written as a subsection of
// another, because containment is the property section extraction has to get
// right: a subsection is an entry in its own right and a parent's prose contains
// it.
const fixturePage = `# Fatal Halts

## When loading your files

### A value is not the shape the schema declares

The document declared a value the schema does not admit.

#### A value is not the shape the schema declares, in a nested key

The same fault, met inside a nested key.

### A reference names nothing

<!-- covers: pkg/example.ErrReferenceNamesNothing -->

The document names something that does not resolve.

## Finding a halt from the short code the build printed

<!-- BEGIN GENERATED: halt-condition-sentences -->
| Short code | Opens with | Documented under |
| --- | --- | --- |
| ` + "`E0101`" + ` | ` + "`value is not the declared shape`" + ` | A value is not the shape the schema declares; A value is not the shape the schema declares, in a nested key |
| ` + "`E0102`" + ` | ` + "`reference resolves to nothing`" + ` | A reference names nothing |
<!-- END GENERATED: halt-condition-sentences -->

## The linter domain

Not a halting condition.
`

// TestEntriesResolvesACodeToTheProseThatDocumentsIt drives the read the whole
// capability rests on: a code, through the page's generated region, to the prose
// of the entry that names it.
func TestEntriesResolvesACodeToTheProseThatDocumentsIt(t *testing.T) {
	t.Parallel()

	entries, err := haltref.Entries([]byte(fixturePage), "E0102")
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want one entry, got %d: %+v", len(entries), entries)
	}
	if got, want := entries[0].Heading, "A reference names nothing"; got != want {
		t.Errorf("heading = %q, want %q", got, want)
	}
	if !strings.Contains(entries[0].Body, "names something that does not resolve") {
		t.Errorf("the entry body is not the prose beneath its heading:\n%s", entries[0].Body)
	}
	// The body stops at the next heading of the same or higher level. Without
	// that, every entry would run to the end of the page and the extraction would
	// pass this test while telling a reader nothing useful.
	if strings.Contains(entries[0].Body, "Finding a halt from the short code") {
		t.Errorf("the entry body ran past its own section:\n%s", entries[0].Body)
	}
	// The page's declarations to its own generator render as nothing, so a body
	// carrying one would put vocabulary from another surface in front of whoever
	// reads this at a terminal.
	if strings.Contains(entries[0].Body, "covers:") || strings.Contains(entries[0].Body, "<!--") {
		t.Errorf("the entry body carries markup a reader of the page never sees:\n%s", entries[0].Body)
	}
	// And removing it leaves no gap where it stood.
	if strings.Contains(entries[0].Body, "\n\n\n") {
		t.Errorf("removing the markup left a gap in the prose:\n%q", entries[0].Body)
	}
}

// TestAnEntryContainsItsSubsections pins the containment rule from the other
// side: a parent's prose carries the subsections written beneath it, and a
// subsection is still an entry in its own right.
func TestAnEntryContainsItsSubsections(t *testing.T) {
	t.Parallel()

	entries, err := haltref.Entries([]byte(fixturePage), "E0101")
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("a row naming two entries yields two entries, got %d: %+v", len(entries), entries)
	}
	if !strings.Contains(entries[0].Body, "The same fault, met inside a nested key.") {
		t.Errorf("the parent entry does not contain its subsection:\n%s", entries[0].Body)
	}
	if strings.Contains(entries[1].Body, "The document declared a value") {
		t.Errorf("the subsection entry carries its parent's prose:\n%s", entries[1].Body)
	}
}

// TestACodeTheRegionDoesNotNameIsReportedAsUndocumented separates the two ways
// there is no prose to show. This one is an ordinary fact about the published
// page and leaves the condition itself answerable.
func TestACodeTheRegionDoesNotNameIsReportedAsUndocumented(t *testing.T) {
	t.Parallel()

	_, err := haltref.Entries([]byte(fixturePage), "E0999")
	var undocumented *haltref.UndocumentedConditionError
	if !errors.As(err, &undocumented) {
		t.Fatalf("want an undocumented-condition answer, got %v", err)
	}
	if undocumented.ShortCode != "E0999" {
		t.Errorf("the answer names %q rather than the code asked about", undocumented.ShortCode)
	}
	if !strings.Contains(err.Error(), "E0999") {
		t.Errorf("the answer does not quote the code, so it is not actionable: %s", err)
	}
}

// TestAPageCarryingNoRegionIsReportedAsTheInstrumentFailureItIs is the other way
// there is no prose, and it must not be confused with the one above: a page with
// no region is not a page whose region omits a code.
//
// The positive control is the same read over the fixture page, which succeeds —
// without it a reader could not tell this refusal from a reader that never works.
func TestAPageCarryingNoRegionIsReportedAsTheInstrumentFailureItIs(t *testing.T) {
	t.Parallel()

	if _, err := haltref.Entries([]byte(fixturePage), "E0102"); err != nil {
		t.Fatalf("positive control: the fixture page resolves E0102, got %v", err)
	}

	stripped := strings.ReplaceAll(fixturePage, "<!-- BEGIN GENERATED: halt-condition-sentences -->", "")
	_, err := haltref.Entries([]byte(stripped), "E0102")
	if err == nil {
		t.Fatal("a page with no region resolved a code, so the region is not what the lookup reads")
	}
	var undocumented *haltref.UndocumentedConditionError
	if errors.As(err, &undocumented) {
		t.Fatalf("a page with no region was reported as a condition nobody documented: %v", err)
	}
	if !strings.Contains(err.Error(), haltref.RegionID) {
		t.Errorf("the refusal does not name the region it could not find: %s", err)
	}
}

// TestARowNamingAHeadingThePageLacksIsRefused covers the region and the prose
// parting company. Dropping the entry silently would answer as though the
// condition were merely undocumented, which is a different fact and repaired
// differently.
func TestARowNamingAHeadingThePageLacksIsRefused(t *testing.T) {
	t.Parallel()

	orphaned := strings.ReplaceAll(fixturePage, "### A reference names nothing", "### A reference names nothing at all")
	_, err := haltref.Entries([]byte(orphaned), "E0102")
	if err == nil {
		t.Fatal("a row naming a heading the page does not carry resolved anyway")
	}
	var undocumented *haltref.UndocumentedConditionError
	if errors.As(err, &undocumented) {
		t.Fatalf("an orphaned row was reported as a condition nobody documented: %v", err)
	}
	if !strings.Contains(err.Error(), "A reference names nothing") {
		t.Errorf("the refusal does not name the entry it could not find: %s", err)
	}
}

// TestAHeadingInsideAFenceIsNotAHeading pins the one place a line may open with
// the heading character while opening no heading. Reading a fenced line as a
// heading would split the page at a line no reader sees as a boundary, and every
// entry after it would carry the wrong prose.
func TestAHeadingInsideAFenceIsNotAHeading(t *testing.T) {
	t.Parallel()

	fenced := strings.Replace(fixturePage,
		"The document names something that does not resolve.",
		"```yaml\n# A reference names nothing\nkey: value\n```\n\nThe document names something that does not resolve.", 1)
	entries, err := haltref.Entries([]byte(fenced), "E0102")
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if !strings.Contains(entries[0].Body, "The document names something that does not resolve.") {
		t.Errorf("a comment inside a fence was read as a heading and split the entry:\n%s", entries[0].Body)
	}
}

// TestResolveAnswersFromTheCompilersOwnDeclarations drives the offline half: a
// code, through the registry, to what the compiler declares about the condition.
//
// Both spellings of the argument are exercised, and both are read off one real
// condition rather than quoted, so the case cannot come to name a condition that
// has moved.
func TestResolveAnswersFromTheCompilersOwnDeclarations(t *testing.T) {
	t.Parallel()

	probe := docscache.ErrPageNotFound()
	wantShort, ok := cerrors.ShortCodeOf(probe)
	if !ok {
		t.Fatal("the probe condition reports no short code, so this case would assert nothing")
	}
	wantCode, ok := cerrors.CodeOf(probe)
	if !ok {
		t.Fatal("the probe condition reports no descriptive code, so this case would assert nothing")
	}

	for _, tc := range []struct {
		name string
		arg  string
	}{
		{"the short code a diagnostic prints", wantShort},
		{"the short code as a reader might retype it", strings.ToLower(wantShort)},
		{"the short code with the whitespace of a paste", "  " + wantShort + "\n"},
		{"the descriptive code a structured diagnostic carries", wantCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := haltref.Resolve(tc.arg)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.arg, err)
			}
			if got.ShortCode != wantShort || got.Code != wantCode {
				t.Errorf("Resolve(%q) = %s / %s, want %s / %s", tc.arg, got.ShortCode, got.Code, wantShort, wantCode)
			}
			if got.Message != probe.Error() {
				t.Errorf("Resolve(%q) reports the sentence %q, and the condition registers %q",
					tc.arg, got.Message, probe.Error())
			}
		})
	}
}

// TestACodeNoConditionCarriesIsRefusedWithBothShapes pins the refusal a reader
// acts on. E0000 is the one value that can never be an answer: numbers are handed
// out from E0001 upward, so nothing is ever registered under it.
func TestACodeNoConditionCarriesIsRefusedWithBothShapes(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"E0000", "not-a-code", ""} {
		_, err := haltref.Resolve(arg)
		var unknown *haltref.UnknownCodeError
		if !errors.As(err, &unknown) {
			t.Fatalf("Resolve(%q) = %v, want an unknown-code refusal", arg, err)
		}
		if unknown.Code != arg {
			t.Errorf("the refusal quotes %q rather than the argument %q", unknown.Code, arg)
		}
		// The refusal is only actionable if it says what a code looks like, in
		// both of the shapes a reader could be holding one in.
		for _, want := range []string{"E0164", "resolver/parameter-contract-violation", "sentinels.md"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal for %q omits %q, so a reader cannot correct from it: %s", arg, want, err)
			}
		}
	}
}

// TestARetiredNumberIsAnsweredWithWhatItNamed pins the refusal that keeps the
// numbering's promise reachable: a number quoted from an old report is answered
// with what it named rather than with silence, and never with a different
// condition.
//
// The number is quoted because a retired one is fixed by the same rule that makes
// it worth answering: it named a condition once and is never handed to another, so
// there is nothing here that can drift. A record that stopped naming it would fail
// this case, which is the failure worth having.
func TestARetiredNumberIsAnsweredWithWhatItNamed(t *testing.T) {
	t.Parallel()

	_, err := haltref.Resolve("E0157")
	var retired *haltref.RetiredCodeError
	if !errors.As(err, &retired) {
		t.Fatalf(`Resolve("E0157") = %v, want a retired-code answer`, err)
	}
	if retired.Note == "" {
		t.Error("the retired-code answer carries no note, so it tells a reader nothing about what the number named")
	}
	if !strings.Contains(err.Error(), retired.Note) {
		t.Errorf("the rendered answer drops the note: %s", err)
	}
	if !strings.Contains(err.Error(), "retired") {
		t.Errorf("the answer does not say the number is retired, so it reads as a code that never existed: %s", err)
	}
}

// TestShortCodeInReadsACodeOutOfADiagnostic pins the read a surface handed a
// whole diagnostic performs, together with the near-misses that must not be read
// as codes.
func TestShortCodeInReadsACodeOutOfADiagnostic(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"a diagnostic's bracketed head", "[CATALOG_ERROR E0209] parameter contract violated", "E0209"},
		{"a code quoted mid-sentence", "the build reported E0101, then stopped", "E0101"},
		{"a code at the very end", "reported as E0101", "E0101"},
		{"too few digits", "E020 is not a code", ""},
		{"too many digits", "E02099 is not a code", ""},
		{"part of a longer identifier", "the value KE0209X is not a code", ""},
		{"no code at all", "load release foo: not found", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := haltref.ShortCodeIn(tc.text)
			if tc.want == "" {
				if ok {
					t.Fatalf("ShortCodeIn(%q) read %q, and the text carries no code", tc.text, got)
				}
				return
			}
			if !ok {
				t.Fatalf("ShortCodeIn(%q) read nothing, want %q", tc.text, tc.want)
			}
			if got != tc.want {
				t.Errorf("ShortCodeIn(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestHeadingLevelSeparatesAHeadingFromALineThatMerelyStartsLikeOne pins the rule
// both sides of the page read, since a page split two ways resolves a row to a
// neighbour's prose.
func TestHeadingLevelSeparatesAHeadingFromALineThatMerelyStartsLikeOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		line string
		want int
	}{
		{"# Fatal Halts", 1},
		{"###### Six", 6},
		{"####### Seven is not a heading", 0},
		{"#NoSpace", 0},
		{"#", 0},
		{"###", 0},
		{"not a heading", 0},
		{"", 0},
	} {
		if got := haltref.HeadingLevel(tc.line); got != tc.want {
			t.Errorf("HeadingLevel(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}
