// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command errindex reads the halting-condition identities this module declares
// and serves them to the two consumers that need them.
//
// With -out it refreshes the generated region of the published sentinel
// reference, so the page's rows are a function of the declarations rather than a
// transcription of them. With -index it prints the identity set as tab-separated
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
// the region its markers delimit; every other byte of the page is curated prose
// and survives untouched.
//
// Every -out run also reports two counts over the halting-conditions page: how
// many declared halting conditions no entry names, and how many entries do not
// quote the message the condition they cover registers. The first asks whether a
// condition is documented at all; the second asks whether what its entry says
// matches what the build actually reports, which is the sentence a reader acts
// on.
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

	"github.com/aggrega-ai/aggrega-cli/internal/docsplice"
)

// regionID names the generated region within the published page. It is the id
// carried by the page's BEGIN/END markers.
const regionID = "error-identities"

// pageRelPath is the published page, relative to the documentation tree root.
const pageRelPath = "language-reference/errors/sentinels.md"

func main() {
	root := flag.String("root", ".", "path to the module root to read declarations from")
	out := flag.String("out", "", "path to the documentation tree whose sentinel reference to refresh")
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

	page := filepath.Join(*out, pageRelPath)
	content, err := os.ReadFile(page)
	if err != nil {
		fail(err)
	}
	written, err := docsplice.WriteFile(page, string(content), regionID, region)
	if err != nil {
		fail(err)
	}
	if written {
		fmt.Println("updated", page)
	} else {
		fmt.Println("unchanged", page)
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
