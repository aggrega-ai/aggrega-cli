// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command annotationmatrix refreshes the published well-known-annotation support
// matrix from the support claim each provider declares for itself.
//
// The matrix states, for every provider and every Recipe input kind, whether a
// platform-native placement annotation is read on that path. That is not
// knowledge the compiler core holds: a provider's engine decides what it reads,
// and the declaration on its catalog entity is the claim. So the matrix is
// projected from those declarations crossed with each provider's own
// render-behavior contract, and a published cell cannot say something no
// provider declared.
//
// Two things make the projection complete for the shipped set rather than merely
// consistent with it. The provider set comes from the declarative shipped-plugin
// manifest rather than from a listing of the declarations, so a provider that
// ships without a support claim still gets its rows and its silence is visible
// on the page. And a shipped provider with no published entity is an error
// rather than an all-declined row.
//
// Run it as:
//
//	go run ./internal/tools/annotationmatrix -catalog ../aggrega-hub -out ../aggrega-docs
//	go run ./internal/tools/annotationmatrix -catalog ../aggrega-hub -list
//
// It writes only inside the regions its markers delimit; every other byte of the
// page is curated prose and survives untouched. The -list form prints what the
// providers declare without touching the page, for reading a support claim at
// its source.
//
// What it cannot see, stated because the bound is part of the design: nothing
// here verifies that a declared key is actually read by the engine on the path
// it claims. The compiler cannot know what a rendering engine reads, so a
// declaration is checked for contradiction against the provider's own
// render-behavior contract and is otherwise taken at its word.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/docsplice"
)

// The generated regions within the published page, by the id their BEGIN/END
// markers carry. They are separate regions rather than one because the curated
// prose that explains why boundary-scoped keys sit outside the grid belongs
// between them.
const (
	nodeScopedRegionID = "well-known-annotation-support"
	boundaryRegionID   = "well-known-annotation-boundary-scope"
)

// pageRelPath is the published page, relative to the documentation tree root.
const pageRelPath = "language-reference/providers/well-known-annotations.md"

func main() {
	root := flag.String("root", ".", "path to the module root to read the shipped-plugin manifest from")
	catalog := flag.String("catalog", "", "path to the catalog tree holding the provider overlays and published entities")
	out := flag.String("out", "", "path to the documentation tree whose support matrix to refresh")
	list := flag.Bool("list", false, "print the assembled claims as tab-separated records and write nothing")
	flag.Parse()

	if *catalog == "" {
		fmt.Fprintln(os.Stderr, "annotationmatrix: -catalog is required")
		os.Exit(2)
	}
	if (*out == "") == !*list {
		fmt.Fprintln(os.Stderr, "annotationmatrix: pass exactly one of -out or -list")
		os.Exit(2)
	}

	providers, err := scan(*root, *catalog)
	if err != nil {
		fail(err)
	}
	if len(providers) == 0 {
		// An empty extraction renders as an empty grid, which reads on the page
		// as "the compiler ships no provider" rather than as a broken read. The
		// manifest always names at least one, so zero means the manifest moved
		// or its shape changed — reported as the instrument failure it is
		// rather than served as a result.
		fail(fmt.Errorf("extracted zero providers from %s; the plugin manifest or its shape moved", *root))
	}

	if *list {
		fmt.Print(renderList(providers))
		return
	}

	nodeScoped, err := renderNodeScopedRegion(providers)
	if err != nil {
		fail(err)
	}
	boundaryScoped, err := renderBoundaryScopedRegion(providers)
	if err != nil {
		fail(err)
	}

	page := filepath.Join(*out, filepath.FromSlash(pageRelPath))
	content, err := os.ReadFile(page)
	if err != nil {
		fail(err)
	}

	// Both regions are spliced before anything is written, and the write is
	// compared against the file as read rather than against the intermediate.
	// Splicing the second region through a write helper that compares against
	// what it was handed would report "unchanged" whenever only the first region
	// moved, and silently drop that first splice on the floor.
	next := string(content)
	for _, region := range []struct{ id, inner string }{
		{nodeScopedRegionID, nodeScoped},
		{boundaryRegionID, boundaryScoped},
	} {
		next, err = docsplice.Splice(next, region.id, region.inner)
		if err != nil {
			fail(err)
		}
	}

	// Skipping an identical write keeps an unchanged page's modification time
	// stable, so a generator run over unchanged declarations leaves the tree
	// exactly as it found it.
	if next == string(content) {
		fmt.Println("unchanged", page)
		return
	}
	if err := os.WriteFile(page, []byte(next), 0o644); err != nil {
		fail(err)
	}
	fmt.Println("updated", page)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "annotationmatrix:", err)
	os.Exit(1)
}
