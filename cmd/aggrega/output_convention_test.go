// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
)

// outputFlagViolations reports every breach of the structured-output naming
// convention in cat, one message per breach, each naming the command and the
// flag so a failure points at the declaration to fix.
//
// The convention is that one concept carries one name: a flag that selects an
// output format is spelled --output with the shorthand -o everywhere, so -o means
// the same thing on every command and no command invents a second spelling for
// it. The four rules below are the mechanical half of that; they exist because
// the convention is otherwise upheld by nothing, and the previous divergence
// (one command taking --json while another took -o json, and a third taking -o
// for a directory) arose exactly where nothing was watching.
//
// The rules are deliberately about flag *names and shorthands* only. Which format
// values a command accepts is that command's own business — text|yaml and
// table|json are both legitimate — so nothing here constrains them.
func outputFlagViolations(cat []clisurface.Descriptor) []string {
	var out []string
	note := func(path string, format string, args ...any) {
		out = append(out, path+": "+fmt.Sprintf(format, args...))
	}
	for _, d := range cat {
		for _, f := range d.LocalFlags {
			if f.Name == "json" && f.Type == "bool" {
				note(d.Path, "declares a boolean --json flag; a format is selected with -o/--output instead")
			}
			if f.Name == "output" && f.Shorthand != "o" {
				note(d.Path, "declares --output with shorthand %q; the shorthand is -o", f.Shorthand)
			}
			if f.Shorthand == "o" && f.Name != "output" {
				note(d.Path, "gives --%s the shorthand -o, which is reserved for --output", f.Name)
			}
			if f.Name == "output-dir" && f.Shorthand != "" {
				note(d.Path, "declares --output-dir with shorthand %q; it carries none, so -o stays unambiguous", f.Shorthand)
			}
		}
	}
	return out
}

// TestOutputFlagConventionHoldsAcrossTheCommandTree is the guard proper: it walks
// the projection of the live command tree and requires zero breaches. A command
// added later that spells its structured-output flag differently fails here
// rather than shipping a second vocabulary for the same concept.
//
// A configured CLI plugin command is registered from a fixture so the dynamic
// half of the tree is walked too, not only the statically registered commands.
func TestOutputFlagConventionHoldsAcrossTheCommandTree(t *testing.T) {
	withCLIRegistrations(t, []cliCommandReg{{
		name:   "fixture-plugin-cmd",
		short:  "a configured CLI plugin command",
		uri:    "oci://example.invalid/plugin:v1",
		digest: "sha256:0",
	}})

	cat := clisurface.Catalog(newRootCmd())
	if len(cat) < 2 {
		t.Fatalf("expected the root and its subcommands, got %d entries", len(cat))
	}
	if v := outputFlagViolations(cat); len(v) != 0 {
		t.Errorf("the structured-output naming convention is breached:\n  %s", strings.Join(v, "\n  "))
	}
}

// TestOutputFlagConventionCoversTheConvergedFlags pins that the guard above is
// running over a corpus that actually contains the flags it is meant to police.
// Without this, the zero it reports would be indistinguishable from a walk that
// reached nothing: every command could have lost its --output flag entirely and
// the guard would still pass.
func TestOutputFlagConventionCoversTheConvergedFlags(t *testing.T) {
	withCLIRegistrations(t, nil)

	cat := clisurface.Catalog(newRootCmd())

	byPath := map[string]clisurface.Descriptor{}
	for _, d := range cat {
		byPath[d.Path] = d
	}

	for _, want := range []struct {
		path      string
		flag      string
		shorthand string
	}{
		{"aggrega search", "output", "o"},
		{"aggrega inspect", "output", "o"},
		{"aggrega version", "output", "o"},
		{"aggrega plugin list", "output", "o"},
		{"aggrega plugin gen-catalog", "output-dir", ""},
		{"aggrega import", "output-dir", ""},
	} {
		d, ok := byPath[want.path]
		if !ok {
			t.Errorf("no descriptor for %q; the walk did not reach it", want.path)
			continue
		}
		var found bool
		for _, f := range d.LocalFlags {
			if f.Name != want.flag {
				continue
			}
			found = true
			if f.Shorthand != want.shorthand {
				t.Errorf("%s --%s shorthand = %q; want %q", want.path, want.flag, f.Shorthand, want.shorthand)
			}
		}
		if !found {
			t.Errorf("%s does not declare --%s", want.path, want.flag)
		}
	}
}

// TestOutputFlagViolationsFiresOnTheLiveTree is the other half of the guard's
// calibration: it plants each breach on a real command in a real tree and
// requires the guard to report it, naming that command. Planting on the live tree
// rather than a synthetic one is the point — a checker that fires on a hand-built
// fixture still proves nothing about whether its walk reaches the commands the
// binary actually ships.
func TestOutputFlagViolationsFiresOnTheLiveTree(t *testing.T) {
	cases := []struct {
		name       string
		targetPath string
		plant      func(*cobra.Command)
		wantIn     string
	}{
		{
			name:       "boolean json flag",
			targetPath: "aggrega version",
			plant: func(c *cobra.Command) {
				var b bool
				c.Flags().BoolVar(&b, "json", false, "planted")
			},
			wantIn: "boolean --json",
		},
		{
			name:       "output without the o shorthand",
			targetPath: "aggrega build",
			plant: func(c *cobra.Command) {
				var s string
				c.Flags().StringVar(&s, "output", "", "planted")
			},
			wantIn: "shorthand",
		},
		{
			name:       "o shorthand on another flag",
			targetPath: "aggrega build",
			plant: func(c *cobra.Command) {
				var s string
				c.Flags().StringVarP(&s, "overlay-planted", "o", "", "planted")
			},
			wantIn: "reserved for --output",
		},
		{
			name:       "output-dir carrying a shorthand",
			targetPath: "aggrega build",
			plant: func(c *cobra.Command) {
				var s string
				c.Flags().StringVarP(&s, "output-dir", "d", "", "planted")
			},
			wantIn: "output-dir",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCLIRegistrations(t, nil)

			root := newRootCmd()

			// The unplanted tree is clean, so any violation below is the plant's.
			if v := outputFlagViolations(clisurface.Catalog(root)); len(v) != 0 {
				t.Fatalf("the tree is not clean before planting: %v", v)
			}

			target := findCommandByPath(root, tc.targetPath)
			if target == nil {
				t.Fatalf("no command at %q to plant on", tc.targetPath)
			}
			tc.plant(target)

			got := outputFlagViolations(clisurface.Catalog(root))
			if len(got) == 0 {
				t.Fatalf("the planted breach was not reported")
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, tc.targetPath+":") {
				t.Errorf("expected the report to name %q; got:\n%s", tc.targetPath, joined)
			}
			if !strings.Contains(joined, tc.wantIn) {
				t.Errorf("expected the report to contain %q; got:\n%s", tc.wantIn, joined)
			}
		})
	}
}

// findCommandByPath returns the command in root's tree whose full invocation path
// is path, or nil when the tree has none.
func findCommandByPath(root *cobra.Command, path string) *cobra.Command {
	if root.CommandPath() == path {
		return root
	}
	for _, sub := range root.Commands() {
		if found := findCommandByPath(sub, path); found != nil {
			return found
		}
	}
	return nil
}
