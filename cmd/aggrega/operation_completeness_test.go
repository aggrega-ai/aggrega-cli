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
// The availability walk in the projection package asks whether every known
// capability was ruled on, and structurally cannot notice a capability nobody
// added to the operation set: a new command reaches no operation, so no
// availability record is missing and nothing there fails. Asked from the tree's
// end, the new command is visible. The inverse direction is covered too — a row
// naming a command path the tree no longer carries.
//
// Registering from the table closes most of this on its own: a command reaches
// the tree only through the registrar, which refuses a path the table does not
// give. What it does not close is the surface members the registrar never
// mints — a command the command framework creates for itself when a tree runs,
// and the commands a configured plugin contributes — which is why the walk from
// the tree's end still runs.
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

	if findings := opsurface.CheckMembers(opsurface.SurfaceCLI, clisurface.OperationAvailability(), members); len(findings) != 0 {
		t.Fatalf("the command tree and the operation set disagree over %d of %d commands:\n  %s",
			len(findings), len(members), strings.Join(findings, "\n  "))
	}
}

// TestEveryCommandInTheTreeDeclaresItsWorkspaceScope walks the live command tree
// and fails, naming the command, for every runnable command that declares no
// workspace scope: the declaration is what decides whether the command warns
// that the folder it runs in is no workspace (workspaceScopedCommand), so a
// command with no row would never warn whatever its result depends on.
//
// Hidden commands are included, because a hidden command runs and resolves its
// workspace like any other. Grouping commands are excluded because they run
// nothing, and commands a configured plugin contributes by construction: this
// test registers none.
//
// 🔴 PLANTED DEFECT: delete any row from the declared workspace scopes and this
// test fails naming that command.
func TestEveryCommandInTheTreeDeclaresItsWorkspaceScope(t *testing.T) {
	withCLIRegistrations(t, nil)

	var missing []string
	walked := 0
	for _, c := range reachableCommands(newRootCmd()) {
		if !c.Runnable() {
			continue
		}
		walked++
		if _, declared := clisurface.CommandWorkspaceScope(c.CommandPath()); !declared {
			missing = append(missing, c.CommandPath())
		}
	}
	if walked == 0 {
		t.Fatal("no runnable commands in the tree")
	}
	if len(missing) != 0 {
		t.Fatalf("%d of %d runnable commands declare no workspace scope:\n  %s",
			len(missing), walked, strings.Join(missing, "\n  "))
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

	if findings := opsurface.CheckMembers(opsurface.SurfaceMCP, mcp.OperationAvailability(), members); len(findings) != 0 {
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

	mcpTable := mcp.OperationAvailability()
	for id, cli := range clisurface.OperationAvailability() {
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
