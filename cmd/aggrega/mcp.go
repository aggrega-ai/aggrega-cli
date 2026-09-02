// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"

	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server over stdio for AI-assistant integration",
		Long: `Run Aggrega as a Model Context Protocol (MCP) server over stdio, so an
AI assistant can discover and drive Aggrega operations through a
structured protocol instead of scraping command output.

The server speaks newline-delimited JSON-RPC 2.0 on stdin/stdout,
targeting MCP protocol revision 2025-06-18. It is normally spawned as a
subprocess by an MCP-capable client, not run by hand; the client opens
the connection with an initialize handshake, and the server replies with
its protocol revision and the capabilities it advertises — tools,
resources, and prompts.

Once initialized, the client discovers the server's surface through the
list methods (tools, resources, resource templates, and prompts) and
invokes it through tools/call and resources/read. The server exposes
Aggrega operations — compiling and inspecting a Release, reading catalog
entities — as tools and resources an assistant can call directly.

Stream discipline is strict: stdout carries only protocol messages, one
JSON object per line, so the client can parse the stream without
heuristics. Every log, warning, and diagnostic is written to stderr
instead. The server reads one message per line and exits cleanly when
stdin reaches end of stream, so a client that closes the connection shuts
the server down.`,
		Args: cobra.NoArgs,
		RunE: runMCP,
	}
}

func runMCP(cmd *cobra.Command, args []string) error {
	return mcp.Run(mcp.Config{
		Stdin:         cmd.InOrStdin(),
		Stdout:        cmd.OutOrStdout(),
		Stderr:        cmd.ErrOrStderr(),
		ServerVersion: provenanceCompilerVersion(),
		Commands:      commandSurface(cmd.Root()),
	})
}

// commandSurface projects the live command tree into the surface the MCP server
// derives its long-tail command tools from.
//
// It runs here because this is the only package that can see both the command
// tree and the server. The projection itself is the shared command catalog, so
// no command is restated: a command reaches the tool surface by existing in the
// tree. What this adds is the one fact the catalog deliberately does not record
// — whether a command executes an operation — read from the tree rather than
// inferred from a descriptor, so a grouping command that later gains an
// operation of its own reaches the tool surface with no change here.
func commandSurface(root *cobra.Command) []mcp.CommandSurface {
	if root == nil {
		return nil
	}
	runnable := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		runnable[c.CommandPath()] = c.Runnable()
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	cat := clisurface.Catalog(root)
	out := make([]mcp.CommandSurface, 0, len(cat))
	for _, d := range cat {
		out = append(out, mcp.CommandSurface{Descriptor: d, Runnable: runnable[d.Path]})
	}
	return out
}
