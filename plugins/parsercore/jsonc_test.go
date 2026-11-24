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
