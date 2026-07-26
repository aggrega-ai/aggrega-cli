// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"log"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DiscourageDirectXAggregaInSchema walks a Component or Feature's
// Spec.Schema content looking for x-aggrega-selector or
// x-aggrega-inject extension keywords authored inline. For each
// occurrence, the function emits a warn-level log line naming the
// entity, the offending key, the RFC 6901 JSON Pointer locating
// the key within the schema tree, and the recommended authoring
// path (usesSchema for dependency sockets; featuresSchema for
// credential bindings).
//
// One log line per occurrence; multiple occurrences of the same
// key at different paths produce multiple log lines. Logging
// order follows the YAML authoring order via go.yaml.in/yaml/v3:
// mapping keys traverse in declared order; sequence items by
// index.
//
// The check is observation-only at build time: it does not return
// an error and does not block load. The publishing-linter Wasm
// peer at `aggrega-cli/plugins/publishing-linter/src/rules/
// x_aggrega_discouragement.rs` raises the same semantics to error
// severity at `aggrega lint --pre-publish` time. One logical rule,
// two implementations; equivalence pinned by the Go-side test at
// `aggrega-cli/pkg/validator/x_aggrega_equivalence_test.go`.
func DiscourageDirectXAggregaInSchema(entityName string, schema *yaml.Node) {
	if schema == nil || schema.Kind == 0 {
		return
	}
	walkForXAggregaExtensions(schema, "", entityName)
}

// walkForXAggregaExtensions recursively walks the YAML tree
// logging any mapping key starting with "x-aggrega-" along with
// its RFC 6901 JSON Pointer location. The pointer is empty at
// the schema root; descends with the escaped mapping key on
// mapping entries and the index on sequence entries.
func walkForXAggregaExtensions(n *yaml.Node, pointer, entityName string) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, child := range n.Content {
			walkForXAggregaExtensions(child, pointer, entityName)
		}
	case yaml.SequenceNode:
		for idx, child := range n.Content {
			childPointer := fmt.Sprintf("%s/%d", pointer, idx)
			walkForXAggregaExtensions(child, childPointer, entityName)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			valNode := n.Content[i+1]
			if keyNode.Kind == yaml.ScalarNode {
				childPointer := pointer + "/" + escapePointerSegment(keyNode.Value)
				if strings.HasPrefix(keyNode.Value, "x-aggrega-") {
					// Pointer wrapped in <...> so the equivalence-test
					// parse regex can capture YAML keys containing
					// spaces (RFC 6901 reference tokens permit spaces).
					log.Printf("warning: entity %q authors %s at <%s> in schema content; recommended path is %s",
						entityName, keyNode.Value, childPointer, recommendedAuthoringPath(keyNode.Value))
				}
				walkForXAggregaExtensions(valNode, childPointer, entityName)
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
