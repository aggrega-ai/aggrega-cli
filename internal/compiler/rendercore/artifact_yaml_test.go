// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestMergeProvenanceIntoLabels_EveryArm(t *testing.T) {
	t.Parallel()
	labelsNode := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "existing"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "old-value"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tier"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "backend"},
	}}
	mergeProvenanceIntoLabels(labelsNode, map[string]string{
		"existing": "new-value", // replace branch
		"fresh":    "fresh-val", // append branch
	})
	// existing should now hold "new-value".
	var saw map[string]string
	saw = make(map[string]string)
	for i := 0; i < len(labelsNode.Content); i += 2 {
		saw[labelsNode.Content[i].Value] = labelsNode.Content[i+1].Value
	}
	if saw["existing"] != "new-value" {
		t.Errorf("existing should be replaced; got %q", saw["existing"])
	}
	if saw["fresh"] != "fresh-val" {
		t.Errorf("fresh should be appended; got %q", saw["fresh"])
	}
	if saw["tier"] != "backend" {
		t.Errorf("other keys should be preserved; got %q", saw["tier"])
	}
}

// TestDocumentMapping_AllShapes pins nil-root, document-wrapper,
// bare-mapping, and unsupported-kind paths.
func TestDocumentMapping_AllShapes(t *testing.T) {
	t.Parallel()
	if DocumentMapping(nil) != nil {
		t.Errorf("nil → nil")
	}
	// Document-wrapped mapping.
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
		{Kind: yaml.MappingNode, Content: nil},
	}}
	if got := DocumentMapping(doc); got == nil || got.Kind != yaml.MappingNode {
		t.Errorf("doc-wrapper → mapping; got %v", got)
	}
	// Bare mapping.
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	if got := DocumentMapping(mapping); got != mapping {
		t.Errorf("bare mapping → identity; got %v", got)
	}
	// Empty document.
	empty := &yaml.Node{Kind: yaml.DocumentNode}
	if got := DocumentMapping(empty); got != nil {
		t.Errorf("empty doc → nil; got %v", got)
	}
	// Document containing non-mapping → nil.
	docScalar := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "scalar"},
	}}
	if got := DocumentMapping(docScalar); got != nil {
		t.Errorf("doc with scalar content → nil; got %v", got)
	}
}

// TestFindKeyValueNode_EveryBranch pins missing-key, wrong-kind, and
// hit branches of the mapping-content scan.
func TestFindKeyValueNode_EveryBranch(t *testing.T) {
	t.Parallel()
	// Nil mapping → (nil, nil).
	k, v := FindKeyValueNode(nil, "x")
	if k != nil || v != nil {
		t.Errorf("nil mapping: (%v, %v)", k, v)
	}
	// Non-mapping kind.
	scalar := &yaml.Node{Kind: yaml.ScalarNode}
	k, v = FindKeyValueNode(scalar, "x")
	if k != nil || v != nil {
		t.Errorf("scalar kind: (%v, %v)", k, v)
	}
	// Hit.
	mapping := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "a"},
		{Kind: yaml.ScalarNode, Value: "1"},
		{Kind: yaml.ScalarNode, Value: "b"},
		{Kind: yaml.ScalarNode, Value: "2"},
	}}
	k, v = FindKeyValueNode(mapping, "b")
	if k == nil || v == nil || v.Value != "2" {
		t.Errorf("hit b: (%v, %v)", k, v)
	}
	// Miss.
	k, v = FindKeyValueNode(mapping, "absent")
	if k != nil || v != nil {
		t.Errorf("miss: (%v, %v)", k, v)
	}
}

