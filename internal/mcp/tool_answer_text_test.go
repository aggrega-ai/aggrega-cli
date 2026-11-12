// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// What every tool's structured payload carries besides its fields: the answer's
// text, whole, as text_answer.
//
// A client may hand its model the structured payload of an answer and not its
// text, and a sentence only the text carries then reaches that model nowhere. So
// each answer is read here the way such a client reads it, off the wire, and
// held to one statement: where an answer carries a structured payload, that
// payload is a JSON object whose text_answer is a string equal to the answer's
// one text block. An answer carrying no structured payload is its text alone,
// and is counted rather than judged. No tool is exempt: lint's payload, which
// carries a summary of counts of its own, is held to the same statement.
//
// 🔴 What this check covers, and what it does not:
//
//   - Every tool either transport offers is asked at least one thing, by the
//     calls the every-tool host-location comparison asks, over the process's own
//     streams through the message loop a launching client speaks to and over a
//     real listener. A tool offered on either transport and asked nothing fails.
//   - The command tools, which exist over the process's own streams alone, are
//     each called once with no arguments against a command runner standing in for
//     the binary, so their own payload shape is judged and the command they would
//     run is not.
//   - An output schema a tool advertises must declare text_answer as a required
//     string. A tool advertising none is not judged by that half, and the count of
//     schemas read is logged, so a run that read none says so.
//   - Which sentences a tool composes is each tool's own concern and is not judged
//     here; that the payload carries them, every one, in order, is.

// textAnswerComplaint reads one answer as a client receives it and says why its
// structured payload does not carry its text as text_answer. judged reports
// whether there was a payload to read: a protocol error and an answer carrying no
// structured payload are neither complaints nor judgements.
func textAnswerComplaint(a hostLocationAnswer) (complaint string, judged bool) {
	if a.protocolError != "" || len(a.structuredBytes) == 0 {
		return "", false
	}
	if len(a.texts) != 1 {
		return fmt.Sprintf("the answer carries %d text blocks, so no one string is its text", len(a.texts)), true
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(a.structuredBytes, &fields); err != nil {
		return fmt.Sprintf("the structured payload is not a JSON object (%v): %s", err, a.structuredBytes), true
	}
	raw, carried := fields[textAnswerKey]
	if !carried {
		return fmt.Sprintf("the structured payload carries no %s, so a client reading only it never reads %q",
			textAnswerKey, a.texts[0]), true
	}
	var textAnswer string
	if err := json.Unmarshal(raw, &textAnswer); err != nil {
		return fmt.Sprintf("the structured payload's %s is %s, not a string: the payload carries a %s of its own, "+
			"so the text has no field to travel in", textAnswerKey, raw, textAnswerKey), true
	}
	if textAnswer != a.texts[0] {
		return fmt.Sprintf("the structured payload's %s differs from the text %s", textAnswerKey,
			firstDifference([]byte(a.texts[0]), []byte(textAnswer))), true
	}
	return "", true
}

// outputSchemaComplaint says why an advertised output schema does not declare
// text_answer as a required string.
func outputSchemaComplaint(schema json.RawMessage) string {
	var declared struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &declared); err != nil {
		return fmt.Sprintf("the output schema is not a JSON Schema object (%v): %s", err, schema)
	}
	if property, present := declared.Properties[textAnswerKey]; !present || property.Type != "string" {
		return fmt.Sprintf("the output schema declares no %s of type string: %s", textAnswerKey, schema)
	}
	if !slices.Contains(declared.Required, textAnswerKey) {
		return fmt.Sprintf("the output schema does not require %s: %s", textAnswerKey, schema)
	}
	return ""
}

// answerOverStandardInputAndOutput sends one call to the message loop a client
// that launched this process speaks to, as one framed line, and reads the frame
// that answers it, as that client receives it.
func answerOverStandardInputAndOutput(t *testing.T, s *server, tool string, args map[string]any) hostLocationAnswer {
	t.Helper()
	frame, raw := frameOverStandardInputAndOutput(t, s,
		modernRequest(t, 1, "tools/call", map[string]any{"name": tool, "arguments": args}))
	if frame.Error != nil {
		return hostLocationAnswer{protocolError: frame.Error.Message, raw: raw}
	}
	answer := decodedToolResult(t, tool, frame.Result)
	answer.raw = raw
	return answer
}

// frameOverStandardInputAndOutput runs the message loop over one request line and
// returns the frame answering it, and every byte the loop wrote.
func frameOverStandardInputAndOutput(t *testing.T, s *server, request string) (rpcResponse, []byte) {
	t.Helper()
	var out bytes.Buffer
	s.out = &out
	if err := s.serve(context.Background(), strings.NewReader(request+"\n")); err != nil {
		t.Fatalf("the message loop ended with %v answering %s", err, request)
	}
	return responseFor(t, parseFrames(t, out.String()), 1), out.Bytes()
}

