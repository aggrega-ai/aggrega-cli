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
//	go run ./internal/tools/errindex -index
//
// The module's `make docs` target invokes the first form. It writes only inside
// the region its markers delimit; every other byte of the page is curated prose
// and survives untouched.
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
		return
	}
	fmt.Println("unchanged", page)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "errindex:", err)
	os.Exit(1)
}
