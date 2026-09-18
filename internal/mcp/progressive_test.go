// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// capabilityNames returns the surfaced capability names in order.
func capabilityNames(p describeCapabilitiesResult) []string {
	names := make([]string, 0, len(p.Capabilities))
	for _, c := range p.Capabilities {
		names = append(names, c.Name)
	}
	return names
}

func containsStr(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// TestDefaultToolsListStaysSmall asserts the default tools/list carries the
// curated set plus the one capability tool, and that the deferred lint tool is
// NOT advertised up front.
func TestDefaultToolsListStaysSmall(t *testing.T) {
	s := newTestServer(t)
	res, rpcErr := s.handleToolsList(context.Background())
	if rpcErr != nil {
		t.Fatalf("tools/list returned protocol error: %+v", rpcErr)
	}
	list, ok := res.(listToolsResult)
	if !ok {
		t.Fatalf("tools/list returned %T, want listToolsResult", res)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	if !containsStr(names, describeCapabilitiesName) {
		t.Errorf("default tools/list omits the capability tool %q: %v", describeCapabilitiesName, names)
	}
	if containsStr(names, "lint") {
		t.Errorf("default tools/list advertises the deferred lint tool; it must be surfaced only on demand: %v", names)
	}
	// The default list is exactly the curated registry — deferred tools never
	// leak into it.
	if len(list.Tools) != len(toolRegistry) {
		t.Errorf("default tools/list has %d tools, want the curated %d", len(list.Tools), len(toolRegistry))
	}
}

// TestDescribeCapabilitiesLongTail asserts the capability tool with no kind
// returns the deferred long tail — the tools the default list omits — with each
// marked deferred.
func TestDescribeCapabilitiesLongTail(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{})
	if res.IsError {
		t.Fatalf("describe-capabilities reported error: %s", res.Content[0].Text)
	}
	var payload describeCapabilitiesResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal describe-capabilities payload: %v", err)
	}
	names := capabilityNames(payload)
	if !containsStr(names, "lint") {
		t.Errorf("long-tail result omits the deferred lint tool: %v", names)
	}
	// Every long-tail entry is a deferred tool and carries its full definition.
	for _, c := range payload.Capabilities {
		if !c.Deferred {
			t.Errorf("long-tail capability %q not marked deferred", c.Name)
		}
		if len(c.InputSchema) == 0 || c.Description == "" {
			t.Errorf("long-tail capability %q missing its definition (schema/description)", c.Name)
		}
	}
	// The long tail is exactly the deferred registry.
	if len(payload.Capabilities) != len(deferredToolRegistry) {
		t.Errorf("long tail has %d capabilities, want the deferred %d", len(payload.Capabilities), len(deferredToolRegistry))
	}
}

// TestDescribeCapabilitiesKindFilter asserts a kind filter narrows the surfaced
// set to the tools that serve that kind — including deferred ones — and excludes
// tools scoped to other kinds. It also asserts the capability tool never lists
// itself.
func TestDescribeCapabilitiesKindFilter(t *testing.T) {
	s := newTestServer(t)

	rel := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{Kind: "release"})
	if rel.IsError {
		t.Fatalf("describe-capabilities kind=release reported error: %s", rel.Content[0].Text)
	}
	var relPayload describeCapabilitiesResult
	if err := json.Unmarshal(rel.StructuredContent, &relPayload); err != nil {
		t.Fatalf("unmarshal kind=release payload: %v", err)
	}
	relNames := capabilityNames(relPayload)
	for _, want := range []string{"validate", "build", "describe", "lint"} {
		if !containsStr(relNames, want) {
			t.Errorf("kind=release result omits %q (relevant to release): %v", want, relNames)
		}
	}
	if containsStr(relNames, "gen-catalog") {
		t.Errorf("kind=release surfaced gen-catalog (plugin-only): %v", relNames)
	}
	if containsStr(relNames, describeCapabilitiesName) {
		t.Errorf("kind=release surfaced the capability tool itself: %v", relNames)
	}
	// The deferred lint tool is surfaced as deferred, not curated.
	for _, c := range relPayload.Capabilities {
		if c.Name == "lint" && !c.Deferred {
			t.Errorf("lint surfaced under kind=release not marked deferred")
		}
	}

	plug := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{Kind: "plugin"})
	var plugPayload describeCapabilitiesResult
	if err := json.Unmarshal(plug.StructuredContent, &plugPayload); err != nil {
		t.Fatalf("unmarshal kind=plugin payload: %v", err)
	}
	plugNames := capabilityNames(plugPayload)
	if !containsStr(plugNames, "gen-catalog") {
		t.Errorf("kind=plugin result omits gen-catalog: %v", plugNames)
	}
	// validate serves the plugin kind (binary and Plugin-entity scopes), so it is
	// expected here; build remains release-only and must not surface.
	if !containsStr(plugNames, "validate") {
		t.Errorf("kind=plugin result omits validate (serves plugin scope): %v", plugNames)
	}
	if containsStr(plugNames, "build") {
		t.Errorf("kind=plugin surfaced build (release-only): %v", plugNames)
	}
}

