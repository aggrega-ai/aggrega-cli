// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"errors"
	"strings"
	"testing"
)

// corpusCase is one adversarial input for the segmenter. wantSegs is the
// expected segment count; parseErr marks an input the YAML parser rejects (the
// segmenter must surface the typed error, never a partial split). affixOnly
// marks a non-empty input that parses to zero documents (whitespace-only,
// comment-only): it yields one affix-only segment that preserves the bytes but
// is not a patch target.
type corpusCase struct {
	name      string
	body      string
	wantSegs  int
	parseErr  bool
	affixOnly bool
}

// segmenterCorpus is the adversarial corpus. Every structured case must satisfy
// the round-trip and patch-isolation properties; every parseErr case must
// surface ErrPipelinePatchTargetNotStructured.
func segmenterCorpus() []corpusCase {
	return []corpusCase{
		{name: "empty", body: "", wantSegs: 0},
		{name: "whitespace-only", body: "   \n  \n", wantSegs: 1, affixOnly: true},
		{name: "comment-only-whole-file", body: "# just a comment\n", wantSegs: 1, affixOnly: true},
		{name: "single-no-separator", body: "a: 1\nb: 2\n", wantSegs: 1},
		{name: "single-leading-separator", body: "---\na: 1\n", wantSegs: 1},
		{name: "two-documents", body: "a: 1\n---\nb: 2\n", wantSegs: 2},
		{name: "empty-document-between", body: "a: 1\n---\n---\nb: 2\n", wantSegs: 3},
		{name: "leading-null-document", body: "---\n---\na: 1\n", wantSegs: 2},
		{name: "dash-in-block-literal-scalar", body: "a: |\n  ---\n  not a separator\nb: 2\n", wantSegs: 1},
		{name: "dash-in-block-folded-scalar", body: "a: >\n  --- folded ---\n  text\nb: 2\n", wantSegs: 1},
		{name: "dash-in-flow-scalar", body: "a: \"--- x ---\"\n", wantSegs: 1},
		{name: "tag-directive", body: "%TAG ! tag:example.com,2000:\n---\na: 1\n", wantSegs: 1},
		{name: "yaml-directive-version-mismatch", body: "%YAML 1.2\n---\na: 1\n", parseErr: true},
		{name: "end-marker-then-document", body: "a: 1\n...\n---\nb: 2\n", wantSegs: 2},
		{name: "consecutive-separators", body: "a: 1\n---\n---\nb: 2\n", wantSegs: 3},
		{name: "no-trailing-newline", body: "a: 1", wantSegs: 1},
		{name: "bare-separator-no-newline", body: "---", wantSegs: 1},
		{name: "crlf-line-endings", body: "a: 1\r\n---\r\nb: 2\r\n", wantSegs: 2},
		{name: "bom-prefix", body: "\ufeff" + "a: 1\n", wantSegs: 1},
		{name: "leading-comment", body: "# leading\na: 1\n", wantSegs: 1},
		{name: "inter-document-comment", body: "a: 1\n---\n# mid\nb: 2\n", wantSegs: 2},
		{name: "trailing-comment", body: "a: 1\n# trailing\n", wantSegs: 1},
		{name: "comment-only-document", body: "a: 1\n---\n# only a comment\n", wantSegs: 2},
		{name: "inline-flow-documents", body: "--- {a: 1}\n--- {b: 2}\n", wantSegs: 2},
		{name: "end-marker-no-restart", body: "a: 1\n...\nb: 2\n", parseErr: true},
	}
}

