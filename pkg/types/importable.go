// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// unmarshalImportable implements the shared "string scalar OR {$import:
// <path-or-url>} object" content-shape dispatch used by every Aggrega
// discriminator type that admits both inline and import authoring modes:
// ValuesMapping (Recipe + FeatureRecipe), Workload + ArtifactEntry
// (Recipe), and any future site with the same shape.
//
// The function returns (inline, import) — exactly one populated on
// success; both empty when the node is nil. Callers assign to their
// discriminator type's fields. The sentinel argument lets each
// discriminator surface its own error sentinel for errors.Is matching;
// callers pass their type-specific Err…Shape sentinel.
//
// Shape contract per chapter 06 §"Why content-shape dispatch suffices"
// and chapter 07 §"$import: — whole-content replacement":
//
//   - yaml.ScalarNode → inline mode; the scalar value goes to inline.
//   - yaml.MappingNode with exactly one entry, key "$import", string-
//     scalar value → import mode; the value goes to importTarget.
//   - Any other shape → wrapped sentinel error with file:line provenance.
func unmarshalImportable(node *yaml.Node, sentinel error) (inline, importTarget string, err error) {
	if node == nil {
		return "", "", nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value, "", nil
	case yaml.MappingNode:
		if len(node.Content) != 2 {
			return "", "", fmt.Errorf("%w: got %d keys at line %d", sentinel, len(node.Content)/2, node.Line)
		}
		keyNode := node.Content[0]
		valNode := node.Content[1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Value != "$import" {
			return "", "", fmt.Errorf("%w: got key %q at line %d", sentinel, keyNode.Value, keyNode.Line)
		}
		if valNode.Kind != yaml.ScalarNode {
			return "", "", fmt.Errorf("%w: $import value must be a string scalar at line %d", sentinel, valNode.Line)
		}
		return "", valNode.Value, nil
	default:
		return "", "", fmt.Errorf("%w: got kind %d at line %d", sentinel, node.Kind, node.Line)
	}
}

// marshalImportable is the inverse of unmarshalImportable: it produces
// the yaml.Node representation for the populated mode. Empty inputs
// return (nil, nil) so the owning field's omitempty tag elides the
// emission entirely.
//
// Inline mode emits as a scalar node; multi-line content uses literal
// style ("|") so newlines + indentation survive round-trip. Import
// mode emits a map of one key.
func marshalImportable(inline, importTarget string) (any, error) {
	switch {
	case inline != "":
		node := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: inline,
		}
		for _, r := range inline {
			if r == '\n' {
				node.Style = yaml.LiteralStyle
				break
			}
		}
		return node, nil
	case importTarget != "":
		return map[string]string{"$import": importTarget}, nil
	default:
		return nil, nil
	}
}

