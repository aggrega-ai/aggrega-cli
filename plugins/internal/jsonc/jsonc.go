// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package jsonc reads JSON with comments as JSON: it writes what JSON with
// comments admits and JSON does not as spaces, so a JSON reader reads the rest.
//
// It is the one reading of that syntax the compiler's guests do: the parser for
// JSON with comments strips a document with it and then decodes it exactly as
// the JSON parser decodes a document, and an importer reading a source written
// as JSON with comments asks the host for that parser's tree rather than
// stripping the text itself.
package jsonc

import "bytes"

// Strip returns src with what JSON with comments admits and JSON does not
// written as spaces: each comment — from // to the end of its line, and from /*
// to the next */ — and each trailing comma, a comma followed by nothing but
// whitespace and comments before the } or ] closing its object or list. Every
// line break is kept and every other byte is left where it is, so the text keeps
// its length and its lines, and a parser refusing it names the line and column
// the source itself has there. A byte inside a string is never changed, so a
// value holding // or /* is read as written, and a source that is JSON already
// is returned byte for byte.
//
// A comment opened with /* and never closed is left as written, so a parser
// refuses the source rather than reading a document cut short.
func Strip(src []byte) []byte {
	out := append([]byte(nil), src...)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
	}
	inString := false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c != '/' || i+1 >= len(out) {
			continue
		}
		switch out[i+1] {
		case '/':
			end := i
			for end < len(out) && out[end] != '\n' && out[end] != '\r' {
				end++
			}
			blank(i, end)
			i = end - 1
		case '*':
			closing := bytes.Index(out[i+2:], []byte("*/"))
			if closing < 0 {
				return stripTrailingCommas(out, i)
			}
			end := i + 2 + closing + 2
			blank(i, end)
			i = end - 1
		}
	}
	return stripTrailingCommas(out, len(out))
}

// stripTrailingCommas writes as a space each comma in text[:limit] outside a
// string that is followed by nothing but whitespace before a } or ]. Comments
// before limit are spaces already; what lies past it is left as written.
func stripTrailingCommas(text []byte, limit int) []byte {
	inString := false
	for i := 0; i < limit; i++ {
		c := text[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c != ',' {
			continue
		}
		j := i + 1
		for j < limit && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r') {
			j++
		}
		if j < limit && (text[j] == '}' || text[j] == ']') {
			text[i] = ' '
		}
	}
	return text
}
