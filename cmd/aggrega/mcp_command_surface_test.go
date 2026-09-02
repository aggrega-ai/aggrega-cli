// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
)

// executingRootCmd builds the command tree in the shape the MCP server actually
// receives it.
//
// The distinction is load-bearing rather than incidental: cobra adds its own
// help and completion commands when a tree RUNS, not when it is built, and the
// server is handed the root of a tree that is already running. A test over an
// unexecuted tree cannot see those commands at all, so a surface assertion made
// against one is blind to every tool they would contribute — and blind in the
// direction that reads as success, since the missing commands are missing from
// the expectation too. Initializing them here holds the test to the shape
// production actually serves.
func executingRootCmd() *cobra.Command {
	root := newRootCmd()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	return root
}

// TestCommandSurfaceCoversTheCatalogAndMarksRunnable is the guard that the
// projection handed to the MCP server neither loses a command nor mislabels one.
//
// It compares against the shared catalog and against each command's own answer
// to whether it runs, taken from the one tree walk the tests in this package
// share, so the surface cannot agree with a second copy of itself.
func TestCommandSurfaceCoversTheCatalogAndMarksRunnable(t *testing.T) {
	withCLIRegistrations(t, nil)
	root := executingRootCmd()

	surface := commandSurface(root)
	cat := clisurface.Catalog(root)
	if len(surface) != len(cat) {
		t.Fatalf("surface has %d entries for a %d-entry catalog", len(surface), len(cat))
	}
	for i := range cat {
		if surface[i].Descriptor.Path != cat[i].Path {
			t.Fatalf("surface[%d] = %q, want the catalog's %q (order must be the catalog's)",
				i, surface[i].Descriptor.Path, cat[i].Path)
		}
	}

	// Runnable is read from the tree, never inferred from the descriptor.
	wantRunnable := map[string]bool{}
	for _, c := range reachableCommands(root) {
		wantRunnable[c.CommandPath()] = c.Runnable()
	}
	for _, cs := range surface {
		want, ok := wantRunnable[cs.Descriptor.Path]
		if !ok {
			t.Errorf("surface carries %q, which the tree walk does not reach", cs.Descriptor.Path)
			continue
		}
		if cs.Runnable != want {
			t.Errorf("%q marked runnable=%v, tree says %v", cs.Descriptor.Path, cs.Runnable, want)
		}
	}

	// A grouping command and a leaf are both present, so the assertion above is
	// discriminating rather than vacuously true over one shape.
	var groups, leaves int
	for _, cs := range surface {
		if cs.Runnable {
			leaves++
		} else {
			groups++
		}
	}
	if groups == 0 || leaves == 0 {
		t.Errorf("surface has %d grouping and %d runnable commands; expected both shapes", groups, leaves)
	}
}

