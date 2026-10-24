// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
)

// commandToolWrites is whether each tool generated from the real command tree
// writes, written out by hand from what each command does — its own help, the
// flags it declares and the files its run path creates — and never computed
// from the declaration the test below checks. A row computed from the class the
// command line declares would agree with that declaration whatever it said.
//
// A command writes when it creates, replaces or removes a file as its result, or
// as the stated effect of a flag it declares: an output or trace directory, a
// lockfile, a trust record, an entity.lock, a configuration file, drafts, a
// cache entry an operation's own class counts. It does not write when its only
// file effect is a store every project on the machine shares and no operation's
// class counts — the compiled-module cache a guest load fills, the descriptor a
// plugin listing keeps, the documentation an explanation fetches.
//
// A command added to the tree has no row and fails the test below until one is
// written, which is the point: whether a new command writes is a question its
// author answers, not one the index guesses.
var commandToolWrites = map[string]bool{
	// The build command saves the rendered artifacts beneath the workspace root,
	// which its operation only renders.
	"cli-build": true,

	// Commands that only read.
	"cli-cache-status":  false,
	"cli-config-show":   false,
	"cli-explain":       false,
	"cli-get-schema":    false,
	"cli-inspect":       false,
	"cli-lint":          false,
	"cli-plugin-list":   false,
	"cli-registry-list": false,
	"cli-scaffold":      false,
	"cli-search":        false,
	"cli-validate":      false,
	"cli-version":       false,

	// Commands whose operation already writes, so its own class says so.
	"cli-cache-clear":        true,
	"cli-cache-evict":        true,
	"cli-cache-prune":        true,
	"cli-config-capture":     true,
	"cli-config-init":        true,
	"cli-deprecate":          true,
	"cli-edit-document":      true,
	"cli-import":             true,
	"cli-pin":                true,
	"cli-plugin-add":         true,
	"cli-plugin-gen-catalog": true,
	"cli-plugin-remove":      true,
	"cli-plugin-sync":        true,
	"cli-pull":               true,
	"cli-push":               true,
	"cli-push-artifact":      true,
	"cli-registry-add":       true,
	"cli-registry-remove":    true,
	"cli-registry-update":    true,
	"cli-sign":               true,
	"cli-skills-install":     true,
}

