// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore_test

import (
	"bytes"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
)

// TestParseJSONC_LeadingByteOrderMark proves a JSON-with-comments source opening
// with the UTF-8 byte-order mark yields the tree the same source yields without
// it, and that a later refusal reports the same diagnostic with or without the
// mark, so its line and column count from the first character after the mark.
func TestParseJSONC_LeadingByteOrderMark(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	const body = "{\n  // the image\n  \"replicas\": 3, \"image\": \"nginx\",\n  \"enabled\": true,\n}"
	const broken = "{\n  \"replicas\": }"

	plain := parsercore.ParseJSONC([]byte(body))
	marked := parsercore.ParseJSONC([]byte(bom + body))
	if len(plain.Diagnostics) != 0 || len(marked.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: plain=%+v marked=%+v", plain.Diagnostics, marked.Diagnostics)
	}
	if !bytes.Equal(plain.Tree, marked.Tree) {
		t.Fatalf("tree differs with a leading mark\n plain: %s\nmarked: %s", plain.Tree, marked.Tree)
	}

	refusedPlain := parsercore.ParseJSONC([]byte(broken))
	refusedMarked := parsercore.ParseJSONC([]byte(bom + broken))
	if len(refusedPlain.Diagnostics) == 0 || len(refusedMarked.Diagnostics) == 0 {
		t.Fatalf("malformed source produced no diagnostic: plain=%+v marked=%+v", refusedPlain.Diagnostics, refusedMarked.Diagnostics)
	}
	if refusedPlain.Diagnostics[0].Message != refusedMarked.Diagnostics[0].Message {
		t.Fatalf("refusal differs with a leading mark\n plain: %s\nmarked: %s",
			refusedPlain.Diagnostics[0].Message, refusedMarked.Diagnostics[0].Message)
	}
}

// TestParseJSONC_RefusalNamesLineAndColumn proves a JSON-with-comments source
// refused at a character names the line and the column of that character, counted
// from the first character after a byte-order mark and over the comments the
// source holds, in the source's own characters, so a raw line break inside a string — a string written over two
// lines — is reported where the string breaks. A line ending in a carriage return
// and a line feed counts as one line.
func TestParseJSONC_RefusalNamesLineAndColumn(t *testing.T) {
	for _, row := range []struct {
		name, src, want string
	}{
		{"a line break inside a string", "{\n  \"a\": \"x\ny\"\n}",
			`json parse: invalid character '\n' in string literal at line 2, column 10`},
		{"after a comment, with a byte-order mark and CRLF line ends",
			"\xef\xbb\xbf// note\r\n{\r\n  \"é\": \"x\r\ny\"\r\n}",
			`json parse: invalid character '\r' in string literal at line 3, column 10`},
		{"a value missing", "{\n  \"a\": }", `json parse: invalid character '}' looking for beginning of value at line 2, column 8`},
		{"after a block comment holding a character of two bytes", "/* é */ {\"a\": }",
			`json parse: invalid character '}' looking for beginning of value at line 1, column 15`},
		{"a source cut short", "{\"a\": [", "json parse: unexpected EOF"},
	} {
		out := parsercore.ParseJSONC([]byte(row.src))
		if len(out.Diagnostics) != 1 || out.Diagnostics[0].Message != row.want {
			t.Errorf("%s: diagnostics %+v, want one reading %q", row.name, out.Diagnostics, row.want)
		}
	}
}
