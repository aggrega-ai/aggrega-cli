// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command errindex reads the halting-condition identities this module declares
// and serves them to the two consumers that need them.
//
// With -out it refreshes the generated regions of three published pages, so
// their rows are a function of the declarations rather than a transcription of
// them: the sentinel reference, which resolves any code a build prints; the
// halting-conditions page, whose region resolves a code to the entry that teaches
// the authoring fix; and the advisory page, whose region carries the conditions a
// build reports without stopping. With -index it prints the identity set as
// tab-separated records for a consumer outside this module.
//
// Run it as:
//
//	go run ./internal/tools/errindex -out ../aggrega-docs
//	go run ./internal/tools/errindex -out ../aggrega-docs -gaps
//	go run ./internal/tools/errindex -out ../aggrega-docs -claims
//	go run ./internal/tools/errindex -out ../aggrega-docs -transcripts
//	go run ./internal/tools/errindex -out ../aggrega-docs -crossrefs
//	go run ./internal/tools/errindex -out <copy> -refs ../aggrega-docs
//	go run ./internal/tools/errindex -index
//
// The module's `make docs` target invokes the first form. It writes only inside
// the regions its markers delimit; every other byte of either page is curated
// prose and survives untouched.
//
// # Which conditions reach the reference
//
// A condition earns a place on the sentinel reference by being RAISED —
// something in the module invokes the accessor that publishes it, so a build can
// actually report it — or by its registration naming the work that will raise it.
// A registration that is neither is absent from those pages by construction:
// nothing hand-writes them, so there is no edit that puts an unraisable condition
// on a page promising a check that never fires, and a note cannot be acquired by
// neglect because registration refuses one carrying no text.
//
// Absence is never silent. Every -out run names every excluded registration, and
// every condition published as declared-ahead, so a condition cannot become
// invisible by dropping off the reference — which would only move the problem the
// rule exists to fix.
//
// Every -out run reports four sizes, so a caller can state what its verdict
// covered instead of leaving the reader of a reassuring line to guess whether the
// whole set was behind it or a fragment of one: the identity set it read, the
// subset of that set the reference carries under the rule above, the set the
// halting-conditions page documents, which is what that page's region carries,
// and the set a check declares at warning severity, which is what the advisory
// page's region carries.
//
// # Where the advisory set comes from, and why not from the registration
//
// A registration declares a condition's wording and its two codes and says
// nothing about what a violation does to a build. Consequence belongs to the
// rule that raises the condition rather than to the wording it renders, so it is
// declared there: a check names the condition it raises and states whether a
// violation halts the build or is reported and passed over. Reading both fields
// off one declaration is what lets a severity be attributed to a condition at
// all, and it is why the advisory corpus is narrower than the advisory surface —
// a finding raised by a pass of its own, outside any declared check, carries no
// severity for this to read.
//
// # What every -out run holds the published pages to
//
// Four checks run beside the write, and each refuses. They are separate because
// they fail for different reasons and are repaired differently, and each names
// its own cause on its verdict line so a reader meeting a red does not have to
// re-run anything to learn which of them stopped the run.
//
//   - Coverage: every declared halting condition has an entry on the
//     halting-conditions page. A condition with none is a hole in the published
//     reference rather than a note about it.
//   - Claims: every entry quotes the message the condition it covers registers.
//     Compared against what the entry presents VERBATIM — the emitted-message
//     blocks it quotes and the code spans in its body — never against the entry's
//     prose at large, and never against its heading.
//   - Transcripts: every reproduction of emitted output across the reference
//     quotes text some condition registers, or declares which unregistered
//     surface it reproduces.
//   - Cross-references: every prose reference naming a section of the reference
//     by its title resolves to a heading that exists.
//
// 🔴 None of the four is made redundant by the generated regions, and the reason
// is structural rather than a matter of degree. A splice writes only inside its
// markers, so every byte outside one — and every byte of a page carrying no
// region at all — is an input to BOTH sides of any regenerate-and-compare check
// and can never produce a difference however wrong it is. The regions
// single-source the sentences they carry; they say nothing about the
// emitted-message forms quoted around them, which are occurrence-shaped and are
// a transcription of no declaration.
//
// 🔴 The claims comparison excludes the entry's heading, and the exclusion is what
// makes the check mean anything. An entry's heading names the condition in the
// author's words, and those words are frequently the registered sentence itself,
// so a comparison able to see the heading was answered by it: neutralising an
// entry's emitted-message block left the comparison passing, and only neutralising
// the heading alongside the block made it fail. Do not widen the compared text
// back to the entry's prose — that restores a tier reporting whether an entry
// mentions its condition, in place of one reporting whether the output it quotes
// is what a build prints.
//
// Add -gaps, -claims, -transcripts or -crossrefs to name the members of any of
// the four — the form to reach for when a count has moved and you want to know
// what moved it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/docsplice"
)