// capabilityIndexOverTheRealSurface drives the server at its real entry point
// over its real transport, handed the real command surface, and returns the
// argumentless capability index: the structured entries and the text an
// assistant reads, line by line.
func capabilityIndexOverTheRealSurface(t *testing.T) (map[string]bool, map[string]string) {
	t.Helper()
	withCLIRegistrations(t, nil)
	surface := commandSurface(executingRootCmd())

	var stdin bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", `{"name":"describe-capabilities","arguments":{}}`))

	var stdout, stderr bytes.Buffer
	if err := mcp.Run(context.Background(), mcp.Config{
		Stdin:         &stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		ServerVersion: "test",
		Commands:      surface,
	}); err != nil {
		t.Fatalf("mcp.Run: %v (stderr: %s)", err, stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2)

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Index []struct {
				Name   string `json:"name"`
				Writes bool   `json:"writes"`
			} `json:"index"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(answers[2].Result, &result); err != nil {
		t.Fatalf("unmarshal describe-capabilities: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("the capability index carries no text an assistant could read")
	}

	writes := map[string]bool{}
	for _, e := range result.StructuredContent.Index {
		if strings.HasPrefix(e.Name, "cli-") {
			writes[e.Name] = e.Writes
		}
	}
	lines := map[string]string{}
	for _, line := range strings.Split(result.Content[0].Text, "\n") {
		trimmed := strings.TrimSpace(line)
		if name, _, found := strings.Cut(trimmed, " ["); found && strings.HasPrefix(name, "cli-") {
			lines[name] = trimmed
		}
	}
	return writes, lines
}

// TestEachCommandToolIsIndexedAsWritingExactlyWhereItsCommandWrites holds every
// generated tool's index entry to whether its command writes: the structured
// "writes" a client reads, and the "writes" or "reads only" an assistant reads
// in the same answer's text.
//
// It is held by set difference in both directions as well as per row, so a
// generated tool with no row and a row naming no tool each fail by name rather
// than by a count.
//
// 🔴 PLANTED DEFECTS: delete the "build" row from the command line's declared
// command classes (internal/clisurface/availability.go, commandReach) and
// cli-build is indexed "reads only" again, so its row reddens; make
// deriveCommandTool read the operation's class alone again
// (internal/mcp/commandtools.go, drop the clisurface.CommandReach read) and the
// same row reddens. No other row moves under either, because every other command
// reaches exactly what its operation declares.
func TestEachCommandToolIsIndexedAsWritingExactlyWhereItsCommandWrites(t *testing.T) {
	writes, lines := capabilityIndexOverTheRealSurface(t)

	var unwritten, unserved []string
	for name := range writes {
		if _, ok := commandToolWrites[name]; !ok {
			unwritten = append(unwritten, name)
		}
	}
	for name := range commandToolWrites {
		if _, ok := writes[name]; !ok {
			unserved = append(unserved, name)
		}
	}
	sort.Strings(unwritten)
	sort.Strings(unserved)
	if len(unwritten) > 0 {
		t.Errorf("the index carries %v, and nothing here says whether their commands write: add a row for each, "+
			"read from what the command does", unwritten)
	}
	if len(unserved) > 0 {
		t.Errorf("rows name %v, and the index carries no such tool: the command was renamed, removed or exempted, "+
			"so the row is stale", unserved)
	}

	names := make([]string, 0, len(commandToolWrites))
	for name := range commandToolWrites {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := commandToolWrites[name]
		got, indexed := writes[name]
		if !indexed {
			continue
		}
		if got != want {
			t.Errorf("%s is indexed with writes=%v, want %v: whether a generated tool writes is whether its "+
				"command writes", name, got, want)
		}
		trait := "[reads only;"
		if want {
			trait = "[writes;"
		}
		if line := lines[name]; !strings.HasPrefix(line, name+" "+trait) {
			t.Errorf("the index text reads %q for %s, want it to open %q", line, name, name+" "+trait)
		}
	}
}

// buildArtifactsLand is where the build command's own one-line help says its
// artifacts land, written out rather than read from the command, so a help line
// that stopped saying it fails here instead of being carried along.
const buildArtifactsLand = "under dist/ at the workspace root"

// TestTheBuildCommandToolSaysWhereItsArtifactsLand holds the description and the
// index line a caller reads for cli-build to saying that the command writes the
// rendered artifacts under dist/ at the workspace root: in the tool list, in the
// capability tool's definition of it, and in its one-line purpose in the index.
//
// 🔴 PLANTED DEFECT: drop " under dist/ at the workspace root" from the build
// command's one-line help (cmd/aggrega/build.go, Short) and every row here
// reddens, because the description is composed from that help and has no second
// copy of the sentence to fall back on.
func TestTheBuildCommandToolSaysWhereItsArtifactsLand(t *testing.T) {
	withCLIRegistrations(t, nil)
	surface := commandSurface(executingRootCmd())

	listed, indexed := servedListings(t, surface)
	if description := listed["cli-build"]; !strings.Contains(description, buildArtifactsLand) {
		t.Errorf("the tool list describes cli-build without saying where its artifacts land (%q): %q",
			buildArtifactsLand, description)
	}
	if !containsName(indexed, "cli-build") {
		t.Fatalf("the capability index does not carry cli-build, so nothing below reads it: %v", indexed)
	}

	defined := servedToolDescriptions(t, surface)
	if description := defined["cli-build"]; !strings.Contains(description, buildArtifactsLand) {
		t.Errorf("the capability tool defines cli-build without saying where its artifacts land (%q): %q",
			buildArtifactsLand, description)
	}

	_, lines := capabilityIndexOverTheRealSurface(t)
	if line := lines["cli-build"]; !strings.Contains(line, buildArtifactsLand) {
		t.Errorf("the index line for cli-build does not say where its artifacts land (%q): %q",
			buildArtifactsLand, line)
	}
}

// containsName reports whether names carries name.
func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
