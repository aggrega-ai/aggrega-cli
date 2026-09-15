// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"sync"

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

The server speaks newline-delimited JSON-RPC 2.0 on stdin/stdout. It is
normally spawned as a subprocess by an MCP-capable client, not run by
hand.

It speaks two MCP protocol revisions, 2026-07-28 and 2025-06-18, so a
client of either connects. In 2026-07-28 nothing is settled for the
connection as a whole: every request states the protocol version it
speaks and the capabilities its sender offers, and the server reads both
from the request it is answering rather than holding anything between
messages. In 2025-06-18 a client settles those same facts once, by
opening the connection with an initialize handshake.

Both revisions reach the same server, and a client learns the same
things about it either way. A client on 2026-07-28 calls server/discover
for the revisions the server speaks, the capabilities it advertises —
tools, resources, and prompts — and the guidance it offers a model
before the first call. A client on 2025-06-18 receives those same
capabilities, and that same guidance, in the reply to its handshake.

From there the client discovers the server's surface through the list
methods (tools, resources, resource templates, and prompts) and invokes
it through tools/call and resources/read. The server exposes Aggrega
operations — compiling and inspecting a Release, reading catalog
entities — as tools and resources an assistant can call directly.

Stream discipline is strict: stdout carries only protocol messages, one
JSON object per line, so the client can parse the stream without
heuristics. Every log, warning, and diagnostic is written to stderr
instead. The server reads one message per line and exits cleanly when
stdin reaches end of stream, so a client that closes the connection shuts
the server down.

Asking the process itself to stop — an interrupt from the terminal, or a
termination request from a supervisor or container runtime — shuts the
server down on the first request, whether or not a client is sending
anything: it stops accepting messages, ends the work a call in progress
is doing, and exits cleanly. A second request ends the process at once.`,
		Args: cobra.NoArgs,
		RunE: runMCP,
	}
}

func runMCP(cmd *cobra.Command, args []string) error {
	return mcp.Run(cmd.Context(), mcp.Config{
		Stdin:         cmd.InOrStdin(),
		Stdout:        cmd.OutOrStdout(),
		Stderr:        cmd.ErrOrStderr(),
		ServerVersion: provenanceCompilerVersion(),
		// Supplied for the same reason the command surface below is: the value
		// belongs to this package and the server lives under it. Both halves of the
		// stamp are read through the derivations this package already owns, so a
		// lockfile a tool call writes records the same compiler and the same core
		// plugin versions a lockfile written at a terminal records for this binary.
		Toolchain: toolchainStamp(),
		Commands:  commandSurface(cmd.Root()),
		// Supplied from here for the same reason, and over the same tree: the
		// help a cli:// read serves is the help this tree prints.
		RenderHelp: commandHelp(cmd.Root()),
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

// helpRenderMu serialises help rendering over one command tree.
//
// None of the steps below is a read, which is the whole reason it is here.
// Resolving a path writes the name each command was reached by onto that
// command; installing the help and version flags merges a command's inherited
// flags into its own set; the writer is installed on the command being rendered;
// and the render itself swaps that writer again while it composes the usage
// block. Requests reach the server on their own goroutines, so two reads
// arriving together would meet every one of those, and the one whose writer was
// swapped out collects only the bytes the other had not already taken.
var helpRenderMu sync.Mutex

// commandHelp renders one command's help from the live tree, for the server's
// cli:// resource family.
//
// It is supplied from here for the same reason the command surface is: this is
// the only package that can see both the command tree and the server. The text
// is the tree's own help rendering, so a command reaches the resource family by
// existing in the tree and its help has one home rather than a restatement here.
//
// The path is the command's own name segments. It resolves against the tree the
// same way an invocation does, and a path that does not resolve to a command all
// the way down is reported as no command rather than as the nearest ancestor:
// resolution leaves whatever it could not match, so anything left over means the
// path named something the tree does not have.
//
// Two calls are what make the rendered text the text the command prints. Running
// a command installs its help and version flags on it immediately before the
// run, so the flag list a reader sees includes them; rendering without those
// calls produces a help block missing the help flag itself.
func commandHelp(root *cobra.Command) mcp.HelpRenderer {
	if root == nil {
		return nil
	}
	return func(path []string) (string, bool) {
		helpRenderMu.Lock()
		defer helpRenderMu.Unlock()

		target, leftover, _ := root.Find(path)
		if target == nil || len(leftover) > 0 {
			return "", false
		}
		target.InitDefaultHelpFlag()
		target.InitDefaultVersionFlag()

		// The writer goes on the command being rendered because the lookup walks
		// up the tree from there, and it is put back before the next reader sees
		// the tree.
		previous := target.OutOrStdout()
		defer target.SetOut(previous)

		var rendered bytes.Buffer
		target.SetOut(&rendered)
		if err := target.Help(); err != nil {
			return "", false
		}
		return rendered.String(), true
	}
}
