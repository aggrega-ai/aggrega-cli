// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// capabilityNames returns the tool names a capability result surfaced, in order,
// whichever of the two payloads it carries.
//
// It spans both because the two are answers to the same question asked at
// different depths — which tools are there — and a caller of this helper is
// asking that question rather than asking which shape came back. The shape
// itself is asserted where it is the property under test.
func capabilityNames(p describeCapabilitiesResult) []string {
	names := make([]string, 0, len(p.Index)+len(p.Capabilities))
	for _, e := range p.Index {
		names = append(names, e.Name)
	}
	for _, c := range p.Capabilities {
		names = append(names, c.Name)
	}
	return names
}

// definitionsOf drives the capability tool for the named tools and returns the
// definitions it answered with.
func definitionsOf(t *testing.T, s *server, names ...string) describeCapabilitiesResult {
	t.Helper()
	res := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{Tools: names})
	if res.IsError {
		t.Fatalf("describe-capabilities(tools=%v) reported error: %s", names, res.Content[0].Text)
	}
	var payload describeCapabilitiesResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal describe-capabilities definitions: %v", err)
	}
	return payload
}

// indexOf drives the capability tool with no arguments and returns the index it
// answered with.
func indexOf(t *testing.T, s *server) describeCapabilitiesResult {
	t.Helper()
	res := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{})
	if res.IsError {
		t.Fatalf("describe-capabilities reported error: %s", res.Content[0].Text)
	}
	var payload describeCapabilitiesResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal describe-capabilities index: %v", err)
	}
	return payload
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

// TestDescribeCapabilitiesLongTail asserts the capability tool with no argument
// returns an INDEX of the deferred long tail — the tools the default list omits
// — with every entry carrying a purpose, the kinds it serves and whether it
// writes, and none of them carrying an argument schema.
//
// The absence of the schemas is the property, not a detail of it: the schemas
// are what made the recovery call larger than a client would accept, so an index
// that carried them would be the shape this surface exists to replace.
func TestDescribeCapabilitiesLongTail(t *testing.T) {
	s := newTestServer(t)
	payload := indexOf(t, s)

	names := capabilityNames(payload)
	if !containsStr(names, "lint") {
		t.Errorf("the index omits the deferred lint tool: %v", names)
	}
	if len(payload.Capabilities) > 0 {
		t.Errorf("an argumentless call returned %d full definition(s); discovery answers with the index alone",
			len(payload.Capabilities))
	}
	for _, e := range payload.Index {
		if !e.Deferred {
			t.Errorf("index entry %q not marked as absent from the default list", e.Name)
		}
		if strings.TrimSpace(e.Summary) == "" {
			t.Errorf("index entry %q carries no purpose, so the index has a blank row where a caller reads "+
				"what the tool is for", e.Name)
		}
	}
	// The long tail is exactly the deferred registry.
	if len(payload.Index) != len(deferredToolRegistry) {
		t.Errorf("the index has %d entries, want the deferred %d", len(payload.Index), len(deferredToolRegistry))
	}

	// The whole answer fits in what a client will accept, measured on the bytes a
	// client receives rather than on the count of entries.
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal the index: %v", err)
	}
	const clientPayloadCeiling = 16384
	if len(encoded) > clientPayloadCeiling {
		t.Errorf("the index is %d bytes over this server's tool set, past the %d a client accepts",
			len(encoded), clientPayloadCeiling)
	}
	t.Logf("the index is %d bytes over %d tool(s)", len(encoded), len(payload.Index))
}