// regionID and shortCodeRegionID name the two generated regions within the
// sentinel reference. haltRegionID names the one generated region within the
// halting-conditions page. Each is the id carried by that region's BEGIN/END
// markers.
//
// The first two are two regions on one page rather than two pages because they
// are two views of one correspondence, and the page already tells a reader that
// the condition a code names is looked up here. A second page would be a second
// home for one lookup.
//
// The third is on a different page because it answers a different question. The
// sentinel reference resolves any code a build prints; the halting-conditions
// page resolves a code to the entry that teaches the authoring fix, so its region
// is scoped to the conditions that page documents and lives beside them.
//
// Each is spliced by its own id, so the curated prose between and around them —
// which introduces the table that follows it — survives every write untouched.
// advisoryRegionID names the one generated region within the advisory page. It
// is on a third page because it answers a third question: not which conditions
// exist, and not which of them a page documents, but which of them a build
// reports without stopping. That fact is declared beside the rule that raises a
// condition rather than beside the condition, so it is the one of the three no
// other region can carry.
const (
	regionID          = "error-identities"
	shortCodeRegionID = "condition-short-codes"
	haltRegionID      = "halt-condition-sentences"
	advisoryRegionID  = "advisory-short-codes"
)

// pageRelPath is the sentinel reference, relative to the documentation tree root.
const pageRelPath = "language-reference/errors/sentinels.md"

// warningsRelPath is the advisory page, relative to the documentation tree root.
// It teaches what each advisory means and what to do about it; the region
// spliced into it is the lookup from the code a build prints to the sentence the
// advisory opens with.
const warningsRelPath = "language-reference/errors/warnings.md"

