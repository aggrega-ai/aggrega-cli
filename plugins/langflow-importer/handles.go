// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// editorHandles writes each edge's handles as Langflow's editor writes them, so
// the editor keeps the edge when it opens the flow.
//
// An edge of a Langflow flow names the output it leaves and the input it
// reaches twice: as objects under data.sourceHandle and data.targetHandle, and
// as text under sourceHandle and targetHandle — the object written as JSON with
// every double quote written as "œ". When the editor opens a flow it writes,
// for each edge, the handles it expects from the nodes the edge joins, in its
// own spelling — the keys in order, no space between the tokens — and compares
// the edge's text with them character for character: an edge whose text
// differs is dropped from the flow as an invalid connection, and the flow is
// saved without it. A flow the editor wrote itself is spelled so already. A
// flow written by Langflow's own Python — its starter projects among them —
// writes a space after every colon and every comma, and the platform loads
// such a flow from a file as it is written, so every one of its edges would be
// dropped the first time the flow is opened.
//
// So the text of each handle is written anew in the editor's spelling: the same
// handle, read back as JSON exactly as the editor reads it, written as the
// editor writes JSON. A handle already spelled so is left as written, and so is
// one the editor's reading cannot read or one holding a number, whose spelling
// the editor writes by rules this importer does not reproduce. The edits are
// the platform's Respell, which the plugin kit writes into the carried flow and
// reports with the sentence returned.
func editorHandles(tree json.RawMessage) ([]importer.Edit, string) {
	var top struct {
		Data struct {
			Edges []json.RawMessage `json:"edges"`
		} `json:"data"`
	}
	if json.Unmarshal(tree, &top) != nil {
		return nil, ""
	}
	var edits []importer.Edit
	respelled := 0
	for i, raw := range top.Data.Edges {
		var edge map[string]json.RawMessage
		if json.Unmarshal(raw, &edge) != nil {
			continue
		}
		touched := false
		for _, key := range []string{"sourceHandle", "targetHandle"} {
			var text string
			if json.Unmarshal(edge[key], &text) != nil {
				continue
			}
			spelled, ok := editorSpelling(text)
			if !ok || spelled == text {
				continue
			}
			edits = append(edits, importer.Edit{
				Pointer: importer.Pointer("data", "edges", strconv.Itoa(i), key),
				Value:   importer.String(spelled),
			})
			touched = true
		}
		if touched {
			respelled++
		}
	}
	if respelled == 0 {
		return nil, ""
	}
	return edits, fmt.Sprintf("the handles of %s written as Langflow's editor writes them — the same handles, their "+
		"keys in order with no space between them — since the editor compares an edge's handles with the ones it "+
		"writes for the nodes the edge joins, character for character, when it opens the flow, and drops every edge "+
		"whose handles differ", edgesPhrase(respelled, len(top.Data.Edges)))
}

// edgesPhrase names how many of a flow's edges are written anew, out of all.
func edgesPhrase(respelled, all int) string {
	if all == 1 {
		return "its one edge"
	}
	if respelled == all {
		return fmt.Sprintf("all %d of its edges", all)
	}
	if respelled == 1 {
		return fmt.Sprintf("one of its %d edges", all)
	}
	return fmt.Sprintf("%d of its %d edges", respelled, all)
}

// handleQuote is the character a handle's text writes in place of every double
// quote.
const handleQuote = "œ"

// editorSpelling writes a handle's text as Langflow's editor writes it, and
// false where the editor's reading cannot read the text, or where it holds a
// number. The editor reads a handle by writing every "œ" as a double quote and
// parsing the result as one JSON value, and writes one as JSON — an object's
// keys sorted, no whitespace — with every double quote written as "œ".
func editorSpelling(text string) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(strings.ReplaceAll(text, handleQuote, `"`)))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) != nil {
		return "", false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", false
	}
	var b strings.Builder
	if !writeEditorJSON(&b, value) {
		return "", false
	}
	return strings.ReplaceAll(b.String(), `"`, handleQuote), true
}

// writeEditorJSON writes value as the editor writes JSON: null, a boolean and a
// string as JavaScript's JSON.stringify writes them, an array's elements in
// order and an object's members with their keys sorted as JavaScript sorts
// strings, by UTF-16 code unit, each with no whitespace between its tokens. It
// reports false for a value holding a number.
func writeEditorJSON(b *strings.Builder, value any) bool {
	if value == nil {
		b.WriteString("null")
		return true
	}
	if flag, ok := value.(bool); ok {
		b.WriteString(strconv.FormatBool(flag))
		return true
	}
	if text, ok := value.(string); ok {
		writeJSString(b, text)
		return true
	}
	if list, ok := value.([]any); ok {
		return writeEditorList(b, list)
	}
	if object, ok := value.(map[string]any); ok {
		return writeEditorObject(b, object)
	}
	// A number, which the editor writes by JavaScript's own rules.
	return false
}

// writeEditorList writes an array as writeEditorJSON does.
func writeEditorList(b *strings.Builder, list []any) bool {
	b.WriteByte('[')
	for i, e := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		if !writeEditorJSON(b, e) {
			return false
		}
	}
	b.WriteByte(']')
	return true
}

// writeEditorObject writes an object as writeEditorJSON does, its keys sorted
// by UTF-16 code unit, as JavaScript sorts strings.
func writeEditorObject(b *strings.Builder, object map[string]any) bool {
	keys := make([]string, 0, len(object))
	for k := range object {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y string) int {
		return slices.Compare(utf16.Encode([]rune(x)), utf16.Encode([]rune(y)))
	})
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSString(b, k)
		b.WriteByte(':')
		if !writeEditorJSON(b, object[k]) {
			return false
		}
	}
	b.WriteByte('}')
	return true
}

// writeJSString writes s as JavaScript's JSON.stringify writes a string: a
// double quote and a backslash escaped, the five control characters with a
// short escape written so, every other control character as a \u escape in
// lower-case hexadecimal, and every other character as it is.
func writeJSString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
