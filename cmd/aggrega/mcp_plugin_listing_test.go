// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// Package main's coverage for the one half of the plugin reading that lives
// here: the enumeration the reading is composed from.
//
// 🔴 That half is covered nowhere else, and the reason is structural rather than
// an oversight. Every other proof of this reading stands the server up itself
// and hands it an enumeration by hand, so all of them pass over a command that
// supplies nothing at all — and a tool wired everywhere except at its entry
// point is declared and cannot run, which is the shape that passes quietly at
// every gate. So the case below runs the command.
//
// It is also where the two surfaces are compared, for the same reason: the
// command renders its own listing here, and a comparison against it cannot be
// written anywhere the rendering is unreachable.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
)

// theMissingGuest is a binding the fixture workspace declares and no file
// answers, so the enumeration carries one entry that failed to resolve as well
// as the built-ins that describe cleanly. A listing in which nothing fails would
// leave the failure path uncompared.
const theMissingGuest = "/held/mcp-plugin-listing-9f31/absent.wasm"

// pluginListingWorkspace declares an isolated workspace binding one plugin by a
// path that is not there, and returns its root.
func pluginListingWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	body := "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: " + theMissingGuest + "\n"
	if err := os.WriteFile(filepath.Join(root, ".aggrega-config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write the fixture workspace's configuration: %v", err)
	}
	return resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}).Root()
}

// pluginRecord is one plugin as the tool's structured answer carries it, read
// with the field names that answer uses.
type pluginRecord struct {
	Group        string   `json:"group"`
	URI          *string  `json:"uri"`
	Origin       string   `json:"origin"`
	Type         string   `json:"type"`
	Version      string   `json:"version"`
	Commit       string   `json:"commit"`
	Capabilities []string `json:"capabilities"`
	InputKinds   []string `json:"input_kinds"`
	Boundary     string   `json:"boundary"`
	Exports      []string `json:"exports"`
	Readiness    string   `json:"readiness"`
	Resolved     bool     `json:"resolved"`
	Error        *string  `json:"error"`
}

// pluginListingCallOverStdio drives one plugin-list call at the named server
// entry and returns the answer to it. The two callers differ in which server
// they stand up, which is the whole of what they are comparing, so the request
// they send and the way its answer is read have one home here.
func pluginListingCallOverStdio(t *testing.T, run func(stdin, stdout, stderr *bytes.Buffer)) (isError bool,
	text string, structured json.RawMessage) {

	t.Helper()

	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call",
		`{"name":"plugin-list","arguments":{"response_format":"detailed","limit":500}}`))
	run(&stdin, &stdout, &stderr)

	answers := answersByID(t, stdout.String(), 1, 2)
	if answers[2].Error != nil {
		t.Fatalf("the plugin-list call was not dispatched at all: %s (stderr: %s)",
			answers[2].Error.Message, stderr.String())
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(answers[2].Result, &call); err != nil {
		t.Fatalf("unmarshal the plugin-list answer: %v (body %s)", err, answers[2].Result)
	}
	if len(call.Content) > 0 {
		text = call.Content[0].Text
	}
	return call.IsError, text, call.StructuredContent
}

// TestTheMCPEntryPointSuppliesThePluginEnumerationTheReadingIsComposedFrom
// drives `aggrega mcp` at its real entry point, over its real transport, and
// asserts that a plugin reading called there both answers and answers with the
// enumeration this package performs.
//
// Two claims, and neither substitutes for the other. The call being answered at
// all is the seam itself: a server built without it refuses, which the negative
// control below pins. Comparing every record against what `aggrega plugin list`
// renders over the same workspace is what separates "the field was set to
// something" from "the field was set to the enumeration the command line
// performs" — a stand-in that returned an empty listing would satisfy the first
// and fail the second.
//
// 🔴 PLANTED DEFECT, DEMONSTRATED: remove ListPlugins from the mcp.Config
// literal the command builds and the call is refused ahead of all of this,
// saying the server was started without the plugin enumeration the reading is
// composed from.
func TestTheMCPEntryPointSuppliesThePluginEnumerationTheReadingIsComposedFrom(t *testing.T) {
	// Not parallel: installs the CLI command registrations, a package-level set.
	withCLIRegistrations(t, nil)
	root := pluginListingWorkspace(t)

	isError, text, structured := pluginListingCallOverStdio(t, func(stdin, stdout, stderr *bytes.Buffer) {
		cmd := newRootCmd()
		cmd.SetIn(stdin)
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetArgs([]string{"mcp", "--workspace-root", root})
		if code := executeRoot(context.Background(), cmd); code != 0 {
			t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
		}
	})
	if isError {
		t.Fatalf("the plugin reading was refused: %s", text)
	}

	var payload struct {
		WorkspaceRoot *string        `json:"workspace_root"`
		Count         int            `json:"count"`
		Returned      int            `json:"returned"`
		MoreRemaining bool           `json:"more_remaining"`
		Plugins       []pluginRecord `json:"plugins"`
	}
	if err := json.Unmarshal(structured, &payload); err != nil {
		t.Fatalf("the answer carries no structured payload: %v (body %s)", err, structured)
	}
	if payload.WorkspaceRoot == nil || *payload.WorkspaceRoot != root {
		t.Errorf("the answer names %v as the workspace it was computed over, want the served workspace %q",
			payload.WorkspaceRoot, root)
	}
	if payload.MoreRemaining {
		t.Fatal("the answer is short of the whole listing, so the comparison below would be partial")
	}
	if payload.Returned != payload.Count || payload.Count == 0 {
		t.Fatalf("the answer carries %d of %d records; the comparison below needs all of them",
			payload.Returned, payload.Count)
	}

	// The command's own listing over the same workspace. This is the enumeration
	// the seam is required to be: not a second walk written for this case, but
	// the one `aggrega plugin list` renders.
	var rendered bytes.Buffer
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if err := pluginListRun(context.Background(), rw, &rendered, true); err != nil {
		t.Fatalf("the command's own listing failed, so the comparison measures nothing: %v", err)
	}
	var command pluginListDoc
	if err := json.Unmarshal(rendered.Bytes(), &command); err != nil {
		t.Fatalf("unmarshal the command's listing: %v\n%s", err, rendered.String())
	}

	for _, complaint := range pluginReadingParityComplaints(command, payload.Plugins) {
		t.Error(complaint)
	}
}

