// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// The assertions in this file are driven from the tool registries rather than
// from a hand-written list of tool names, so a tool added to either registry
// later inherits the failure-guidance property instead of escaping it. A
// hand-picked set would keep passing while a newly added tool answered a bad
// call with an opaque code, which is the failure mode these assertions exist to
// prevent.
//
// Two payloads are drivable generically against every registered tool, and the
// pair is deliberate: one is rejected by every tool (a payload that is not an
// arguments object at all) and one is rejected by every tool that needs an
// argument to act (an empty arguments object). A tool's semantic failures — an
// unresolvable path, a diagnostic from the compiler — need workspace state that
// differs per tool, so those are covered by that tool's own test rather than
// here.

// internalLabelTokens are implementation labels that must never appear in text a
// caller reads. A caller cannot see, correct, or look up an internal shape, so
// naming one costs the message its only purpose: the correction the caller was
// supposed to act on.
var internalLabelTokens = []string{
	"mcp.",
	"*mcp",
	"internal/",
	"Go value",
	"Go struct field",
	"reflect.",
	"json:",
}

// opaqueCodeOnly matches a message that is nothing but a bare identifier code.
// Such a message tells a caller that something failed and nothing about what to
// do next, which is precisely the shape this file rejects.
var opaqueCodeOnly = regexp.MustCompile(`^[A-Za-z]{0,6}[-_]?[0-9]+$`)

// guidanceMinimumWords is the floor below which a failure message cannot be
// carrying both a cause and a correction. It is set just under the shortest
// message the server currently produces, so it rejects a terse dismissal
// without pinning any particular phrasing.
const guidanceMinimumWords = 4

// registeredToolSpecs returns every invocable tool, curated set first and then
// the deferred long tail, so an assertion spans both registries rather than the
// default-advertised one alone.
func registeredToolSpecs() []toolSpec {
	out := make([]toolSpec, 0, len(toolRegistry)+len(deferredToolRegistry))
	out = append(out, toolRegistry...)
	out = append(out, deferredToolRegistry...)
	return out
}

// declaredArgumentKeys returns the argument names a tool's own input schema
// declares. It reads the spec's schema rather than any list kept beside it, so a
// tool that gains an argument is measured against its new surface.
func declaredArgumentKeys(t *testing.T, spec toolSpec) []string {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.inputSchema), &schema); err != nil {
		t.Fatalf("%s: input schema does not parse as JSON: %v", spec.name, err)
	}
	keys := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		keys = append(keys, k)
	}
	return keys
}

// toolFailure is one failure a tool call produced: the text a caller reads, and
// which of the two channels carried it. The channel matters to what the text
// owes, which is why it is captured rather than flattened away — see
// assertActionableGuidance.
type toolFailure struct {
	text string
	// viaProtocol is true when the failure travelled as a JSON-RPC error
	// envelope, and false when it travelled as a tool result marked as a
	// failure. Nothing failed at all when failed is false.
	viaProtocol bool
	failed      bool
}

// callToolRaw drives a tool through the real tools/call dispatch with a verbatim
// arguments payload and reports what came back.
func callToolRaw(t *testing.T, s *server, name, rawArgs string) toolFailure {
	t.Helper()
	params := json.RawMessage(fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, rawArgs))
	res, rpcErr := s.handleToolsCall(params)
	if rpcErr != nil {
		return toolFailure{text: rpcErr.Message, viaProtocol: true, failed: true}
	}
	cr, ok := res.(*callToolResult)
	if !ok {
		t.Fatalf("%s: tools/call returned %T, want *callToolResult", name, res)
	}
	if len(cr.Content) == 0 || cr.Content[0].Type != "text" {
		t.Fatalf("%s: tools/call returned no text content block: %+v", name, cr.Content)
	}
	return toolFailure{text: cr.Content[0].Text, failed: cr.IsError}
}

