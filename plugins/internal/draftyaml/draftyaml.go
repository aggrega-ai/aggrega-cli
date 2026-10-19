// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package draftyaml carries text read from an imported source into the YAML
// drafts an importer writes by hand: it reads the text a scalar of the source's
// parsed tree is written as, and writes text as a scalar that reads back as
// exactly the text, and inside a comment that nothing a source holds can end.
// The importers that write their drafts line by line, rather than marshalling
// them, read and write every value through it, so a value is carried one way in
// every draft.
package draftyaml

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// String writes s as a double-quoted YAML scalar reading back as exactly s: a
// double quote and a backslash escaped, every printable ASCII character as it
// is, and every other character as its \u or \U escape, so no character a YAML
// reader could take for a line break, and no control character, is written raw.
// A byte that is not part of a UTF-8 sequence is written as U+FFFD.
func String(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			fmt.Fprintf(&b, `\U%08X`, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Comment writes text read from a source into a comment: as it is where it holds
// only letters, digits, spaces and the punctuation a name, a path or a variable
// reference is written with, and otherwise quoted as String writes it, so nothing
// a source holds can end the comment's line. Empty text is written "".
func Comment(text string) string {
	if text == "" {
		return `""`
	}
	for _, r := range text {
		if r >= utf8.RuneSelf || !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '@' || r == '$' ||
			r == '{' || r == '}' || r == '#' || r == '+' || r == '=' || r == ' ' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return String(text)
		}
	}
	return text
}

// CommentList writes names into a comment, each through Comment, joined by
// commas.
func CommentList(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, Comment(n))
	}
	return strings.Join(out, ", ")
}

// ScalarText returns the text a scalar of a source's parsed tree is written as:
// a string's text, and a number's or a boolean's as written. An object, a list
// and null have none.
func ScalarText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	switch v.(type) {
	case float64, bool:
		return strings.TrimSpace(string(raw)), true
	}
	return "", false
}
