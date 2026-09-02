// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestEveryCommandBelongsToAnOperation is the command line's growth half of the
// operation-completeness guard, and it runs here because this is the only
// package that can build the command tree the check reads.
//
// The disposition walk in the projection package asks whether every known
// capability was ruled on, and structurally cannot notice a capability nobody
// added to the operation set: a new command reaches no operation, so no
// disposition is missing and nothing there fails. Asked from the tree's end,
// the new command is visible. The inverse direction is covered too — a
// disposition naming a command path the tree no longer carries.
//
// Grouping commands are excluded: a command that only holds subcommands
// executes nothing and is therefore no capability. Hidden commands are excluded
// for the same reason the generated tool surface skips them — they are not part
// of the documented surface. Commands contributed by a configured plugin are
// excluded by construction: this test registers none, and a plugin's command is
// the plugin's capability rather than the compiler's.
func TestEveryCommandBelongsToAnOperation(t *testing.T) {
	withCLIRegistrations(t, nil)

	root := newRootCmd()
	runnable := map[string]bool{}
	hidden := map[string]bool{}
	for _, c := range reachableCommands(root) {
		runnable[c.CommandPath()] = c.Runnable()
		hidden[c.CommandPath()] = c.Hidden
	}

	var members []string
	for _, d := range clisurface.Catalog(root) {
		if !runnable[d.Path] || hidden[d.Path] {
			continue
		}
		members = append(members, d.Path)
	}
	if len(members) == 0 {
		t.Fatal("no runnable commands in the tree")
	}

	if findings := opsurface.CheckMembers(opsurface.SurfaceCLI, clisurface.Dispositions(), members); len(findings) != 0 {
		t.Fatalf("the command tree and the operation set disagree over %d of %d commands:\n  %s",
			len(findings), len(members), strings.Join(findings, "\n  "))
	}
}

// TestEveryToolBelongsToAnOperation is the tool protocol's growth half, and it
// runs here for the same reason: the generated command tools exist only for a
// session handed a command surface, and this is the only package that can build
// one from the live tree.
//
// The member set is the whole tool surface a session serves — the curated set,
// the statically declared long tail, and the tools derived from the command
// tree — so a tool reachable by name and claimed by no operation is named here.
func TestEveryToolBelongsToAnOperation(t *testing.T) {
	withCLIRegistrations(t, nil)

	root := newRootCmd()
	members := append(mcp.StaticToolNames(), mcp.GeneratedToolNames(commandSurface(root))...)
	if len(members) == 0 {
		t.Fatal("no tools on the surface")
	}

	if findings := opsurface.CheckMembers(opsurface.SurfaceMCP, mcp.Dispositions(), members); len(findings) != 0 {
		t.Fatalf("the tool surface and the operation set disagree over %d of %d tools:\n  %s",
			len(findings), len(members), strings.Join(findings, "\n  "))
	}
}

// TestEveryGeneratedToolTracksItsCommand pins the derivation across the two
// surfaces at once: for every command the operation set records a path for, the
// tool the protocol claims is the one the derivation actually produces from that
// path.
//
// This is the guard on the anti-drift property the generated surface exists
// for. Hand-following a renamed command onto the tool surface fixes one
// occurrence; this fails the moment the two stop being computed from the same
// path.
func TestEveryGeneratedToolTracksItsCommand(t *testing.T) {
	withCLIRegistrations(t, nil)

	root := newRootCmd()
	generated := map[string]bool{}
	for _, name := range mcp.GeneratedToolNames(commandSurface(root)) {
		generated[name] = true
	}

	mcpTable := mcp.Dispositions()
	for id, cli := range clisurface.Dispositions() {
		if !cli.IsExposed() {
			continue
		}
		for _, path := range cli.Names {
			if _, exempt := mcp.CommandExempted(path); exempt {
				continue
			}
			var found bool
			for _, name := range mcpTable[id].Names {
				if generated[name] {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("the %q operation is a command at %q, but none of the tool names it claims (%v) "+
					"is a tool the derivation produces from that path", id, path, mcpTable[id].Names)
			}
		}
	}
}
