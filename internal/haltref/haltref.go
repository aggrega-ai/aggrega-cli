// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package haltref

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/docsplice"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// PagePath is the halting-conditions page, as a slash-separated path relative to
// the documentation tree's root.
//
// One constant serves two addressings because they are the same path: the
// generator writes the page at this path beneath the tree it is given, and the
// documentation bundle carries the tree with its layout intact, so a reader
// asking the cache for this path gets the page the generator wrote.
const PagePath = "language-reference/errors/fatal-halts.md"

// RegionID is the id of the generated region on [PagePath] that pairs each
// condition's short code with the entry documenting it.
//
// It is declared here rather than in the generator so that the side writing the
// region and the side reading it name one region. Two declarations would agree
// until one was edited, and the reader's failure would be silent: a region it
// could not find reads exactly like a condition no entry documents.
const RegionID = "halt-condition-sentences"

// ReferencePagePath is the page listing every condition under both of its codes,
// as a slash-separated path relative to the documentation tree's root.
//
// It is a different page from [PagePath] because it answers a different question,
// and the difference is what makes it worth reading. The halting-conditions page
// documents the conditions an author is taught to repair; this one resolves EVERY
// condition the compiler declares — including the ones declared inside a provider
// guest, which the host binary does not link and therefore cannot resolve from its
// own registry.
const ReferencePagePath = "language-reference/errors/sentinels.md"

// ReferenceRegionID is the id of the generated region on [ReferencePagePath] that
// carries every condition under its short code, its descriptive code and the
// sentence it registers.
//
// It is declared here for the reason [RegionID] is: the generator writes the
// region and this package reads it, and a second spelling of the id would leave
// the reader silently resolving nothing.
const ReferenceRegionID = "condition-short-codes"

// docsTreePrefix is where the published documentation sits when a reader has the
// repository rather than a served copy. A message points a reader at a page the
// way they will find it, and the bundle-relative path is not that way.
const docsTreePrefix = "aggrega-docs/"

// PublishedPath renders a bundle-internal page path as a reader finds it in the
// published documentation.
func PublishedPath(page string) string { return docsTreePrefix + page }

// Entry is one entry on the halting-conditions page: the heading that opens it
// and the prose beneath, up to the next heading of the same or higher level.
type Entry struct {
	// Heading is the entry's heading text, without the leading marker.
	Heading string

	// Body is the entry's markdown, the heading line included, as a reader of
	// the rendered page sees it.
	//
	// What a reader does NOT see is removed, and that is the reason this is not
	// simply the page's bytes. The page carries declarations addressed to the
	// program that generates its tables, written as markup comments precisely so
	// they render as nothing — so a caller reproducing the raw bytes would put
	// vocabulary belonging to another surface in front of somebody reading a
	// terminal, which the comment form exists to prevent.
	Body string
}

// UnknownCodeError reports a code no condition is registered under.
//
// Its text is a definitive negative, so a caller may surface it only once every
// place a condition is declared has actually been searched. [Resolve] and
// [ResolveIn] each return it for their own half of that search, and neither half
// is the whole one: a condition a provider declares is absent from the registry
// and present in the published reference. Where a half could not be searched at
// all, what the caller has measured is an [UnresolvedCodeError] instead.
type UnknownCodeError struct {
	// Code is the argument as it was supplied, so the refusal quotes what the
	// caller actually typed rather than a normalised form they never wrote.
	Code string
}

// Error renders the corrective text for a code that names no condition. It states
// both shapes a code takes, because a caller who typed one of them may be holding
// a value of the other, and it names the page that lists every condition under
// both, so the correction needs no second lookup.
func (e *UnknownCodeError) Error() string {
	return fmt.Sprintf("no halting condition is registered under %q. %s Every condition is listed "+
		"under both in %s", e.Code, codeShapes(), PublishedPath(ReferencePagePath))
}

// UnresolvedCodeError reports a code that resolved to nothing here while the
// published reference could not be read, so whether it names a condition is
// undetermined.
//
// It is separate from [UnknownCodeError] because the two are different
// measurements and a reader acts on them differently. A code absent from a
// reference that WAS read names nothing, and the repair is the code. A code
// absent only from this binary's own declarations may still name a condition a
// provider declares — those are registered inside the guest that raises them,
// never in the host that loaded it — so with the reference unread there are two
// repairs open and nothing yet to tell which one applies.
//
// Nothing in this package constructs it. Neither read here knows whether the
// other one happened, and that is precisely the fact this reports, so it belongs
// to whichever caller sequenced them.
type UnresolvedCodeError struct {
	// Code is the argument as it was supplied, for the reason
	// [UnknownCodeError] keeps it that way.
	Code string
}

