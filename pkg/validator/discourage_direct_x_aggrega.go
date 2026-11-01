// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// XAggregaFinding is one x-aggrega-* extension keyword authored inline in a
// Component's or Feature's schema content: the entity that authored it, the
// keyword, the RFC 6901 JSON Pointer locating the key within the schema tree, and
// the declarative table recommended in its place.
type XAggregaFinding struct {
	Entity      string
	Key         string
	Pointer     string
	Recommended string
}

// Message is the finding's sentence, naming the entity, the keyword, its
// location and the recommended authoring path. The pointer is wrapped in <...>
// so a reader — and the equivalence test's parse — can tell where a pointer
// whose reference tokens contain spaces ends.
func (f XAggregaFinding) Message() string {
	return fmt.Sprintf("entity %q authors %s at <%s> in schema content; recommended path is %s",
		f.Entity, f.Key, f.Pointer, f.Recommended)
}

// DiscourageDirectXAggregaInSchema walks a Component or Feature's
// Spec.Schema content looking for x-aggrega-* extension keywords authored
// inline, and returns one finding per occurrence: multiple occurrences of
// the same key at different paths produce multiple findings. The order
// follows the YAML authoring order via go.yaml.in/yaml/v3: mapping keys
// traverse in declared order; sequence items by index.
//
// The check is observation-only and writes nothing: it does not return an
// error, does not block load, and leaves reporting to the caller, which
// knows how often it has read the entity and where its reader looks. The
// publishing-linter Wasm peer at `aggrega-cli/plugins/publishing-linter/src/rules/
// x_aggrega_discouragement.rs` raises the same semantics to error
// severity at `aggrega lint --pre-publish` time. One logical rule,
// two implementations; equivalence pinned by the Go-side test at
// `aggrega-cli/pkg/validator/x_aggrega_equivalence_test.go`.
func DiscourageDirectXAggregaInSchema(entityName string, schema *yaml.Node) []XAggregaFinding {
	if schema == nil || schema.Kind == 0 {
		return nil
	}
	var findings []XAggregaFinding
	walkForXAggregaExtensions(schema, "", entityName, &findings)
	return findings
}

// walkForXAggregaExtensions recursively walks the YAML tree
// collecting any mapping key starting with "x-aggrega-" along with
// its RFC 6901 JSON Pointer location. The pointer is empty at
// the schema root; descends with the escaped mapping key on
// mapping entries and the index on sequence entries.
func walkForXAggregaExtensions(n *yaml.Node, pointer, entityName string, findings *[]XAggregaFinding) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, child := range n.Content {
			walkForXAggregaExtensions(child, pointer, entityName, findings)
		}
	case yaml.SequenceNode:
		for idx, child := range n.Content {
			childPointer := fmt.Sprintf("%s/%d", pointer, idx)
			walkForXAggregaExtensions(child, childPointer, entityName, findings)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			valNode := n.Content[i+1]
			if keyNode.Kind == yaml.ScalarNode {
				childPointer := pointer + "/" + escapePointerSegment(keyNode.Value)
				if strings.HasPrefix(keyNode.Value, "x-aggrega-") {
					*findings = append(*findings, XAggregaFinding{
						Entity:      entityName,
						Key:         keyNode.Value,
						Pointer:     childPointer,
						Recommended: recommendedAuthoringPath(keyNode.Value),
					})
				}
				walkForXAggregaExtensions(valNode, childPointer, entityName, findings)
			}
		}
	}
}

// escapePointerSegment applies RFC 6901 token escaping: `~` -> `~0`,
// `/` -> `~1`. JSON Schema keyword names don't carry either
// character, but authored property keys could.
func escapePointerSegment(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// recommendedAuthoringPath returns the user-facing recommendation
// string for a given x-aggrega-* extension keyword.
func recommendedAuthoringPath(extKey string) string {
	switch extKey {
	case "x-aggrega-selector":
		return "the usesSchema field's declarative socket table"
	case "x-aggrega-inject":
		return "the usesSchema or featuresSchema declarative tables"
	default:
		return "the appropriate declarative table on spec"
	}
}
