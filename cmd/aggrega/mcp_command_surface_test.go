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

// servedToolDescriptions drives the server over its real transport with the
// supplied command surface and returns every tool a client can reach, by name,
// with the description that client is shown.
//
// Both listings are read, because the two halves of a twice-exposed pair sit on
// different ones: the curated tool is advertised up front and the tool generated
// from its command is surfaced only on demand.
func servedToolDescriptions(t *testing.T, surface []mcp.CommandSurface) map[string]string {
	t.Helper()

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

	out := map[string]string{}

	var listResp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	for _, tl := range listResp.Result.Tools {
		out[tl.Name] = tl.Description
	}

	var callResp struct {
		Result struct {
			StructuredContent struct {
				Capabilities []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"capabilities"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &callResp); err != nil {
		t.Fatalf("unmarshal describe-capabilities: %v", err)
	}
	for _, c := range callResp.Result.StructuredContent.Capabilities {
		out[c.Name] = c.Description
	}
	return out
}

// TestTwiceExposedCapabilitiesDiscloseEachOtherOverTheWire is the end-to-end
// half of the disclosure guarantee: the package-level guard proves the two
// descriptions are built, and this proves they reach a client over the real
// transport with the real command tree behind them.
//
// The pair set is derived here rather than listed, and derived from the
// availability records alone, so a command that gains or loses a curated
// counterpart moves this with no edit. A listed set would have been six on the
// day it was written and stale on the day a command was added.
func TestTwiceExposedCapabilitiesDiscloseEachOtherOverTheWire(t *testing.T) {
	withCLIRegistrations(t, nil)
	root := executingRootCmd()
	surface := commandSurface(root)

	static := map[string]bool{}
	for _, name := range mcp.StaticToolNames() {
		static[name] = true
	}
	generated := map[string]bool{}
	for _, name := range mcp.GeneratedToolNames(surface) {
		generated[name] = true
	}

	// A pair is one operation the tool protocol reaches under two names: one this
	// server answers in-process, one it answers by running a command.
	type pair struct{ curated, generated string }
	pairs := map[string]pair{}
	for id, availability := range mcp.OperationAvailability() {
		if !availability.IsExposed() {
			continue
		}
		var p pair
		for _, name := range availability.Names {
			switch {
			case static[name]:
				p.curated = name
			case generated[name]:
				p.generated = name
			}
		}
		if p.curated != "" && p.generated != "" {
			pairs[id] = p
		}
	}
	if len(pairs) == 0 {
		t.Fatal("no capability is reachable both in-process and by running its command, so this asserts nothing")
	}
	t.Logf("capabilities reachable both in-process and by running a command: %d", len(pairs))

	served := servedToolDescriptions(t, surface)
	for id, p := range pairs {
		curated, ok := served[p.curated]
		if !ok {
			t.Errorf("the %q operation claims %q, which no listing carries", id, p.curated)
			continue
		}
		gen, ok := served[p.generated]
		if !ok {
			t.Errorf("the %q operation claims %q, which no listing carries", id, p.generated)
			continue
		}
		if !strings.Contains(curated, "`"+p.generated+"`") {
			t.Errorf("a client is offered %q and %q for the %q capability, and %q does not mention %q:\n%s",
				p.curated, p.generated, id, p.curated, p.generated, curated)
		}
		if !strings.Contains(gen, "`"+p.curated+"`") {
			t.Errorf("a client is offered %q and %q for the %q capability, and %q does not mention %q:\n%s",
				p.curated, p.generated, id, p.generated, p.curated, gen)
		}
	}
}

// TestNoToolRunsAGuestFromACallerSuppliedPath is the acceptance an exemption
// entry cannot supply on its own: a list entry declares an intent, and what has
// to hold is that no tool exists.
//
// Two halves run over the real transport against the live command tree, and
// neither covers the other. The enumeration proves the name is absent from
// everything a client can discover — the default listing and the on-demand long
// tail, which are different sets answered by different calls. The call proves a
// client naming it anyway is refused, which the enumeration says nothing about: a
// name absent from every listing is still a name a caller can send, and the
// refusal is what stops the send reaching a command. So the refusal is not a
// courtesy on top of the exemption; it is the mechanism the exemption acts
// through.
//
// Three sibling subcommands of the same group are asserted present, and they are
// what stops this going quiet. Without them the absence would be
// indistinguishable from a derivation that reached the plugin group not at all,
// or from a transport that answered nothing — both of which read as success.
//
// 🔴 The two halves are deliberately separate sessions with a fatal between them,
// and the reason is measured rather than stylistic. A generated command tool's
// handler re-invokes the running binary as a subprocess, and this package's
// binary is the test binary, so a call that RESOLVES here re-enters the suite
// instead of running a command. Sending the call unconditionally therefore turns
// a regression from a legible red into a hang that expires on the suite's own
// timeout. Establishing the absence first, and stopping if it does not hold,
// keeps the send on the one path where nothing can be dispatched.
func TestNoToolRunsAGuestFromACallerSuppliedPath(t *testing.T) {
	withCLIRegistrations(t, nil)
	root := executingRootCmd()
	surface := commandSurface(root)

	const withheld = "cli-plugin-run-local"

	// Every name a client can reach: the default listing plus the long tail the
	// capability tool surfaces on demand.
	reachable := map[string]bool{}
	for name := range servedToolDescriptions(t, surface) {
		reachable[name] = true
	}

	for _, sibling := range []string{"cli-plugin-list", "cli-plugin-sync", "cli-plugin-add"} {
		if !reachable[sibling] {
			t.Fatalf("%q is not reachable, so the enumeration did not reach the plugin group and the "+
				"absence of %q measures nothing", sibling, withheld)
		}
	}

	if reachable[withheld] {
		// Fatal rather than an error: the call below must not be sent while a
		// tool of this name resolves, for the reason in the doc comment.
		t.Fatalf("%q is reachable, so the exemption is declared and a tool is generated anyway", withheld)
	}

	var stdin bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", `{"name":"`+withheld+`","arguments":{}}`))

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
	if len(lines) != 2 {
		t.Fatalf("got %d response lines, want 2:\n%s", len(lines), stdout.String())
	}

	var callResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &callResp); err != nil {
		t.Fatalf("unmarshal tools/call %s: %v", withheld, err)
	}
	if callResp.Error == nil {
		t.Fatalf("calling %q was answered rather than refused: %s", withheld, string(callResp.Result))
	}
	if !strings.Contains(callResp.Error.Message, withheld) {
		t.Errorf("the refusal does not name what was refused: %q", callResp.Error.Message)
	}
	for _, pointer := range []string{"available tools", "describe-capabilities"} {
		if !strings.Contains(callResp.Error.Message, pointer) {
			t.Errorf("the refusal does not point the caller at %s, so an assistant meeting it learns only "+
				"that something failed: %q", pointer, callResp.Error.Message)
		}
	}
}
