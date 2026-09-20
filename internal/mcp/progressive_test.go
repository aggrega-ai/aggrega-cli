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

// listedNames drives tools/list and returns the names it advertised, in order.
func listedNames(t *testing.T, s *server) []string {
	t.Helper()
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
	return names
}

// TestAToolBehindTheFirstSetIsListedAndCallable asserts both halves of what a
// client actually needs, over a tool that sits in the tail rather than in the
// set a caller meets first.
//
// The two halves are asserted together because either alone is satisfiable by a
// build that serves nobody. A name in a listing that the call path refuses is
// the worse of the two failures and reads as the better one — a client registers
// what tools/list gave it, so the model reaches for the tool and meets a refusal
// it cannot act on — and a callable tool absent from the listing is a capability
// no client can find, which is the condition this listing exists to end.
func TestAToolBehindTheFirstSetIsListedAndCallable(t *testing.T) {
	s := newTestServer(t)
	var ran bool
	s.runLint = func(_ context.Context, _ lintRequest, stdout, _ io.Writer) error {
		ran = true
		_, _ = io.WriteString(stdout, "lint passed\n")
		return nil
	}

	names := listedNames(t, s)
	if !containsStr(names, describeCapabilitiesName) {
		t.Errorf("tools/list omits the capability tool %q: %v", describeCapabilitiesName, names)
	}
	if !containsStr(names, "lint") {
		t.Fatalf("tools/list omits lint, which sits in the tail rather than in the set a caller meets "+
			"first; a client registers what this answer gave it, so a tool missing here is one no model "+
			"can call. It was offered %v", names)
	}

	res := callTool(t, s, "lint", lintArgs{Entity: "catalog/component/api.yaml"})
	if res.IsError {
		t.Fatalf("lint was listed and then refused the call: %s", res.Content[0].Text)
	}
	if !ran {
		t.Error("the call was answered without reaching the tool, so the listing's promise was not kept")
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
// It moves the source and reads the projection the capability tool answers with,
// because a projection composed a second time would follow the registry on the
// day it was written and stop following it afterwards.
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
// Every other descriptor field is deliberately left empty, and invented help
// text is worse than none: it would read as a measurement of the real surface
// while being a measurement of whatever a test author happened to type. The cost
// is that a generated tool built from one of these descriptors carries an empty
// definition, so any byte figure taken over a session holding them is a FLOOR
// rather than the figure a client loads — which is why the one check that
// measures says so in the line it logs.
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

// TestEveryToolServedIsAnnounced is the completeness gate over what a client is
// offered, and it carries the measurement of what that costs.
//
// Its subject is the one a client cannot recover from: a tool this server has,
// that this request may run, and that no listing named. A client registers the
// definitions tools/list gave it and nothing else, so a tool quietly absent from
// that answer is an operation no model can reach — however invocable its name
// remains on the wire. The check is therefore a set difference that NAMES what
// went missing, rather than a count: both sides short by one agree perfectly.
//
// The expected set is read from the registries and from this session's derived
// command tools directly, never through the composition under test, which is
// what lets the two disagree. A session with no credential to narrow it and no
// narrowing setting in force announces every one of them.
//
// THERE IS NO CEILING ON THE BYTES, and its absence is the decision rather than
// an oversight. How much of a tool list to put in front of a model is the
// client's decision — the protocol has it fetch the definitions and defer
// injecting them — so a bound asserted here would be this server making that
// decision on the client's behalf, which is the posture the announced surface
// was widened to end. A client that genuinely cannot carry the list says so in
// configuration, and mcp.announce_tools narrows what it is offered; that is a
// statement someone makes about one client, not a constant compiled in here.
//
// The figure is still measured and still logged, because the size being known
// is worth having and is a different thing from the size being bounded: a later
// reader arguing about the surface argues from a measurement rather than from
// an impression. The measurement is of what a client LOADS rather than of what
// the registries declare — a tool answering a capability a command also carries
// discloses the generated tool that reaches it, and those bytes are composed at
// the surface that answers — so the walk reads the advertised description.
func TestEveryToolServedIsAnnounced(t *testing.T) {
	s := &server{out: io.Discard, logw: io.Discard, commandTools: deriveCommandTools(commandBackedSurface())}

	// The oracle, read from the registries rather than from the composition under
	// test. A session speaking the process's own streams admits every reach class
	// and this one carries no narrowing, so every name here must be announced.
	want := make([]string, 0, len(toolRegistry)+len(deferredToolRegistry)+len(s.commandTools))
	declared := make(map[string]string, cap(want))
	for _, registry := range [][]toolSpec{toolRegistry, deferredToolRegistry, s.commandTools} {
		for i := range registry {
			want = append(want, registry[i].name)
			declared[registry[i].name] = registry[i].description
		}
	}
	if len(s.commandTools) == 0 {
		t.Fatal("this session derived no command tool, so the half of the surface that exists only for a " +
			"caller at this machine's keyboard is not in the comparison below")
	}

	served := s.servedTools(context.Background())
	announced := make(map[string]bool, len(served))
	for i := range served {
		announced[served[i].name] = true
	}
	for _, name := range want {
		if !announced[name] {
			t.Errorf("%s is a tool this session serves and this request may run, and no listing named it. "+
				"A client registers what tools/list gave it, so a tool missing from that answer is one no "+
				"model can call", name)
		}
	}
	for i := range served {
		if _, known := declared[served[i].name]; !known {
			t.Errorf("tools/list announced %q, which no registry this session holds declares", served[i].name)
		}
	}

	bytesTotal := 0
	var disclosed int
	for i := range served {
		// The advertised description rather than the declared one: part of what a
		// client loads is composed at the surface that answers, so summing the
		// declared field would measure fewer bytes than reach the client.
		bytesTotal += len(served[i].name)
		bytesTotal += len(s.advertisedDescription(served[i]))
		bytesTotal += len(served[i].inputSchema)
		// Compared by NAME against what the registry declares, never by position:
		// the served order spans three registries and is narrowed per request, so a
		// positional comparison would read one tool's text against another's the
		// moment anything moved.
		if base, known := declared[served[i].name]; known && len(served[i].description) > len(base) {
			disclosed++
		}
	}
	approxTokens := bytesTotal / 4
	// 🔴 The figure is a FLOOR and is labelled as one, because the session under
	// test is handed a stand-in command surface whose descriptors carry no help
	// text and no flags. That stand-in gets the one thing this check needs right —
	// which commands back an operation — and it deliberately invents nothing else,
	// so every generated tool here contributes a name and an empty definition where
	// a real one contributes its command's help and its whole flag schema. A reader
	// quoting this number as what a client loads would understate it substantially;
	// what a client loads is measured by driving a built binary.
	t.Logf("announced definition set, FLOOR against a stand-in command surface: %d tools "+
		"(%d disclosing a generated twin), %d bytes, ~%d tokens (approx bytes/4)",
		len(served), disclosed, bytesTotal, approxTokens)

	// The guard that keeps the measurement honest, and it is the one thing kept
	// from the ceiling this check replaced. A surface that derived no disclosure
	// would be measuring the bare registries rather than a session, and would
	// report a smaller figure than any client ever loads.
	if disclosed == 0 {
		t.Fatal("no tool disclosed a generated twin, so this measures the bare registry rather than a session")
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
