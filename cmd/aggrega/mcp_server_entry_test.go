// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/skills"
)

// desktopBundleManifest is the desktop extension bundle's manifest, as this
// package's folder reaches it: two folders up is the module root. It is inside
// the module, so the test cache tracks it.
var desktopBundleManifest = filepath.Join("..", "..", "extensions", "mcpb", "manifest.json")

// TestTheMCPServerEntryStartsTheMCPServer holds the command every plugin
// manifest writes for the MCP server to the command tree this binary runs:
// the executable's name is the root command's, and its arguments, resolved
// through the root command, land on the command that serves the protocol. It
// holds the desktop bundle's manifest to the same command, since that bundle
// launches the server with arguments of its own.
//
// 🔴 PLANTED DEFECT: change the MCP server command's Use, or the argument the
// skills package's server entry writes, and the resolution half reddens naming
// the command it found; change the bundle manifest's first argument, and the
// bundle half reddens naming it.
func TestTheMCPServerEntryStartsTheMCPServer(t *testing.T) {
	name, args := skills.MCPServerCommand()
	if len(args) == 0 {
		t.Fatal("the server entry names no arguments, so it selects no command and the checks below prove nothing")
	}
	want := newMCPCmd()

	t.Run("the arguments resolve to the MCP server command", func(t *testing.T) {
		root := newRootCmd()
		if root.Name() != name {
			t.Errorf("the server entry starts %q, and the command tree's root is %q", name, root.Name())
		}
		found, rest, err := root.Find(args)
		if err != nil {
			t.Fatalf("the root command does not resolve the server entry's arguments %q: %v", args, err)
		}
		if len(rest) != 0 {
			t.Errorf("resolving %q leaves the arguments %q to the command it found, which selects no further", args, rest)
		}
		if found == root || found.Name() != want.Name() || found.Use != want.Use || found.Parent() != root {
			t.Errorf("the server entry's arguments %q resolve to %q, want the MCP server command %q",
				args, found.CommandPath(), root.Name()+" "+want.Name())
		}
	})

	t.Run("the desktop bundle launches the same command", func(t *testing.T) {
		raw, err := os.ReadFile(desktopBundleManifest)
		if err != nil {
			t.Fatalf("read the desktop bundle's manifest at %s: %v", desktopBundleManifest, err)
		}
		var manifest struct {
			Server struct {
				MCPConfig struct {
					Args []string `json:"args"`
				} `json:"mcp_config"`
			} `json:"server"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("the desktop bundle's manifest at %s does not decode: %v", desktopBundleManifest, err)
		}
		got := manifest.Server.MCPConfig.Args
		if len(got) < len(args) || strings.Join(got[:len(args)], " ") != strings.Join(args, " ") {
			t.Errorf("the desktop bundle's manifest at %s launches the server with %q, which does not start with "+
				"the server entry's arguments %q", desktopBundleManifest, got, args)
		}
	})
}