// TestNamingToolsReturnsTheirDefinitionsAndNothingElse asserts the delivery half:
// a call naming tools answers with exactly those tools, argument schemas
// included, and an index for none of them.
func TestNamingToolsReturnsTheirDefinitionsAndNothingElse(t *testing.T) {
	s := newTestServer(t)

	for _, want := range [][]string{{"lint"}, {"lint", "cache-status", "publish-entity"}} {
		t.Run(strings.Join(want, "+"), func(t *testing.T) {
			payload := definitionsOf(t, s, want...)
			if got := capabilityNames(payload); !slices.Equal(got, want) {
				t.Fatalf("naming %v returned %v; the result carries exactly the tools named", want, got)
			}
			if len(payload.Index) > 0 {
				t.Errorf("a call naming tools also returned a %d-entry index", len(payload.Index))
			}
			for _, c := range payload.Capabilities {
				if c.Description == "" || len(c.InputSchema) == 0 {
					t.Errorf("%q came back without the definition that was asked for", c.Name)
				}
			}
		})
	}

	// A name nothing answers to fails the whole call rather than being dropped: a
	// result silently short of what was asked for reads as an answer.
	res := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{Tools: []string{"lint", "no-such-tool"}})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "no-such-tool") {
		t.Errorf("naming an unknown tool alongside a real one = %+v, want a failure naming it", res)
	}

	// The two selectors answer different questions and are refused together
	// rather than one being silently ignored.
	both := callTool(t, s, describeCapabilitiesName,
		describeCapabilitiesArgs{Kind: "release", Tools: []string{"lint"}})
	if !both.IsError || !strings.Contains(both.Content[0].Text, "not both") {
		t.Errorf("kind and tools together = %+v, want a failure naming the exclusion", both)
	}
}

// TestTheIndexIsProjectedFromTheRegistry is the derivation assertion: the line a
// caller reads is computed from the tool's own registered text, so a hand
// -maintained list beside the registry would fail here.
//
// It moves the source and reads the projection, in both places the projection is
// rendered — the tool's answer and the connect-time guidance — because a second
// composition would follow the registry in one of them and not the other.
//
// 🔴 It swaps a package-level registry, so it must NOT be made parallel and no
// test that reads that registry may be made parallel either. The swap is safe as
// written because the runner resumes a parallel test only once every sequential
// one has finished, and this one restores the registry before it returns — which
// is a property of the runner rather than of this test, and is why the
// requirement is written down instead of left to be rediscovered.
func TestTheIndexIsProjectedFromTheRegistry(t *testing.T) {
	const planted = "A planted purpose no registry declares."
	restore := deferredToolRegistry
	moved := append([]toolSpec(nil), deferredToolRegistry...)
	for i := range moved {
		if moved[i].name == "lint" {
			moved[i].summary = planted
		}
	}
	deferredToolRegistry = moved
	t.Cleanup(func() { deferredToolRegistry = restore })

	s := newTestServer(t)
	payload := indexOf(t, s)
	var found bool
	for _, e := range payload.Index {
		if e.Name == "lint" {
			found = e.Summary == planted
		}
	}
	if !found {
		t.Errorf("the index did not follow the registered text, so it is authored somewhere else")
	}
	if !strings.Contains(deferredToolIndexParagraph(), planted) {
		t.Errorf("the guidance's index did not follow the registered text, so it is a second copy")
	}
}

// TestEveryProjectedToolCarriesAPurpose is the anti-blank-row assertion over the
// whole served surface, generated command tools included.
//
// The registry entry point refuses a statically declared tool without a purpose,
// which is what makes a blank row impossible for those; the generated half never
// passes through it, so its guarantee is asserted here against a real derivation.
func TestEveryProjectedToolCarriesAPurpose(t *testing.T) {
	s := newCommandToolServer(t, nil)
	generated := 0
	for _, e := range projectTools(s.deferredTools(context.Background()), true) {
		if strings.HasPrefix(e.Name, commandToolPrefix) {
			generated++
		}
		if strings.TrimSpace(e.Summary) == "" {
			t.Errorf("%q is projected with no purpose", e.Name)
		}
	}
	if generated == 0 {
		t.Fatal("no generated command tool was projected, so the half this assertion exists for was not measured")
	}
	for _, e := range projectTools(toolRegistry, false) {
		if strings.TrimSpace(e.Summary) == "" {
			t.Errorf("%q is projected with no purpose", e.Name)
		}
	}
}

