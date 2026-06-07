// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// injectAggregaLabels post-processes rendered YAML (possibly multi-document) and
// merges aggrega.ai provenance labels into each document's metadata.labels map.
// Chart-author labels are preserved; aggrega keys overwrite on collision.
//
// Documents without a metadata map are left untouched (they aren't K8s-shaped).
// Multi-doc boundaries ("---") are preserved via yaml.Encoder per document.
func injectAggregaLabels(yamlBytes []byte, labels map[string]string, indent int) ([]byte, error) {
	if len(labels) == 0 {
		return yamlBytes, nil
	}

	// Sort label keys once for deterministic merge output.
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	dec := yaml.NewDecoder(bytes.NewReader(yamlBytes))
	var docs []*yaml.Node
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse rendered YAML: %w", err)
		}
		docs = append(docs, &doc)
	}

	for _, doc := range docs {
		if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
			continue
		}
		root := doc.Content[0]
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		mergeLabelsIntoMetadata(root, keys, labels)
	}

	var buf bytes.Buffer
	for i, doc := range docs {
		if i > 0 {
			buf.WriteString("---\n")
		}
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(renderer.ClampYAMLIndent(indent))
		if err := enc.Encode(doc); err != nil {
			return nil, fmt.Errorf("encode rendered YAML: %w", err)
		}
		_ = enc.Close()
	}

	return buf.Bytes(), nil
}

// mergeLabelsIntoMetadata walks a root mapping node for a K8s manifest, finds or
// creates `metadata.labels`, and merges the aggrega labels into it (aggrega wins
// on key collision, chart-author labels preserved otherwise).
func mergeLabelsIntoMetadata(root *yaml.Node, keys []string, labels map[string]string) {
	metadata := findMapChild(root, "metadata")
	if metadata == nil {
		metadata = appendMapChild(root, "metadata")
	}
	labelsNode := findMapChild(metadata, "labels")
	if labelsNode == nil {
		labelsNode = appendMapChild(metadata, "labels")
	}
	// Build a lookup of existing keys for overwrite semantics.
	existing := map[string]int{}
	for i := 0; i < len(labelsNode.Content); i += 2 {
		if i+1 >= len(labelsNode.Content) {
			break
		}
		existing[labelsNode.Content[i].Value] = i + 1
	}
	for _, k := range keys {
		v := labels[k]
		if idx, ok := existing[k]; ok {
			labelsNode.Content[idx].Kind = yaml.ScalarNode
			labelsNode.Content[idx].Tag = "!!str"
			labelsNode.Content[idx].Value = v
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		// Quote-preserve arbitrary label values (safe default for CI-friendly output).
		valNode.Style = chooseScalarStyle(v)
		labelsNode.Content = append(labelsNode.Content, keyNode, valNode)
	}
}

// findMapChild returns the value node mapped from `key` in a MappingNode, or nil.
func findMapChild(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(m.Content); i += 2 {
		if i+1 >= len(m.Content) {
			break
		}
		k := m.Content[i]
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// appendMapChild adds an empty MappingNode under `key` in a MappingNode and
// returns the new child node.
func appendMapChild(m *yaml.Node, key string) *yaml.Node {
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, keyNode, child)
	return child
}

// chooseScalarStyle returns a conservative style for label values. Plain style
// is used for simple identifiers; any value containing special YAML characters
// falls back to double-quoted for safety.
func chooseScalarStyle(v string) yaml.Style {
	if strings.ContainsAny(v, ":#{}[],&*!|>'\"%@`\n") {
		return yaml.DoubleQuotedStyle
	}
	return 0
}

// injectAggregaAnnotations post-processes rendered YAML (possibly multi-
// document) and merges aggrega-managed annotations into each document's
// metadata.annotations map. Chart-author annotations are preserved;
// aggrega keys overwrite on collision.
//
// Documents without a metadata map are left untouched (they aren't K8s-
// shaped). Multi-doc boundaries ("---") are preserved via yaml.Encoder
// per document.
//
// hookWeight, when non-empty, is emitted as helm.sh/hook-weight ONLY on
// resources that already carry a helm.sh/hook annotation (chart-author
// hook markers). Non-hook resources receive only the always-emit
// annotations set.
func injectAggregaAnnotations(yamlBytes []byte, annotations map[string]string, hookWeight string, indent int) ([]byte, error) {
	if len(annotations) == 0 && hookWeight == "" {
		return yamlBytes, nil
	}

	keys := make([]string, 0, len(annotations))
	for k := range annotations {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	dec := yaml.NewDecoder(bytes.NewReader(yamlBytes))
	var docs []*yaml.Node
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse rendered YAML: %w", err)
		}
		docs = append(docs, &doc)
	}

	for _, doc := range docs {
		if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
			continue
		}
		root := doc.Content[0]
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		mergeAnnotationsIntoMetadata(root, keys, annotations, hookWeight)
	}

	var buf bytes.Buffer
	for i, doc := range docs {
		if i > 0 {
			buf.WriteString("---\n")
		}
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(renderer.ClampYAMLIndent(indent))
		if err := enc.Encode(doc); err != nil {
			return nil, fmt.Errorf("encode rendered YAML: %w", err)
		}
		_ = enc.Close()
	}

	return buf.Bytes(), nil
}

