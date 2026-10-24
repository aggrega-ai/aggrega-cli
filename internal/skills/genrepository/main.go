// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command genrepository writes the tree of the public skills repository from
// the skills built into the aggrega binary.
//
// Run it with:
//
//	go run ./internal/skills/genrepository -out <folder>
//
// The skills package's go:generate directive runs it over the copy committed
// beside that package; pointed at a checkout of the public repository, it
// brings that checkout to the same tree. The folder ends up holding exactly the
// generated tree: a file the tree no longer has is removed.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/skills"
)

func main() {
	out := flag.String("out", "", "folder to write the repository tree into")
	flag.Parse()
	if *out == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: genrepository -out <folder>")
		os.Exit(2)
	}
	if err := skills.WriteRepository(*out); err != nil {
		fmt.Fprintln(os.Stderr, "genrepository:", err)
		os.Exit(1)
	}
}