// TestWhetherAToolWritesIsReadFromItsReachClass pins the derivation rather than
// the values: a tool that answers an operation the operation set declares a reach
// beyond none for is projected as writing, and one that answers an operation
// declaring none is not.
//
// The class is read back from the operation set rather than from the spec's own
// copy of it, so the assertion can disagree with this package: a spec carrying a
// class the operation set no longer declares fails here rather than being
// compared against itself.
func TestWhetherAToolWritesIsReadFromItsReachClass(t *testing.T) {
	var reading, writing int
	for _, spec := range append(append([]toolSpec(nil), toolRegistry...), deferredToolRegistry...) {
		op, known := opsurface.Lookup(spec.operation)
		if !known {
			t.Errorf("%q answers %q, which the operation set does not declare", spec.name, spec.operation)
			continue
		}
		declaredWrite := op.Reach != opsurface.ReachNone || spec.reachEscalation != nil
		if spec.writes() != declaredWrite {
			t.Errorf("%q is projected as writes=%t against the %q reach the operation set declares for %q",
				spec.name, spec.writes(), op.Reach, spec.operation)
		}
		if declaredWrite {
			writing++
		} else {
			reading++
		}
	}
	// Both sides have to be populated, or the agreement above holds over one
	// answer given to every tool.
	if reading == 0 || writing == 0 {
		t.Fatalf("the registries project %d reading and %d writing tool(s); an all-one-way surface would "+
			"satisfy the comparison above whatever it computed", reading, writing)
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
	for _, c := range relPayload.Index {
		if c.Name == "lint" && !c.Deferred {
			t.Errorf("lint surfaced under kind=release not marked deferred")
		}
	}
	// A kind narrows discovery, so it answers with the index rather than with
	// every matching tool's argument schema — the same undeliverable shape one
	// step smaller.
	if len(relPayload.Capabilities) > 0 {
		t.Errorf("kind=release returned %d full definition(s); narrowing is discovery and answers with the index",
			len(relPayload.Capabilities))
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
	// The bound is held exactly as before rather than rounded up: the same
	// headroom the two raises above left, so the gate trips on the next addition
	// as sharply as it tripped on this one.
	//
	// It was raised a fourth time, and this raise is the third one's case again
	// with a sharper argument: the capability tool gained an argument that names
	// the tools whose definitions a caller wants. No tool joins the default list,
	// and the bytes bought are one array property's schema entry and a reworded
	// description — the description shrank, so the raise is the schema alone.
	//
	// What it buys is what makes the deferral work at all. The call that recovers
	// the deferred definitions used to return every one of them in full, which is
	// larger than a client accepts, so the tools held out of the default list were
	// not merely loaded late — they were unreachable. Splitting that call into an
	// index and a by-name fetch is what makes the bytes this gate protects an
	// actual saving rather than a payload moved somewhere a client refuses to read
	// it.
	//
	// The ceiling moved by the measured delta and by nothing else, so the headroom
	// is the figure it was before rather than a rounder one, and the run logs it
	// so the next raise argues from a measurement rather than from this comment.
	//
	// It was raised a fifth time, and this raise is the third one's case again for
	// the two tools that enumerate what a workspace holds. No tool joins the default
	// list. Each of the two gained the argument that chooses between a summary of the
	// workspace and the records themselves, and the two that bound the records to a
	// page, and each says in its description which shape it answers in when a call
	// names none. The bytes are two schema entries apiece and the sentences that
	// disclose them.
	//
	// What they buy is the reason the raise is worth making at all: before it, the
	// call the guidance tells an assistant to make first returned every record the
	// workspace held, which on a large one is megabytes into a context window for a
	// question that was only ever "what is in here". Paying a kilobyte in every
	// session's definitions to stop paying a megabyte in the first call of a session
	// on a large workspace is the trade, and it is the same one the deferral of the
	// long tail makes.
	//
	// The clause could have been left in the schemas and kept out of the
	// DESCRIPTIONS, which would have cost a third of these bytes, and that was
	// rejected for the reason the third raise rejected it: the description is what a
	// client reads when it chooses a tool, and a tool whose description promises
	// every entity while the tool answers with a summary would misreport its own
	// answer in the one place a caller reads before calling.
	//
	// The bound is held exactly as before rather than rounded up: the same headroom
	// every raise above left, so the gate trips on the next addition as sharply as it
	// tripped on this one.
	const ceiling = 25841
	if bytesTotal > ceiling {
		t.Errorf("default definition budget %d bytes exceeds %d; move long-tail tools to the deferred set", bytesTotal, ceiling)
	}
	t.Logf("definition budget headroom: %d bytes below the %d ceiling", ceiling-bytesTotal, ceiling)
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
