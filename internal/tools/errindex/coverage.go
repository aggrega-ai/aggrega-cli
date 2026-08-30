// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// haltsRelPath is the halting-conditions page, relative to the documentation
// tree root. That page is written for someone authoring entities, so it names
// conditions by what the author did wrong rather than by the identifier a
// program matches — which is why the correspondence between the two has to be
// declared rather than derived from the prose.
const haltsRelPath = "language-reference/errors/fatal-halts.md"

// coverageAnchor matches the declaration an entry carries naming the identities
// it covers:
//
//	<!-- covers: pkg/resolver.ErrParameterMissing, pkg/resolver.ErrParameterContractViolation -->
//
// It is an HTML comment so that it is invisible to a reader of the rendered
// page. The page's audience is an entity author, who never types these
// identifiers and would be reading vocabulary belonging to a different surface.
//
// The declaration sits on the entry it describes rather than in a table
// elsewhere, because a table would be a second copy of the correspondence and
// the copy is what drifts: an entry could be rewritten, split or removed with
// nothing to make the table follow it. Here the correspondence moves with the
// prose it describes or disappears with it.
var coverageAnchor = regexp.MustCompile(`(?s)<!--\s*covers:(.*?)-->`)

// pageEntry is one entry on the halting-conditions page: the heading that opens
// it, the identities its coverage declaration names, and the prose beneath it.
//
// The prose runs from the heading to the next heading of the same or higher
// level, which is the document's own containment rather than a flat split. An
// entry written as a subsection of another is an entry in its own right, and a
// parent's prose contains its subsections. The asymmetry that follows is
// deliberate: a subsection's claim is checked against the subsection alone, so
// no claim is satisfied by prose belonging to a sibling — which is the whole
// reason a claim is compared against its own entry instead of against the page.
type pageEntry struct {
	Heading string   // the heading text as the page writes it; empty when no heading opens the entry
	Handles []string // the identities the declaration names, in the order it names them
	Prose   string   // the entry's text, the declaration included
}

