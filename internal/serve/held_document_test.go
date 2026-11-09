// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestADocumentTheClientHoldsIsAnsweredFromWhatTheClientSent drives
// aggrega/validate, then aggrega/diagnostics, naming a document the client holds
// open — one row per place its path can lead and per disagreement between what
// the client sent and the bytes on disk — and reads back which of the two the
// answer is about.
//
// A document the client holds open is answered from what the client sent: its
// path is the name the answer is kept under, and no byte is read there. So the
// request is answered, never refused, wherever the path leads — inside the
// workspace, through a link leading out of it, or outside it named absolutely —
// and the place the path leads to is left as it was. The two content rows per
// place are what tell the sent bytes from the disk's: content that does not parse
// over a file that does draws the parse finding, and content that parses over a
// file that does not draws none, from the validation and from the diagnostics
// asked after it.
func TestADocumentTheClientHoldsIsAnsweredFromWhatTheClientSent(t *testing.T) {
	t.Parallel()

	const parses = "apiVersion: aggrega.ai/v1beta1\nkind: Feature\nmetadata:\n  name: probe/held\n" +
		"  version: \"1.0.0\"\nspec: {}\n"
	const doesNotParse = "kind: [unclosed\n"

	places := []struct {
		name string
		// file is the value the request sends, given the workspace root and the
		// directory outside it.
		file func(root, outside string) string
	}{
		{"inside the workspace", func(string, string) string { return filepath.Join("docs", "held.yaml") }},
		{"through a link leading out of the workspace", func(string, string) string { return filepath.Join("links", "held.yaml") }},
		{"outside the workspace, named absolutely", func(_, outside string) string { return filepath.Join(outside, "held.yaml") }},
	}
	contents := []struct {
		name             string
		sent, onDisk     string
		wantParseFinding bool
	}{
		{"content that does not parse over a file that does", doesNotParse, parses, true},
		{"content that parses over a file that does not", parses, doesNotParse, false},
	}

	for _, place := range places {
		for _, content := range contents {
			t.Run(place.name+": "+content.name, func(t *testing.T) {
				t.Parallel()
				root, outside := t.TempDir(), t.TempDir()
				plantFile(t, filepath.Join(root, "docs", "held.yaml"), content.onDisk)
				plantFile(t, filepath.Join(outside, "held.yaml"), content.onDisk)
				plantLink(t, outside, filepath.Join(root, "links"))
				before := treeListing(t, outside)
				srv := linkedFilesServer(t, root, "workspace: {}\n")
				file := place.file(root, outside)

				resp := callDaemonMethod(t, srv, "aggrega/validate", map[string]any{"file": file, "content": content.sent})
				if resp.Error != nil {
					t.Fatalf("validate naming %q was refused with %d: %s — a document the client holds is answered "+
						"from what it sent, wherever its path leads", file, resp.Error.Code, resp.Error.Message)
				}
				var validated validateResult
				decodeDaemonResult(t, resp, &validated)
				if got := hasParseFinding(validated.Diagnostics); got != content.wantParseFinding {
					t.Errorf("validate reported a parse finding: %v, want %v — the answer is about the bytes on "+
						"disk rather than the ones sent (diagnostics: %+v)", got, content.wantParseFinding, validated.Diagnostics)
				}

				// A buffer that does not parse is kept by no document store, so a later
				// request about it is answered as one about a file nothing validated; the
				// second request is asked where the buffer parsed.
				if !content.wantParseFinding {
					asked := callDaemonMethod(t, srv, "aggrega/diagnostics", map[string]any{"file": file})
					if asked.Error != nil {
						t.Fatalf("diagnostics naming %q was refused with %d: %s", file, asked.Error.Code, asked.Error.Message)
					}
					var held diagnosticsResult
					decodeDaemonResult(t, asked, &held)
					if hasParseFinding(held.Diagnostics) {
						t.Errorf("diagnostics reported a parse finding for content that parses — the answer is about "+
							"the bytes on disk (diagnostics: %+v)", held.Diagnostics)
					}
				}

				if after := treeListing(t, outside); strings.Join(after, "\n") != strings.Join(before, "\n") {
					t.Errorf("the directory outside the workspace changed:\nbefore:\n  %s\nafter:\n  %s",
						strings.Join(before, "\n  "), strings.Join(after, "\n  "))
				}
			})
		}
	}
}

// hasParseFinding reports whether diags carries the finding a document that does
// not parse draws: the parser's own message, which opens with its name.
func hasParseFinding(diags []Diagnostic) bool {
	for _, d := range diags {
		if strings.HasPrefix(d.Message, "yaml: ") {
			return true
		}
	}
	return false
}
