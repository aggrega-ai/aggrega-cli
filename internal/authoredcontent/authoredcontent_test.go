// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package authoredcontent

import (
	"bytes"
	"strings"
	"testing"
)

// TestTheReportedFormRoundTripsContentVerbatim drives the reported form through
// both halves of its one implementation and requires the content back
// unchanged, including the shapes a hand-rolled framing gets wrong.
//
// The rows are chosen for what they would break rather than for coverage: a
// document whose content opens with a comment or a YAML document marker is
// exactly what a delimiter-based framing cannot carry, and a generated catalog
// entity opens with a comment banner, so that row is the shipped case rather
// than a hypothetical one.
func TestTheReportedFormRoundTripsContentVerbatim(t *testing.T) {
	for _, row := range []struct {
		name string
		docs []Document
	}{
		{
			name: "content opening with a comment banner, as a generated entity does",
			docs: []Document{{
				Path:    "/out/linter/publishing-linter/1.0.0/plugin.yaml",
				Content: "# generated; DO NOT EDIT\napiVersion: aggrega.ai/v1beta1\nkind: Plugin\n",
			}},
		},
		{
			name: "content carrying a document marker and a trailing blank line",
			docs: []Document{{
				Path:    "/home/aggrega/config.yaml",
				Content: "---\nworkspace: {}\nregistries:\n  - host: hub.aggrega.ai\n\n",
			}},
		},
		{
			name: "several documents, whose order is part of the answer",
			docs: []Document{
				{Path: "/out/a.yaml", Content: "kind: A\n"},
				{Path: "/out/b.yaml", Content: "kind: B\n"},
			},
		},
		{
			name: "content with no trailing newline",
			docs: []Document{{Path: "/out/c.yaml", Content: "kind: C"}},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Report(&buf, row.docs); err != nil {
				t.Fatalf("Report: %v", err)
			}
			got, err := Decode(buf.Bytes())
			if err != nil {
				t.Fatalf("Decode: %v\n%s", err, buf.String())
			}
			if len(got) != len(row.docs) {
				t.Fatalf("reported %d documents, decoded %d", len(row.docs), len(got))
			}
			for i := range row.docs {
				if got[i].Path != row.docs[i].Path {
					t.Errorf("document %d path = %q; want %q", i, got[i].Path, row.docs[i].Path)
				}
				if got[i].Content != row.docs[i].Content {
					t.Errorf("document %d content round-tripped as %q; want %q", i, got[i].Content, row.docs[i].Content)
				}
			}
		})
	}
}

// TestReportingNothingReportsTheEmptySet asserts a capability with nothing to
// report still answers with a parseable document set.
//
// An empty stream would be indistinguishable from a truncated answer or a
// crashed call, so a caller could not tell "no documents" from "no answer".
func TestReportingNothingReportsTheEmptySet(t *testing.T) {
	var buf bytes.Buffer
	if err := Report(&buf, nil); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("reporting nothing produced an empty stream, which a caller cannot tell from a truncated answer")
	}
	if !strings.Contains(buf.String(), "documents") {
		t.Errorf("the empty answer does not carry the documents key:\n%s", buf.String())
	}
	docs, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("Decode the empty answer: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("decoded %d documents from the empty answer", len(docs))
	}
}

// TestReportingIsDeterministic asserts the same document set reports the same
// bytes, which is what lets a caller diff two answers.
func TestReportingIsDeterministic(t *testing.T) {
	docs := []Document{
		{Path: "/out/a.yaml", Content: "kind: A\n"},
		{Path: "/out/b.yaml", Content: "kind: B\n"},
	}
	var first, second bytes.Buffer
	if err := Report(&first, docs); err != nil {
		t.Fatalf("first Report: %v", err)
	}
	if err := Report(&second, docs); err != nil {
		t.Fatalf("second Report: %v", err)
	}
	if first.String() != second.String() {
		t.Errorf("two reports of one document set differ\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

// TestTheWritingDeliveryIsTheZeroValue asserts a caller that states no delivery
// writes.
//
// It is the mechanical form of "the delivery is added, not substituted": an
// existing caller that never heard of a reporting delivery cannot acquire one by
// having the field appear in the options it fills.
func TestTheWritingDeliveryIsTheZeroValue(t *testing.T) {
	var unstated Delivery
	if unstated.Reports() {
		t.Error("the zero-value delivery reports; an existing caller would stop writing without changing a line")
	}
	if !DeliveryFor(true).Reports() {
		t.Error("DeliveryFor(true) does not report")
	}
	if DeliveryFor(false).Reports() {
		t.Error("DeliveryFor(false) reports")
	}
}
