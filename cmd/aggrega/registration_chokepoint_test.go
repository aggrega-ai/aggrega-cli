// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registrationBypass is the call that attaches a command to a tree without going
// through the registrar. It is a third-party method, so it cannot be made
// unreachable the way an unexported constructor can: what keeps it out of this
// package is this check.
const registrationBypass = "AddCommand("

// TestNoCommandReachesTheTreeOutsideTheRegistrar is the negative half of the
// registration binding: the registrar refuses a command the operation table does
// not account for, and that refusal is worth nothing while a second path exists
// that never consults the table.
//
// It scans this package's own sources rather than the built tree, because the
// property is about how a command may be attached rather than about which
// commands are attached today. A bypass added tomorrow would build, run and pass
// every behavioural test in this package; the only thing that sees it is a
// reading of the source.
//
// Test files are excluded. They build synthetic trees to exercise tree-shaped
// behaviour — a two-level parent and leaf for an error-rendering check, say —
// and those are fixtures rather than the product's command surface. Gating them
// would force a fixture to name an operation it is not.
//
// The scan is needled below, because a scanner that matched nothing would report
// exactly what a clean package reports.
func TestNoCommandReachesTheTreeOutsideTheRegistrar(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing this package's sources: %v", err)
	}

	scanned := 0
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("reading %s: %v", path, readErr)
		}
		scanned++

		for i, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, registrationBypass) {
				continue
			}
			t.Errorf("%s:%d attaches a command outside the registrar:\n\t%s\n"+
				"Every capability reaches the tree through mustAttach with the operation it carries, so a "+
				"command the operation table does not account for cannot be registered. Use mustAttach, "+
				"mustAttachGroup for a command that only holds subcommands, or mustAttachContributed for a "+
				"command a configured plugin supplies.",
				path, i+1, strings.TrimSpace(line))
		}
	}

	if scanned == 0 {
		t.Fatal("the scan read no source file, so its silence says nothing about this package")
	}
	t.Logf("scanned %d non-test source file(s) in this package", scanned)
}

// TestTheRegistrarScanWouldSeeABypass is the needle. It plants the pattern the
// scan above looks for and asserts the match fires, so a clean verdict there is
// evidence about the package rather than about a scanner that stopped matching.
//
// The second half is the negative control: an ordinary line must not match, so a
// scanner that reported everything would fail here rather than pass both.
func TestTheRegistrarScanWouldSeeABypass(t *testing.T) {
	planted := "\troot.AddCommand(newBuildCmd())"
	if !strings.Contains(planted, registrationBypass) {
		t.Fatalf("the scan's pattern %q does not match a bypass written the ordinary way: %q",
			registrationBypass, planted)
	}

	ordinary := "\tmustAttach(root, newBuildCmd(), \"build\")"
	if strings.Contains(ordinary, registrationBypass) {
		t.Fatalf("the scan's pattern %q matches a correct registration: %q", registrationBypass, ordinary)
	}
}