// TestSplitDocCountAndTokens asserts the document count and the opaque
// positional tokens (bare filename for single-document files, "<name>#<ord>"
// otherwise) for each corpus case, and that parse-rejecting inputs surface the
// typed sentinel.
func TestSplitDocCountAndTokens(t *testing.T) {
	const name = "manifest.yaml"
	for _, tc := range segmenterCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			segs, err := Split(name, []byte(tc.body))
			if tc.parseErr {
				if err == nil {
					t.Fatalf("Split(%q) = nil error, want %v", tc.body, ErrPipelinePatchTargetNotStructured())
				}
				if !errors.Is(err, ErrPipelinePatchTargetNotStructured()) {
					t.Fatalf("Split(%q) error = %v, want errors.Is ErrPipelinePatchTargetNotStructured", tc.body, err)
				}
				if segs != nil {
					t.Fatalf("Split(%q) returned segments alongside a typed error: %#v", tc.body, segs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Split(%q) unexpected error: %v", tc.body, err)
			}
			if len(segs) != tc.wantSegs {
				t.Fatalf("Split(%q) segs = %d, want %d", tc.body, len(segs), tc.wantSegs)
			}
			for i, sg := range segs {
				// A multi-document file tokens each document "<name>#<ord>"; a
				// single segment (one document, or one affix-only segment) uses
				// the bare name.
				want := name
				if tc.wantSegs > 1 {
					want = name + "#" + itoa(i)
				}
				if sg.Token != want {
					t.Errorf("seg[%d] token = %q, want %q", i, sg.Token, want)
				}
			}
		})
	}
}

// TestSplitPartitionContiguous asserts the segments tile the original bytes
// exactly — Start[0] == 0, each End meets the next Start, the last End is the
// file length — which is the structural guarantee behind the round-trip.
func TestSplitPartitionContiguous(t *testing.T) {
	for _, tc := range segmenterCorpus() {
		if tc.parseErr || tc.wantSegs == 0 {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			segs, err := Split("f.yaml", []byte(tc.body))
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			if segs[0].Start != 0 {
				t.Errorf("first segment Start = %d, want 0", segs[0].Start)
			}
			for i := 0; i < len(segs); i++ {
				sg := segs[i]
				if sg.Start < 0 || sg.End > len(tc.body) || sg.Start > sg.End {
					t.Fatalf("seg[%d] range [%d,%d) out of bounds for len %d", i, sg.Start, sg.End, len(tc.body))
				}
				if sg.bodyStart < sg.Start || sg.bodyStart > sg.bodyEnd || sg.bodyEnd > sg.End {
					t.Fatalf("seg[%d] sub-ranges not nested: Start=%d bodyStart=%d bodyEnd=%d End=%d",
						i, sg.Start, sg.bodyStart, sg.bodyEnd, sg.End)
				}
				if i+1 < len(segs) && sg.End != segs[i+1].Start {
					t.Errorf("seg[%d] End=%d != seg[%d] Start=%d (gap or overlap)", i, sg.End, i+1, segs[i+1].Start)
				}
			}
			if segs[len(segs)-1].End != len(tc.body) {
				t.Errorf("last segment End = %d, want %d", segs[len(segs)-1].End, len(tc.body))
			}
		})
	}
}

// TestRoundTripProperty asserts join(split(x)) == x byte-for-byte for every
// structured corpus case. join is ApplyBack with no patches.
func TestRoundTripProperty(t *testing.T) {
	for _, tc := range segmenterCorpus() {
		if tc.parseErr {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			segs, err := Split("f.yaml", body)
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			got := ApplyBack(body, segs, nil)
			if string(got) != tc.body {
				t.Fatalf("round-trip mismatch\n orig=%q\n  got=%q", tc.body, string(got))
			}
		})
	}
}

// TestBodyStartLandsOnContent asserts bodyStart points at the document's first
// content token, not at the `---` separator or a leading comment — the
// correction that the leading affix (separator + comments) is preserved as
// affix, never folded into the re-serialized body.
func TestBodyStartLandsOnContent(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		docIndex int
		wantLead string // expected leading affix of the doc at docIndex
		wantBody string // expected body prefix at docIndex
	}{
		{"leading-comment", "# hi\na: 1\n", 0, "# hi\n", "a: 1\n"},
		{"inter-doc-comment", "a: 1\n---\n# mid\nb: 2\n", 1, "---\n# mid\n", "b: 2\n"},
		{"empty-doc-then-content", "a: 1\n---\n---\nb: 2\n", 2, "---\n", "b: 2\n"},
		{"post-end-marker-doc", "a: 1\n...\n---\n# c\nb: 2\n", 1, "---\n# c\n", "b: 2\n"},
		{"separator-then-content", "a: 1\n---\nb: 2\n", 1, "---\n", "b: 2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			segs, err := Split("f.yaml", body)
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			if tc.docIndex >= len(segs) {
				t.Fatalf("docIndex %d out of range (%d docs)", tc.docIndex, len(segs))
			}
			sg := segs[tc.docIndex]
			gotLead := string(body[sg.Start:sg.bodyStart])
			gotBody := string(body[sg.bodyStart:sg.bodyEnd])
			if gotLead != tc.wantLead {
				t.Errorf("leading affix = %q, want %q (%s)", gotLead, tc.wantLead, segmentDebugString(body, sg))
			}
			if !strings.HasPrefix(gotBody, strings.TrimRight(tc.wantBody, "\n")) {
				t.Errorf("body = %q, want prefix %q (%s)", gotBody, tc.wantBody, segmentDebugString(body, sg))
			}
		})
	}
}

