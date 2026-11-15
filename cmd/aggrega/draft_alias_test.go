// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The hidden scaffold spelling of the draft command: the same document on
// standard output, the deprecation notice on standard error ahead of anything
// else, absent from help, and absent from the tools an assistant is offered.

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
)

// scaffoldNoticeLine is the line the hidden scaffold spelling writes to
// standard error, newline included.
const scaffoldNoticeLine = "`aggrega scaffold` is deprecated and will be removed in 0.10.0; use `aggrega draft`\n"

// TestScaffoldAliasStdoutMatchesDraft pins that the two spellings hand back the
// same document byte for byte, and that the hidden one adds exactly the notice
// on standard error and nothing on standard output.
func TestScaffoldAliasStdoutMatchesDraft(t *testing.T) {
	isolateCLIEnvironment(t)
	draftOut, draftNotes, err := runExplainCommandWith(t, context.Background(), "draft", "component", "my-service")
	if err != nil {
		t.Fatalf("aggrega draft component my-service: %v\nnotes: %s", err, draftNotes)
	}
	if !strings.Contains(draftOut, "kind: Component") {
		t.Fatalf("aggrega draft drafted no Component, so comparing the spellings asserts nothing:\n%s", draftOut)
	}
	if draftNotes != "" {
		t.Errorf("aggrega draft wrote to standard error: %q", draftNotes)
	}
	aliasOut, aliasNotes, err := runExplainCommandWith(t, context.Background(), "scaffold", "component", "my-service")
	if err != nil {
		t.Fatalf("aggrega scaffold component my-service: %v\nnotes: %s", err, aliasNotes)
	}
	if aliasOut != draftOut {
		t.Errorf("the scaffold spelling wrote\n%s\nwant the draft spelling's bytes\n%s", aliasOut, draftOut)
	}
	if aliasNotes != scaffoldNoticeLine {
		t.Errorf("the scaffold spelling wrote %q to standard error, want exactly %q", aliasNotes, scaffoldNoticeLine)
	}
}

// TestScaffoldAliasNoticeOnArgumentError pins that the notice precedes the
// refusal of arguments the command does not accept, so a caller who mistyped
// still learns the spelling is going away.
func TestScaffoldAliasNoticeOnArgumentError(t *testing.T) {
	isolateCLIEnvironment(t)
	_, notes, err := runExplainCommandWith(t, context.Background(), "scaffold", "--list")
	const refusal = "--list prints the documentation's runnable examples and is given together with --example: " +
		"aggrega draft --example --list"
	if err == nil || err.Error() != refusal {
		t.Fatalf("scaffold --list answered %v, want the refusal %q", err, refusal)
	}
	if !strings.HasPrefix(notes, scaffoldNoticeLine) {
		t.Errorf("standard error reads %q, want it to open with the notice %q", notes, scaffoldNoticeLine)
	}
	if strings.Count(notes, scaffoldNoticeLine) != 1 {
		t.Errorf("standard error carries the notice %d times, want once: %q",
			strings.Count(notes, scaffoldNoticeLine), notes)
	}
}

// TestScaffoldAliasNoticeOnExampleList pins the notice on the example form: it
// opens standard error, and the list on standard output is the draft spelling's.
func TestScaffoldAliasNoticeOnExampleList(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(), signer, signer)

	draftOut, draftNotes, err := f.run(t, f.online(), "draft", "--example", "--list")
	if err != nil {
		t.Fatalf("aggrega draft --example --list: %v\nnotes: %s", err, draftNotes)
	}
	aliasOut, aliasNotes, err := f.run(t, f.online(), "scaffold", "--example", "--list")
	if err != nil {
		t.Fatalf("aggrega scaffold --example --list: %v\nnotes: %s", err, aliasNotes)
	}
	if !strings.Contains(draftOut, "demo") {
		t.Fatalf("the list names no example, so comparing the spellings asserts nothing:\n%s", draftOut)
	}
	if aliasOut != draftOut {
		t.Errorf("the scaffold spelling listed\n%s\nwant the draft spelling's bytes\n%s", aliasOut, draftOut)
	}
	if aliasNotes != scaffoldNoticeLine+draftNotes {
		t.Errorf("the scaffold spelling wrote %q to standard error, want the notice followed by the draft "+
			"spelling's own notes %q", aliasNotes, scaffoldNoticeLine+draftNotes)
	}
}

// TestScaffoldAliasNoticeOnExampleWrite pins the notice on writing an example:
// it opens standard error, ahead of every note the acquisition writes.
func TestScaffoldAliasNoticeOnExampleWrite(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(), signer, signer)

	dest := t.TempDir() + "/demo"
	out, notes, err := f.run(t, f.online(), "scaffold", "--example", "demo", dest)
	if err != nil {
		t.Fatalf("aggrega scaffold --example demo: %v\nnotes: %s", err, notes)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the example was not written: %v\n%s", err, out)
	}
	if !strings.HasPrefix(notes, scaffoldNoticeLine) || strings.Count(notes, scaffoldNoticeLine) != 1 {
		t.Errorf("standard error reads %q, want it to open with the notice %q, once", notes, scaffoldNoticeLine)
	}
}

// TestScaffoldHiddenFromHelp pins that the command list a person reads offers
// draft and not the deprecated spelling.
func TestScaffoldHiddenFromHelp(t *testing.T) {
	isolateCLIEnvironment(t)
	out, notes, err := runExplainCommandWith(t, context.Background(), "--help")
	if err != nil {
		t.Fatalf("aggrega --help: %v\nnotes: %s", err, notes)
	}
	if !strings.Contains(out, "\n  draft ") {
		t.Errorf("aggrega --help lists no draft command:\n%s", out)
	}
	if strings.Contains(out, "scaffold") {
		t.Errorf("aggrega --help names scaffold:\n%s", out)
	}
}

// TestNoCommandToolForHiddenScaffold pins that the tools generated from the
// command tree carry draft and never the hidden scaffold spelling, read from
// the derivation itself rather than assumed of it.
func TestNoCommandToolForHiddenScaffold(t *testing.T) {
	withCLIRegistrations(t, nil)
	names := mcp.GeneratedToolNames(commandSurface(newRootCmd()))
	if !slices.Contains(names, mcpCommandToolName("aggrega draft")) {
		t.Errorf("the generated tools %v carry no %q", names, mcpCommandToolName("aggrega draft"))
	}
	if slices.Contains(names, mcpCommandToolName("aggrega scaffold")) {
		t.Errorf("the generated tools %v carry %q for the hidden spelling", names, mcpCommandToolName("aggrega scaffold"))
	}
}