// Error renders the corrective text for a code left undetermined. It claims only
// what was measured — that this binary declares no such code — and leaves both
// repairs standing, because at this point either could be the one the reader
// needs.
func (e *UnresolvedCodeError) Error() string {
	return fmt.Sprintf("%q is not one of the halting conditions this binary declares, and the "+
		"published reference — which lists the conditions a provider declares for itself as well — "+
		"could not be read, so whether the code names a condition is undetermined. Either repair may "+
		"be the one you need: correct the code, or get the documentation. %s", e.Code, codeShapes())
}

// codeShapes renders the sentence every refusal needs when the reader may be
// holding the other spelling of a code: the two shapes a code takes, with an
// example of each.
//
// It has one home because two refusals state it. A second copy would agree on the
// day it was written and diverge the first time either was edited, and a reader
// would have no way of telling which spelling of the advice was the current one.
//
// The two examples are the two codes of ONE condition, deliberately. Examples
// drawn from different conditions read as a pair and teach, in passing, that a
// short code and a descriptive code correspond one to one — which is the thing a
// reader has to understand in order to use either.
func codeShapes() string {
	return "A code is either the short code a diagnostic prints — the letter E and four digits, as " +
		"in E0164 — or the descriptive code a structured diagnostic carries, which for that same " +
		"condition is resolver/parameter-contract-violation."
}

// RetiredCodeError reports a short code that named a condition and no longer
// does.
//
// It is separate from [UnknownCodeError] because the two are different facts and
// a reader acts on them differently. An unrecognised code may be a typo; a
// retired one was correct when it was recorded, and what the reader needs is what
// it named and that no other condition will ever take the number.
type RetiredCodeError struct {
	// ShortCode is the retired number, normalised to the shape a registration
	// declares.
	ShortCode string

	// Note is the sentence the retirement record holds for the number: what it
	// named and what became of the condition.
	Note string
}

// Error renders the corrective text for a retired short code.
func (e *RetiredCodeError) Error() string {
	return fmt.Sprintf("the short code %s is retired: nothing reports it now, and the number is never "+
		"handed to a different condition, so a code quoted from an old report is never sent somewhere "+
		"else. %s", e.ShortCode, e.Note)
}

// UndocumentedConditionError reports a condition the halting-conditions page
// carries no entry for.
//
// It is not a failure to resolve the code: the condition exists and its own
// sentence is available from the compiler's declarations. What is absent is the
// prose that teaches the authoring fix, so a caller renders this as the note it
// is rather than as a refusal.
type UndocumentedConditionError struct {
	// ShortCode is the condition's short code, which is how the page's region
	// addresses an entry.
	ShortCode string
}

// Error renders the note for a condition no entry documents.
func (e *UndocumentedConditionError) Error() string {
	return fmt.Sprintf("the halting-conditions page carries no entry for %s yet, so there is no "+
		"walk-through of the authoring fix to show. The condition is listed with every other in %s",
		e.ShortCode, PublishedPath(ReferencePagePath))
}

// Resolve returns the condition the given code names, reading the compiler's own
// declarations. It reaches nothing outside the binary, so it answers offline.
//
// Both of a condition's codes are accepted, and neither is a second name for the
// capability: they are two spellings of the ARGUMENT, and the product puts each
// of them in front of a different reader. The short code opens a printed
// diagnostic; the descriptive code travels as a field of its own on the
// structured diagnostic a client reads. Accepting only one would leave whoever
// was handed the other with a code the tool declines to explain.
//
// The short code is matched without regard to case, because it is a value read
// off a terminal and typed back in. The descriptive code is matched exactly: it
// is an identifier a consumer branches on, and a lenient match would answer for a
// code that is not the one the caller holds.
//
// A retired short code yields a [RetiredCodeError] carrying what the number
// named, and anything else a [UnknownCodeError]. The two are separate because a
// reader repairs them differently.
func Resolve(code string) (cerrors.Condition, error) {
	trimmed := strings.TrimSpace(code)
	upper := strings.ToUpper(trimmed)

	if c, ok := cerrors.ConditionByShortCode(upper); ok {
		return c, nil
	}
	if c, ok := cerrors.ConditionByCode(trimmed); ok {
		return c, nil
	}
	if note, retired := cerrors.RetiredShortCode(upper); retired {
		return cerrors.Condition{}, &RetiredCodeError{ShortCode: upper, Note: note}
	}
	return cerrors.Condition{}, &UnknownCodeError{Code: code}
}