// TestPatchIsolation asserts the patch-isolation property and reports what
// happens to a patched document's affixes — the hardening-mandate cases. For
// each input every OTHER document's original byte-range must survive verbatim,
// and the patched document's leading and trailing affixes (separator, end
// marker, comments) must be preserved while only its body is replaced.
func TestPatchIsolation(t *testing.T) {
	const replacement = "PATCHED: true\n"
	cases := []struct {
		name      string
		body      string
		patchDoc  int
		wantLead  string
		wantTrail string
	}{
		{"trailing-end-marker", "a: 1\n...\n---\nb: 2\n", 0, "", "...\n"},
		{"trailing-comment", "a: 1\n# trailing in doc0\n---\nb: 2\n", 0, "", "# trailing in doc0\n"},
		{"doc-preceded-by-null", "a: 1\n---\n---\nb: 2\n", 2, "---\n", ""},
		{"inter-doc-comment-on-target", "a: 1\n---\n# mid\nb: 2\n", 1, "---\n# mid\n", ""},
		{"both-affixes", "x: 0\n---\n# lead\ny: 9\n...\n", 1, "---\n# lead\n", "...\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			segs, err := Split("f.yaml", body)
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			if tc.patchDoc >= len(segs) {
				t.Fatalf("patchDoc %d out of range (%d docs)", tc.patchDoc, len(segs))
			}
			target := segs[tc.patchDoc]

			// Affix-fidelity report: the patched segment's affixes are what the
			// apply-back preserves verbatim.
			gotLead := string(body[target.Start:target.bodyStart])
			gotTrail := string(body[target.bodyEnd:target.End])
			if gotLead != tc.wantLead {
				t.Errorf("patched-doc leading affix = %q, want %q", gotLead, tc.wantLead)
			}
			if gotTrail != tc.wantTrail {
				t.Errorf("patched-doc trailing affix = %q, want %q", gotTrail, tc.wantTrail)
			}

			out := ApplyBack(body, segs, map[string][]byte{target.Token: []byte(replacement)})

			// Reconstruct the exact expected output: every other segment
			// verbatim, the patched segment = lead + replacement + trail.
			var exp strings.Builder
			for i, sg := range segs {
				if i == tc.patchDoc {
					exp.WriteString(gotLead)
					exp.WriteString(replacement)
					exp.WriteString(gotTrail)
					continue
				}
				exp.Write(body[sg.Start:sg.End])
			}
			if string(out) != exp.String() {
				t.Fatalf("apply-back mismatch\n  got=%q\n want=%q", string(out), exp.String())
			}

			// Every other document's original bytes must appear unchanged.
			for i, sg := range segs {
				if i == tc.patchDoc {
					continue
				}
				orig := string(body[sg.Start:sg.End])
				if orig == "" {
					continue
				}
				if !strings.Contains(string(out), orig) {
					t.Errorf("sibling doc[%d] bytes %q not preserved verbatim in output %q", i, orig, string(out))
				}
			}
		})
	}
}

