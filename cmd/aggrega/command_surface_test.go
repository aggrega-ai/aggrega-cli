// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"reflect"
	"sort"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
)

// reachableCommands collects every command reachable from root using a
// breadth-first walk written independently of the catalog's own depth-first
// projection, so a bug in one cannot hide in the other.
//
// This is the one walk of the tree the tests in this package share. A second copy
// would agree with it today and that agreement is precisely what would hide the
// divergence later, so a test needing the tree asks here and projects whatever it
// needs from the commands returned.
func reachableCommands(root *cobra.Command) []*cobra.Command {
	var all []*cobra.Command
	queue := []*cobra.Command{root}
	for len(queue) > 0 {
		cmd := queue[0]
		queue = queue[1:]
		all = append(all, cmd)
		queue = append(queue, cmd.Commands()...)
	}
	return all
}

// reachableCommandPaths projects the command paths out of the shared walk.
func reachableCommandPaths(root *cobra.Command) []string {
	cmds := reachableCommands(root)
	paths := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		paths = append(paths, cmd.CommandPath())
	}
	return paths
}

func descriptorPaths(cat []clisurface.Descriptor) []string {
	paths := make([]string, 0, len(cat))
	for _, d := range cat {
		paths = append(paths, d.Path)
	}
	return paths
}

// TestCommandCatalogCoversTheLiveTree is the guard that a command cannot escape
// the projection: it compares the catalog against an independent walk of the
// same tree instance by path in both directions, so a command added to the tree
// without reaching the catalog fails here. Paths are compared rather than counts
// alone, because a count that happens to match while a path differs is not a
// pass. A configured CLI plugin command is registered from a fixture so the
// dynamic half of the tree is covered too, and the tree is built once and shared
// by both walks so the fixture cannot make them disagree.
func TestCommandCatalogCoversTheLiveTree(t *testing.T) {
	withCLIRegistrations(t, []cliCommandReg{{
		name:   "fixture-plugin-cmd",
		short:  "a configured CLI plugin command",
		uri:    "oci://example.invalid/plugin:v1",
		digest: "sha256:0",
	}})

	root := newRootCmd()

	want := reachableCommandPaths(root)
	got := descriptorPaths(clisurface.Catalog(root))

	sortedWant := append([]string(nil), want...)
	sortedGot := append([]string(nil), got...)
	sort.Strings(sortedWant)
	sort.Strings(sortedGot)

	if !reflect.DeepEqual(sortedWant, sortedGot) {
		t.Fatalf("the catalog does not cover the live command tree:\n reachable %v\n catalog   %v", sortedWant, sortedGot)
	}
	if len(got) != len(want) {
		t.Errorf("the catalog has %d entries for %d reachable commands", len(got), len(want))
	}
	if len(got) < 2 {
		t.Fatalf("expected the root and its subcommands, got %v", got)
	}
	if got[0] != "aggrega" {
		t.Errorf("expected the root first, got %q", got[0])
	}

	for _, path := range []string{
		"aggrega build",
		"aggrega auth token",         // a command nested two levels deep
		"aggrega fixture-plugin-cmd", // registered dynamically, not statically
	} {
		if !containsCommandPath(got, path) {
			t.Errorf("expected %q in the catalog, got %v", path, got)
		}
	}
}

// TestCommandCatalogRecordsTheRootGlobalFlags pins that the flags every command
// inherits are projected as the root's persistent flags, which is the view the
// generated global-flags reference is rendered from.
func TestCommandCatalogRecordsTheRootGlobalFlags(t *testing.T) {
	withCLIRegistrations(t, nil)

	cat := clisurface.Catalog(newRootCmd())
	if len(cat) == 0 {
		t.Fatal("empty catalog")
	}
	rootFlags := cat[0].PersistentFlags

	for _, name := range []string{"workspace-root", "config", "no-color", "plain"} {
		if !containsFlagNamed(rootFlags, name) {
			t.Errorf("expected the root persistent flag %q, got %+v", name, rootFlags)
		}
	}

	// A subcommand sees those as inherited, never as its own.
	var sub clisurface.Descriptor
	for _, d := range cat {
		if d.Path == "aggrega build" {
			sub = d
		}
	}
	if sub.Path == "" {
		t.Fatal("no descriptor for the build command")
	}
	if !containsFlagNamed(sub.InheritedFlags, "workspace-root") {
		t.Errorf("expected workspace-root inherited by build, got %+v", sub.InheritedFlags)
	}
	if containsFlagNamed(sub.LocalFlags, "workspace-root") {
		t.Errorf("a root persistent flag leaked into build's local flags: %+v", sub.LocalFlags)
	}
}

func containsCommandPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func containsFlagNamed(flags []clisurface.Flag, name string) bool {
	for _, f := range flags {
		if f.Name == name {
			return true
		}
	}
	return false
}