// ResolveIn returns the condition the given code names, read out of the published
// reference page's generated region rather than out of this binary's registry.
//
// It exists because the registry is NOT the whole declared set, and the gap is
// structural rather than a lapse. Domain-specific work runs inside sandboxed guest
// modules, and a condition a guest declares is registered when that guest runs —
// inside the guest, never in the host that loaded it. So the host's registry
// answers for the conditions the host itself declares and for no others, while a
// build reports all of them.
//
// The published region does not have that limit: it is rendered from the
// declarations across the whole module, guests included, so it resolves every
// condition. It is therefore the fallback rather than the primary read — the
// registry answers offline and instantly for most codes, and this covers the rest,
// which is the ordering that keeps the common case free of the documentation
// entirely.
//
// Both codes are accepted, for the reason [Resolve] accepts both: the region
// carries each condition under its short code and its descriptive code, so
// whichever a reader is holding resolves.
func ResolveIn(page []byte, code string) (cerrors.Condition, error) {
	region, ok := docsplice.Region(string(page), ReferenceRegionID)
	if !ok {
		return cerrors.Condition{}, fmt.Errorf("%s carries no %q region, so there is no published "+
			"correspondence to resolve a code against; the page is not the one this reads",
			ReferencePagePath, ReferenceRegionID)
	}

	trimmed := strings.TrimSpace(code)
	upper := strings.ToUpper(trimmed)
	for _, line := range strings.Split(region, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}
		short, descriptive := unquoteCell(cells[1]), unquoteCell(cells[2])
		// The short code is matched without regard to case and the descriptive
		// code exactly, which is the rule [Resolve] applies for the same reasons.
		if !strings.EqualFold(short, upper) && descriptive != trimmed {
			continue
		}
		return cerrors.Condition{Code: descriptive, ShortCode: short, Message: unquoteCell(cells[3])}, nil
	}
	return cerrors.Condition{}, &UnknownCodeError{Code: code}
}

// Entries returns the entries the halting-conditions page documents the condition
// under, read out of that page's bytes.
//
// The lookup goes through the page's generated region rather than through its
// prose. The region is rendered from the same declarations [Resolve] reads and is
// held current by a drift gate, so a row is a checked correspondence; searching
// the prose for a code would be reading whatever an author happened to write.
//
// A condition the page documents under more than one entry yields one Entry per
// heading, in the order the region names them, because each entry describes a
// different way the condition is met.
//
// A page carrying no region at all is reported as the instrument failure it is,
// separately from a condition the region does not name: the first means the page
// is not the one this reads, and the second is an ordinary fact about a
// condition.
func Entries(page []byte, shortCode string) ([]Entry, error) {
	text := string(page)
	region, ok := docsplice.Region(text, RegionID)
	if !ok {
		return nil, fmt.Errorf("%s carries no %q region, so there is no correspondence to read a code "+
			"against; the page is not the one this resolves entries from", PagePath, RegionID)
	}

	headings := headingsFor(region, shortCode)
	if len(headings) == 0 {
		return nil, &UndocumentedConditionError{ShortCode: shortCode}
	}

	sections := sectionsIn(text)
	entries := make([]Entry, 0, len(headings))
	for _, h := range headings {
		body, found := sections[h]
		if !found {
			// A row naming a heading the page does not carry means the region and
			// the prose have parted company. Reporting it names both halves, which
			// is what a repair needs; silently dropping the entry would answer as
			// though the condition were merely undocumented.
			return nil, fmt.Errorf("%s pairs %s with the entry %q, and no heading on the page opens "+
				"that entry; the region and the prose it names have parted company",
				PagePath, shortCode, h)
		}
		entries = append(entries, Entry{Heading: h, Body: body})
	}
	return entries, nil
}

// headingsFor returns the entry headings the region's row for shortCode names, in
// row order.
//
// The region's rows are `| short code | sentence | entry heading |`, and a cell
// can carry no table separator because the generator refuses a declaration that
// would break a row — so splitting on the separator recovers the cells exactly.
// Several headings on one row are joined by a semicolon and a space, which is the
// form the generator writes for a condition documented more than once.
func headingsFor(region, shortCode string) []string {
	for _, line := range strings.Split(region, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}
		if !strings.EqualFold(unquoteCell(cells[1]), shortCode) {
			continue
		}
		var out []string
		for _, h := range strings.Split(cells[3], ";") {
			if h = strings.TrimSpace(h); h != "" {
				out = append(out, h)
			}
		}
		return out
	}
	return nil
}