// TestDescribeCapabilitiesUnknownKind asserts an unrecognized kind is a
// tool-level error carrying the valid-kind set.
func TestDescribeCapabilitiesUnknownKind(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{Kind: "not-a-kind"})
	if !res.IsError {
		t.Fatal("describe-capabilities unknown kind did not report an error")
	}
	if !strings.Contains(res.Content[0].Text, "Valid kinds:") {
		t.Errorf("unknown-kind guidance missing the valid-kind set: %q", res.Content[0].Text)
	}
}

// TestDeferredLintInvocableViaToolsCall asserts a deferred tool discovered
// through the capability tool is invocable by name through the same tools/call
// path as a curated tool, and that the lint tool translates its wire arguments
// into the in-process request correctly through the injected runner seam.
func TestDeferredLintInvocableViaToolsCall(t *testing.T) {
	s := newTestServer(t)
	var gotReq lintRequest
	s.runLint = func(_ context.Context, req lintRequest, stdout, stderr io.Writer) error {
		gotReq = req
		_, _ = io.WriteString(stdout, "lint passed: core/compute/api-server\n")
		_, _ = io.WriteString(stderr, "[warning] name-shape: consider a hyphenated name\n")
		return nil
	}

	// Entity mode with pre-publish and a workspace override.
	res := callTool(t, s, "lint", lintArgs{
		Entity: "catalog/component/api.yaml", PrePublish: true, WorkspaceRoot: "/ws",
	})
	if res.IsError {
		t.Fatalf("lint reported error: %s", res.Content[0].Text)
	}
	if gotReq.entity != "catalog/component/api.yaml" || !gotReq.prePublish || gotReq.workspaceRoot != "/ws" || gotReq.release != "" {
		t.Fatalf("lint request = %+v, want entity mode with pre-publish and workspace override", gotReq)
	}
	// The success summary and the advisory (stderr) both reach the caller.
	if !strings.Contains(res.Content[0].Text, "lint passed") {
		t.Errorf("lint result dropped the success summary: %q", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "name-shape") {
		t.Errorf("lint result dropped the advisory finding: %q", res.Content[0].Text)
	}

	// Release mode routes to the closure-scoped lint.
	res2 := callTool(t, s, "lint", lintArgs{Release: "envs/prod/release.yaml"})
	if res2.IsError {
		t.Fatalf("lint release mode reported error: %s", res2.Content[0].Text)
	}
	if gotReq.release != "envs/prod/release.yaml" || gotReq.entity != "" {
		t.Fatalf("lint release request = %+v, want release mode", gotReq)
	}

	// A halting lint surfaces the operation's findings as guidance.
	s.runLint = func(_ context.Context, _ lintRequest, _, stderr io.Writer) error {
		_, _ = io.WriteString(stderr, "[error] category-matches-directory: category label does not match directory")
		return errDummyExit
	}
	bad := callTool(t, s, "lint", lintArgs{Entity: "catalog/component/api.yaml"})
	if !bad.IsError || !strings.Contains(bad.Content[0].Text, "category-matches-directory") {
		t.Errorf("halting lint did not surface findings as guidance: %+v", bad)
	}

	// Mode selection is exclusive, and its refusal reaches the caller as a tool
	// result rather than as an error envelope.
	both := callTool(t, s, "lint", lintArgs{Entity: "a", Release: "b"})
	if !both.IsError || !strings.Contains(both.Content[0].Text, "exactly one of 'entity' or 'release'") {
		t.Errorf("lint with both entity and release = %+v, want a failing tool result naming the exclusion", both)
	}
}

// commandBackedSurface projects the commands the command line records an
// operation for into the surface a session is handed.
//
// It stands in for the live command tree, which this package cannot build, and
// it has to get exactly one thing right: which commands back an operation, and
// therefore which curated tools disclose a generated twin. Those paths are read
// from the command line's own availability records rather than listed here, so a
// command added, renamed or newly exempted moves this with it.
//
// Every other descriptor field is deliberately left empty. None of them reaches
// a curated tool's definition — the disclosure it carries is built from the
// generated tool's name and the command path alone — so filling them with
// invented help text would put bytes into a measurement that production never
// has.
// One entry per PATH rather than per record. A path can be named by more than
// one record — a capability several commands offer under a flag is exposed at
// each of their paths — and a command tree carries each command once, so a
// per-record walk would stand in for a surface no session serves.
func commandBackedSurface() []CommandSurface {
	table := clisurface.OperationAvailability()
	seen := map[string]bool{}
	var out []CommandSurface
	for _, op := range opsurface.Operations() {
		a, ok := table[op.ID]
		if !ok || !a.IsExposed() {
			continue
		}
		for _, path := range a.Names {
			if seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, CommandSurface{
				Descriptor: clisurface.Descriptor{Path: path},
				Runnable:   true,
			})
		}
	}
	return out
}

