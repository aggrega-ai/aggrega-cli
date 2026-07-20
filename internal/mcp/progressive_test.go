// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
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
	s := newTestServer()
	res, rpcErr := s.handleToolsList()
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
	s := newTestServer()
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
	s := newTestServer()

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
	s := newTestServer()
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
// path as a curated tool, and that the lint tool assembles the subcommand
// argument vector correctly through the injected runner seam.
func TestDeferredLintInvocableViaToolsCall(t *testing.T) {
	s := newTestServer()
	var gotArgs []string
	s.runLint = func(_ context.Context, args []string, stdout, stderr io.Writer) error {
		gotArgs = args
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
	wantArgs := []string{"lint", "--workspace-root", "/ws", "catalog/component/api.yaml", "--pre-publish"}
	if !equalStrings(gotArgs, wantArgs) {
		t.Fatalf("lint argv = %v, want %v", gotArgs, wantArgs)
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
	if !equalStrings(gotArgs, []string{"lint", "--release", "envs/prod/release.yaml"}) {
		t.Fatalf("lint release argv = %v", gotArgs)
	}

	// A halting lint surfaces the subcommand's findings as guidance.
	s.runLint = func(_ context.Context, _ []string, _, stderr io.Writer) error {
		_, _ = io.WriteString(stderr, "[error] category-matches-directory: category label does not match directory")
		return errDummyExit
	}
	bad := callTool(t, s, "lint", lintArgs{Entity: "catalog/component/api.yaml"})
	if !bad.IsError || !strings.Contains(bad.Content[0].Text, "category-matches-directory") {
		t.Errorf("halting lint did not surface findings as guidance: %+v", bad)
	}

	// Mode selection is exclusive.
	params, _ := json.Marshal(callToolParams{Name: "lint", Arguments: mustJSON(t, lintArgs{Entity: "a", Release: "b"})})
	_, rpcErr := s.handleToolsCall(params)
	if rpcErr == nil || rpcErr.Code != codeInvalidParams {
		t.Errorf("lint with both entity and release = %+v, want invalid-params", rpcErr)
	}
}

// TestDefaultDefinitionTokenBudget measures the up-front definition budget a
// client loads from the default tools/list — the bytes of every advertised
// tool's name, description, and argument schema — and records it. Keeping the
// long tail deferred is what holds this figure modest.
func TestDefaultDefinitionTokenBudget(t *testing.T) {
	bytesTotal := 0
	for i := range toolRegistry {
		bytesTotal += len(toolRegistry[i].name)
		bytesTotal += len(toolRegistry[i].description)
		bytesTotal += len(toolRegistry[i].inputSchema)
	}
	approxTokens := bytesTotal / 4
	t.Logf("default tools/list definition budget: %d tools, %d bytes, ~%d tokens (approx bytes/4)",
		len(toolRegistry), bytesTotal, approxTokens)

	// A ceiling that fails loudly if the default set balloons — a full long-tail
	// tool's definition runs ~2000 bytes, so this trips if one creeps back into
	// the default list rather than staying deferred.
	const ceiling = 14500
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
