// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command errindex reads the halting-condition identities this module declares
// and serves them to the two consumers that need them.
//
// With -out it refreshes the generated regions of two published pages, so their
// rows are a function of the declarations rather than a transcription of them:
// the sentinel reference, which resolves any code a build prints, and the
// halting-conditions page, whose region resolves a code to the entry that teaches
// the authoring fix. With -index it prints the identity set as tab-separated
// records for a consumer outside this module.
//
// Run it as:
//
//	go run ./internal/tools/errindex -out ../aggrega-docs
//	go run ./internal/tools/errindex -out ../aggrega-docs -gaps
//	go run ./internal/tools/errindex -out ../aggrega-docs -claims
//	go run ./internal/tools/errindex -index
//
// The module's `make docs` target invokes the first form. It writes only inside
// the regions its markers delimit; every other byte of either page is curated
// prose and survives untouched.
//
// Every -out run reports two sizes, so a caller can state what its verdict
// covered instead of leaving the reader of a reassuring line to guess whether the
// whole set was behind it or a fragment of one: the identity set it read, and the
// smaller set the halting-conditions page documents, which is what that page's
// region carries.
//
// Every -out run also reports two counts over the halting-conditions page: how
// many declared halting conditions no entry names, and how many entries do not
// quote the message the condition they cover registers. The first asks whether a
// condition is documented at all; the second asks whether what its entry says
// matches what the build actually reports, which is the sentence a reader acts
// on.
//
// 🔴 The second count is not made redundant by that page's generated region, and
// the reason is structural rather than a matter of degree. A splice writes only
// inside its markers, so the curated prose around them is an input to BOTH sides
// of any regenerate-and-compare check and can never produce a difference however
// wrong it is. The region single-sources the sentence it carries; it says nothing
// about the emitted-message forms the entries quote around it, which are
// occurrence-shaped and are not a transcription of any declaration. So the count
// keeps a subject the region cannot reach.
//
// Both are warn tiers: neither changes the exit code, because not every
// condition needs a user-facing entry and an entry legitimately reframes the text
// a condition registers. Add -gaps or -claims to name the members of either
// count — the form to reach for when a count has moved and you want to know what
// moved it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

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
const (
	regionID          = "error-identities"
	shortCodeRegionID = "condition-short-codes"
	haltRegionID      = "halt-condition-sentences"
)

// pageRelPath is the sentinel reference, relative to the documentation tree root.
const pageRelPath = "language-reference/errors/sentinels.md"

func main() {
	root := flag.String("root", ".", "path to the module root to read declarations from")
	out := flag.String("out", "", "path to the documentation tree whose generated error-reference regions to refresh")
	index := flag.Bool("index", false, "print the identity index as tab-separated records and write nothing")
	gaps := flag.Bool("gaps", false, "name every declared halting condition no entry on the halting-conditions page covers")
	claims := flag.Bool("claims", false, "name every entry on the halting-conditions page whose prose does not quote the message the condition it covers registers")
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

	region, err := renderRegion(ids)
	if err != nil {
		fail(err)
	}
	shortCodeRegion, err := renderShortCodeRegion(ids)
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
	haltRegion, err := renderHaltRegion(ids, entries)
	if err != nil {
		fail(err)
	}

	page := filepath.Join(*out, pageRelPath)
	haltPage := filepath.Join(*out, haltsRelPath)

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

	// The halting-conditions region's size is reported separately and for the
	// same reason, because it is a different set: the conditions THAT page
	// documents, which is a subset of the declared ones. One figure covering both
	// would leave a reader of either verdict unable to say what it covered.
	fmt.Println("documented conditions", len(covered))

	// Every page is named, whether it moved or not. A run that writes one page
	// and leaves the other current is the common case, and a single line reporting
	// one of them reads as a run that only ever addressed one.
	for _, p := range []string{page, haltPage} {
		if slices.Contains(updated, p) {
			fmt.Println("updated", p)
			continue
		}
		fmt.Println("unchanged", p)
	}

	// Both warn tiers report and neither fails, so they go to stderr and leave
	// the exit code alone: a condition with no user-facing entry, and an entry
	// that reframes the text its condition registers, are notes for whoever
	// maintains that page rather than reasons to refuse the build.
	reportGaps(os.Stderr, ids, covered, *gaps)
	reportClaims(os.Stderr, ids, entries, *claims)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "errindex:", err)
	os.Exit(1)
}
