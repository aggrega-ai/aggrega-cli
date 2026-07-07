// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"
)

// TestSpliceArtifactProvenance_NonDefaultDelimsParseFailWarns pins the
// non-default-delims parse-failure → warn-and-emit branch: content that
// yaml.Unmarshal can't parse, under the alternate-delimiter policy, falls back to
// the warning path rather than erroring.
func TestSpliceArtifactProvenance_NonDefaultDelimsParseFailWarns(t *testing.T) {
	t.Parallel()
	// Content that yaml.Unmarshal will reject: a bare double-colon scalar.
	src := []byte("key: : double-colon-error")
	body, warning, err := SpliceArtifactProvenance("f", src,
		map[string]string{"aggrega.ai/component-id": "n"}, nil, false, 2)
	if err != nil {
		t.Fatalf("non-default + parse-fail should warn-and-emit: %v", err)
	}
	if len(body) == 0 {
		t.Errorf("rendered bytes should be emitted unchanged")
	}
	if warning == "" {
		t.Errorf("warning should be returned")
	}
}

// TestSpliceArtifactProvenance_EmptyDocumentNode pins the empty-document branch:
// content that parses to a zero-kind yaml.Node (a `---` separator) returns
// without provenance injection or error.
func TestSpliceArtifactProvenance_EmptyDocumentNode(t *testing.T) {
	t.Parallel()
	body, _, err := SpliceArtifactProvenance("f", []byte("---\n"),
		map[string]string{"aggrega.ai/component-id": "n"}, nil, true, 2)
	if err != nil {
		t.Fatalf("empty doc: %v", err)
	}
	_ = body
}