// assertActionableGuidance checks one failure message against the properties a
// message an assistant acts on must have: it exists, it says enough to carry a
// cause and a correction, and it leaks no implementation label the caller cannot
// act on.
//
// One property is asked of one channel only, and the asymmetry is the point
// rather than an omission. A failure travelling as an error envelope must name
// the tool, because that envelope carries a code and a message and nothing that
// identifies which call produced it. A failure travelling as a tool result need
// not: it is the answer to a call the client made by name, so the subject is
// already established, and requiring the name again would push every message
// toward restating what the envelope already says.
func assertActionableGuidance(t *testing.T, tool, scenario string, f toolFailure) {
	t.Helper()
	trimmed := strings.TrimSpace(f.text)
	if trimmed == "" {
		t.Errorf("%s (%s): failed with no guidance text at all", tool, scenario)
		return
	}
	if opaqueCodeOnly.MatchString(trimmed) {
		t.Errorf("%s (%s): guidance is a bare code with no correction: %q", tool, scenario, trimmed)
	}
	if n := len(strings.Fields(trimmed)); n < guidanceMinimumWords {
		t.Errorf("%s (%s): guidance is %d word(s), too terse to carry a cause and a correction: %q",
			tool, scenario, n, trimmed)
	}
	if f.viaProtocol && !strings.Contains(trimmed, tool) {
		t.Errorf("%s (%s): an error envelope carries no tool name of its own, and this guidance does not name the tool either: %q",
			tool, scenario, trimmed)
	}
	for _, token := range internalLabelTokens {
		if strings.Contains(trimmed, token) {
			t.Errorf("%s (%s): guidance leaks the implementation label %q, which the caller cannot act on: %q",
				tool, scenario, token, trimmed)
		}
	}
}

// TestEveryRegisteredToolRejectsANonObjectPayloadWithGuidance asserts that every
// tool in either registry refuses a payload that is not an arguments object, and
// that its refusal is guidance rather than a decoder's internal complaint. No
// tool is exempt: a payload of the wrong shape reaches every tool's argument
// decoding before any of its own logic.
func TestEveryRegisteredToolRejectsANonObjectPayloadWithGuidance(t *testing.T) {
	s := newTestServer(t)
	for _, spec := range registeredToolSpecs() {
		f := callToolRaw(t, s, spec.name, `[]`)
		if !f.failed {
			t.Errorf("%s: accepted an array where an arguments object is required: %q", spec.name, f.text)
			continue
		}
		assertActionableGuidance(t, spec.name, "non-object payload", f)
	}
}

// TestEveryRegisteredToolRejectsAWrongTypedArgumentWithGuidance asserts that a
// declared argument sent as the wrong JSON type is refused with guidance naming
// the offending field. The field is chosen from the tool's own schema, so the
// case is generated per tool rather than hand-written, and a tool whose schema
// declares no string argument is reported as uncovered rather than skipped
// silently.
func TestEveryRegisteredToolRejectsAWrongTypedArgumentWithGuidance(t *testing.T) {
	s := newTestServer(t)
	for _, spec := range registeredToolSpecs() {
		key, ok := firstStringArgument(t, spec)
		if !ok {
			t.Logf("%s: no string-typed argument declared; the wrong-type case is not drivable for this tool", spec.name)
			continue
		}
		f := callToolRaw(t, s, spec.name, fmt.Sprintf(`{%q: 5}`, key))
		if !f.failed {
			t.Errorf("%s: accepted a number for the string argument %q: %q", spec.name, key, f.text)
			continue
		}
		assertActionableGuidance(t, spec.name, "wrong-typed "+key, f)
		if !strings.Contains(f.text, key) {
			t.Errorf("%s: wrong-type guidance does not name the offending argument %q: %q", spec.name, key, f.text)
		}
	}
}

// TestEveryRegisteredToolGuidesAnEmptyCall asserts that a tool given an empty
// arguments object either acts (it needs no argument) or refuses with guidance
// naming an argument the caller can supply. The argument names come from the
// tool's own input schema, so the assertion measures each tool against the
// surface it advertises rather than against a list kept in this file.
func TestEveryRegisteredToolGuidesAnEmptyCall(t *testing.T) {
	s := newTestServer(t)
	for _, spec := range registeredToolSpecs() {
		f := callToolRaw(t, s, spec.name, `{}`)
		if !f.failed {
			// A tool that needs no argument answers an empty call; that is a
			// success, not a missing refusal.
			continue
		}
		assertActionableGuidance(t, spec.name, "empty call", f)
		keys := declaredArgumentKeys(t, spec)
		if len(keys) == 0 {
			continue
		}
		named := false
		for _, k := range keys {
			if strings.Contains(f.text, k) {
				named = true
				break
			}
		}
		if !named {
			t.Errorf("%s: empty-call guidance names none of the arguments it declares (%s): %q",
				spec.name, strings.Join(keys, ", "), f.text)
		}
	}
}