// TestEveryToolAnswerCarriesItsTextInItsStructuredPart drives every tool over
// both transports, and every command tool over the process's own streams, and
// fails where an answer's structured payload does not carry the answer's text as
// text_answer, or where a tool advertises an output schema that does not declare
// it.
//
// 🔴 PLANTED DEFECT: in handleToolsCall (internal/mcp/tools.go), append a sentence
// to the text of one tool's result after withTextAnswer has run — for the result
// of config-show, say, " A planted sentence." added to Content[0].Text on the line
// before the return. Every config-show row fails on both transports, naming where
// the text_answer and the text part. Remove the withTextAnswer call instead, and
// every row that answered with a structured payload fails, the payload carrying no
// text_answer.
//
// Not parallel: the fixture sets the environment the configuration cascade reads.
func TestEveryToolAnswerCarriesItsTextInItsStructuredPart(t *testing.T) {
	fx := newHostLocationFixture(t)
	streams := fx.overTheProcessStreams(t)
	listener, credential := fx.overAListener(t)

	var driven []string
	for _, group := range hostLocationEveryTool() {
		driven = append(driven, group.tool)
	}
	offered := append(listener.listed(t, credential), listener.surfaced(t, credential)...)
	for _, spec := range streams.servedTools(context.Background()) {
		offered = append(offered, spec.name)
	}
	var undriven []string
	for _, name := range offered {
		if !slices.Contains(driven, name) && !slices.Contains(undriven, name) {
			undriven = append(undriven, name)
		}
	}
	for _, name := range undriven {
		t.Errorf("%q is offered and nothing here asks it anything, so whether its structured payload carries its "+
			"text is judged by nothing", name)
	}

	sides := []struct {
		name   string
		answer func(t *testing.T, tool string, args map[string]any) hostLocationAnswer
	}{
		{"over standard input and output", func(t *testing.T, tool string, args map[string]any) hostLocationAnswer {
			return answerOverStandardInputAndOutput(t, streams, tool, args)
		}},
		{"over a listener", func(t *testing.T, tool string, args map[string]any) hostLocationAnswer {
			return answerOverTheListener(t, listener, credential, tool, args)
		}},
	}
	judgedAnywhere := 0
	for _, group := range hostLocationEveryTool() {
		t.Run(group.tool, func(t *testing.T) {
			if group.prepare != nil {
				group.prepare(t)
			}
			judged := make([]int, len(sides))
			textOnly := make([]int, len(sides))
			for _, call := range group.calls {
				t.Run(call.scenario, func(t *testing.T) {
					for i, side := range sides {
						fx.reset(t)
						complaint, carried := textAnswerComplaint(side.answer(t, group.tool, call.args))
						if complaint != "" {
							t.Errorf("%s, %s, %s: %s", group.tool, call.scenario, side.name, complaint)
						}
						if carried {
							judged[i]++
						} else {
							textOnly[i]++
						}
					}
				})
			}
			for i, side := range sides {
				judgedAnywhere += judged[i]
				t.Logf("%s %s: %d answer(s) with a structured payload judged, %d answered with text alone",
					group.tool, side.name, judged[i], textOnly[i])
			}
		})
	}
	if judgedAnywhere == 0 {
		t.Error("no answer on either transport carried a structured payload, so nothing was judged")
	}

	t.Run("every command tool, over standard input and output", func(t *testing.T) {
		s := newCommandToolServer(t, func(_ context.Context, _ string, _ []string, stdout, stderr io.Writer) error {
			_, _ = io.WriteString(stdout, "the command's own output\n")
			_, _ = io.WriteString(stderr, "a note the command wrote on its error stream\n")
			return nil
		})
		if len(s.commandTools) == 0 {
			t.Fatal("precondition: the fixture command surface derives no command tool, so nothing below is judged")
		}
		judged := 0
		for _, spec := range s.commandTools {
			complaint, carried := textAnswerComplaint(answerOverStandardInputAndOutput(t, s, spec.name, map[string]any{}))
			if complaint != "" {
				t.Errorf("%s: %s", spec.name, complaint)
			}
			if carried {
				judged++
			}
		}
		if judged == 0 {
			t.Error("no command tool answered with a structured payload, so nothing was judged")
		}
		t.Logf("%d of %d command tool(s) answered with a structured payload and were judged", judged, len(s.commandTools))
	})

	t.Run("every output schema a tool advertises declares text_answer", func(t *testing.T) {
		listings := []struct {
			name string
			list func(t *testing.T) json.RawMessage
		}{
			{"over standard input and output", func(t *testing.T) json.RawMessage {
				frame, raw := frameOverStandardInputAndOutput(t, streams, modernRequest(t, 1, "tools/list", nil))
				if frame.Error != nil {
					t.Fatalf("tools/list was answered with an error envelope: %s", raw)
				}
				return frame.Result
			}},
			{"over a listener", func(t *testing.T) json.RawMessage {
				headers := modernHeaders("tools/list", "")
				headers[headerAuthorization] = credential
				got := listener.send(t, http.MethodPost, endpointPath, headers, modernRequest(t, 1, "tools/list", nil))
				if got.status != http.StatusOK || got.frame.Error != nil {
					t.Fatalf("tools/list was answered %d (body %q)", got.status, got.raw)
				}
				return got.frame.Result
			}},
		}
		read := 0
		for _, listing := range listings {
			var listed struct {
				Tools []struct {
					Name         string          `json:"name"`
					OutputSchema json.RawMessage `json:"outputSchema"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(listing.list(t), &listed); err != nil {
				t.Fatalf("%s: tools/list does not decode: %v", listing.name, err)
			}
			if len(listed.Tools) == 0 {
				t.Fatalf("%s: tools/list lists no tool, so no schema could be read", listing.name)
			}
			for _, tool := range listed.Tools {
				if len(tool.OutputSchema) == 0 {
					continue
				}
				read++
				if complaint := outputSchemaComplaint(tool.OutputSchema); complaint != "" {
					t.Errorf("%s, %s: %s", listing.name, tool.Name, complaint)
				}
			}
		}
		t.Logf("%d advertised output schema(s) read across both transports", read)
	})
}

// TestWithTextAnswerWritesTheTextFirstAndLeavesEveryOtherByte holds the copy's
// encoding: text_answer is written first, as the JSON encoding of the one text
// block, and every byte of the payload a tool composed follows it unchanged; a
// payload with a summary of its own, as lint's carries, gains text_answer beside
// it; a result whose shape the copy does not write into is handed on exactly as
// it came.
//
// 🔴 PLANTED DEFECT: in withTextAnswer, decode the payload into a map and encode
// the map with text_answer added, rather than writing the key ahead of the
// payload's own bytes. The row whose fields are not in alphabetical order fails,
// its fields reordered.
func TestWithTextAnswerWritesTheTextFirstAndLeavesEveryOtherByte(t *testing.T) {
	t.Parallel()
	const text = "Configuration in force for <the workspace> & its layers.\n\n  one\ttwo \"quoted\""
	encodedText, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("encode the text: %v", err)
	}
	result := func(payload string, texts ...string) *callToolResult {
		res := &callToolResult{StructuredContent: json.RawMessage(payload)}
		for _, block := range texts {
			res.Content = append(res.Content, toolTextContent{Type: "text", Text: block})
		}
		return res
	}
	for _, tc := range []struct {
		name string
		in   *callToolResult
		// want is the structured payload the copy hands on; empty wants the one it
		// was given.
		want string
	}{
		{name: "fields not in alphabetical order", in: result(`{"zeta":1,"alpha":{"b":2,"a":1},"mid":"x"}`, text),
			want: `{"text_answer":` + string(encodedText) + `,"zeta":1,"alpha":{"b":2,"a":1},"mid":"x"}`},
		{name: "an object with no field", in: result(`{}`, text),
			want: `{"text_answer":` + string(encodedText) + `}`},
		{name: "a failure carrying a payload", in: func() *callToolResult {
			res := result(`{"files":[]}`, text)
			res.IsError = true
			return res
		}(), want: `{"text_answer":` + string(encodedText) + `,"files":[]}`},
		{name: "a payload with a summary of counts of its own, as lint's carries",
			in: result(`{"findings":[],"summary":{"entitiesChecked":1,"findings":0,"errors":0,"warnings":0,"infos":0}}`, text),
			want: `{"text_answer":` + string(encodedText) +
				`,"findings":[],"summary":{"entitiesChecked":1,"findings":0,"errors":0,"warnings":0,"infos":0}}`},
		{name: "a payload already carrying the key", in: result(`{"findings":[],"text_answer":{"warn":1}}`, text)},
		{name: "a payload that is not an object", in: result(`["a","b"]`, text)},
		{name: "a payload that is null", in: result(`null`, text)},
		{name: "two text blocks", in: result(`{"a":1}`, text, "a second block")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			given := string(tc.in.StructuredContent)
			want := tc.want
			if want == "" {
				want = given
			}
			if got := string(withTextAnswer(tc.in).StructuredContent); got != want {
				t.Errorf("the structured payload handed on is\n  %s\nwant\n  %s", got, want)
			}
		})
	}
	t.Run("a result carrying no structured payload", func(t *testing.T) {
		t.Parallel()
		res := &callToolResult{Content: []toolTextContent{{Type: "text", Text: text}}}
		if got := withTextAnswer(res).StructuredContent; got != nil {
			t.Errorf("a text-only answer was given a structured payload: %s", got)
		}
	})
	t.Run("no result", func(t *testing.T) {
		t.Parallel()
		if got := withTextAnswer(nil); got != nil {
			t.Errorf("no result came back as %+v", got)
		}
	})
}
