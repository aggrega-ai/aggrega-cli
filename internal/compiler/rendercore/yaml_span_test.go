// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"go.yaml.in/yaml/v3"
)

// spanEndOf parses body, navigates to the value at a single-token path, and
// returns the primitive's byte span of that value — the direct exercise of the
// one node-to-span primitive.
func spanEndOf(t *testing.T, body, key string) (int, int) {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc := root.Content[0]
	offs := lineStartOffsets(body)
	bo := byteOffsetFor([]byte(body), offs)
	keyNode, valNode, parent, err := navigate(doc, []string{key})
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	flow := parent != nil && parent.Style&yaml.FlowStyle != 0
	sp := spanOfNode([]byte(body), valNode, containerIndent(keyNode, parent), flow, offs, bo)
	return sp.Start, sp.End
}

// TestSpanOfNodeMultiLineConstructs pins the primitive's span end across every
// multi-line construct the parser reports only the start line of: a block
// literal and folded scalar (with chomping), a multi-line quoted scalar, a plain
// scalar folded across lines, and a multi-line flow collection. The end must
// cover the whole construct, not truncate at the first newline.
func TestSpanOfNodeMultiLineConstructs(t *testing.T) {
	cases := []struct {
		name string
		body string
		key  string
		want string // the exact source bytes the span [start,end) must cover
	}{
		{"block-literal", "a: |\n  one\n  two\nb: 2\n", "a", "|\n  one\n  two"},
		{"block-literal-strip", "a: |-\n  one\n  two\nb: 2\n", "a", "|-\n  one\n  two"},
		{"block-folded", "a: >\n  one\n  two\nb: 2\n", "a", ">\n  one\n  two"},
		{"block-literal-comment-body", "a: |\n  one\n  # kept literal\nb: 2\n", "a", "|\n  one\n  # kept literal"},
		{"multi-line-double-quoted", "a: \"one\n  two\"\nb: 2\n", "a", "\"one\n  two\""},
		{"multi-line-single-quoted", "a: 'one\n  two'\nb: 2\n", "a", "'one\n  two'"},
		{"plain-folded", "a: one\n  two\nb: 2\n", "a", "one\n  two"},
		{"flow-map-own-line-close", "a: {\n  x: 1\n}\nb: 2\n", "a", "{\n  x: 1\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := spanEndOf(t, tc.body, tc.key)
			got := tc.body[start:end]
			if got != tc.want {
				t.Fatalf("span mismatch\n want %q\n  got %q", tc.want, got)
			}
		})
	}
}

// TestC1_ReplaceMultiLineScalarRoundTrips is the C1 proof: a replace of a value
// whose source is a multi-line block or quoted scalar edits the whole construct
// and re-parses to the intended value — the old first-newline-truncated span left
// the block body behind, producing a silently wrong re-parse.
func TestC1_ReplaceMultiLineScalarRoundTrips(t *testing.T) {
	codec := newStockYAMLCodec(2)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"block-literal", "a: |\n  ---\n  not a separator\nb: 2\n", "a: newval\nb: 2\n"},
		{"block-folded", "a: >\n  folded\n  text\nb: 2\n", "a: newval\nb: 2\n"},
		{"multi-line-double-quoted", "a: \"one\n  two\"\nb: 2\n", "a: \"newval\"\nb: 2\n"},
		{"nested-block-literal", "root:\n  a: |\n    x\n    y\n  b: 2\n", "root:\n  a: newval\n  b: 2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "/a"
			if strings.HasPrefix(tc.body, "root:") {
				path = "/root/a"
			}
			out := string(reassembleOne(t, codec, "v.yaml", []byte(tc.body), []frp.JSONPatchOp{
				{Key: "v.yaml", Op: "replace", Path: path, Value: "newval"},
			}))
			if out != tc.want {
				t.Fatalf("replace corrupted multi-line scalar\n want %q\n  got %q", tc.want, out)
			}
			// The result must re-parse and carry the intended value.
			var chk map[string]any
			if err := yaml.Unmarshal([]byte(out), &chk); err != nil {
				t.Fatalf("output does not re-parse: %v\n%s", err, out)
			}
		})
	}
}

// TestC2_AddRemoveLastMemberBlockScalar is the C2 proof: when a block mapping's
// last member value is a multi-line block scalar, an add lands after the whole
// block (not inside its continuation) and a remove deletes the whole block.
func TestC2_AddRemoveLastMemberBlockScalar(t *testing.T) {
	codec := newStockYAMLCodec(2)
	const body = "root:\n  name: web\n  config: |\n    line1\n    line2\n"

	add := string(reassembleOne(t, codec, "v.yaml", []byte(body), []frp.JSONPatchOp{
		{Key: "v.yaml", Op: "add", Path: "/root/added", Value: "x"},
	}))
	const wantAdd = "root:\n  name: web\n  config: |\n    line1\n    line2\n  added: x\n"
	if add != wantAdd {
		t.Fatalf("add landed inside the block scalar\n want %q\n  got %q", wantAdd, add)
	}

	rm := string(reassembleOne(t, codec, "v.yaml", []byte(body), []frp.JSONPatchOp{
		{Key: "v.yaml", Op: "remove", Path: "/root/config"},
	}))
	const wantRm = "root:\n  name: web\n"
	if rm != wantRm {
		t.Fatalf("remove orphaned the block scalar body\n want %q\n  got %q", wantRm, rm)
	}
}

// TestC3_ProvenanceSpliceAfterBlockScalarAnnotation is the C3 proof: when the
// last annotation's value is a multi-line block scalar, the provenance splice
// appends after the block rather than inside its continuation, and every
// pre-existing byte survives verbatim.
func TestC3_ProvenanceSpliceAfterBlockScalarAnnotation(t *testing.T) {
	const body = "apiVersion: v1\n" +
		"kind: ConfigMap\n" +
		"metadata:\n" +
		"  name: cm\n" +
		"  annotations:\n" +
		"    a.example.com/first: keep\n" +
		"    z.example.com/last-config: |\n" +
		"      {\"a\":1}\n" +
		"      {\"b\":2}\n"

	out, warn, err := SpliceArtifactProvenance("cm.yaml", []byte(body),
		nil, map[string]string{"aggrega.ai/provenance": "stamp"}, true, 2)
	if err != nil {
		t.Fatalf("splice error: %v", err)
	}
	if warn != "" {
		t.Fatalf("unexpected warning (splice fell back to round-trip): %q", warn)
	}
	got := string(out)

	// The block scalar body must survive verbatim and unbroken.
	if !strings.Contains(got, "    z.example.com/last-config: |\n      {\"a\":1}\n      {\"b\":2}\n") {
		t.Fatalf("block scalar annotation corrupted:\n%s", got)
	}
	// The new provenance annotation must land after the block, at the entry indent.
	if !strings.Contains(got, "    aggrega.ai/provenance: stamp") {
		t.Fatalf("provenance annotation not spliced:\n%s", got)
	}
	// The stamp must be after the block scalar's last line, not spliced into it.
	blockLast := strings.Index(got, "{\"b\":2}")
	stamp := strings.Index(got, "aggrega.ai/provenance: stamp")
	if stamp < blockLast {
		t.Fatalf("provenance spliced inside the block scalar continuation:\n%s", got)
	}
	// Output must re-parse.
	var chk any
	if err := yaml.Unmarshal(out, &chk); err != nil {
		t.Fatalf("spliced output does not re-parse: %v\n%s", err, got)
	}
}