// TestInjectProvenanceNode_AllShapes pins the labels-injection helper
// against the documented shape contract.
func TestInjectProvenanceNode_AllShapes(t *testing.T) {
	t.Parallel()
	// Nil root → no-op.
	if err := injectProvenanceNode("f", nil, nil, nil); err != nil {
		t.Errorf("nil root: %v", err)
	}

	// Empty document → no-op.
	empty := &yaml.Node{Kind: yaml.DocumentNode}
	if err := injectProvenanceNode("f", empty, map[string]string{"k": "v"}, nil); err != nil {
		t.Errorf("empty: %v", err)
	}

	// Document without metadata → no-op.
	noMeta := []byte(`apiVersion: v1
kind: Pod
spec: {}
`)
	var doc yaml.Node
	if err := yaml.Unmarshal(noMeta, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := injectProvenanceNode("f", &doc, map[string]string{"k": "v"}, nil); err != nil {
		t.Errorf("no metadata: %v", err)
	}

	// Document with metadata as scalar (wrong kind) → no-op.
	metaScalar := []byte(`apiVersion: v1
kind: Pod
metadata: bad-shape
`)
	if err := yaml.Unmarshal(metaScalar, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := injectProvenanceNode("f", &doc, map[string]string{"k": "v"}, nil); err != nil {
		t.Errorf("metadata scalar: %v", err)
	}

	// Happy path: full doc with metadata; labels + annotations both
	// land via mergeOrAppendProvenanceMapping.
	full := []byte(`apiVersion: v1
kind: Pod
metadata:
  name: pod
spec: {}
`)
	if err := yaml.Unmarshal(full, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := injectProvenanceNode("f", &doc, map[string]string{"label-key": "lv"}, map[string]string{"ann-key": "av"}); err != nil {
		t.Errorf("happy: %v", err)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "label-key: lv") {
		t.Errorf("label not injected; got:\n%s", out)
	}
	if !strings.Contains(string(out), "ann-key: av") {
		t.Errorf("annotation not injected; got:\n%s", out)
	}
}

// TestMergeProvenanceIntoMappingChild_EveryBranch pins the empty-
// entries short-circuit, existing-mapping merge, wrong-kind error,
// and new-mapping append. The parent path is a caller-supplied label
// for the message, so a placer locating a non-metadata parent names
// its own site rather than reporting a metadata path it never read.
func TestMergeProvenanceIntoMappingChild_EveryBranch(t *testing.T) {
	t.Parallel()
	metadata := &yaml.Node{Kind: yaml.MappingNode}

	// Empty entries → no-op (no panic).
	if err := MergeProvenanceIntoMappingChild("f", "metadata", metadata, "labels", nil); err != nil {
		t.Errorf("empty entries: %v", err)
	}
	if len(metadata.Content) != 0 {
		t.Errorf("empty entries should not append; got Content=%v", metadata.Content)
	}

	// Append new mapping branch.
	if err := MergeProvenanceIntoMappingChild("f", "metadata", metadata, "labels", map[string]string{
		"k": "v",
	}); err != nil {
		t.Errorf("append: %v", err)
	}
	if len(metadata.Content) != 2 {
		t.Errorf("after append: Content len=%d; want 2", len(metadata.Content))
	}

	// Existing mapping (different key) → merge into the appended one.
	if err := MergeProvenanceIntoMappingChild("f", "metadata", metadata, "labels", map[string]string{
		"k2": "v2",
	}); err != nil {
		t.Errorf("merge into existing: %v", err)
	}

	// Wrong-kind error branch: write the child as a scalar.
	bad := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "labels"},
		{Kind: yaml.ScalarNode, Value: "scalar-instead-of-mapping"},
	}}
	err := MergeProvenanceIntoMappingChild("f", "services.db", bad, "labels", map[string]string{"k": "v"})
	if err == nil {
		t.Fatalf("wrong-kind should error")
	}
	if !strings.Contains(err.Error(), "services.db.labels") {
		t.Errorf("the refusal should name the caller's parent path, got %q", err)
	}
}

// TestPhaseB1_NilState pins the nil-state defensive branch of phaseB1
// — exercised via direct call with a minimal TemplateContext.