// TestEveryReleaseTakingToolGuidesAnUnresolvableRelease asserts that a tool
// pointed at a Release that is not there answers with a tool-level failure
// carrying guidance, rather than with a bare miss. The tools are selected by
// reading which ones declare a release argument, so this spans both registries
// and picks up a later tool that takes a Release without anyone adding it here.
//
// This is the channel the argument-shape assertions above cannot reach: those
// calls are refused before the operation runs, whereas this one runs the
// operation and reports its outcome, which is where a caller most needs to be
// told what to fix.
func TestEveryReleaseTakingToolGuidesAnUnresolvableRelease(t *testing.T) {
	s := newTestServer(t)
	drivable := 0
	for _, spec := range registeredToolSpecs() {
		if !declaresStringArgument(t, spec, "release") {
			continue
		}
		drivable++
		f := callToolRaw(t, s, spec.name, `{"release": "no-such-release.yaml"}`)
		if !f.failed {
			t.Errorf("%s: reported success for a Release that is not there: %q", spec.name, f.text)
			continue
		}
		assertActionableGuidance(t, spec.name, "unresolvable release", f)
		if !strings.Contains(f.text, "no-such-release.yaml") {
			t.Errorf("%s: guidance does not name the Release it could not reach: %q", spec.name, f.text)
		}
		// The operation ran and failed, so the caller is owed the authoring
		// contract for what it was reading, not just the failure.
		if !strings.Contains(f.text, "See: ") {
			t.Errorf("%s: guidance for a failed operation carries no documentation pointer: %q", spec.name, f.text)
		}
	}
	if drivable == 0 {
		t.Fatal("no registered tool declares a release argument; the assertion measured nothing")
	}
}

// declaresStringArgument reports whether a tool's own input schema declares the
// named argument as a string, so a case can be generated for the tools that
// accept a given kind of input without naming those tools here.
func declaresStringArgument(t *testing.T, spec toolSpec, name string) bool {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.inputSchema), &schema); err != nil {
		t.Fatalf("%s: input schema does not parse as JSON: %v", spec.name, err)
	}
	prop, ok := schema.Properties[name]
	return ok && prop.Type == "string"
}

// firstStringArgument returns the lexically first string-typed argument a tool
// declares, so the wrong-type case is generated deterministically from the
// tool's own schema.
func firstStringArgument(t *testing.T, spec toolSpec) (string, bool) {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.inputSchema), &schema); err != nil {
		t.Fatalf("%s: input schema does not parse as JSON: %v", spec.name, err)
	}
	best := ""
	for name, prop := range schema.Properties {
		if prop.Type != "string" {
			continue
		}
		if best == "" || name < best {
			best = name
		}
	}
	return best, best != ""
}

// TestDecodeFailureGuidanceCases pins the four shapes the shared decode-failure
// route renders, so each one is asserted directly rather than only through
// whichever tool happens to reach it.
func TestDecodeFailureGuidanceCases(t *testing.T) {
	type target struct {
		Kind    string   `json:"kind"`
		Dirs    []string `json:"catalog_dirs"`
		Enabled bool     `json:"enabled"`
	}
	decode := func(raw string) error {
		var v target
		return json.Unmarshal([]byte(raw), &v)
	}

	cases := []struct {
		name    string
		raw     string
		want    []string
		notWant []string
	}{
		{
			name: "malformed json names the offset",
			raw:  `{"kind": `,
			want: []string{"validate:", "not valid JSON", "byte offset", "call it again"},
		},
		{
			name: "wrong-typed field names the field and both types",
			raw:  `{"kind": 5}`,
			want: []string{`validate: the "kind" field must be a string`, "a number was sent", "Correct that field"},
		},
		{
			name: "wrong-typed array field reads as an array rather than a code shape",
			raw:  `{"catalog_dirs": "one"}`,
			want: []string{`the "catalog_dirs" field must be an array`, "a string was sent"},
		},
		{
			name: "non-object payload names the required shape",
			raw:  `[]`,
			want: []string{"the arguments must be a JSON object", "an array was sent"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := decode(tc.raw)
			if err == nil {
				t.Fatalf("payload %s decoded cleanly; the case proves nothing", tc.raw)
			}
			got := decodeFailureGuidance("validate", payloadArguments, err)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("guidance missing %q:\n%s", want, got)
				}
			}
			assertActionableGuidance(t, "validate", tc.name, toolFailure{text: got, viaProtocol: true, failed: true})
		})
	}

	// A decode failure the route cannot classify still yields a correction
	// rather than the underlying complaint.
	got := decodeFailureGuidance("build", payloadParams, fmt.Errorf("some transport condition"))
	for _, want := range []string{"build: the params could not be read", "call it again"} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback guidance missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "some transport condition") {
		t.Errorf("fallback guidance pasted the underlying condition verbatim: %s", got)
	}
}
