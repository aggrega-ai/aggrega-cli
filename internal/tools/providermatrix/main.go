// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command providermatrix refreshes the published provider capability grid from
// the render-behavior contract each provider guest declares for itself.
//
// The grid states which of the three Recipe input kinds each provider accepts,
// and whether it honours omit entries. That is not knowledge the compiler core
// holds: both are the provider's own claim, carried in the descriptor its
// describe() export returns, and the core reads that claim rather than
// consulting a table of provider names.
// So the grid is projected from the guests' own declarations — the same
// declarations the running compiler reads — and a published cell cannot say
// something a provider does not claim.
//
// Run it as:
//
//	go run ./internal/tools/providermatrix -out ../aggrega-docs
//	go run ./internal/tools/providermatrix -list
//
// It writes only inside the region its markers delimit; every other byte of the
// page is curated prose and survives untouched. A drift check compares the
// region with a fresh projection and prints this command when it is stale, which
// is the path that catches a declaration changing without a refresh.
//
// The -list form prints what the guests declare without touching the page, for
// reading a capability claim at its source.
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
const regionID = "provider-capability-matrix"

// pageRelPath is the published page, relative to the documentation tree root.
const pageRelPath = "architecture/16-recipe-input-composition.md"

func main() {
	root := flag.String("root", ".", "path to the module root to read provider declarations from")
	out := flag.String("out", "", "path to the documentation tree whose capability grid to refresh")
	list := flag.Bool("list", false, "print the extracted contracts as tab-separated records and write nothing")
	flag.Parse()

	if (*out == "") == !*list {
		fmt.Fprintln(os.Stderr, "providermatrix: pass exactly one of -out or -list")
		os.Exit(2)
	}

	providers, err := scan(*root)
	if err != nil {
		fail(err)
	}
	if len(providers) == 0 {
		// An empty extraction renders as an empty grid, which reads on the page
		// as "no provider accepts anything" rather than as a broken read. The
		// declarative manifest always names at least one provider, so zero means
		// the manifest moved or the descriptor form changed — reported as the
		// instrument failure it is rather than served as a result.
		fail(fmt.Errorf("extracted zero provider contracts from %s; the plugin manifest or the descriptor form moved", *root))
	}

	if *list {
		fmt.Print(renderList(providers))
		return
	}

	region, err := renderRegion(providers)
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
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "providermatrix:", err)
	os.Exit(1)
}