// rpcLine frames one JSON-RPC request as the server's newline-delimited
// transport expects.
func rpcLine(id int, method, params string) string {
	if params == "" {
		params = "{}"
	}
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"` + method + `","params":` + params + "}\n"
}

// mcpCommandToolName renders a command path as the tool name the server
// advertises for it. It mirrors the server's own derivation because the naming
// rule is not exported; the guard against the two drifting is that this test
// asserts the names it computes are actually surfaced, so a divergence fails
// here rather than passing quietly.
func mcpCommandToolName(path string) string {
	return "cli-" + strings.Join(strings.Fields(path)[1:], "-")
}

// TestMCPServerExposesTheRealCommandSurface drives the server at its real entry
// point over its real transport, with the real command tree, and asserts the
// long tail a client discovers is the command surface.
//
// This is the end-to-end half of the completeness guarantee: the package-level
// tests prove the derivation over a fixture, and this proves the wiring carries
// the live tree into it.
func TestMCPServerExposesTheRealCommandSurface(t *testing.T) {
	withCLIRegistrations(t, nil)
	root := executingRootCmd()
	surface := commandSurface(root)

	var stdin bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/list", ""))
	stdin.WriteString(rpcLine(3, "tools/call", `{"name":"describe-capabilities","arguments":{}}`))

	var stdout, stderr bytes.Buffer
	if err := mcp.Run(mcp.Config{
		Stdin:         &stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		ServerVersion: "test",
		Commands:      surface,
	}); err != nil {
		t.Fatalf("mcp.Run: %v (stderr: %s)", err, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d response lines, want 3:\n%s", len(lines), stdout.String())
	}

	// The default tool list must not carry the long tail.
	var listResp struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	for _, tl := range listResp.Result.Tools {
		if strings.HasPrefix(tl.Name, "cli-") {
			t.Errorf("the default tool list carries a generated command tool %q", tl.Name)
		}
	}

	// The capability listing carries one tool per non-exempt runnable command.
	var callResp struct {
		Result struct {
			StructuredContent struct {
				Capabilities []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Deferred    bool   `json:"deferred"`
				} `json:"capabilities"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &callResp); err != nil {
		t.Fatalf("unmarshal describe-capabilities: %v", err)
	}
	surfaced := map[string]bool{}
	for _, c := range callResp.Result.StructuredContent.Capabilities {
		surfaced[c.Name] = true
	}

	// Exemption is decided by the server's own exported matcher, never by a
	// second copy of the rule here. The rule covers a subtree as well as an
	// exact path, so a copy that compared paths for equality would report a
	// deliberately withheld subcommand as missing.
	declared := mcp.CommandExemptions()
	if len(declared) == 0 {
		t.Fatal("no exemptions declared; the exemption list is not readable from here")
	}
	for _, e := range declared {
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("exemption for %q carries no reason", e.Path)
		}
	}

	// Every runnable, non-exempt command in the live tree is discoverable. The
	// expected set is computed from the tree, so a command added to the tree is
	// covered without editing this test.
	var missing []string
	for _, cs := range surface {
		if !cs.Runnable || cs.Descriptor.Hidden {
			continue
		}
		if _, exempt := mcp.CommandExempted(cs.Descriptor.Path); exempt {
			continue
		}
		if name := mcpCommandToolName(cs.Descriptor.Path); !surfaced[name] {
			missing = append(missing, cs.Descriptor.Path+" ("+name+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("commands not discoverable over MCP: %v", missing)
	}

	// Every exempt command is absent, and every exempt path still names a
	// command the tree actually has — an exemption for a renamed command would
	// otherwise sit there withholding nothing.
	inTree := map[string]bool{}
	for _, cs := range surface {
		inTree[cs.Descriptor.Path] = true
	}
	for _, e := range declared {
		if !inTree[e.Path] {
			t.Errorf("exemption names %q, which is not a command in the tree", e.Path)
		}
	}
	for _, cs := range surface {
		if _, exempt := mcp.CommandExempted(cs.Descriptor.Path); !exempt {
			continue
		}
		if name := mcpCommandToolName(cs.Descriptor.Path); surfaced[name] {
			t.Errorf("exempt command %q is exposed as %q", cs.Descriptor.Path, name)
		}
	}

	// Named spot checks, so the assertion above cannot pass over an empty set.
	for _, want := range []string{"cli-build", "cli-validate", "cli-push", "cli-sign", "cli-plugin-gen-catalog", "cli-cache-status"} {
		if !surfaced[want] {
			t.Errorf("expected %q in the capability listing", want)
		}
	}
	// A grouping command yields no tool.
	for _, notWanted := range []string{"cli-plugin", "cli-cache", "cli-auth", "cli-config", "cli-registry"} {
		if surfaced[notWanted] {
			t.Errorf("grouping command exposed as %q", notWanted)
		}
	}

	// The commands cobra adds when the tree runs are exempt, and the completion
	// group is exempt as a subtree so a shell cobra adds later is covered with
	// no edit here. These exist only in an executing tree, which is why this
	// assertion is meaningful only against executingRootCmd.
	for _, notWanted := range []string{
		"cli-help",
		"cli-completion-bash", "cli-completion-zsh",
		"cli-completion-fish", "cli-completion-powershell",
	} {
		if surfaced[notWanted] {
			t.Errorf("a cobra-generated command is exposed as %q", notWanted)
		}
	}
	// The precondition for that assertion: those commands are in the tree, so
	// their absence from the surface is an exemption doing work rather than the
	// tree simply not having them.
	if !inTree["aggrega completion bash"] || !inTree["aggrega help"] {
		t.Error("the executing tree lacks cobra's help/completion commands; the exemption above proves nothing")
	}
}