// pluginReadingParityComplaints compares what the command rendered against what
// the tool answered, record for record, and reports every value that differs.
//
// Both sides are read at their own surface rather than through a shared
// projection, which is the point: a caller reading the structured answer and a
// reader of the rendered listing have to be reading one fact, and a comparison
// through one projection could only ever prove that projection agrees with
// itself.
func pluginReadingParityComplaints(command pluginListDoc, answered []pluginRecord) []string {
	var out []string

	type groupedEntry struct {
		group string
		entry pluginEntryDoc
	}
	expected := make([]groupedEntry, 0, len(command.Embedded)+len(command.Configured))
	for _, e := range command.Embedded {
		expected = append(expected, groupedEntry{"embedded", e})
	}
	for _, e := range command.Configured {
		expected = append(expected, groupedEntry{"configured", e})
	}

	if len(expected) != len(answered) {
		return append(out, "the command renders "+strconv.Itoa(len(expected))+" plugins and the reading answers "+
			strconv.Itoa(len(answered))+", so the two are not describing one enumeration")
	}
	if len(expected) == 0 {
		return append(out, "the enumeration is empty, so this comparison measured nothing")
	}

	failures := 0
	for i := range expected {
		want, got := expected[i], answered[i]
		where := " (record " + strconv.Itoa(i) + ", " + want.entry.URI + ")"

		if got.Group != want.group {
			out = append(out, "the reading reports group "+got.Group+" where the command renders "+
				want.group+where)
		}
		if got.URI == nil {
			out = append(out, "the reading keeps back a reference over the process's own streams"+where)
		} else if *got.URI != want.entry.URI {
			out = append(out, "the reference differs: "+*got.URI+" vs "+want.entry.URI+where)
		}
		for _, f := range []struct {
			name, got, want string
		}{
			{"origin", got.Origin, want.entry.Origin},
			{"type", got.Type, want.entry.Type},
			{"version", got.Version, want.entry.Version},
			{"commit", got.Commit, want.entry.Commit},
			{"boundary", got.Boundary, want.entry.Boundary},
			{"readiness", got.Readiness, want.entry.Readiness},
		} {
			if f.got != f.want {
				out = append(out, "the "+f.name+" differs: "+f.got+" vs "+f.want+where)
			}
		}
		if !sameStrings(got.Capabilities, want.entry.Capabilities) {
			out = append(out, "the served capabilities differ"+where)
		}
		if !sameStrings(got.InputKinds, want.entry.InputKinds) {
			out = append(out, "the rendered input kinds differ"+where)
		}
		if !sameStrings(got.Exports, want.entry.Exports) {
			out = append(out, "the exports differ"+where)
		}

		switch {
		case want.entry.Error == "" && got.Error != nil:
			out = append(out, "the reading reports a failure the command does not: "+*got.Error+where)
		case want.entry.Error != "" && got.Error == nil:
			out = append(out, "the reading drops the failure the command renders"+where)
		case want.entry.Error != "" && *got.Error != want.entry.Error:
			out = append(out, "the failure text differs: "+*got.Error+" vs "+want.entry.Error+where)
		}
		if want.entry.Error != "" {
			failures++
		}
	}

	// 🔴 The vacuity guard. Every comparison above is satisfied by a fixture in
	// which nothing fails, which would leave the failure path uncompared.
	if failures == 0 {
		out = append(out, "no plugin failed to resolve or describe, so the failure path was not compared: "+
			"the fixture no longer binds a guest that is not there")
	}
	return out
}

// sameStrings compares two lists treating an absent one and an empty one as the
// same answer, because the two surfaces spell "none" differently: the command's
// listing omits an empty list and the reading carries it as an empty array.
func sameStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return slices.Equal(a, b)
}

// TestThePluginReadingRefusesWithoutTheCommandsEnumeration is the negative
// control for the case above. It stands the server up with no enumeration —
// which is what every caller other than the command does — and pins the sentence
// such a session answers with, so a green above is known to be the absence of
// THIS refusal rather than the absence of any refusal at all.
func TestThePluginReadingRefusesWithoutTheCommandsEnumeration(t *testing.T) {
	t.Parallel()
	root := pluginListingWorkspace(t)

	isError, text, _ := pluginListingCallOverStdio(t, func(stdin, stdout, stderr *bytes.Buffer) {
		if err := mcp.Run(context.Background(), mcp.Config{
			Stdin:         stdin,
			Stdout:        stdout,
			Stderr:        stderr,
			ServerVersion: "test",
			WorkspaceRoot: root,
		}); err != nil {
			t.Fatalf("mcp.Run: %v (stderr: %s)", err, stderr.String())
		}
	})
	if !isError {
		t.Fatalf("a session carrying no enumeration answered the plugin reading rather than refusing it: %s",
			text)
	}
	if !strings.Contains(text, "without the plugin enumeration the reading is composed from") {
		t.Errorf("the refusal a session with no enumeration answers with has moved; the case above is "+
			"asserting the absence of a sentence nothing produces: %q", text)
	}
}
