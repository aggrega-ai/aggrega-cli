// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The assertions in this file hold every tool declaring a response format to the
// set of values it publishes, and they are driven from that declaration rather
// than from a list of tool names kept here.
//
// A list would be the thing that goes stale: it agrees with the surface on the
// day it is written, and a tool that gains the argument afterwards escapes the
// property instead of inheriting it — silently, because every tool the list does
// name still passes. Deriving the population from each registered tool's own
// input schema is what makes "every tool that declares it" a claim the run
// measures rather than one somebody remembered to keep true.

// responseFormatArgument is the argument name these assertions are about. It is
// the wire spelling a caller sends, which is why it is a literal here and not a
// constant borrowed from elsewhere: what is under test is the published surface.
const responseFormatArgument = "response_format"

// declaredResponseFormatEnum reports the values a tool's own input schema
// publishes for the response-format argument, and whether it declares the
// argument at all.
//
// It reads the schema the tool is advertised under rather than the Go shape the
// arguments decode into: the schema is the only thing a caller can read before
// calling, so it is the surface the refusal has to agree with.
func declaredResponseFormatEnum(t *testing.T, spec toolSpec) ([]string, bool) {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.inputSchema), &schema); err != nil {
		t.Fatalf("%s: input schema does not parse as JSON: %v", spec.name, err)
	}
	property, declared := schema.Properties[responseFormatArgument]
	if !declared {
		return nil, false
	}
	return property.Enum, true
}

// toolsDeclaringAResponseFormat returns every tool this session can invoke that
// declares the response-format argument, in the order the registries serve them.
func toolsDeclaringAResponseFormat(t *testing.T, s *server) []toolSpec {
	t.Helper()
	var out []toolSpec
	for _, spec := range registeredToolSpecs(s) {
		if _, declared := declaredResponseFormatEnum(t, spec); declared {
			out = append(out, spec)
		}
	}
	return out
}

// TestEveryToolDeclaringAResponseFormatPublishesTheTokensTheParserMatches holds
// each declaring tool's published set against the tokens the one parser matches
// against, so the schema literal and the parser cannot drift apart.
//
// They are two spellings of one set — the schema's, which a caller reads before
// calling, and the parser's, which decides what the server accepts — and nothing
// else compares them. A schema declaring a value the parser refuses denies a
// schema-constrained caller a legal call; one omitting a value the parser accepts
// hides a shape the tool will answer in. Both are silent, so both are asserted.
func TestEveryToolDeclaringAResponseFormatPublishesTheTokensTheParserMatches(t *testing.T) {
	s := newGuidanceServer(t)
	declaring := toolsDeclaringAResponseFormat(t, s)
	if len(declaring) == 0 {
		t.Fatal("no registered tool declares a response format, so these assertions measured nothing")
	}

	want := []string{responseFormatDetailed, responseFormatConcise}
	for _, spec := range declaring {
		enum, _ := declaredResponseFormatEnum(t, spec)
		if len(enum) != len(want) {
			t.Errorf("%s publishes %d response format(s) %v; the parser matches %d %v",
				spec.name, len(enum), enum, len(want), want)
			continue
		}
		published := map[string]bool{}
		for _, v := range enum {
			published[v] = true
		}
		for _, token := range want {
			if !published[token] {
				t.Errorf("%s publishes %v, which omits %q — a shape the parser accepts and no caller reading "+
					"the schema would ask for", spec.name, enum, token)
			}
		}
	}
	t.Logf("held %d tool(s) declaring %s against the parser's tokens", len(declaring), responseFormatArgument)
}