// readEntries returns one entry per coverage declaration on the page, in page
// order.
//
// It is the only reader of the page, so the two tiers that consume it compare
// against one segmentation of one read rather than each parsing the tree for
// itself.
//
// A declaration no heading opens carries an empty Heading and empty Prose rather
// than being dropped. Dropping it would leave both tiers comparing fewer claims
// than the page declares while reporting nothing, which reads exactly like a
// page with nothing wrong; the callers report it as the instrument failure it
// is.
func readEntries(docsRoot string) ([]pageEntry, error) {
	data, err := os.ReadFile(filepath.Join(docsRoot, haltsRelPath))
	if err != nil {
		return nil, err
	}
	text := string(data)
	lines := strings.Split(text, "\n")

	type heading struct {
		line  int
		level int
		text  string
	}
	var headings []heading
	// A fenced block is tracked because a code sample is the one place a line may
	// open with the heading character while opening no heading — a YAML comment,
	// say. Reading one as a heading would split the page at a line no reader sees
	// as a boundary, and every claim after it would be compared against the wrong
	// prose.
	fenced := false
	for i, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if level := headingLevel(line); level > 0 {
			headings = append(headings, heading{line: i, level: level, text: strings.TrimSpace(line[level:])})
		}
	}

	ends := make([]int, len(headings))
	for i, h := range headings {
		ends[i] = len(lines)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].level <= h.level {
				ends[i] = headings[j].line
				break
			}
		}
	}

	matches := coverageAnchor.FindAllStringSubmatchIndex(text, -1)
	entries := make([]pageEntry, 0, len(matches))
	for _, m := range matches {
		entry := pageEntry{Handles: handlesIn(text[m[2]:m[3]])}
		// The heading that opens a declaration's entry is the last one at or
		// before it. Headings are ordered, so any later one opens an entry the
		// declaration precedes, and the last preceding one is the innermost
		// whose span the declaration falls inside.
		at := -1
		line := strings.Count(text[:m[0]], "\n")
		for i, h := range headings {
			if h.line > line {
				break
			}
			at = i
		}
		if at >= 0 {
			entry.Heading = headings[at].text
			entry.Prose = strings.Join(lines[headings[at].line:ends[at]], "\n")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// headingLevel returns the level of the heading a line opens, or zero when the
// line opens none. The marker has to be followed by a space, which is what
// separates a heading from a line merely beginning with the same character.
func headingLevel(line string) int {
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

// handlesIn splits a coverage declaration's body into the handles it names.
func handlesIn(body string) []string {
	return strings.FieldsFunc(body, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

// wellFormedHandle reports whether h is a qualified (package, name) handle.
//
// A bare name is refused rather than resolved. Three names in this module are
// declared independently in two packages each, so a bare name names no single
// identity — and an anchor carrying one would silently claim nothing, which
// fails toward a page that looks covered.
func wellFormedHandle(h string) bool {
	i := strings.LastIndex(h, ".")
	if i <= 0 || i == len(h)-1 {
		return false
	}
	return strings.HasPrefix(h[i+1:], "Err")
}

// resolveCoverage returns the set of identities the page's declarations name,
// keyed by the handle of the identity that declares each.
//
// It fails on a declaration that resolves to nothing, and it fails here, before
// anything is written: a false correspondence is worse than a missing one
// because it reads as verified and nothing downstream can tell the two apart, so
// a page carrying one must not be rendered from at all.
//
// The opposite direction — an identity no entry names — is also refused, by
// [reportGaps]. It is kept out of this function because the two refuse at
// different moments and have to: an unresolvable handle poisons the render,
// while an uncovered condition is repaired by writing an entry against a region
// that has to be rendered first.
func resolveCoverage(ids []Identity, entries []pageEntry, docsRoot string) (map[string]bool, error) {
	// Zero declarations and declarations naming nothing are different failures:
	// the first means the marker form moved, the second means every declaration
	// is empty. Both are reported as instrument failures rather than served as
	// "the page covers nothing".
	if len(entries) == 0 {
		return nil, fmt.Errorf("no coverage anchors found in %s; the anchor form or the page layout moved",
			filepath.Join(docsRoot, haltsRelPath))
	}
	var anchored []string
	for _, e := range entries {
		anchored = append(anchored, e.Handles...)
	}
	if len(anchored) == 0 {
		return nil, fmt.Errorf("%d coverage anchors in %s name no identity at all",
			len(entries), filepath.Join(docsRoot, haltsRelPath))
	}

	byHandle := make(map[string]Identity, len(ids))
	for _, id := range ids {
		byHandle[id.Handle()] = id
	}

	// Every malformed and every unresolvable handle is collected before any is
	// reported. Failing on the first would hide the rest behind it, so a page
	// with several would need as many runs to repair as it has defects.
	var malformed, unknown []string
	covered := map[string]bool{}
	for _, h := range anchored {
		if !wellFormedHandle(h) {
			malformed = append(malformed, h)
			continue
		}
		id, ok := byHandle[h]
		if !ok {
			unknown = append(unknown, h)
			continue
		}
		// An anchor may name a re-export, which is the same identity under a
		// name a caller was told to match. It is credited to the identity it
		// forwards to, so naming either handle covers the condition once.
		if id.AliasOf != "" {
			covered[id.AliasOf] = true
			continue
		}
		covered[h] = true
	}
	if len(malformed) == 0 && len(unknown) == 0 {
		return covered, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s declares a correspondence that resolves to nothing", haltsRelPath)
	if len(malformed) > 0 {
		sort.Strings(malformed)
		fmt.Fprintf(&b, "\n  not a qualified package.Name handle: %s", strings.Join(malformed, ", "))
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		fmt.Fprintf(&b, "\n  no such identity is declared: %s", strings.Join(unknown, ", "))
	}
	b.WriteString("\n  an entry naming an identity nothing declares reads as a verified correspondence " +
		"while describing nothing; correct the handle, or remove it and let the identity be reported as uncovered")
	return nil, fmt.Errorf("%s", b.String())
}

// gapsIn returns every declared identity no entry names, and how many declared
// identities were considered.
//
// Re-exports are not counted on either side. A re-export is the same identity
// under a second name, so counting it would report one condition as two gaps
// against a population that never grew.
func gapsIn(ids []Identity, covered map[string]bool) (gaps []Identity, origins int) {
	for _, id := range ids {
		if id.AliasOf != "" {
			continue
		}
		origins++
		if !covered[id.Handle()] {
			gaps = append(gaps, id)
		}
	}
	return gaps, origins
}

// reportGaps writes the gap tier to w and returns how many declared halting
// conditions no entry names, alongside how many were considered. A non-zero
// shortfall is a refusal, and the caller exits non-zero on it.
//
// Every declared halting condition earns an entry, so a condition with none is a
// hole in the published reference rather than a note about it. A figure that
// only reports leaves the set free to regrow such a hole one declaration at a
// time, with every run of every gate green while it happens — and a coverage
// figure nobody has seen fail is decoration.
//
// A blocking tier is safe to introduce only over a set that is already complete,
// because a check that is red on the day it ships stays red and gets switched
// off — which would cost the blocking direction in [resolveCoverage] along with
// it. That is a constraint on the moment such a tier is turned on, not an
// argument against turning it on: the shortfall is what says whether the
// condition holds, and it is the caller's whole verdict here.
//
// The word on a shortfall line is not WARN, because the line stops the run. The
// tier beside it does warn, and two tiers printing the same word while one of
// them fails is how a reader learns to skim both.
//
// The enumeration is opt-in because this runs inside the routine build, where
// printing every uncovered identity on every build produces a wall of output
// that is scrolled past rather than read — which costs the count its meaning
// too. The count is the signal that something moved; the list is what you ask
// for once it has.
func reportGaps(w io.Writer, ids []Identity, covered map[string]bool, list bool) (uncovered, considered int) {
	gaps, origins := gapsIn(ids, covered)
	if len(gaps) == 0 {
		fmt.Fprintf(w, "coverage: all %d declared halting conditions are named by an entry on %s\n",
			origins, haltsRelPath)
		return 0, origins
	}
	if !list {
		fmt.Fprintf(w, "REFUSED: %d of %d declared halting conditions have no entry on %s (re-run with -gaps to list them)\n",
			len(gaps), origins, haltsRelPath)
		return len(gaps), origins
	}
	// The message is printed beside the handle because a bare list of names is
	// not actionable: writing a condition's entry means knowing what it reports,
	// and the message is that description, already declared beside the identity.
	fmt.Fprintf(w, "REFUSED: %d of %d declared halting conditions have no entry on %s:\n",
		len(gaps), origins, haltsRelPath)
	for _, id := range gaps {
		fmt.Fprintf(w, "REFUSED:   %s: %s\n", id.Handle(), id.Message)
	}
	return len(gaps), origins
}

// entryClaim is one comparison the claims tier made and found wanting: the entry
// that carries the declaration, the handle it names, and the message that
// identity registers.
type entryClaim struct {
	Heading string
	Handle  string
	Message string
}

// proseWords splits s into the unit the claims comparison works in: runs of
// letters and digits, lower-cased.
//
// Everything else separates words, which is what makes the comparison blind to
// typography by construction rather than by a list of variants somebody would
// have to keep current. A backtick, an emphasis marker, a colon, a dash of any
// width and a quotation mark of either curl are all separators, so two texts
// differing only in those cannot compare unequal; a line break separates the
// same way, so a message the page wraps across lines reads as one sequence.
//
// What survives is the words and their order, so a different word, a
// reordering, an elision or an insertion is still seen.
func proseWords(s string) []string {
	var out []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return out
}

// quotesRun reports whether want appears as a contiguous run inside have.
//
// Contiguity is what makes the answer mean something. An unordered check — every
// word of want present somewhere in have — passes prose that carries the right
// vocabulary in an order that says something else, which is the shape a drifted
// template takes most often.
func quotesRun(have, want []string) bool {
	for i := 0; i+len(want) <= len(have); i++ {
		if slices.Equal(have[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// claimsIn compares each declaration against the prose of the entry carrying it
// and returns every entry whose prose does not quote the message the named
// identity registers, how many comparisons were made, and every declaration
// that could not be compared at all.
//
// The unit is the (entry, declaration) pair and not the identity. Several
// conditions are covered by more than one entry, each describing a different way
// the condition is met, so crediting an identity once would let a second entry
// pass on prose belonging to the first — the correspondence that reads as
// verified while describing nothing.
//
// A declaration naming a re-export needs no special handling: a re-export
// carries the message of the identity it forwards to, so the text compared is
// the same whichever of the two names the declaration used.
func claimsIn(ids []Identity, entries []pageEntry) (unquoted []entryClaim, compared int, unreadable []string) {
	byHandle := make(map[string]Identity, len(ids))
	for _, id := range ids {
		byHandle[id.Handle()] = id
	}
	// Entries are walked in page order and each declaration in the order it
	// names its handles, so both the count and the enumeration are a function of
	// the page rather than of a map's iteration.
	for _, e := range entries {
		prose := proseWords(e.Prose)
		for _, h := range e.Handles {
			if e.Heading == "" {
				unreadable = append(unreadable,
					h+": no heading opens the entry that declares it, so there is no entry prose to compare against")
				continue
			}
			id, ok := byHandle[h]
			if !ok {
				// Unreachable through the generator, which resolves every
				// declaration before this runs and fails on one naming nothing.
				// It is reported rather than skipped so that a caller wiring
				// this tier up on its own cannot get a quiet short count.
				unreadable = append(unreadable,
					h+": no such identity is declared, so there is no registered message to compare")
				continue
			}
			want := proseWords(id.Message)
			if len(want) == 0 {
				unreadable = append(unreadable,
					h+": registers no message, so there is nothing for the entry to quote")
				continue
			}
			compared++
			if quotesRun(prose, want) {
				continue
			}
			unquoted = append(unquoted, entryClaim{Heading: e.Heading, Handle: h, Message: id.Message})
		}
	}
	return unquoted, compared, unreadable
}

// reportClaims writes the claims tier to w: a count always, and the entries
// themselves when list is set.
//
// This tier answers the question the gap tier cannot. The gap tier asks whether
// a condition has an entry at all; this one asks whether the entry it has says
// what the condition actually reports, which is the sentence a reader acts on.
//
// It never fails, and unlike the gap tier that is permanent rather than a
// property of the page's current state. An entry's message template legitimately
// paraphrases, reorders and splits the text a condition registers, so a
// comparison strict enough to catch a template that has drifted also refuses
// prose that is perfectly good. Two classes therefore sit in this count
// permanently: a condition whose registered text is generic because it carries a
// wrapped failure that the entry documents in its place, and an entry that
// reframes the registered text across several message forms. A check that is red
// on the day it ships stays red and gets switched off, so what this reports is a
// count that moves when something drifts, never a verdict on the page.
//
// The gap tier asks a question with no such class: a condition either has an
// entry or has none, and no legitimate authoring choice produces the second. That
// is why one of the two refuses and this one does not.
//
// The enumeration is opt-in for the reason the gap tier's is: this runs inside
// the routine build, where printing every entry on every build produces a wall
// of output that is scrolled past rather than read.
func reportClaims(w io.Writer, ids []Identity, entries []pageEntry, list bool) {
	unquoted, compared, unreadable := claimsIn(ids, entries)
	// A declaration that could not be compared prints whether or not the
	// enumeration was asked for. It means the comparison did not happen, and a
	// tier quietly comparing fewer claims than the page declares reads exactly
	// like one that found nothing wrong.
	for _, u := range unreadable {
		fmt.Fprintf(w, "WARN: %s could not be compared: %s\n", haltsRelPath, u)
	}
	if len(unquoted) == 0 {
		fmt.Fprintf(w, "WARN: none — all %d entry claims on %s quote the message their condition registers\n",
			compared, haltsRelPath)
		return
	}
	if !list {
		fmt.Fprintf(w, "WARN: %d of %d entry claims on %s do not quote the message their condition registers "+
			"(re-run with -claims to list them)\n", len(unquoted), compared, haltsRelPath)
		return
	}
	fmt.Fprintf(w, "WARN: %d of %d entry claims on %s do not quote the message their condition registers:\n",
		len(unquoted), compared, haltsRelPath)
	// The registered message is printed beside the entry because the report is
	// not actionable without it: deciding whether an entry has drifted or is
	// reframing means seeing the text it was meant to carry.
	for _, c := range unquoted {
		fmt.Fprintf(w, "WARN:   %s under %q: %s\n", c.Handle, c.Heading, c.Message)
	}
}
