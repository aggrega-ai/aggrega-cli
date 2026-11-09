// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package draftyaml carries text read from an imported source into the YAML
// drafts an importer writes by hand: it reads the text a scalar of the source's
// parsed tree is written as, and writes text as a scalar that reads back as
// exactly the text, and inside a comment that nothing a source holds can end.
// The importers that write their drafts line by line, rather than marshalling
// them, read and write every value through it, so a value is carried one way in
// every draft, and write every comment line of a drafted Aggregate through
// CommentLine.
package draftyaml

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// CommentLine writes one comment line of a drafted Aggregate — indent, the hash,
// and text, each text read from a source in it written first through Comment —
// escaped so the line reads back as written once the compiler has evaluated the
// Aggregate's text as a template, its comments included. It is the plugin kit's
// importer.CommentLine, the one writer of an Aggregate's comment lines: the kit's
// own platform drafts write theirs through it too, and the kit cannot reach this
// package, so the writer lives there and is named here beside the drafts' other
// writers. A Release is not evaluated as a template, so its comments are written
// as they are.
var CommentLine = importer.CommentLine

// String is the plugin kit's importer.QuotedString, the one writer of a
// double-quoted scalar, whose documentation is the contract: the kit's own
// platform drafts write theirs through it too, and the kit cannot reach this
// package, so the writer lives there and is named here beside the drafts' other
// writers.
var String = importer.QuotedString

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