func main() {
	root := flag.String("root", ".", "path to the module root to read declarations from")
	out := flag.String("out", "", "path to the documentation tree whose generated error-reference regions to refresh")
	index := flag.Bool("index", false, "print the identity index as tab-separated records and write nothing")
	gaps := flag.Bool("gaps", false, "name every declared halting condition no entry on the halting-conditions page covers")
	claims := flag.Bool("claims", false, "name every entry on the halting-conditions page that does not quote the message the condition it covers registers")
	transcripts := flag.Bool("transcripts", false, "name every reproduction of emitted output in the published error reference that carries no text a declared condition registers")
	crossrefs := flag.Bool("crossrefs", false, "name every prose reference that names a section of the published error reference by a title no heading there matches")
	refs := flag.String("refs", "", "path to the documentation tree to read prose references from; defaults to the -out tree")
	flag.Parse()

	if (*out == "") == !*index {
		fmt.Fprintln(os.Stderr, "errindex: pass exactly one of -out or -index")
		os.Exit(2)
	}

	modulePath, err := modulePathOf(*root)
	if err != nil {
		fail(err)
	}
	ids, err := scan(*root, modulePath)
	if err != nil {
		fail(err)
	}
	if len(ids) == 0 {
		// Both outputs read as "nothing is wrong" over an empty set: the page
		// would render as an empty catalogue and the index as a clean slate. An
		// empty extraction means the declaration form moved, so it is reported as
		// the instrument failure it is rather than served as a result.
		fail(fmt.Errorf("extracted zero identities from %s; the declaration form or the tree layout moved", *root))
	}

	if *index {
		fmt.Print(renderIndex(ids))
		return
	}

	// The admission rule is applied ONCE, here, and both reference regions are
	// rendered from its result. A condition reaches the published reference when
	// something raises it, or when its registration names the work that will;
	// everything else is absent from those pages by construction, because no
	// hand writes them.
	//
	// The full declared set stays in hand and is what the coverage side below
	// reads. The halting-conditions page declares its own corpus, so an entry
	// there may name a condition this rule excludes — that is reported by name
	// rather than refused, because refusing would take down an entry an author
	// who hit the behaviour still needs. [reportUnpublished] carries the whole
	// reasoning.
	published, _ := admit(ids)
	if len(published) == 0 {
		// The two regions would render as an empty catalogue, which reads as a
		// module with no halting conditions rather than as a rule that excluded
		// everything. An extraction that finds declarations and admits none of
		// them is a broken rule, not a result.
		fail(fmt.Errorf("the admission rule excluded all %d declared identities; "+
			"raise detection or the declared-ahead note has stopped being read", len(ids)))
	}

	region, err := renderRegion(published)
	if err != nil {
		fail(err)
	}

	// The retirement record is read from the declaration the registry itself
	// consults to refuse a registration under a retired number, so the reference
	// resolves a retired code because the program refuses to reissue it, and not
	// because a list beside the page says so.
	//
	// It is read for the reference pages only. The machine-readable index above
	// carries identities a consumer can match, and a retired code names no
	// identity — a record with every field but the code empty would be a row that
	// looks like a condition and matches nothing.
	retired, err := scanRetirements(*root)
	if err != nil {
		fail(err)
	}

	shortCodeRegion, err := renderShortCodeRegion(published, retired)
	if err != nil {
		fail(err)
	}

	// The coverage declarations are read and checked before anything is
	// written, so a correspondence that resolves to nothing never leaves a
	// half-refreshed tree behind it. The warn reports themselves are printed
	// after the write, where they read as notes about the tree rather than as a
	// reason the write failed.
	//
	// The page is read once here and the same segmentation feeds both tiers. Two
	// reads would be two states of one file, and the tier reading the second
	// would be reporting against a page the other never saw.
	entries, err := readEntries(*out)
	if err != nil {
		fail(err)
	}
	covered, err := resolveCoverage(ids, entries, *out)
	if err != nil {
		fail(err)
	}

	// The halting-conditions region is rendered from the same read of the page
	// that fed the two warn tiers, so the region's membership and the counts
	// reported beneath it describe one state of one file.
	haltRegion, documented, err := renderHaltRegion(ids, entries)
	if err != nil {
		fail(err)
	}

	// Both derivations of the documented-condition set are now in hand, from one
	// read of one page, and they are compared before anything is written. Below,
	// this run reports the size of one of them as a description of the region the
	// other produced; an unchecked disagreement would make that figure describe a
	// set the published bytes do not carry.
	if err := rejectCoverageDisagreement(covered, documented); err != nil {
		fail(err)
	}

	// The advisory region is rendered from the same identity set the two
	// reference regions are, narrowed by the severity a check declares rather
	// than by the admission rule: a condition a check declares at warning
	// severity is raised by that check, so the two corpora cannot disagree here.
	advisoryRegion, err := renderAdvisoryRegion(ids)
	if err != nil {
		fail(err)
	}

	page := filepath.Join(*out, pageRelPath)
	haltPage := filepath.Join(*out, haltsRelPath)
	advisoryPage := filepath.Join(*out, warningsRelPath)

	// The regions are spliced one at a time, each by its own id into the page
	// that carries it, and a page is re-read before each splice so a later
	// splice sees the bytes an earlier one wrote. Splicing is idempotent, so a
	// run over an already-current page writes nothing and leaves the file's
	// modification time where it found it.
	var updated []string
	for _, r := range []struct{ path, id, body string }{
		{page, regionID, region},
		{page, shortCodeRegionID, shortCodeRegion},
		{haltPage, haltRegionID, haltRegion},
		{advisoryPage, advisoryRegionID, advisoryRegion},
	} {
		content, err := os.ReadFile(r.path)
		if err != nil {
			fail(err)
		}
		wrote, err := docsplice.WriteFile(r.path, string(content), r.id, r.body)
		if err != nil {
			fail(err)
		}
		if wrote && !slices.Contains(updated, r.path) {
			updated = append(updated, r.path)
		}
	}

	// The size of the set this run read is reported beside what it did with it.
	// Without it, refreshing the page from the whole declared set and refreshing
	// it from three identities produce the same line, so a reader of a
	// reassuring one cannot tell which of the two they have — and a caller that
	// wants to say what its verdict covered has nothing to say it with. The
	// count is emitted from the extraction that rendered the region, so the
	// figure and the bytes it describes cannot disagree.
	fmt.Println("identities", len(ids))

	// The size of the set the two reference regions actually carry, which since
	// the admission rule is no longer the same as the set that was read. Without
	// it the line above describes the extraction and nothing describes the
	// published bytes, so a reader could not tell a run that published every
	// declared condition from one that excluded a third of them — and the whole
	// point of the rule is that those two runs are different.
	fmt.Println("published conditions", len(published))

	// The halting-conditions region's size is reported separately and for the
	// same reason, because it is a different set: the conditions THAT page
	// documents, which is a subset of the declared ones. One figure covering both
	// would leave a reader of either verdict unable to say what it covered.
	fmt.Println("documented conditions", len(covered))

	// The advisory region's size is reported separately and for the reason the
	// sizes above are: it is a different set again — the conditions a check
	// declares at warning severity — and one figure covering it and any of the
	// others would leave a reader of either verdict unable to say what it
	// covered. It is emitted from the same extraction that rendered the region,
	// so the figure and the bytes it describes cannot disagree.
	fmt.Println("advisory conditions", len(advisoriesIn(ids)))

	// The size of the retirement record, reported for the reason the sizes above
	// are and for one more that is specific to it: an absent record and a module
	// that has retired nothing produce the same empty list, so a run that stopped
	// finding the declaration is indistinguishable from a clean one unless the
	// figure is stated. A module early in its life legitimately reports zero here.
	fmt.Println("retired codes", len(retired))

	// Every page is named, whether it moved or not. A run that writes one page
	// and leaves the other current is the common case, and a single line reporting
	// one of them reads as a run that only ever addressed one.
	for _, p := range []string{page, haltPage, advisoryPage} {
		if slices.Contains(updated, p) {
			fmt.Println("updated", p)
			continue
		}
		fmt.Println("unchanged", p)
	}

	// Every tier reports before any of them decides the exit code, so a run that
	// refuses on coverage still shows what the others found. Refusing where the
	// shortfall is computed would let the first defect hide the rest, and a page
	// with several would need as many runs to repair as it has defects.
	//
	// The admission tier warns; the coverage and claims tiers refuse, and each
	// names its own cause on its verdict line so a reader meeting a red can tell
	// a condition with no entry from an entry quoting the wrong text without
	// re-running anything.
	uncovered, considered := reportGaps(os.Stderr, ids, covered, *gaps)
	unquoted, claimsCompared := reportClaims(os.Stderr, ids, entries, *claims)

	// The transcript tier reads the whole published reference rather than the one
	// page the tiers above read, because the shape it catches is on the pages
	// that carry no generated region at all — where nothing else in the build
	// looks.
	reproductions, err := transcriptsIn(*out)
	if err != nil {
		fail(err)
	}
	unattributed, read := reportTranscripts(os.Stderr, ids, reproductions, *transcripts)

	// The cross-reference tier reads the whole documentation tree rather than the
	// error reference alone, because half of what it checks is written elsewhere:
	// a page naming one of these sections by title goes stale when the section is
	// renamed, and nothing on the renamed page records that anything named it.
	//
	// It therefore reads a root of its own where one is given, and the asymmetry
	// is deliberate. A caller that regenerates into a throwaway copy copies only
	// what this command writes, which is two pages; the tree the references live
	// in is two orders of magnitude larger, and copying it in order to read it
	// would cost more than every check here put together. Nothing is written
	// through this root.
	refsRoot := *out
	if *refs != "" {
		refsRoot = *refs
	}
	found, err := crossrefsIn(refsRoot)
	if err != nil {
		fail(err)
	}
	unresolved, resolved := reportCrossrefs(os.Stderr, refsRoot, found, *crossrefs)

	// The admission tier names its members in full on every run rather than
	// counting them behind a flag, because the defect it repairs is a condition
	// being invisible in every place at once. A count would relocate that
	// invisibility instead of removing it.
	reportUnpublished(os.Stderr, ids, covered)

	// The coverage refusal comes after the write above, and the ordering is
	// load-bearing rather than incidental. The regions are what a missing entry
	// is written AGAINST: the short-code row a reader arrives by is rendered
	// there, and so is the sentence the entry has to quote. Refusing before the
	// write would leave the page without the row for the very condition the
	// refusal asks to be documented, so the repair would need a run the refusal
	// itself prevents. Splicing is idempotent and touches only the marked
	// regions, so a refused run leaves the tree exactly where a passing one
	// would, minus the entry that is missing.
	//
	// The figure is repeated here rather than left on the tier's own line above,
	// because the remaining tiers report between the two, and a verdict reading
	// only as "something was refused" sends the reader back up through them.
	if uncovered > 0 {
		fail(fmt.Errorf("%d of the %d declared halting conditions have no entry on %s; "+
			"every declared halting condition earns one, so an undocumented condition is a hole in the "+
			"published reference rather than a note about it — write its entry and declare what that entry "+
			"covers, or re-run with -gaps to name the conditions", uncovered, considered, haltsRelPath))
	}

	// The wording refusal is separate from the coverage one above and is reached
	// only once that has passed, because the two are repaired differently and a
	// run reporting both at once tells a reader to write an entry for a condition
	// whose entry is the very thing quoting the wrong text.
	if unquoted > 0 {
		fail(fmt.Errorf("%d of the %d entry claims on %s do not quote the message their condition registers; "+
			"an entry teaches a reader which build output it explains, so one quoting text no build prints "+
			"sends them looking for a line that does not exist — quote the registered sentence in the entry's "+
			"emitted-message block, or, where no emitted line contains it contiguously, name it as the "+
			"condition the entry is recorded under, or re-run with -claims to name the entries",
			unquoted, claimsCompared, haltsRelPath))
	}

	if unattributed > 0 {
		fail(fmt.Errorf("%d of the %d reproductions of emitted output under %s carry no text any declared "+
			"condition registers; a reproduction is read as a transcript of what a build prints, so one "+
			"carrying text the module never registers sends a reader searching their own output for a line "+
			"that does not exist — correct the reproduction against what the build now prints, or re-run "+
			"with -transcripts to name them", unattributed, read, errorsRelDir))
	}

	if unresolved > 0 {
		fail(fmt.Errorf("%d of the %d prose references naming a section of %s by title resolve to no heading "+
			"there; such a reference is prose rather than a link, so nothing else in the build sees it and a "+
			"renamed section leaves it quoting a title that no longer exists — correct the title, or re-run "+
			"with -crossrefs to name the references", unresolved, resolved, errorsRelDir))
	}
}