// mergeAnnotationsIntoMetadata walks a root mapping node for a K8s
// manifest, finds or creates `metadata.annotations`, and merges the
// aggrega annotations into it (aggrega wins on key collision; chart-
// author entries preserved otherwise).
//
// hookWeight, when non-empty, is injected as helm.sh/hook-weight ONLY
// when the manifest already carries a helm.sh/hook annotation.
func mergeAnnotationsIntoMetadata(root *yaml.Node, keys []string, annotations map[string]string, hookWeight string) {
	metadata := findMapChild(root, "metadata")
	if metadata == nil {
		metadata = appendMapChild(root, "metadata")
	}
	annotationsNode := findMapChild(metadata, "annotations")
	if annotationsNode == nil {
		annotationsNode = appendMapChild(metadata, "annotations")
	}

	// Detect helm.sh/hook before mutating annotations: scan existing
	// keys for the marker. The chart author sets this via templates;
	// Aggrega never adds it.
	isHook := false
	for i := 0; i < len(annotationsNode.Content); i += 2 {
		if i+1 >= len(annotationsNode.Content) {
			break
		}
		if annotationsNode.Content[i].Value == "helm.sh/hook" {
			isHook = true
			break
		}
	}

	existing := map[string]int{}
	for i := 0; i < len(annotationsNode.Content); i += 2 {
		if i+1 >= len(annotationsNode.Content) {
			break
		}
		existing[annotationsNode.Content[i].Value] = i + 1
	}

	for _, k := range keys {
		v := annotations[k]
		if idx, ok := existing[k]; ok {
			annotationsNode.Content[idx].Kind = yaml.ScalarNode
			annotationsNode.Content[idx].Tag = "!!str"
			annotationsNode.Content[idx].Value = v
			annotationsNode.Content[idx].Style = chooseScalarStyle(v)
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		valNode.Style = chooseScalarStyle(v)
		annotationsNode.Content = append(annotationsNode.Content, keyNode, valNode)
	}

	if isHook && hookWeight != "" {
		const hookWeightKey = "helm.sh/hook-weight"
		if idx, ok := existing[hookWeightKey]; ok {
			annotationsNode.Content[idx].Kind = yaml.ScalarNode
			annotationsNode.Content[idx].Tag = "!!str"
			annotationsNode.Content[idx].Value = hookWeight
			annotationsNode.Content[idx].Style = chooseScalarStyle(hookWeight)
		} else {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: hookWeightKey}
			valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: hookWeight}
			valNode.Style = chooseScalarStyle(hookWeight)
			annotationsNode.Content = append(annotationsNode.Content, keyNode, valNode)
		}
	}
}