// TestPatchInlineFlowSeparatorSurvives is the inline-flow apply-back case: when
// a separator and content share a line (`--- {b: 2}`), patching that document
// must keep the `---` separator (it is structurally required for a non-first
// document) so the patched file still parses as two documents — the column-aware
// bodyStart puts the `--- ` bytes into the leading affix rather than the body.
func TestPatchInlineFlowSeparatorSurvives(t *testing.T) {
	body := []byte("--- {a: 1}\n--- {b: 2}\n")
	segs, err := Split("f.yaml", body)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}

	// The second document's leading affix must carry the inline `--- `.
	doc1 := segs[1]
	gotLead := string(body[doc1.Start:doc1.bodyStart])
	if gotLead != "--- " {
		t.Fatalf("doc#1 leading affix = %q, want %q", gotLead, "--- ")
	}

	out := ApplyBack(body, segs, map[string][]byte{doc1.Token: []byte("{b: 99}\n")})

	// (1) The patched output still parses as two documents — the `---` survived.
	docs, derr := decodeAllDocs(string(out))
	if derr != nil {
		t.Fatalf("patched output re-parse failed: %v\noutput=%q", derr, string(out))
	}
	if len(docs) != 2 {
		t.Fatalf("patched output has %d documents, want 2 (separator lost?)\noutput=%q", len(docs), string(out))
	}

	// (2) doc#0 stays byte-identical.
	doc0 := segs[0]
	if !strings.Contains(string(out), string(body[doc0.Start:doc0.End])) {
		t.Errorf("doc#0 bytes %q not preserved verbatim in %q", string(body[doc0.Start:doc0.End]), string(out))
	}

	// (3) the patched doc#1 carries the patched body.
	if !strings.Contains(string(out), "{b: 99}") {
		t.Errorf("patched body {b: 99} not present in output %q", string(out))
	}

	// Exact reconstruction: doc#0 verbatim + doc#1 affix + patched body.
	want := string(body[doc0.Start:doc0.End]) + "--- " + "{b: 99}\n"
	if string(out) != want {
		t.Fatalf("apply-back mismatch\n  got=%q\n want=%q", string(out), want)
	}
}

// TestColumnAwareBodyStartNoBlockRegression asserts the column-aware bodyStart
// does not shift block-style documents whose root content sits at column 1: for
// every structured corpus case, each body's first byte is a non-space,
// non-separator content byte (never a leading `-` of a `---` and never a space),
// confirming the `--- `/indent prefix is in the affix and the column adjustment
// is zero for column-1 content.
func TestColumnAwareBodyStartNoBlockRegression(t *testing.T) {
	for _, tc := range segmenterCorpus() {
		if tc.parseErr || tc.affixOnly {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			segs, err := Split("f.yaml", body)
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			for i, sg := range segs {
				if sg.bodyStart == sg.bodyEnd {
					continue // empty/null document — no body byte to inspect
				}
				first := body[sg.bodyStart]
				// The body must begin at content, not at a separator's `-` or a
				// leading space/tab that belongs in the affix.
				if first == '-' && strings.HasPrefix(string(body[sg.bodyStart:]), "---") {
					t.Errorf("seg[%d] body begins at a `---` separator: %q", i, segmentDebugString(body, sg))
				}
				if first == ' ' || first == '\t' {
					t.Errorf("seg[%d] body begins with whitespace (indent should be affix): %q", i, segmentDebugString(body, sg))
				}
			}
		})
	}
}

// TestSingleSourceDecoder is a parity guard: the segmenter and the provenance
// splice must agree on document count for every structured corpus case,
// confirming one decoder (decodeAllDocsWithLines, which decodeAllDocs delegates
// to) governs both paths rather than two divergent decoders.
func TestSingleSourceDecoder(t *testing.T) {
	for _, tc := range segmenterCorpus() {
		if tc.parseErr || tc.affixOnly {
			// A non-empty zero-document file yields one affix-only segment, so
			// segment count intentionally differs from the parsed-document
			// count of zero; the parity check applies to document-bearing files.
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			segs, err := Split("f.yaml", []byte(tc.body))
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			docs, derr := decodeAllDocs(tc.body)
			if derr != nil {
				t.Fatalf("decodeAllDocs: %v", derr)
			}
			if len(segs) != len(docs) {
				t.Fatalf("segment count %d != decodeAllDocs count %d", len(segs), len(docs))
			}
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