// rejectCoverageDisagreement refuses when the two derivations of the
// documented-condition set do not name the same conditions.
//
// One side resolves the page's coverage declarations to the identities they
// name; the other renders a row per condition the page documents. Both read the
// same declarations and both credit a re-export to the identity it forwards to,
// so over any page they BOTH accept they describe the same set — and that makes
// their agreement checkable rather than merely intended. Either may refuse a
// page the other would accept, which is why this runs only once both have
// succeeded.
//
// 🔴 Do not "simplify" this by having one derivation call the other, or by
// lifting the shared step into a helper both use. The comparison below is the
// only thing that would report a divergence, and a single implementation cannot
// diverge from itself: unifying them deletes the check while leaving it looking
// like it is still there. The two are a deliberate near-duplicate for exactly
// this reason.
//
// Both directions are reported, and named rather than counted. Two sets of equal
// size can still name different conditions, so a size comparison would pass a
// swap; and a refusal that says only that two numbers differ leaves whoever has
// to repair the page with nowhere to start.
func rejectCoverageDisagreement(covered map[string]bool, documented []Identity) error {
	inRegion := make(map[string]bool, len(documented))
	for _, id := range documented {
		inRegion[id.Handle()] = true
	}

	var missingFromRegion, missingFromCoverage []string
	for handle := range covered {
		if !inRegion[handle] {
			missingFromRegion = append(missingFromRegion, handle)
		}
	}
	for handle := range inRegion {
		if !covered[handle] {
			missingFromCoverage = append(missingFromCoverage, handle)
		}
	}
	if len(missingFromRegion) == 0 && len(missingFromCoverage) == 0 {
		return nil
	}

	// Sorted, because the two walks above are over maps and a refusal whose
	// wording moves between runs cannot be compared against a previous one.
	var b strings.Builder
	fmt.Fprintf(&b, "the conditions %s documents and the conditions its generated region carries disagree",
		haltsRelPath)
	if len(missingFromRegion) > 0 {
		sort.Strings(missingFromRegion)
		fmt.Fprintf(&b, "\n  documented by an entry and carried by no row: %s", strings.Join(missingFromRegion, ", "))
	}
	if len(missingFromCoverage) > 0 {
		sort.Strings(missingFromCoverage)
		fmt.Fprintf(&b, "\n  carried by a row and documented by no entry: %s", strings.Join(missingFromCoverage, ", "))
	}
	b.WriteString("\n  the region is the lookup from a code to the entry that teaches the authoring fix, so a " +
		"condition on one side and not the other resolves to nothing while reading as a resolved correspondence")
	return fmt.Errorf("%s", b.String())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "errindex:", err)
	os.Exit(1)
}