// TestDefaultDefinitionTokenBudget measures the up-front definition budget a
// client loads from the default tools/list — the bytes of every advertised
// tool's name, description, and argument schema — and records it. Keeping the
// long tail deferred is what holds this figure modest.
//
// It measures what a session serves rather than what the registry declares, and
// the two are no longer the same figure: a curated tool answering a capability a
// command also carries discloses the tool generated from that command, so those
// bytes reach the client and a walk over the bare registry would stop counting
// them. A budget test that measures less than a client loads reports a figure
// nobody pays.
func TestDefaultDefinitionTokenBudget(t *testing.T) {
	s := &server{out: io.Discard, logw: io.Discard, commandTools: deriveCommandTools(commandBackedSurface())}
	served := s.curatedTools(context.Background())

	bytesTotal := 0
	var disclosed int
	for i := range served {
		// The advertised description rather than the declared one: part of what a
		// client loads is composed at the surface that answers, so summing the
		// declared field would measure fewer bytes than reach the client.
		bytesTotal += len(served[i].name)
		bytesTotal += len(s.advertisedDescription(served[i]))
		bytesTotal += len(served[i].inputSchema)
		if len(served[i].description) > len(toolRegistry[i].description) {
			disclosed++
		}
	}
	approxTokens := bytesTotal / 4
	t.Logf("default tools/list definition budget: %d tools (%d disclosing a generated twin), %d bytes, "+
		"~%d tokens (approx bytes/4)", len(served), disclosed, bytesTotal, approxTokens)

	// The measurement is only worth its ceiling if the disclosures are in it. A
	// surface that derived none would measure the bare registry and pass a
	// ceiling set for a figure that includes them.
	if disclosed == 0 {
		t.Fatal("no curated tool disclosed a generated twin, so this measures the bare registry rather than a session")
	}

	// A ceiling that fails loudly if the default set balloons — a full long-tail
	// tool's definition runs ~2000 bytes, so this trips if one creeps back into
	// the default list rather than staying deferred. The headroom is deliberately
	// less than one such definition, which is what makes the ceiling discriminate
	// a creeping tool from ordinary drift.
	//
	// It sits where it does for two reasons, and both are a deliberate trade
	// rather than drift. The curated tools declare their argument-combination
	// rules in their own input schemas rather than only in prose, so a caller
	// reading a schema structurally cannot compose a call the server would refuse;
	// and each tool sharing a capability with a command names that command's tool
	// and says how the two differ, so a caller choosing between two
	// similarly-named tools is not choosing blind. Both cost definition bytes once
	// and save a wrong call every time.
	//
	// It was raised once, for the two tools that enumerate what a workspace holds,
	// and the argument the raise owes is this. What the ceiling exists to catch is
	// a LONG-TAIL tool creeping into the default list — a tool serving a narrow
	// case, which a client should load only when it turns out to need it. These two
	// are the opposite of that: every other tool in the default set takes a subject,
	// and until these landed there was no tool here that could tell a caller what
	// subjects the workspace has. A caller arriving cold could name nothing and had
	// no way to find out. Deferring them would have left that intact — the entry
	// point to the surface reachable only by a caller who already knew to go looking
	// for it.
	//
	// The raise is bounded by what it admits, not by what was needed: both are the
	// cheapest definitions in the set bar one, each roughly half a long-tail tool's,
	// and the new ceiling leaves LESS headroom than the old one did. So the gate
	// discriminates more sharply after the raise than before it, which is the
	// property that matters — a ceiling raised to a round number with room to spare
	// would have bought the two tools by retiring the check.
	//
	// It was raised a second time, for the tool that resolves a Release's
	// dependencies, and that raise owes the same argument. The tool is the opposite
	// of a narrow case: it is the PRECONDITION of a tool the default set already
	// carries. A build renders only from the pins a workspace already holds, and it
	// answers an unpinned external reference, and a drifted lockfile, by saying so
	// and stopping — so a caller that meets either has to resolve the dependencies
	// before its build can produce anything. Deferring it would leave the remedy for
	// a refusal the default set can produce reachable only by a caller who already
	// knew to go looking for it, which is the same ground the enumeration tools were
	// admitted on.
	//
	// The bound is held the same way rather than relaxed: the headroom after this
	// raise is the twelve bytes it was before, so the gate still trips on a
	// long-tail definition creeping in and still discriminates that from ordinary
	// drift. What the raise admits is one definition's worth of bytes, argued above;
	// it buys no room for a second.
	//
	// It was raised a third time, and this raise differs in kind from the three
	// above it: it admits no tool at all. The build tool gained an argument that
	// resolves the Release's dependencies before it renders, so the bytes bought are
	// one boolean's schema entry and one clause in a description — not a definition.
	// What the ceiling exists to catch is a long-tail TOOL creeping into the default
	// list, and an argument on a tool already in that list is categorically not
	// that; the figure moved because an existing definition says more, which is the
	// ordinary drift the headroom was sized to tolerate until a capability this
	// specific came along.
	//
	// The clause could have been kept out of the DESCRIPTION and left in the schema
	// alone, which would have cost a third of these bytes, and that was rejected.
	// The argument reaches a registry and writes the lockfile, and the description
	// is what a client reads when it chooses a tool, so a build that can write while
	// its description says only that it renders in memory would disclose a write in
	// the one place a caller reads after it has already chosen. The bytes buy the
	// disclosure, not the capability.
	//
	// The bound is held exactly as before rather than rounded up: twelve bytes of
	// headroom, which is the same figure the two raises above left, so the gate
	// trips on the next addition as sharply as it tripped on this one.
	const ceiling = 22983
	if bytesTotal > ceiling {
		t.Errorf("default definition budget %d bytes exceeds %d; move long-tail tools to the deferred set", bytesTotal, ceiling)
	}
}

// errDummyExit stands in for a subprocess non-zero exit in the lint runner fake.
var errDummyExit = &exitError{}

type exitError struct{}

func (*exitError) Error() string { return "exit status 1" }

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
