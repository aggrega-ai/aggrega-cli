// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestEveryCatalogFlagShowsADirectoryPlaceholder walks every command the root
// reaches and holds each --catalog flag's help line to what a reader types: the
// argument shows as <dir>, and the sentence carries no back-quote — the help
// listing reads a flag's first back-quoted word as its argument's name and prints
// any other with its back-quotes. The walk finds the commands rather than naming
// them, so a command that gains the flag is held to it too; finding none fails,
// since a walk that met no flag proves nothing.
//
// 🔴 PLANTED DEFECT: in helpCatalogDir (env.go), write `catalog` back-quoted in
// place of `<dir>`: every row prints `--catalog catalog` and the case fails.
func TestEveryCatalogFlagShowsADirectoryPlaceholder(t *testing.T) {
	t.Parallel()
	carrying := 0
	for _, c := range reachableCommands(newRootCmd()) {
		f := c.Flags().Lookup("catalog")
		if f == nil {
			continue
		}
		carrying++
		name, usage := pflag.UnquoteUsage(f)
		if name != "<dir>" || strings.Contains(usage, "`") {
			t.Errorf("%s: --catalog shows the argument %q with the help %q; want <dir> and no back-quote",
				c.CommandPath(), name, usage)
		}
		if line := c.Flags().FlagUsages(); !strings.Contains(line, "--catalog <dir>") {
			t.Errorf("%s: the help listing does not show --catalog <dir>:\n%s", c.CommandPath(), line)
		}
	}
	if carrying == 0 {
		t.Fatal("no reachable command carries --catalog, so the walk held nothing")
	}
}
