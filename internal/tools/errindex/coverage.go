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

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
)

// haltsRelPath is the halting-conditions page, relative to the documentation
// tree root. That page is written for someone authoring entities, so it names
// conditions by what the author did wrong rather than by the identifier a
// program matches — which is why the correspondence between the two has to be
// declared rather than derived from the prose.
//
// The path is read from the package that resolves a code against that page
// rather than spelled again here. The side that writes the page's region and the
// side that reads it have to name one page, and a second spelling of it would
// agree until the day one of them was edited — at which point the reader would
// resolve nothing and report it as a condition no entry documents.
const haltsRelPath = haltref.PagePath

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
// line opens none.
//
// The rule is read from the package that resolves a code against this page
// rather than implemented again here, because both sides segment the same page:
// this one to decide which entry a coverage declaration sits under, that one to
// find the entry a rendered row names. Two implementations would split the page
// two ways, and a row would then resolve to a neighbour's prose while reading as
// a resolved correspondence.
func headingLevel(line string) int {
	return haltref.HeadingLevel(line)
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
// conditions no entry names, alongside how many were considered.
//
// 🔴 This tier REPORTS and does not refuse. The caller discards the shortfall
// deliberately, and the reason is recorded at that call site: a blocking figure
// here would refuse the moment any condition is declared ahead of its entry,
// which is the ordinary shape of work in progress — a condition is registered by
// the change that raises it and documented afterwards. So a blocking tier would
// redden the gate against correct work elsewhere in the tree, and stop somebody
// who is never the person able to clear it. The opposite direction in
// [resolveCoverage] does refuse, because a declaration naming nothing is
// something that is actually wrong rather than something merely not yet written.
//
// Every declared halting condition earns an entry, so a condition with none is a
// hole in the published reference rather than a note about it. The figure is
// therefore the signal that something moved, and it is worth promoting to a
// refusal once the declared set is stable and the documented set has caught up.
//
// The word on a shortfall line is WARN, matching what the line does. A tier that
// stops the run says so in its own word instead, so a reader meeting a red can
// tell which tier produced it without re-running anything.
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
		fmt.Fprintf(w, "WARN: %d of %d declared halting conditions have no entry on %s (re-run with -gaps to list them)\n",
			len(gaps), origins, haltsRelPath)
		return len(gaps), origins
	}
	// The message is printed beside the handle because a bare list of names is
	// not actionable: writing a condition's entry means knowing what it reports,
	// and the message is that description, already declared beside the identity.
	fmt.Fprintf(w, "WARN: %d of %d declared halting conditions have no entry on %s:\n",
		len(gaps), origins, haltsRelPath)
	for _, id := range gaps {
		fmt.Fprintf(w, "WARN:   %s: %s\n", id.Handle(), id.Message)
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

// entryText splits an entry's prose into the two texts the claims comparison
// works over: the emitted-message blocks the entry quotes, and everything the
// entry presents verbatim — those blocks together with the code spans in its
// body.
//
// The heading is excluded from both, and that exclusion is the point of the
// split rather than a detail of it. An entry's heading names the condition in
// the author's words, and those words are frequently the registered sentence
// itself, so a comparison able to see the heading is answered by it: neutralising
// an entry's block leaves such a comparison passing, and only neutralising the
// heading alongside the block makes it fail. A tier a heading can satisfy reports
// whether the entry mentions the condition, never whether the output it quotes is
// what a build prints — and the second is the question a reader acts on.
//
// Two texts rather than one, because they answer different questions. The block
// is the transcript: what a build actually printed, quoted as a blockquote or
// reproduced in a fenced text transcript. The wider text also admits an inline
// code span, which is how an entry quotes a registered sentence no transcript can
// contain — several conditions register a sentence whose emitted line interpolates
// a position into the middle of it, so the fixed words appear contiguously in no
// output at all, and the entry names the condition it is recorded under instead.
//
// Ordinary explanatory prose is in neither text. An entry that describes the
// condition in its own words therefore satisfies nothing, which is what separates
// this from a comparison against the entry at large.
func entryText(prose string) (block, quoted []string) {
	lines := strings.Split(prose, "\n")
	// The heading opens the prose, so dropping the first line when it is one
	// removes it without needing to know which entry this is.
	if len(lines) > 0 && headingLevel(lines[0]) > 0 {
		lines = lines[1:]
	}

	var blockText, spanText strings.Builder
	fenced := false
	transcript := false
	for _, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if !fenced {
				// Only a transcript fence carries emitted output. A fence
				// carrying source in some other language is a code sample, and
				// admitting one would let a snippet answer for a message.
				info := strings.TrimLeft(strings.TrimLeft(t, "`~"), " \t")
				transcript = info == "text"
			} else {
				transcript = false
			}
			fenced = !fenced
			continue
		}
		if fenced {
			if transcript {
				blockText.WriteString(line)
				blockText.WriteByte('\n')
			}
			continue
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, ">") {
			blockText.WriteString(strings.TrimPrefix(t, ">"))
			blockText.WriteByte('\n')
			continue
		}
		for _, span := range codeSpansIn(line) {
			spanText.WriteString(span)
			spanText.WriteByte('\n')
		}
	}

	block = proseWords(blockText.String())
	// The wider text is the block's words followed by the spans', and the two are
	// separated by the newline each carries. proseWords treats a line break as a
	// separator, so no run can straddle the join and read as quoted text that the
	// entry never wrote contiguously.
	quoted = proseWords(blockText.String() + "\n" + spanText.String())
	return block, quoted
}

