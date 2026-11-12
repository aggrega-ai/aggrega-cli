// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestFlagStateDoesNotLeakAcrossInvocations pins the property BEHAVIOURALLY
// rather than by counting declarations: a declaration count passes the moment
// someone re-adds shared flag storage under a different name, so it certifies
// nothing about what a second invocation actually observes.
//
// The shape matters and is the whole point. Both command trees are constructed
// BEFORE either runs. Binding a flag writes its default into the bound
// variable, so constructing a tree after another has already run would reset
// shared storage and mask exactly the defect under test. With both trees alive
// at once, shared storage is observable: the first invocation's parsed value is
// still in the cell the second invocation reads.
//
// Each row runs one command twice — once with a flag, then without it — and
// asserts the second run reflects the flag's default rather than the first
// run's value. The `version` command is the subject because it is hermetic: it
// reads no workspace, touches no network, and writes nothing to disk.
func TestFlagStateDoesNotLeakAcrossInvocations(t *testing.T) {
	cases := []struct {
		name string
		// flagged is the first invocation, which sets the flag.
		flagged []string
		// bare is the second invocation, which does not.
		bare []string
		// wantInFlagged must appear in the first run's output, so a row whose
		// fixture stopped exercising the flag fails loudly instead of passing
		// vacuously.
		wantInFlagged string
		// wantNotInBare is the leak: the second run must not carry it.
		wantNotInBare string
	}{
		{
			name:          "output format does not carry into the next invocation",
			flagged:       []string{"version", "-o", "json"},
			bare:          []string{"version"},
			wantInFlagged: `"abiVersion"`,
			wantNotInBare: `"abiVersion"`,
		},
		{
			name:          "verbose does not carry into the next invocation",
			flagged:       []string{"version", "--verbose"},
			bare:          []string{"version"},
			wantInFlagged: "Embedded plugins (",
			wantNotInBare: "Embedded plugins (",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both trees constructed up front — see the doc comment above.
			first := newRootCmd()
			second := newRootCmd()

			flaggedOut := runCommandTree(t, first, tc.flagged)
			if !strings.Contains(flaggedOut, tc.wantInFlagged) {
				t.Fatalf("the flagged run does not show the flag taking effect, so this row proves nothing about leakage.\nwant to contain: %s\ngot:\n%s",
					tc.wantInFlagged, flaggedOut)
			}

			bareOut := runCommandTree(t, second, tc.bare)
			if strings.Contains(bareOut, tc.wantNotInBare) {
				t.Errorf("the second invocation observed the first invocation's flag value.\nmust not contain: %s\ngot:\n%s",
					tc.wantNotInBare, bareOut)
			}
		})
	}
}

// runCommandTree executes one already-constructed command tree with the given
// arguments and returns everything it wrote. It deliberately takes the tree
// rather than building one, because when the tree is built is the property
// under test.
func runCommandTree(t *testing.T, root *cobra.Command, args []string) string {
	t.Helper()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v\noutput:\n%s", args, err, buf.String())
	}
	return buf.String()
}
