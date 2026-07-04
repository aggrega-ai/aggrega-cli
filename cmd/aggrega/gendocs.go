// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build clidocs

// This file provides an alternate program entry point, selected by the
// `clidocs` build tag, that regenerates the CLI reference documentation from
// the live cobra command tree. It is excluded from the normal build, so the
// shipped aggrega binary never depends on the documentation tree: the
// generator only writes markdown to a directory passed on the command line.
//
// Run it with:
//
//	go run -tags clidocs ./cmd/aggrega -out ../aggrega-docs
//
// which the module's `make docs` target and the `go:generate` directive both
// invoke. Living in package main lets the generator reach the unexported
// command-tree constructor directly without exporting it or duplicating the
// tree in a separate package.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/clidocs"
)

func main() {
	out := flag.String("out", "../aggrega-docs", "path to the aggrega-docs documentation tree")
	flag.Parse()

	res, err := clidocs.Generate(newRootCmd(), *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate cli docs:", err)
		os.Exit(1)
	}

	for _, p := range res.Written {
		fmt.Println("updated", p)
	}
	fmt.Printf("cli docs: %d updated, %d unchanged\n", len(res.Written), len(res.Unchanged))
}
