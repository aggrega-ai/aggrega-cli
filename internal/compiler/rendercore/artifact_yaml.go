// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"
	"regexp"
	"sort"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// helmTemplateTokenRe matches the maximal `{{ ... }}` spans on a single
// line — the Helm references that must survive a provenance round-trip
// verbatim. Structural / multi-line actions never reach the verification;
// they fail the YAML parse first.
var helmTemplateTokenRe = regexp.MustCompile(`\{\{.*?\}\}`)

// helmTemplateTokens returns the Helm `{{ ... }}` references in s. The
// provenance round-trip is accepted only when each is preserved verbatim
// in the re-marshalled output — yaml.v3 parses an unquoted `{{ x }}` value
// as a flow mapping and re-marshals it as `{? {x: ”} : ”}`, which this
// check detects as a dropped token.
func helmTemplateTokens(s string) []string {
	return helmTemplateTokenRe.FindAllString(s, -1)
}

// injectProvenanceNode merges the canonical provenance-label set into the
// document's metadata.labels block, mutating the supplied *yaml.Node in
// place. yaml.Node-based traversal preserves the user's authored field
// order through the round-trip — keys outside metadata stay where the
// author wrote them; keys inside metadata stay where the author wrote them;
// existing user-authored labels stay where the author wrote them.
//
// Provenance labels APPEND after any existing user-authored labels in
// alphabetical order of the provenance-label key (deterministic for
// byte-equality of output across runs of identical input). When a
// provenance label key collides with a user-authored label, the user-
// authored value is overwritten with the compiler's value at the existing
// position — provenance labels are authoritative on key collision per the
// chapter-16 contract.
//
// When the document does not carry a metadata block (compose service
// fragments, raw config-only fragments) the injector returns nil unchanged
// — silent no-op per chapter 16's "manifests without metadata skip
// silently" contract.
//
// Q1a guardrail: when metadata.labels exists but its kind is not a YAML
// mapping (scalar, sequence, alias, etc.), the function returns a
// CATALOG_ERROR rather than silently discarding the user-authored content.
// Aligns with Invariant 4 — the compiler never silently mutates user
// content into compliance.
func injectProvenanceNode(filename string, root *yaml.Node, labels, annotations map[string]string) error {
	mapping := DocumentMapping(root)
	if mapping == nil {
		return nil
	}
	metadata := MappingValueByKey(mapping, "metadata")
	if metadata == nil {
		return nil
	}
	metadata = UnwrapAlias(metadata)
	if metadata.Kind != yaml.MappingNode {
		return nil
	}

	if err := mergeOrAppendProvenanceMapping(filename, metadata, "labels", labels); err != nil {
		return err
	}
	if err := mergeOrAppendProvenanceMapping(filename, metadata, "annotations", annotations); err != nil {
		return err
	}
	return nil
}

// mergeOrAppendProvenanceMapping merges entries into the named mapping
// child of metadata, creating the child if absent. Same Q1a guardrail as
// the original labels-only path: if the child exists but its kind is not
// a YAML mapping, return CATALOG_ERROR rather than silently discarding
// user content. No-op when entries is empty.
func mergeOrAppendProvenanceMapping(filename string, metadata *yaml.Node, key string, entries map[string]string) error {
	if len(entries) == 0 {
		return nil
	}
	keyNode, valNode := FindKeyValueNode(metadata, key)
	if keyNode != nil && valNode != nil {
		valNode = UnwrapAlias(valNode)
		if valNode.Kind != yaml.MappingNode {
			return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: filename},
				fmt.Sprintf("artifact %q metadata.%s has unexpected shape (expected mapping, got %s); cannot merge provenance entries",
					filename, key, NodeKindName(valNode.Kind)),
				nil, nil)
		}
		mergeProvenanceIntoLabels(valNode, entries)
		return nil
	}
	newMapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mergeProvenanceIntoLabels(newMapping, entries)
	metadata.Content = append(metadata.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		newMapping,
	)
	return nil
}

// MappingValueByKey looks up the value node for a given string key inside
// a mapping. Returns nil when the key is absent. Iterates Content in pairs
// — Content[2i] is the key, Content[2i+1] is the value.
func MappingValueByKey(mapping *yaml.Node, key string) *yaml.Node {
	_, value := FindKeyValueNode(mapping, key)
	return value
}

// mergeProvenanceIntoLabels mutates labelsNode IN PLACE: it overwrites the
// value of any pre-existing provenance keys by mutating the existing value
// node (Kind/Tag/Value/Content reset) and appends net-new provenance keys
// to labelsNode.Content in alphabetical key order. Caller-shared *yaml.Node
// references see the mutation immediately. The append order is
// deterministic so the marshalled output is byte-identical across runs of
// identical input.
func mergeProvenanceIntoLabels(labelsNode *yaml.Node, labels map[string]string) {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := labels[k]
		if _, existing := FindKeyValueNode(labelsNode, k); existing != nil {
			existing.Kind = yaml.ScalarNode
			existing.Tag = "!!str"
			existing.Value = v
			existing.Content = nil
			continue
		}
		labelsNode.Content = append(labelsNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v},
		)
	}
}

// NodeKindName returns a human-readable name for a yaml.Node Kind, used
// in CATALOG_ERROR messages so the operator sees what shape the labels
// block actually had.
func NodeKindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return fmt.Sprintf("unknown kind %d", int(k))
	}
}