// unquoteCell returns a table cell's text with its surrounding whitespace and its
// code-span fence removed.
//
// The fence is trimmed rather than matched because the generator widens it for a
// value carrying backticks of its own, and pads it where the value begins or ends
// with one. Trimming the backticks and then the padding recovers the value for
// every one of those forms without this needing to model which was used.
func unquoteCell(cell string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(cell), "`"))
}

// sectionsIn returns the page's sections keyed by heading text: each heading
// against its own prose, running to the next heading of the same or higher level.
//
// Containment is the document's own, not a flat split, so an entry written as a
// subsection is a section in its own right and a parent's prose contains its
// subsections — which is the same rule the generator segments the page by when it
// decides which entry a coverage declaration sits under. The two have to agree,
// or a region naming an entry would resolve to prose belonging to a neighbour.
//
// A heading text that occurs twice keeps the FIRST occurrence. The region names
// entries by their text, so a repeated heading is already ambiguous on the page
// itself; taking the first is the answer a reader searching the page for that
// text would land on.
func sectionsIn(text string) map[string]string {
	lines := strings.Split(text, "\n")

	type heading struct {
		line  int
		level int
		text  string
	}
	var headings []heading
	// A fenced block is tracked because a code sample is the one place a line may
	// open with the heading character while opening no heading. Reading one as a
	// heading would split the page at a line no reader sees as a boundary.
	fenced := false
	for i, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if level := HeadingLevel(line); level > 0 {
			headings = append(headings, heading{line: i, level: level, text: strings.TrimSpace(line[level:])})
		}
	}

	out := make(map[string]string, len(headings))
	for i, h := range headings {
		if _, seen := out[h.text]; seen {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].level <= h.level {
				end = headings[j].line
				break
			}
		}
		out[h.text] = readerFacing(lines[h.line:end])
	}
	return out
}

// readerFacing joins an entry's lines as a reader of the rendered page sees them:
// the markup comments dropped, and the blank line each of them leaves behind
// collapsed so the prose does not gain a gap where one was removed.
//
// A comment is dropped whole-line rather than by editing within a line, because
// that is the shape the page uses — a declaration addressed to the generator sits
// on a line of its own — and because a partial removal inside a line would leave
// a sentence a reader could not tell had been cut.
func readerFacing(lines []string) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->") {
			continue
		}
		if t == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// HeadingLevel returns the level of the heading a line opens, or zero when the
// line opens none.
//
// The marker has to be followed by a space, which is what separates a heading
// from a line merely beginning with the same character.
//
// It is exported because the generator that writes this page's region reads the
// rule from here. Both sides segment the page, and a page split two ways is a
// region naming an entry that resolves to a different entry's prose.
func HeadingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n == len(line) {
		return 0
	}
	if line[n] != ' ' && line[n] != '\t' {
		return 0
	}
	return n
}

// ShortCodeIn returns the first short code appearing in text, and whether one
// does.
//
// It exists for a surface handed a whole diagnostic rather than a bare code: a
// printed diagnostic opens with its category and the condition's short code, so
// the code a reader would have quoted is already in the text they pasted.
//
// The shape is matched exactly — the letter E and four digits, bounded on both
// sides by something that is not a letter or a digit — so an identifier that
// merely begins that way is not read as a code. A value of the right shape that
// no condition is registered under simply resolves to nothing, which is the
// caller's answer to give rather than this function's.
func ShortCodeIn(text string) (string, bool) {
	for i := 0; i+shortCodeWidth <= len(text); i++ {
		if text[i] != 'E' {
			continue
		}
		if i > 0 && isCodeAdjacent(text[i-1]) {
			continue
		}
		digits := true
		for j := i + 1; j < i+shortCodeWidth; j++ {
			if text[j] < '0' || text[j] > '9' {
				digits = false
				break
			}
		}
		if !digits {
			continue
		}
		if end := i + shortCodeWidth; end < len(text) && isCodeAdjacent(text[end]) {
			continue
		}
		return text[i : i+shortCodeWidth], true
	}
	return "", false
}

// shortCodeWidth is the width of a short code: the letter E and four digits.
const shortCodeWidth = 5

// isCodeAdjacent reports whether b would make a short-code-shaped run part of a
// longer identifier, in which case the run is not a code.
func isCodeAdjacent(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	}
	return false
}