// codeSpansIn returns the backtick-delimited spans on one line.
//
// The spans are what an entry uses to present a string it is quoting rather than
// describing, so they are read as the entry's own verbatim text. Splitting on the
// backtick makes every odd segment a span, which handles a line carrying several
// and a line carrying none without either being a special case.
func codeSpansIn(line string) []string {
	parts := strings.Split(line, "`")
	var spans []string
	for i := 1; i < len(parts); i += 2 {
		if parts[i] != "" {
			spans = append(spans, parts[i])
		}
	}
	return spans
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

// claimsIn compares each declaration against the text of the entry carrying it
// and returns every entry that does not quote the message the named identity
// registers, how many comparisons were made, how many of those passed on
// something other than an emitted-message block, and every declaration that could
// not be compared at all.
//
// The text compared is the entry's verbatim text rather than its prose — see
// [entryText] for what that admits and, more importantly, for what it excludes.
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
func claimsIn(ids []Identity, entries []pageEntry) (unquoted []entryClaim, compared, offBlock int, unreadable []string) {
	byHandle := make(map[string]Identity, len(ids))
	for _, id := range ids {
		byHandle[id.Handle()] = id
	}
	// Entries are walked in page order and each declaration in the order it
	// names its handles, so both the count and the enumeration are a function of
	// the page rather than of a map's iteration.
	for _, e := range entries {
		block, quoted := entryText(e.Prose)
		for _, h := range e.Handles {
			if e.Heading == "" {
				unreadable = append(unreadable,
					h+": no heading opens the entry that declares it, so there is no entry text to compare against")
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
			if quotesRun(block, want) {
				continue
			}
			if quotesRun(quoted, want) {
				// Passed on a code span rather than on a transcript. Counted
				// because it is the residue that says how much of the page is
				// held to what a build prints and how much to what the entry
				// says it is recorded under — a figure that moves when the
				// balance moves, rather than a verdict on either.
				offBlock++
				continue
			}
			unquoted = append(unquoted, entryClaim{Heading: e.Heading, Handle: h, Message: id.Message})
		}
	}
	return unquoted, compared, offBlock, unreadable
}

// reportClaims writes the claims tier to w and returns how many entry claims do
// not quote the message their condition registers, alongside how many were
// compared. A non-zero shortfall is a refusal, and the caller exits non-zero on
// it. The entries themselves are named when list is set.
//
// This tier answers the question the gap tier cannot. The gap tier asks whether
// a condition has an entry at all; this one asks whether the entry it has quotes
// what the condition actually reports, which is the sentence a reader acts on.
//
// It refuses, and what makes refusing safe is the text it compares against
// rather than any softening of the comparison. Compared against an entry at
// large, the tier had to tolerate an entry legitimately paraphrasing, reordering
// and splitting the registered text, so a comparison strict enough to catch a
// drifted template also refused prose that was perfectly good — and a check that
// is red on the day it ships stays red and gets switched off. Compared against
// [entryText] — what the entry presents verbatim, its heading excluded — the two
// classes that used to sit in the count permanently do not arise: a condition
// whose registered sentence no emitted line contains contiguously is quoted by
// the entry as the sentence it is recorded under, and that is a code span, which
// the comparison reads.
//
// The word on a shortfall line is not WARN, because the line stops the run, and
// it names the wording cause rather than the coverage one: two refusing tiers
// printing an indistinguishable line is how a reader comes to re-run a gate to
// find out which of them failed.
//
// The residue figure beside the verdict is a count and never a verdict. It says
// how many claims passed on a code span rather than on a transcript of emitted
// output, which is what tells a reader how much of the page is held to what a
// build prints. A page whose residue grows is drifting toward describing its
// conditions instead of quoting them, and no threshold here could say when that
// has gone too far — so the figure is reported and judged by whoever reads it.
//
// The enumeration is opt-in for the reason the gap tier's is: this runs inside
// the routine build, where printing every entry on every build produces a wall
// of output that is scrolled past rather than read.
func reportClaims(w io.Writer, ids []Identity, entries []pageEntry, list bool) (unquotedCount, compared int) {
	unquoted, compared, offBlock, unreadable := claimsIn(ids, entries)
	// A declaration that could not be compared prints whether or not the
	// enumeration was asked for. It means the comparison did not happen, and a
	// tier quietly comparing fewer claims than the page declares reads exactly
	// like one that found nothing wrong.
	for _, u := range unreadable {
		fmt.Fprintf(w, "WARN: %s could not be compared: %s\n", haltsRelPath, u)
	}
	if len(unquoted) == 0 {
		fmt.Fprintf(w, "claims: all %d entry claims on %s quote the message their condition registers "+
			"(%d of them from a code span naming the condition rather than from a quoted transcript)\n",
			compared, haltsRelPath, offBlock)
		return 0, compared
	}
	if !list {
		fmt.Fprintf(w, "REFUSED: %d of %d entry claims on %s do not quote the message their condition registers "+
			"(re-run with -claims to list them)\n", len(unquoted), compared, haltsRelPath)
		return len(unquoted), compared
	}
	fmt.Fprintf(w, "REFUSED: %d of %d entry claims on %s do not quote the message their condition registers:\n",
		len(unquoted), compared, haltsRelPath)
	// The registered message is printed beside the entry because the report is
	// not actionable without it: repairing an entry means seeing the text it was
	// meant to carry.
	for _, c := range unquoted {
		fmt.Fprintf(w, "REFUSED:   %s under %q: %s\n", c.Handle, c.Heading, c.Message)
	}
	return len(unquoted), compared
}