// TestEveryToolDeclaringAResponseFormatRefusesAnUndeclaredValue drives every such
// tool with values its published set rejects and requires each to be refused with
// the correction a caller acts on.
//
// The refusal is required to name three things, because a caller correcting a
// call needs all three and no two of them substitute for the third: the argument,
// so the caller knows which one to change; the value it sent, so it can tell a
// typo from a misunderstanding; and the values the tool accepts, so the next call
// is composed rather than guessed at.
//
// The wrong-case and padded rows are not variants of the unknown-value row. A
// published enumeration is an exact-match constraint, so a declared token spelled
// in another case, or with whitespace around it, is a value the schema rejects
// exactly as an invented one is — and those two are the shapes a forgiving parser
// admits while still looking correct.
func TestEveryToolDeclaringAResponseFormatRefusesAnUndeclaredValue(t *testing.T) {
	s := newGuidanceServer(t)
	declaring := toolsDeclaringAResponseFormat(t, s)
	if len(declaring) == 0 {
		t.Fatal("no registered tool declares a response format, so this assertion measured nothing")
	}

	undeclared := []struct {
		scenario string
		value    string
	}{
		{"a value the published set does not carry", "verbose"},
		{"a declared value in the wrong case", strings.ToUpper(responseFormatDetailed)},
		{"a declared value with whitespace around it", " " + responseFormatDetailed + " "},
		{"a blank value, which selects nothing and is not absence", ""},
	}

	for _, spec := range declaring {
		for _, row := range undeclared {
			t.Run(spec.name+"/"+row.scenario, func(t *testing.T) {
				payload := fmt.Sprintf(`{%q: %q}`, responseFormatArgument, row.value)
				f := callToolRaw(t, s, spec.name, payload)
				if !f.failed {
					t.Fatalf("%s accepted %q, a value its published set rejects, and answered: %q",
						spec.name, row.value, f.text)
				}
				assertActionableGuidance(t, spec.name, row.scenario, f)

				if !strings.Contains(f.text, responseFormatArgument) {
					t.Errorf("%s: the refusal does not name the argument the caller has to change: %q",
						spec.name, f.text)
				}
				if !strings.Contains(f.text, fmt.Sprintf("%q", row.value)) {
					t.Errorf("%s: the refusal does not quote the value that was sent (%q): %q",
						spec.name, row.value, f.text)
				}
				for _, token := range []string{responseFormatDetailed, responseFormatConcise} {
					if !strings.Contains(f.text, token) {
						t.Errorf("%s: the refusal does not name %q among the values it accepts: %q",
							spec.name, token, f.text)
					}
				}
			})
		}
	}
	t.Logf("drove %d tool(s) x %d undeclared value(s)", len(declaring), len(undeclared))
}

// TestNoToolRefusesADeclaredOrAbsentResponseFormat is the control that keeps the
// assertion above from passing over a tool that refuses every call.
//
// Without it, a tool broken so badly that nothing reaches it would satisfy every
// row above — each call fails, and the failures would only have to carry the
// right words. What is asserted here is the other half of the contract: an absent
// argument keeps the tool's documented default and is not a failure, and a value
// the schema publishes is never refused for being that value.
//
// It reads the accepted values out of each tool's own schema rather than naming
// them, so a tool that publishes a third value is driven with it too. The check
// is on the REFUSAL rather than on success, because most of these tools also need
// a subject and a call naming none is refused for that — which is a different
// refusal and the one this control must not mistake for its own. The two are told
// apart by the rendering of the accepted set, which only the format refusal
// carries, so nothing here pins a phrasing.
func TestNoToolRefusesADeclaredOrAbsentResponseFormat(t *testing.T) {
	s := newGuidanceServer(t)
	declaring := toolsDeclaringAResponseFormat(t, s)
	if len(declaring) == 0 {
		t.Fatal("no registered tool declares a response format, so this control measured nothing")
	}

	drove := 0
	for _, spec := range declaring {
		type legalCall struct{ scenario, payload string }
		calls := []legalCall{{"an absent response format", `{}`}}
		enum, _ := declaredResponseFormatEnum(t, spec)
		for _, value := range enum {
			calls = append(calls, legalCall{
				scenario: fmt.Sprintf("the published value %q", value),
				payload:  fmt.Sprintf(`{%q: %q}`, responseFormatArgument, value),
			})
		}
		for _, call := range calls {
			drove++
			f := callToolRaw(t, s, spec.name, call.payload)
			if !f.failed {
				continue
			}
			if strings.Contains(f.text, declaredResponseFormats()) {
				t.Errorf("%s (%s): refused a call for its response format when it had nothing to refuse: %q",
					spec.name, call.scenario, f.text)
			}
		}
	}
	t.Logf("drove %d legal response-format call(s) across %d tool(s)", drove, len(declaring))
}
